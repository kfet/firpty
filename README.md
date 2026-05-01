# firpty

A Go-native, tmux-free terminal multiplexer for driving interactive
processes. Extracted from the [fir](https://github.com/kfet/fir)
coding-agent harness, where it serves as a fallback for the `tmux-driver`
skill on systems without tmux.

## Features

- Named sessions with multiple windows (`session:window` addressing)
- Send text + Enter, send raw bytes, capture rendered output
- Wait for a regex pattern with timeout
- Built-in VT100/ANSI screen emulator with scrollback
- Unix-socket JSON-RPC server (`firpty serve`) + tiny CLI client
- 100% unit-test coverage of the core package (real PTY/exec wrappers
  excluded via `.covignore`)

## Install

```bash
go install github.com/kfet/firpty/cmd/firpty@latest
```

## Use as a library

```go
m := firpty.NewManager()
sess, err := m.New("myproj", "shell")
_ = m.Send(sess.Name, "echo hello")
out, _ := m.Capture(sess.Name, 50)
fmt.Println(out)
_ = m.KillWindow("myproj", "shell")
```

## Use the CLI

```bash
firpty serve &                  # starts server on $FIRPTY_SOCKET_DIR/pty.sock
firpty new myproj shell
firpty send myproj 'echo hi'
firpty wait  myproj 'hi' 5
firpty capture myproj 50
firpty kill myproj
firpty shutdown
```

## Test

```bash
make test          # runs with -race -shuffle=on, gates on 100% coverage
make open_coverage # HTML report
```

## License

MIT — see [LICENSE](LICENSE).
