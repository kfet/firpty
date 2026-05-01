package firpty

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Request is a JSON-RPC-style request to the firpty server.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Response is a JSON-RPC-style response from the firpty server.
type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Server runs a Manager behind a Unix socket.
type Server struct {
	mgr           *Manager
	listener      net.Listener
	wg            sync.WaitGroup
	shutdownDelay time.Duration
}

// netListen / netDialTimeout are package-level vars so tests can simulate
// listen/dial failures.
var (
	netListen      = net.Listen
	netDialTimeout = net.DialTimeout
)

// DefaultSocketPath returns the default Unix socket path for the firpty
// server. Honours $FIRPTY_SOCKET_DIR (preferred) and the legacy
// $FIR_PTY_SOCKET_DIR.
func DefaultSocketPath() string {
	dir := os.Getenv("FIRPTY_SOCKET_DIR")
	if dir == "" {
		dir = os.Getenv("FIR_PTY_SOCKET_DIR")
	}
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "firpty")
	}
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "pty.sock")
}

// NewServer creates a Server bound to sockPath with the given Manager.
// If mgr is nil, a default Manager is created.
func NewServer(sockPath string, mgr *Manager) (*Server, error) {
	if mgr == nil {
		mgr = NewManager()
	}
	_ = os.Remove(sockPath) // remove stale socket
	ln, err := netListen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", sockPath, err)
	}
	return &Server{mgr: mgr, listener: ln, shutdownDelay: 100 * time.Millisecond}, nil
}

// SetShutdownDelay overrides the post-shutdown listener-close delay (default
// 100ms). Tests use 0 for determinism.
func (s *Server) SetShutdownDelay(d time.Duration) { s.shutdownDelay = d }

// Manager exposes the underlying Manager (useful for tests / advanced use).
func (s *Server) Manager() *Manager { return s.mgr }

// Addr returns the listener's address.
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Serve accepts connections until the listener is closed. It always returns
// a non-nil error (typically the Accept error from a closed listener).
func (s *Server) Serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

// Close stops accepting and waits for in-flight handlers.
func (s *Server) Close() error {
	err := s.listener.Close()
	s.wg.Wait()
	return err
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	resp := s.dispatch(req)
	_ = json.NewEncoder(conn).Encode(resp)
}

func (s *Server) dispatch(req Request) Response {
	switch req.Method {
	case "new":
		var p struct {
			Session string `json:"session"`
			Window  string `json:"window"`
		}
		_ = json.Unmarshal(req.Params, &p)
		sess, err := s.mgr.New(p.Session, p.Window)
		if err != nil {
			return errResp(err)
		}
		return okResp(map[string]string{"name": sess.Name})

	case "new_window":
		var p struct {
			Session string `json:"session"`
			Window  string `json:"window"`
			Command string `json:"command"`
		}
		_ = json.Unmarshal(req.Params, &p)
		sess, err := s.mgr.NewWindow(p.Session, p.Window, p.Command)
		if err != nil {
			return errResp(err)
		}
		return okResp(map[string]string{"name": sess.Name})

	case "send":
		var p struct {
			Target string `json:"target"`
			Text   string `json:"text"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if err := s.mgr.Send(p.Target, p.Text); err != nil {
			return errResp(err)
		}
		return okResp("ok")

	case "send_raw":
		var p struct {
			Target string `json:"target"`
			Data   string `json:"data"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if err := s.mgr.SendRaw(p.Target, []byte(p.Data)); err != nil {
			return errResp(err)
		}
		return okResp("ok")

	case "capture":
		var p struct {
			Target string `json:"target"`
			Lines  int    `json:"lines"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.Lines == 0 {
			p.Lines = 200
		}
		out, err := s.mgr.Capture(p.Target, p.Lines)
		if err != nil {
			return errResp(err)
		}
		return okResp(map[string]string{"output": out})

	case "wait":
		var p struct {
			Target  string `json:"target"`
			Pattern string `json:"pattern"`
			Timeout int    `json:"timeout"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.Timeout == 0 {
			p.Timeout = 15
		}
		if err := s.mgr.Wait(p.Target, p.Pattern, time.Duration(p.Timeout)*time.Second); err != nil {
			return errResp(err)
		}
		return okResp("ok")

	case "list":
		var p struct {
			Session string `json:"session"`
		}
		_ = json.Unmarshal(req.Params, &p)
		return okResp(s.mgr.List(p.Session))

	case "kill":
		var p struct {
			Session string `json:"session"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if err := s.mgr.Kill(p.Session); err != nil {
			return errResp(err)
		}
		return okResp("ok")

	case "kill_window":
		var p struct {
			Session string `json:"session"`
			Window  string `json:"window"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if err := s.mgr.KillWindow(p.Session, p.Window); err != nil {
			return errResp(err)
		}
		return okResp("ok")

	case "alive":
		var p struct {
			Target string `json:"target"`
		}
		_ = json.Unmarshal(req.Params, &p)
		return okResp(map[string]bool{"alive": s.mgr.Alive(p.Target)})

	case "shutdown":
		for _, name := range s.mgr.List("") {
			_ = s.mgr.Kill(name)
		}
		// Close the listener after a brief delay so the current response
		// has time to flush. We deliberately do NOT wg.Wait here — that
		// would deadlock the in-flight handler. Server.Close() (called by
		// the embedder) does the wait.
		go func() {
			time.Sleep(s.shutdownDelay)
			_ = s.listener.Close()
		}()
		return okResp("ok")

	default:
		return Response{Error: fmt.Sprintf("unknown method: %s", req.Method)}
	}
}

func errResp(err error) Response { return Response{Error: err.Error()} }

func okResp(v any) Response {
	b, err := json.Marshal(v)
	if err != nil {
		return Response{Error: err.Error()}
	}
	return Response{Result: b}
}

// Client is a single-shot client for the firpty server.
type Client struct {
	SocketPath string
	// DialTimeout overrides the default 2s dial timeout (used in tests).
	DialTimeout time.Duration
	// CallDeadline overrides the per-call deadline (used in tests).
	CallDeadline time.Duration
}

// Call sends method+params and returns the response.
func (c *Client) Call(method string, params any) (*Response, error) {
	dialTO := c.DialTimeout
	if dialTO == 0 {
		dialTO = 2 * time.Second
	}
	conn, err := netDialTimeout("unix", c.SocketPath, dialTO)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", c.SocketPath, err)
	}
	defer conn.Close()

	deadline := c.CallDeadline
	if deadline == 0 {
		deadline = 120 * time.Second
	}
	_ = conn.SetDeadline(time.Now().Add(deadline))

	paramBytes, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(conn).Encode(Request{Method: method, Params: paramBytes}); err != nil {
		return nil, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("server closed connection")
		}
		return nil, err
	}
	return &resp, nil
}
