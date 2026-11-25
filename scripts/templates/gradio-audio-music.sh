#!/usr/bin/env bash
# Colab Tools Starter Template — Gradio Audio / Speech / Music Studio
# Compatible with CosyVoice, Fish Speech, F5-TTS, Whisper WebUI, ACE-Step, YuE, etc.
# Copy to Google Drive (e.g. /content/drive/MyDrive/colab-tools/gradio-audio-music.sh),
# set LAUNCH_CMD to your model's Gradio entrypoint, and choose "音频 / 语音" or "音乐生成".
set -euo pipefail

SERVICE_PORT="${SERVICE_PORT:-7860}"
WORKLOAD_KIND="${WORKLOAD_KIND:-music}"
WORKLOAD_NAME="${WORKLOAD_NAME:-Gradio Audio/Music Studio}"
APP_DIR="${APP_DIR:-/content/audio-studio}"
LAUNCH_CMD="${LAUNCH_CMD:-python3 app.py --server-name 127.0.0.1 --server-port ${SERVICE_PORT}}"
STATUS_FILE="/tmp/colab-tools-status.json"
LOG_FILE="/tmp/gradio-workload.log"

write_status() {
  local ui_url="${1:-}"
  python3 - "$STATUS_FILE" "$WORKLOAD_KIND" "$WORKLOAD_NAME" "$ui_url" "$SERVICE_PORT" <<'PY'
import json, pathlib, sys
dst, kind, name, ui_url, port_s = sys.argv[1:]
port = int(port_s)
if not (1024 <= port <= 65535):
    raise SystemExit(f"Invalid port: {port}")
payload = {
    "kind": kind,
    "name": name,
    "ui_url": ui_url,
    "port": port,
    "detail": f"Gradio :{port} · GPU+CPU Watchdog",
}
tmp = pathlib.Path(dst + ".tmp")
tmp.write_text(json.dumps(payload, ensure_ascii=False))
tmp.replace(dst)
PY
}

if [ ! -d "$APP_DIR" ]; then
  echo "[error] APP_DIR does not exist: $APP_DIR (customize APP_DIR and LAUNCH_CMD in this template)" >&2
  exit 1
fi

cd "$APP_DIR"
if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/" >/dev/null 2>&1; then
  nohup bash -c "$LAUNCH_CMD" > "$LOG_FILE" 2>&1 &
fi

for _ in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:${SERVICE_PORT}/" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/" >/dev/null 2>&1; then
  echo "[error] Gradio service failed to start on port ${SERVICE_PORT}:"
  tail -n 30 "$LOG_FILE" || true
  exit 1
fi

# Extract optional Gradio share URL if enabled, otherwise connect securely via Colab Tools SSH Tunnel
PUBLIC_URL="$(grep -Eo 'https://[a-z0-9-]+\.gradio\.live' "$LOG_FILE" | tail -n 1 || true)"
write_status "$PUBLIC_URL"

echo "WORKLOAD_NAME=${WORKLOAD_NAME}"
if [ -n "$PUBLIC_URL" ]; then
  echo "PUBLIC_URL=${PUBLIC_URL}"
fi
echo "WORKLOAD_READY=1"
