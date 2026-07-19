package mcp

import (
    "context"
    "encoding/json"
    "io"
    "testing"
    "time"

    "github.com/anmingwei/multi-agent-platform/internal/tool"
)

// fakeTransport 是仅用于测试的 Transport，会回放一个脚本化的 MCP server。
type fakeTransport struct {
    stdin  io.WriteCloser
    stdout io.ReadCloser
}

func startFakeMCP(t *testing.T, handlers map[string]func(id int64, params json.RawMessage) []byte) *fakeTransport {
    t.Helper()
    inR, inW := io.Pipe()
    outR, outW := io.Pipe()

    go func() {
        defer outW.Close()
        dec := json.NewDecoder(inR)
        for {
            var req struct {
                ID     int64           `json:"id"`
                Method string          `json:"method"`
                Params json.RawMessage `json:"params"`
            }
            if err := dec.Decode(&req); err != nil {
                return
            }
            h, ok := handlers[req.Method]
            if !ok {
                continue
            }
            if id := req.ID; id != 0 || req.Method == "notifications/initialized" {
                // notification 没有 id，但我们的 handler 只对调用返回响应。
            }
            outW.Write(append(h(req.ID, req.Params), '\n'))
        }
    }()

    return &fakeTransport{stdin: inW, stdout: outR}
}

func (f *fakeTransport) Start(ctx context.Context) error { return nil }
func (f *fakeTransport) Send(message []byte) error       { _, err := f.stdin.Write(append(message, '\n')); return err }
func (f *fakeTransport) Receive(timeout time.Duration) ([]byte, error) {
    buf := make([]byte, 4096)
    n, err := f.stdout.Read(buf)
    if n > 0 {
        return buf[:n], nil
    }
    return nil, err
}
func (f *fakeTransport) Close() error { return f.stdin.Close() }

func TestLoaderLoadAndUnload(t *testing.T) {
    reg := tool.NewRegistry()
    loader := NewLoader(reg)

    ft := startFakeMCP(t, map[string]func(id int64, params json.RawMessage) []byte{
        "initialize": func(id int64, params json.RawMessage) []byte {
            b, _ := json.Marshal(map[string]any{
                "jsonrpc": "2.0",
                "id":      id,
                "result": map[string]any{
                    "protocolVersion": "2024-11-05",
                    "serverInfo":      map[string]any{"name": "demo", "version": "1.0"},
                    "capabilities":    map[string]any{},
                },
            })
            return b
        },
        "tools/list": func(id int64, params json.RawMessage) []byte {
            b, _ := json.Marshal(map[string]any{
                "jsonrpc": "2.0",
                "id":      id,
                "result": map[string]any{
                    "tools": []map[string]any{
                        {
                            "name":        "hello",
                            "description": "Says hello",
                            "inputSchema": map[string]any{"type": "object"},
                        },
                    },
                },
            })
            return b
        },
        "tools/call": func(id int64, params json.RawMessage) []byte {
            b, _ := json.Marshal(map[string]any{
                "jsonrpc": "2.0",
                "id":      id,
                "result": map[string]any{
                    "content": []map[string]any{
                        {"type": "text", "text": "hello"},
                    },
                },
            })
            return b
        },
    })

    cfg := ServerConfig{Name: "demo", Transport: "stdio", Enabled: true}
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := loader.LoadServerWithTransport(ctx, cfg, ft); err != nil {
        t.Fatalf("LoadServer: %v", err)
    }

    names := loader.LoadedNames()
    if len(names) != 1 || names[0] != "demo" {
        t.Fatalf("loaded names = %v, want [demo]", names)
    }

    if _, err := reg.Execute("mcp__demo__hello", map[string]any{}); err != nil {
        t.Fatalf("execute proxy: %v", err)
    }

    if err := loader.UnloadServer("demo"); err != nil {
        t.Fatalf("UnloadServer: %v", err)
    }

    if _, err := reg.Execute("mcp__demo__hello", map[string]any{}); err == nil {
        t.Fatalf("expected tool to be unregistered")
    }
}

func TestLoaderDisabledServer(t *testing.T) {
    reg := tool.NewRegistry()
    loader := NewLoader(reg)

    cfg := ServerConfig{Name: "off", Enabled: false}
    if err := loader.LoadServer(context.Background(), cfg); err != nil {
        t.Fatalf("LoadServer on disabled: %v", err)
    }
    if len(loader.LoadedNames()) != 0 {
        t.Fatalf("disabled server should not be loaded")
    }
}
