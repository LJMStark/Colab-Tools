#!/usr/bin/env bash
# Colab Tools Built-in / Starter Template — General ComfyUI Video & Image Studio
# Uses zero-copy extra_model_paths.yaml for Google Drive models (preserving legacy & custom model directories)
# and bidirectionally syncs user workflows & rendered outputs with Google Drive safely without FUSE SQLite locks.
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

# Optional custom node snapshot restore (additive-only Git + CNR/Registry, never disables built-in nodes)
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
    if d and (nodes_root / d).exists():
        continue
    if (nodes_root / pkg_id).exists():
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

# Zero-copy mount ALL Google Drive models/ categories (preserving legacy clip/, unet/, t2i_adapter/ and any custom subfolders)
if [ "$DRIVE_AVAILABLE" = "1" ]; then
  write_status "挂载模型仓" "60%" "零拷贝挂载" "0.60" "true" "通过 extra_model_paths.yaml 挂载 Drive 模型仓…"
  python3 - "$COMFY_DIR/extra_model_paths.yaml" "$DRIVE_MODELS" <<'PY' || true
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
    "# Auto-generated by Colab Tools (zero-copy Google Drive model mount, preserves legacy & custom folders)",
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

# Install true bidirectional three-way workflow sync & snapshot helpers, run initial safe sync, and import read-only node examples
LOCAL_WF_DIR="$COMFY_DIR/user/default/workflows"
mkdir -p "$LOCAL_WF_DIR" "$LOCAL_WF_DIR/examples"

cat > /tmp/colab-tools-flush.py <<'PY'
import configparser
import datetime
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
    import tomllib  # Python 3.11+
except Exception:
    tomllib = None

COMFY_DIR = pathlib.Path(os.environ.get("COMFY_DIR", "/content/ComfyUI"))
LOCAL_WF_DIR = COMFY_DIR / "user" / "default" / "workflows"
DRIVE_ROOT = pathlib.Path(os.environ.get("DRIVE_WORKSPACE_ROOT", "/content/drive/MyDrive/ColabTools"))
DRIVE_WF_DIR = DRIVE_ROOT / "workflows"
DRIVE_SNAP_DIR = DRIVE_ROOT / "snapshots"
DRIVE_SNAP_FILE = DRIVE_SNAP_DIR / "custom_nodes.json"
STATE_FILE = pathlib.Path(os.environ.get("WF_SYNC_STATE_FILE", "/tmp/colab-tools-wf-state.json"))
LOCK_FILE = pathlib.Path(os.environ.get("WF_SYNC_LOCK_FILE", "/tmp/colab-tools-wf-sync.lock"))
DRIVE_MOUNT_CHECK = pathlib.Path(os.environ.get("DRIVE_MOUNT_CHECK", "/content/drive/MyDrive"))

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


def scan_json_tree(root: pathlib.Path):
    res = {}
    had_error = False
    if not root.is_dir():
        return res, False

    def on_walk_err(_exc):
        nonlocal had_error
        had_error = True

    for dirpath, dirnames, filenames in os.walk(root, onerror=on_walk_err):
        dirnames[:] = [d for d in dirnames if d != "examples" and not d.startswith(".")]
        cur_dir = pathlib.Path(dirpath)
        for fname in filenames:
            if not fname.endswith(".json") or fname.startswith(".") or ".part." in fname:
                continue
            p = cur_dir / fname
            try:
                if not p.is_file():
                    continue
                rel = p.relative_to(root).as_posix()
                st = p.stat()
                res[rel] = (sha256_file(p), st.st_mtime_ns)
            except OSError:
                had_error = True
    return res, had_error


def load_state() -> dict:
    if not STATE_FILE.is_file():
        return {}
    try:
        data = json.loads(STATE_FILE.read_text(encoding="utf-8"))
        if isinstance(data, dict):
            return {str(k): str(v) for k, v in data.items()}
    except Exception:
        pass
    return {}


def unique_conflict_relpath(rel_str: str) -> str:
    p = pathlib.PurePosixPath(rel_str)
    ts = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    cand = str(p.with_name(f"{p.stem}.conflict-{ts}{p.suffix}"))
    if not (LOCAL_WF_DIR / cand).exists() and not (DRIVE_WF_DIR / cand).exists():
        return cand
    idx = 2
    while True:
        cand = str(p.with_name(f"{p.stem}.conflict-{ts}-{idx}{p.suffix}"))
        if not (LOCAL_WF_DIR / cand).exists() and not (DRIVE_WF_DIR / cand).exists():
            return cand
        idx += 1


def current_file_hash(p: pathlib.Path):
    if not p.exists():
        return None
    return sha256_file(p)


def preserve_conflict_pair(rel: str, l_path: pathlib.Path, d_path: pathlib.Path, new_state: dict) -> None:
    lh = sha256_file(l_path)
    dh = sha256_file(d_path)
    if lh == dh:
        new_state[rel] = lh
        return
    lm = l_path.stat().st_mtime_ns
    dm = d_path.stat().st_mtime_ns
    c_rel = unique_conflict_relpath(rel)
    c_local = LOCAL_WF_DIR / c_rel
    c_drive = DRIVE_WF_DIR / c_rel
    if lm >= dm:
        atomic_copy(d_path, c_local)
        atomic_copy(d_path, c_drive)
        atomic_copy(l_path, d_path)
        new_state[rel] = lh
        new_state[c_rel] = dh
    else:
        atomic_copy(l_path, c_local)
        atomic_copy(l_path, c_drive)
        atomic_copy(d_path, l_path)
        new_state[rel] = dh
        new_state[c_rel] = lh


def sync_workflows() -> int:
    if not DRIVE_MOUNT_CHECK.is_dir():
        return 0
    LOCAL_WF_DIR.mkdir(parents=True, exist_ok=True)
    DRIVE_WF_DIR.mkdir(parents=True, exist_ok=True)

    try:
        import fcntl
        lock_fh = LOCK_FILE.open("w")
        fcntl.flock(lock_fh.fileno(), fcntl.LOCK_EX)
    except Exception:
        lock_fh = None

    try:
        prev_state = load_state()
        local_map, local_err = scan_json_tree(LOCAL_WF_DIR)
        drive_map, drive_err = scan_json_tree(DRIVE_WF_DIR)
        if local_err or drive_err:
            raise RuntimeError(f"incomplete workflow scan (local_err={local_err}, drive_err={drive_err}); aborting sync to prevent false deletions")

        new_state = {}
        actions = 0

        for rel in sorted(set(local_map.keys()) | set(drive_map.keys())):
            l_info = local_map.get(rel)
            d_info = drive_map.get(rel)
            base_h = prev_state.get(rel)
            l_path = LOCAL_WF_DIR / rel
            d_path = DRIVE_WF_DIR / rel

            if l_info and d_info:
                lh, _ = l_info
                dh, _ = d_info
                if lh == dh:
                    new_state[rel] = lh
                elif base_h is not None and lh == base_h:
                    cur_lh = current_file_hash(l_path)
                    if cur_lh is not None and cur_lh != lh:
                        preserve_conflict_pair(rel, l_path, d_path, new_state)
                    else:
                        cur_dh = current_file_hash(d_path)
                        if cur_dh is not None:
                            atomic_copy(d_path, l_path)
                            new_state[rel] = cur_dh
                    actions += 1
                elif base_h is not None and dh == base_h:
                    cur_dh = current_file_hash(d_path)
                    if cur_dh is not None and cur_dh != dh:
                        preserve_conflict_pair(rel, l_path, d_path, new_state)
                    else:
                        cur_lh = current_file_hash(l_path)
                        if cur_lh is not None:
                            atomic_copy(l_path, d_path)
                            new_state[rel] = cur_lh
                    actions += 1
                else:
                    preserve_conflict_pair(rel, l_path, d_path, new_state)
                    actions += 1
            elif l_info and not d_info:
                lh, _ = l_info
                if base_h is not None and lh == base_h:
                    cur_lh = current_file_hash(l_path)
                    cur_dh = current_file_hash(d_path)
                    if cur_dh is not None:
                        preserve_conflict_pair(rel, l_path, d_path, new_state)
                    elif cur_lh == lh:
                        l_path.unlink(missing_ok=True)
                    elif cur_lh is not None:
                        atomic_copy(l_path, d_path)
                        new_state[rel] = cur_lh
                    actions += 1
                else:
                    cur_dh = current_file_hash(d_path)
                    if cur_dh is not None and cur_dh != lh:
                        preserve_conflict_pair(rel, l_path, d_path, new_state)
                    else:
                        cur_lh = current_file_hash(l_path)
                        if cur_lh is not None:
                            atomic_copy(l_path, d_path)
                            new_state[rel] = cur_lh
                    actions += 1
            elif d_info and not l_info:
                dh, _ = d_info
                if base_h is not None and dh == base_h:
                    cur_dh = current_file_hash(d_path)
                    cur_lh = current_file_hash(l_path)
                    if cur_lh is not None:
                        preserve_conflict_pair(rel, l_path, d_path, new_state)
                    elif cur_dh == dh:
                        d_path.unlink(missing_ok=True)
                    elif cur_dh is not None:
                        atomic_copy(d_path, l_path)
                        new_state[rel] = cur_dh
                    actions += 1
                else:
                    cur_lh = current_file_hash(l_path)
                    if cur_lh is not None and cur_lh != dh:
                        preserve_conflict_pair(rel, l_path, d_path, new_state)
                    else:
                        cur_dh = current_file_hash(d_path)
                        if cur_dh is not None:
                            atomic_copy(d_path, l_path)
                            new_state[rel] = cur_dh
                    actions += 1

        atomic_write_text(STATE_FILE, json.dumps(new_state, ensure_ascii=False, indent=2))
        return actions
    finally:
        if lock_fh is not None:
            try:
                lock_fh.close()
            except Exception:
                pass


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
    name = ""
    version = ""
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

    git_entries = []
    cnr_entries = []
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

    payload = {
        "version": 2,
        "updated_at": int(time.time()),
        "nodes": git_entries,
        "cnr_nodes": cnr_entries,
    }
    text = json.dumps(payload, ensure_ascii=False, indent=2)
    if DRIVE_SNAP_FILE.exists():
        try:
            old = json.loads(DRIVE_SNAP_FILE.read_text(encoding="utf-8"))
            if old.get("nodes") == git_entries and old.get("cnr_nodes", []) == cnr_entries:
                return 0
        except Exception:
            pass
    atomic_write_text(DRIVE_SNAP_FILE, text)
    return len(git_entries) + len(cnr_entries)


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


def quick_tree_sig(root: pathlib.Path):
    sig = {}
    if root.is_dir():
        for p in root.rglob("*.json"):
            if not p.is_file():
                continue
            rel = p.relative_to(root)
            if any(part == "examples" or part.startswith(".") for part in rel.parts) or ".part." in p.name:
                continue
            try:
                st = p.stat()
                sig[rel.as_posix()] = (st.st_mtime_ns, st.st_size)
            except OSError:
                pass
    return sig


def take_nodes_state():
    root = flush_mod.COMFY_DIR / "custom_nodes"
    if not root.is_dir():
        return ()
    return tuple(sorted(d.name for d in root.iterdir() if d.is_dir() and not d.name.startswith(".")))


if __name__ == "__main__":
    last_local = None
    last_drive = None
    last_nodes = None
    tick = 0
    while True:
        try:
            cur_local = quick_tree_sig(flush_mod.LOCAL_WF_DIR)
            cur_drive = quick_tree_sig(flush_mod.DRIVE_WF_DIR)
            if cur_local != last_local or cur_drive != last_drive or (tick % 4 == 0):
                flush_mod.sync_workflows()
                last_local = quick_tree_sig(flush_mod.LOCAL_WF_DIR)
                last_drive = quick_tree_sig(flush_mod.DRIVE_WF_DIR)
            if os.environ.get("RESTORE_CUSTOM_NODES_SNAPSHOT", "0") == "1":
                cur_nodes = take_nodes_state()
                if cur_nodes != last_nodes:
                    flush_mod.save_custom_nodes_snapshot()
                    last_nodes = cur_nodes
        except Exception as exc:
            print(f"[wf-sync] warn: {exc}", flush=True)
        tick += 1
        time.sleep(15)
PY

if [ "$DRIVE_AVAILABLE" = "1" ]; then
  COMFY_DIR="$COMFY_DIR" RESTORE_CUSTOM_NODES_SNAPSHOT="0" python3 /tmp/colab-tools-flush.py || true
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

COMFY_ARGS=(
  "$COMFY_DIR/main.py"
  --listen 127.0.0.1
  --port "$SERVICE_PORT"
  --output-directory "$OUTPUT_DIR"
  --disable-auto-launch
)

if python3 -c 'import comfyui_manager' >/dev/null 2>&1 && python3 "$COMFY_DIR/main.py" --help 2>&1 | grep -q -- '--enable-manager'; then
  COMFY_ARGS+=(--enable-manager)
fi

if [ "$DRIVE_AVAILABLE" = "1" ] && [ -f "$COMFY_DIR/extra_model_paths.yaml" ]; then
  COMFY_ARGS+=(--extra-model-paths-config "$COMFY_DIR/extra_model_paths.yaml")
fi

nohup python3 "${COMFY_ARGS[@]}" > "$LOG_FILE" 2>&1 &

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
