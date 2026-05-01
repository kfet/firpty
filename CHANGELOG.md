# Changelog

All notable changes to firpty.

## [Unreleased]

### Added
- `firpty version` subcommand prints the build-time version.
- Release pipeline: `make publish` tags + pushes; GoReleaser CI builds darwin/linux × amd64/arm64 binaries on tag push.
- `.fir/skills/release/SKILL.md` documenting the release flow.

### Changed
- `make build` now depends on `make test` (which gates on 100% coverage).
- Dropped redundant `cover` Make target; `open_coverage` depends on `test`.

### Removed
- Dead `Session.readErr` field (written but never read).
- Unused `osTempDir` / `osMkdirAll` / `osRemove` indirections; inlined direct `os.*` calls.

## [0.1.0] - 2026-05-01

Initial release. Extracted from `github.com/kfet/fir/pkg/ptydriver`.

### Added
- `Manager` with named sessions/windows, Send/SendRaw/Capture/Wait/Kill/Alive
- `Screen` VT100/ANSI emulator with scrollback
- `Server`/`Client` Unix-socket JSON-RPC pair
- `firpty` CLI mirroring the historical `fir pty …` commands
- `Starter` and `Clock` injection points for testability
- `.covignore`-based 100% coverage gate over the core package
