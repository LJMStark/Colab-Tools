#!/usr/bin/env bash
# Colab Tools 通用远端自适应启动脚本（存放于 Google Drive: MyDrive/colab-llm/scripts/start_server.sh）
# 安全与健壮特性：
#   - flock 独占锁防止并发启动互相覆盖
#   - PID 文件精准管理，仅停止本脚本启动的 llama-server / cloudflared 进程
#   - 独立 mktemp 临时目录 + trap 清理；缓存压缩包原子替换
#   - 脱敏写入 /tmp/llama-server.meta，不在 stdout 打印明文 API_KEY
#   - 明确校验公网 URL 就绪后输出 SERVER_READY=1，否则返回非零退出码
set -euo pipefail

LOCK_FILE="/tmp/colab-tools-llm.lock"
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  echo "[ERROR] 已有另一个启动进程正在运行" >&2
  exit 1
fi

DRIVE_ROOT="${DRIVE_ROOT:-/content/drive/MyDrive/colab-llm}"
BIN_DIR="/content/bin"
MODEL_DIR="/content/models"
HF_REPO="${HF_REPO:-huihui-ai/Huihui-Qwen3.8-27B-abliterated-GGUF}"
LLAMA_TAG="${LLAMA_TAG:-b12676}"
CLOUDFLARED_VER="${CLOUDFLARED_VER:-2026.11.2}"
PORT="${LLAMA_PORT:-18080}"
ALIAS="${MODEL_ALIAS:-qwen3.8-27b-uncensored}"

LLAMA_PID_FILE="/tmp/colab-tools-llama.pid"
TUNNEL_PID_FILE="/tmp/colab-tools-cloudflared.pid"
WORK_TMP="$(mktemp -d /tmp/colab-tools-work.XXXXXX)"
trap 'rm -rf "$WORK_TMP"' EXIT

mkdir -p "$BIN_DIR" "$MODEL_DIR" "$DRIVE_ROOT/bin" "$DRIVE_ROOT/models" "$DRIVE_ROOT/scripts"

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

BUNDLE="$DRIVE_ROOT/bin/llama-${LLAMA_TAG}-cuda128-bundle.tar.gz"
if [ ! -x "$BIN_DIR/llama-server" ] || [ ! -x "$BIN_DIR/cloudflared" ]; then
  if [ -f "$BUNDLE" ] && tar -tzf "$BUNDLE" >/dev/null 2>&1; then
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
    # Atomic bundle persistence to Drive
    tar -czf "$WORK_TMP/bundle.tar.gz" -C "$BIN_DIR" .
    tar -tzf "$WORK_TMP/bundle.tar.gz" >/dev/null
    mv "$WORK_TMP/bundle.tar.gz" "${BUNDLE}.tmp" && mv "${BUNDLE}.tmp" "$BUNDLE"
  fi
  chmod +x "$BIN_DIR/llama-server" "$BIN_DIR/cloudflared"
fi

export LD_LIBRARY_PATH="$BIN_DIR:/usr/lib64-nvidia:/usr/local/nvidia/lib64:/usr/local/cuda/lib64:${LD_LIBRARY_PATH:-}"

GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader | head -1)
VRAM_MB=$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits | head -1 | tr -d " ")
echo "[gpu] Detected: $GPU_NAME (${VRAM_MB} MiB)"

if [ -n "${FORCE_MODEL_FILE:-}" ]; then
  MODEL_FILE="$FORCE_MODEL_FILE"
  CTX_CANDIDATES="${FORCE_CTX:-262144:q8_0 131072:q8_0}"
elif [ "$VRAM_MB" -ge 36000 ]; then
  MODEL_FILE="Huihui-Qwen3.8-27B-abliterated-Q8_0.gguf"
  CTX_CANDIDATES="262144:q8_0 131072:q8_0 65536:q8_0"
elif [ "$VRAM_MB" -ge 20000 ]; then
  MODEL_FILE="Huihui-Qwen3.8-27B-abliterated-UD-Q4_K_XL.gguf"
  CTX_CANDIDATES="262144:q8_0 262144:q4_0 131072:q8_0 65536:q8_0"
else
  MODEL_FILE="Huihui-Qwen3.8-27B-abliterated-UD-IQ4_XS.gguf"
  CTX_CANDIDATES="32768:q4_0 16384:q8_0"
fi

LOCAL_MODEL="$MODEL_DIR/$MODEL_FILE"
DRIVE_MODEL="$DRIVE_ROOT/models/$MODEL_FILE"
if [ ! -f "$LOCAL_MODEL" ]; then
  if [ -f "$DRIVE_MODEL" ]; then
    echo "[model] Copying $MODEL_FILE from Drive to local NVMe..."
    cp "$DRIVE_MODEL" "${LOCAL_MODEL}.tmp" && mv "${LOCAL_MODEL}.tmp" "$LOCAL_MODEL"
  else
    echo "[model] Downloading $MODEL_FILE from HuggingFace..."
    curl -fSL --retry 3 -o "${LOCAL_MODEL}.tmp" "https://huggingface.co/${HF_REPO}/resolve/main/${MODEL_FILE}"
    mv "${LOCAL_MODEL}.tmp" "$LOCAL_MODEL"
    (cp "$LOCAL_MODEL" "${DRIVE_MODEL}.tmp" && mv "${DRIVE_MODEL}.tmp" "$DRIVE_MODEL") >/tmp/drive-cache.log 2>&1 &
  fi
fi

KEY_FILE="$DRIVE_ROOT/api_key.txt"
if [ ! -s "$KEY_FILE" ]; then
  umask 077
  echo "sk-colab-$(openssl rand -hex 16)" > "${KEY_FILE}.tmp"
  mv "${KEY_FILE}.tmp" "$KEY_FILE"
  chmod 600 "$KEY_FILE"
fi
API_KEY=$(cat "$KEY_FILE")

stop_managed_pid "$LLAMA_PID_FILE" "llama-server"
stop_managed_pid "$TUNNEL_PID_FILE" "cloudflared"

TEMPLATE_ARG=()
if [ -f "$DRIVE_ROOT/scripts/qwen38-claude.jinja" ]; then
  TEMPLATE_ARG=(--chat-template-file "$DRIVE_ROOT/scripts/qwen38-claude.jinja")
fi

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
    > /tmp/llama-server.log 2>&1 < /dev/null &
  SERVER_PID=$!
  echo "$SERVER_PID" > "$LLAMA_PID_FILE"

  for i in $(seq 1 60); do
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
done

if [ "$READY" -ne 1 ]; then
  echo "[ERROR] llama-server failed to start" >&2
  tail -n 25 /tmp/llama-server.log >&2
  exit 1
fi

# Persist sanitized metadata for telemetry (never includes API_KEY)
cat > /tmp/llama-server.meta <<METAEOF
{"model":"${MODEL_FILE}","ctx":${CHOSEN_CTX},"kv":"${CHOSEN_KV}","alias":"${ALIAS}","port":${PORT}}
METAEOF

rm -f /tmp/cloudflared.log /tmp/public_url.txt
setsid "$BIN_DIR/cloudflared" tunnel --url "http://127.0.0.1:${PORT}" --no-autoupdate \
  > /tmp/cloudflared.log 2>&1 < /dev/null &
echo $! > "$TUNNEL_PID_FILE"

PUBLIC_URL=""
for i in $(seq 1 25); do
  PUBLIC_URL=$(grep -oE "https://[a-zA-Z0-9-]+\.trycloudflare\.com" /tmp/cloudflared.log | head -1 || true)
  if [ -n "$PUBLIC_URL" ]; then
    echo "$PUBLIC_URL" > /tmp/public_url.txt
    break
  fi
  sleep 1
done

if [ -z "$PUBLIC_URL" ]; then
  echo "[ERROR] cloudflared failed to issue a public HTTPS URL" >&2
  stop_managed_pid "$TUNNEL_PID_FILE" "cloudflared"
  exit 1
fi

echo "GPU=$GPU_NAME"
echo "VRAM=$(nvidia-smi --query-gpu=memory.used,memory.total --format=csv,noheader)"
echo "MODEL=$MODEL_FILE"
echo "ALIAS=$ALIAS"
echo "CTX=$CHOSEN_CTX"
echo "PUBLIC_URL=$PUBLIC_URL"
echo "SERVER_READY=1"
