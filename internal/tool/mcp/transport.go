package mcp

import (
    "bufio"
    "context"
    "fmt"
    "io"
    "os"
    "os/exec"
    "sync"
    "time"
)

// Transport abstracts the byte channel between the MCP client and a Server.
type Transport interface {
    Start(ctx context.Context) error
    Send(message []byte) error
    Receive(timeout time.Duration) ([]byte, error)
    Close() error
}

// stdioTransport spawns a local child process and speaks newline-delimited
// JSON-RPC over its stdin/stdout.
type stdioTransport struct {
    cfg    ServerConfig
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout io.ReadCloser
    stderr io.ReadCloser
    mu     sync.Mutex
    closed bool
}

func newStdioTransport(cfg ServerConfig) *stdioTransport {
    return &stdioTransport{cfg: cfg}
}

// Start builds and starts the child process. The process is killed when Close
// is called or when ctx is cancelled.
func (t *stdioTransport) Start(ctx context.Context) error {
    t.mu.Lock()
    defer t.mu.Unlock()
    if t.closed {
        return fmt.Errorf("transport closed")
    }

    if t.cfg.Command == "" {
        return fmt.Errorf("stdio transport requires command")
    }

    cmd := exec.CommandContext(ctx, t.cfg.Command, t.cfg.Args...)
    for k, v := range t.cfg.Environment {
        cmd.Env = append(os.Environ(), k+"="+v)
    }

    stdin, err := cmd.StdinPipe()
    if err != nil {
        return fmt.Errorf("stdin pipe: %w", err)
    }
    stdout, err := cmd.StdoutPipe()
    if err != nil {
        stdin.Close()
        return fmt.Errorf("stdout pipe: %w", err)
    }
    stderr, err := cmd.StderrPipe()
    if err != nil {
        stdin.Close()
        stdout.Close()
        return fmt.Errorf("stderr pipe: %w", err)
    }

    if err := cmd.Start(); err != nil {
        stdin.Close()
        stdout.Close()
        stderr.Close()
        return fmt.Errorf("start command: %w", err)
    }

    t.cmd = cmd
    t.stdin = stdin
    t.stdout = stdout
    t.stderr = stderr

    // Drain stderr to prevent child from blocking on full pipe; discard for now.
    go io.Copy(io.Discard, stderr)
    return nil
}

// Send writes a single JSON-RPC message terminated by a newline.
func (t *stdioTransport) Send(message []byte) error {
    t.mu.Lock()
    defer t.mu.Unlock()
    if t.stdin == nil {
        return fmt.Errorf("transport not started")
    }
    _, err := t.stdin.Write(append(message, '\n'))
    return err
}

// Receive reads one newline-terminated line from stdout, respecting timeout.
func (t *stdioTransport) Receive(timeout time.Duration) ([]byte, error) {
    t.mu.Lock()
    r := t.stdout
    t.mu.Unlock()
    if r == nil {
        return nil, fmt.Errorf("transport not started")
    }

    done := make(chan struct{})
    var line []byte
    var readErr error
    go func() {
        defer close(done)
        scanner := bufio.NewScanner(r)
        // Allow up to 4 MB per message.
        scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
        if scanner.Scan() {
            line = scanner.Bytes()
        } else {
            if err := scanner.Err(); err != nil {
                readErr = err
            } else {
                readErr = io.EOF
            }
        }
    }()

    select {
    case <-done:
        if readErr != nil {
            return nil, readErr
        }
        return line, nil
    case <-time.After(timeout):
        return nil, fmt.Errorf("receive timeout")
    }
}

// Close terminates the child process if it is running.
func (t *stdioTransport) Close() error {
    t.mu.Lock()
    defer t.mu.Unlock()
    if t.closed {
        return nil
    }
    t.closed = true
    if t.stdin != nil {
        t.stdin.Close()
    }
    if t.cmd != nil && t.cmd.Process != nil {
        _ = t.cmd.Process.Kill()
    }
    return nil
}
