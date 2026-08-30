package firpty

import (
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"
)

// newTestManager builds a Manager wired with fakes and returns helpers.
func newTestManager(t *testing.T) (*Manager, func() []*fakeProc, func(error), *fakeClock) {
	t.Helper()
	starter, get, setFail := recordingStarter()
	clk := newFakeClock()
	m := NewManager(WithStarter(starter), WithClock(clk), WithTickInterval(time.Millisecond),
		WithKillGrace(50*time.Millisecond), WithDrainGrace(100*time.Millisecond))
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

// --- teardown -----------------------------------------------------------

// waitFor polls a condition to a deadline and fails loudly. Used to
// synchronise with the pump and reap goroutines.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// The ordinary path: a hangup is enough, and it goes to the GROUP.
func TestKillWindow_SignalsTheGroupWithSIGHUPFirst(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	if err := m.KillWindow("p", "w"); err != nil {
		t.Fatal(err)
	}
	if got := get()[0].sent(); len(got) != 1 || got[0] != syscall.SIGHUP {
		t.Fatalf("signals = %v, want [SIGHUP]", got)
	}
}

// A program that traps SIGHUP gets killGrace and then SIGKILL. Without the
// escalation it would outlive the window that owns it.
func TestKillWindow_EscalatesToSIGKILLWhenSIGHUPIsIgnored(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	get()[0].ignoreHUP.Store(true)
	if err := m.KillWindow("p", "w"); err != nil {
		t.Fatal(err)
	}
	got := get()[0].sent()
	if len(got) != 2 || got[0] != syscall.SIGHUP || got[1] != syscall.SIGKILL {
		t.Fatalf("signals = %v, want [SIGHUP SIGKILL]", got)
	}
	if m.Alive("p:w") {
		t.Fatal("the window survived SIGKILL")
	}
}

// The bug this release exists for. The program exits on its own, leaving a
// child holding the terminal. Killing only the leader — which is what
// KillWindow used to do, and what it would still do if teardown decided from
// the pid rather than from the terminal — leaves that child running with its
// terminal closed under it.
func TestKillWindow_ReachesChildrenThatOutlivedTheProgram(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	s, _ := m.New("p", "w")
	f := get()[0]
	f.exitLeavingSurvivor()
	waitFor(t, "the program to be reaped", func() bool { return !m.Alive("p:w") })

	done := make(chan error, 1)
	go func() { done <- m.KillWindow("p", "w") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("KillWindow hung: the terminal is held by a survivor and nothing bounded the wait")
	}
	if got := f.sent(); len(got) == 0 {
		t.Fatal("the survivor was never signalled: KillWindow orphaned it")
	}
	select {
	case <-s.done:
	default:
		t.Fatal("the terminal was never released")
	}
}

// The other half of the same decision. A window that has been reaped AND
// whose terminal has no holder left must be signalled with NOTHING: the pid
// is free for the kernel to reuse, so the group could be a stranger's.
func TestKillWindow_DoesNotSignalAWindowWithNothingLeftToKill(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	f := get()[0]
	f.emitAndClose("bye")
	waitFor(t, "the window to settle", func() bool {
		s, err := m.get("p:w")
		if err != nil {
			return false
		}
		return s.gone()
	})
	if err := m.KillWindow("p", "w"); err != nil {
		t.Fatal(err)
	}
	if got := f.sent(); len(got) != 0 {
		t.Fatalf("signalled a window with nothing left to kill: %v", got)
	}
}

// A descendant that called setsid(2) left the process group, so the signal
// never reaches it and it can hold the terminal indefinitely. Teardown must
// give up on the last bytes rather than block forever. The escape is modelled
// by a SignalGroup that does nothing, which is exactly what the kernel does
// with a signal aimed at a group the process is no longer in.
func TestKillWindow_StopsWaitingForATerminalNobodyWillRelease(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	f := get()[0]
	f.ignoreHUP.Store(true)
	f.exitLeavingSurvivor()
	f.mu.Lock()
	f.signalErr = errFake // signals land nowhere and are refused
	f.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- m.KillWindow("p", "w") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a refused signal should be reported")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("KillWindow hung on a terminal its signal could never free")
	}
}

// A refused signal is reported rather than swallowed: it means a program is
// still running with nobody driving it. Here both signals are refused — the
// hangup lands nowhere, so teardown escalates and that is refused too — and
// the caller hears about the last failure.
func TestKillWindow_ReportsARefusedSignal(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	f := get()[0]
	f.mu.Lock()
	f.signalErr = errFake
	f.mu.Unlock()
	err := m.KillWindow("p", "w")
	if err == nil || !strings.Contains(err.Error(), "fake error") {
		t.Fatalf("err = %v, want the refusal reported", err)
	}
	if got := get()[0].sent(); len(got) != 2 {
		t.Fatalf("signals = %v, want the refused hangup to be escalated", got)
	}
}

// Teardown runs once even when a window is reachable twice — Wait must not be
// called twice on the same child.
func TestKillWindow_IsIdempotent(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	s, _ := m.New("p", "w")
	if err := m.KillWindow("p", "w"); err != nil {
		t.Fatal(err)
	}
	if err := m.teardown(s); err != nil {
		t.Fatal(err)
	}
	if got := get()[0].sent(); len(got) != 1 {
		t.Fatalf("teardown ran twice: %v", got)
	}
}

// A window whose program exits on its own is reaped without anybody killing
// it. Before the reaper existed it stayed a zombie for the life of the
// Manager.
func TestWindowIsReapedWithoutBeingKilled(t *testing.T) {
	m, get, _, _ := newTestManager(t)
	_, _ = m.New("p", "w")
	get()[0].emitAndClose("done")
	waitFor(t, "the program to be reaped", func() bool { return !m.Alive("p:w") })
}

func TestInterpretKill(t *testing.T) {
	if err := interpretKill(nil); err != nil {
		t.Errorf("interpretKill(nil) = %v", err)
	}
	if err := interpretKill(syscall.ESRCH); err != nil {
		t.Errorf("interpretKill(ESRCH) = %v, want nil: an empty group is the goal, not a failure", err)
	}
	if err := interpretKill(syscall.EPERM); err != syscall.EPERM {
		t.Errorf("interpretKill(EPERM) = %v, want it reported", err)
	}
}
