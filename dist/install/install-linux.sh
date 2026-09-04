#!/bin/sh
#
# Install Graylog Collector on Linux.
#
# The script downloads the Collector package (deb or rpm) for this machine
# from the GitHub releases, installs it, writes the enrollment configuration,
# and starts the systemd service.
#
# Usage:
#   sudo sh install-linux.sh --endpoint <URL> --token <TOKEN> [OPTIONS]
#   sudo sh install-linux.sh --endpoint <URL> --token-file <PATH> [OPTIONS]
#
# Options:
#   -e, --endpoint <URL>    Enrollment endpoint, usually the Graylog server URL
#   -t, --token <TOKEN>     Enrollment token, provided by the Graylog server
#   --token-file <PATH>     Read the enrollment token from a file instead. This keeps
#                           the token out of the shell history and process list.
#   -v, --version <VERSION> Collector version to install (default: latest release)
#   --skip-tls-verify       Do not verify the TLS certificate of the Graylog server.
#                           Use this when the server has a self-signed certificate.
#   -h, --help              Show this help
#
# Requirements: root privileges, curl or wget, dpkg or rpm.
#
set -eu

GITHUB_REPO="Graylog2/collector"
MANIFEST_NAME="SHA256SUMS"
AUTH_CHECK_PATH="/v1/opamp-enroll-auth-check"
CONFIG_DIR="/etc/graylog/collector"
ENROLLMENT_FILE="$CONFIG_DIR/enrollment.env"
SERVICE_NAME="graylog-collector.service"
KEYS_DIR="/var/lib/graylog-collector/keys"

ENDPOINT=""
TOKEN=""
TOKEN_FILE=""
VERSION=""
SKIP_TLS_VERIFY=""

usage() {
	cat <<EOF
Usage: sudo sh install-linux.sh --endpoint <URL> --token <TOKEN> [OPTIONS]
       sudo sh install-linux.sh --endpoint <URL> --token-file <PATH> [OPTIONS]

Options:
  -e, --endpoint <URL>    Enrollment endpoint, usually the Graylog server URL
  -t, --token <TOKEN>     Enrollment token, provided by the Graylog server
  --token-file <PATH>     Read the enrollment token from a file instead. This keeps
                          the token out of the shell history and process list.
  -v, --version <VERSION> Collector version to install (default: latest release)
  --skip-tls-verify       Do not verify the TLS certificate of the Graylog server.
                          Use this when the server has a self-signed certificate.
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
			--skip-tls-verify)
				SKIP_TLS_VERIFY="1"
				shift
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
		# Take the first line and drop whitespace, including a Windows CR.
		TOKEN="$(head -n 1 "$TOKEN_FILE" | tr -d '\r[:space:]')"
	fi
	[ -n "$TOKEN" ] || fail "--token or --token-file is required"
}

check_root() {
	[ "$(id -u)" -eq 0 ] || fail "this script must run as root (use sudo)"
}

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

# Send one GET request with the enrollment token to the given URL. Prints the
# HTTP status code and the content type, separated by a space, and returns 0
# when the server answered. Otherwise prints the error text and returns 1. Pass "insecure" as the second argument to skip
# certificate verification. The token travels in a config file, so it stays
# out of the process list.
probe_url() {
	if [ "$DOWNLOADER" = "curl" ]; then
		insecure_flag=""
		[ "${2:-}" != "insecure" ] || insecure_flag="-k"
		# -w appends the status code and content type as the last line, "000"
		# without a response. curl does not follow redirects without -L. See
		# the wget note below.
		output="$(curl -sS --connect-timeout 10 --max-time 20 -o /dev/null -w '\n%{http_code} %{content_type}' \
			-K "$HEADER_FILE" ${insecure_flag:+"$insecure_flag"} "$1" 2>&1)" || true
		result="$(printf '%s\n' "$output" | tail -n 1)"
		if [ -n "$result" ] && [ "${result%% *}" != "000" ]; then
			printf '%s' "$result"
			return 0
		fi
		printf '%s\n' "$output" | sed '$d'
	else
		insecure_flag=""
		[ "${2:-}" != "insecure" ] || insecure_flag="--no-check-certificate"
		# -S prints the response headers. The exit code varies with the status
		# code, so the status is taken from the last HTTP header line. Redirects
		# are not followed: a wrong sub path often redirects to the web
		# interface, which would answer 200.
		output="$(wget -nv -S -t 1 -T 20 -O /dev/null --max-redirect=0 --config="$HEADER_FILE" \
			${insecure_flag:+"$insecure_flag"} "$1" 2>&1)" || true
		status="$(printf '%s\n' "$output" | sed -n 's/^  HTTP\/[^ ]* \([0-9][0-9][0-9]\).*/\1/p' | tail -n 1)"
		if [ -n "$status" ]; then
			content_type="$(printf '%s\n' "$output" | sed -n 's/^  [Cc]ontent-[Tt]ype: *//p' | tail -n 1)"
			printf '%s %s' "$status" "$content_type"
			return 0
		fi
		printf '%s\n' "$output"
	fi
	return 1
}

# The token goes into a config file instead of a --header argument. Arguments
# show up in the process list for every local user, and --token-file exists to
# keep the token out of there.
write_header_file() {
	HEADER_FILE="$tmp_dir/headers"
	(
		umask 077
		if [ "$DOWNLOADER" = "curl" ]; then
			printf 'header = "Authorization: Bearer %s"\n' "$TOKEN" > "$HEADER_FILE"
		else
			printf 'header = Authorization: Bearer %s\n' "$TOKEN" > "$HEADER_FILE"
		fi
	)
}

# The server base URL is the endpoint without a trailing slash and without the
# default OpAMP path, like the supervisor derives it.
auth_check_url() {
	base="${1%/}"
	base="${base%/v1/opamp}"
	printf '%s%s' "$base" "$AUTH_CHECK_PATH"
}

# Ask the server to validate the enrollment token before anything is installed.
# The supervisor does the same at startup, so a bad token fails here instead of
# in a crash loop. The check also explains the two common URL mistakes: The
# supervisor rejects a server certificate that the system does not trust, such
# as a self-signed one. When a request fails with verification but succeeds
# without it, the certificate is the problem, and the user needs
# --skip-tls-verify. When an http:// URL fails but the same host answers on
# https://, the URL has the wrong scheme.
check_endpoint() {
	case "$ENDPOINT" in
		https://*|http://*) ;;
		*)
			# Not an HTTP URL. Leave it to the supervisor to reject it.
			return 0
			;;
	esac

	log "Checking connection to $ENDPOINT"
	check_url="$(auth_check_url "$ENDPOINT")"
	write_header_file

	case "$ENDPOINT" in
		https://*)
			if [ -n "$SKIP_TLS_VERIFY" ]; then
				echo "WARNING: TLS certificate verification is disabled for $ENDPOINT" >&2
				status="$(probe_url "$check_url" insecure)" || fail "cannot connect to $ENDPOINT: $status"
			elif ! status="$(probe_url "$check_url")"; then
				probe_error="$status"
				if probe_url "$check_url" insecure >/dev/null; then
					fail "the TLS certificate of $ENDPOINT is not trusted by this system.
       If the Graylog server uses a self-signed certificate, run this script
       again with --skip-tls-verify. The Collector then skips certificate
       verification for this server."
				fi
				fail "cannot connect to $ENDPOINT: $probe_error"
			fi
			;;
		http://*)
			if ! status="$(probe_url "$check_url")"; then
				probe_error="$status"
				https_endpoint="https://${ENDPOINT#http://}"
				if https_error="$(probe_url "https://${check_url#http://}" insecure)"; then
					fail "cannot connect to $ENDPOINT, but the server answers on
       $https_endpoint. Run this script again with that URL."
				fi
				fail "cannot connect to $ENDPOINT: $probe_error
       Also tried $https_endpoint: $https_error"
			fi
			;;
	esac

	content_type="${status#* }"
	status="${status%% *}"
	case "$status" in
		200)
			# The real endpoint answers with an empty body. Graylog serves its
			# web interface for unknown paths, with the same status code.
			case "$content_type" in
				text/html*)
					fail "$ENDPOINT points to the Graylog web interface, not to its API.
       Check the path of the URL. Usually the Graylog server URL has no path."
					;;
			esac
			;;
		401|403)
			fail "the Graylog server at $ENDPOINT rejected the enrollment token (HTTP $status).
       Check the token, or create a new one in Graylog."
			;;
		*)
			fail "unexpected response HTTP $status from $check_url.
       Is $ENDPOINT the Graylog server URL?"
			;;
	esac
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

# Download the release manifest and set ASSET_URL, ASSET_NAME, and
# ASSET_DIGEST for the package that matches this machine. The manifest
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

	# The asset sits next to the manifest. That also works for "latest".
	ASSET_URL="${manifest_url%/*}/$ASSET_NAME"
}

verify_checksum() {
	log "Verifying SHA-256 checksum"
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
		rpm) rpm -U --replacepkgs --oldpackage "$1" ;;
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
	if [ -n "$SKIP_TLS_VERIFY" ]; then
		echo "GLC_SERVER__AUTH__INSECURE_TLS=true" >> "$ENROLLMENT_FILE"
	fi
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

	tmp_dir="$(mktemp -d)"
	trap 'rm -rf "$tmp_dir"' EXIT

	check_endpoint
	detect_package_type
	detect_arch
	resolve_release

	package_file="$tmp_dir/$ASSET_NAME"
	log "Downloading $ASSET_URL"
	download_file "$ASSET_URL" "$package_file"
	verify_checksum "$package_file"
	install_package "$package_file"
	write_enrollment_config
	start_service

	log "Graylog Collector installed ($ASSET_NAME)"
	echo "Check the service status with: systemctl status $SERVICE_NAME"
}

main "$@"
