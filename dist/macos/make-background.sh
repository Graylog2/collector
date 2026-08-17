#!/usr/bin/env bash
#
# Build the installer background image from a single square source PNG
# using the macOS sips tool. Used by the package:macos:pkg:prepare task
# to create the logo shown in the installer window (referenced by
# distribution.xml for both the light and dark appearance).
#
# The image is resized to 256x256 and tagged with 144 DPI so the
# installer renders it at 128pt, leaving some margin on the left and
# the bottom of the background area.
#
# Usage: make-background.sh <source.png> <output.png>
#
set -euo pipefail

if [ $# -ne 2 ]; then
  echo "Usage: $0 <source.png> <output.png>" >&2
  exit 1
fi

SRC="$1"
OUT="$2"

sips -z 256 256 -s dpiWidth 144 -s dpiHeight 144 "$SRC" --out "$OUT" >/dev/null
