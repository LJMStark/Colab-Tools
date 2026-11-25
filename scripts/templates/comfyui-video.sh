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

write_status() {
  local ui_url="${1:-}"
  local tmp="${STATUS_FILE}.tmp"
  cat > "$tmp" <<JSON
{
  "kind": "${WORKLOAD_KIND}",
  "name": "${WORKLOAD_NAME}",
  "ui_url": "${ui_url}",
  "port": ${SERVICE_PORT},
  "detail": "ComfyUI :${SERVICE_PORT} · Output -> Drive"
}
JSON
  mv "$tmp" "$STATUS_FILE"
}

if [ ! -d "$COMFY_DIR" ]; then
  echo "[template] Cloning ComfyUI into $COMFY_DIR..."
  git clone --depth 1 https://github.com/comfyanonymous/ComfyUI.git "$COMFY_DIR"
  pip install -q -r "$COMFY_DIR/requirements.txt"
fi

# Link Google Drive models & outputs when present
if [ -d "$DRIVE_MODELS" ] && [ ! -L "$COMFY_DIR/models/checkpoints" ]; then
  mkdir -p "$COMFY_DIR/models"
  ln -sfn "$DRIVE_MODELS" "$COMFY_DIR/models/drive_shared"
fi
ln -sfn "$DRIVE_OUTPUT" "$COMFY_DIR/output"

# Start ComfyUI cleanly if not already listening
if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
  nohup python3 "$COMFY_DIR/main.py" \
    --listen 127.0.0.1 \
    --port "$SERVICE_PORT" \
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
