package firpty

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// --- fake PTYProcess ----------------------------------------------------

type fakeProc struct {
	pr *io.PipeReader
	pw *io.PipeWriter

	mu      sync.Mutex
	written []byte

	writeErr error // returned by Write if non-nil

	killCalls int32
	waitErr   error
	waitCalls int32
	closed    int32
}

func newFakeProc() *fakeProc {
	pr, pw := io.Pipe()
	return &fakeProc{pr: pr, pw: pw}
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
	if atomic.CompareAndSwapInt32(&f.closed, 0, 1) {
		_ = f.pw.Close()
	}
	return nil
}

func (f *fakeProc) Kill() error {
	atomic.AddInt32(&f.killCalls, 1)
	// Killing also unblocks pump readers.
	if atomic.CompareAndSwapInt32(&f.closed, 0, 1) {
		_ = f.pw.Close()
	}
	return nil
}

func (f *fakeProc) Wait() error {
	atomic.AddInt32(&f.waitCalls, 1)
	return f.waitErr
}

// emit makes the pump observe data on its Read.
func (f *fakeProc) emit(s string) {
	_, _ = f.pw.Write([]byte(s))
}

// emitAndClose writes data then closes, simulating a process that printed
// final output then exited.
func (f *fakeProc) emitAndClose(s string) {
	if s != "" {
		_, _ = f.pw.Write([]byte(s))
	}
	_ = f.pw.Close()
	atomic.StoreInt32(&f.closed, 1)
}

// closeWithErr terminates the read side with a custom error (covers the
// non-EOF read-error branch in pump).
func (f *fakeProc) closeWithErr(err error) {
	_ = f.pw.CloseWithError(err)
	atomic.StoreInt32(&f.closed, 1)
}

func (f *fakeProc) writes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.written))
	copy(out, f.written)
	return out
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
func (f *fakeTicker) Stop()                {}

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
func (c *fakeClock) NewTicker(time.Duration) Ticker        { return c.ticker }

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
