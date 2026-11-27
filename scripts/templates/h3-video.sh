#!/usr/bin/env bash
# Colab Tools Built-in Deploy Script — MiniMax H3 / HyperFlow H3 Flash Video Studio (ComfyUI)
# Automatically uploaded & launched by Colab Tools (zero manual Drive upload required).
# Supports: original (31B), flash (13.7B INT8 fast), flash_gguf (24GB VRAM), custom HF / Drive-only.
set -euo pipefail

SERVICE_PORT="${SERVICE_PORT:-8188}"
WORKLOAD_KIND="video"
H3_VARIANT="${H3_VARIANT:-flash}"                 # flash | original | flash_gguf | custom
H3_GGUF_QUANT="${H3_GGUF_QUANT:-Q4_K_M}"          # Q4_K_M | Q6_K | Q8_0
H3_CUSTOM_HF_REPO="${H3_CUSTOM_HF_REPO:-}"
H3_CUSTOM_HF_FILES="${H3_CUSTOM_HF_FILES:-}"
H3_DRIVE_ONLY="${H3_DRIVE_ONLY:-0}"
H3_ENABLE_SOL_ATTN="${H3_ENABLE_SOL_ATTN:-1}"
USE_GOOGLE_DRIVE="${USE_GOOGLE_DRIVE:-1}"
DRIVE_MODELS_DIR="${DRIVE_MODELS_DIR:-}"
DRIVE_OUTPUT_DIR="${DRIVE_OUTPUT_DIR:-/content/drive/MyDrive/ColabTools/outputs}"
DRIVE_WHEELS_DIR="${DRIVE_WHEELS_DIR:-/content/drive/MyDrive/ColabTools/wheels}"
CACHE_BASE_TO_DRIVE="${CACHE_BASE_TO_DRIVE:-1}"
ENABLE_CLOUDFLARED="${ENABLE_CLOUDFLARED:-0}"
RESTORE_CUSTOM_NODES_SNAPSHOT="${RESTORE_CUSTOM_NODES_SNAPSHOT:-0}"

COMFY_DIR="${COMFY_DIR:-/content/ComfyUI}"
STATUS_FILE="/tmp/colab-tools-status.json"
LOG_FILE="/tmp/comfyui.log"
CF_LOG_FILE="/tmp/cloudflared-video.log"
DRIVE_WORKSPACE_ROOT="/content/drive/MyDrive/ColabTools"
DRIVE_WORKFLOWS_DIR="${DRIVE_WORKSPACE_ROOT}/workflows"
DRIVE_SNAPSHOTS_DIR="${DRIVE_WORKSPACE_ROOT}/snapshots"
DRIVE_EXTRA_MODELS_DIR="${DRIVE_WORKSPACE_ROOT}/models"

export HF_XET_HIGH_PERFORMANCE=1
export HF_HUB_ENABLE_HF_TRANSFER=1

# Only respect H3_DRIVE_ONLY in custom variant
if [ "$H3_VARIANT" != "custom" ]; then
  H3_DRIVE_ONLY="0"
fi

case "$H3_VARIANT" in
  original)   WORKLOAD_NAME="H3 原版 (31B)" ;;
  flash_gguf) WORKLOAD_NAME="H3 Flash 轻量 (${H3_GGUF_QUANT})" ;;
  custom)     WORKLOAD_NAME="H3 自定义模型" ;;
  *)          WORKLOAD_NAME="H3 Flash 极速版" ;;
esac

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

fail_deploy() {
  local reason="$1"
  echo "[error] $reason" >&2
  write_status "部署中断" "失败" "请检查" "0.0" "false" "$reason" ""
  exit 1
}

on_err() {
  local line="$1"
  write_status "部署中断" "失败" "第 ${line} 行" "0.0" "false" "请查看 /tmp/colab-tools-deploy.log" ""
}
trap 'on_err $LINENO' ERR

# 1. Resolve Google Drive paths (auto-detect both standard ColabTools & classic ComfyUI_Models layouts)
DRIVE_AVAILABLE=0
if [ "$USE_GOOGLE_DRIVE" = "1" ] && [ -d "/content/drive/MyDrive" ]; then
  DRIVE_AVAILABLE=1
  if [ -n "$DRIVE_MODELS_DIR" ] && [ "${DRIVE_MODELS_DIR%/MiniMax_H3}" = "$DRIVE_MODELS_DIR" ]; then
    DRIVE_EXTRA_MODELS_DIR="$DRIVE_MODELS_DIR"
  fi
  if [ -z "$DRIVE_MODELS_DIR" ]; then
    if [ -d "${DRIVE_WORKSPACE_ROOT}/models/MiniMax_H3" ]; then
      DRIVE_MODELS_DIR="${DRIVE_WORKSPACE_ROOT}/models/MiniMax_H3"
    elif [ -d "/content/drive/MyDrive/ComfyUI_Models/MiniMax_H3" ]; then
      DRIVE_MODELS_DIR="/content/drive/MyDrive/ComfyUI_Models/MiniMax_H3"
    else
      DRIVE_MODELS_DIR="${DRIVE_WORKSPACE_ROOT}/models/MiniMax_H3"
    fi
  fi
  OUTPUT_DIR="$DRIVE_OUTPUT_DIR"
  mkdir -p \
    "$OUTPUT_DIR" \
    "$DRIVE_WORKFLOWS_DIR" \
    "$DRIVE_SNAPSHOTS_DIR" \
    "$DRIVE_MODELS_DIR/text_encoders" \
    "$DRIVE_MODELS_DIR/vae" \
    "$DRIVE_MODELS_DIR/diffusion_models" \
    "$DRIVE_EXTRA_MODELS_DIR/checkpoints" \
    "$DRIVE_EXTRA_MODELS_DIR/diffusion_models" \
    "$DRIVE_EXTRA_MODELS_DIR/text_encoders" \
    "$DRIVE_EXTRA_MODELS_DIR/vae" \
    "$DRIVE_EXTRA_MODELS_DIR/loras" \
    "$DRIVE_EXTRA_MODELS_DIR/clip_vision" \
    "$DRIVE_EXTRA_MODELS_DIR/controlnet" \
    "$DRIVE_EXTRA_MODELS_DIR/upscale_models" \
    "$DRIVE_EXTRA_MODELS_DIR/embeddings"
else
  OUTPUT_DIR="/content/output"
  mkdir -p "$OUTPUT_DIR"
fi

if [ "$H3_DRIVE_ONLY" = "1" ] && [ "$DRIVE_AVAILABLE" != "1" ]; then
  fail_deploy "仅用 Drive 模式需要先开启并授权 Google Drive"
fi

# 2. Install ComfyUI, H3 custom nodes, Manager v4 & Workflow-Models-Downloader onto local NVMe
write_status "正在部署" "15%" "装环境" "0.15" "true" "安装 ComfyUI、Manager 与补模插件…"
if [ ! -d "$COMFY_DIR" ]; then
  git clone --depth 1 https://github.com/comfyanonymous/ComfyUI.git "$COMFY_DIR"
  pip install -q -r "$COMFY_DIR/requirements.txt"
fi
pip install -q -U 'huggingface_hub[hf_xet]' hf_transfer safetensors gguf sentencepiece protobuf imageio-ffmpeg

mkdir -p "$COMFY_DIR/custom_nodes" "$COMFY_DIR/models/text_encoders" "$COMFY_DIR/models/vae" "$COMFY_DIR/models/diffusion_models" "$COMFY_DIR/user/default/workflows" "$COMFY_DIR/user/default/workflows/examples"

# Official ComfyUI-Manager v4 (pip package + --enable-manager, best-effort)
if [ -f "$COMFY_DIR/manager_requirements.txt" ]; then
  pip install -q -r "$COMFY_DIR/manager_requirements.txt" || true
else
  pip install -q comfyui-manager || true
fi

# Configure Manager cloud permissions gently without wiping existing user settings
python3 - "$COMFY_DIR" <<'PY' || true
import configparser, pathlib, sys
comfy_dir = pathlib.Path(sys.argv[1])
for rel in ("user/default/ComfyUI-Manager/config.ini", "user/__manager/config.ini"):
    cfg_path = comfy_dir / rel
    cfg_path.parent.mkdir(parents=True, exist_ok=True)
    cp = configparser.ConfigParser()
    if cfg_path.exists():
        try:
            cp.read(cfg_path, encoding="utf-8")
        except Exception:
            pass
    if "default" not in cp:
        cp["default"] = {}
    cp["default"]["security_level"] = "normal"
    cp["default"]["network_mode"] = "personal_cloud"
    with cfg_path.open("w", encoding="utf-8") as fh:
        cp.write(fh)
PY

# Required nodes for Flash/INT8 fail fast on clone failure; optional nodes for custom best-effort
if [ "$H3_VARIANT" = "flash" ] || [ "$H3_VARIANT" = "flash_gguf" ]; then
  if [ ! -d "$COMFY_DIR/custom_nodes/ComfyUI-HyperFlow-Flash" ]; then
    git clone --depth 1 https://github.com/Video-Rebirth/ComfyUI-HyperFlow-Flash.git "$COMFY_DIR/custom_nodes/ComfyUI-HyperFlow-Flash" \
      || fail_deploy "安装必需节点 ComfyUI-HyperFlow-Flash 失败"
  fi
  if [ "$H3_VARIANT" = "flash" ] && [ ! -d "$COMFY_DIR/custom_nodes/comfyui_hyperflow_int8_nodes" ]; then
    git clone --depth 1 https://github.com/BennyDaBall930/comfyui_hyperflow_int8_nodes.git "$COMFY_DIR/custom_nodes/comfyui_hyperflow_int8_nodes" \
      || fail_deploy "安装必需节点 comfyui_hyperflow_int8_nodes 失败"
  fi
elif [ "$H3_VARIANT" = "custom" ]; then
  if [ ! -d "$COMFY_DIR/custom_nodes/ComfyUI-HyperFlow-Flash" ]; then
    git clone --depth 1 https://github.com/Video-Rebirth/ComfyUI-HyperFlow-Flash.git "$COMFY_DIR/custom_nodes/ComfyUI-HyperFlow-Flash" || true
  fi
  if [ ! -d "$COMFY_DIR/custom_nodes/comfyui_hyperflow_int8_nodes" ]; then
    git clone --depth 1 https://github.com/BennyDaBall930/comfyui_hyperflow_int8_nodes.git "$COMFY_DIR/custom_nodes/comfyui_hyperflow_int8_nodes" || true
  fi
fi

# Server-side workflow missing-model downloader (downloads straight to VM NVMe, never browser)
if [ ! -d "$COMFY_DIR/custom_nodes/ComfyUI-Workflow-Models-Downloader" ]; then
  git clone --depth 1 https://github.com/slahiri/ComfyUI-Workflow-Models-Downloader.git "$COMFY_DIR/custom_nodes/ComfyUI-Workflow-Models-Downloader" || true
fi
if [ -f "$COMFY_DIR/custom_nodes/ComfyUI-Workflow-Models-Downloader/requirements.txt" ]; then
  pip install -q -r "$COMFY_DIR/custom_nodes/ComfyUI-Workflow-Models-Downloader/requirements.txt" || true
fi

# Optional custom node snapshot restore (only when enabled in Settings -> Advanced)
if [ "$RESTORE_CUSTOM_NODES_SNAPSHOT" = "1" ] && [ "$DRIVE_AVAILABLE" = "1" ] && [ -f "$DRIVE_SNAPSHOTS_DIR/custom_nodes.json" ]; then
  echo "[snapshot] Restoring custom nodes from $DRIVE_SNAPSHOTS_DIR/custom_nodes.json"
  python3 - "$COMFY_DIR/custom_nodes" "$DRIVE_SNAPSHOTS_DIR/custom_nodes.json" <<'PY' || true
import json, pathlib, re, subprocess, sys
nodes_root = pathlib.Path(sys.argv[1])
snap_path = pathlib.Path(sys.argv[2])
try:
    doc = json.loads(snap_path.read_text(encoding="utf-8"))
except Exception as exc:
    print(f"[snapshot] skip unreadable snapshot: {exc}")
    raise SystemExit(0)
for item in doc.get("nodes", []):
    if not isinstance(item, dict):
        continue
    d = str(item.get("dir", "")).strip()
    u = str(item.get("url", "")).strip()
    if not d or "/" in d or "\\" in d or ".." in d or not re.match(r"^[A-Za-z0-9._-]+$", d):
        continue
    if not u.startswith("https://") or any(ch.isspace() for ch in u):
        continue
    target = nodes_root / d
    if target.exists():
        continue
    print(f"[snapshot] cloning {d} from {u}")
    r = subprocess.run(["git", "clone", "--depth", "1", u, str(target)], check=False)
    if r.returncode != 0:
        print(f"[snapshot] warn: failed to clone {d}, skipping")
        continue
    req = target / "requirements.txt"
    if req.is_file():
        subprocess.run([sys.executable, "-m", "pip", "install", "-q", "-r", str(req)], check=False)
PY
fi

# Zero-copy mount user's Google Drive models/ directory via extra_model_paths.yaml
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  python3 - "$COMFY_DIR/extra_model_paths.yaml" "$DRIVE_EXTRA_MODELS_DIR" <<'PY' || true
import json, pathlib, sys
yaml_path = pathlib.Path(sys.argv[1])
base_dir = sys.argv[2].rstrip("/") + "/"
base_q = json.dumps(base_dir)
content = f"""# Auto-generated by Colab Tools (zero-copy Google Drive model mount)
colab_drive_models:
  base_path: {base_q}
  checkpoints: checkpoints/
  diffusion_models: diffusion_models/
  unet: diffusion_models/
  text_encoders: text_encoders/
  clip: text_encoders/
  vae: vae/
  loras: loras/
  clip_vision: clip_vision/
  controlnet: controlnet/
  upscale_models: upscale_models/
  embeddings: embeddings/
"""
yaml_path.write_text(content, encoding="utf-8")
PY
fi

# Optional sol-attn acceleration (prefer cached prebuilt wheels from Drive to skip 10m compile)
if [ "$H3_ENABLE_SOL_ATTN" = "1" ]; then
  if [ "$DRIVE_AVAILABLE" = "1" ] && [ -d "$DRIVE_WHEELS_DIR" ] && ls "$DRIVE_WHEELS_DIR"/*.whl >/dev/null 2>&1; then
    pip install -q --no-index --find-links "$DRIVE_WHEELS_DIR" "$DRIVE_WHEELS_DIR"/*.whl || true
  fi
fi

# Helper: fetch a file by checking Drive first (rsync sequential copy to NVMe, NEVER symlink FUSE!), else HF fast download
sync_or_download() {
  local sub_dir="$1"     # text_encoders | vae | diffusion_models
  local filename="$2"
  local hf_repo="$3"
  local hf_path="$4"
  local cache_back="$5"  # 1 | 0

  local local_dst="$COMFY_DIR/models/${sub_dir}/${filename}"
  if [ -f "$local_dst" ] && [ -s "$local_dst" ]; then
    echo "[ok] Already on NVMe: $filename"
    return 0
  fi

  local drive_src=""
  if [ "$DRIVE_AVAILABLE" = "1" ]; then
    for cand in \
      "$DRIVE_MODELS_DIR/${sub_dir}/${filename}" \
      "$DRIVE_MODELS_DIR/${filename}" \
      "$DRIVE_EXTRA_MODELS_DIR/${sub_dir}/${filename}" \
      "/content/drive/MyDrive/ComfyUI_Models/MiniMax_H3/${sub_dir}/${filename}" \
      "/content/drive/MyDrive/ColabTools/models/MiniMax_H3/${sub_dir}/${filename}"; do
      if [ -f "$cand" ] && [ -s "$cand" ]; then
        drive_src="$cand"
        break
      fi
    done
  fi

  if [ -n "$drive_src" ]; then
    echo "[rsync] Drive -> NVMe: $filename"
    local part_dst="${local_dst}.part.$$"
    rm -f "$part_dst"
    if ! rsync -ahW "$drive_src" "$part_dst"; then
      rm -f "$part_dst"
      fail_deploy "从 Google Drive 拷贝模型失败：${filename}"
    fi
    python3 - "$drive_src" "$part_dst" "$local_dst" <<'PY'
import pathlib, sys
src_p, part_p, dst_p = (pathlib.Path(p) for p in sys.argv[1:])
src_sz = src_p.stat().st_size
part_sz = part_p.stat().st_size if part_p.exists() else 0
if src_sz <= 0 or part_sz != src_sz:
    part_p.unlink(missing_ok=True)
    raise SystemExit(f"size mismatch copying {dst_p.name}: expected {src_sz}, got {part_sz}")
part_p.replace(dst_p)
PY
    return 0
  fi

  if [ "$H3_DRIVE_ONLY" = "1" ]; then
    fail_deploy "仅用 Drive 模式缺少必需权重：${sub_dir}/${filename}"
  fi
  if [ -z "$hf_repo" ]; then
    fail_deploy "缺少模型下载地址：${filename}"
  fi

  echo "[hf] Fast downloading $hf_repo / $hf_path -> NVMe (zero double-copy)"
  python3 - "$hf_repo" "$hf_path" "$local_dst" <<'PY'
import os, pathlib, shutil, sys
from huggingface_hub import hf_hub_download
repo, rpath, dst = sys.argv[1:]
cached = pathlib.Path(hf_hub_download(repo_id=repo, filename=rpath))
real_blob = cached.resolve()
src_sz = real_blob.stat().st_size
if src_sz <= 0:
    raise SystemExit(f"downloaded empty file for {rpath}")
dst_p = pathlib.Path(dst)
dst_p.parent.mkdir(parents=True, exist_ok=True)
part_p = dst_p.with_name(f"{dst_p.name}.part.{os.getpid()}")
try:
    same_fs = os.stat(real_blob).st_dev == os.stat(dst_p.parent).st_dev
    if same_fs:
        os.replace(real_blob, part_p)
        if cached != real_blob:
            cached.unlink(missing_ok=True)
    else:
        shutil.copy2(real_blob, part_p)
        if part_p.stat().st_size != src_sz:
            raise RuntimeError(f"incomplete copy for {dst_p.name}")
        real_blob.unlink(missing_ok=True)
        if cached != real_blob:
            cached.unlink(missing_ok=True)
    if part_p.stat().st_size != src_sz:
        raise RuntimeError(f"size mismatch after move for {dst_p.name}")
    part_p.replace(dst_p)
except BaseException:
    part_p.unlink(missing_ok=True)
    raise
PY

  # Optionally back up shared 21.5GB base weights back to Drive atomically for future fast cold-starts
  if [ "$cache_back" = "1" ] && [ "$DRIVE_AVAILABLE" = "1" ] && [ "$CACHE_BASE_TO_DRIVE" = "1" ]; then
    local drive_dst="$DRIVE_MODELS_DIR/${sub_dir}/${filename}"
    if [ ! -s "$drive_dst" ]; then
      echo "[backup] Caching base file to Drive: $filename"
      local drive_part="${drive_dst}.part.$$"
      rm -f "$drive_part"
      if rsync -ahW "$local_dst" "$drive_part"; then
        python3 - "$local_dst" "$drive_part" "$drive_dst" <<'PY' || rm -f "$drive_part"
import pathlib, sys
src_p, part_p, dst_p = (pathlib.Path(p) for p in sys.argv[1:])
if part_p.exists() and part_p.stat().st_size == src_p.stat().st_size and src_p.stat().st_size > 0:
    part_p.replace(dst_p)
else:
    part_p.unlink(missing_ok=True)
PY
      else
        rm -f "$drive_part"
      fi
    fi
  fi
}

# 3. Shared 21.5 GB MiniMax H3 base (verified exact paths in Comfy-Org/MiniMax-H3)
write_status "准备底座" "35%" "拷到本地盘" "0.35" "true" "文本编码器与音视频 VAE（约 21.5GB）…"
sync_or_download "text_encoders" "qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors" "Comfy-Org/MiniMax-H3" "text_encoders/qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors" "1"
sync_or_download "vae" "minimax_h3_video_vae_fp16.safetensors" "Comfy-Org/MiniMax-H3" "vae/minimax_h3_video_vae_fp16.safetensors" "1"
sync_or_download "vae" "minimax_h3_audio_vae_fp32.safetensors" "Comfy-Org/MiniMax-H3" "vae/minimax_h3_audio_vae_fp32.safetensors" "1"

# 4. Variant-specific diffusion weights (all verified against live HuggingFace repo listings)
write_status "拉取主模型" "65%" "$WORKLOAD_NAME" "0.65" "true" "高速写入本地 NVMe 盘…"
case "$H3_VARIANT" in
  original)
    sync_or_download "diffusion_models" "minimax_h3_fl2va_pruned_fp8_scaled.safetensors" "Comfy-Org/MiniMax-H3" "diffusion_models/minimax_h3_fl2va_pruned_fp8_scaled.safetensors" "0"
    sync_or_download "diffusion_models" "minimax_h3_ref2va_pruned_fp8_scaled.safetensors" "Comfy-Org/MiniMax-H3" "diffusion_models/minimax_h3_ref2va_pruned_fp8_scaled.safetensors" "0"
    ;;
  flash_gguf)
    sync_or_download "diffusion_models" "minimax_h3_fl2va_hyperflow_flash_14b-${H3_GGUF_QUANT}.gguf" "VideoRebirthAI/hyperflow-h3-flash" "fl2va_gguf/minimax_h3_fl2va_hyperflow_flash_14b-${H3_GGUF_QUANT}.gguf" "0"
    sync_or_download "diffusion_models" "minimax_h3_ref2va_hyperflow_flash_14b-${H3_GGUF_QUANT}.gguf" "VideoRebirthAI/hyperflow-h3-flash" "ref2va_gguf/minimax_h3_ref2va_hyperflow_flash_14b-${H3_GGUF_QUANT}.gguf" "0"
    ;;
  custom)
    if [ "$H3_DRIVE_ONLY" = "1" ]; then
      if [ ! -d "$DRIVE_MODELS_DIR/diffusion_models" ]; then
        fail_deploy "Drive 缺少 diffusion_models 目录：${DRIVE_MODELS_DIR}/diffusion_models"
      fi
      rsync -ahW "$DRIVE_MODELS_DIR/diffusion_models/" "$COMFY_DIR/models/diffusion_models/"
      found_weights="$(find "$COMFY_DIR/models/diffusion_models" -maxdepth 2 -type f \( -name '*.safetensors' -o -name '*.gguf' \) | head -n 1 || true)"
      if [ -z "$found_weights" ]; then
        fail_deploy "Drive 的 diffusion_models 目录为空，未找到任何 .safetensors 或 .gguf 模型"
      fi
    elif [ -n "$H3_CUSTOM_HF_REPO" ] && [ -n "$H3_CUSTOM_HF_FILES" ]; then
      IFS=',' read -ra FILES <<< "$H3_CUSTOM_HF_FILES"
      for raw_f in "${FILES[@]}"; do
        f="${raw_f#"${raw_f%%[![:space:]]*}"}"
        f="${f%"${f##*[![:space:]]}"}"
        if [ -n "$f" ]; then
          base_name="$(basename "$f")"
          target_sub="diffusion_models"
          case "$f" in
            text_encoders/*) target_sub="text_encoders" ;;
            vae/*)           target_sub="vae" ;;
          esac
          sync_or_download "$target_sub" "$base_name" "$H3_CUSTOM_HF_REPO" "$f" "0"
        fi
      done
    else
      fail_deploy "自定义模式未配置模型来源"
    fi
    ;;
  *)
    # Default: H3 Flash 13.7B INT8 fast weights
    sync_or_download "diffusion_models" "minimax_h3_fl2va_hyperflow_flash_int8_convrot.safetensors" "BennyDaBall/HyperFlow-H3-Flash-INT8-ComfyUI" "minimax_h3_fl2va_hyperflow_flash_int8_convrot.safetensors" "0"
    sync_or_download "diffusion_models" "minimax_h3_ref2va_hyperflow_flash_int8_convrot.safetensors" "BennyDaBall/HyperFlow-H3-Flash-INT8-ComfyUI" "minimax_h3_ref2va_hyperflow_flash_int8_convrot.safetensors" "0"
    ;;
esac

# 4b. Sync workflows from Drive -> local NVMe (never symlink FUSE / never point --user-directory to Drive),
#     copy top-level custom_node example workflows into examples/ (read-only reference), and install flush/sync helpers.
LOCAL_WF_DIR="$COMFY_DIR/user/default/workflows"
mkdir -p "$LOCAL_WF_DIR" "$LOCAL_WF_DIR/examples"
if [ "$DRIVE_AVAILABLE" = "1" ] && [ -d "$DRIVE_WORKFLOWS_DIR" ]; then
  python3 - "$DRIVE_WORKFLOWS_DIR" "$LOCAL_WF_DIR" <<'PY' || true
import os, pathlib, shutil, sys
src_root = pathlib.Path(sys.argv[1])
dst_root = pathlib.Path(sys.argv[2])
for p in src_root.rglob("*.json"):
    if not p.is_file():
        continue
    rel = p.relative_to(src_root)
    if any(part == "examples" or part.startswith(".") for part in rel.parts):
        continue
    if ".part." in p.name:
        continue
    dst = dst_root / rel
    dst.parent.mkdir(parents=True, exist_ok=True)
    if dst.exists() and dst.stat().st_size == p.stat().st_size and dst.stat().st_mtime >= p.stat().st_mtime:
        continue
    part = dst.with_name(f".{dst.name}.part.{os.getpid()}")
    shutil.copy2(p, part)
    part.replace(dst)
PY
fi

# Import top-level example workflows from installed custom nodes into local workflows/examples/ (excluding api/)
python3 - "$COMFY_DIR/custom_nodes" "$LOCAL_WF_DIR/examples" <<'PY' || true
import pathlib, shutil, sys
nodes_root = pathlib.Path(sys.argv[1])
ex_dst = pathlib.Path(sys.argv[2])
ex_dst.mkdir(parents=True, exist_ok=True)
for ex_dir in nodes_root.glob("*/example_workflows"):
    if not ex_dir.is_dir():
        continue
    for wf in ex_dir.glob("*.json"):
        if not wf.is_file():
            continue
        dst = ex_dst / wf.name
        if not dst.exists():
            shutil.copy2(wf, dst)
PY

cat > /tmp/colab-tools-flush.py <<'PY'
import configparser
import hashlib
import json
import os
import pathlib
import re
import shutil
import sys
import time

COMFY_DIR = pathlib.Path(os.environ.get("COMFY_DIR", "/content/ComfyUI"))
LOCAL_WF_DIR = COMFY_DIR / "user" / "default" / "workflows"
DRIVE_ROOT = pathlib.Path("/content/drive/MyDrive/ColabTools")
DRIVE_WF_DIR = DRIVE_ROOT / "workflows"
DRIVE_SNAP_FILE = DRIVE_ROOT / "snapshots" / "custom_nodes.json"
BUILTIN_NODES = {
    "ComfyUI-HyperFlow-Flash",
    "comfyui_hyperflow_int8_nodes",
    "ComfyUI-Workflow-Models-Downloader",
    "ComfyUI-Manager",
}


def atomic_copy(src: pathlib.Path, dst: pathlib.Path) -> None:
    dst.parent.mkdir(parents=True, exist_ok=True)
    part = dst.with_name(f".{dst.name}.part.{os.getpid()}")
    try:
        shutil.copy2(src, part)
        part.replace(dst)
    except BaseException:
        part.unlink(missing_ok=True)
        raise


def atomic_write_text(dst: pathlib.Path, text: str) -> None:
    dst.parent.mkdir(parents=True, exist_ok=True)
    part = dst.with_name(f".{dst.name}.part.{os.getpid()}")
    try:
        part.write_text(text, encoding="utf-8")
        part.replace(dst)
    except BaseException:
        part.unlink(missing_ok=True)
        raise


def sha256_file(p: pathlib.Path) -> str:
    h = hashlib.sha256()
    with p.open("rb") as fh:
        for chunk in iter(lambda: fh.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def sync_workflows() -> int:
    if not pathlib.Path("/content/drive/MyDrive").is_dir() or not LOCAL_WF_DIR.is_dir():
        return 0
    DRIVE_WF_DIR.mkdir(parents=True, exist_ok=True)
    copied = 0
    for p in LOCAL_WF_DIR.rglob("*.json"):
        if not p.is_file():
            continue
        rel = p.relative_to(LOCAL_WF_DIR)
        if any(part == "examples" or part.startswith(".") for part in rel.parts):
            continue
        if ".part." in p.name:
            continue
        dst = DRIVE_WF_DIR / rel
        if dst.exists():
            try:
                ss, ds = p.stat(), dst.stat()
                if ss.st_size == ds.st_size and int(ss.st_mtime) == int(ds.st_mtime):
                    continue
                if ss.st_size == ds.st_size and sha256_file(p) == sha256_file(dst):
                    continue
            except OSError:
                pass
        atomic_copy(p, dst)
        copied += 1
    return copied


def save_custom_nodes_snapshot() -> int:
    if os.environ.get("RESTORE_CUSTOM_NODES_SNAPSHOT", "0") != "1":
        return 0
    if not pathlib.Path("/content/drive/MyDrive").is_dir():
        return 0
    nodes_root = COMFY_DIR / "custom_nodes"
    if not nodes_root.is_dir():
        return 0
    entries = []
    for d in sorted(nodes_root.iterdir(), key=lambda x: x.name):
        if not d.is_dir() or d.name in BUILTIN_NODES or d.name.startswith("."):
            continue
        git_cfg = d / ".git" / "config"
        if not git_cfg.is_file():
            continue
        cp = configparser.ConfigParser()
        try:
            cp.read(git_cfg, encoding="utf-8")
            url = cp.get('remote "origin"', "url", fallback="").strip()
        except Exception:
            url = ""
        if url.startswith("https://") and re.match(r"^[A-Za-z0-9._-]+$", d.name):
            entries.append({"dir": d.name, "url": url})
    payload = {
        "version": 1,
        "updated_at": int(time.time()),
        "nodes": entries,
    }
    text = json.dumps(payload, ensure_ascii=False, indent=2)
    if DRIVE_SNAP_FILE.exists():
        try:
            old = json.loads(DRIVE_SNAP_FILE.read_text(encoding="utf-8"))
            if old.get("nodes") == entries:
                return 0
        except Exception:
            pass
    atomic_write_text(DRIVE_SNAP_FILE, text)
    return len(entries)


if __name__ == "__main__":
    wf_n = sync_workflows()
    sn_n = save_custom_nodes_snapshot()
    print(f"FLUSH_OK workflows={wf_n} snapshot_nodes={sn_n}")
PY

cat > /tmp/colab-tools-wf-sync.py <<'PY'
import importlib.util
import os
import pathlib
import time

spec = importlib.util.spec_from_file_location("flush_mod", "/tmp/colab-tools-flush.py")
flush_mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(flush_mod)


def take_wf_state():
    state = {}
    if flush_mod.LOCAL_WF_DIR.is_dir():
        for p in flush_mod.LOCAL_WF_DIR.rglob("*.json"):
            if not p.is_file():
                continue
            rel = p.relative_to(flush_mod.LOCAL_WF_DIR)
            if any(part == "examples" or part.startswith(".") for part in rel.parts) or ".part." in p.name:
                continue
            try:
                st = p.stat()
                state[str(rel)] = (st.st_mtime_ns, st.st_size)
            except OSError:
                pass
    return state


def take_nodes_state():
    root = flush_mod.COMFY_DIR / "custom_nodes"
    if not root.is_dir():
        return ()
    return tuple(sorted(d.name for d in root.iterdir() if d.is_dir() and not d.name.startswith(".")))


if __name__ == "__main__":
    last_wf = None
    last_nodes = None
    while True:
        try:
            cur_wf = take_wf_state()
            if cur_wf != last_wf:
                flush_mod.sync_workflows()
                last_wf = cur_wf
            if os.environ.get("RESTORE_CUSTOM_NODES_SNAPSHOT", "0") == "1":
                cur_nodes = take_nodes_state()
                if cur_nodes != last_nodes:
                    flush_mod.save_custom_nodes_snapshot()
                    last_nodes = cur_nodes
        except Exception as exc:
            print(f"[wf-sync] warn: {exc}", flush=True)
        time.sleep(15)
PY

# Stop any previous wf-sync daemon safely and start a fresh one when Drive is available
if [ -f /tmp/colab-tools-wf-sync.pid ]; then
  old_wf_pid="$(cat /tmp/colab-tools-wf-sync.pid 2>/dev/null || true)"
  if [ -n "$old_wf_pid" ] && [ -r "/proc/${old_wf_pid}/cmdline" ] && tr '\0' ' ' < "/proc/${old_wf_pid}/cmdline" | grep -q "colab-tools-wf-sync.py"; then
    kill "$old_wf_pid" 2>/dev/null || true
  fi
fi
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  COMFY_DIR="$COMFY_DIR" RESTORE_CUSTOM_NODES_SNAPSHOT="$RESTORE_CUSTOM_NODES_SNAPSHOT" \
    nohup python3 /tmp/colab-tools-wf-sync.py > /tmp/colab-tools-wf-sync.log 2>&1 &
  echo "$!" > /tmp/colab-tools-wf-sync.pid
fi

# 5. Cleanly restart ComfyUI so new output directory, Drive mount, and freshly installed nodes always take effect
write_status "启动画布" "88%" "ComfyUI" "0.88" "true" "正在启动 ComfyUI :${SERVICE_PORT}…"
pkill -f "${COMFY_DIR}/main.py" 2>/dev/null || true
for _ in $(seq 1 10); do
  if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

LOWVRAM_FLAG=""
if [ "$H3_VARIANT" = "flash" ]; then
  VRAM_MB="$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -n 1 | tr -d '[:space:]' || true)"
  if [ -n "$VRAM_MB" ] && [ "$VRAM_MB" -eq "$VRAM_MB" ] 2>/dev/null && [ "$VRAM_MB" -le 26000 ]; then
    LOWVRAM_FLAG="--lowvram"
    echo "[gpu] Detected ${VRAM_MB}MB VRAM (<=26000MB) with H3 Flash, enabling --lowvram"
  fi
fi

MANAGER_FLAG=""
if python3 -c 'import comfyui_manager' >/dev/null 2>&1 && python3 "$COMFY_DIR/main.py" --help 2>&1 | grep -q -- '--enable-manager'; then
  MANAGER_FLAG="--enable-manager"
fi

EXTRA_PATHS_FLAG=""
if [ "$DRIVE_AVAILABLE" = "1" ] && [ -f "$COMFY_DIR/extra_model_paths.yaml" ]; then
  EXTRA_PATHS_FLAG="--extra-model-paths-config $COMFY_DIR/extra_model_paths.yaml"
fi

nohup python3 "$COMFY_DIR/main.py" \
  --listen 127.0.0.1 \
  --port "$SERVICE_PORT" \
  --output-directory "$OUTPUT_DIR" \
  --disable-auto-launch \
  ${LOWVRAM_FLAG:+$LOWVRAM_FLAG} \
  ${MANAGER_FLAG:+$MANAGER_FLAG} \
  ${EXTRA_PATHS_FLAG:+$EXTRA_PATHS_FLAG} \
  > "$LOG_FILE" 2>&1 &

for _ in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
    break
  fi
  sleep 2
done

if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
  tail -n 30 "$LOG_FILE" >&2 || true
  fail_deploy "ComfyUI 未能在 :${SERVICE_PORT} 正常启动"
fi

# 6. Always stop previous cloudflared tunnel first so disabling the toggle truly closes public access
pkill -f "cloudflared tunnel --url http://127.0.0.1:${SERVICE_PORT}" 2>/dev/null || true
PUBLIC_UI_URL=""
if [ "$ENABLE_CLOUDFLARED" = "1" ]; then
  if ! command -v cloudflared >/dev/null 2>&1; then
    curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o /usr/local/bin/cloudflared
    chmod +x /usr/local/bin/cloudflared
  fi
  nohup cloudflared tunnel --url "http://127.0.0.1:${SERVICE_PORT}" --no-autoupdate > "$CF_LOG_FILE" 2>&1 &
  for _ in $(seq 1 20); do
    PUBLIC_UI_URL="$(grep -Eo 'https://[a-zA-Z0-9.-]+\.trycloudflare\.com' "$CF_LOG_FILE" | head -n 1 || true)"
    if [ -n "$PUBLIC_UI_URL" ]; then
      break
    fi
    sleep 1
  done
fi

SAVE_NOTE="成片仅存云端（未挂 Drive）"
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  SAVE_NOTE="工作流/成片自动存 Drive · $(basename "$OUTPUT_DIR")"
fi

# Mark ready (omit busy flag so live ComfyUI /queue takes over idle watchdog accurately!)
write_status "画布已就绪" "待命" "0 排队" "1.0" "unset" "${WORKLOAD_NAME} · ${SAVE_NOTE}" "$PUBLIC_UI_URL"
echo "WORKLOAD_NAME=${WORKLOAD_NAME}"
if [ -n "$PUBLIC_UI_URL" ]; then
  echo "PUBLIC_URL=${PUBLIC_UI_URL}"
fi
echo "WORKLOAD_READY=1"
