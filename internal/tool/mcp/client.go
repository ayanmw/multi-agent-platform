package mcp

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"
    "time"
)

// Client 实现了一个基于可插拔 Transport 的 JSON-RPC MCP 客户端。
//
// 它负责 initialize 握手、递增的请求 ID、响应分发（忽略 notification），
// 以及列出和调用远端 tool 的高层辅助方法。
type Client struct {
    transport Transport

    nextID int64
    mu     sync.Mutex

    // capabilities 保存 initialize 返回的 server capabilities。
    capabilities map[string]any
    // serverInfo 标识远端 MCP server。
    serverInfo ServerInfo
    // protocolVersion 是协商出的 MCP 协议版本。
    protocolVersion string
}

// NewClient 创建一个尚未初始化的 MCP 客户端。
func NewClient(transport Transport) *Client {
    return &Client{
        transport: transport,
        nextID:    1,
    }
}

// jsonRPCRequest 是传出消息的传输层格式。
type jsonRPCRequest struct {
    JSONRPC string `json:"jsonrpc"`
    ID      int64  `json:"id"`
    Method  string `json:"method"`
    Params  any    `json:"params,omitempty"`
}

// jsonRPCResponse 是传入消息的传输层格式。
// ID 保留原始 JSON，因为有些 server 会把 ID 以字符串形式回传，而我们用的是整数。
type jsonRPCResponse struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id"`
    Result  json.RawMessage `json:"result"`
    Error   *jsonRPCError   `json:"error"`
}

// jsonRPCError 表示 JSON-RPC 2.0 的 error 对象。
type jsonRPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}

func (e *jsonRPCError) Error() string {
    return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// Initialize 执行 MCP initialize 握手并返回协商出的协议版本。
//
// 握手同时会把 server capabilities 和 serverInfo 记录在 Client 上，
// 供后续路由决策使用（例如仅当 server 支持时才调用 tools/list）。
func (c *Client) Initialize(ctx context.Context) (string, error) {
    params := map[string]any{
        "protocolVersion": "2024-11-05",
        "capabilities":    map[string]any{},
        "clientInfo": map[string]any{
            "name":    "multi-agent-platform",
            "version": "0.1.0",
        },
    }

    resp, err := c.request(ctx, "initialize", params)
    if err != nil {
        return "", fmt.Errorf("initialize request: %w", err)
    }

    var initResult struct {
        ProtocolVersion string         `json:"protocolVersion"`
        ServerInfo      ServerInfo     `json:"serverInfo"`
        Capabilities    map[string]any `json:"capabilities"`
    }
    if err := json.Unmarshal(resp.Result, &initResult); err != nil {
        return "", fmt.Errorf("unmarshal initialize result: %w", err)
    }

    c.mu.Lock()
    c.protocolVersion = initResult.ProtocolVersion
    c.serverInfo = initResult.ServerInfo
    c.capabilities = initResult.Capabilities
    c.mu.Unlock()

    if err := c.sendNotification(ctx, "notifications/initialized", map[string]any{}); err != nil {
        return "", fmt.Errorf("send initialized notification: %w", err)
    }

    return initResult.ProtocolVersion, nil
}

// ServerInfo 返回初始化阶段收到的 server 信息。
func (c *Client) ServerInfo() ServerInfo {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.serverInfo
}

// Capabilities 返回初始化阶段收到的 server capabilities。
func (c *Client) Capabilities() map[string]any {
    c.mu.Lock()
    defer c.mu.Unlock()
    cp := make(map[string]any, len(c.capabilities))
    for k, v := range c.capabilities {
        cp[k] = v
    }
    return cp
}

// listToolsResult 对应 tools/list 响应的结构。
type listToolsResult struct {
    Tools []ToolDefinition `json:"tools"`
}

// ListTools 发送 tools/list 并返回 server 声明的 tool 列表。
func (c *Client) ListTools(ctx context.Context) ([]ToolDefinition, error) {
    resp, err := c.request(ctx, "tools/list", map[string]any{})
    if err != nil {
        return nil, fmt.Errorf("tools/list request: %w", err)
    }

    var result listToolsResult
    if err := json.Unmarshal(resp.Result, &result); err != nil {
        return nil, fmt.Errorf("unmarshal tools/list result: %w", err)
    }
    return result.Tools, nil
}

// ToolCallResult 包含一次 tools/call 调用的解析输出。
type ToolCallResult struct {
    Text    string
    Content []map[string]any
}

// CallTool 通过名称和 JSON 参数调用远端 tool。
//
// 它返回所有 content 项中拼接后的文本内容，同时返回原始 content slice，
// 供需要图片或内嵌资源等结构化数据的调用方使用。
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
    params := map[string]any{
        "name":      name,
        "arguments": args,
    }
    resp, err := c.request(ctx, "tools/call", params)
    if err != nil {
        return nil, fmt.Errorf("tools/call request: %w", err)
    }

    var callResult struct {
        Content []map[string]any `json:"content"`
    }
    if err := json.Unmarshal(resp.Result, &callResult); err != nil {
        return nil, fmt.Errorf("unmarshal tools/call result: %w", err)
    }

    var texts []string
    for _, item := range callResult.Content {
        if t, ok := item["type"].(string); ok && t == "text" {
            if text, ok := item["text"].(string); ok {
                texts = append(texts, text)
            }
        }
    }

    return &ToolCallResult{
        Text:    joinTexts(texts),
        Content: callResult.Content,
    }, nil
}

// request 发送一个 JSON-RPC 方法调用，并阻塞等待匹配 ID 的响应。
// notification（没有 id 字段的消息）会被忽略，因此 server 并发推送给
// client 的消息不会打断轮询。
func (c *Client) request(ctx context.Context, method string, params any) (*jsonRPCResponse, error) {
    c.mu.Lock()
    id := c.nextID
    c.nextID++
    c.mu.Unlock()

    req := jsonRPCRequest{
        JSONRPC: "2.0",
        ID:      id,
        Method:  method,
        Params:  params,
    }
    body, err := json.Marshal(req)
    if err != nil {
        return nil, fmt.Errorf("marshal request: %w", err)
    }

    if err := c.transport.Send(body); err != nil {
        return nil, fmt.Errorf("transport send: %w", err)
    }

    for {
        timeout, err := remainingTimeout(ctx)
        if err != nil {
            return nil, err
        }

        line, err := c.transport.Receive(timeout)
        if err != nil {
            select {
            case <-ctx.Done():
                return nil, ctx.Err()
            default:
                return nil, fmt.Errorf("transport receive: %w", err)
            }
        }

        var resp jsonRPCResponse
        if err := json.Unmarshal(line, &resp); err != nil {
            // 如果 server 输出了非 JSON 噪声，继续读取直到找到发给我们的合法消息。
            continue
        }

        // notification 没有 id 字段；忽略并继续轮询。
        if len(resp.ID) == 0 {
            continue
        }

        respID, err := parseJSONRPCID(resp.ID)
        if err != nil {
            return nil, fmt.Errorf("parse response id: %w", err)
        }
        if respID != id {
            // 来自其它请求的乱序响应；继续读取。
            continue
        }

        if resp.Error != nil {
            return nil, resp.Error
        }

        return &resp, nil
    }
}

// sendNotification 写入一条单向 JSON-RPC notification（无 id）。
func (c *Client) sendNotification(ctx context.Context, method string, params any) error {
    req := map[string]any{
        "jsonrpc": "2.0",
        "method":  method,
        "params":  params,
    }
    body, err := json.Marshal(req)
    if err != nil {
        return fmt.Errorf("marshal notification: %w", err)
    }
    return c.transport.Send(body)
}

// parseJSONRPCID 处理可能是数字或字符串的 JSON-RPC id。
func parseJSONRPCID(raw json.RawMessage) (int64, error) {
    if len(raw) == 0 {
        return 0, fmt.Errorf("empty id")
    }
    // JSON 数字。
    var num int64
    if err := json.Unmarshal(raw, &num); err == nil {
        return num, nil
    }
    // JSON 字符串：我们生成的是数字 id，因此字符串 id 属于异常情况。
    var str string
    if err := json.Unmarshal(raw, &str); err == nil {
        return 0, fmt.Errorf("unexpected string id %q", str)
    }
    return 0, fmt.Errorf("unparseable id %s", raw)
}

// remainingTimeout 将 context 的 deadline 转换为适合 transport.Receive 使用的
// duration。如果没有设置 deadline，则使用一个保守的较长超时。
func remainingTimeout(ctx context.Context) (time.Duration, error) {
    deadline, ok := ctx.Deadline()
    if !ok {
        return 30 * time.Second, nil
    }
    d := time.Until(deadline)
    if d <= 0 {
        return 0, ctx.Err()
    }
    return d, nil
}

// joinTexts 用换行符分隔拼接文本内容项。
func joinTexts(parts []string) string {
    var out string
    for i, p := range parts {
        if i > 0 {
            out += "\n"
        }
        out += p
    }
    return out
}
