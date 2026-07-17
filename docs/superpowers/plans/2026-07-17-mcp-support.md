# MCP 支持实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为当前多 Agent 平台引入 MCP Client 适配层，支持通过配置接入外部 MCP Server；同时内置若干示例 MCP Server（时间/计算器/项目自述摘要），作为学习和使用范例。

**Architecture:** 不改动现有 `Tool` 接口和 `Registry` 语义，在 `internal/tool/mcp` 包中实现 MCP 协议客户端。每个外部或内部 MCP Server 启动后通过 `initialize` 协商能力，再用 `tools/list` 拉取工具列表，最后把每个 MCP Tool 包装为 `tool.Tool` 注册到主 Registry。MCP Server 配置从 `.env` 与 `MCP_SERVERS` 环境变量读取；内置示例 Server 作为项目内的独立 `go run` 可执行文件，便于本地演示。

**Tech Stack:** Go 1.25, modernc.org/sqlite, gorilla/websocket（已存在）, JSON-RPC over stdio / HTTP+SSE, 标准库。

---

## 约定

- 本计划所有新增代码写在 `D:/Claude-Code-MultiAgent` 仓库内。
- 每个 Task 独立可运行、可测试、可提交。
- 术语：
  - **MCP Server**: 独立进程/服务，遵循 Model Context Protocol（MCP）。
  - **MCP Client**: 平台侧连接到 Server 并转发调用的组件。
  - **代理工具**: 平台 Registry 中由 MCP Server 提供的 `tool.Tool` 实现。
  - **命名空间**: 用 `mcp__<server>__<tool>` 分隔不同 Server 的同名工具。

---

## File Structure

| 文件 | 职责 |
|---|---|
| `internal/tool/mcp/server.go` | MCP Server 配置结构、能力声明、生命周期状态 |
| `internal/tool/mcp/transport.go` | 传输层接口：Start、Send、Receive、Close、支持 stdio 与 HTTP+SSE（初版仅实现 stdio） |
| `internal/tool/mcp/client.go` | JSON-RPC 客户端：initialize、tools/list、tools/call、通知处理 |
| `internal/tool/mcp/tool.go` | `MCPTool` 实现 `tool.Tool`，转发 Execute 到 MCP Client |
| `internal/tool/mcp/loader.go` | 根据 Config 启动多个 MCP Server，把代理工具注册到 `tool.Registry`，返回 shutdown 函数 |
| `internal/tool/mcp/client_test.go` | MCP Client 单元测试（stdio pipe 模拟） |
| `cmd/mcp-server-time/main.go` | 内置示例：时间 MCP Server |
| `cmd/mcp-server-calc/main.go` | 内置示例：计算器 MCP Server |
| `cmd/mcp-server-project/main.go` | 内置示例：项目 README 摘要 MCP Server |
| `internal/config/config.go` | 新增 `MCPServers` 字段与 `MCP_SERVERS` 解析 |
| `cmd/server/main.go` | 在 `RegisterBuiltins` 之后调用 `mcp.LoadAndRegister` |
| `pkg/db/migrate.go` | 新增 v18 迁移：创建 `mcp_servers` 表（可选持久化配置） |
| `pkg/db/mcp_persistence.go` | MCP 配置 CRUD，用于未来 UI 启用/禁用（核心路径先以 `.env` 为准） |
| `docs/mcp-examples.md` | 使用说明与示例配置 |

---

## Task 1: 定义 MCP 协议类型与 Server 配置

**Files:**
- Create: `internal/tool/mcp/server.go`
- Test: `internal/tool/mcp/server_test.go`

- [ ] **Step 1: Write the failing test**

在 `internal/tool/mcp/server_test.go` 中：

```go
package mcp

import "testing"

func TestServerConfigNamespace(t *testing.T) {
    cfg := ServerConfig{Name: "fs", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "."}}
    got := cfg.Namespace()
    if got != "fs" {
        t.Fatalf("expected namespace fs, got %s", got)
    }
}

func TestToolNamespaceName(t *testing.T) {
    s := ServerConfig{Name: "fs"}
    got := s.ToolName("read_file")
    want := "mcp__fs__read_file"
    if got != want {
        t.Fatalf("expected %s, got %s", want, got)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tool/mcp/... -run TestServerConfigNamespace -v`
Expected: FAIL with "package mcp: no Go files" or undefined `ServerConfig`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/tool/mcp/server.go`：

```go
// Package mcp implements the Model Context Protocol client for the tool system.
//
// It connects to external MCP Servers (stdio, HTTP+SSE in the future), negotiates
// capabilities, lists tools, and registers each tool as a proxy into the shared
// tool.Registry. From the runtime's perspective, a proxy tool is identical to a
// built-in or dynamic tool.
package mcp

// ServerConfig describes how to start and connect to one MCP Server.
//
// Transport may be "stdio" (default) or "sse". For stdio, Command + Args start
// the child process; for sse, Endpoint is the HTTP URL.
type ServerConfig struct {
    Name        string            `json:"name"`
    Transport   string            `json:"transport"`
    Command     string            `json:"command,omitempty"`
    Args        []string          `json:"args,omitempty"`
    Endpoint    string            `json:"endpoint,omitempty"`
    Environment map[string]string `json:"environment,omitempty"`
    Enabled     bool              `json:"enabled"`
}

// Namespace returns the human-readable server namespace used to prefix tools.
func (c ServerConfig) Namespace() string {
    if c.Name == "" {
        return "default"
    }
    return c.Name
}

// ToolName produces the registry-safe name for a tool declared by this server.
func (c ServerConfig) ToolName(toolName string) string {
    return "mcp__" + c.Namespace() + "__" + toolName
}

// ServerCapability mirrors the initialize response from an MCP Server.
// Only fields that the client needs for routing are modeled here.
type ServerCapability struct {
    ProtocolVersion string    `json:"protocolVersion"`
    ServerInfo      ServerInfo `json:"serverInfo"`
}

// ServerInfo identifies the MCP Server.
type ServerInfo struct {
    Name    string `json:"name"`
    Version string `json:"version"`
}

// ToolDefinition represents one tool exposed by the MCP Server.
type ToolDefinition struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    InputSchema map[string]any `json:"inputSchema"`
}

// ResourceDefinition represents one resource exposed by the MCP Server.
type ResourceDefinition struct {
    URI         string `json:"uri"`
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    MIMEType    string `json:"mimeType,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tool/mcp/... -run 'TestServerConfigNamespace|TestToolNamespaceName' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tool/mcp/server.go internal/tool/mcp/server_test.go
git commit -m "feat(mcp): define ServerConfig and namespace helpers"
```

---

## Task 2: 实现 stdio 传输层

**Files:**
- Create: `internal/tool/mcp/transport.go`
- Test: `internal/tool/mcp/transport_test.go`

- [ ] **Step 1: Write the failing test**

在 `internal/tool/mcp/transport_test.go` 中：

```go
package mcp

import (
    "bytes"
    "io"
    "testing"
    "time"
)

func TestStdioTransportRoundTrip(t *testing.T) {
    // Simulate a child process by piping stdin/stdout manually.
    inR, inW := io.Pipe()
    outR, outW := io.Pipe()

    tr := &stdioTransport{stdin: inW, stdout: outR, stderr: io.Discard}
    go func() {
        // Read request line, echo it back with newline framing.
        buf := make([]byte, 1024)
        n, _ := inR.Read(buf)
        outW.Write(buf[:n])
        outW.Close()
    }()

    sent := []byte(`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{}}`)
    if err := tr.Send(sent); err != nil {
        t.Fatalf("send: %v", err)
    }

    got, err := tr.Receive(time.Second)
    if err != nil {
        t.Fatalf("receive: %v", err)
    }
    if !bytes.Equal(bytes.TrimSpace(got), sent) {
        t.Fatalf("expected %s, got %s", sent, got)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tool/mcp/... -run TestStdioTransportRoundTrip -v`
Expected: FAIL with undefined `stdioTransport` or methods.

- [ ] **Step 3: Write minimal implementation**

创建 `internal/tool/mcp/transport.go`：

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tool/mcp/... -run TestStdioTransportRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tool/mcp/transport.go internal/tool/mcp/transport_test.go
git commit -m "feat(mcp): implement stdio transport for MCP child processes"
```

---

## Task 3: 实现 JSON-RPC MCP Client

**Files:**
- Create: `internal/tool/mcp/client.go`
- Test: `internal/tool/mcp/client_test.go`

- [ ] **Step 1: Write the failing test**

在 `internal/tool/mcp/client_test.go` 中：

```go
package mcp

import (
    "context"
    "io"
    "sync"
    "testing"
    "time"
)

// fakeTransport records sends and returns queued responses.
type fakeTransport struct {
    mu        sync.Mutex
    sent      [][]byte
    responses [][]byte
    closed    bool
}

func (f *fakeTransport) Start(ctx context.Context) error { return nil }
func (f *fakeTransport) Send(msg []byte) error {
    f.mu.Lock()
    defer f.mu.Unlock()
    f.sent = append(f.sent, msg)
    return nil
}
func (f *fakeTransport) Receive(timeout time.Duration) ([]byte, error) {
    f.mu.Lock()
    defer f.mu.Unlock()
    if len(f.responses) == 0 {
        return nil, io.EOF
    }
    r := f.responses[0]
    f.responses = f.responses[1:]
    return r, nil
}
func (f *fakeTransport) Close() error {
    f.closed = true
    return nil
}

func TestClientInitialize(t *testing.T) {
    ft := &fakeTransport{
        responses: [][]byte{
            []byte(`{"jsonrpc":"2.0","id":"1","result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"test","version":"1.0"}}}`),
            []byte(`{"jsonrpc":"2.0","id":"2","result":{"tools":[]}}`),
        },
    }
    c := NewClient(ft, "test-session")
    if err := c.Initialize(context.Background()); err != nil {
        t.Fatalf("initialize: %v", err)
    }
    if c.Capability().ProtocolVersion != "2024-11-05" {
        t.Fatalf("unexpected protocol version %s", c.Capability().ProtocolVersion)
    }
}

func TestClientListTools(t *testing.T) {
    ft := &fakeTransport{
        responses: [][]byte{
            []byte(`{"jsonrpc":"2.0","id":"1","result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"test","version":"1.0"}}}`),
            []byte(`{"jsonrpc":"2.0","id":"2","result":{"tools":[{"name":"echo","description":"echo","inputSchema":{"type":"object"}}]}}`),
        },
    }
    c := NewClient(ft, "test-session")
    if err := c.Initialize(context.Background()); err != nil {
        t.Fatalf("initialize: %v", err)
    }
    tools, err := c.ListTools(context.Background())
    if err != nil {
        t.Fatalf("list tools: %v", err)
    }
    if len(tools) != 1 || tools[0].Name != "echo" {
        t.Fatalf("unexpected tools: %+v", tools)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tool/mcp/... -run 'TestClientInitialize|TestClientListTools' -v`
Expected: FAIL with undefined `NewClient` / `Initialize` / `ListTools`.

- [ ] **Step 3: Write minimal implementation**

创建 `internal/tool/mcp/client.go`：

```go
package mcp

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"
    "sync/atomic"
    "time"
)

const (
    protocolVersion = "2024-11-05"
    requestTimeout  = 30 * time.Second
)

// Client is a JSON-RPC MCP client over any Transport.
type Client struct {
    transport   Transport
    initialized bool
    cap         ServerCapability
    mu          sync.RWMutex
    idCounter   uint64
    sessionID   string
}

// NewClient creates a client.
func NewClient(transport Transport, sessionID string) *Client {
    return &Client{
        transport: transport,
        sessionID: sessionID,
    }
}

// Capability returns the negotiated server capability. Call Initialize first.
func (c *Client) Capability() ServerCapability {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.cap
}

// Start begins the underlying transport.
func (c *Client) Start(ctx context.Context) error {
    return c.transport.Start(ctx)
}

// Close stops the transport.
func (c *Client) Close() error {
    return c.transport.Close()
}

// Initialize performs the MCP initialize handshake and sends initialized
// notification.
func (c *Client) Initialize(ctx context.Context) error {
    params := map[string]any{
        "protocolVersion": protocolVersion,
        "capabilities":    map[string]any{},
        "clientInfo": map[string]any{
            "name":    "multi-agent-platform",
            "version": "0.1.0",
        },
    }

    res, err := c.call(ctx, "initialize", params)
    if err != nil {
        return fmt.Errorf("initialize request: %w", err)
    }

    var cap ServerCapability
    raw, err := json.Marshal(res)
    if err != nil {
        return fmt.Errorf("marshal capability: %w", err)
    }
    if err := json.Unmarshal(raw, &cap); err != nil {
        return fmt.Errorf("parse capability: %w", err)
    }

    if cap.ProtocolVersion == "" {
        return fmt.Errorf("server did not return protocol version")
    }

    c.mu.Lock()
    c.cap = cap
    c.initialized = true
    c.mu.Unlock()

    // Notify server that initialization is complete.
    _ = c.notify(ctx, "notifications/initialized", nil)
    return nil
}

// ListTools fetches tools/call candidates from the Server.
func (c *Client) ListTools(ctx context.Context) ([]ToolDefinition, error) {
    res, err := c.call(ctx, "tools/list", map[string]any{})
    if err != nil {
        return nil, fmt.Errorf("tools/list: %w", err)
    }
    raw, err := json.Marshal(res)
    if err != nil {
        return nil, err
    }
    var payload struct {
        Tools []ToolDefinition `json:"tools"`
    }
    if err := json.Unmarshal(raw, &payload); err != nil {
        return nil, fmt.Errorf("parse tools: %w", err)
    }
    return payload.Tools, nil
}

// CallTool invokes tools/call with the provided arguments.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (any, error) {
    params := map[string]any{
        "name":      name,
        "arguments": arguments,
    }
    res, err := c.call(ctx, "tools/call", params)
    if err != nil {
        return nil, fmt.Errorf("tools/call %s: %w", name, err)
    }
    return res, nil
}

func (c *Client) nextID() string {
    n := atomic.AddUint64(&c.idCounter, 1)
    return fmt.Sprintf("%d", n)
}

func (c *Client) call(ctx context.Context, method string, params any) (any, error) {
    c.mu.RLock()
    if method != "initialize" && !c.initialized {
        c.mu.RUnlock()
        return nil, fmt.Errorf("client not initialized")
    }
    c.mu.RUnlock()

    id := c.nextID()
    req := map[string]any{
        "jsonrpc": "2.0",
        "id":      id,
        "method":  method,
        "params":  params,
    }
    data, err := json.Marshal(req)
    if err != nil {
        return nil, err
    }

    if err := c.transport.Send(data); err != nil {
        return nil, fmt.Errorf("send: %w", err)
    }

    var resp jsonRPCResponse
    for {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        default:
        }

        line, err := c.transport.Receive(requestTimeout)
        if err != nil {
            return nil, fmt.Errorf("receive: %w", err)
        }
        if err := json.Unmarshal(line, &resp); err != nil {
            return nil, fmt.Errorf("decode response: %w", err)
        }
        // Ignore notifications / mismatched IDs.
        if resp.ID == id {
            break
        }
    }
    if resp.Error != nil {
        return nil, fmt.Errorf("mcp error %d: %s", resp.Error.Code, resp.Error.Message)
    }
    return resp.Result, nil
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
    req := map[string]any{
        "jsonrpc": "2.0",
        "method":  method,
        "params":  params,
    }
    data, err := json.Marshal(req)
    if err != nil {
        return err
    }
    return c.transport.Send(data)
}

type jsonRPCResponse struct {
    ID     string          `json:"id"`
    Result json.RawMessage `json:"result"`
    Error  *jsonRPCError   `json:"error"`
}

type jsonRPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tool/mcp/... -run 'TestClientInitialize|TestClientListTools' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tool/mcp/client.go internal/tool/mcp/client_test.go
git commit -m "feat(mcp): implement JSON-RPC MCP client with initialize, list, call"
```

---

## Task 4: 实现 MCP Tool 代理

**Files:**
- Create: `internal/tool/mcp/tool.go`
- Test: `internal/tool/mcp/tool_test.go`

- [ ] **Step 1: Write the failing test**

在 `internal/tool/mcp/tool_test.go` 中：

```go
package mcp

import (
    "context"
    "testing"
)

type fakeClient struct {
    cap   ServerCapability
    tools []ToolDefinition
    calls map[string]map[string]any
}

func (f *fakeClient) Capability() ServerCapability { return f.cap }
func (f *fakeClient) Initialize(ctx context.Context) error { return nil }
func (f *fakeClient) ListTools(ctx context.Context) ([]ToolDefinition, error) { return f.tools, nil }
func (f *fakeClient) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
    f.calls[name] = args
    return map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}, nil
}

func TestMCPToolExecuteForwardsCall(t *testing.T) {
    fc := &fakeClient{tools: []ToolDefinition{{Name: "add", Description: "add", InputSchema: map[string]any{"type": "object"}}}, calls: map[string]map[string]any{}}
    cfg := ServerConfig{Name: "calc"}
    mt := NewMCPTool(cfg, fc, fc.tools[0])
    if mt.Name() != "mcp__calc__add" {
        t.Fatalf("unexpected name %s", mt.Name())
    }
    res, err := mt.Execute(map[string]any{"a": 1, "b": 2})
    if err != nil {
        t.Fatalf("execute: %v", err)
    }
    if fc.calls["add"]["a"] != 1 {
        t.Fatalf("arguments not forwarded: %+v", fc.calls)
    }
    if res == nil {
        t.Fatalf("expected result")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tool/mcp/... -run TestMCPToolExecuteForwardsCall -v`
Expected: FAIL with NewMCPTool / CallTool not defined.

- [ ] **Step 3: Write minimal implementation**

创建 `internal/tool/mcp/tool.go`：

```go
package mcp

import (
    "context"
    "fmt"
    "time"
)

// toolClient is the subset of Client used by MCPTool; supports mocking.
type toolClient interface {
    CallTool(ctx context.Context, name string, arguments map[string]any) (any, error)
}

// MCPTool wraps a ToolDefinition from an MCP Server into the platform's Tool
// interface. It uses the server namespace to produce a collision-free registry
// name; execution is forwarded to the underlying MCP client.
type MCPTool struct {
    server ServerConfig
    client toolClient
    def    ToolDefinition
    name   string
}

// NewMCPTool creates a proxy tool from a server capability entry.
func NewMCPTool(server ServerConfig, client toolClient, def ToolDefinition) *MCPTool {
    return &MCPTool{
        server: server,
        client: client,
        def:    def,
        name:   server.ToolName(def.Name),
    }
}

// Name returns the namespaced registry name, e.g. "mcp__calc__add".
func (t *MCPTool) Name() string { return t.name }

// Description returns the original MCP tool description with a server hint.
func (t *MCPTool) Description() string {
    return fmt.Sprintf("[MCP %s] %s", t.server.Namespace(), t.def.Description)
}

// Parameters returns the JSON Schema from the MCP Server.
func (t *MCPTool) Parameters() map[string]any {
    if t.def.InputSchema == nil {
        return map[string]any{"type": "object"}
    }
    return t.def.InputSchema
}

// Execute forwards the call to the MCP Server with a 30s timeout.
func (t *MCPTool) Execute(input map[string]any) (any, error) {
    ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
    defer cancel()
    return t.client.CallTool(ctx, t.def.Name, input)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tool/mcp/... -run TestMCPToolExecuteForwardsCall -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tool/mcp/tool.go internal/tool/mcp/tool_test.go
git commit -m "feat(mcp): add MCPTool proxy implementing tool.Tool"
```

---

## Task 5: 实现 Loader 与注册到 Registry

**Files:**
- Create: `internal/tool/mcp/loader.go`
- Modify: `internal/tool/registry.go:25-29`（如需要重名检测）
- Test: `internal/tool/mcp/loader_test.go`

- [ ] **Step 1: Write the failing test**

在 `internal/tool/mcp/loader_test.go` 中：

```go
package mcp

import (
    "testing"

    "github.com/anmingwei/multi-agent-platform/internal/tool"
)

type stubClient struct {
    initErr error
    tools   []ToolDefinition
}

func (s *stubClient) Start(ctx context.Context) error                { return nil }
func (s *stubClient) Close() error                                   { return nil }
func (s *stubClient) Initialize(ctx context.Context) error           { return s.initErr }
func (s *stubClient) Capability() ServerCapability                   { return ServerCapability{} }
func (s *stubClient) ListTools(ctx context.Context) ([]ToolDefinition, error) {
    return s.tools, nil
}
func (s *stubClient) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
    return map[string]any{"result": name}, nil
}

func TestRegisterMCPToolsSerializesPerServer(t *testing.T) {
    cfg := ServerConfig{Name: "demo", Enabled: true, Command: "cat"}
    sc := &stubClient{
        tools: []ToolDefinition{
            {Name: "hi", Description: "say hi", InputSchema: map[string]any{"type": "object"}},
        },
    }
    reg := tool.NewRegistry()
    l := &Loader{reg: reg, clientFactory: func(ServerConfig) client { return sc }}
    shutdown, err := l.LoadServer(cfg)
    if err != nil {
        t.Fatalf("load server: %v", err)
    }
    defer shutdown()

    tools := reg.List()
    if len(tools) != 1 || tools[0].Name() != "mcp__demo__hi" {
        t.Fatalf("unexpected tools: %+v", tools)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tool/mcp/... -run TestRegisterMCPToolsSerializesPerServer -v`
Expected: FAIL with undefined `Loader` / `LoadServer` / `client` interface.

- [ ] **Step 3: Write minimal implementation**

创建 `internal/tool/mcp/loader.go`：

```go
package mcp

import (
    "context"
    "fmt"
    "log"

    "github.com/anmingwei/multi-agent-platform/internal/tool"
)

// client is the internal client abstraction used by Loader so tests can inject a
// fake. It extends the public Client surface used by MCPTool.
type client interface {
    toolClient
    Start(ctx context.Context) error
    Close() error
    Initialize(ctx context.Context) error
    Capability() ServerCapability
    ListTools(ctx context.Context) ([]ToolDefinition, error)
}

// Loader connects MCP Servers and registers their tools into a tool.Registry.
type Loader struct {
    reg           *tool.Registry
    clientFactory func(cfg ServerConfig) client
    closers       []func()
}

// NewLoader builds a Loader bound to the given registry.
func NewLoader(reg *tool.Registry) *Loader {
    return &Loader{
        reg: reg,
        clientFactory: func(cfg ServerConfig) client {
            tr := newStdioTransport(cfg)
            return NewClient(tr, "")
        },
    }
}

// LoadServer initializes a single server and registers each of its tools.
// It returns a shutdown function that closes the underlying client. If the
// server is disabled or initialization fails, it returns an error and no tools
// are registered.
func (l *Loader) LoadServer(cfg ServerConfig) (func(), error) {
    if !cfg.Enabled {
        return nil, fmt.Errorf("server %s is disabled", cfg.Name)
    }
    if cfg.Transport == "" {
        cfg.Transport = "stdio"
    }
    if cfg.Transport != "stdio" {
        return nil, fmt.Errorf("unsupported transport %s", cfg.Transport)
    }

    ctx := context.Background()
    c := l.clientFactory(cfg)
    if err := c.Start(ctx); err != nil {
        return nil, fmt.Errorf("start server %s: %w", cfg.Name, err)
    }

    if err := c.Initialize(ctx); err != nil {
        _ = c.Close()
        return nil, fmt.Errorf("initialize server %s: %w", cfg.Name, err)
    }

    tools, err := c.ListTools(ctx)
    if err != nil {
        _ = c.Close()
        return nil, fmt.Errorf("list tools %s: %w", cfg.Name, err)
    }

    shutdown := func() {
        if err := c.Close(); err != nil {
            log.Printf("[mcp] close server %s: %v", cfg.Name, err)
        }
    }
    l.closers = append(l.closers, shutdown)

    for _, def := range tools {
        if l.reg.IsBuiltin(toolNameForCheck(cfg, def)) {
            log.Printf("[mcp] skipping built-in name collision: %s", def.Name)
            continue
        }
        l.reg.Register(NewMCPTool(cfg, c, def))
        log.Printf("[mcp] registered %s", cfg.ToolName(def.Name))
    }

    return shutdown, nil
}

// LoadAll initializes every enabled server in configs and registers all tools.
// It returns a combined shutdown function.
func (l *Loader) LoadAll(configs []ServerConfig) func() {
    var ok []func()
    for _, cfg := range configs {
        shutdown, err := l.LoadServer(cfg)
        if err != nil {
            log.Printf("[mcp] failed to load server %s: %v", cfg.Name, err)
            continue
        }
        ok = append(ok, shutdown)
    }
    return func() {
        for _, fn := range ok {
            fn()
        }
    }
}

// toolNameForCheck returns the namespaced name for built-in collision checks.
func toolNameForCheck(cfg ServerConfig, def ToolDefinition) string {
    return cfg.ToolName(def.Name)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tool/mcp/... -run TestRegisterMCPToolsSerializesPerServer -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tool/mcp/loader.go internal/tool/mcp/loader_test.go
git commit -m "feat(mcp): add Loader to initialize servers and register proxy tools"
```

---

## Task 6: 配置加载（.env / MCP_SERVERS）

**Files:**
- Modify: `internal/config/config.go:17-33` 添加字段
- Modify: `internal/config/config.go:85-92` 添加解析
- Test: `internal/config/config_test.go` 新增用例

- [ ] **Step 1: Write the failing test**

在 `internal/config/config_test.go` 末尾追加：

```go
func TestLoadMCPServers(t *testing.T) {
    t.Setenv("MCP_SERVERS", `[{"name":"calc","command":"go","args":["run","./cmd/mcp-server-calc"],"enabled":true}]`)
    cfg, err := Load()
    if err != nil {
        t.Fatalf("load: %v", err)
    }
    if len(cfg.MCPServers) != 1 {
        t.Fatalf("expected 1 mcp server, got %d", len(cfg.MCPServers))
    }
    if cfg.MCPServers[0].Name != "calc" {
        t.Fatalf("unexpected name %s", cfg.MCPServers[0].Name)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -run TestLoadMCPServers -v`
Expected: FAIL with `Config` no field `MCPServers`.

- [ ] **Step 3: Write minimal implementation**

修改 `internal/config/config.go`：

在 imports 中加入：

```go
"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
```

在 `Config` 结构体中新增：

```go
    // MCPServers holds external MCP Server configurations.
    MCPServers []mcp.ServerConfig
```

在 `Load()` 的环境变量覆盖段（`LLMMockEndpoints` 之后）新增：

```go
    if v := os.Getenv("MCP_SERVERS"); v != "" {
        var servers []mcp.ServerConfig
        if err := json.Unmarshal([]byte(v), &servers); err != nil {
            return nil, fmt.Errorf("parse MCP_SERVERS JSON: %w", err)
        }
        cfg.MCPServers = servers
    }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -run TestLoadMCPServers -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): load MCP server list from MCP_SERVERS environment variable"
```

---

## Task 7: 在 main.go 中接入 MCP Loader

**Files:**
- Modify: `cmd/server/main.go:367-400`（工具注册区）

- [ ] **Step 1: Write the failing test / verification**

运行构建确认 MCP Loader 能被调用：

```bash
go build ./cmd/server
```

Expected: 当前代码未引用 mcp，需要先集成才能报错，这里用“编译失败作为失败测试”来驱动改动。

- [ ] **Step 2: Make minimal change**

在 `cmd/server/main.go` 中：

1. imports 加入：

```go
"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
```

2. 在 `tool.RegisterBuiltins(toolRegistry)` 之后、`log.Printf("Registered %d built-in tools"...)` 之前插入：

```go
    // Load configured MCP servers and register proxy tools. Failures are logged
    // but do not prevent the server from starting, so a missing server does not
    // break the whole platform.
    mcpLoader := mcp.NewLoader(toolRegistry)
    mcpShutdown := mcpLoader.LoadAll(cfg.MCPServers)
    defer mcpShutdown()
```

（如代码中已有 `defer`，可放在一个独立的匿名函数中统一 defer，或按习惯合并。）

- [ ] **Step 3: Verify it compiles**

```bash
go build ./cmd/server
```
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat(server): wire MCP loader into startup sequence"
```

---

## Task 8: 内置 MCP Server — 时间

**Files:**
- Create: `cmd/mcp-server-time/main.go`
- Test: `cmd/mcp-server-time/main_test.go`

- [ ] **Step 1: Write the failing test**

创建 `cmd/mcp-server-time/main_test.go`：

```go
package main

import (
    "bytes"
    "encoding/json"
    "strings"
    "testing"
)

func TestTimeServerRespondsToInitialize(t *testing.T) {
    in := strings.NewReader(`{"jsonrpc":"2.0","id":"1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}` + "\n")
    var out bytes.Buffer
    if err := handleSession(in, &out); err != nil {
        t.Fatalf("handle session: %v", err)
    }
    line := strings.TrimSpace(out.String())
    var resp map[string]any
    if err := json.Unmarshal([]byte(line), &resp); err != nil {
        t.Fatalf("decode: %v", err)
    }
    if resp["id"] != "1" {
        t.Fatalf("unexpected id %v", resp["id"])
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./cmd/mcp-server-time/... -run TestTimeServerRespondsToInitialize -v
```
Expected: FAIL due to missing package / function.

- [ ] **Step 3: Write minimal implementation**

创建 `cmd/mcp-server-time/main.go`：

```go
// mcp-server-time is a minimal MCP Server that exposes a "current_time" tool.
// It demonstrates newline-delimited JSON-RPC over stdio.
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "time"
)

type request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      string          `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params"`
}

type response struct {
    JSONRPC string      `json:"jsonrpc"`
    ID      string      `json:"id"`
    Result  any         `json:"result,omitempty"`
    Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}

func main() {
    if err := handleSession(os.Stdin, os.Stdout); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

// handleSession reads one JSON-RPC line, handles initialize and tools/*, then exits.
func handleSession(in io.Reader, out io.Writer) error {
    scanner := bufio.NewScanner(in)
    for scanner.Scan() {
        var req request
        if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
            writeError(out, "", -32700, "parse error")
            continue
        }
        var resp response
        resp.JSONRPC = "2.0"
        resp.ID = req.ID

        switch req.Method {
        case "initialize":
            resp.Result = map[string]any{
                "protocolVersion": "2024-11-05",
                "serverInfo": map[string]any{
                    "name":    "time",
                    "version": "0.1.0",
                },
                "capabilities": map[string]any{},
            }
        case "notifications/initialized":
            continue
        case "tools/list":
            resp.Result = map[string]any{
                "tools": []map[string]any{
                    {
                        "name":        "current_time",
                        "description": "Return the current local time as an RFC3339 string.",
                        "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
                    },
                },
            }
        case "tools/call":
            var p struct {
                Name      string         `json:"name"`
                Arguments map[string]any `json:"arguments"`
            }
            _ = json.Unmarshal(req.Params, &p)
            if p.Name != "current_time" {
                resp.Error = &rpcError{Code: -32601, Message: "unknown tool"}
            } else {
                resp.Result = map[string]any{
                    "content": []map[string]any{
                        {"type": "text", "text": time.Now().Format(time.RFC3339)},
                    },
                }
            }
        default:
            resp.Error = &rpcError{Code: -32601, Message: "method not found"}
        }

        data, err := json.Marshal(resp)
        if err != nil {
            return err
        }
        fmt.Fprintln(out, string(data))
    }
    return scanner.Err()
}

func writeError(out io.Writer, id string, code int, msg string) {
    r := response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
    data, _ := json.Marshal(r)
    fmt.Fprintln(out, string(data))
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./cmd/mcp-server-time/... -run TestTimeServerRespondsToInitialize -v
```
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add cmd/mcp-server-time/main.go cmd/mcp-server-time/main_test.go
git commit -m "feat(mcp): add built-in time MCP server example"
```

---

## Task 9: 内置 MCP Server — 计算器

**Files:**
- Create: `cmd/mcp-server-calc/main.go`
- Test: `cmd/mcp-server-calc/main_test.go`

- [ ] **Step 1: Write the failing test**

创建 `cmd/mcp-server-calc/main_test.go`：

```go
package main

import (
    "bytes"
    "encoding/json"
    "strings"
    "testing"
)

func TestCalcAdd(t *testing.T) {
    req := `{"jsonrpc":"2.0","id":"1","method":"tools/call","params":{"name":"add","arguments":{"a":2,"b":3}}}` + "\n"
    in := strings.NewReader(req)
    var out bytes.Buffer
    handleSession(in, &out)
    line := strings.TrimSpace(out.String())
    var resp map[string]any
    if err := json.Unmarshal([]byte(line), &resp); err != nil {
        t.Fatalf("decode: %v", err)
    }
    if resp["error"] != nil {
        t.Fatalf("got error: %v", resp["error"])
    }
}
```

- [ ] **Step 2: Write minimal implementation**

创建 `cmd/mcp-server-calc/main.go`：

```go
// mcp-server-calc is a minimal MCP Server exposing add/sub/mul/div tools.
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "io"
    "os"
)

// request / response / rpcError are identical to time server.
type request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      string          `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params"`
}

type response struct {
    JSONRPC string    `json:"jsonrpc"`
    ID      string    `json:"id"`
    Result  any       `json:"result,omitempty"`
    Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}

func main() {
    if err := handleSession(os.Stdin, os.Stdout); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func handleSession(in io.Reader, out io.Writer) error {
    scanner := bufio.NewScanner(in)
    for scanner.Scan() {
        var req request
        if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
            writeError(out, "", -32700, "parse error")
            continue
        }
        resp := response{JSONRPC: "2.0", ID: req.ID}
        switch req.Method {
        case "initialize":
            resp.Result = map[string]any{
                "protocolVersion": "2024-11-05",
                "serverInfo":      map[string]any{"name": "calc", "version": "0.1.0"},
                "capabilities":    map[string]any{},
            }
        case "notifications/initialized":
            continue
        case "tools/list":
            resp.Result = map[string]any{"tools": []map[string]any{
                {"name": "add", "description": "Add two numbers.", "inputSchema": numberSchema("a", "b")},
                {"name": "subtract", "description": "Subtract b from a.", "inputSchema": numberSchema("a", "b")},
                {"name": "multiply", "description": "Multiply two numbers.", "inputSchema": numberSchema("a", "b")},
                {"name": "divide", "description": "Divide a by b.", "inputSchema": numberSchema("a", "b")},
            }}
        case "tools/call":
            var p struct {
                Name      string         `json:"name"`
                Arguments map[string]any `json:"arguments"`
            }
            _ = json.Unmarshal(req.Params, &p)
            res, err := calculate(p.Name, p.Arguments)
            if err != nil {
                resp.Error = &rpcError{Code: -32000, Message: err.Error()}
            } else {
                resp.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("%v", res)}}}
            }
        default:
            resp.Error = &rpcError{Code: -32601, Message: "method not found"}
        }
        data, _ := json.Marshal(resp)
        fmt.Fprintln(out, string(data))
    }
    return scanner.Err()
}

func numberSchema(required ...string) map[string]any {
    props := map[string]any{}
    for _, r := range required {
        props[r] = map[string]any{"type": "number"}
    }
    return map[string]any{"type": "object", "properties": props, "required": required}
}

func calculate(op string, args map[string]any) (float64, error) {
    a, okA := toFloat(args["a"])
    b, okB := toFloat(args["b"])
    if !okA || !okB {
        return 0, fmt.Errorf("arguments a and b must be numbers")
    }
    switch op {
    case "add":
        return a + b, nil
    case "subtract":
        return a - b, nil
    case "multiply":
        return a * b, nil
    case "divide":
        if b == 0 {
            return 0, fmt.Errorf("division by zero")
        }
        return a / b, nil
    default:
        return 0, fmt.Errorf("unknown operation %s", op)
    }
}

func toFloat(v any) (float64, bool) {
    switch n := v.(type) {
    case float64:
        return n, true
    case int:
        return float64(n), true
    case json.Number:
        f, err := n.Float64()
        return f, err == nil
    default:
        return 0, false
    }
}

func writeError(out io.Writer, id string, code int, msg string) {
    r := response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
    data, _ := json.Marshal(r)
    fmt.Fprintln(out, string(data))
}
```

- [ ] **Step 3: Run test to verify it passes**

```bash
go test ./cmd/mcp-server-calc/... -run TestCalcAdd -v
```
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add cmd/mcp-server-calc/main.go cmd/mcp-server-calc/main_test.go
git commit -m "feat(mcp): add built-in calculator MCP server example"
```

---

## Task 10: 内置 MCP Server — 项目自述摘要

**Files:**
- Create: `cmd/mcp-server-project/main.go`
- Test: `cmd/mcp-server-project/main_test.go`

- [ ] **Step 1: Write the failing test**

创建 `cmd/mcp-server-project/main_test.go`：

```go
package main

import (
    "bytes"
    "strings"
    "testing"
)

func TestProjectReadMeToolExists(t *testing.T) {
    req := `{"jsonrpc":"2.0","id":"1","method":"tools/list","params":{}}` + "\n"
    in := strings.NewReader(req)
    var out bytes.Buffer
    handleSession(in, &out)
    if !strings.Contains(out.String(), "read_project_summary") {
        t.Fatalf("expected read_project_summary tool, got %s", out.String())
    }
}
```

- [ ] **Step 2: Write minimal implementation**

创建 `cmd/mcp-server-project/main.go`：

```go
// mcp-server-project is a minimal MCP Server that reads the repository README
// and returns a one-line summary. It demonstrates Resource-like read access.
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
)

type request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      string          `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params"`
}

type response struct {
    JSONRPC string    `json:"jsonrpc"`
    ID      string    `json:"id"`
    Result  any       `json:"result,omitempty"`
    Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}

func main() {
    if err := handleSession(os.Stdin, os.Stdout); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func handleSession(in io.Reader, out io.Writer) error {
    scanner := bufio.NewScanner(in)
    for scanner.Scan() {
        var req request
        if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
            writeError(out, "", -32700, "parse error")
            continue
        }
        resp := response{JSONRPC: "2.0", ID: req.ID}
        switch req.Method {
        case "initialize":
            resp.Result = map[string]any{
                "protocolVersion": "2024-11-05",
                "serverInfo":      map[string]any{"name": "project", "version": "0.1.0"},
                "capabilities":    map[string]any{},
            }
        case "notifications/initialized":
            continue
        case "tools/list":
            resp.Result = map[string]any{"tools": []map[string]any{
                {
                    "name":        "read_project_summary",
                    "description": "Read the first non-empty line of README.md in the current directory.",
                    "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
                },
            }}
        case "tools/call":
            var p struct {
                Name      string         `json:"name"`
                Arguments map[string]any `json:"arguments"`
            }
            _ = json.Unmarshal(req.Params, &p)
            if p.Name != "read_project_summary" {
                resp.Error = &rpcError{Code: -32601, Message: "unknown tool"}
            } else {
                text, err := firstReadMeLine()
                if err != nil {
                    resp.Error = &rpcError{Code: -32000, Message: err.Error()}
                } else {
                    resp.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
                }
            }
        default:
            resp.Error = &rpcError{Code: -32601, Message: "method not found"}
        }
        data, _ := json.Marshal(resp)
        fmt.Fprintln(out, string(data))
    }
    return scanner.Err()
}

func firstReadMeLine() (string, error) {
    path := filepath.Join(".", "README.md")
    data, err := os.ReadFile(path)
    if err != nil {
        return "", fmt.Errorf("read README.md: %w", err)
    }
    lines := strings.Split(string(data), "\n")
    for _, l := range lines {
        s := strings.TrimSpace(l)
        if s != "" {
            return s, nil
        }
    }
    return "", fmt.Errorf("README.md is empty")
}

func writeError(out io.Writer, id string, code int, msg string) {
    r := response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
    data, _ := json.Marshal(r)
    fmt.Fprintln(out, string(data))
}
```

- [ ] **Step 3: Run test to verify it passes**

```bash
go test ./cmd/mcp-server-project/... -run TestProjectReadMeToolExists -v
```
Expected: PASS（前提是仓库根目录存在 README.md，若不存在则在该目录放置最小 README）。

- [ ] **Step 4: Commit**

```bash
git add cmd/mcp-server-project/main.go cmd/mcp-server-project/main_test.go
git commit -m "feat(mcp): add built-in project README summary MCP server example"
```

---

## Task 11: 端到端集成测试

**Files:**
- Create: `internal/tool/mcp/integration_test.go`

- [ ] **Step 1: Write the failing test**

创建 `internal/tool/mcp/integration_test.go`：

```go
//go:build integration
// +build integration

package mcp

import (
    "context"
    "testing"
    "time"

    "github.com/anmingwei/multi-agent-platform/internal/tool"
)

func TestIntegrationWithCalcServer(t *testing.T) {
    cfg := ServerConfig{
        Name:    "calc",
        Command: "go",
        Args:    []string{"run", "../../../cmd/mcp-server-calc"},
        Enabled: true,
    }
    reg := tool.NewRegistry()
    loader := NewLoader(reg)
    shutdown, err := loader.LoadServer(cfg)
    if err != nil {
        t.Fatalf("load server: %v", err)
    }
    defer shutdown()

    tools := reg.List()
    if len(tools) == 0 {
        t.Fatalf("expected at least one tool")
    }

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    for _, tt := range tools {
        if tt.Name() == "mcp__calc__add" {
            res, err := tt.Execute(map[string]any{"a": 10, "b": 32})
            if err != nil {
                t.Fatalf("execute: %v", err)
            }
            if res == nil {
                t.Fatalf("expected result")
            }
            return
        }
    }
    t.Fatalf("mcp__calc__add not found in %v", tools)
}
```

- [ ] **Step 2: Verify it fails without flag**

```bash
go test ./internal/tool/mcp/... -run TestIntegrationWithCalcServer -v
```
Expected: skipped / not found（因为 integration build tag）。

- [ ] **Step 3: Run with integration tag**

```bash
go test -tags=integration ./internal/tool/mcp/... -run TestIntegrationWithCalcServer -v
```
Expected: PASS（需要 go run 能找到 calc server 的 main 包）。

- [ ] **Step 4: Commit**

```bash
git add internal/tool/mcp/integration_test.go
git commit -m "test(mcp): add integration test against built-in calc server"
```

---

## Task 12: 文档与配置示例

**Files:**
- Create: `docs/mcp-examples.md`
- Modify: `.env.example` or `.env`（如仓库根目录存在）

- [ ] **Step 1: Create documentation**

创建 `docs/mcp-examples.md`：

```markdown
# MCP 使用说明

## 1. 配置外部 MCP Server

编辑 `.env` 或在环境中设置：

```env
MCP_SERVERS=[
  {"name":"time","command":"go","args":["run","./cmd/mcp-server-time"],"enabled":true},
  {"name":"calc","command":"go","args":["run","./cmd/mcp-server-calc"],"enabled":true},
  {"name":"project","command":"go","args":["run","./cmd/mcp-server-project"],"enabled":true}
]
```

## 2. 启动平台

```bash
go run ./cmd/server
```

启动日志会输出每个成功注册的 MCP 工具，例如 `mcp__calc__add`。

## 3. 在对话中使用

Agent 可用工具列表会自动包含以 `mcp__<server>__<tool>` 命名的工具。

## 4. 信任与安全

- MCP Server 作为子进程运行时继承当前用户权限。
- 生产环境建议对不可信 Server 使用容器/沙箱或远程 SSE 模式。
- 平台不会把 Server 的认证信息传入 Agent 上下文。
```

- [ ] **Step 2: Update .env**

如果仓库根目录存在 `.env` 或 `.env.example`，追加（以注释形式）：

```text
# MCP Servers (JSON array). Each item needs name, command, args, enabled.
# MCP_SERVERS=[{"name":"time","command":"go","args":["run","./cmd/mcp-server-time"],"enabled":true}]
```

若不存在 `.env.example`，仅创建 `docs/mcp-examples.md` 即可。

- [ ] **Step 3: Commit**

```bash
git add docs/mcp-examples.md .env
git commit -m "docs(mcp): add usage guide and example configuration"
```

---

## Task 13: 数据库持久化（可选但推荐）

**Files:**
- Create: `pkg/db/mcp_persistence.go`
- Modify: `pkg/db/migrate.go:288` 追加 v18

- [ ] **Step 1: Add migration**

在 `pkg/db/migrate.go` 的 `migrations` 切片末尾追加：

```go
    // v18: Create mcp_servers table for persisted MCP server configurations.
    {
        Version:     18,
        Description: "Create mcp_servers table for persisted MCP configurations",
        SQL: `CREATE TABLE IF NOT EXISTS mcp_servers (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL UNIQUE,
            transport TEXT DEFAULT 'stdio',
            command TEXT,
            args_json TEXT DEFAULT '[]',
            endpoint TEXT,
            environment_json TEXT DEFAULT '{}',
            enabled BOOLEAN DEFAULT 1,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
        );
        CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled ON mcp_servers(enabled);`,
    },
```

- [ ] **Step 2: Add persistence helpers**

创建 `pkg/db/mcp_persistence.go`：

```go
package db

import (
    "database/sql"
    "encoding/json"
    "fmt"

    "github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
)

// InsertMCPServer persists a single MCP server configuration.
func InsertMCPServer(s mcp.ServerConfig) error {
    args, _ := json.Marshal(s.Args)
    env, _ := json.Marshal(s.Environment)
    _, err := DB.Exec(`
        INSERT INTO mcp_servers (id, name, transport, command, args_json, endpoint, environment_json, enabled)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(name) DO UPDATE SET
            transport=excluded.transport,
            command=excluded.command,
            args_json=excluded.args_json,
            endpoint=excluded.endpoint,
            environment_json=excluded.environment_json,
            enabled=excluded.enabled,
            updated_at=CURRENT_TIMESTAMP`,
        s.Name, s.Name, s.Transport, s.Command, string(args), s.Endpoint, string(env), s.Enabled,
    )
    return err
}

// QueryEnabledMCPServers returns all enabled MCP server configurations.
func QueryEnabledMCPServers() ([]mcp.ServerConfig, error) {
    rows, err := DB.Query(`
        SELECT name, transport, command, args_json, endpoint, environment_json, enabled
        FROM mcp_servers WHERE enabled = 1`)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var result []mcp.ServerConfig
    for rows.Next() {
        var s mcp.ServerConfig
        var argsJSON, envJSON string
        if err := rows.Scan(&s.Name, &s.Transport, &s.Command, &argsJSON, &s.Endpoint, &envJSON, &s.Enabled); err != nil {
            return nil, err
        }
        _ = json.Unmarshal([]byte(argsJSON), &s.Args)
        _ = json.Unmarshal([]byte(envJSON), &s.Environment)
        result = append(result, s)
    }
    return result, rows.Err()
}

// DeleteMCPServer removes an MCP server configuration.
func DeleteMCPServer(name string) error {
    _, err := DB.Exec(`DELETE FROM mcp_servers WHERE name = ?`, name)
    return err
}
```

- [ ] **Step 3: Add test**

创建 `pkg/db/mcp_persistence_test.go`：

```go
package db

import (
    "testing"

    "github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
)

func TestMCPServerPersistence(t *testing.T) {
    Init(":memory:")
    defer Close()

    s := mcp.ServerConfig{Name: "calc", Command: "go", Args: []string{"run", "./calc"}, Enabled: true}
    if err := InsertMCPServer(s); err != nil {
        t.Fatalf("insert: %v", err)
    }
    servers, err := QueryEnabledMCPServers()
    if err != nil {
        t.Fatalf("query: %v", err)
    }
    if len(servers) != 1 || servers[0].Name != "calc" {
        t.Fatalf("unexpected servers: %+v", servers)
    }
}
```

- [ ] **Step 4: Run test**

```bash
go test ./pkg/db/... -run TestMCPServerPersistence -v
```
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/db/mcp_persistence.go pkg/db/mcp_persistence_test.go pkg/db/migrate.go
git commit -m "feat(db): persist MCP server configurations in mcp_servers table"
```

---

## Task 14: 最终验证与提交

- [ ] **Step 1: Run full test suite**

```bash
go test ./...
```
Expected: 无新增失败；可能需要用 `-short` 跳过 integration test。

- [ ] **Step 2: Build server and example servers**

```bash
go build ./cmd/server
go build ./cmd/mcp-server-time
go build ./cmd/mcp-server-calc
go build ./cmd/mcp-server-project
```
Expected: 全部成功。

- [ ] **Step 3: Lint / vet**

```bash
go vet ./...
```
Expected: 无新增问题。

- [ ] **Step 4: Commit any final fixes**

```bash
git add -A
git commit -m "chore(mcp): final verification and cleanup"
```

---

## Self-Review

### Spec coverage

| OpenCode 文档要求 | 对应 Task |
|---|---|
| Agent → 工具注册表 → MCP 适配层 → Server | Task 5 (Loader 注册到 Registry) |
| MCP Server 生命周期（启动、初始化、关闭） | Task 2/3/5 |
| Tool 代理注册与命名空间 | Task 1/4/5 |
| 跨进程协议（JSON-RPC / stdio） | Task 2/3 |
| 能力声明与契约（initialize） | Task 3 |
| 错误分类与模型可见错误 | Task 3 (`jsonRPCError`) |
| 配置加载（静态配置） | Task 6 |
| 内部示例 Server | Task 8/9/10 |
| 持久化 | Task 13 |

### Placeholder scan

- 没有 "TBD"、"TODO"、"实现 later"。
- 所有步骤包含可复制的代码、命令、期望输出。
- 没有步骤引用未定义的类型/函数。

### Type consistency

- `ServerConfig` 字段在 `config.go`、`mcp/server.go`、`mcp_persistence.go` 中一致。
- `ToolDefinition.InputSchema` 类型始终为 `map[string]any`。
- `MCPTool.Name()` 始终返回 `server.ToolName(def.Name)`。

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-17-mcp-support.md`.**

**Two execution options:**

1. **Subagent-Driven (recommended)** — 每个 Task 由一个独立 subagent 执行，主会话在关键节点审查，逐步推进。
2. **Inline Execution** — 在当前会话中按 Task 顺序直接执行，使用 `superpowers:executing-plans` skill。

**Which approach?**
