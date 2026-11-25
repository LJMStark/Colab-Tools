# Contributing to Colab Tools

Thank you for your interest in improving **Colab Tools**!

## Development Setup

- macOS 14.0+ or Windows 10/11 (x64) with Go (`GOTOOLCHAIN=auto` manages Go 1.27+ automatically; `CGO_ENABLED=0`)
- Official `google-colab-cli` (`0.8.x`): `uv tool install google-colab-cli` (inside WSL2 on Windows until native Windows CLI lands)

```bash
git clone https://github.com/LJMStark/Colab-Tools.git
cd Colab-Tools
GOTOOLCHAIN=auto go test -race ./...
chmod +x scripts/package.sh
./scripts/package.sh --all --zip
```

## Coding Guidelines

1. **Official CLI Only**: Never add private/undocumented Colab web endpoints, cookie scrapers, or fake kernel keepalive loops.
2. **Privacy & Safety**: Never persist plaintext tokens or API keys outside the system secrets vault (macOS Keychain / Windows Credential Manager); validate all remote values through `internal/core` sanitizers before passing to subprocesses.
3. **Verify Before PR**:
   - Run `GOTOOLCHAIN=auto go vet ./...` and `GOTOOLCHAIN=auto go test -race ./...` and ensure all tests pass.
   - Run `bash -n scripts/package.sh scripts/remote/start_server.sh scripts/templates/*.sh`.
   - Ensure release binaries do not leak local workspace paths (`./scripts/package.sh --all --zip`).

## Submitting Changes

- Fork the repo, create a focused feature branch (`feat/...` or `fix/...`), and open a Pull Request with a clear summary and test notes.
- For UI changes, regenerate and attach dark & light `@2x` screenshots via `COLABTOOLS_EXPORT_DIR`.
