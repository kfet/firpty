package firpty

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// newTestManager builds a Manager wired with fakes and returns helpers.
func newTestManager(t *testing.T) (*Manager, func() []*fakeProc, func(error), *fakeClock) {
	t.Helper()
	starter, get, setFail := recordingStarter()
	clk := newFakeClock()
	m := NewManager(WithStarter(starter), WithClock(clk), WithTickInterval(time.Millisecond))
	return m, get, setFail, clk
}

func TestManager_NewSession(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	s, err := m.New("proj", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "proj:shell" {
		t.Fatalf("name %q", s.Name)
	}
	if got := m.List(""); len(got) != 1 || got[0] != "proj" {
		t.Fatalf("list %v", got)
	}
	if got := m.List("proj"); len(got) != 1 || got[0] != "shell" {
		t.Fatalf("windows %v", got)
	}
}

func TestManager_NewRequiresSession(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	if _, err := m.New("", "x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestManager_NewWindowRequiresName(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	if _, err := m.NewWindow("p", "", ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestManager_NewWindowDuplicate(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	if _, err := m.NewWindow("p", "w", "cmd"); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestManager_NewStarterError(t *testing.T) {
	m, _, setFail, _ := newTestManager(t)
	setFail(errors.New("boom"))
	if _, err := m.New("p", "w"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
}

func TestManager_SendAndSendRaw(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	if _, err := m.New("p", "w"); err != nil {
		t.Fatal(err)
	}
	if err := m.Send("p:w", "hi"); err != nil {
		t.Fatal(err)
	}
	if err := m.SendRaw("p:w", []byte{0x03}); err != nil {
		t.Fatal(err)
	}
	procs := get()
	if len(procs) != 1 {
		t.Fatalf("procs=%d", len(procs))
	}
	w := string(procs[0].writes())
	if w != "hi\n\x03" {
		t.Fatalf("written %q", w)
	}
}

func TestManager_SendNoSuch(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	if err := m.Send("nope", "x"); err == nil {
		t.Fatal("expected error")
	}
	if err := m.SendRaw("nope", nil); err == nil {
		t.Fatal("expected error")
	}
	if _, err := m.Capture("nope", 5); err == nil {
		t.Fatal("expected error")
	}
	if err := m.Wait("nope", "x", time.Millisecond); err == nil {
		t.Fatal("expected error")
	}
	if m.Alive("nope") {
		t.Fatal("expected not alive")
	}
}

func TestManager_SendWriteError(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	get()[0].writeErr = errors.New("write fail")
	if err := m.Send("p:w", "x"); err == nil {
		t.Fatal("expected err")
	}
	if err := m.SendRaw("p:w", []byte("x")); err == nil {
		t.Fatal("expected err")
	}
}

func TestManager_CaptureAfterEmit(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	get()[0].emit("hello\r\n")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		out, _ := m.Capture("p:w", 0)
		if strings.Contains(out, "hello") {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	out, err := m.Capture("p:w", 0)
	if err != nil || !strings.Contains(out, "hello") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestManager_CaptureBySessionName(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("only", "w1")
	get()[0].emit("x")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		out, _ := m.Capture("only", 0)
		if strings.Contains(out, "x") {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("never observed output via session-name resolution")
}

func TestManager_GetSessionWithNoWindowsFallsThrough(t *testing.T) {
	// Empty groups entry shouldn't match.
	m, _, _, _ := newTestManager(t)
	m.groups["ghost"] = nil
	if _, err := m.get("ghost"); err == nil {
		t.Fatal("expected not-found")
	}
}

func TestManager_GetSessionEntryButMissingWindow(t *testing.T) {
	// groups names a window that isn't in sessions map (corruption guard).
	m, _, _, _ := newTestManager(t)
	m.groups["x"] = []string{"wgone"}
	if _, err := m.get("x"); err == nil {
		t.Fatal("expected not-found")
	}
}

func TestManager_Wait_MatchOnTick(t *testing.T) {
	m, get, _, clk := newTestManager(t)
	_, _ = m.New("p", "w")
	get()[0].emit("PATTERN_MATCH\n")

	done := make(chan error, 1)
	go func() { done <- m.Wait("p:w", "PATTERN_MATCH", time.Hour) }()
	// Give pump a moment to process emit, then drive tick.
	time.Sleep(20 * time.Millisecond)
	clk.tick()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait err %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wait did not return on match")
	}
}

func TestManager_Wait_Timeout(t *testing.T) {
	m, _, _, clk := newTestManager(t)
	_, _ = m.New("p", "w")
	done := make(chan error, 1)
	go func() { done <- m.Wait("p:w", "NEVER", time.Hour) }()
	clk.timeout()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "timeout") {
			t.Fatalf("err %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not timeout")
	}
}

func TestManager_Wait_ProcessExitedNoMatch(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	done := make(chan error, 1)
	go func() { done <- m.Wait("p:w", "NEVER", time.Hour) }()
	get()[0].emitAndClose("nothing here")
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "exited") {
			t.Fatalf("err %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not return on exit")
	}
}

func TestManager_Wait_ProcessExitedWithMatch(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	done := make(chan error, 1)
	go func() { done <- m.Wait("p:w", "FINAL", time.Hour) }()
	get()[0].emitAndClose("FINAL_OUTPUT")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("err %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not return on exit")
	}
}

func TestManager_Wait_BadPattern(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	if err := m.Wait("p:w", "[invalid", time.Hour); err == nil {
		t.Fatal("expected regex error")
	}
}

func TestManager_KillSession(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	_, _ = m.New("p", "w1")
	_, _ = m.NewWindow("p", "w2", "")
	if err := m.Kill("p"); err != nil {
		t.Fatal(err)
	}
	if got := m.List(""); len(got) != 0 {
		t.Fatalf("list %v", got)
	}
}

func TestManager_KillUnknown(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	if err := m.Kill("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestManager_KillWindowUnknown(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	if err := m.KillWindow("p", "w"); err == nil {
		t.Fatal("expected error")
	}
}

// TestManager_KillReturnsErrorAggregate exercises the multi-error join
// branch of Kill by deleting one of the windows out from under it so the
// per-window KillWindow call fails.
func TestManager_KillReturnsErrorAggregate(t *testing.T) {
	m, _, _, _ := newTestManager(t)
	_, _ = m.New("p", "w1")
	_, _ = m.NewWindow("p", "w2", "")
	// Race: snapshot windows, then remove one from sessions map directly.
	m.mu.Lock()
	delete(m.sessions, "p:w2")
	m.mu.Unlock()
	if err := m.Kill("p"); err == nil {
		t.Fatal("expected aggregate error")
	}
}

func TestManager_AliveBecomesFalseOnExit(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	if !m.Alive("p:w") {
		t.Fatal("should be alive")
	}
	get()[0].emitAndClose("")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !m.Alive("p:w") {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("Alive never became false")
}

func TestManager_PumpHandlesNonEOFError(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	get()[0].closeWithErr(errors.New("read fail"))
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !m.Alive("p:w") {
			if get()[0].waitErr != nil {
				_ = get()[0].waitErr // explicit — keep linter happy
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("pump did not exit on read error")
}
