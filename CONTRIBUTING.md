# Contributing to Colab Tools

Thank you for your interest in improving **Colab Tools**!

## Development Setup

- macOS 14.0+ with Xcode / Swift 6.0+ toolchain
- Official `google-colab-cli` (`0.8.x`): `uv tool install google-colab-cli`

```bash
git clone https://github.com/LJMStark/Colab-Tools.git
cd Colab-Tools
swift test
./scripts/build-app.sh
```

## Coding Guidelines

1. **Official CLI Only**: Never add private/undocumented Colab web endpoints, cookie scrapers, or fake kernel keepalive loops.
2. **Privacy & Safety**: Never persist plaintext tokens or API keys outside macOS Keychain; validate all remote values through `InputValidator` before passing to subprocesses.
3. **Verify Before PR**:
   - Run `swift test` and ensure all tests pass with zero warnings.
   - Run `bash -n scripts/build-app.sh scripts/remote/start_server.sh`.
   - Ensure release binaries do not leak local workspace paths (`./scripts/build-app.sh`).

## Submitting Changes

- Fork the repo, create a focused feature branch (`feat/...` or `fix/...`), and open a Pull Request with a clear summary and test notes.
- For UI changes, attach before/after screenshots of the menu bar popover in dark and light mode.
