// Command firpty is a tiny CLI front-end for the firpty server. It mirrors
// the historical "fir pty …" subcommand surface so existing shell helpers
// (notably the tmux-driver skill) keep working when the firpty binary is on
// PATH instead of fir.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/kfet/firpty"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: firpty <command> [args...]")
		fmt.Fprintln(stderr, "commands: serve, new, win, send, sendraw, capture, wait, list, kill, killwin, alive, shutdown")
		return 1
	}

	cmd, rest := args[0], args[1:]

	if cmd == "serve" {
		sock := firpty.DefaultSocketPath()
		srv, err := firpty.NewServer(sock, nil)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "firpty server listening on %s\n", sock)
		fmt.Fprintln(stdout, sock)
		_ = srv.Serve()
		return 0
	}

	client := &firpty.Client{SocketPath: firpty.DefaultSocketPath()}
	return runClient(client, cmd, rest, stdout, stderr)
}

func runClient(client *firpty.Client, cmd string, args []string, stdout, stderr *os.File) int {
	switch cmd {
	case "new":
		if !need(args, 1, "firpty new NAME [WINDOW]", stderr) {
			return 1
		}
		win := "shell"
		if len(args) > 1 {
			win = args[1]
		}
		return call(client, "new", map[string]string{"session": args[0], "window": win}, stderr)

	case "win":
		if !need(args, 2, "firpty win NAME WINDOW [CMD]", stderr) {
			return 1
		}
		p := map[string]string{"session": args[0], "window": args[1]}
		if len(args) > 2 {
			p["command"] = args[2]
		}
		return call(client, "new_window", p, stderr)

	case "send":
		if !need(args, 2, "firpty send TARGET TEXT", stderr) {
			return 1
		}
		return call(client, "send", map[string]string{"target": args[0], "text": args[1]}, stderr)

	case "sendraw":
		if !need(args, 2, "firpty sendraw TARGET DATA", stderr) {
			return 1
		}
		return call(client, "send_raw", map[string]string{"target": args[0], "data": args[1]}, stderr)

	case "capture":
		if !need(args, 1, "firpty capture TARGET [LINES]", stderr) {
			return 1
		}
		lines := 200
		if len(args) > 1 {
			if n, err := strconv.Atoi(args[1]); err == nil {
				lines = n
			}
		}
		resp, err := client.Call("capture", map[string]any{"target": args[0], "lines": lines})
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if resp.Error != "" {
			fmt.Fprintf(stderr, "error: %s\n", resp.Error)
			return 1
		}
		var result struct {
			Output string `json:"output"`
		}
		_ = json.Unmarshal(resp.Result, &result)
		fmt.Fprint(stdout, result.Output)
		return 0

	case "wait":
		if !need(args, 2, "firpty wait TARGET PATTERN [TIMEOUT]", stderr) {
			return 1
		}
		timeout := 15
		if len(args) > 2 {
			if n, err := strconv.Atoi(args[2]); err == nil {
				timeout = n
			}
		}
		return call(client, "wait", map[string]any{"target": args[0], "pattern": args[1], "timeout": timeout}, stderr)

	case "list":
		var session string
		if len(args) > 0 {
			session = args[0]
		}
		resp, err := client.Call("list", map[string]string{"session": session})
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if resp.Error != "" {
			fmt.Fprintf(stderr, "error: %s\n", resp.Error)
			return 1
		}
		var items []string
		_ = json.Unmarshal(resp.Result, &items)
		for _, it := range items {
			fmt.Fprintln(stdout, it)
		}
		return 0

	case "kill":
		if !need(args, 1, "firpty kill NAME", stderr) {
			return 1
		}
		return call(client, "kill", map[string]string{"session": args[0]}, stderr)

	case "killwin":
		if !need(args, 2, "firpty killwin NAME WINDOW", stderr) {
			return 1
		}
		return call(client, "kill_window", map[string]string{"session": args[0], "window": args[1]}, stderr)

	case "alive":
		if !need(args, 1, "firpty alive TARGET", stderr) {
			return 1
		}
		resp, err := client.Call("alive", map[string]string{"target": args[0]})
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if resp.Error != "" {
			fmt.Fprintf(stderr, "error: %s\n", resp.Error)
			return 1
		}
		var result struct {
			Alive bool `json:"alive"`
		}
		_ = json.Unmarshal(resp.Result, &result)
		if result.Alive {
			fmt.Fprintln(stdout, "alive")
			return 0
		}
		fmt.Fprintln(stdout, "dead")
		return 1

	case "shutdown":
		return call(client, "shutdown", nil, stderr)
	}
	fmt.Fprintf(stderr, "unknown firpty command: %s\n", cmd)
	return 1
}

func need(args []string, n int, usage string, stderr *os.File) bool {
	if len(args) < n {
		fmt.Fprintf(stderr, "usage: %s\n", usage)
		return false
	}
	return true
}

func call(client *firpty.Client, method string, params any, stderr *os.File) int {
	resp, err := client.Call(method, params)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if resp.Error != "" {
		fmt.Fprintf(stderr, "error: %s\n", resp.Error)
		return 1
	}
	return 0
}
