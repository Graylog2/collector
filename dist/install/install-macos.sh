#!/bin/sh
#
# Install Graylog Collector on macOS.
#
# The script downloads the Collector installer package (.pkg) from the
# GitHub releases, verifies its checksum and signature, writes the enrollment
# configuration, and installs the package. The package registers and starts
# the launchd service.
#
# Usage:
#   sudo sh install-macos.sh --endpoint <URL> --token <TOKEN> [--version <VERSION>]
#   sudo sh install-macos.sh --endpoint <URL> --token-file <PATH> [--version <VERSION>]
#
# Options:
#   -e, --endpoint <URL>    Enrollment endpoint, usually the Graylog server URL
#   -t, --token <TOKEN>     Enrollment token, provided by the Graylog server
#   --token-file <PATH>     Read the enrollment token from a file instead. This keeps
#                           the token out of the shell history and process list.
#   -v, --version <VERSION> Collector version to install (default: latest release)
#   -h, --help              Show this help
#
# Requirements: root privileges, curl or wget.
#
set -eu

GITHUB_REPO="Graylog2/collector"
MANIFEST_NAME="SHA256SUMS"
CONFIG_DIR="/Library/Application Support/Graylog/Collector"
CONFIG_FILE="$CONFIG_DIR/supervisor.yaml"
CONFIG_MARKER="# Created by install-macos.sh"
KEYS_DIR="$CONFIG_DIR/keys"
SERVICE_LABEL="org.graylog.collector"
# Apple Developer ID that signs the Graylog Collector installer packages.
SIGNING_IDENTITY="Developer ID Installer: Graylog, Inc. (6NH52462TL)"

ENDPOINT=""
TOKEN=""
TOKEN_FILE=""
VERSION=""

usage() {
	cat <<EOF
Usage: sudo sh install-macos.sh --endpoint <URL> --token <TOKEN> [--version <VERSION>]
       sudo sh install-macos.sh --endpoint <URL> --token-file <PATH> [--version <VERSION>]

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

check_macos() {
	[ "$(uname -s)" = "Darwin" ] || fail "this script only supports macOS"
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

# Download the release manifest and set RELEASE_TAG, ASSET_URL, ASSET_NAME,
# and ASSET_DIGEST for the universal macOS package. The manifest lists one
# "<sha256>  <file name>" line per release artifact.
resolve_release() {
	if [ -n "$VERSION" ]; then
		manifest_url="https://github.com/$GITHUB_REPO/releases/download/$VERSION/$MANIFEST_NAME"
	else
		manifest_url="https://github.com/$GITHUB_REPO/releases/latest/download/$MANIFEST_NAME"
	fi

	log "Fetching release manifest for ${VERSION:-latest}"
	manifest="$(fetch_stdout "$manifest_url")" || fail "release ${VERSION:-latest} not found, or it has no $MANIFEST_NAME"

	entry="$(printf '%s\n' "$manifest" | grep -E '^[0-9a-fA-F]{64}[[:space:]]+graylog-collector-[^[:space:]]+-darwin-universal\.pkg$' | head -n 1)"
	[ -n "$entry" ] || fail "release manifest has no macOS package"

	ASSET_DIGEST="${entry%% *}"
	ASSET_NAME="${entry##* }"

	RELEASE_TAG="$(printf '%s' "$ASSET_NAME" | sed 's/^graylog-collector-\(.*\)-darwin-universal\.pkg$/\1/')"
	ASSET_URL="https://github.com/$GITHUB_REPO/releases/download/$RELEASE_TAG/$ASSET_NAME"
}

verify_checksum() {
	log "Verifying sha256 checksum"
	actual="$(shasum -a 256 "$1" | cut -d ' ' -f 1)"
	[ "$actual" = "$ASSET_DIGEST" ] || fail "checksum mismatch for $ASSET_NAME (expected $ASSET_DIGEST, got $actual)"
}

# installer(8) accepts unsigned packages, and Gatekeeper does not assess files
# that curl or wget downloaded. Require a valid Apple developer signature from
# Graylog before the package is installed.
verify_signature() {
	log "Verifying package signature"
	signature="$(pkgutil --check-signature "$1" || true)"

	printf '%s\n' "$signature" | grep -q "Status: signed by a developer certificate issued by Apple" ||
		fail "$ASSET_NAME has no valid Apple developer signature"
	printf '%s\n' "$signature" | grep -qF "$SIGNING_IDENTITY" ||
		fail "$ASSET_NAME is not signed by \"$SIGNING_IDENTITY\""
}

# The Collector is enrolled when a signing key and certificate exist. It then
# keeps using them and ignores the enrollment token, so tell the user.
is_enrolled() {
	[ -f "$KEYS_DIR/signing.key" ] && [ -f "$KEYS_DIR/signing.crt" ]
}

warn_existing_enrollment() {
	is_enrolled || return 0
	cat >&2 <<EOF
WARNING: The Collector is already enrolled. Credentials exist in "$KEYS_DIR".
         The Collector keeps using them and ignores the enrollment token.
         To enroll again, first remove the Collector in Graylog. The server
         rejects an enrollment with new credentials while it still knows the
         Collector. Then stop the service, remove the credentials, and run
         this script again:

             sudo launchctl bootout system/$SERVICE_LABEL
             sudo rm -rf "$KEYS_DIR"

EOF
}

# Stop early when a configuration file exists that this script did not create.
# A file with the marker comment came from an earlier run and is replaced.
check_existing_config() {
	[ -f "$CONFIG_FILE" ] || return 0
	head -n 1 "$CONFIG_FILE" | grep -qF "$CONFIG_MARKER" && return 0

	message="$CONFIG_FILE exists and was not created by this script.
Set the enrollment endpoint and token in that file by hand, or remove the file and run this script again."
	if is_enrolled; then
		message="$message
Note: Removing the file does not enroll the Collector again. See the warning above."
	fi
	fail "$message"
}

# The package starts the service right after installation, so the enrollment
# configuration must exist before the package is installed.
write_enrollment_config() {
	log "Writing enrollment configuration to $CONFIG_FILE"
	mkdir -p "$CONFIG_DIR"
	umask 077
	cat > "$CONFIG_FILE" <<EOF
$CONFIG_MARKER
server:
  auth:
    enrollment_endpoint: "$ENDPOINT"
    enrollment_token: "$TOKEN"
EOF
	chown root:wheel "$CONFIG_FILE"
	chmod 0600 "$CONFIG_FILE"
}

install_package() {
	log "Installing $ASSET_NAME"
	installer -pkg "$1" -target /
}

main() {
	parse_args "$@"
	check_macos
	check_root
	warn_existing_enrollment
	check_existing_config
	detect_downloader
	resolve_release

	tmp_dir="$(mktemp -d)"
	trap 'rm -rf "$tmp_dir"' EXIT

	package_file="$tmp_dir/$ASSET_NAME"
	log "Downloading $ASSET_URL"
	download_file "$ASSET_URL" "$package_file"
	verify_checksum "$package_file"
	verify_signature "$package_file"
	write_enrollment_config
	install_package "$package_file"

	log "Graylog Collector $RELEASE_TAG installed"
	echo "Check the service status with: sudo launchctl print system/$SERVICE_LABEL"
}

main "$@"
