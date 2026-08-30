# Changelog

All notable changes to firpty.

## [Unreleased]

## [0.2.0] - 2026-08-30

### Fixed
- **`KillWindow` orphaned every grandchild.** Teardown was `proc.Close()` then `cmd.Process.Kill()` — SIGKILL to the session leader and nothing else. Anything the program had started (a language server, a build, a shell command) kept running with its terminal torn out from under it, and `fir` inherited that on every window it closed.

  Teardown now signals the process **GROUP**: SIGHUP first, so programs attached to a terminal can flush their own state the way they do on a real hangup, then SIGKILL after a grace period for the ones that trap it. The PTY master is closed **last** rather than first, so the pump is still copying whatever the program said on its way out.

  Whether to signal at all is decided from the **terminal**, not from the pid. Signalling a group whose leader has already been reaped is a recycled-pid hazard — the number is free for the kernel to hand out again — but that hazard only exists once the group is EMPTY, and an empty group cannot be the thing holding the window's terminal. So a window is "nothing left to kill" only when its child has been reaped AND its pump has stopped. Deciding the other way round deadlocks: nothing kills the survivor, so the master never reaches EOF, so the wait never ends.

  The final wait for the terminal is bounded (`WithDrainGrace`, default 5s) for the descendant that called `setsid(2)` and so was never in the group to be signalled.

- **Windows were never reaped until somebody killed them.** `Wait` was only ever called from `KillWindow`, so a program that exited on its own stayed a zombie for the life of the `Manager`. Each window now has a reaper goroutine.

- **`Alive` reported on the output stream, not the process.** A window whose program had exited but whose terminal was still held open by something it spawned read as alive. It now reports on the child having been reaped, which is what the name says.

### Added
- `WithKillGrace` and `WithDrainGrace` manager options, bounding the SIGHUP→SIGKILL escalation and the final wait for the terminal.
- A real-PTY test (`real_test.go`) covering the group teardown end to end against actual processes. The rest of the suite drives fakes, which cannot say whether the spawned child really leads a process group — a kernel fact, and the one the old teardown got wrong.

### Changed
- **Breaking (API):** `PTYProcess.Kill() error` is replaced by `PTYProcess.SignalGroup(syscall.Signal) error`. `Kill` was the bug: it could only ever end one process. Any external implementation of the interface must be updated; there are none known outside this module.

### Added (tooling)
- `firpty version` subcommand prints the build-time version.
- Release pipeline: `make publish` tags + pushes; GoReleaser CI builds darwin/linux × amd64/arm64 binaries on tag push.
- `make all` alias for `make build`.
- CI runs on Linux + macOS with workflow concurrency limits.
- `.fir/skills/release/SKILL.md` documenting the release flow.

### Changed (toolchain)
- **Breaking (toolchain):** minimum Go is now **1.24** (was 1.23). Needed so the coverage gate can be pinned as a `tool` directive in `go.mod` (`tool` directives landed in Go 1.24). CI (`go-version-file: go.mod`) follows automatically.
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
