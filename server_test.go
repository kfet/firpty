package firpty

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// shortSock returns a /tmp-based socket path (macOS limits AF_UNIX paths to
// 104 bytes, and t.TempDir() can exceed that).
var sockCounter int64

func shortSock(t *testing.T) string {
	t.Helper()
	n := atomic.AddInt64(&sockCounter, 1)
	p := filepath.Join(os.TempDir(), fmt.Sprintf("firpty-%d-%d.sock", os.Getpid(), n))
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

// withTestServer spins up a Server bound to a tmp socket, with a Manager
// using fake starter + clock. Returns the client and helpers.
func withTestServer(t *testing.T) (*Client, *Server, func() []*fakeProc, *fakeClock) {
	t.Helper()
	sock := shortSock(t)

	starter, get, _ := recordingStarter()
	clk := newFakeClock()
	mgr := NewManager(WithStarter(starter), WithClock(clk), WithTickInterval(time.Millisecond))

	srv, err := NewServer(sock, mgr)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })

	return &Client{SocketPath: sock, DialTimeout: time.Second, CallDeadline: 2 * time.Second}, srv, get, clk
}

func TestServer_NewAndAddrAndManager(t *testing.T) {
	cli, srv, _, _ := withTestServer(t)
	if srv.Manager() == nil {
		t.Fatal("Manager() nil")
	}
	if srv.Addr() == nil {
		t.Fatal("Addr() nil")
	}
	resp, err := cli.Call("new", map[string]string{"session": "s1", "window": "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("error: %s", resp.Error)
	}
	var r struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(resp.Result, &r)
	if r.Name != "s1:w1" {
		t.Fatalf("name %q", r.Name)
	}
}

func TestServer_NewServer_NilManager(t *testing.T) {
	sock := shortSock(t)
	srv, err := NewServer(sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if srv.Manager() == nil {
		t.Fatal("manager not auto-created")
	}
}

func TestServer_NewServer_ListenError(t *testing.T) {
	orig := netListen
	defer func() { netListen = orig }()
	netListen = func(network, addr string) (net.Listener, error) {
		return nil, errors.New("listen blew up")
	}
	if _, err := NewServer("/tmp/whatever.sock", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestServer_AllDispatchHappyPaths(t *testing.T) {
	cli, _, get, clk := withTestServer(t)

	// new
	doCall(t, cli, "new", map[string]string{"session": "p", "window": "w"})
	// new_window
	doCall(t, cli, "new_window", map[string]string{"session": "p", "window": "w2", "command": "echo hi"})
	// send
	doCall(t, cli, "send", map[string]string{"target": "p:w", "text": "hello"})
	// send_raw
	doCall(t, cli, "send_raw", map[string]string{"target": "p:w", "data": "x"})

	// Make pump observe data so Capture is non-empty.
	procs := get()
	procs[0].emit("HELLO\n")
	time.Sleep(20 * time.Millisecond)

	// capture (default lines)
	resp := doCall(t, cli, "capture", map[string]any{"target": "p:w"})
	var capR struct {
		Output string `json:"output"`
	}
	_ = json.Unmarshal(resp.Result, &capR)
	if !strings.Contains(capR.Output, "HELLO") {
		t.Fatalf("capture %q", capR.Output)
	}

	// capture explicit lines
	doCall(t, cli, "capture", map[string]any{"target": "p:w", "lines": 5})

	// list (all)
	resp = doCall(t, cli, "list", map[string]string{})
	var items []string
	_ = json.Unmarshal(resp.Result, &items)
	if len(items) != 1 || items[0] != "p" {
		t.Fatalf("list %v", items)
	}
	// list specific session
	resp = doCall(t, cli, "list", map[string]string{"session": "p"})
	_ = json.Unmarshal(resp.Result, &items)
	if len(items) != 2 {
		t.Fatalf("windows %v", items)
	}

	// alive
	resp = doCall(t, cli, "alive", map[string]string{"target": "p:w"})
	var aliveR struct {
		Alive bool `json:"alive"`
	}
	_ = json.Unmarshal(resp.Result, &aliveR)
	if !aliveR.Alive {
		t.Fatal("not alive")
	}

	// wait — fire a tick on the fake clock to make it match
	go func() {
		time.Sleep(20 * time.Millisecond)
		clk.tick()
	}()
	doCall(t, cli, "wait", map[string]any{"target": "p:w", "pattern": "HELLO", "timeout": 5})

	// wait with default timeout (0 → 15s; fire tick immediately to match)
	go func() {
		time.Sleep(20 * time.Millisecond)
		clk.tick()
	}()
	doCall(t, cli, "wait", map[string]any{"target": "p:w", "pattern": "HELLO"})

	// kill_window
	doCall(t, cli, "kill_window", map[string]string{"session": "p", "window": "w2"})

	// kill (session)
	doCall(t, cli, "kill", map[string]string{"session": "p"})
}

func TestServer_DispatchErrorBranches(t *testing.T) {
	cli, _, _, _ := withTestServer(t)

	cases := []struct {
		method string
		params any
	}{
		{"new", map[string]string{}},                                 // empty session → manager error
		{"new_window", map[string]string{"session": "x"}},            // empty window
		{"send", map[string]string{"target": "nope", "text": "x"}},   // no such
		{"send_raw", map[string]string{"target": "nope", "data": ""}},
		{"capture", map[string]any{"target": "nope", "lines": 5}},
		{"wait", map[string]any{"target": "nope", "pattern": ".", "timeout": 1}},
		{"kill", map[string]string{"session": "nope"}},
		{"kill_window", map[string]string{"session": "nope", "window": "x"}},
	}
	for _, c := range cases {
		resp, err := cli.Call(c.method, c.params)
		if err != nil {
			t.Fatalf("%s call: %v", c.method, err)
		}
		if resp.Error == "" {
			t.Fatalf("%s: expected error response, got %s", c.method, string(resp.Result))
		}
	}
}

func TestServer_UnknownMethod(t *testing.T) {
	cli, _, _, _ := withTestServer(t)
	resp, err := cli.Call("nosuch", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Error, "unknown method") {
		t.Fatalf("err %q", resp.Error)
	}
}

func TestServer_AliveOnUnknownTarget(t *testing.T) {
	cli, _, _, _ := withTestServer(t)
	resp, err := cli.Call("alive", map[string]string{"target": "nope"})
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Alive bool `json:"alive"`
	}
	_ = json.Unmarshal(resp.Result, &r)
	if r.Alive {
		t.Fatal("should be dead")
	}
}

func TestServer_Shutdown(t *testing.T) {
	cli, srv, _, _ := withTestServer(t)
	srv.SetShutdownDelay(0)
	doCall(t, cli, "new", map[string]string{"session": "p", "window": "w"})
	doCall(t, cli, "shutdown", nil)

	// Server should close shortly.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("unix", srv.Addr().String(), 50*time.Millisecond)
		if err != nil {
			return
		}
		_ = c.Close()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server did not shut down")
}

func TestServer_HandleConnDecodeError(t *testing.T) {
	cli, _, _, _ := withTestServer(t)
	// Connect raw and send junk → decoder fails → handleConn returns silently.
	conn, err := net.DialTimeout("unix", cli.SocketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("not json\n"))
	_ = conn.Close()
	// Subsequent valid call still works (pool of new conns).
	doCall(t, cli, "list", map[string]string{})
}

func TestServer_ServeReturnsOnClose(t *testing.T) {
	sock := shortSock(t)
	srv, err := NewServer(sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()
	_ = srv.Close()
	select {
	case e := <-errCh:
		if e == nil {
			t.Fatal("Serve should return non-nil after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestClient_DialError(t *testing.T) {
	cli := &Client{SocketPath: "/no/such/socket", DialTimeout: 50 * time.Millisecond}
	if _, err := cli.Call("ping", nil); err == nil {
		t.Fatal("expected dial error")
	}
}

func TestClient_DialErrorViaInjection(t *testing.T) {
	orig := netDialTimeout
	defer func() { netDialTimeout = orig }()
	netDialTimeout = func(string, string, time.Duration) (net.Conn, error) {
		return nil, errors.New("dial fail")
	}
	cli := &Client{SocketPath: "/x"}
	if _, err := cli.Call("m", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestClient_DefaultsApply(t *testing.T) {
	// Cover the "DialTimeout==0 / CallDeadline==0" branches by using a real
	// server and a client without overrides.
	sock := shortSock(t)
	starter, _, _ := recordingStarter()
	mgr := NewManager(WithStarter(starter), WithClock(newFakeClock()), WithTickInterval(time.Millisecond))
	srv, err := NewServer(sock, mgr)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go func() { _ = srv.Serve() }()

	cli := &Client{SocketPath: sock} // no overrides
	doCall(t, cli, "list", map[string]string{})
}

func TestClient_ServerClosesMidResponse(t *testing.T) {
	// Server drains the request fully, then closes without writing a
	// response → client's json decoder gets io.EOF, mapped to "server
	// closed connection".
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		var req Request
		_ = json.NewDecoder(c).Decode(&req)
		_ = c.Close()
	}()

	cli := &Client{SocketPath: sock, DialTimeout: time.Second, CallDeadline: time.Second}
	_, err = cli.Call("anything", map[string]string{"k": "v"})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("err %v", err)
	}
	wg.Wait()
}

func TestClient_EncodeError(t *testing.T) {
	// Use net.Pipe and close the read end so that Write() — and thus
	// json.Encode — fails synchronously. Hits the "encode" error branch
	// of Call.
	orig := netDialTimeout
	defer func() { netDialTimeout = orig }()
	a, b := net.Pipe()
	_ = b.Close() // closing the peer makes a.Write fail with io.ErrClosedPipe
	netDialTimeout = func(string, string, time.Duration) (net.Conn, error) { return a, nil }

	cli := &Client{SocketPath: "/x", DialTimeout: time.Second, CallDeadline: time.Second}
	if _, err := cli.Call("m", map[string]string{"k": "v"}); err == nil {
		t.Fatal("expected encode error")
	}
}

func TestClient_ServerHardCloses(t *testing.T) {
	// Server closes immediately without reading anything; client's Encode
	// fails (broken pipe). Covers the encode-error branch of Call.
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_ = c.Close()
	}()

	cli := &Client{SocketPath: sock, DialTimeout: time.Second, CallDeadline: time.Second}
	_, err = cli.Call("anything", nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	wg.Wait()
}

func TestClient_ServerSendsBadJSON(t *testing.T) {
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		// Drain request
		buf := make([]byte, 1024)
		_, _ = c.Read(buf)
		// Send garbage that's not EOF but also not valid JSON.
		_, _ = c.Write([]byte("{not-json"))
		_ = c.Close()
	}()
	cli := &Client{SocketPath: sock, DialTimeout: time.Second, CallDeadline: time.Second}
	if _, err := cli.Call("x", nil); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestClient_MarshalError(t *testing.T) {
	cli := &Client{SocketPath: "/tmp/nope", DialTimeout: time.Millisecond}
	// Channel can't be marshalled — but we never reach marshal because dial
	// happens first. So instead intercept dial to succeed with a fake conn.
	orig := netDialTimeout
	defer func() { netDialTimeout = orig }()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	netDialTimeout = func(string, string, time.Duration) (net.Conn, error) { return a, nil }
	if _, err := cli.Call("m", make(chan int)); err == nil {
		t.Fatal("expected marshal error")
	}
}

func TestServer_OkRespMarshalError(t *testing.T) {
	// Hit okResp's err path by passing a value that json can't marshal.
	resp := okResp(make(chan int))
	if resp.Error == "" {
		t.Fatal("expected marshal error response")
	}
}

func TestServer_ErrResp(t *testing.T) {
	resp := errResp(errors.New("x"))
	if resp.Error != "x" {
		t.Fatalf("got %q", resp.Error)
	}
}

func TestDefaultSocketPath_FirptyEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FIRPTY_SOCKET_DIR", dir)
	t.Setenv("FIR_PTY_SOCKET_DIR", "")
	got := DefaultSocketPath()
	if got != filepath.Join(dir, "pty.sock") {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultSocketPath_LegacyEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FIRPTY_SOCKET_DIR", "")
	t.Setenv("FIR_PTY_SOCKET_DIR", dir)
	got := DefaultSocketPath()
	if got != filepath.Join(dir, "pty.sock") {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultSocketPath_Default(t *testing.T) {
	t.Setenv("FIRPTY_SOCKET_DIR", "")
	t.Setenv("FIR_PTY_SOCKET_DIR", "")
	got := DefaultSocketPath()
	if !strings.HasSuffix(got, "pty.sock") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, string(os.PathSeparator)+"firpty"+string(os.PathSeparator)) {
		t.Fatalf("expected firpty subdir, got %q", got)
	}
}

func doCall(t *testing.T, cli *Client, method string, params any) *Response {
	t.Helper()
	resp, err := cli.Call(method, params)
	if err != nil {
		t.Fatalf("call %s err: %v", method, err)
	}
	if resp.Error != "" {
		t.Fatalf("response %s err: %s", method, resp.Error)
	}
	return resp
}

func mustOK(t *testing.T, resp *Response, err error) *Response {
	t.Helper()
	if err != nil {
		t.Fatalf("call err: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("response err: %s", resp.Error)
	}
	return resp
}
