package mcp

import (
    "bytes"
    "encoding/json"
    "io"
    "sync"
    "testing"
)

// mockTransport 是一个仅用于测试的 transport，对每个请求都回放固定的一组
// 响应。它同时校验每条发出消息都以换行结尾。
type replayTransport struct {
    sent  *bytes.Buffer
    lines [][]byte
    mu    struct {
        sync.Mutex
        pos int
    }
}

func newReplayTransport(lines []string) *replayTransport {
    t := &replayTransport{sent: &bytes.Buffer{}}
    for _, l := range lines {
        t.lines = append(t.lines, []byte(l))
    }
    return t
}

// WAIT: 无法在函数内定义的 struct 中嵌入 sync.Mutex？其实可以，
// 但为了让包构建更简单，我们改用下方基于 channel 的 mock。

// 使用与 transport_test.go 相同的 pipe 模式重写测试。

func TestProxyToolNameAndParameters(t *testing.T) {
    cfg := ServerConfig{Name: "time"}
    def := ToolDefinition{
        Name:        "get_current_time",
        Description: "Returns current time",
        InputSchema: map[string]any{
            "type": "object",
        },
    }
    dummy := NewClient(&stdioTransport{})
    proxy := NewProxyTool(cfg.Namespace(), def, dummy)

    if got, want := proxy.Name(), "mcp__time__get_current_time"; got != want {
        t.Errorf("Name() = %q, want %q", got, want)
    }
    if got, want := proxy.Description(), "Returns current time"; got != want {
        t.Errorf("Description() = %q, want %q", got, want)
    }
    if got, want := proxy.Parameters()["type"], "object"; got != want {
        t.Errorf("Parameters() type = %v, want %v", got, want)
    }
}

func TestProxyToolExecute(t *testing.T) {
    inR, inW := io.Pipe()
    outR, outW := io.Pipe()

    tr := &stdioTransport{stdin: inW, stdout: outR, stderr: nil}
    client := NewClient(tr)

    // Fake MCP server：读取请求，用匹配的 id 回复。
    go func() {
        defer outW.Close()
        dec := json.NewDecoder(inR)
        for {
            var req struct {
                ID     int64  `json:"id"`
                Method string `json:"method"`
            }
            if err := dec.Decode(&req); err != nil {
                return
            }
            resp := map[string]any{
                "jsonrpc": "2.0",
                "id":      req.ID,
                "result": map[string]any{
                    "content": []map[string]any{
                        {"type": "text", "text": "hello from mcp"},
                    },
                },
            }
            b, _ := json.Marshal(resp)
            outW.Write(append(b, '\n'))
        }
    }()

    def := ToolDefinition{
        Name:        "greet",
        Description: "Greets",
        InputSchema: map[string]any{"type": "object"},
    }
    proxy := NewProxyTool("demo", def, client)

    out, err := proxy.Execute(map[string]any{"name": "mcp"})
    if err != nil {
        t.Fatalf("Execute: %v", err)
    }
    if got, want := out, "hello from mcp"; got != want {
        t.Errorf("Execute result = %q, want %q", got, want)
    }
}
