#!/usr/bin/env bash
set -euo pipefail

NATIVE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="$NATIVE_DIR/dist/Mac OCR Native.app"
CONTENTS_DIR="$APP_DIR/Contents"
MACOS_DIR="$CONTENTS_DIR/MacOS"
DEFAULT_PROXY_URL="${MACOCR_DEFAULT_PROXY_URL:-${PUBLIC_API_BASE_URL:-https://ocr.dungxbuif.com}}"
DEFAULT_MODE="${MACOCR_DEFAULT_MODE:-${APP_ENV:-production}}"
DEFAULT_PORT="${MACOCR_DEFAULT_PORT:-${NATIVE_PORT:-8787}}"
DEFAULT_LIMIT="${MACOCR_DEFAULT_LIMIT:-${NATIVE_LIMIT:-6}}"
DEFAULT_AUTH_SECRET="${MACOCR_DEFAULT_AUTH_SECRET:-}"
DEFAULT_AUTH_MODE="${MACOCR_DEFAULT_AUTH_MODE:-shared}"
DEFAULT_TRANSPORT="${MACOCR_DEFAULT_TRANSPORT:-websocket}"
ADAPTIVE_CONCURRENCY="${MACOCR_ADAPTIVE_CONCURRENCY:-true}"
RESERVE_CORES="${MACOCR_RESERVE_CORES:-2}"
RESERVE_MEMORY_GB="${MACOCR_RESERVE_MEMORY_GB:-10}"
MEMORY_PER_UNIT_GB="${MACOCR_MEMORY_PER_UNIT_GB:-2}"
IMAGE_JOB_UNITS="${MACOCR_IMAGE_JOB_UNITS:-1}"
PDF_JOB_UNITS="${MACOCR_PDF_JOB_UNITS:-3}"
CAPACITY_RECOVERY_SAMPLES="${MACOCR_CAPACITY_RECOVERY_SAMPLES:-5}"
DEFAULT_NODE_ID="${MACOCR_DEFAULT_NODE_ID:-${NATIVE_NODE_ID:-ocr-native-01}}"
LOG_RETENTION_DAYS="${MACOCR_LOG_RETENTION_DAYS:-30}"
APP_VERSION="${MACOCR_VERSION:-2.0.0}"
APP_BUILD_NUMBER="${MACOCR_BUILD_NUMBER:-4}"

cd "$NATIVE_DIR"
# Share one bundle across Apple Silicon and Intel Macs by default.
MACOCR_ARCHS_VALUE="${MACOCR_ARCHS:-arm64 x86_64}"
read -r -a MACOCR_ARCH_ARRAY <<< "$MACOCR_ARCHS_VALUE"
MACOCR_BUILD_ARGS=(-c release)
for MACOCR_ARCH_VALUE in "${MACOCR_ARCH_ARRAY[@]}"; do
  case "$MACOCR_ARCH_VALUE" in arm64|x86_64) ;; *) echo "Unsupported architecture: $MACOCR_ARCH_VALUE" >&2; exit 1 ;; esac
  MACOCR_BUILD_ARGS+=(--arch "$MACOCR_ARCH_VALUE")
done
swift build "${MACOCR_BUILD_ARGS[@]}"
MACOCR_BIN_DIR="$(swift build "${MACOCR_BUILD_ARGS[@]}" --show-bin-path)"

rm -rf "$APP_DIR"
mkdir -p "$MACOS_DIR"
cp "$MACOCR_BIN_DIR/mac-ocr-native" "$MACOS_DIR/mac-ocr-native"

cat >"$CONTENTS_DIR/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>en</string>
  <key>CFBundleDisplayName</key><string>Mac OCR Native</string>
  <key>CFBundleExecutable</key><string>mac-ocr-native</string>
  <key>CFBundleIdentifier</key><string>com.macocr.native</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>Mac OCR Native</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0.3</string>
  <key>CFBundleVersion</key><string>3</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
  <key>MacOCRDefaultAuthMode</key><string></string>
  <key>MacOCRDefaultTransport</key><string></string>
  <key>MacOCRDefaultProxyURL</key><string></string>
  <key>MacOCRDefaultMode</key><string></string>
  <key>MacOCRDefaultPort</key><string></string>
  <key>MacOCRDefaultLimit</key><string></string>
  <key>MacOCRDefaultAuthSecret</key><string></string>
  <key>MacOCRAdaptiveConcurrency</key><string></string>
  <key>MacOCRReserveCores</key><string></string>
  <key>MacOCRReserveMemoryGB</key><string></string>
  <key>MacOCRMemoryPerUnitGB</key><string></string>
  <key>MacOCRImageJobUnits</key><string></string>
  <key>MacOCRPDFJobUnits</key><string></string>
  <key>MacOCRCapacityRecoverySamples</key><string></string>
  <key>MacOCRDefaultNodeID</key><string></string>
  <key>MacOCRLogRetentionDays</key><string></string>
</dict>
</plist>
PLIST

plutil -replace MacOCRDefaultAuthMode -string "$DEFAULT_AUTH_MODE" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRDefaultTransport -string "$DEFAULT_TRANSPORT" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRDefaultProxyURL -string "$DEFAULT_PROXY_URL" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRDefaultMode -string "$DEFAULT_MODE" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRDefaultPort -string "$DEFAULT_PORT" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRDefaultLimit -string "$DEFAULT_LIMIT" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRDefaultAuthSecret -string "$DEFAULT_AUTH_SECRET" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRAdaptiveConcurrency -string "$ADAPTIVE_CONCURRENCY" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRReserveCores -string "$RESERVE_CORES" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRReserveMemoryGB -string "$RESERVE_MEMORY_GB" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRMemoryPerUnitGB -string "$MEMORY_PER_UNIT_GB" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRImageJobUnits -string "$IMAGE_JOB_UNITS" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRPDFJobUnits -string "$PDF_JOB_UNITS" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRCapacityRecoverySamples -string "$CAPACITY_RECOVERY_SAMPLES" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRDefaultNodeID -string "$DEFAULT_NODE_ID" "$CONTENTS_DIR/Info.plist"
plutil -replace MacOCRLogRetentionDays -string "$LOG_RETENTION_DAYS" "$CONTENTS_DIR/Info.plist"
plutil -replace CFBundleShortVersionString -string "$APP_VERSION" "$CONTENTS_DIR/Info.plist"
plutil -replace CFBundleVersion -string "$APP_BUILD_NUMBER" "$CONTENTS_DIR/Info.plist"

codesign --force --deep --sign - "$APP_DIR"
printf '%s\n' "$APP_DIR"
