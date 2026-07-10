#!/usr/bin/env bash
#
# Uninstall the Graylog Collector from macOS.
#
# macOS .pkg installers have no uninstall hook (unlike dpkg/rpm), so removal is
# this script's job. By default it stops and removes the service, binary, logs,
# and package receipt but KEEPS the enrollment config and data, so a later
# reinstall stays enrolled. This mirrors the Linux package's "remove" vs "purge"
# distinction (see dist/linux/preremove.sh and postremove.sh).
#
# Usage:
#   sudo ./uninstall.sh           # remove software, keep config/data
#   sudo ./uninstall.sh --purge   # also remove config/data (incl. the token)
#
set -euo pipefail

LABEL="com.graylog.collector"
PKG_ID="com.graylog.collector"          # pkgbuild --identifier
PLIST="/Library/LaunchDaemons/${LABEL}.plist"
INSTALL_DIR="/usr/local/graylog-collector"
DATA_DIR="/Library/Application Support/Graylog/Collector"
PARENT_DIR="/Library/Application Support/Graylog"
LOG_FILES=(
  "/var/log/graylog-collector.log"
  "/var/log/graylog-collector.err.log"
)

PURGE="false"

usage() {
  cat <<EOF
Usage: sudo $0 [--purge]

  --purge   Also remove enrollment config and data in
            "${DATA_DIR}", including the enrollment token.
            Default keeps them so a reinstall stays enrolled.
  -h        Show this help.
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --purge) PURGE="true" ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 1 ;;
  esac
  shift
done

if [ "$(id -u)" -ne 0 ]; then
  echo "This script must be run as root (use sudo)." >&2
  exit 1
fi

# Stop and unload the daemon. Try both by-path and by-label so it works even if
# the plist is already gone (partial/previous removal). Ignore "not loaded".
echo "Stopping ${LABEL}..."
launchctl bootout system "$PLIST" 2>/dev/null || true
launchctl bootout "system/${LABEL}" 2>/dev/null || true

# Remove the launchd job definition, the installed software, and logs.
# The :? guards are defensive: never let an empty variable turn into "rm -rf /".
echo "Removing files..."
rm -f "$PLIST"
for f in "${LOG_FILES[@]}"; do
  rm -f "$f"
done

# Drop the package receipt so pkgutil no longer reports the product installed.
if pkgutil --pkg-info "$PKG_ID" >/dev/null 2>&1; then
  echo "Forgetting package receipt ${PKG_ID}..."
  pkgutil --forget "$PKG_ID" >/dev/null
fi

if [ "$PURGE" = "true" ]; then
  echo "Purging configuration and data..."
  rm -rf "${DATA_DIR:?}"
  # Remove the parent "Graylog" dir only if empty - it may be shared with
  # other Graylog products, so never force it.
  rmdir "$PARENT_DIR" 2>/dev/null || true
else
  echo "Keeping configuration and data in: ${DATA_DIR}"
  echo "(Re-run with --purge to remove them, including the enrollment token.)"
fi

# Remove the install dir LAST: it contains this very script. On Unix the kernel
# keeps the running file alive through its open descriptor until the script
# exits, so deleting it mid-run is safe.
rm -rf "${INSTALL_DIR:?}"

echo ""
echo "Graylog Collector uninstalled."
