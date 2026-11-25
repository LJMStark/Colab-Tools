# Security Policy

## Credential Storage & Isolation

- **Default Profile**: Uses the official `google-colab-cli` OAuth2 Keychain entry (`service: google-colab-cli`) and default `~/.config/colab-cli/sessions.json`.
- **Isolated Multi-Account Profiles**: OAuth2 credentials and optional LLM API keys are stored encrypted in the macOS Keychain (`io.github.ljmstark.colabtools`). When executing an official `colab --auth=adc` command, an exclusive owner-only (`0600`, `O_CREAT | O_EXCL | O_NOFOLLOW`) temporary file is created under `~/.config/colab-tools/.ephemeral/`, zero-overwritten, and unlinked immediately upon exit. Stale files from terminated PIDs are cleaned up on launch.
- **No Shell Injection**: All session names, model aliases, and remote endpoints are validated via `InputValidator` before reaching any subprocess. Terminal launchers use `0700` self-deleting wrapper scripts so API keys never appear in `osascript` arguments or shell history.
- **CC Switch Database Safety**: Before writing to `~/.cc-switch/cc-switch.db`, Colab Tools performs a WAL-safe SQLite online backup (`sqlite3_backup_*`) and runs inside a short `BEGIN IMMEDIATE` transaction that refuses to modify active (`is_current = 1`) or `official` providers.

## Reporting a Vulnerability

Please **do not** open a public GitHub issue for security vulnerabilities. Instead, report privately via X DM to [@starkljm](https://x.com/starkljm) or GitHub Private Vulnerability Reporting, including reproduction steps and affected versions.
