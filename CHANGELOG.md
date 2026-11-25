# Changelog

All notable changes to **Colab Tools** are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/).

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
