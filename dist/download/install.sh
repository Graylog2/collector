#!/usr/bin/env bash
#
# Download, install, and enroll the Graylog Collector on macOS.
#
# Usage:
#   sudo ENROLLTOKEN="eyJhb..." ENROLLENDPOINT="https://graylog.example.com" ./install.sh
#
set -euo pipefail

: "${ENROLLTOKEN:?Set ENROLLTOKEN to the enrollment token}"
: "${ENROLLENDPOINT:?Set ENROLLENDPOINT to your Graylog server URL}"

# NOTE: placeholder URL - the real download-service route is not finalized yet.
PKG_URL="${PKG_URL:-${ENROLLENDPOINT%/}/collectors/download/macos/graylog-collector.pkg}"

LABEL="com.graylog.collector"
CONFIG_DIR="/Library/Application Support/Graylog/Collector"
CONFIG_FILE="${CONFIG_DIR}/supervisor.yaml"
INSTALL_LOG="/var/log/install.log"

TMP_DIR="$(mktemp -d -t graylog-collector)"
PKG_PATH="${TMP_DIR}/graylog-collector.pkg"

cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

fail() {
  echo "Error: $*" >&2
  echo "" >&2
  echo "Installer log: ${INSTALL_LOG}" >&2
  exit 1
}

assert_admin() {
  if [ "$(id -u)" -ne 0 ]; then
    fail "Please run this command with sudo (root privileges are required)."
  fi
}

download_pkg() {
  echo "Downloading Graylog Collector..."
  echo "Package URL: ${PKG_URL}"
  # --proto '=https': refuse to fetch an installer we run as root over plaintext.
  curl -fSL --proto '=https' --tlsv1.2 -o "$PKG_PATH" "$PKG_URL" \
    || fail "Failed to download package from ${PKG_URL}"
}

write_config() {
  echo "Writing enrollment configuration..."
  echo "Enrollment endpoint: ${ENROLLENDPOINT}"
  # Pre-seed the config BEFORE install: postinstall kickstarts the daemon, and
  # it reads this file on first launch. umask 077 -> born 0600, no readable window.
  mkdir -p "$CONFIG_DIR"
  ( umask 077
    cat > "$CONFIG_FILE" <<EOF
server:
  auth:
    enrollment_endpoint: "${ENROLLENDPOINT}"
    enrollment_token: "${ENROLLTOKEN}"
EOF
  )
  chown root:wheel "$CONFIG_FILE"
  chmod 600 "$CONFIG_FILE"
}

install_collector() {
  echo "Installing Graylog Collector..."
  installer -pkg "$PKG_PATH" -target / \
    || fail "Package installation failed. See ${INSTALL_LOG}"
}

verify_service() {
  if ! launchctl print "system/${LABEL}" >/dev/null 2>&1; then
    echo "Warning: collector installed, but service '${LABEL}' was not found." >&2
    return
  fi

  local state
  state="$(launchctl print "system/${LABEL}" 2>/dev/null \
    | awk -F'= ' '/^[[:space:]]*state =/{gsub(/[[:space:]]/,"",$2); print $2; exit}')"

  if [ "$state" != "running" ]; then
    echo "Starting Graylog Collector service..."
    launchctl kickstart "system/${LABEL}" || true
    state="$(launchctl print "system/${LABEL}" 2>/dev/null \
      | awk -F'= ' '/^[[:space:]]*state =/{gsub(/[[:space:]]/,"",$2); print $2; exit}')"
  fi

  if [ "$state" = "running" ]; then
    echo "Graylog Collector service is running."
  else
    echo "Warning: Graylog Collector service state is: ${state:-unknown}" >&2
  fi
}

assert_admin
download_pkg
write_config
install_collector
verify_service

echo ""
echo "Success. Waiting for this collector to appear in Graylog..."
