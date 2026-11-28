#!/usr/bin/env bash
# Colab Tools Unified Built-in Deploy Script — ComfyUI Studio (H3 Video & General ComfyUI)
# Controlled by COMFY_RECIPE=h3|general (zero manual Drive upload required).
# Security & reliability:
#   - Strictly binds 127.0.0.1:${SERVICE_PORT} (accessed via local encrypted SSH tunnel; NO unauthenticated public cloudflared).
#   - One-way workflow backup (bootstrap Drive -> NVMe on start, flock-guarded incremental Local -> Drive backup during runtime & pre-stop flush).
#   - Optional cloud-side idle unload guard (stops ComfyUI process after extended idle to release VRAM; billing shutdown stays local).
set -euo pipefail

COMFY_RECIPE="${COMFY_RECIPE:-h3}"                # h3 | general
SERVICE_PORT="${SERVICE_PORT:-8188}"
WORKLOAD_KIND="${WORKLOAD_KIND:-video}"
H3_VARIANT="${H3_VARIANT:-flash}"                 # flash | original | flash_gguf | custom
H3_GGUF_QUANT="${H3_GGUF_QUANT:-Q4_K_M}"          # Q4_K_M | Q6_K | Q8_0
H3_CUSTOM_HF_REPO="${H3_CUSTOM_HF_REPO:-}"
H3_CUSTOM_HF_FILES="${H3_CUSTOM_HF_FILES:-}"
H3_DRIVE_ONLY="${H3_DRIVE_ONLY:-0}"
H3_ENABLE_SOL_ATTN="${H3_ENABLE_SOL_ATTN:-1}"
USE_GOOGLE_DRIVE="${USE_GOOGLE_DRIVE:-1}"
DRIVE_MODELS_DIR="${DRIVE_MODELS_DIR:-${DRIVE_MODELS:-}}"
DRIVE_OUTPUT_DIR="${DRIVE_OUTPUT_DIR:-${DRIVE_OUTPUT:-/content/drive/MyDrive/ColabTools/outputs}}"
DRIVE_WHEELS_DIR="${DRIVE_WHEELS_DIR:-/content/drive/MyDrive/ColabTools/wheels}"
CACHE_BASE_TO_DRIVE="${CACHE_BASE_TO_DRIVE:-1}"
RESTORE_CUSTOM_NODES_SNAPSHOT="${RESTORE_CUSTOM_NODES_SNAPSHOT:-0}"
IDLE_AUTO_STOP_MINUTES="${IDLE_AUTO_STOP_MINUTES:-0}"

COMFY_DIR="${COMFY_DIR:-/content/ComfyUI}"
STATUS_FILE="/tmp/colab-tools-status.json"
LOG_FILE="/tmp/comfyui.log"
DRIVE_WORKSPACE_ROOT="/content/drive/MyDrive/ColabTools"
DRIVE_WORKFLOWS_DIR="${DRIVE_WORKSPACE_ROOT}/workflows"
DRIVE_SNAPSHOTS_DIR="${DRIVE_WORKSPACE_ROOT}/snapshots"
DRIVE_EXTRA_MODELS_DIR="${DRIVE_WORKSPACE_ROOT}/models"

export HF_XET_HIGH_PERFORMANCE=1
export HF_HUB_ENABLE_HF_TRANSFER=1

if [ ! -f /tmp/colab-tools-session-start.ts ]; then
  date +%s > /tmp/colab-tools-session-start.ts 2>/dev/null || true
fi

# Gently release VRAM from any previously running LLM proxy if switching modes in-place (verify /proc/<pid>/cmdline)
if [ -f /tmp/colab-tools-llama.pid ]; then
  old_llm_pid="$(cat /tmp/colab-tools-llama.pid 2>/dev/null || true)"
  if [ -n "$old_llm_pid" ] && [ -r "/proc/${old_llm_pid}/cmdline" ] && tr '\0' ' ' < "/proc/${old_llm_pid}/cmdline" | grep -q "llama-server"; then
    kill "$old_llm_pid" 2>/dev/null || true
  fi
  rm -f /tmp/colab-tools-llama.pid
fi
pkill -TERM -f '/content/bin/llama-server' 2>/dev/null || true

if [ "$COMFY_RECIPE" != "h3" ] || [ "$H3_VARIANT" != "custom" ]; then
  H3_DRIVE_ONLY="0"
fi

if [ "$COMFY_RECIPE" = "h3" ]; then
  case "$H3_VARIANT" in
    original)   WORKLOAD_NAME="H3 原版 (31B)" ;;
    flash_gguf) WORKLOAD_NAME="H3 Flash 轻量 (${H3_GGUF_QUANT})" ;;
    custom)     WORKLOAD_NAME="H3 自定义模型" ;;
    *)          WORKLOAD_NAME="H3 Flash 极速版" ;;
  esac
else
  WORKLOAD_NAME="${WORKLOAD_NAME:-通用 ComfyUI}"
fi

write_status() {
  local label="$1"
  local val="$2"
  local unit="$3"
  local ratio="$4"
  local busy="$5"
  local detail="$6"
  python3 - "$STATUS_FILE" "$WORKLOAD_KIND" "$WORKLOAD_NAME" "$SERVICE_PORT" "$label" "$val" "$unit" "$ratio" "$busy" "$detail" <<'PY'
import json, pathlib, sys
dst, kind, name, port_s, label, val, unit, ratio_s, busy_s, detail = sys.argv[1:]
payload = {
    "kind": kind,
    "name": name,
    "port": int(port_s),
    "metric_label": label,
    "metric_value": val,
    "metric_unit": unit,
    "metric_ratio": max(0.0, min(1.0, float(ratio_s))),
    "detail": detail,
    "ui_url": "",
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
  write_status "部署中断" "失败" "请检查" "0.0" "false" "$reason"
  exit 1
}

on_err() {
  local line="$1"
  write_status "部署中断" "失败" "第 ${line} 行" "0.0" "false" "请查看 /tmp/colab-tools-deploy.log"
}
trap 'on_err $LINENO' ERR

# 1. Resolve Google Drive paths (supports both ColabTools and legacy ComfyUI_Models layouts)
DRIVE_AVAILABLE=0
if [ "$USE_GOOGLE_DRIVE" = "1" ] && [ -d "/content/drive/MyDrive" ]; then
  DRIVE_AVAILABLE=1
  if [ "$COMFY_RECIPE" = "h3" ]; then
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
  else
    if [ -n "$DRIVE_MODELS_DIR" ]; then
      DRIVE_EXTRA_MODELS_DIR="$DRIVE_MODELS_DIR"
    fi
  fi
  OUTPUT_DIR="$DRIVE_OUTPUT_DIR"
  mkdir -p \
    "$OUTPUT_DIR" \
    "$DRIVE_WORKFLOWS_DIR" \
    "$DRIVE_SNAPSHOTS_DIR" \
    "$DRIVE_EXTRA_MODELS_DIR/checkpoints" \
    "$DRIVE_EXTRA_MODELS_DIR/diffusion_models" \
    "$DRIVE_EXTRA_MODELS_DIR/text_encoders" \
    "$DRIVE_EXTRA_MODELS_DIR/vae" \
    "$DRIVE_EXTRA_MODELS_DIR/loras" \
    "$DRIVE_EXTRA_MODELS_DIR/clip_vision" \
    "$DRIVE_EXTRA_MODELS_DIR/controlnet" \
    "$DRIVE_EXTRA_MODELS_DIR/upscale_models" \
    "$DRIVE_EXTRA_MODELS_DIR/embeddings"
  if [ "$COMFY_RECIPE" = "h3" ]; then
    mkdir -p "$DRIVE_MODELS_DIR/text_encoders" "$DRIVE_MODELS_DIR/vae" "$DRIVE_MODELS_DIR/diffusion_models"
  fi
else
  OUTPUT_DIR="/content/output"
  mkdir -p "$OUTPUT_DIR"
fi

if [ "$H3_DRIVE_ONLY" = "1" ] && [ "$DRIVE_AVAILABLE" != "1" ]; then
  fail_deploy "仅用 Drive 模式需要先开启并授权 Google Drive"
fi

# 2. Install ComfyUI, Manager v4 & Workflow-Models-Downloader onto local NVMe
write_status "正在部署" "15%" "装环境" "0.15" "true" "安装 ComfyUI、Manager 与补模插件…"
if [ ! -d "$COMFY_DIR" ]; then
  git clone --depth 1 https://github.com/comfyanonymous/ComfyUI.git "$COMFY_DIR"
  pip install -q -r "$COMFY_DIR/requirements.txt"
fi
if [ "$COMFY_RECIPE" = "h3" ]; then
  pip install -q -U 'huggingface_hub[hf_xet]' hf_transfer safetensors gguf sentencepiece protobuf imageio-ffmpeg
else
  pip install -q -U 'huggingface_hub[hf_xet]' hf_transfer || true
fi

mkdir -p "$COMFY_DIR/custom_nodes" "$COMFY_DIR/models/text_encoders" "$COMFY_DIR/models/vae" "$COMFY_DIR/models/diffusion_models" "$COMFY_DIR/user/default/workflows" "$COMFY_DIR/user/default/workflows/examples"

# Official ComfyUI-Manager v4 (best-effort)
if [ -f "$COMFY_DIR/manager_requirements.txt" ]; then
  pip install -q -r "$COMFY_DIR/manager_requirements.txt" || true
else
  pip install -q comfyui-manager || true
fi

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

# H3-specific custom nodes
if [ "$COMFY_RECIPE" = "h3" ]; then
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
fi

# Server-side workflow missing-model downloader (downloads straight to VM NVMe)
if [ ! -d "$COMFY_DIR/custom_nodes/ComfyUI-Workflow-Models-Downloader" ]; then
  git clone --depth 1 https://github.com/slahiri/ComfyUI-Workflow-Models-Downloader.git "$COMFY_DIR/custom_nodes/ComfyUI-Workflow-Models-Downloader" || true
fi
if [ -f "$COMFY_DIR/custom_nodes/ComfyUI-Workflow-Models-Downloader/requirements.txt" ]; then
  pip install -q -r "$COMFY_DIR/custom_nodes/ComfyUI-Workflow-Models-Downloader/requirements.txt" || true
fi

# Optional additive custom node snapshot restore (Git + CNR/Registry)
if [ "$RESTORE_CUSTOM_NODES_SNAPSHOT" = "1" ] && [ "$DRIVE_AVAILABLE" = "1" ]; then
  python3 - "$COMFY_DIR" "$DRIVE_SNAPSHOTS_DIR" <<'PY' || true
import json, os, pathlib, re, shutil, subprocess, sys

comfy_dir = pathlib.Path(sys.argv[1])
nodes_root = comfy_dir / "custom_nodes"
snap_dir = pathlib.Path(sys.argv[2])


def find_cm_cli():
    exe = shutil.which("cm-cli")
    if exe:
        return [exe]
    try:
        import comfyui_manager  # type: ignore
        pkg_dir = pathlib.Path(comfyui_manager.__file__).resolve().parent
        for cand in (pkg_dir / "cm_cli.py", pkg_dir / "cm-cli.py"):
            if cand.is_file():
                return [sys.executable, str(cand)]
    except Exception:
        pass
    legacy = nodes_root / "ComfyUI-Manager" / "cm-cli.py"
    if legacy.is_file():
        return [sys.executable, str(legacy)]
    return None


cm_cmd = find_cm_cli()
env = os.environ.copy()
env["COMFYUI_PATH"] = str(comfy_dir)

snap_path = snap_dir / "custom_nodes.json"
if not snap_path.is_file():
    raise SystemExit(0)
try:
    doc = json.loads(snap_path.read_text(encoding="utf-8"))
except Exception as exc:
    print(f"[snapshot] skip unreadable snapshot: {exc}")
    raise SystemExit(0)

for item in doc.get("cnr_nodes", []):
    if not isinstance(item, dict):
        continue
    d = str(item.get("dir", "")).strip()
    pkg_id = str(item.get("id", "")).strip()
    if d and ("/" in d or "\\" in d or ".." in d or not re.match(r"^[A-Za-z0-9._-]+$", d)):
        continue
    if not pkg_id or not re.match(r"^[A-Za-z0-9._@-]+$", pkg_id):
        continue
    if (d and (nodes_root / d).exists()) or (nodes_root / pkg_id).exists():
        continue
    if cm_cmd:
        print(f"[snapshot] installing registry node {pkg_id}")
        subprocess.run(cm_cmd + ["install", pkg_id], env=env, check=False)
    elif shutil.which("comfy"):
        print(f"[snapshot] installing registry node via comfy-cli {pkg_id}")
        subprocess.run(["comfy", "--workspace", str(comfy_dir), "node", "registry-install", pkg_id], env=env, check=False)

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

# Zero-copy mount user Google Drive models/ categories
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  if [ "$COMFY_RECIPE" != "h3" ]; then
    write_status "挂载模型仓" "55%" "零拷贝挂载" "0.55" "true" "通过 extra_model_paths.yaml 挂载 Drive 模型仓…"
  fi
  python3 - "$COMFY_DIR/extra_model_paths.yaml" "$DRIVE_EXTRA_MODELS_DIR" <<'PY' || true
import json, pathlib, re, sys

yaml_path = pathlib.Path(sys.argv[1])
base_path = pathlib.Path(sys.argv[2])
base_q = json.dumps(str(base_path).rstrip("/") + "/")

entries = {
    "checkpoints": ["checkpoints/"],
    "diffusion_models": ["diffusion_models/", "unet/"],
    "unet": ["unet/", "diffusion_models/"],
    "text_encoders": ["text_encoders/", "clip/"],
    "clip": ["clip/", "text_encoders/"],
    "vae": ["vae/"],
    "loras": ["loras/"],
    "clip_vision": ["clip_vision/"],
    "controlnet": ["controlnet/", "t2i_adapter/"],
    "upscale_models": ["upscale_models/"],
    "embeddings": ["embeddings/"],
    "style_models": ["style_models/"],
    "vae_approx": ["vae_approx/"],
    "configs": ["configs/"],
    "diffusers": ["diffusers/"],
    "gligen": ["gligen/"],
    "hypernetworks": ["hypernetworks/"],
    "photomaker": ["photomaker/"],
    "audio_encoders": ["audio_encoders/"],
    "latent_upscale_models": ["latent_upscale_models/"],
    "model_patches": ["model_patches/"],
}

if base_path.is_dir():
    for child in sorted(base_path.iterdir(), key=lambda x: x.name):
        if not child.is_dir() or child.name.startswith(".") or child.name in ("MiniMax_H3", "t2i_adapter"):
            continue
        if not re.match(r"^[A-Za-z0-9._-]+$", child.name):
            continue
        if child.name not in entries:
            entries[child.name] = [f"{child.name}/"]

lines = [
    "# Auto-generated by Colab Tools (zero-copy Google Drive model mount)",
    "colab_drive_models:",
    f"  base_path: {base_q}",
]
for key, paths in entries.items():
    if len(paths) == 1:
        lines.append(f"  {key}: {paths[0]}")
    else:
        lines.append(f"  {key}: |")
        for p in paths:
            lines.append(f"    {p}")
yaml_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
PY
fi

# 3. H3 model provisioning (only when COMFY_RECIPE=h3)
if [ "$COMFY_RECIPE" = "h3" ]; then
  if [ "$H3_ENABLE_SOL_ATTN" = "1" ]; then
    if [ "$DRIVE_AVAILABLE" = "1" ] && [ -d "$DRIVE_WHEELS_DIR" ] && ls "$DRIVE_WHEELS_DIR"/*.whl >/dev/null 2>&1; then
      pip install -q --no-index --find-links "$DRIVE_WHEELS_DIR" "$DRIVE_WHEELS_DIR"/*.whl || true
    fi
  fi

  sync_or_download() {
    local sub_dir="$1"
    local filename="$2"
    local hf_repo="$3"
    local hf_path="$4"
    local cache_back="$5"

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

    echo "[hf] Fast downloading $hf_repo / $hf_path -> NVMe"
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
    part_p.unlink(missing_ok=True)
    linked = False
    if os.stat(real_blob).st_dev == os.stat(dst_p.parent).st_dev:
        try:
            os.link(real_blob, part_p)
            linked = True
        except OSError:
            linked = False
    if not linked:
        shutil.copy2(real_blob, part_p)
    if part_p.stat().st_size != src_sz:
        raise RuntimeError(f"size mismatch materializing {dst_p.name}")
    part_p.replace(dst_p)
except BaseException:
    part_p.unlink(missing_ok=True)
    raise
PY

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

  write_status "准备底座" "35%" "拷到本地盘" "0.35" "true" "文本编码器与音视频 VAE（约 21.5GB）…"
  sync_or_download "text_encoders" "qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors" "Comfy-Org/MiniMax-H3" "text_encoders/qwen3vl_32b_minimax_h3_nvfp4_awq.safetensors" "1"
  sync_or_download "vae" "minimax_h3_video_vae_fp16.safetensors" "Comfy-Org/MiniMax-H3" "vae/minimax_h3_video_vae_fp16.safetensors" "1"
  sync_or_download "vae" "minimax_h3_audio_vae_fp32.safetensors" "Comfy-Org/MiniMax-H3" "vae/minimax_h3_audio_vae_fp32.safetensors" "1"

  write_status "拉取主模型" "65%" "$WORKLOAD_NAME" "0.65" "true" "高速写入本地 NVMe 盘…"
  ACTUAL_FL2VA_MODEL=""
  ACTUAL_REF2VA_MODEL=""
  case "$H3_VARIANT" in
    original)
      ACTUAL_FL2VA_MODEL="minimax_h3_fl2va_pruned_fp8_scaled.safetensors"
      ACTUAL_REF2VA_MODEL="minimax_h3_ref2va_pruned_fp8_scaled.safetensors"
      sync_or_download "diffusion_models" "$ACTUAL_FL2VA_MODEL" "Comfy-Org/MiniMax-H3" "diffusion_models/${ACTUAL_FL2VA_MODEL}" "0"
      sync_or_download "diffusion_models" "$ACTUAL_REF2VA_MODEL" "Comfy-Org/MiniMax-H3" "diffusion_models/${ACTUAL_REF2VA_MODEL}" "0"
      ;;
    flash_gguf)
      ACTUAL_FL2VA_MODEL="minimax_h3_fl2va_hyperflow_flash_14b-${H3_GGUF_QUANT}.gguf"
      ACTUAL_REF2VA_MODEL="minimax_h3_ref2va_hyperflow_flash_14b-${H3_GGUF_QUANT}.gguf"
      sync_or_download "diffusion_models" "$ACTUAL_FL2VA_MODEL" "VideoRebirthAI/hyperflow-h3-flash" "fl2va_gguf/${ACTUAL_FL2VA_MODEL}" "0"
      sync_or_download "diffusion_models" "$ACTUAL_REF2VA_MODEL" "VideoRebirthAI/hyperflow-h3-flash" "ref2va_gguf/${ACTUAL_REF2VA_MODEL}" "0"
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
        ACTUAL_FL2VA_MODEL="$(basename "$found_weights")"
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
            if [ "$target_sub" = "diffusion_models" ]; then
              if [ -z "$ACTUAL_FL2VA_MODEL" ] || echo "$base_name" | grep -qi "fl2va"; then
                ACTUAL_FL2VA_MODEL="$base_name"
              fi
              if echo "$base_name" | grep -qi "ref2va"; then
                ACTUAL_REF2VA_MODEL="$base_name"
              fi
            fi
          fi
        done
      else
        fail_deploy "自定义模式未配置模型来源"
      fi
      ;;
    *)
      ACTUAL_FL2VA_MODEL="minimax_h3_fl2va_hyperflow_flash_int8_convrot.safetensors"
      ACTUAL_REF2VA_MODEL="minimax_h3_ref2va_hyperflow_flash_int8_convrot.safetensors"
      sync_or_download "diffusion_models" "$ACTUAL_FL2VA_MODEL" "BennyDaBall/HyperFlow-H3-Flash-INT8-ComfyUI" "$ACTUAL_FL2VA_MODEL" "0"
      sync_or_download "diffusion_models" "$ACTUAL_REF2VA_MODEL" "BennyDaBall/HyperFlow-H3-Flash-INT8-ComfyUI" "$ACTUAL_REF2VA_MODEL" "0"
      ;;
  esac
fi

# 4. Install lightweight one-way workflow backup (Drive -> NVMe on boot, flock-guarded Local -> Drive incremental backup)
#    plus optional custom_nodes snapshot & cloud-side idle unload guard.
LOCAL_WF_DIR="$COMFY_DIR/user/default/workflows"
mkdir -p "$LOCAL_WF_DIR" "$LOCAL_WF_DIR/examples"

cat > /tmp/colab-tools-flush.py <<'PY'
import configparser
import fcntl
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import time

try:
    import tomllib
except Exception:
    tomllib = None

COMFY_DIR = pathlib.Path(os.environ.get("COMFY_DIR", "/content/ComfyUI"))
COMFY_RECIPE = os.environ.get("COMFY_RECIPE", "h3").strip().lower()
LOCAL_WF_DIR = COMFY_DIR / "user" / "default" / "workflows"
DRIVE_ROOT = pathlib.Path(os.environ.get("DRIVE_WORKSPACE_ROOT", "/content/drive/MyDrive/ColabTools"))
DRIVE_WF_DIR = DRIVE_ROOT / "workflows"
DRIVE_SNAP_FILE = DRIVE_ROOT / "snapshots" / "custom_nodes.json"
LOCK_FILE = pathlib.Path(os.environ.get("WF_SYNC_LOCK_FILE", "/tmp/colab-tools-wf-sync.lock"))
DRIVE_MOUNT_CHECK = pathlib.Path(os.environ.get("DRIVE_MOUNT_CHECK", "/content/drive/MyDrive"))

BUILTIN_NODES = {"ComfyUI-Workflow-Models-Downloader", "ComfyUI-Manager"}
if COMFY_RECIPE == "h3":
    BUILTIN_NODES |= {"ComfyUI-HyperFlow-Flash", "comfyui_hyperflow_int8_nodes"}


class DriveNotMountedError(RuntimeError):
    pass


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


def iter_workflows(root: pathlib.Path):
    if not root.is_dir():
        return
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d != "examples" and not d.startswith(".")]
        cur = pathlib.Path(dirpath)
        for fname in filenames:
            if not fname.endswith(".json") or fname.startswith(".") or ".part." in fname:
                continue
            p = cur / fname
            if p.is_file():
                yield p, p.relative_to(root).as_posix()


def bootstrap_workflows_from_drive() -> int:
    """Copy existing workflows from Google Drive into local NVMe on boot without overwriting newer local edits."""
    if not DRIVE_MOUNT_CHECK.is_dir() or not DRIVE_WF_DIR.is_dir():
        return 0
    LOCAL_WF_DIR.mkdir(parents=True, exist_ok=True)
    copied = 0
    with LOCK_FILE.open("w") as lock_fh:
        fcntl.flock(lock_fh.fileno(), fcntl.LOCK_EX)
        for src, rel in iter_workflows(DRIVE_WF_DIR):
            dst = LOCAL_WF_DIR / rel
            if not dst.exists():
                try:
                    atomic_copy(src, dst)
                    copied += 1
                except OSError:
                    pass
    return copied


def backup_workflows_to_drive() -> int:
    """One-way incremental backup from local ComfyUI workflows to Google Drive (never deletes existing files)."""
    if not DRIVE_MOUNT_CHECK.is_dir():
        raise DriveNotMountedError("Google Drive is not mounted")
    LOCAL_WF_DIR.mkdir(parents=True, exist_ok=True)
    DRIVE_WF_DIR.mkdir(parents=True, exist_ok=True)
    copied = 0
    errors = []
    with LOCK_FILE.open("w") as lock_fh:
        fcntl.flock(lock_fh.fileno(), fcntl.LOCK_EX)
        for src, rel in iter_workflows(LOCAL_WF_DIR):
            dst = DRIVE_WF_DIR / rel
            try:
                if dst.is_file() and dst.stat().st_size == src.stat().st_size and sha256_file(dst) == sha256_file(src):
                    continue
                atomic_copy(src, dst)
                copied += 1
            except OSError as exc:
                errors.append(f"{rel}: {exc}")
    if errors:
        raise RuntimeError("workflow backup failed: " + "; ".join(errors[:5]))
    return copied


def find_cm_cli():
    exe = shutil.which("cm-cli")
    if exe:
        return [exe]
    try:
        import comfyui_manager  # type: ignore
        pkg_dir = pathlib.Path(comfyui_manager.__file__).resolve().parent
        for cand in (pkg_dir / "cm_cli.py", pkg_dir / "cm-cli.py"):
            if cand.is_file():
                return [sys.executable, str(cand)]
    except Exception:
        pass
    legacy = COMFY_DIR / "custom_nodes" / "ComfyUI-Manager" / "cm-cli.py"
    if legacy.is_file():
        return [sys.executable, str(legacy)]
    return None


def read_cnr_metadata(node_dir: pathlib.Path):
    pp = node_dir / "pyproject.toml"
    if not pp.is_file():
        return None
    name, version = "", ""
    raw = pp.read_text(encoding="utf-8", errors="ignore")
    if tomllib is not None:
        try:
            doc = tomllib.loads(raw)
            proj = doc.get("project", {}) if isinstance(doc, dict) else {}
            if isinstance(proj, dict):
                name = str(proj.get("name", "")).strip()
                version = str(proj.get("version", "")).strip()
        except Exception:
            pass
    if not name:
        m = re.search(r'(?m)^\s*name\s*=\s*["\']([A-Za-z0-9._@-]+)["\']', raw)
        if m:
            name = m.group(1).strip()
    if name and re.match(r"^[A-Za-z0-9._@-]+$", name):
        return {"dir": node_dir.name, "id": name, "version": version}
    return None


def save_custom_nodes_snapshot() -> int:
    if os.environ.get("RESTORE_CUSTOM_NODES_SNAPSHOT", "0") != "1":
        return 0
    if not DRIVE_MOUNT_CHECK.is_dir():
        return 0
    nodes_root = COMFY_DIR / "custom_nodes"
    if not nodes_root.is_dir():
        return 0

    git_entries, cnr_entries = [], []
    for d in sorted(nodes_root.iterdir(), key=lambda x: x.name):
        if not d.is_dir() or d.name in BUILTIN_NODES or d.name.startswith("."):
            continue
        if not re.match(r"^[A-Za-z0-9._-]+$", d.name):
            continue
        git_cfg = d / ".git" / "config"
        if git_cfg.is_file():
            cp = configparser.ConfigParser()
            try:
                cp.read(git_cfg, encoding="utf-8")
                url = cp.get('remote "origin"', "url", fallback="").strip()
            except Exception:
                url = ""
            if url.startswith("https://"):
                git_entries.append({"dir": d.name, "url": url})
                continue
        cnr_meta = read_cnr_metadata(d)
        if cnr_meta is not None:
            cnr_entries.append(cnr_meta)

    cm_cmd = find_cm_cli()
    if cm_cmd:
        tmp_cm = pathlib.Path(f"/tmp/cm-snap-{os.getpid()}.json")
        try:
            env = os.environ.copy()
            env["COMFYUI_PATH"] = str(COMFY_DIR)
            r = subprocess.run(cm_cmd + ["save-snapshot", "--output", str(tmp_cm)], env=env, capture_output=True, text=True, check=False)
            if r.returncode == 0 and tmp_cm.is_file() and tmp_cm.stat().st_size > 2:
                cm_doc = json.loads(tmp_cm.read_text(encoding="utf-8", errors="ignore"))
                seen_cnr = {item["id"] for item in cnr_entries if isinstance(item, dict) and item.get("id")}
                raw_cnr = cm_doc.get("cnr_custom_nodes", {}) if isinstance(cm_doc, dict) else {}
                if isinstance(raw_cnr, dict):
                    for pkg_id, ver in sorted(raw_cnr.items()):
                        pid = str(pkg_id).strip()
                        if pid and pid not in seen_cnr and pid not in BUILTIN_NODES and re.match(r"^[A-Za-z0-9._@-]+$", pid):
                            cnr_entries.append({"dir": pid, "id": pid, "version": str(ver or "")})
                            seen_cnr.add(pid)
        except Exception:
            pass
        finally:
            tmp_cm.unlink(missing_ok=True)

    payload = {"version": 2, "updated_at": int(time.time()), "nodes": git_entries, "cnr_nodes": cnr_entries}
    with LOCK_FILE.open("w") as lock_fh:
        fcntl.flock(lock_fh.fileno(), fcntl.LOCK_EX)
        if DRIVE_SNAP_FILE.exists():
            try:
                old = json.loads(DRIVE_SNAP_FILE.read_text(encoding="utf-8"))
                if old.get("nodes") == git_entries and old.get("cnr_nodes", []) == cnr_entries:
                    return 0
            except Exception:
                pass
        atomic_write_text(DRIVE_SNAP_FILE, json.dumps(payload, ensure_ascii=False, indent=2))
    return len(git_entries) + len(cnr_entries)


if __name__ == "__main__":
    if "--bootstrap" in sys.argv[1:]:
        n = bootstrap_workflows_from_drive()
        print(f"BOOTSTRAP_OK workflows={n}")
        raise SystemExit(0)
    if not DRIVE_MOUNT_CHECK.is_dir():
        print("FLUSH_NO_DRIVE=1")
        raise SystemExit(2)
    try:
        wf_n = backup_workflows_to_drive()
    except DriveNotMountedError:
        print("FLUSH_NO_DRIVE=1")
        raise SystemExit(2)
    sn_n = save_custom_nodes_snapshot()
    print(f"FLUSH_OK workflows={wf_n} snapshot_nodes={sn_n}")
PY

cat > /tmp/colab-tools-wf-sync.py <<'PY'
import importlib.util
import json
import os
import pathlib
import subprocess
import time
import urllib.request

spec = importlib.util.spec_from_file_location("flush_mod", "/tmp/colab-tools-flush.py")
flush_mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(flush_mod)

SERVICE_PORT = int(os.environ.get("SERVICE_PORT", "8188"))
IDLE_AUTO_STOP_MIN = int(os.environ.get("IDLE_AUTO_STOP_MINUTES", "0") or "0")
STATUS_FILE = pathlib.Path("/tmp/colab-tools-status.json")


def local_tree_sig(root: pathlib.Path):
    sig = {}
    for p, rel in flush_mod.iter_workflows(root):
        try:
            st = p.stat()
            sig[rel] = (st.st_mtime_ns, st.st_size)
        except OSError:
            pass
    return sig


def take_nodes_state():
    root = flush_mod.COMFY_DIR / "custom_nodes"
    if not root.is_dir():
        return ()
    return tuple(sorted(d.name for d in root.iterdir() if d.is_dir() and not d.name.startswith(".")))


def check_comfy_queue_busy():
    try:
        with urllib.request.urlopen(f"http://127.0.0.1:{SERVICE_PORT}/queue", timeout=2.5) as resp:
            if resp.status != 200:
                return None
            data = json.loads(resp.read().decode("utf-8", errors="ignore"))
            running = len(data.get("queue_running") or [])
            pending = len(data.get("queue_pending") or [])
            return (running + pending) > 0
    except Exception:
        return None


def unload_idle_comfyui():
    try:
        if flush_mod.DRIVE_MOUNT_CHECK.is_dir():
            flush_mod.backup_workflows_to_drive()
    except Exception:
        pass
    subprocess.run(["pkill", "-TERM", "-f", f"{flush_mod.COMFY_DIR}/main.py"], check=False)
    try:
        payload = {}
        if STATUS_FILE.is_file():
            payload = json.loads(STATUS_FILE.read_text(encoding="utf-8", errors="ignore"))
        payload["metric_label"] = "已空闲降载"
        payload["metric_value"] = "待机"
        payload["metric_unit"] = "释放显存"
        payload["metric_ratio"] = 0.0
        payload["busy"] = False
        payload["detail"] = f"ComfyUI 连续空闲超 {IDLE_AUTO_STOP_MIN} 分钟已自动停进程，需使用时点「重新部署」恢复"
        flush_mod.atomic_write_text(STATUS_FILE, json.dumps(payload, ensure_ascii=False))
    except Exception:
        pass


if __name__ == "__main__":
    last_local = None
    last_nodes = None
    idle_since = None
    unloaded = False
    while True:
        try:
            if flush_mod.DRIVE_MOUNT_CHECK.is_dir():
                cur_local = local_tree_sig(flush_mod.LOCAL_WF_DIR)
                if cur_local != last_local:
                    flush_mod.backup_workflows_to_drive()
                    # Record pre-backup snapshot so any edits made during copy trigger the next cycle.
                    last_local = cur_local
                if os.environ.get("RESTORE_CUSTOM_NODES_SNAPSHOT", "0") == "1":
                    cur_nodes = take_nodes_state()
                    if cur_nodes != last_nodes:
                        flush_mod.save_custom_nodes_snapshot()
                        last_nodes = cur_nodes
            if IDLE_AUTO_STOP_MIN > 0 and not unloaded:
                busy = check_comfy_queue_busy()
                if busy is True or busy is None:
                    idle_since = None
                else:
                    now = time.monotonic()
                    if idle_since is None:
                        idle_since = now
                    elif now - idle_since >= IDLE_AUTO_STOP_MIN * 60:
                        unload_idle_comfyui()
                        unloaded = True
        except Exception as exc:
            print(f"[wf-sync] warn: {exc}", flush=True)
        time.sleep(20)
PY

# Boot-time one-way import from Drive -> NVMe (never overwrites existing local edits)
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  COMFY_DIR="$COMFY_DIR" COMFY_RECIPE="$COMFY_RECIPE" python3 /tmp/colab-tools-flush.py --bootstrap || true
fi

# Prepare a single verified starter workflow matching this deployment's exact model filenames
# without overwriting user-edited workflows, and ensure input/first_frame.png exists.
COMFY_RECIPE="$COMFY_RECIPE" H3_VARIANT="$H3_VARIANT" H3_GGUF_QUANT="$H3_GGUF_QUANT" \
  ACTUAL_FL2VA_MODEL="${ACTUAL_FL2VA_MODEL:-}" ACTUAL_REF2VA_MODEL="${ACTUAL_REF2VA_MODEL:-}" \
  python3 - "$COMFY_DIR" "$LOCAL_WF_DIR" <<'PY' || true
import json
import os
import pathlib
import struct
import sys
import zlib

comfy_dir = pathlib.Path(sys.argv[1])
wf_dir = pathlib.Path(sys.argv[2])
nodes_root = comfy_dir / "custom_nodes"
input_dir = comfy_dir / "input"
input_dir.mkdir(parents=True, exist_ok=True)
wf_dir.mkdir(parents=True, exist_ok=True)

# 1. Ensure input/first_frame.png exists without overwriting user-provided input images.
first_frame = input_dir / "first_frame.png"
if not (first_frame.is_file() and first_frame.stat().st_size > 100):
    created = False
    try:
        from PIL import Image
        for pat in ("*/assets/*.png", "*/assets/*.jpg", "*/example_workflows/*.png", "*/example_workflows/*.jpg"):
            for cand in sorted(nodes_root.glob(pat)):
                if cand.is_file() and cand.stat().st_size > 1024:
                    with Image.open(cand) as img:
                        img.convert("RGB").save(first_frame, format="PNG")
                    created = True
                    break
            if created:
                break
    except Exception:
        created = False

    if not created:
        # Fallback: pure-stdlib 768x512 smooth twilight gradient PNG (zero external deps/network).
        w, h = 768, 512
        rows = []
        for y in range(h):
            t = y / max(1, h - 1)
            r = int(28 + 190 * (t ** 1.4))
            g = int(42 + 120 * (t ** 1.2))
            b = int(84 + 90 * (1.0 - abs(t - 0.45)))
            row = bytearray(1 + w * 3)
            row[0] = 0
            for x in range(w):
                dx = (x / max(1, w - 1)) - 0.5
                glow = max(0.0, 1.0 - (dx * dx * 3.2 + (t - 0.62) ** 2 * 6.0))
                idx = 1 + x * 3
                row[idx] = min(255, int(r + 48 * glow))
                row[idx + 1] = min(255, int(g + 38 * glow))
                row[idx + 2] = min(255, int(b + 20 * glow))
            rows.append(bytes(row))
        raw_img = b"".join(rows)

        def _chunk(tag: bytes, data: bytes) -> bytes:
            return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

        ihdr = struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)
        png_bytes = (
            b"\x89PNG\r\n\x1a\n"
            + _chunk(b"IHDR", ihdr)
            + _chunk(b"IDAT", zlib.compress(raw_img, level=6))
            + _chunk(b"IEND", b"")
        )
        tmp_png = first_frame.with_suffix(".png.tmp")
        tmp_png.write_bytes(png_bytes)
        tmp_png.replace(first_frame)

recipe = os.environ.get("COMFY_RECIPE", "h3").strip().lower()
variant = os.environ.get("H3_VARIANT", "flash").strip().lower()
gguf_quant = os.environ.get("H3_GGUF_QUANT", "Q4_K_M").strip()

if recipe != "h3":
    sys.exit(0)

def list_models(sub: str):
    d = comfy_dir / "models" / sub
    if not d.is_dir():
        return []
    return sorted([p.name for p in d.iterdir() if p.is_file() and not p.name.startswith(".")])

diff_models = list_models("diffusion_models")
text_encoders = list_models("text_encoders")
vaes = list_models("vae")

def pick_model(cands, includes, exts=(".safetensors", ".gguf")):
    for name in cands:
        low = name.lower()
        if any(low.endswith(e) for e in exts) and all(tok in low for tok in includes):
            return name
    return ""

# Prefer the exact filenames chosen by this deployment run instead of guessing alphabetically.
fl2va_model = os.environ.get("ACTUAL_FL2VA_MODEL", "").strip()
ref2va_model = os.environ.get("ACTUAL_REF2VA_MODEL", "").strip()
if not fl2va_model:
    fl2va_model = pick_model(diff_models, ["fl2va"]) or (diff_models[0] if diff_models else "")
if not ref2va_model:
    ref2va_model = pick_model(diff_models, ["ref2va"]) or fl2va_model
text_enc = pick_model(text_encoders, ["qwen"]) or (text_encoders[0] if text_encoders else "")
video_vae = pick_model(vaes, ["video"]) or (vaes[0] if vaes else "")
audio_vae = pick_model(vaes, ["audio"]) or ""

src_wf = None
if variant == "flash":
    preferred = [
        nodes_root / "comfyui_hyperflow_int8_nodes" / "example_workflows" / "hyperflow_int8_i2v.json",
        nodes_root / "ComfyUI-HyperFlow-Flash" / "example_workflows" / "hyperflow_i2v_int8.json",
    ]
    starter_name = "01_H3_Flash_起步模板.json"
elif variant == "flash_gguf":
    # Strictly use GGUF-compatible loader workflows; never fall back to INT8 loader nodes.
    preferred = [
        nodes_root / "ComfyUI-HyperFlow-Flash" / "example_workflows" / "hyperflow_i2v_gguf.json",
    ]
    starter_name = f"01_H3_GGUF_{gguf_quant}_起步模板.json"
elif variant == "original":
    preferred = [
        comfy_dir / "templates" / "video_minimax_h3_i2v.json",
    ]
    starter_name = "01_H3_原版31B_起步模板.json"
else:
    if fl2va_model.lower().endswith(".gguf"):
        preferred = [nodes_root / "ComfyUI-HyperFlow-Flash" / "example_workflows" / "hyperflow_i2v_gguf.json"]
    else:
        preferred = [
            nodes_root / "comfyui_hyperflow_int8_nodes" / "example_workflows" / "hyperflow_int8_i2v.json",
            nodes_root / "ComfyUI-HyperFlow-Flash" / "example_workflows" / "hyperflow_i2v_int8.json",
        ]
    starter_name = "01_H3_自定义_起步模板.json"

for cand in preferred:
    if cand.is_file():
        src_wf = cand
        break

if src_wf is None:
    print(f"[workflow] No matching starter workflow found for variant={variant}; skipping auto-generation.", flush=True)
    sys.exit(0)

dst_wf = wf_dir / starter_name
if dst_wf.exists():
    # Never overwrite an existing starter workflow (the user may have customized it and synced it via Drive).
    sys.exit(0)

try:
    data = json.loads(src_wf.read_text(encoding="utf-8"))
except Exception:
    sys.exit(0)

def rewrite_value(v):
    if not isinstance(v, str):
        return v
    low = v.lower()
    if low.endswith((".png", ".jpg", ".jpeg", ".webp")):
        if "first_frame" in low or "example" in low or "input" in low or "/" not in v:
            return "first_frame.png"
    if low.endswith((".safetensors", ".gguf")):
        if "fl2va" in low and fl2va_model:
            return fl2va_model
        if "ref2va" in low and ref2va_model:
            return ref2va_model
        if ("qwen" in low or "text_encoder" in low) and text_enc:
            return text_enc
        if "audio" in low and "vae" in low and audio_vae:
            return audio_vae
        if "vae" in low and video_vae:
            return video_vae
        if "h3" in low and fl2va_model:
            return fl2va_model
    return v

def walk(obj):
    if isinstance(obj, dict):
        for k, val in list(obj.items()):
            if k == "widgets_values" and isinstance(val, list):
                obj[k] = [rewrite_value(x) for x in val]
            else:
                walk(val)
    elif isinstance(obj, list):
        for item in obj:
            walk(item)

walk(data)

tmp_wf = dst_wf.with_suffix(".json.tmp")
tmp_wf.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
tmp_wf.replace(dst_wf)
PY

# Restart background workflow backup + idle unload guard daemon
if [ -f /tmp/colab-tools-wf-sync.pid ]; then
  old_wf_pid="$(cat /tmp/colab-tools-wf-sync.pid 2>/dev/null || true)"
  if [ -n "$old_wf_pid" ] && [ -r "/proc/${old_wf_pid}/cmdline" ] && tr '\0' ' ' < "/proc/${old_wf_pid}/cmdline" | grep -q "colab-tools-wf-sync.py"; then
    kill "$old_wf_pid" 2>/dev/null || true
  fi
fi
if [ "$DRIVE_AVAILABLE" = "1" ] || [ "${IDLE_AUTO_STOP_MINUTES:-0}" -gt 0 ] 2>/dev/null; then
  COMFY_DIR="$COMFY_DIR" COMFY_RECIPE="$COMFY_RECIPE" SERVICE_PORT="$SERVICE_PORT" \
    RESTORE_CUSTOM_NODES_SNAPSHOT="$RESTORE_CUSTOM_NODES_SNAPSHOT" IDLE_AUTO_STOP_MINUTES="$IDLE_AUTO_STOP_MINUTES" \
    nohup python3 /tmp/colab-tools-wf-sync.py > /tmp/colab-tools-wf-sync.log 2>&1 &
  echo "$!" > /tmp/colab-tools-wf-sync.pid
fi

# 5. Cleanly retire any legacy unauthenticated cloudflared tunnel targeting this video port,
#    then restart ComfyUI bound strictly to 127.0.0.1 (accessed via local encrypted SSH tunnel)
write_status "启动画布" "88%" "ComfyUI" "0.88" "true" "正在启动 ComfyUI :${SERVICE_PORT}…"
if [ -f /tmp/colab-tools-cloudflared.pid ]; then
  old_cf_pid="$(cat /tmp/colab-tools-cloudflared.pid 2>/dev/null || true)"
  if [ -n "$old_cf_pid" ] && [ -r "/proc/${old_cf_pid}/cmdline" ]; then
    old_cf_cmd="$(tr '\0' ' ' < "/proc/${old_cf_pid}/cmdline" 2>/dev/null || true)"
    if echo "$old_cf_cmd" | grep -q "cloudflared" && echo "$old_cf_cmd" | grep -q "127.0.0.1:${SERVICE_PORT}"; then
      kill "$old_cf_pid" 2>/dev/null || true
      rm -f /tmp/colab-tools-cloudflared.pid
    fi
  fi
fi
pkill -f "cloudflared.*tunnel.*127\.0\.0\.1:${SERVICE_PORT}\b" 2>/dev/null || true
pkill -f "${COMFY_DIR}/main.py" 2>/dev/null || true
for _ in $(seq 1 10); do
  if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

printf '%s\n' "$OUTPUT_DIR" > /tmp/colab-tools-output-dir.txt 2>/dev/null || true

COMFY_ARGS=(
  "$COMFY_DIR/main.py"
  --listen 127.0.0.1
  --port "$SERVICE_PORT"
  --output-directory "$OUTPUT_DIR"
  --disable-auto-launch
)

if [ "$COMFY_RECIPE" = "h3" ] && [ "$H3_VARIANT" = "flash" ]; then
  VRAM_MB="$(nvidia-smi --query-gpu=memory.total --format=csv,noheader,nounits 2>/dev/null | head -n 1 | tr -d '[:space:]' || true)"
  if [ -n "$VRAM_MB" ] && [ "$VRAM_MB" -eq "$VRAM_MB" ] 2>/dev/null && [ "$VRAM_MB" -le 26000 ]; then
    COMFY_ARGS+=(--lowvram)
    echo "[gpu] Detected ${VRAM_MB}MB VRAM (<=26000MB) with H3 Flash, enabling --lowvram"
  fi
fi

if python3 -c 'import comfyui_manager' >/dev/null 2>&1 && python3 "$COMFY_DIR/main.py" --help 2>&1 | grep -q -- '--enable-manager'; then
  COMFY_ARGS+=(--enable-manager)
fi

if [ "$DRIVE_AVAILABLE" = "1" ] && [ -f "$COMFY_DIR/extra_model_paths.yaml" ]; then
  COMFY_ARGS+=(--extra-model-paths-config "$COMFY_DIR/extra_model_paths.yaml")
fi

nohup python3 "${COMFY_ARGS[@]}" > "$LOG_FILE" 2>&1 &

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

SAVE_NOTE="成片暂存云端（未挂 Drive）"
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  SAVE_NOTE="工作流增量备份 & 成片存 Drive · $(basename "$OUTPUT_DIR")"
fi

# Mark ready (omit busy flag so live ComfyUI /queue takes over idle watchdog accurately)
write_status "画布已就绪" "待命" "0 排队" "1.0" "unset" "${WORKLOAD_NAME} · ${SAVE_NOTE}"
echo "WORKLOAD_NAME=${WORKLOAD_NAME}"
echo "WORKLOAD_READY=1"
