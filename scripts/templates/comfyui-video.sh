#!/usr/bin/env bash
# Colab Tools Built-in / Starter Template — General ComfyUI Video & Image Studio
# Uses sequential `rsync -ahW` from Google Drive to local NVMe (never FUSE symlinks for large weights)
# and writes every rendered video/image straight to Google Drive outputs.
set -euo pipefail

SERVICE_PORT="${SERVICE_PORT:-8188}"
WORKLOAD_KIND="${WORKLOAD_KIND:-video}"
USE_GOOGLE_DRIVE="${USE_GOOGLE_DRIVE:-1}"
COMFY_DIR="${COMFY_DIR:-/content/ComfyUI}"
# Prefer new DRIVE_*_DIR env vars while preserving backwards compatibility with legacy DRIVE_MODELS / DRIVE_OUTPUT
DRIVE_MODELS="${DRIVE_MODELS_DIR:-${DRIVE_MODELS:-/content/drive/MyDrive/ColabTools/models}}"
DRIVE_OUTPUT="${DRIVE_OUTPUT_DIR:-${DRIVE_OUTPUT:-/content/drive/MyDrive/ColabTools/outputs}}"
ENABLE_CLOUDFLARED="${ENABLE_CLOUDFLARED:-0}"
WORKLOAD_NAME="${WORKLOAD_NAME:-ComfyUI 视频/绘图}"
STATUS_FILE="/tmp/colab-tools-status.json"
LOG_FILE="/tmp/comfyui.log"
CF_LOG_FILE="/tmp/cloudflared-comfy.log"

write_status() {
  local label="$1"
  local val="$2"
  local unit="$3"
  local ratio="$4"
  local busy="$5"
  local detail="$6"
  local ui_url="${7:-}"
  python3 - "$STATUS_FILE" "$WORKLOAD_KIND" "$WORKLOAD_NAME" "$SERVICE_PORT" "$label" "$val" "$unit" "$ratio" "$busy" "$detail" "$ui_url" <<'PY'
import json, pathlib, sys
dst, kind, name, port_s, label, val, unit, ratio_s, busy_s, detail, ui_url = sys.argv[1:]
payload = {
    "kind": kind,
    "name": name,
    "port": int(port_s),
    "metric_label": label,
    "metric_value": val,
    "metric_unit": unit,
    "metric_ratio": max(0.0, min(1.0, float(ratio_s))),
    "detail": detail,
    "ui_url": ui_url,
}
if busy_s == "true":
    payload["busy"] = True
elif busy_s == "false":
    payload["busy"] = False
tmp = pathlib.Path(dst + ".tmp")
tmp.write_text(json.dumps(payload, ensure_ascii=False))
tmp.replace(dst)
PY
}

DRIVE_AVAILABLE=0
if [ "$USE_GOOGLE_DRIVE" = "1" ] && [ -d "/content/drive/MyDrive" ]; then
  DRIVE_AVAILABLE=1
  OUTPUT_DIR="$DRIVE_OUTPUT"
  mkdir -p "$OUTPUT_DIR"
else
  OUTPUT_DIR="/content/output"
  mkdir -p "$OUTPUT_DIR"
fi

write_status "正在部署" "25%" "装 ComfyUI" "0.25" "true" "准备 ComfyUI 运行环境…"
if [ ! -d "$COMFY_DIR" ]; then
  git clone --depth 1 https://github.com/comfyanonymous/ComfyUI.git "$COMFY_DIR"
  pip install -q -r "$COMFY_DIR/requirements.txt"
fi

# Copy Drive models sequentially to local NVMe (never symlink large .safetensors directly over Drive FUSE)
if [ "$DRIVE_AVAILABLE" = "1" ] && [ -d "$DRIVE_MODELS" ]; then
  write_status "拷贝模型" "60%" "拷到本地盘" "0.60" "true" "将 Drive 模型顺序拷至本地高速盘…"
  mkdir -p "$COMFY_DIR/models"
  rsync -ahW --ignore-existing "$DRIVE_MODELS/" "$COMFY_DIR/models/" || true
fi

# Cleanly restart ComfyUI so updated --output-directory / newly copied models always apply
write_status "启动画布" "85%" "ComfyUI" "0.85" "true" "正在启动 ComfyUI :${SERVICE_PORT}…"
pkill -f "${COMFY_DIR}/main.py" 2>/dev/null || true
for _ in $(seq 1 10); do
  if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

nohup python3 "$COMFY_DIR/main.py" \
  --listen 127.0.0.1 \
  --port "$SERVICE_PORT" \
  --output-directory "$OUTPUT_DIR" \
  --disable-auto-launch \
  > "$LOG_FILE" 2>&1 &

for _ in $(seq 1 45); do
  if curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
  echo "[error] ComfyUI did not become ready on port ${SERVICE_PORT}" >&2
  tail -n 30 "$LOG_FILE" >&2 || true
  exit 1
fi

# Always reclaim previous cloudflared tunnel before optionally starting a new one
pkill -f "cloudflared tunnel --url http://127.0.0.1:${SERVICE_PORT}" 2>/dev/null || true
PUBLIC_URL=""
if [ "$ENABLE_CLOUDFLARED" = "1" ]; then
  if ! command -v cloudflared >/dev/null 2>&1; then
    curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o /usr/local/bin/cloudflared
    chmod +x /usr/local/bin/cloudflared
  fi
  nohup cloudflared tunnel --url "http://127.0.0.1:${SERVICE_PORT}" --no-autoupdate > "$CF_LOG_FILE" 2>&1 &
  for _ in $(seq 1 20); do
    PUBLIC_URL="$(grep -Eo 'https://[a-zA-Z0-9.-]+\.trycloudflare\.com' "$CF_LOG_FILE" | head -n 1 || true)"
    if [ -n "$PUBLIC_URL" ]; then
      break
    fi
    sleep 1
  done
fi

SAVE_NOTE="成片暂存云端"
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  SAVE_NOTE="成片直存 Drive · $(basename "$OUTPUT_DIR")"
fi
write_status "画布已就绪" "待命" "0 排队" "1.0" "unset" "ComfyUI :${SERVICE_PORT} · ${SAVE_NOTE}" "$PUBLIC_URL"
echo "WORKLOAD_NAME=${WORKLOAD_NAME}"
if [ -n "$PUBLIC_URL" ]; then
  echo "PUBLIC_URL=${PUBLIC_URL}"
fi
echo "WORKLOAD_READY=1"
