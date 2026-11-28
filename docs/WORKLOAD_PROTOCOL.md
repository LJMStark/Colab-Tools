# Colab Tools Open Workload Status Protocol (`/tmp/colab-tools-status.json`)

**Colab Tools** automatically monitors any GPU/TPU workload running inside a Google Colab runtime — whether it is a video diffusion workflow (ComfyUI / H3), an LLM inference server (`llama.cpp` / vLLM), or a general PyTorch / LoRA training job.

## 1. Zero-Config Auto-Detection (No Code Changes Required)
Even without writing a status file, Colab Tools automatically inspects:
1. **ComfyUI Queue**: `GET http://127.0.0.1:<port>/queue` (`:8188` by default) → extracts `queue_running` and `queue_pending` and drives the idle auto-shutdown watchdog accurately.
2. **WebUI Port Probe**: checks `<remoteServicePort>`, `:7860`, and `:8188` for live HTTP responses so you can open the UI via one-click local encrypted SSH port tunneling.
3. **Dual Low-Load Hardware Watchdog**: polls `nvidia-smi` (GPU utilization + active CUDA processes) and `/proc/stat` (host CPU utilization). A session only counts as idle when **BOTH `GPU < 5%` AND `CPU < 25%`**, preventing false shutdowns while downloading 20 GB model weights or transcoding videos with FFmpeg.

---

## 2. Optional Rich Status File (`/tmp/colab-tools-status.json`)
To display custom model names, exact step progress, or explicit busy flags directly inside the macOS console, write a small JSON file atomically (`mv` from a temp file) to `/tmp/colab-tools-status.json`:

```json
{
  "kind": "video",
  "name": "H3 Flash (ComfyUI)",
  "port": 8188,
  "metric_label": "渲染进度",
  "metric_value": "18/30",
  "metric_unit": "steps",
  "metric_ratio": 0.60,
  "busy": true,
  "queue_running": 1,
  "queue_pending": 2,
  "detail": "720p · 48fps"
}
```

### Field Reference (All Fields Optional)
| Field | Type | Description |
| :--- | :--- | :--- |
| `kind` | `string` | One of `video`, `llm`, `general`. |
| `name` | `string` | Human-readable workload / model name shown in the console. |
| `ui_url` | `string` | Valid `https://` public URL (LLM authenticated proxy) or `http://127.0.0.1:<port>`. |
| `port` | `integer` | Listening port (`1024–65535`) used automatically by the local encrypted SSH tunnel (`colab ssh --proxy-mode`). |
| `metric_label` | `string` | Custom title for the telemetry card (e.g., `"渲染进度"`, `"Epoch"`). |
| `metric_value` | `string` | Large primary readout (e.g., `"18/30"`, `"Epoch 4"`). |
| `metric_unit` | `string` | Subtle baseline unit next to `metric_value` (e.g., `"steps"`). |
| `metric_ratio` | `number` | Progress bar ratio in `[0.0, 1.0]`. |
| `busy` | `boolean` | Authoritative activity signal for the idle auto-shutdown watchdog (`true` pauses countdown; `false` allows idle countdown). |
| `queue_running` | `integer` | Number of actively running jobs. |
| `queue_pending` | `integer` | Number of queued jobs. |
| `detail` | `string` | Subtle footer caption under the progress bar (e.g., `"loss 0.42 · lr 1e-4"`). |

---

## 3. Built-in Templates & Custom Drive Scripts
Built-in embedded templates live in [`scripts/templates/`](../scripts/templates/):
- `comfyui-deploy.sh` — Unified ComfyUI & H3 video deployment template (`COMFY_RECIPE=h3|general`)
- `llm-proxy.sh` — Parameterized `llama.cpp` OpenAI/Anthropic proxy starter (also mirrored at `scripts/remote/start_server.sh`)

Optional community examples for Drive custom startup scripts live in [`docs/examples/`](examples/):
- `gradio-audio-music.sh` — Gradio audio / speech / music starter (CosyVoice, Fish Speech, ACE-Step)
- `training-job.sh` — PyTorch / LoRA training progress reporter

If you configure a custom Google Drive startup script in **Settings → Advanced**, Colab Tools injects `SERVICE_PORT` and `WORKLOAD_KIND` (`video`, `llm`, `general`). When ready, print the following sentinels to `stdout`:

```bash
echo "WORKLOAD_NAME=Custom Workload"
echo "PUBLIC_URL=https://..."   # optional for authenticated LLM proxy
echo "WORKLOAD_READY=1"
```
