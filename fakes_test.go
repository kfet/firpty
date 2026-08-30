package firpty

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// --- fake PTYProcess ----------------------------------------------------

// fakeProc models a PTY-backed child and, crucially, the process GROUP it
// leads. The two are separable here exactly as they are in the kernel: the
// leader can exit while something it spawned keeps the terminal open, which
// is the case teardown exists for and the one a Kill-the-leader fake could
// never express.
type fakeProc struct {
	pr *io.PipeReader // the master's read side: open while the terminal is held
	pw *io.PipeWriter

	mu      sync.Mutex
	written []byte
	signals []syscall.Signal

	writeErr  error // returned by Write if non-nil
	signalErr error // returned by SignalGroup if non-nil
	waitErr   error

	// ignoreHUP models a program that traps SIGHUP: only SIGKILL ends it.
	ignoreHUP atomic.Bool
	// survivor models a child of the child — a language server, a build —
	// that keeps the terminal open after the leader itself has exited, and
	// only lets go when the whole group is killed.
	survivor atomic.Bool

	exited    chan struct{}
	exitOnce  sync.Once
	closeOnce sync.Once
}

func newFakeProc() *fakeProc {
	pr, pw := io.Pipe()
	return &fakeProc{pr: pr, pw: pw, exited: make(chan struct{})}
}

func (f *fakeProc) Read(p []byte) (int, error) { return f.pr.Read(p) }

func (f *fakeProc) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.mu.Lock()
	f.written = append(f.written, p...)
	f.mu.Unlock()
	return len(p), nil
}

func (f *fakeProc) Close() error {
	f.releaseTerminal()
	return nil
}

func (f *fakeProc) SignalGroup(sig syscall.Signal) error {
	f.mu.Lock()
	f.signals = append(f.signals, sig)
	err := f.signalErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	switch {
	case sig == syscall.SIGKILL:
		// Nothing survives SIGKILL, not even a survivor.
		f.exitLeader()
		f.releaseTerminal()
	case f.ignoreHUP.Load():
		// Trapped: the signal lands and changes nothing.
	default:
		f.exitLeader()
		if !f.survivor.Load() {
			f.releaseTerminal()
		}
	}
	return nil
}

func (f *fakeProc) Wait() error {
	<-f.exited
	return f.waitErr
}

// exitLeader ends the child process itself, unblocking Wait.
func (f *fakeProc) exitLeader() { f.exitOnce.Do(func() { close(f.exited) }) }

// releaseTerminal drops the last hold on the slave, so the master reaches
// EOF and the pump stops.
func (f *fakeProc) releaseTerminal() { f.closeOnce.Do(func() { _ = f.pw.Close() }) }

// emit makes the pump observe data on its Read.
func (f *fakeProc) emit(s string) {
	_, _ = f.pw.Write([]byte(s))
}

// emitAndClose writes data then ends the process and its terminal,
// simulating a program that printed final output then exited cleanly.
func (f *fakeProc) emitAndClose(s string) {
	if s != "" {
		_, _ = f.pw.Write([]byte(s))
	}
	f.exitLeader()
	f.releaseTerminal()
}

// exitLeavingSurvivor models the interesting case: the program exits, but
// something it started still holds the terminal, so the pump never stops on
// its own. Only a group signal frees it.
func (f *fakeProc) exitLeavingSurvivor() {
	f.survivor.Store(true)
	f.exitLeader()
}

// closeWithErr terminates the read side with a custom error (covers the
// non-EOF read-error branch in pump). The process is gone with it: a master
// that errors has no terminal left to hold.
func (f *fakeProc) closeWithErr(err error) {
	_ = f.pw.CloseWithError(err)
	f.closeOnce.Do(func() {})
	f.exitLeader()
}

func (f *fakeProc) writes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.written))
	copy(out, f.written)
	return out
}

// sent returns the signals delivered to the process group, in order.
func (f *fakeProc) sent() []syscall.Signal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]syscall.Signal(nil), f.signals...)
}

// --- starter helpers ----------------------------------------------------

// recordingStarter returns a Starter that hands out fakeProc instances and
// records each one for the test to manipulate.
func recordingStarter() (Starter, func() []*fakeProc, func(error)) {
	var (
		mu    sync.Mutex
		procs []*fakeProc
		failW error
	)
	starter := func(_ string) (PTYProcess, error) {
		mu.Lock()
		defer mu.Unlock()
		if failW != nil {
			return nil, failW
		}
		f := newFakeProc()
		procs = append(procs, f)
		return f, nil
	}
	get := func() []*fakeProc {
		mu.Lock()
		defer mu.Unlock()
		out := make([]*fakeProc, len(procs))
		copy(out, procs)
		return out
	}
	setFail := func(err error) {
		mu.Lock()
		failW = err
		mu.Unlock()
	}
	return starter, get, setFail
}

// --- fake Clock ---------------------------------------------------------

type fakeTicker struct{ ch chan time.Time }

func (f *fakeTicker) C() <-chan time.Time { return f.ch }
func (f *fakeTicker) Stop()               {}

type fakeClock struct {
	afterCh chan time.Time
	ticker  *fakeTicker
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		afterCh: make(chan time.Time, 1),
		ticker:  &fakeTicker{ch: make(chan time.Time, 8)},
	}
}

func (c *fakeClock) After(time.Duration) <-chan time.Time { return c.afterCh }
func (c *fakeClock) NewTicker(time.Duration) Ticker       { return c.ticker }

func (c *fakeClock) tick()    { c.ticker.ch <- time.Now() }
func (c *fakeClock) timeout() { c.afterCh <- time.Now() }

// --- misc ---------------------------------------------------------------

var errFake = errors.New("fake error")

// waitForOutput blocks until the screen captures contain substr, or fails
// the test on timeout. Used to synchronise with the pump goroutine.
func waitForOutput(s *Screen, substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if substr == "" || contains(s.Capture(0), substr) {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
