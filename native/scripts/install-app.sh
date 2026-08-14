#!/usr/bin/env bash
set -euo pipefail

NATIVE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_APP="$NATIVE_DIR/dist/Mac OCR Native.app"
INSTALL_ROOT="$HOME/Applications"
INSTALLED_APP="$INSTALL_ROOT/Mac OCR Native.app"
INSTALLED_EXECUTABLE="$INSTALLED_APP/Contents/MacOS/mac-ocr-native"

"$NATIVE_DIR/scripts/build-app.sh" >/dev/null
mkdir -p "$INSTALL_ROOT"
if pgrep -f "$INSTALLED_EXECUTABLE" >/dev/null 2>&1; then
  pkill -TERM -f "$INSTALLED_EXECUTABLE"
  for _ in 1 2 3 4 5; do
    pgrep -f "$INSTALLED_EXECUTABLE" >/dev/null 2>&1 || break
    sleep 0.2
  done
fi
rm -rf "$INSTALLED_APP"
ditto "$SOURCE_APP" "$INSTALLED_APP"
open "$INSTALLED_APP"
printf 'Installed and opened: %s\n' "$INSTALLED_APP"
