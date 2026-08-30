// Package firpty provides a Go-native terminal multiplexer for driving
// interactive processes. It exposes:
//
//   - A Manager that owns named PTY-backed sessions and windows
//   - A Screen that emulates enough of VT100/ANSI to capture program output
//   - A Server that exposes the Manager over a Unix-socket JSON protocol
//   - A Client that talks to the Server
//
// firpty is the standalone library/tool extracted from the fir coding-agent
// harness, used as a tmux fallback for headless agent orchestration.
package firpty

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// PTYProcess is the abstraction over a PTY-backed child process used by
// Manager. It is implemented by the real (creack/pty + os/exec) backend in
// ext.go and by fakes in tests.
type PTYProcess interface {
	// Read reads bytes from the PTY master.
	Read(p []byte) (int, error)
	// Write writes bytes to the PTY master.
	Write(p []byte) (int, error)
	// Close closes the PTY master.
	Close() error
	// SignalGroup sends sig to the process GROUP led by the child, so that
	// everything the child spawned is signalled too. A group that is
	// already gone is not an error.
	SignalGroup(sig syscall.Signal) error
	// Wait blocks until the child process exits and returns its error.
	Wait() error
}

// Starter creates a new PTYProcess running the given shell command. If
// command is empty, the process is the user's interactive shell.
type Starter func(command string) (PTYProcess, error)

// Clock abstracts time.Now / time.After / time.NewTicker so Wait can be
// tested deterministically.
type Clock interface {
	After(time.Duration) <-chan time.Time
	NewTicker(time.Duration) Ticker
}

// Ticker is the minimal subset of time.Ticker that Wait uses.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Session represents a single PTY-backed window.
type Session struct {
	Name   string
	proc   PTYProcess
	screen *Screen
	// done is closed when the pump stops, which happens when the PTY master
	// reaches EOF — that is, when the last process holding the slave has let
	// go of it. It is therefore the answer to "does this window's terminal
	// still have a holder?", which is what teardown decides from.
	done chan struct{}
	// exited is closed when the child process itself has been reaped.
	exited chan struct{}
	// tornDown makes teardown idempotent: a window can be reached both
	// directly through KillWindow and through Kill on its session, and
	// Wait must not run twice on the same child.
	tornDown sync.Once
}

// Manager owns a collection of named sessions, analogous to a tmux server.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session // "session:window" -> Session
	groups   map[string][]string // session -> ordered list of window names

	starter    Starter
	clock      Clock
	tickEvery  time.Duration
	killGrace  time.Duration
	drainGrace time.Duration
}

// ManagerOption customises a Manager (used by tests).
type ManagerOption func(*Manager)

// WithStarter overrides the PTY starter (default: real PTY via creack/pty).
func WithStarter(s Starter) ManagerOption { return func(m *Manager) { m.starter = s } }

// WithClock overrides the clock used by Wait (default: real time).
func WithClock(c Clock) ManagerOption { return func(m *Manager) { m.clock = c } }

// WithTickInterval overrides Wait's poll interval (default: 200ms).
func WithTickInterval(d time.Duration) ManagerOption {
	return func(m *Manager) { m.tickEvery = d }
}

// WithKillGrace overrides how long a window's process group gets to exit
// after SIGHUP before it is SIGKILLed (default: 2s).
func WithKillGrace(d time.Duration) ManagerOption {
	return func(m *Manager) { m.killGrace = d }
}

// WithDrainGrace overrides how long teardown waits for the terminal to be
// released before closing the master anyway (default: 5s).
func WithDrainGrace(d time.Duration) ManagerOption {
	return func(m *Manager) { m.drainGrace = d }
}

// NewManager creates a Manager with default real-PTY backend.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		sessions:   make(map[string]*Session),
		groups:     make(map[string][]string),
		starter:    defaultStarter,
		clock:      realClock{},
		tickEvery:  200 * time.Millisecond,
		killGrace:  2 * time.Second,
		drainGrace: 5 * time.Second,
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

func key(session, window string) string { return session + ":" + window }

// New creates a new session whose first window runs the user's shell.
func (m *Manager) New(session, window string) (*Session, error) {
	if window == "" {
		window = "shell"
	}
	return m.newWindow(session, window, "")
}

// NewWindow creates a new window in an existing (or new) session, optionally
// running command instead of an interactive shell.
func (m *Manager) NewWindow(session, window, command string) (*Session, error) {
	if window == "" {
		return nil, errors.New("window name required")
	}
	return m.newWindow(session, window, command)
}

func (m *Manager) newWindow(session, window, command string) (*Session, error) {
	if session == "" {
		return nil, errors.New("session name required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(session, window)
	if _, exists := m.sessions[k]; exists {
		return nil, fmt.Errorf("session %q window %q already exists", session, window)
	}

	proc, err := m.starter(command)
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	screen := NewScreen(50, 200)
	s := &Session{
		Name:   k,
		proc:   proc,
		screen: screen,
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}

	go s.pump()
	go s.reap()

	m.sessions[k] = s
	m.groups[session] = append(m.groups[session], window)
	return s, nil
}

func (s *Session) pump() {
	defer close(s.done)
	buf := make([]byte, 8192)
	for {
		n, err := s.proc.Read(buf)
		if n > 0 {
			_, _ = s.screen.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// reap waits for the child so it does not linger as a zombie for the whole
// life of the Manager. Before this existed, Wait was only ever called from
// KillWindow, so a window whose program exited on its own stayed a zombie
// until somebody killed it — and long-lived hosts (fir) accumulated them.
func (s *Session) reap() {
	defer close(s.exited)
	_ = s.proc.Wait()
}

// Send writes text+"\n" to the target window.
func (m *Manager) Send(target, text string) error {
	s, err := m.get(target)
	if err != nil {
		return err
	}
	_, err = io.WriteString(s.proc, text+"\n")
	return err
}

// SendRaw writes raw bytes to the target window.
func (m *Manager) SendRaw(target string, data []byte) error {
	s, err := m.get(target)
	if err != nil {
		return err
	}
	_, err = s.proc.Write(data)
	return err
}

// Capture returns the last lines of output from the target window. lines<=0
// means everything available.
func (m *Manager) Capture(target string, lines int) (string, error) {
	s, err := m.get(target)
	if err != nil {
		return "", err
	}
	return s.screen.Capture(lines), nil
}

// Wait polls the target's screen for a regex match, returning nil on match,
// a timeout error on deadline, or an error if the process exits without a
// match.
func (m *Manager) Wait(target, pattern string, timeout time.Duration) error {
	s, err := m.get(target)
	if err != nil {
		return err
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern: %w", err)
	}
	deadline := m.clock.After(timeout)
	tick := m.clock.NewTicker(m.tickEvery)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			return fmt.Errorf("timeout after %s waiting for %q; last output:\n%s",
				timeout, pattern, s.screen.Capture(20))
		case <-tick.C():
			if re.MatchString(s.screen.Capture(0)) {
				return nil
			}
		case <-s.done:
			if re.MatchString(s.screen.Capture(0)) {
				return nil
			}
			return fmt.Errorf("process exited before pattern %q matched", pattern)
		}
	}
}

// List returns session names (when session==""), or the windows of a
// specific session.
func (m *Manager) List(session string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if session == "" {
		names := make([]string, 0, len(m.groups))
		for n := range m.groups {
			names = append(names, n)
		}
		return names
	}
	return append([]string(nil), m.groups[session]...)
}

// Kill destroys all windows in a session.
func (m *Manager) Kill(session string) error {
	m.mu.Lock()
	windows := append([]string(nil), m.groups[session]...)
	m.mu.Unlock()
	if len(windows) == 0 {
		return fmt.Errorf("no such session: %s", session)
	}
	var errs []string
	for _, w := range windows {
		if err := m.KillWindow(session, w); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// KillWindow destroys a single window.
func (m *Manager) KillWindow(session, window string) error {
	k := key(session, window)
	m.mu.Lock()
	s, ok := m.sessions[k]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no such window: %s", k)
	}
	delete(m.sessions, k)
	windows := m.groups[session]
	for i, w := range windows {
		if w == window {
			m.groups[session] = append(windows[:i], windows[i+1:]...)
			break
		}
	}
	if len(m.groups[session]) == 0 {
		delete(m.groups, session)
	}
	m.mu.Unlock()

	return m.teardown(s)
}

// teardown ends a window: SIGHUP to the whole process group, SIGKILL after
// killGrace, and only then the master is closed.
//
// The group, not the process. Everything the window's program started — a
// language server, a build, a shell command — inherited its process group,
// and signalling only the leader leaves them running with their terminal
// torn out from under them. That is what this function used to do.
//
// SIGHUP before SIGKILL because a hangup is the signal a program attached to
// a terminal expects when its terminal goes away, and interactive tools
// (shells, editors, coding agents) flush their own state on it. SIGKILL is
// the fallback for the ones that ignore it, not the opening move.
//
// The master is closed last: until then the pump is still copying whatever
// the program said on its way out into the screen, and that is exactly the
// output somebody will want to read afterwards.
func (m *Manager) teardown(s *Session) error {
	var err error
	s.tornDown.Do(func() {
		if !s.gone() {
			if serr := s.proc.SignalGroup(syscall.SIGHUP); serr != nil {
				err = fmt.Errorf("SIGHUP %s: %w", s.Name, serr)
			}
			if !m.waitSettled(s, m.killGrace) {
				if serr := s.proc.SignalGroup(syscall.SIGKILL); serr != nil {
					err = fmt.Errorf("SIGKILL %s: %w", s.Name, serr)
				}
			}
		}
		// Bounded, because this wait is not ours to win outright: a
		// descendant that called setsid(2) has left the process group, was
		// never signalled, and can hold the slave open for as long as it
		// likes. Closing the master is then the only move left — the last
		// bytes of output matter less than a KillWindow that returns.
		m.waitSettled(s, m.drainGrace)
		_ = s.proc.Close()
	})
	return err
}

// gone reports whether a window has nothing left to kill: its child has been
// reaped AND its terminal has no holder left.
//
// Both halves matter, and the second is the interesting one. Signalling a
// group whose leader has already been reaped is a recycled-pid hazard: once
// wait(2) has returned, the number is free for the kernel to hand out again,
// and kill(-pid) would land on a stranger's process group. But that hazard
// only exists once the group is EMPTY — the kernel keeps a pid number
// allocated for as long as it is still some live process's pgid — and an
// empty group cannot be the thing holding this window's terminal.
//
// So the terminal is the evidence, not the pid. A reaped child whose pump has
// NOT stopped means something else is still on that terminal and must be
// signalled; skipping it there deadlocks, because nothing then kills the
// survivor, so the master never reaches EOF, so the drain wait never ends.
func (s *Session) gone() bool {
	select {
	case <-s.exited:
	default:
		return false
	}
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// waitSettled waits up to d for the window to become gone, and reports
// whether it did. One deadline spans both waits, so d bounds the pair rather
// than each half.
func (m *Manager) waitSettled(s *Session, d time.Duration) bool {
	deadline := time.After(d)
	select {
	case <-s.exited:
	case <-deadline:
		return false
	}
	select {
	case <-s.done:
		return true
	case <-deadline:
		return false
	}
}

// Alive reports whether the target's process is still running.
//
// The process, not its output stream. A window whose program has exited but
// whose terminal is still held open by something it spawned is not alive, and
// reporting otherwise would have a caller wait for a turn that nobody is
// going to take.
func (m *Manager) Alive(target string) bool {
	s, err := m.get(target)
	if err != nil {
		return false
	}
	select {
	case <-s.exited:
		return false
	default:
		return true
	}
}

// interpretKill decides which kill(2) results a caller should hear about. A
// group that is already gone is not a failure — the goal is that it be gone,
// and the last member may have exited between the decision and the signal —
// whereas a refusal means a program is still running with nobody driving it.
func interpretKill(err error) error {
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

func (m *Manager) get(target string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[target]; ok {
		return s, nil
	}
	if windows, ok := m.groups[target]; ok && len(windows) > 0 {
		if s, ok := m.sessions[key(target, windows[0])]; ok {
			return s, nil
		}
	}
	return nil, fmt.Errorf("no such session/window: %s", target)
}
