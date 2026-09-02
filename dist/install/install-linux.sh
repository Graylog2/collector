#!/bin/sh
#
# Install Graylog Collector on Linux.
#
# The script downloads the Collector package (deb or rpm) for this machine
# from the GitHub releases, installs it, writes the enrollment configuration,
# and starts the systemd service.
#
# Usage:
#   sudo sh install-linux.sh --endpoint <URL> --token <TOKEN> [--version <VERSION>]
#   sudo sh install-linux.sh --endpoint <URL> --token-file <PATH> [--version <VERSION>]
#
# Options:
#   -e, --endpoint <URL>    Enrollment endpoint, usually the Graylog server URL
#   -t, --token <TOKEN>     Enrollment token, provided by the Graylog server
#   --token-file <PATH>     Read the enrollment token from a file instead. This keeps
#                           the token out of the shell history and process list.
#   -v, --version <VERSION> Collector version to install (default: latest release)
#   -h, --help              Show this help
#
# Requirements: root privileges, curl or wget, dpkg or rpm.
#
set -eu

GITHUB_REPO="Graylog2/collector"
MANIFEST_NAME="SHA256SUMS"
CONFIG_DIR="/etc/graylog/collector"
ENROLLMENT_FILE="$CONFIG_DIR/enrollment.env"
SERVICE_NAME="graylog-collector.service"
KEYS_DIR="/var/lib/graylog-collector/keys"

ENDPOINT=""
TOKEN=""
TOKEN_FILE=""
VERSION=""

usage() {
	cat <<EOF
Usage: sudo sh install-linux.sh --endpoint <URL> --token <TOKEN> [--version <VERSION>]
       sudo sh install-linux.sh --endpoint <URL> --token-file <PATH> [--version <VERSION>]

Options:
  -e, --endpoint <URL>    Enrollment endpoint, usually the Graylog server URL
  -t, --token <TOKEN>     Enrollment token, provided by the Graylog server
  --token-file <PATH>     Read the enrollment token from a file instead. This keeps
                          the token out of the shell history and process list.
  -v, --version <VERSION> Collector version to install (default: latest release)
  -h, --help              Show this help
EOF
}

log() {
	echo "==> $*"
}

fail() {
	echo "ERROR: $*" >&2
	exit 1
}

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
			-e|--endpoint)
				[ $# -ge 2 ] || fail "$1 requires a value"
				ENDPOINT="$2"
				shift 2
				;;
			-t|--token)
				[ $# -ge 2 ] || fail "$1 requires a value"
				TOKEN="$2"
				shift 2
				;;
			--token-file)
				[ $# -ge 2 ] || fail "$1 requires a value"
				TOKEN_FILE="$2"
				shift 2
				;;
			-v|--version)
				[ $# -ge 2 ] || fail "$1 requires a value"
				VERSION="${2#v}"
				shift 2
				;;
			-h|--help)
				usage
				exit 0
				;;
			*)
				usage >&2
				fail "unknown argument: $1"
				;;
		esac
	done

	[ -n "$ENDPOINT" ] || fail "--endpoint is required"
	if [ -n "$TOKEN_FILE" ]; then
		[ -z "$TOKEN" ] || fail "use either --token or --token-file, not both"
		[ -r "$TOKEN_FILE" ] || fail "cannot read token file: $TOKEN_FILE"
		# Command substitution strips the trailing newline.
		TOKEN="$(cat "$TOKEN_FILE")"
	fi
	[ -n "$TOKEN" ] || fail "--token or --token-file is required"
}

check_root() {
	[ "$(id -u)" -eq 0 ] || fail "this script must run as root (use sudo)"
}

# The Collector is enrolled when a signing key and certificate exist. It then
# keeps using them and ignores the enrollment token, so tell the user.
warn_existing_enrollment() {
	[ -f "$KEYS_DIR/signing.key" ] && [ -f "$KEYS_DIR/signing.crt" ] || return 0
	cat >&2 <<EOF
WARNING: The Collector is already enrolled. Credentials exist in "$KEYS_DIR".
         The Collector keeps using them and ignores the enrollment token.
         To enroll again, first remove the Collector in Graylog. The server
         rejects an enrollment with new credentials while it still knows the
         Collector. Then stop the service, remove the credentials, and run
         this script again:

             sudo systemctl stop $SERVICE_NAME
             sudo rm -rf "$KEYS_DIR"

EOF
}

# Select the download tool. download_file writes to the given file path;
# fetch_stdout writes the response body to stdout.
detect_downloader() {
	if command -v curl >/dev/null 2>&1; then
		DOWNLOADER="curl"
	elif command -v wget >/dev/null 2>&1; then
		DOWNLOADER="wget"
	else
		fail "neither curl nor wget is installed"
	fi
}

fetch_stdout() {
	if [ "$DOWNLOADER" = "curl" ]; then
		curl -fsSL --retry 3 "$1"
	else
		wget -q -O - "$1"
	fi
}

download_file() {
	if [ "$DOWNLOADER" = "curl" ]; then
		curl -fSL --retry 3 --progress-bar -o "$2" "$1"
	else
		wget -q --show-progress -O "$2" "$1"
	fi
}

# Detect the package type from /etc/os-release first, then fall back to the
# available package tools.
detect_package_type() {
	os_id=""
	os_id_like=""
	if [ -r /etc/os-release ]; then
		# shellcheck disable=SC1091
		os_id="$(. /etc/os-release && echo "${ID:-}")"
		# shellcheck disable=SC1091
		os_id_like="$(. /etc/os-release && echo "${ID_LIKE:-}")"
	fi

	case " $os_id $os_id_like " in
		*" debian "*|*" ubuntu "*)
			PACKAGE_TYPE="deb"
			;;
		*" rhel "*|*" fedora "*|*" centos "*|*" suse "*|*" sles "*|*" opensuse "*)
			PACKAGE_TYPE="rpm"
			;;
		*)
			if command -v dpkg >/dev/null 2>&1; then
				PACKAGE_TYPE="deb"
			elif command -v rpm >/dev/null 2>&1; then
				PACKAGE_TYPE="rpm"
			else
				fail "unsupported distribution: neither dpkg nor rpm found"
			fi
			;;
	esac
}

# Map the machine architecture to the architecture name used in the
# package file name. Debian and RPM use different names.
detect_arch() {
	machine="$(uname -m)"
	case "$PACKAGE_TYPE:$machine" in
		deb:x86_64|deb:amd64) ARCH="amd64" ;;
		deb:aarch64|deb:arm64) ARCH="arm64" ;;
		rpm:x86_64|rpm:amd64) ARCH="x86_64" ;;
		rpm:aarch64|rpm:arm64) ARCH="aarch64" ;;
		*) fail "unsupported architecture: $machine" ;;
	esac
}

# Download the release manifest and set RELEASE_TAG, ASSET_URL, ASSET_NAME,
# and ASSET_DIGEST for the package that matches this machine. The manifest
# lists one "<sha256>  <file name>" line per release artifact.
resolve_release() {
	if [ -n "$VERSION" ]; then
		manifest_url="https://github.com/$GITHUB_REPO/releases/download/$VERSION/$MANIFEST_NAME"
	else
		manifest_url="https://github.com/$GITHUB_REPO/releases/latest/download/$MANIFEST_NAME"
	fi

	log "Fetching release manifest for ${VERSION:-latest}"
	manifest="$(fetch_stdout "$manifest_url")" || fail "release ${VERSION:-latest} not found, or it has no $MANIFEST_NAME"

	case "$PACKAGE_TYPE" in
		deb) name_pattern="graylog-collector_[^[:space:]]+_${ARCH}\\.deb" ;;
		rpm) name_pattern="graylog-collector-[^[:space:]]+\\.${ARCH}\\.rpm" ;;
	esac

	entry="$(printf '%s\n' "$manifest" | grep -E "^[0-9a-fA-F]{64}[[:space:]]+${name_pattern}\$" | head -n 1)"
	[ -n "$entry" ] || fail "release manifest has no $PACKAGE_TYPE package for $ARCH"

	ASSET_DIGEST="${entry%% *}"
	ASSET_NAME="${entry##* }"

	# The version sits between the package name and the package revision.
	case "$PACKAGE_TYPE" in
		deb) RELEASE_TAG="$(printf '%s' "$ASSET_NAME" | sed 's/^graylog-collector_\(.*\)-[0-9]*_.*\.deb$/\1/')" ;;
		rpm) RELEASE_TAG="$(printf '%s' "$ASSET_NAME" | sed 's/^graylog-collector-\(.*\)-[0-9]*\..*\.rpm$/\1/')" ;;
	esac
	ASSET_URL="https://github.com/$GITHUB_REPO/releases/download/$RELEASE_TAG/$ASSET_NAME"
}

verify_checksum() {
	log "Verifying sha256 checksum"
	if command -v sha256sum >/dev/null 2>&1; then
		actual="$(sha256sum "$1" | cut -d ' ' -f 1)"
	else
		actual="$(shasum -a 256 "$1" | cut -d ' ' -f 1)"
	fi
	[ "$actual" = "$ASSET_DIGEST" ] || fail "checksum mismatch for $ASSET_NAME (expected $ASSET_DIGEST, got $actual)"
}

# The packages declare no dependencies (see dist/linux/nfpm.yaml), so the
# low-level tools are enough. Switch to apt-get, dnf, yum, or zypper when
# the packages gain dependencies.
install_package() {
	log "Installing $ASSET_NAME"
	case "$PACKAGE_TYPE" in
		deb) dpkg -i "$1" ;;
		rpm) rpm -U --replacepkgs "$1" ;;
	esac
}

write_enrollment_config() {
	log "Writing enrollment configuration to $ENROLLMENT_FILE"
	mkdir -p "$CONFIG_DIR"
	umask 077
	cat > "$ENROLLMENT_FILE" <<EOF
GLC_SERVER__AUTH__ENROLLMENT_ENDPOINT=$ENDPOINT
GLC_SERVER__AUTH__ENROLLMENT_TOKEN=$TOKEN
EOF
	chmod 0600 "$ENROLLMENT_FILE"
}

start_service() {
	if ! command -v systemctl >/dev/null 2>&1; then
		echo "WARNING: systemctl not found, start the Collector manually" >&2
		return 0
	fi

	log "Starting $SERVICE_NAME"
	systemctl daemon-reload
	systemctl enable "$SERVICE_NAME"
	systemctl restart "$SERVICE_NAME"
}

main() {
	parse_args "$@"
	check_root
	warn_existing_enrollment
	detect_downloader
	detect_package_type
	detect_arch
	resolve_release

	tmp_dir="$(mktemp -d)"
	trap 'rm -rf "$tmp_dir"' EXIT

	package_file="$tmp_dir/$ASSET_NAME"
	log "Downloading $ASSET_URL"
	download_file "$ASSET_URL" "$package_file"
	verify_checksum "$package_file"
	install_package "$package_file"
	write_enrollment_config
	start_service

	log "Graylog Collector $RELEASE_TAG installed"
	echo "Check the service status with: systemctl status $SERVICE_NAME"
}

main "$@"
