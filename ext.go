package firpty

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// This file holds thin wrappers around external dependencies that cannot be
// covered by unit tests without spawning real processes or relying on OS
// timing. Everything in here is intentionally trivial — no branching logic
// of consequence — and is excluded from coverage via .covignore.

// realPTYProcess implements PTYProcess against a real PTY + child process.
type realPTYProcess struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func (r *realPTYProcess) Read(p []byte) (int, error)  { return r.ptmx.Read(p) }
func (r *realPTYProcess) Write(p []byte) (int, error) { return r.ptmx.Write(p) }
func (r *realPTYProcess) Close() error                { return r.ptmx.Close() }

// SignalGroup sends sig to the process GROUP the child leads.
//
// The group, not the process. pty.StartWithSize starts the child with
// setsid, so it leads its own session and process group and everything it
// spawns inherits that group. Signalling only the child leaves a language
// server, a build, or a shell command running with its terminal torn out
// from under it — the orphan this package used to create on every
// KillWindow.
func (r *realPTYProcess) SignalGroup(sig syscall.Signal) error {
	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}
	return interpretKill(syscall.Kill(-r.cmd.Process.Pid, sig))
}

func (r *realPTYProcess) Wait() error {
	if r.cmd == nil {
		return nil
	}
	return r.cmd.Wait()
}

// defaultStarter spawns the user's shell (or `shell -c command` if non-empty)
// attached to a fresh PTY of generous default size.
func defaultStarter(command string) (PTYProcess, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	var cmd *exec.Cmd
	if command != "" {
		cmd = exec.Command(shell, "-c", command)
	} else {
		cmd = exec.Command(shell)
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 200})
	if err != nil {
		return nil, err
	}
	return &realPTYProcess{ptmx: ptmx, cmd: cmd}, nil
}

// realClock implements Clock with real time.
type realClock struct{}

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
func (realClock) NewTicker(d time.Duration) Ticker       { return realTicker{time.NewTicker(d)} }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }
