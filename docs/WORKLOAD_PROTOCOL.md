# Colab Tools Open Workload Status Protocol (`/tmp/colab-tools-status.json`)

**Colab Tools** automatically monitors any GPU/TPU workload running inside a Google Colab runtime — whether it is an LLM (`llama.cpp` / vLLM), a video or image diffusion server (ComfyUI / Wan2.2 / Flux), an audio or music studio (CosyVoice / Fish Speech / ACE-Step), or a PyTorch / LoRA training job.

## 1. Zero-Config Auto-Detection (No Code Changes Required)
Even without writing a status file, Colab Tools automatically inspects:
1. **ComfyUI Queue**: `GET http://127.0.0.1:<port>/queue` (`:8188` by default) → extracts `queue_running` and `queue_pending` and drives the idle auto-shutdown watchdog accurately.
2. **Gradio / WebUI Port Probe**: checks `<remoteServicePort>`, `:7860`, and `:8188` for live HTTP responses so you can open the UI via one-click SSH port tunneling.
3. **Dual Low-Load Hardware Watchdog**: polls `nvidia-smi` (GPU utilization + active CUDA processes) and `/proc/stat` (host CPU utilization). A session only counts as idle when **BOTH `GPU < 5%` AND `CPU < 25%`**, preventing false shutdowns while downloading 20 GB model weights or transcoding videos with FFmpeg.

---

## 2. Optional Rich Status File (`/tmp/colab-tools-status.json`)
To display custom model names, exact step progress, WebUI URLs, or explicit busy flags directly inside the macOS HUD, write a small JSON file atomically (`mv` from a temp file) to `/tmp/colab-tools-status.json`:

```json
{
  "kind": "video",
  "name": "Wan2.2-14B (ComfyUI)",
  "ui_url": "https://your-demo-node.trycloudflare.com",
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
| `kind` | `string` | One of `llm`, `video`, `image`, `audio`, `music`, `general`. |
| `name` | `string` | Human-readable workload / model name shown in the HUD endpoint table. |
| `ui_url` | `string` | Valid `https://` public URL (or `http://127.0.0.1:<port>`). Wired to the one-click **WebUI / Preview** button. |
| `port` | `integer` | Listening port (`1024–65535`) used automatically by the **Tunnel** button (`colab ssh --proxy-mode`). |
| `metric_label` | `string` | Custom title for the bottom-left telemetry card (e.g., `"渲染进度"`, `"Epoch"`). |
| `metric_value` | `string` | Large primary readout (e.g., `"18/30"`, `"Epoch 4"`). |
| `metric_unit` | `string` | Subtle baseline unit next to `metric_value` (e.g., `"steps"`, `"tracks"`). |
| `metric_ratio` | `number` | Progress bar ratio in `[0.0, 1.0]`. |
| `busy` | `boolean` | Authoritative activity signal for the idle auto-shutdown watchdog (`true` pauses countdown; `false` allows idle countdown). |
| `queue_running` | `integer` | Number of actively running jobs. |
| `queue_pending` | `integer` | Number of queued jobs. |
| `detail` | `string` | Subtle footer caption under the progress bar (e.g., `"loss 0.42 · lr 1e-4"`). |

---

## 3. Drive Starter Script Convention
If you configure a custom Google Drive startup script in **Settings → Drive Startup Script**, Colab Tools injects:
- `SERVICE_PORT` (configured remote service port)
- `WORKLOAD_KIND` (`video`, `image`, `audio`, `music`, `llm`, `general`)

When ready, print the following sentinels to `stdout`:
```bash
echo "WORKLOAD_NAME=Wan2.2-14B (ComfyUI)"
echo "PUBLIC_URL=https://..."   # optional (leave empty if using SSH tunnel)
echo "WORKLOAD_READY=1"
```

See ready-to-customize templates in [`scripts/templates/`](../scripts/templates/):
- `comfyui-video.sh` — ComfyUI video & image generation starter
- `gradio-audio-music.sh` — Gradio audio / speech / music starter (CosyVoice, Fish Speech, ACE-Step)
- `training-job.sh` — PyTorch / LoRA training progress reporter
