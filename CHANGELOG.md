# Changelog

All notable changes to **Colab Tools** are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

## [0.3.10] - 2026-11-26

### Changed
- **First-Principles Beginner UX Simplification (6+4 Pages → 3 Clean Tabs)**:
  - Streamlined top navigation from 6 tabs (`状态 / 部署 / 抢卡 / 接入 / 账号 / 设置` + 4 nested settings sub-tabs) down to **3 clear tabs (`主页` · `账号` · `设置`)** with zero nested settings sub-tabs.
  - Replaced the empty dashed offline dashboard with a **zero-config Home Launchpad**: defaults to **H3 Flash 极速版 + Smart GPU picking (`A100 → G4 → L4`) + Google Drive sync + 10m post-queue auto-shutdown**, letting beginners start in **1 click** without hunting through settings.
  - Consolidated GPU picking into **3 unified modes (`智能挑卡` / `省点模式` / `高级自定义`)** inside a collapsible drawer, moved LLM coding-tool integrations contextually under the **大模型代理** preset, unified auto-shutdown into a single source of truth, and removed the conflicting legacy 6-workload selector from Advanced Settings.
  - Added one-click **「恢复小白推荐默认」** and direct subscription tier selection in Accounts.

### Fixed
- **Eliminated False 15-Minute Rate-Limit Locks & Hidden Provision Errors**:
  - Updated `ClassifyProvisionFailure` to match real `google-colab-cli 0.8.x` outputs (`Backend rejected accelerator '...'` → single-card 400 prune & auto-skip; `Allocation refused (precondition failed)` → active-session check that auto-skips to the next card when 0 sessions are active or surfaces a one-click **「停掉旧机器」** button when sessions exist; `502/503/504/ReadTimeout` → retryable shortage; `401/403` and command timeouts → clear actionable retry without 15-minute lockout).
  - Restricted 15-minute cooldown strictly to genuine `429` rate limits / abuse warnings, displayed the actual plain-Chinese reason (with expandable raw CLI snippet), and added a manual **「解除等待」** escape hatch.

## [0.3.9] - 2026-11-26

### Changed
- **Redesigned Light-Bezel App Icon (`assets/AppIcon.png`)**: Replaced the legacy dark robot icon with a refined Dieter Rams–inspired anodized light-silver metallic dial icon featuring an interlocking amber-orange & graphite infinity ring with an integrated minimal play triangle — honoring Google Colab's infinity motif while reflecting one-click video model deployment and precision compute control at every icon size (`1024 × 1024`, macOS HIG padding & crisp transparent corners).
- **Updated Official Showcase Previews**: Re-exported all desktop console and onboarding screenshots with the new app icon.

## [0.3.8] - 2026-11-26

### Added
- **Zero-Hands Built-in H3 Video Model Deployment (`部署` Tab)**:
  - Embedded [`scripts/templates/h3-video.sh`](scripts/templates/h3-video.sh) and [`scripts/templates/comfyui-video.sh`](scripts/templates/comfyui-video.sh) directly into the binary (`scripts/templates/embed.go`), uploaded via Base64 over stdin on start so users never need to edit or upload scripts manually.
  - Default two-card picker for **H3 Original (31B)** and **H3 Flash (13.7B fast)**, plus expandable options for **Flash Lightweight GGUF (24GB L4 friendly)**, **custom HuggingFace repos/files** (strictly validated against injection), or **Drive-only local models** — all sharing the same 21.5 GB base weights (text encoder + video/audio VAE) without redownloading.
- **Smart Google Drive Workflow (Fast Local NVMe Copy + Real-Time Output Save)**:
  - Eliminated slow/flaky FUSE symlinks in both `h3-video.sh` and `comfyui-video.sh`: existing Drive models and prebuilt acceleration wheels (`.whl`) are copied sequentially via `rsync -ahW` onto `/content` local NVMe, new weights download directly over datacenter links (`hf_xet` / `hf_transfer`, with optional base cache back to Drive), and every rendered video saves straight to Drive `outputs/` so disconnects never lose work.
- **Auto-Open ComfyUI via Local Encrypted Tunnel & Fast Queue-Drain Shutdown**:
  - Automatically establishes a local OpenSSH encrypted tunnel (`127.0.0.1:8188`) via `colab ssh --proxy-mode` and launches the default browser once ComfyUI is ready (optional `cloudflared` link toggle available when cross-device preview is needed).
  - Status dashboard now displays live deployment progress, ComfyUI running/pending queue counts, rendered clip counts, one-click `开 ComfyUI` / `换 H3 版本` / `Drive 成片` shortcuts, and fast auto-shutdown (`10m / 15m / 40m`) after the video queue drains to zero.

### Changed
- **Plain-Language Copy Overhaul**: Rewrote onboarding, deployment UI, and bilingual `README.md` / `README.zh-CN.md` in clear, approachable everyday language focused on real video creation and LLM proxy workflows.

## [0.3.7] - 2026-11-25

### Changed
- **Cross-Platform Pure-Go + GPU UI Architecture (`macOS` + `Windows`)**: Rebuilt **Colab Tools** on top of [`egoist/mygo`](https://github.com/egoist/mygo) (`v0.10.0`, pure Go, `CGO_ENABLED=0`, zero Electron / zero WebView) while preserving the Mole-inspired desktop console (`1160 × 760`), 4×2 Bento vector micro-charts, 420pt system-tray companion popover, and full feature parity across macOS and Windows.
- **Zero-CGO Cross-Platform Persistence & Security**:
  - Replaced macOS-only `Security.framework` calls with `mygo.Secrets` (macOS Keychain / Windows Credential Manager), with transparent one-way migration for legacy `v0.3.6` `account:<UUID>` / `apikey:<UUID>` entries and `config.json` Swift reference dates (`scheduledNextFireAt`, `circuitBreakUntilByAccount`, `isolated_adc`).
  - Replaced C `sqlite3_backup_*` in CC Switch sync with pure-Go `modernc.org/sqlite` + `VACUUM INTO` WAL-safe backups and `BEGIN IMMEDIATE` transactions.
- **Windows Native-First + Seamless WSL2 CLI Bridge**: Automatically prefers a healthy native `colab.exe` (direct argv invocation without PowerShell quoting bugs) and transparently falls back to `wsl.exe` bridging official `google-colab-cli` inside WSL2 with automatic `C:\...` → `/mnt/c/...` path translation, CRLF normalization, and `taskkill /T /F` process-tree cleanup.
- **Dual-Platform Packaging & CI/CD**: Replaced `scripts/build-app.sh` with [`scripts/package.sh`](scripts/package.sh) (`mygo build` + `-trimpath` + local-path leakage verification) producing signed `Colab-Tools-v0.3.7-macOS-arm64.zip` and `Colab-Tools-v0.3.7-Windows-amd64.zip`, backed by `macos-15` + `windows-latest` GitHub Actions CI & Release workflows.

### Fixed
- **State Machine, SSH Forwarding & Watchdog Hardening (Codex Review)**:
  - Settled transitional `PhaseStarting` / `PhaseStopping` phases cleanly on authoritative server snapshots and independent post-stop `colab sessions` verification (never staying stuck or misreporting unverified stops).
  - Switched SSH tunneling on both macOS and Windows to local OpenSSH `-N -L` backed by `ProxyCommand="colab ... ssh --proxy-mode -s <session>"` instead of passing `-L` after `colab ssh --`.
  - Reset idle timer when LLM `/slots` `LatestTaskID` advances between 20s polls, marked signal `.unknown` on probe failures, and only incremented idle seconds while fresh idle evidence is observed.
  - Moved stale ephemeral ADC file cleanup behind `RequestSingleInstanceLock()`, guarded allocation heatmap UI reads with lock-safe snapshots, scoped polite-queue cancel cleanup by queue token, and hardened `package.sh` local-path auditing against `pipefail` SIGPIPE.

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
