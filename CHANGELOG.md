# Changelog

All notable changes to firpty.

## [0.1.0] - 2026-05-01

Initial release. Extracted from `github.com/kfet/fir/pkg/ptydriver`.

### Added
- `Manager` with named sessions/windows, Send/SendRaw/Capture/Wait/Kill/Alive
- `Screen` VT100/ANSI emulator with scrollback
- `Server`/`Client` Unix-socket JSON-RPC pair
- `firpty` CLI mirroring the historical `fir pty …` commands
- `Starter` and `Clock` injection points for testability
- `.covignore`-based 100% coverage gate over the core package
