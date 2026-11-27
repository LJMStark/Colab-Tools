#!/usr/bin/env bash
# Colab Tools Built-in / Starter Template — General ComfyUI Video & Image Studio
# Uses zero-copy extra_model_paths.yaml for Google Drive models (never fills local disk with bulk rsync)
# and persists user workflows & rendered outputs back to Google Drive safely without FUSE SQLite locks.
set -euo pipefail

SERVICE_PORT="${SERVICE_PORT:-8188}"
WORKLOAD_KIND="${WORKLOAD_KIND:-video}"
USE_GOOGLE_DRIVE="${USE_GOOGLE_DRIVE:-1}"
COMFY_DIR="${COMFY_DIR:-/content/ComfyUI}"
# Prefer new DRIVE_*_DIR env vars while preserving backwards compatibility with legacy DRIVE_MODELS / DRIVE_OUTPUT
DRIVE_MODELS="${DRIVE_MODELS_DIR:-${DRIVE_MODELS:-/content/drive/MyDrive/ColabTools/models}}"
DRIVE_OUTPUT="${DRIVE_OUTPUT_DIR:-${DRIVE_OUTPUT:-/content/drive/MyDrive/ColabTools/outputs}}"
ENABLE_CLOUDFLARED="${ENABLE_CLOUDFLARED:-0}"
RESTORE_CUSTOM_NODES_SNAPSHOT="${RESTORE_CUSTOM_NODES_SNAPSHOT:-0}"
WORKLOAD_NAME="${WORKLOAD_NAME:-ComfyUI 视频/绘图}"
STATUS_FILE="/tmp/colab-tools-status.json"
LOG_FILE="/tmp/comfyui.log"
CF_LOG_FILE="/tmp/cloudflared-comfy.log"
DRIVE_WORKSPACE_ROOT="/content/drive/MyDrive/ColabTools"
DRIVE_WORKFLOWS_DIR="${DRIVE_WORKSPACE_ROOT}/workflows"
DRIVE_SNAPSHOTS_DIR="${DRIVE_WORKSPACE_ROOT}/snapshots"

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

on_err() {
  local line="$1"
  write_status "部署中断" "失败" "第 ${line} 行" "0.0" "false" "请点「查看日志」排查 /tmp/colab-tools-deploy.log" ""
}
trap 'on_err $LINENO' ERR

DRIVE_AVAILABLE=0
if [ "$USE_GOOGLE_DRIVE" = "1" ] && [ -d "/content/drive/MyDrive" ]; then
  DRIVE_AVAILABLE=1
  if [ -z "$DRIVE_MODELS" ]; then
    DRIVE_MODELS="${DRIVE_WORKSPACE_ROOT}/models"
  fi
  OUTPUT_DIR="$DRIVE_OUTPUT"
  mkdir -p \
    "$OUTPUT_DIR" \
    "$DRIVE_WORKFLOWS_DIR" \
    "$DRIVE_SNAPSHOTS_DIR" \
    "$DRIVE_MODELS/checkpoints" \
    "$DRIVE_MODELS/diffusion_models" \
    "$DRIVE_MODELS/text_encoders" \
    "$DRIVE_MODELS/vae" \
    "$DRIVE_MODELS/loras" \
    "$DRIVE_MODELS/clip_vision" \
    "$DRIVE_MODELS/controlnet" \
    "$DRIVE_MODELS/upscale_models" \
    "$DRIVE_MODELS/embeddings"
else
  OUTPUT_DIR="/content/output"
  mkdir -p "$OUTPUT_DIR"
fi

write_status "正在部署" "25%" "装 ComfyUI" "0.25" "true" "安装 ComfyUI、Manager 与补模插件…"
if [ ! -d "$COMFY_DIR" ]; then
  git clone --depth 1 https://github.com/comfyanonymous/ComfyUI.git "$COMFY_DIR"
  pip install -q -r "$COMFY_DIR/requirements.txt"
fi
pip install -q -U 'huggingface_hub[hf_xet]' hf_transfer || true

mkdir -p "$COMFY_DIR/custom_nodes" "$COMFY_DIR/models" "$COMFY_DIR/user/default/workflows" "$COMFY_DIR/user/default/workflows/examples"

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

# Zero-copy mount Google Drive models/ directory via extra_model_paths.yaml (replaces bulk rsync)
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  write_status "挂载模型仓" "60%" "零拷贝挂载" "0.60" "true" "通过 extra_model_paths.yaml 挂载 Drive 模型仓…"
  python3 - "$COMFY_DIR/extra_model_paths.yaml" "$DRIVE_MODELS" <<'PY' || true
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

# Sync workflows from Drive -> local NVMe, import node examples, and install flush/sync helpers
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

# Cleanly restart ComfyUI so updated --output-directory / extra_model_paths / Manager always apply
write_status "启动画布" "85%" "ComfyUI" "0.85" "true" "正在启动 ComfyUI :${SERVICE_PORT}…"
pkill -f "${COMFY_DIR}/main.py" 2>/dev/null || true
for _ in $(seq 1 10); do
  if ! curl -sf "http://127.0.0.1:${SERVICE_PORT}/queue" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

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
  ${MANAGER_FLAG:+$MANAGER_FLAG} \
  ${EXTRA_PATHS_FLAG:+$EXTRA_PATHS_FLAG} \
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
  SAVE_NOTE="工作流/成片直存 Drive · $(basename "$OUTPUT_DIR")"
fi
write_status "画布已就绪" "待命" "0 排队" "1.0" "unset" "ComfyUI :${SERVICE_PORT} · ${SAVE_NOTE}" "$PUBLIC_URL"
echo "WORKLOAD_NAME=${WORKLOAD_NAME}"
if [ -n "$PUBLIC_URL" ]; then
  echo "PUBLIC_URL=${PUBLIC_URL}"
fi
echo "WORKLOAD_READY=1"
