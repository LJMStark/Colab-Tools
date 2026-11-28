# Security Policy

## Credential Storage & Isolation

- **Default Profile**: Uses the official `google-colab-cli` OAuth2 Keychain entry (`service: google-colab-cli`) and default `~/.config/colab-cli/sessions.json`.
- **Isolated Multi-Account Profiles**: OAuth2 credentials and optional LLM API keys are stored encrypted in the macOS Keychain (`io.github.ljmstark.colabtools`). When executing an official `colab --auth=adc` command, an exclusive owner-only (`0600`, `O_CREAT | O_EXCL | O_NOFOLLOW`) temporary file is created under `~/.config/colab-tools/.ephemeral/`, zero-overwritten, and unlinked immediately upon exit. Stale files from terminated PIDs are cleaned up on launch.
- **No Shell Injection**: All session names, model aliases, HuggingFace repos/files, Drive subpaths, and remote endpoints are validated via `internal/core` sanitizers (`IsValidSessionName`, `IsValidEndpointURL`, `IsValidHFRepo`, `SanitizeDriveSubpath`, etc.) and escaped with `runner.ShellEscapePOSIX` before reaching any subprocess. Built-in deployment scripts and environment variables are streamed via Base64 over stdin. Terminal launchers use `0700` self-deleting wrapper scripts so API keys never appear in `osascript` arguments or shell history.
- **Network Exposure Boundaries**:
  - **Video / ComfyUI mode**: Strictly binds ComfyUI to `127.0.0.1` and accesses it through a local encrypted SSH tunnel (`colab ssh --proxy-mode`). Unauthenticated `cloudflared` public tunnels are disabled, and any legacy video tunnel targeting the same port is automatically terminated on redeploy.
  - **LLM Proxy mode**: Generates a cryptographically random Bearer API key (`openssl rand -hex 24` / Python `secrets.token_hex(24)`, `0600` local file) before launching `llama-server` and its authenticated `cloudflared` HTTPS endpoint.
- **CC Switch Database Safety**: Before writing to `~/.cc-switch/cc-switch.db`, Colab Tools performs a pure-Go WAL-safe SQLite online backup (`VACUUM INTO`) and runs inside a short `BEGIN IMMEDIATE` transaction that refuses to modify active (`is_current = 1`) or `official` providers.
- **Billing & Data Protection**: Shutdown commands are independently verified against `colab sessions` (matching both session name and original `endpoint`, including sessions turned into `[?]`), pre-shutdown workflow backups require a strict `FLUSH_OK` line with zero file errors, and idle auto-shutdown defers when unmounted temporary output directories hold unsaved videos.

## Reporting a Vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities. Instead, report privately via X DM to [@starkljm](https://x.com/starkljm) or GitHub Private Vulnerability Reporting, including reproduction steps and affected versions.
