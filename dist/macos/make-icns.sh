#!/usr/bin/env bash
#
# Build an .icns icon from a single square source PNG using the macOS
# sips and iconutil tools. Used by the package:macos:pkg:prepare task
# to create the app bundle icon shown in Finder and in System Settings
# (Login Items & Extensions).
#
# Usage: make-icns.sh <source.png> <output.icns>
#
set -euo pipefail

if [ $# -ne 2 ]; then
  echo "Usage: $0 <source.png> <output.icns>" >&2
  exit 1
fi

SRC="$1"
OUT="$2"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
ICONSET="${TMP_DIR}/AppIcon.iconset"
mkdir -p "$ICONSET"

# Only render sizes up to 256px: the source artwork is 288x288, so the
# 512/1024 variants would just be blurry upscales.
sips -z 16 16   "$SRC" --out "${ICONSET}/icon_16x16.png" >/dev/null
sips -z 32 32   "$SRC" --out "${ICONSET}/icon_16x16@2x.png" >/dev/null
sips -z 32 32   "$SRC" --out "${ICONSET}/icon_32x32.png" >/dev/null
sips -z 64 64   "$SRC" --out "${ICONSET}/icon_32x32@2x.png" >/dev/null
sips -z 128 128 "$SRC" --out "${ICONSET}/icon_128x128.png" >/dev/null
sips -z 256 256 "$SRC" --out "${ICONSET}/icon_128x128@2x.png" >/dev/null
sips -z 256 256 "$SRC" --out "${ICONSET}/icon_256x256.png" >/dev/null

iconutil -c icns -o "$OUT" "$ICONSET"
