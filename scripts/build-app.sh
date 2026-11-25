#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
APP_DISPLAY_NAME="Colab Tools"
EXECUTABLE_NAME="ColabTools"
VERSION="0.3.1"
BUILD_DIR="$ROOT_DIR/build"
APP_BUNDLE="$BUILD_DIR/${APP_DISPLAY_NAME}.app"
INSTALL_FLAG=0
ZIP_FLAG=0

for arg in "$@"; do
  case "$arg" in
    --install) INSTALL_FLAG=1 ;;
    --zip) ZIP_FLAG=1 ;;
  esac
done

echo "[1/5] Building ${APP_DISPLAY_NAME} v${VERSION} (release, sanitized paths)..."
cd "$ROOT_DIR"
# Strip host workspace absolute paths from compiled binary
swift build -c release \
  -Xswiftc -file-prefix-map -Xswiftc "${ROOT_DIR}=." \
  -Xswiftc -debug-prefix-map -Xswiftc "${ROOT_DIR}=." \
  -Xcc -ffile-prefix-map="${ROOT_DIR}=."

BIN_PATH="$(swift build -c release --show-bin-path)/${EXECUTABLE_NAME}"
if [ ! -x "$BIN_PATH" ]; then
  echo "[ERROR] Binary not found: $BIN_PATH" >&2
  exit 1
fi

echo "[2/5] Generating macOS AppIcon.icns..."
ICON_SRC="$ROOT_DIR/assets/AppIcon.png"
ICNS_OUT="$BUILD_DIR/AppIcon.icns"
mkdir -p "$BUILD_DIR"
if [ -f "$ICON_SRC" ]; then
  ICONSET="$(mktemp -d "${BUILD_DIR}/AppIcon.iconset.XXXXXX")"
  mv "$ICONSET" "${ICONSET}.iconset"
  ICONSET="${ICONSET}.iconset"
  trap 'rm -rf "$ICONSET"' EXIT
  for sz in 16 32 128 256 512; do
    sips -z "$sz" "$sz" "$ICON_SRC" --out "$ICONSET/icon_${sz}x${sz}.png" >/dev/null
    sz2=$((sz * 2))
    sips -z "$sz2" "$sz2" "$ICON_SRC" --out "$ICONSET/icon_${sz}x${sz}@2x.png" >/dev/null
  done
  iconutil -c icns "$ICONSET" -o "$ICNS_OUT"
  rm -rf "$ICONSET"
fi

echo "[3/5] Assembling '${APP_DISPLAY_NAME}.app'..."
STAGING_BUNDLE="$BUILD_DIR/${APP_DISPLAY_NAME}.staging.app"
rm -rf "$STAGING_BUNDLE"
mkdir -p "$STAGING_BUNDLE/Contents/MacOS" "$STAGING_BUNDLE/Contents/Resources"
cp "$BIN_PATH" "$STAGING_BUNDLE/Contents/MacOS/${EXECUTABLE_NAME}"
chmod +x "$STAGING_BUNDLE/Contents/MacOS/${EXECUTABLE_NAME}"
# Strip local symbol table & debug info so no local developer paths leak into release artifacts
strip -x "$STAGING_BUNDLE/Contents/MacOS/${EXECUTABLE_NAME}" 2>/dev/null || true

if [ -f "$ICNS_OUT" ]; then
  cp "$ICNS_OUT" "$STAGING_BUNDLE/Contents/Resources/AppIcon.icns"
fi
if [ -f "$ICON_SRC" ]; then
  sips -z 256 256 "$ICON_SRC" --out "$STAGING_BUNDLE/Contents/Resources/AppIconPreview.png" >/dev/null
fi
if [ -f "$ROOT_DIR/assets/wechat-qr.png" ]; then
  cp "$ROOT_DIR/assets/wechat-qr.png" "$STAGING_BUNDLE/Contents/Resources/wechat-qr.png"
fi

cat > "$STAGING_BUNDLE/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>zh_CN</string>
  <key>CFBundleExecutable</key>
  <string>${EXECUTABLE_NAME}</string>
  <key>CFBundleIdentifier</key>
  <string>io.github.ljmstark.colabtools</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>${APP_DISPLAY_NAME}</string>
  <key>CFBundleDisplayName</key>
  <string>${APP_DISPLAY_NAME}</string>
  <key>CFBundleIconFile</key>
  <string>AppIcon</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>${VERSION}</string>
  <key>CFBundleVersion</key>
  <string>4</string>
  <key>LSMinimumSystemVersion</key>
  <string>14.0</string>
  <key>LSUIElement</key>
  <true/>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
EOF

codesign --force --deep --sign - "$STAGING_BUNDLE"
codesign --verify --deep --strict "$STAGING_BUNDLE"

rm -rf "$APP_BUNDLE"
mv "$STAGING_BUNDLE" "$APP_BUNDLE"

echo "[4/5] Verifying release binary has zero host path leakage..."
if strings "$APP_BUNDLE/Contents/MacOS/${EXECUTABLE_NAME}" | grep -q "/Users/"; then
  echo "[WARN] Binary still contains /Users/ strings:" >&2
  strings "$APP_BUNDLE/Contents/MacOS/${EXECUTABLE_NAME}" | grep "/Users/" | head -5 >&2
else
  echo "  -> Binary path check passed (clean, arch: $(lipo -archs "$APP_BUNDLE/Contents/MacOS/${EXECUTABLE_NAME}"))."
fi

echo "[5/5] Built -> $APP_BUNDLE"

if [ "$INSTALL_FLAG" -eq 1 ]; then
  TARGET="/Applications/${APP_DISPLAY_NAME}.app"
  TEMP_TARGET="/Applications/${APP_DISPLAY_NAME}.new.app"
  rm -rf "$TEMP_TARGET"
  cp -R "$APP_BUNDLE" "$TEMP_TARGET"
  codesign --verify --deep --strict "$TEMP_TARGET"
  pkill -x "$EXECUTABLE_NAME" 2>/dev/null || true
  sleep 0.5
  rm -rf "$TARGET"
  mv "$TEMP_TARGET" "$TARGET"
  echo "[installed] -> $TARGET"
fi

if [ "$ZIP_FLAG" -eq 1 ]; then
  ARCH="$(uname -m)"
  ZIP_PATH="$BUILD_DIR/Colab-Tools-v${VERSION}-macOS-${ARCH}.zip"
  rm -f "$ZIP_PATH"
  ditto -c -k --sequesterRsrc --keepParent "$APP_BUNDLE" "$ZIP_PATH"
  echo "[packaged] -> $ZIP_PATH"
fi
