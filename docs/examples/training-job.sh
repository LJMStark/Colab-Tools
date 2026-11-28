#!/usr/bin/env bash
# Colab Tools Helper Template — PyTorch / LoRA Training Progress Reporter
# Source this helper inside your training loop (or write /tmp/colab-tools-status.json from Python)
# so Colab Tools displays real-time Epoch/Step progress and pauses idle auto-shutdown while training.
set -euo pipefail

STATUS_FILE="/tmp/colab-tools-status.json"

report_training_progress() {
  local run_name="${1:-LoRA Training}"
  local current_step="${2:-0}"
  local total_steps="${3:-100}"
  local detail="${4:-training}"
  local busy="${5:-true}"

  python3 - "$STATUS_FILE" "$run_name" "$current_step" "$total_steps" "$detail" "$busy" <<'PY'
import json, pathlib, sys
dst, name, cur, tot, detail, busy = sys.argv[1:]
cur_f, tot_f = float(cur), max(1.0, float(tot))
payload = {
    "kind": "general",
    "name": name,
    "port": 8888,
    "metric_label": "训练进度",
    "metric_value": f"{int(cur_f)}/{int(tot_f)}",
    "metric_unit": "steps",
    "metric_ratio": min(1.0, max(0.0, cur_f / tot_f)),
    "busy": busy.lower() == "true",
    "detail": detail,
}
tmp = pathlib.Path(dst + ".tmp")
tmp.write_text(json.dumps(payload, ensure_ascii=False))
tmp.replace(dst)
PY
}

# Example invocation when executed directly:
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  report_training_progress "${1:-LoRA-Run}" "${2:-10}" "${3:-100}" "${4:-loss 0.42 · lr 1e-4}" "${5:-true}"
  echo "WORKLOAD_READY=1"
fi
