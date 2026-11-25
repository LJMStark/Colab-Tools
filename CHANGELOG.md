# Changelog

All notable changes to **Colab Tools** are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [0.3.6] - 2026-11-25

### Added
- **Mole-Style Desktop Main Console (`⌘0` / `⌘1–⌘5` / `⌘,`)**: Upgraded the standalone Settings window into a full responsive macOS desktop console (`1160 × 760 pt`, min `980 × 660 pt`) with a centered floating capsule navigation bar (`Status`, `Compute & Queue`, `Integrations`, `Accounts`, `Settings`) and dynamic Dock visibility (appears in Dock while open for `⌘Tab`, automatically returns to quiet menu-bar-only accessory mode when closed).
- **4×2 Bento Status Dashboard & Live Session Table**: At-a-glance status screen inside the main window featuring CU balance + status aura orb, 10-segment VRAM meter, GPU utilization sparkline, context/workload horizon bar, session runtime ring gauge (with smart target-GPU standby estimation), interactive `15m / 40m / 60m` idle watchdog selector, queue/schedule summary, one-click sync & quick actions, plus a bottom live table covering all accounts, active runtimes, extra-session billing warnings, and full-session stop-all.
- **First-Launch Environment Doctor & 3-Step Onboarding Wizard (`EnvironmentDoctor`)**:
  - Automatically checks `google-colab-cli` via official `colab version` (distinguishing healthy vs. broken binaries), local package managers (`uv`, `pipx`, `Homebrew`, `python3`), and Google Colab account authorization on first launch (silently skipped for upgraded users who already have bound accounts, and re-openable anytime under **Settings → Environment**).
  - Supports transparent, cancellable one-click `google-colab-cli` installation (`uv tool install` → `pipx install` → `brew install` → explicit-consent official Astral `uv` installer) with POSIX process-group cleanup (`POSIX_SPAWN_SETPGROUP`), live log streaming, post-install verification, and a copy-command manual fallback.
- **4 New Core Unit Test Suites (29/29 total passing)**: Added regression tests for `EnvironmentDoctor` package-manager selection, `colab version` parsing, POSIX process-group cancellation/timeout child-tree cleanup, target-GPU offline runtime estimation, and legacy `config.json` onboarding migration.

### Changed
- **Apple macOS HIG + CJK Typography Overhaul (`ColabTypography`)**: Systematically scaled up typography across the main console and menu bar popover to follow macOS HIG & Chinese legibility standards (`13–13.5pt` body, `14pt` section headers, `22pt` page titles, `12pt` captions, `>= 11pt` badge floor), restored native `.controlSize(.regular)` in the main window, and widened the menu bar companion popover to `420pt` with a top-bar button to expand the full desktop console.

## [0.3.5] - 2026-11-25

### Added
- **Standalone Native macOS Settings App (`Cmd + ,`)**: Moved complex configuration out of the narrow menu bar popover into a dedicated Apple System Settings–style sidebar window (`General`, `Compute & Queue`, `Integrations`, `Accounts`, `Advanced`, `About`) with instant-apply controls and native `SMAppService.mainApp` Launch-at-Login support.
- **Visual Compute & Queue Workbench**:
  - **2-Column Hardware Target Grid**: Select GPUs/TPUs (`G4 / H100 / A100 / L4 / T4 / v6e-1 / v5e-1`) with VRAM & estimated `CU/h` readouts and `↑ / ↓` priority ordering preserved across saves.
  - **Two-Stage Auto-Fallback**: Focus rounds `1..N` exclusively on primary scarce cards (e.g., `G4 → H100`), then automatically append a safe fallback card (e.g., `L4`) from round `N+1` onward.
  - **Off-Peak Scheduled Start**: Schedule one-shot or daily off-peak launches (e.g., `07:00` local morning presets) with automatic de-duplication, cooldown checks, and global queue mutex compliance.
  - **Local Allocation Hit-Rate Heatmap (`AllocationInsights`)**: Strictly local, privacy-preserving 6-slot (`00–04` .. `20–24`) hit-rate chart in `~/.config/colab-tools/allocation-stats.json` with automatic golden-hour recommendations after 3+ samples.
- **Multi-Client Integrations Hub (`ClientIntegrations`)**:
  - Automatically scans installed AI coding clients (`CC Switch`, `OpenCode`, `Codex CLI`, `Claude Code`) and floats detected tools to the top.
  - **Native Safe Standby Sync**: Extends safe standby writes beyond CC Switch to **OpenCode** (`~/.config/opencode/opencode.json` using per-account `{env:COLAB_TOOLS_API_KEY_<UUID>}`, never storing plaintext keys) and **Codex CLI** (`~/.codex/config.toml` idempotent per-account `# BEGIN COLAB-TOOLS MANAGED <key>` block without touching top-level defaults), with serialized writes, symlink/ownership verification, unmanaged conflict rejection, and private `0700`/`0600` timestamped backups under `~/.config/colab-tools/backups/`.
  - **Universal Dual-Protocol Copy Hub**: Safe-by-default `OpenAI (/v1)` and `Anthropic` template copy cards (never embedding Keychain secrets into offline placeholders; explicit online-only button to copy raw secret) for Cherry Studio, Cline, Roo Code, Cursor, Claude Code Router, `ccm`, and any standard client.
- **9 New Core Unit Test Suites (25/25 total passing)**: Full regression coverage for per-account client isolation, unmanaged TOML/JSON conflict protection, invalid UTF-8 non-overwrite + `0700/0600` backup permissions, two-stage fallback & 10-minute schedule grace/expiry windows, corrupted stats clamping & unknown enum field-level decoding, and safe default snippet export.

### Changed
- **Streamlined Menu Bar Popover**: Removed the top 3-tab segmented switcher (`Control / Settings / About`) in favor of crisp `+ / Refresh / ⚙` action icons; kept high-frequency workload picker & queue toggle in the popover while summarizing queue priority, fallback, and schedule into a clean single-line status row with a one-click `Configure ›` deep link into the Settings window.
- **Multi-Client Bottom Action**: Upgraded the LLM dock button to sync all enabled integrations in one click and added notification toggle support (`enableNotifications`) while keeping critical billing and circuit-breaker alerts active.

## [0.3.4] - 2026-11-25

### Security & Anti-False-Shutdown Hardening (Codex Review Follow-Up)
- **LLM API Key Origin Isolation**: Strictly separated workload `status.json` `ui_url` (`displayUIURL`) from credential-bearing LLM `publicURL`, bound LLM `/slots` polling to the current active session endpoint, and blocked cross-origin HTTP redirects from forwarding `x-api-key`.
- **Safe In-Flight Queue Cancellation**: Cancelling a polite queue while `colab new` is already in flight now verifies server-side sessions before declaring offline, alerting immediately if an allocation succeeded so billing runtimes are never hidden.
- **Per-Round Identity Verification & Persisted Cooldowns**: Re-verifies default OAuth2 CLI login before every queue round, persists 15-minute anti-abuse circuit-breaker cooldowns across app restarts and all start paths, enforces an absolute 40-minute queue deadline, and permanently prunes HTTP `400` unsupported targets across rounds.
- **Watchdog Hardening**: Made remote GPU & CPU telemetry optional so missing readings or TPU runtimes return `.unknown` instead of `0%` idle; enforced remote mtime freshness (`<= 5m`) on `/tmp/colab-tools-status.json` so stale files never override a live busy ComfyUI `/queue`; aggregated all llama-server `/slots` entries instead of checking only slot 0; preserved legacy `enableHardwareIdleFallback = false` on old `config.json` upgrades.
- **Concurrency & Entitlement Safeguards**: Added per-run queue mutex tokens, synchronous busy-state acquisition against double-clicks, immediate post-allocation 10-minute reminder scheduling, custom script execution for `.general` workloads, and Google Colab FAQ compliance guardrails for free-tier runtimes without positive CU balance.
- **Template & Build Script Fixes**: Switched ComfyUI and Gradio starter templates to `python3 json.dumps` with explicit `--output-directory` verification; fixed `pipefail` + `grep -q` SIGPIPE false-clean check in `build-app.sh` so path leaks hard-fail before replacing the app bundle; aligned context menu header status and non-zero failure exit in `PreviewExporter`.

## [0.3.3] - 2026-11-25

### Added
- **Multimodal Workload System (`WorkloadKind`)**: Replaced the binary LLM toggle with 6 workload profiles (`LLM`, `Video`, `Image`, `Audio / Speech`, `Music`, `Training / General`) with seamless migration for existing configs.
- **Open Workload Status Protocol & Auto-Detection**: Reads `/tmp/colab-tools-status.json` for custom model names, WebUI URLs, progress ratios, and busy states; automatically probes ComfyUI (`:8188/queue`), Gradio (`:7860`), host CPU utilization, and active CUDA compute processes.
- **Dual Low-Load Anti-False-Shutdown Watchdog**: Extends idle auto-shutdown beyond LLM `/slots` to all multimodal and training workloads — prioritizing app signals (`status.json` / ComfyUI `/queue`) and falling back to **GPU < 5% AND CPU < 25%** so 20GB model downloads or FFmpeg CPU transcoding are never mistakenly killed.
- **Dynamic Port Tunneling & WebUI Launcher**: SSH tunnels automatically forward the active workload port (`18080`, `8188`, `7860`, `8888`, or custom) and bottom HUD quick actions adapt to one-click WebUI/preview opening.
- **Starter Templates & Protocol Spec**: Added `docs/WORKLOAD_PROTOCOL.md` and starter scripts under `scripts/templates/` (`comfyui-video.sh`, `gradio-audio-music.sh`, `training-job.sh`).

## [0.3.2] - 2026-11-25

### Added
- **Polite Anti-Abuse GPU/TPU Queue (`PoliteQueuePolicy`)**:
  - Select 1 target (single-card wait) or multiple targets (high-to-low priority chain) filtered automatically by your subscription tier (`G4 / H100 / A100 / L4 / T4 / v6e-1 / v5e-1`).
  - Enforces single-layer retries (`COLAB_CLI_ASSIGN_RETRIES=0`), minute-scale AWS/Google Equal Jitter backoff (`3m → 6m → 9m → 12m` ceilings, 12–20s inter-target spacing), hard caps (`5` rounds / `40` minutes), global single-account queue mutex, and pre-flight session verification.
  - Immediate hard circuit-breaker + 15-minute cooldown on `412 / 429 / 401 / 403`, abuse messages, timeouts, or unknown errors (only explicit `503` capacity shortages continue queueing).
  - Automatic post-allocation workflow: macOS notification with hardware & estimated `CU/hr`, LLM auto-start + CC Switch sync when enabled, and a 10-minute anti-forget reminder for non-LLM compute sessions (auto-cancelled on browser/SSH open or stop).
- **3 New Unit Test Suites**: Coverage for Equal Jitter caps, tier filtering, circuit-breaker classification, and backwards-compatible `config.json` decoding (11/11 total tests).

### Changed
- **Cleaner In-App About Panel**: Removed the embedded WeChat QR code from the in-app About tab and `.app` bundle resources (kept in GitHub README for community onboarding).

## [0.3.1] - 2026-11-25

### Fixed
- **Settings Panel Horizontal Clipping**: Locked inner scroll content width to the popover frame (`396pt`) and wrapped the CC Switch toggle into a clean two-line layout so switching menu bar display mode (`仅图标 / 紧凑 / 详细`) never shifts or clips controls at the left edge.
- **Popover Height & Sizing Stability**: Enabled `NSHostingController.sizingOptions = .preferredContentSize` and replaced the fixed `620pt` scroll cap with dynamic screen-aware height measurement (up to `860pt` tall), allowing the full vertical HUD dashboard to display in one uninterrupted tall view like native macOS monitoring utilities while gracefully scrolling on smaller displays.

### Added
- **CC Switch Standby Row**: Added a dedicated CC Switch channel status line inside the Active Sessions & Endpoint card so users can confirm official subscription isolation at a glance.

## [0.3.0] - 2026-11-25

### Added
- **Redesigned macOS HUD Interface**: High-contrast hero Compute Units readout, metadata spec pills, 2×2 micro-chart telemetry cards (10-segment VRAM bar, live GPU utilization sparkline, horizon context bar, and interactive `15m / 40m / 60m` idle watchdog selector), wide quota/accelerator focus card, table-style session list, and 3-column bottom summary strip.
- **External / Orphan Session Warning Banner**: Surfaces non-managed or `[?]` orphan server-side sessions to help catch forgotten billing.
- **Google AI Ultra 5x / 20x Tier Details**: Updated subscription tier descriptions for `5x (1,000 CU)` and `20x (2,000 CU)` allowances.
- **Open-Source Infrastructure**: Bilingual `README.md` + `README.zh-CN.md` with `@2x` Retina UI previews, GitHub Actions CI & automated release workflows, `CONTRIBUTING.md`, `SECURITY.md`, and issue templates.

### Changed
- **Formalized Packaging**: `build-app.sh` now strips host build paths (`-file-prefix-map` + `strip -x`), verifies zero `/Users/` leakage, and performs atomic staging installation.

## [0.2.0] - 2026-11-25

### Added
- Initial public release of **Colab Tools** (native Swift 6 + SwiftUI + AppKit menu bar app).
- Multi-account Keychain ADC isolation (`--auth=adc` + `--config`), three-state idle shutdown watchdog, WAL-safe CC Switch standby sync, deadlock-free process runner, and 8 core regression unit tests.
