package firpty

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The rest of the suite drives fakes, which is right for the teardown policy
// but cannot say anything about the wiring underneath it: whether the child
// the default starter spawns really leads a process group, and whether a
// signal aimed at that group really reaches what the child started. Those are
// kernel facts, and the only way to check them is against a real process.
//
// This is the test the old KillWindow would have failed. It SIGKILLed the
// session leader and nothing else, so the grandchild below outlived it.
func TestRealPTY_TeardownReachesGrandchildren(t *testing.T) {
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skipf("ps not available: %v", err)
	}
	// The default starter runs $SHELL. Pin it, so this test measures the
	// kernel's behaviour rather than the developer's login shell's job
	// control.
	t.Setenv("SHELL", "/bin/sh")

	dir := t.TempDir()
	survivor := filepath.Join(dir, "survivor.sh")
	// The survivor ignores SIGHUP, so the kernel's own hangup — delivered to
	// the foreground process group when the session leader dies — does not
	// clean it up for us. Only the group kill will. That is the shape of a
	// language server or a build the program shelled out to.
	if err := os.WriteFile(survivor, []byte("#!/bin/sh\ntrap '' HUP\n: > \"$1\"\nsleep 300\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(dir, "ready")
	// The leader must not exit until the survivor has installed its trap —
	// which is why the survivor, not the leader, creates the ready file.
	// Otherwise the leader exits first, the kernel's hangup arrives before
	// `trap` has run, the survivor dies on its own, and the test proves
	// nothing.
	command := survivor + " " + ready + " &\n" +
		"n=0; while [ ! -f " + ready + " ] && [ $n -lt 2000 ]; do sleep 0.01; n=$((n+1)); done\n" +
		"printf SPAWNED\nexit 0\n"

	m := NewManager()
	_, err := m.NewWindow("real", "w", command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Kill("real") })

	waitFor(t, "the leader to print and exit", func() bool { return !m.Alive("real:w") })
	// The premise: the survivor outlived the program that started it.
	// Without this the test would pass on a window that had already emptied
	// itself.
	if !survivorRunning(t, survivor) {
		t.Fatal("the survivor was already gone, so this test proves nothing")
	}

	done := make(chan error, 1)
	go func() { done <- m.KillWindow("real", "w") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("KillWindow() = %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("KillWindow hung on a terminal held by a survivor")
	}

	// Bounded rather than immediate: a killed survivor is a zombie until its
	// new parent reaps it, and until then it is still visible.
	deadline := time.Now().Add(20 * time.Second)
	for survivorRunning(t, survivor) {
		if time.Now().After(deadline) {
			t.Fatal("the survivor outlived KillWindow: the window orphaned it")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// survivorRunning reports whether any process still names the script path.
// The path is inside the test's own temp dir, so it cannot collide with
// anything else on the machine.
func survivorRunning(t *testing.T, path string) bool {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-o", "args=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, path) && !strings.Contains(line, "<defunct>") {
			return true
		}
	}
	return false
}
