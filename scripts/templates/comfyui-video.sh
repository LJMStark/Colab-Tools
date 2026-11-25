#!/usr/bin/env bash
# Colab Tools Starter Template — ComfyUI Video / Image Generation
# Copy to Google Drive (e.g. /content/drive/MyDrive/colab-tools/comfyui-video.sh),
# customize COMFY_DIR / MODEL_NAME, and select "视频生成" or "图像生成" in Colab Tools.
set -euo pipefail

SERVICE_PORT="${SERVICE_PORT:-8188}"
WORKLOAD_KIND="${WORKLOAD_KIND:-video}"
COMFY_DIR="${COMFY_DIR:-/content/ComfyUI}"
DRIVE_MODELS="${DRIVE_MODELS:-/content/drive/MyDrive/comfyui-models}"
DRIVE_OUTPUT="${DRIVE_OUTPUT:-/content/drive/MyDrive/comfyui-output}"
WORKLOAD_NAME="${WORKLOAD_NAME:-ComfyUI (${WORKLOAD_KIND})}"
STATUS_FILE="/tmp/colab-tools-status.json"
LOG_FILE="/tmp/comfyui.log"

mkdir -p "$DRIVE_OUTPUT"
if [ ! -w "$DRIVE_OUTPUT" ]; then
  echo "[error] Output directory is not writable: $DRIVE_OUTPUT" >&2
  exit 1
fi

write_status() {
  local ui_url="${1:-}"
  python3 - "$STATUS_FILE" "$WORKLOAD_KIND" "$WORKLOAD_NAME" "$ui_url" "$SERVICE_PORT" "$DRIVE_OUTPUT" <<'PY'
import json, pathlib, sys
dst, kind, name, ui_url, port_s, out_dir = sys.argv[1:]
port = int(port_s)
if not (1024 <= port <= 65535):
    raise SystemExit(f"Invalid port: {port}")
payload = {
    "kind": kind,
    "name": name,
    "ui_url": ui_url,
    "port": port,
    "detail": f"ComfyUI :{port} · Output -> {pathlib.Path(out_dir).name}",
}
tmp = pathlib.Path(dst + ".tmp")
tmp.write_text(json.dumps(payload, ensure_ascii=False))
tmp.replace(dst)
PY
}

if [ ! -d "$COMFY_DIR" ]; then
  echo "[template] Cloning ComfyUI into $COMFY_DIR..."
  git clone --depth 1 https://github.com/comfyanonymous/ComfyUI.git "$COMFY_DIR"
  pip install -q -r "$COMFY_DIR/requirements.txt"
fi

# Link Google Drive models when present
if [ -d "$DRIVE_MODELS" ] && [ ! -e "$COMFY_DIR/models/drive_shared" ]; then
  mkdir -p "$COMFY_DIR/models"
  ln -s "$DRIVE_MODELS" "$COMFY_DIR/models/drive_shared"
fi

# Start ComfyUI cleanly with explicit Drive --output-directory (never relying on symlinking an existing directory)
if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
  nohup python3 "$COMFY_DIR/main.py" \
    --listen 127.0.0.1 \
    --port "$SERVICE_PORT" \
    --output-directory "$DRIVE_OUTPUT" \
    --disable-auto-launch \
    > "$LOG_FILE" 2>&1 &
fi

# Wait up to 90s for ComfyUI API readiness
for _ in $(seq 1 45); do
  if curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
  echo "[error] ComfyUI did not become ready on port ${SERVICE_PORT}. Tail of log:"
  tail -n 30 "$LOG_FILE" || true
  exit 1
fi

# Optional: leave PUBLIC_URL empty and use Colab Tools' built-in one-click SSH Tunnel (127.0.0.1:8188)
PUBLIC_URL=""
write_status "$PUBLIC_URL"

echo "WORKLOAD_NAME=${WORKLOAD_NAME}"
if [ -n "$PUBLIC_URL" ]; then
  echo "PUBLIC_URL=${PUBLIC_URL}"
fi
echo "WORKLOAD_READY=1"
