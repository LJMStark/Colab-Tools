# Contributing to Colab Tools

Thank you for your interest in improving **Colab Tools**!

## Development Setup

- macOS 14.0+ with Go (`GOTOOLCHAIN=auto` manages Go 1.27+ automatically; `CGO_ENABLED=0`)
- Official `google-colab-cli` (`0.8.x`): `uv tool install google-colab-cli`

```bash
git clone https://github.com/LJMStark/Colab-Tools.git
cd Colab-Tools
GOTOOLCHAIN=auto go test -race ./...
chmod +x scripts/package.sh
./scripts/package.sh --mac --zip
```

## Coding Guidelines

1. **Official CLI Only**: Never add private/undocumented Colab web endpoints, cookie scrapers, or fake kernel keepalive loops.
2. **Privacy & Safety**: Never persist plaintext tokens or API keys outside macOS Keychain; validate all remote values through `internal/core` sanitizers before passing to subprocesses.
3. **Verify Before PR**:
   - Run `GOTOOLCHAIN=auto go vet ./...`, `GOTOOLCHAIN=auto go tool mygo vet .`, and `GOTOOLCHAIN=auto go test -race ./...` and ensure all tests pass.
   - Verify LLM script parity (`cmp -s scripts/templates/llm-proxy.sh scripts/remote/start_server.sh`) and run shell syntax checks one file at a time (never `bash -n a.sh b.sh`, which only checks the first file):
     ```bash
     for f in scripts/package.sh scripts/remote/start_server.sh scripts/templates/*.sh docs/examples/*.sh; do bash -n "$f" || exit 1; done
     ```
   - Ensure release binaries do not leak local workspace paths (`./scripts/package.sh --mac --zip`).

## Submitting Changes

- Fork the repo, create a focused feature branch (`feat/...` or `fix/...`), and open a Pull Request with a clear summary and test notes.
- For UI changes, regenerate the official `@2x` screenshots via `COLABTOOLS_EXPORT_DIR="$PWD/docs/images" go test ./internal/uiapp -run '^TestExportOfficialShowcasePreviews$'`.
