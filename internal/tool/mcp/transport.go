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

// Transport 抽象 MCP client 与 Server 之间的字节通道。
type Transport interface {
    Start(ctx context.Context) error
    Send(message []byte) error
    Receive(timeout time.Duration) ([]byte, error)
    Close() error
}

// stdioTransport 启动本地子进程，并经其 stdin/stdout 传输换行分隔的
// JSON-RPC 消息。
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

// Start 构建并启动子进程。当 Close 被调用或 ctx 被取消时，子进程会被 kill。
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

    // 排空 stderr，避免子进程因 pipe 满而阻塞；当前直接丢弃。
    go io.Copy(io.Discard, stderr)
    return nil
}

// Send 写入一条以换行符结尾的 JSON-RPC 消息。
func (t *stdioTransport) Send(message []byte) error {
    t.mu.Lock()
    defer t.mu.Unlock()
    if t.stdin == nil {
        return fmt.Errorf("transport not started")
    }
    _, err := t.stdin.Write(append(message, '\n'))
    return err
}

// Receive 从 stdout 读取一行换行符结尾的数据，并遵循 timeout。
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
        // 每条消息最多 4 MB。
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

// Close 终止仍在运行的子进程。
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
