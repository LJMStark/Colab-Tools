#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="$(python3 -c 'import json; print(json.load(open("mygo.json"))["version"])')"
BUILD_MAC=false
BUILD_WIN=false
MAKE_ZIP=false
DO_INSTALL=false

for arg in "$@"; do
  case "$arg" in
    --mac) BUILD_MAC=true ;;
    --win) BUILD_WIN=true ;;
    --all) BUILD_MAC=true; BUILD_WIN=true ;;
    --zip) MAKE_ZIP=true ;;
    --install) DO_INSTALL=true; BUILD_MAC=true ;;
    *)
      echo "Unknown flag: $arg" >&2
      echo "Usage: $0 [--mac] [--win] [--all] [--zip] [--install]" >&2
      exit 2
      ;;
  esac
done

if ! $BUILD_MAC && ! $BUILD_WIN; then
  BUILD_MAC=true
fi

export GOTOOLCHAIN=auto
export CGO_ENABLED=0
export GOFLAGS="-trimpath"

mkdir -p build

audit_no_local_paths() {
  local bin="$1"
  local matches
  matches="$(strings "$bin" | grep -E '/Users/[A-Za-z0-9._-]+/' || true)"
  if [[ -n "$matches" ]]; then
    echo "ERROR: local user path leaked into binary: $bin" >&2
    printf '%s\n' "$matches" | head -n 10 >&2
    exit 1
  fi
}

if $BUILD_MAC; then
  echo "== Building macOS arm64 (.app) v${VERSION} =="
  rm -rf build/mac "build/Colab Tools.app"
  go tool mygo build -platform darwin/arm64 -skip-dmg -o build/mac .

  APP_SRC="$(find build/mac -maxdepth 3 -name '*.app' -type d | head -n 1)"
  if [[ -z "$APP_SRC" ]]; then
    echo "ERROR: mygo build did not produce a .app bundle under build/mac" >&2
    ls -laR build/mac >&2
    exit 1
  fi

  mv "$APP_SRC" "build/Colab Tools.app"
  MAC_BIN="$(find "build/Colab Tools.app/Contents/MacOS" -maxdepth 1 -type f | head -n 1)"
  if [[ -z "$MAC_BIN" ]]; then
    echo "ERROR: missing macOS executable inside bundle" >&2
    exit 1
  fi

  audit_no_local_paths "$MAC_BIN"
  codesign --verify --deep --strict "build/Colab Tools.app"
  echo "Verified macOS bundle: build/Colab Tools.app ($(du -sh "build/Colab Tools.app" | awk '{print $1}'))"

  if $MAKE_ZIP; then
    MAC_ZIP="build/Colab-Tools-v${VERSION}-macOS-arm64.zip"
    rm -f "$MAC_ZIP"
    ditto -c -k --sequesterRsrc --keepParent "build/Colab Tools.app" "$MAC_ZIP"
    echo "Packaged macOS zip: $MAC_ZIP ($(du -sh "$MAC_ZIP" | awk '{print $1}'))"
  fi
fi

if $BUILD_WIN; then
  echo "== Building Windows amd64 (.exe) v${VERSION} =="
  rm -rf build/win
  go tool mygo build -platform windows/amd64 -skip-installer -o build/win .

  WIN_SRC="$(find build/win -maxdepth 3 -name '*.exe' -type f | head -n 1)"
  if [[ -z "$WIN_SRC" ]]; then
    echo "ERROR: mygo build did not produce a .exe under build/win" >&2
    ls -laR build/win >&2
    exit 1
  fi

  audit_no_local_paths "$WIN_SRC"
  mkdir -p build/win-stage
  rm -rf build/win-stage/*
  cp "$WIN_SRC" "build/win-stage/Colab Tools.exe"
  echo "Verified Windows binary: build/win-stage/Colab Tools.exe ($(du -sh "build/win-stage/Colab Tools.exe" | awk '{print $1}'))"

  if $MAKE_ZIP; then
    WIN_ZIP="build/Colab-Tools-v${VERSION}-Windows-amd64.zip"
    rm -f "$WIN_ZIP"
    python3 -c '
import zipfile, sys
with zipfile.ZipFile(sys.argv[1], "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
    zf.write(sys.argv[2], "Colab Tools.exe")
' "$WIN_ZIP" "build/win-stage/Colab Tools.exe"
    echo "Packaged Windows zip: $WIN_ZIP ($(du -sh "$WIN_ZIP" | awk '{print $1}'))"
  fi
fi

if $DO_INSTALL; then
  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "ERROR: --install is only supported on macOS" >&2
    exit 1
  fi
  echo "== Installing to /Applications/Colab Tools.app =="
  osascript -e 'tell application "Colab Tools" to quit' >/dev/null 2>&1 || true
  sleep 1
  pkill -x "Colab Tools" >/dev/null 2>&1 || true
  pkill -x "colab-tools" >/dev/null 2>&1 || true
  sleep 0.5
  rm -rf "/Applications/Colab Tools.app"
  ditto "build/Colab Tools.app" "/Applications/Colab Tools.app"
  xattr -dr com.apple.quarantine "/Applications/Colab Tools.app" >/dev/null 2>&1 || true
  open "/Applications/Colab Tools.app"
  echo "Launched /Applications/Colab Tools.app"
fi
