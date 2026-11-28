#!/usr/bin/env bash
# Colab Tools Built-in & Reference Remote Script — OpenAI/Anthropic-Compatible LLM Proxy (llama.cpp + authenticated cloudflared)
# Works out of the box with zero manual Drive upload, and automatically caches binaries/models on Google Drive when mounted.
# Override any setting via env vars: HF_REPO, MODEL_ALIAS, FORCE_MODEL_FILE, FORCE_CTX, LLAMA_TAG, CLOUDFLARED_VER, LLAMA_PORT.
set -euo pipefail

LOCK_FILE="/tmp/colab-tools-llm.lock"
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  echo "[ERROR] 已有另一个大模型启动进程正在运行" >&2
  exit 1
fi

USE_GOOGLE_DRIVE="${USE_GOOGLE_DRIVE:-1}"
DRIVE_ROOT="${DRIVE_ROOT:-/content/drive/MyDrive/colab-llm}"
BIN_DIR="/content/bin"
MODEL_DIR="/content/models"
HF_REPO="${HF_REPO:-bartowski/Qwen2.5-14B-Instruct-GGUF}"
LLAMA_TAG="${LLAMA_TAG:-b12676}"
CLOUDFLARED_VER="${CLOUDFLARED_VER:-2026.11.2}"
PORT="${LLAMA_PORT:-18080}"
ALIAS="${MODEL_ALIAS:-qwen2.5-14b}"

STATUS_FILE="/tmp/colab-tools-status.json"
LLAMA_PID_FILE="/tmp/colab-tools-llama.pid"
TUNNEL_PID_FILE="/tmp/colab-tools-cloudflared.pid"
LOCAL_KEY_FILE="/tmp/colab-tools-api-key.txt"
WORK_TMP="$(mktemp -d /tmp/colab-tools-work.XXXXXX)"
CLEANUP_ON_FAIL=1
LAUNCHED_LLAMA_PID=""
LAUNCHED_CF_PID=""

stop_managed_pid() {
  local pid_file="$1"
  local expected_comm="$2"
  if [ -f "$pid_file" ]; then
    local pid
    pid=$(cat "$pid_file" 2>/dev/null || true)
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      local comm
      comm=$(ps -p "$pid" -o comm= 2>/dev/null | tr -d " " || true)
      if [ "$comm" = "$expected_comm" ]; then
        kill "$pid" 2>/dev/null || true
        for _ in 1 2 3 4 5; do
          kill -0 "$pid" 2>/dev/null || break
          sleep 0.5
        done
        kill -9 "$pid" 2>/dev/null || true
      fi
    fi
    rm -f "$pid_file"
  fi
}

cleanup_on_exit() {
  rm -rf "$WORK_TMP"
  if [ "$CLEANUP_ON_FAIL" = "1" ]; then
    if [ -n "$LAUNCHED_CF_PID" ]; then
      stop_managed_pid "$TUNNEL_PID_FILE" "cloudflared"
    fi
    if [ -n "$LAUNCHED_LLAMA_PID" ]; then
      stop_managed_pid "$LLAMA_PID_FILE" "llama-server"
    fi
  fi
}
trap cleanup_on_exit EXIT

DRIVE_AVAILABLE=0
if [ "$USE_GOOGLE_DRIVE" = "1" ] && [ -d "/content/drive/MyDrive" ]; then
  DRIVE_AVAILABLE=1
  mkdir -p "$DRIVE_ROOT/bin" "$DRIVE_ROOT/models" "$DRIVE_ROOT/scripts"
fi
mkdir -p "$BIN_DIR" "$MODEL_DIR"

# Gently stop any previously running ComfyUI process & workflow sync daemon when switching modes in-place (verify /proc/<pid>/cmdline)
if [ -f /tmp/colab-tools-wf-sync.pid ]; then
  old_wf_pid=$(cat /tmp/colab-tools-wf-sync.pid 2>/dev/null || true)
  if [ -n "$old_wf_pid" ] && [ -r "/proc/${old_wf_pid}/cmdline" ] && tr '\0' ' ' < "/proc/${old_wf_pid}/cmdline" | grep -q "colab-tools-wf-sync.py"; then
    kill "$old_wf_pid" 2>/dev/null || true
  fi
  rm -f /tmp/colab-tools-wf-sync.pid
fi
pkill -TERM -f '/content/ComfyUI/main.py' 2>/dev/null || true

write_status() {
  local label="$1"
  local val="$2"
  local unit="$3"
  local ratio="$4"
  local busy="$5"
  local detail="$6"
  local ui_url="${7:-}"
  python3 - "$STATUS_FILE" "$ALIAS" "$PORT" "$label" "$val" "$unit" "$ratio" "$busy" "$detail" "$ui_url" <<'PY' || true
import json, pathlib, sys
dst, name, port_s, label, val, unit, ratio_s, busy_s, detail, ui_url = sys.argv[1:]
payload = {
    "kind": "llm",
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

on_err() {
  local line="$1"
  write_status "部署中断" "失败" "第 ${line} 行" "0.0" "false" "请查看 /tmp/colab-tools-deploy.log" ""
}
trap 'on_err $LINENO' ERR

generate_api_key() {
  local rand_hex=""
  if command -v openssl >/dev/null 2>&1; then
    rand_hex="$(openssl rand -hex 24 2>/dev/null || true)"
  fi
  if [ "${#rand_hex}" -lt 32 ]; then
    rand_hex="$(python3 -c 'import secrets; print(secrets.token_hex(24))')"
  fi
  if [ "${#rand_hex}" -lt 32 ]; then
    echo "[ERROR] failed to generate cryptographically random API key" >&2
    exit 1
  fi
  printf 'sk-colab-%s\n' "$rand_hex"
}

write_status "准备运行环境" "20%" "llama.cpp" "0.20" "true" "准备推理引擎与安全通道…"
BUNDLE="$DRIVE_ROOT/bin/llama-${LLAMA_TAG}-cuda128-bundle.tar.gz"
if [ ! -x "$BIN_DIR/llama-server" ] || [ ! -x "$BIN_DIR/cloudflared" ]; then
  if [ "$DRIVE_AVAILABLE" = "1" ] && [ -f "$BUNDLE" ] && tar -tzf "$BUNDLE" >/dev/null 2>&1; then
    echo "[boot] Restoring verified binaries from Google Drive..."
    tar -xzf "$BUNDLE" -C "$BIN_DIR"
  else
    echo "[boot] Downloading llama.cpp ${LLAMA_TAG} CUDA 12.8 & cloudflared ${CLOUDFLARED_VER}..."
    mkdir -p "$WORK_TMP/extract"
    curl -fSL --retry 3 -o "$WORK_TMP/llama.tar.gz" \
      "https://github.com/ggml-org/llama.cpp/releases/download/${LLAMA_TAG}/llama-${LLAMA_TAG}-bin-ubuntu-cuda-12.8-x64.tar.gz"
    curl -fSL --retry 3 -o "$WORK_TMP/cudart.tar.gz" \
      "https://github.com/ggml-org/llama.cpp/releases/download/${LLAMA_TAG}/cudart-llama-${LLAMA_TAG}-bin-ubuntu-cuda-12.8-x64.tar.gz"
    tar -xzf "$WORK_TMP/llama.tar.gz" -C "$WORK_TMP/extract"
    tar -xzf "$WORK_TMP/cudart.tar.gz" -C "$WORK_TMP/extract"
    cp -a "$WORK_TMP/extract/llama-${LLAMA_TAG}/"* "$BIN_DIR/"
    cp -a "$WORK_TMP/extract/cudart-"*/* "$BIN_DIR/"
    curl -fSL --retry 3 -o "$WORK_TMP/cloudflared" \
      "https://github.com/cloudflare/cloudflared/releases/download/${CLOUDFLARED_VER}/cloudflared-linux-amd64"
    mv "$WORK_TMP/cloudflared" "$BIN_DIR/cloudflared"
    chmod +x "$BIN_DIR/llama-server" "$BIN_DIR/cloudflared"
    if [ "$DRIVE_AVAILABLE" = "1" ]; then
      tar -czf "$WORK_TMP/bundle.tar.gz" -C "$BIN_DIR" .
      tar -tzf "$WORK_TMP/bundle.tar.gz" >/dev/null
      mv "$WORK_TMP/bundle.tar.gz" "${BUNDLE}.tmp" && mv "${BUNDLE}.tmp" "$BUNDLE"
    fi
  fi
  chmod +x "$BIN_DIR/llama-server" "$BIN_DIR/cloudflared"
fi

export LD_LIBRARY_PATH="$BIN_DIR:/usr/lib64-nvidia:/usr/local/nvidia/lib64:/usr/local/cuda/lib64:${LD_LIBRARY_PATH:-}"

GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader | head -1)
VRAM_MB=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits | head -1 | tr -d " ")
echo "[gpu] Detected: $GPU_NAME (${VRAM_MB} MiB)"

# Auto-pick single-file GGUF matching GPU VRAM when FORCE_MODEL_FILE is not explicitly provided
if [ -n "${FORCE_MODEL_FILE:-}" ]; then
  MODEL_FILE="$FORCE_MODEL_FILE"
else
  MODEL_FILE="$(python3 - "$HF_REPO" "$VRAM_MB" <<'PY'
import json, re, sys, urllib.request
repo, vram_s = sys.argv[1], sys.argv[2]
vram = int(vram_s or "16000")
url = f"https://huggingface.co/api/models/{repo}"
req = urllib.request.Request(url, headers={"User-Agent": "Colab-Tools/0.3"})
with urllib.request.urlopen(req, timeout=20) as resp:
    doc = json.loads(resp.read().decode("utf-8"))
files = [
    s.get("rfilename", "")
    for s in doc.get("siblings", [])
    if isinstance(s, dict)
    and str(s.get("rfilename", "")).lower().endswith(".gguf")
    and "mmproj" not in str(s.get("rfilename", "")).lower()
    and not re.search(r"-\d{5}-of-\d{5}\.gguf$", str(s.get("rfilename", "")), re.I)
]
if not files:
    raise SystemExit("no single-file .gguf found in repo " + repo)
if vram >= 36000:
    prefs = ("q8_0", "q6_k_l", "q6_k", "q5_k_m", "q4_k_m")
elif vram >= 20000:
    prefs = ("q6_k", "q5_k_m", "q4_k_xl", "q4_k_m", "q4_k_s", "iq4_xs")
else:
    prefs = ("q4_k_m", "iq4_xs", "q4_k_s", "q3_k_m")
chosen = ""
for p in prefs:
    for f in files:
        if p in f.lower():
            chosen = f
            break
    if chosen:
        break
print(chosen or files[0])
PY
)"
fi

if [ -n "${FORCE_CTX:-}" ]; then
  CTX_CANDIDATES="$FORCE_CTX"
elif [ "$VRAM_MB" -ge 36000 ]; then
  CTX_CANDIDATES="131072:q8_0 65536:q8_0 32768:q8_0"
elif [ "$VRAM_MB" -ge 20000 ]; then
  CTX_CANDIDATES="65536:q8_0 65536:q4_0 32768:q8_0"
else
  CTX_CANDIDATES="32768:q4_0 16384:q8_0"
fi

write_status "拉取模型权重" "50%" "$ALIAS" "0.50" "true" "准备 ${MODEL_FILE}…"
MODEL_BASENAME="$(basename "$MODEL_FILE")"
LOCAL_MODEL="$MODEL_DIR/$MODEL_BASENAME"
DRIVE_MODEL="$DRIVE_ROOT/models/$MODEL_BASENAME"
if [ ! -f "$LOCAL_MODEL" ]; then
  if [ "$DRIVE_AVAILABLE" = "1" ] && [ -f "$DRIVE_MODEL" ]; then
    echo "[model] Copying $MODEL_BASENAME from Drive to local NVMe..."
    cp "$DRIVE_MODEL" "${LOCAL_MODEL}.tmp" && mv "${LOCAL_MODEL}.tmp" "$LOCAL_MODEL"
  else
    echo "[model] Downloading $MODEL_FILE from HuggingFace ($HF_REPO)..."
    curl -fSL --retry 3 -o "${LOCAL_MODEL}.tmp" "https://huggingface.co/${HF_REPO}/resolve/main/${MODEL_FILE}"
    mv "${LOCAL_MODEL}.tmp" "$LOCAL_MODEL"
    if [ "$DRIVE_AVAILABLE" = "1" ]; then
      (cp "$LOCAL_MODEL" "${DRIVE_MODEL}.tmp" && mv "${DRIVE_MODEL}.tmp" "$DRIVE_MODEL") 9>&- >/tmp/drive-cache.log 2>&1 &
    fi
  fi
fi

# Generate or reuse bearer API key (persisted on Drive when mounted, mirrored to /tmp with 0600 for fast local fetch)
umask 077
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  KEY_FILE="$DRIVE_ROOT/api_key.txt"
  if [ ! -s "$KEY_FILE" ]; then
    generate_api_key > "${KEY_FILE}.tmp"
    mv "${KEY_FILE}.tmp" "$KEY_FILE"
    chmod 600 "$KEY_FILE"
  fi
  cp "$KEY_FILE" "${LOCAL_KEY_FILE}.tmp" && mv "${LOCAL_KEY_FILE}.tmp" "$LOCAL_KEY_FILE"
  chmod 600 "$LOCAL_KEY_FILE"
else
  if [ ! -s "$LOCAL_KEY_FILE" ]; then
    generate_api_key > "${LOCAL_KEY_FILE}.tmp"
    mv "${LOCAL_KEY_FILE}.tmp" "$LOCAL_KEY_FILE"
    chmod 600 "$LOCAL_KEY_FILE"
  fi
fi
API_KEY="$(tr -d '[:space:]' < "$LOCAL_KEY_FILE")"
if [ "${#API_KEY}" -lt 24 ]; then
  echo "[ERROR] invalid API key length" >&2
  exit 1
fi

stop_managed_pid "$LLAMA_PID_FILE" "llama-server"
stop_managed_pid "$TUNNEL_PID_FILE" "cloudflared"

TEMPLATE_ARG=()
if [ -n "${CHAT_TEMPLATE_FILE:-}" ] && [ -f "$CHAT_TEMPLATE_FILE" ]; then
  TEMPLATE_ARG=(--chat-template-file "$CHAT_TEMPLATE_FILE")
fi

write_status "启动模型服务" "80%" "llama-server" "0.80" "true" "正在加载显存与 KV 缓存…"
READY=0
CHOSEN_CTX=0
CHOSEN_KV="q8_0"
for SPEC in $CTX_CANDIDATES; do
  CTX="${SPEC%%:*}"
  KVQ="${SPEC##*:}"
  [ "$KVQ" = "$CTX" ] && KVQ="q8_0"
  echo "[llama] Launching ctx=$CTX kv=$KVQ ..."
  rm -f /tmp/llama-server.log
  setsid env LD_LIBRARY_PATH="$LD_LIBRARY_PATH" \
    "$BIN_DIR/llama-server" \
    -m "$LOCAL_MODEL" \
    --alias "$ALIAS" \
    --host 127.0.0.1 --port "$PORT" \
    --api-key "$API_KEY" \
    -ngl 99 -fa on --jinja "${TEMPLATE_ARG[@]}" \
    -c "$CTX" -np 1 -ctk "$KVQ" -ctv "$KVQ" \
    -b 2048 -ub 512 \
    9>&- > /tmp/llama-server.log 2>&1 < /dev/null &
  SERVER_PID=$!
  LAUNCHED_LLAMA_PID="$SERVER_PID"
  echo "$SERVER_PID" > "$LLAMA_PID_FILE"

  for _ in $(seq 1 60); do
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      break
    fi
    if curl -sf "http://127.0.0.1:${PORT}/health" | grep -q "ok"; then
      READY=1
      CHOSEN_CTX="$CTX"
      CHOSEN_KV="$KVQ"
      break 2
    fi
    sleep 2
  done
  stop_managed_pid "$LLAMA_PID_FILE" "llama-server"
  LAUNCHED_LLAMA_PID=""
done

if [ "$READY" -ne 1 ]; then
  echo "[ERROR] llama-server failed to start" >&2
  tail -n 25 /tmp/llama-server.log >&2
  write_status "部署中断" "失败" "llama-server" "0.0" "false" "模型启动失败，请查看日志" ""
  exit 1
fi

# Persist sanitized metadata for telemetry using json.dumps (never includes API_KEY)
python3 - "/tmp/llama-server.meta" "$MODEL_BASENAME" "$CHOSEN_CTX" "$CHOSEN_KV" "$ALIAS" "$PORT" <<'PY'
import json, pathlib, sys
dst, model, ctx_s, kv, alias, port_s = sys.argv[1:]
payload = {"model": model, "ctx": int(ctx_s), "kv": kv, "alias": alias, "port": int(port_s)}
tmp = pathlib.Path(dst + ".tmp")
tmp.write_text(json.dumps(payload, ensure_ascii=False))
tmp.replace(dst)
PY

rm -f /tmp/cloudflared.log /tmp/public_url.txt
setsid "$BIN_DIR/cloudflared" tunnel --url "http://127.0.0.1:${PORT}" --no-autoupdate \
  9>&- > /tmp/cloudflared.log 2>&1 < /dev/null &
LAUNCHED_CF_PID=$!
echo "$LAUNCHED_CF_PID" > "$TUNNEL_PID_FILE"

PUBLIC_URL=""
for _ in $(seq 1 25); do
  PUBLIC_URL=$(grep -oE "https://[a-zA-Z0-9-]+\.trycloudflare\.com" /tmp/cloudflared.log | head -1 || true)
  if [ -n "$PUBLIC_URL" ]; then
    echo "$PUBLIC_URL" > /tmp/public_url.txt
    break
  fi
  sleep 1
done

if [ -z "$PUBLIC_URL" ]; then
  echo "[ERROR] cloudflared failed to issue a public HTTPS URL" >&2
  write_status "部署中断" "失败" "代理隧道" "0.0" "false" "未能生成公网 HTTPS 代理地址" ""
  exit 1
fi

CLEANUP_ON_FAIL=0
write_status "大模型已就绪" "在线" "${ALIAS}" "1.0" "unset" "API Key 鉴权保护 · ${MODEL_BASENAME}" "$PUBLIC_URL"
echo "GPU=$GPU_NAME"
echo "VRAM=$(nvidia-smi --query-gpu=memory.used,memory.total --format=csv,noheader)"
echo "MODEL=$MODEL_BASENAME"
echo "ALIAS=$ALIAS"
echo "CTX=$CHOSEN_CTX"
echo "PUBLIC_URL=$PUBLIC_URL"
echo "SERVER_READY=1"
