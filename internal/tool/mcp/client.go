package mcp

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"
    "time"
)

// Client implements a JSON-RPC MCP client on top of a pluggable Transport.
//
// It handles the initialize handshake, incremental request IDs, response
// demultiplexing (ignoring notifications), and high-level helpers for
// listing and calling remote tools.
type Client struct {
    transport Transport

    nextID int64
    mu     sync.Mutex

    // capabilities holds the server capabilities returned by initialize.
    capabilities map[string]any
    // serverInfo identifies the remote MCP server.
    serverInfo ServerInfo
    // protocolVersion is the negotiated MCP protocol version.
    protocolVersion string
}

// NewClient creates an uninitialized MCP client.
func NewClient(transport Transport) *Client {
    return &Client{
        transport: transport,
        nextID:    1,
    }
}

// jsonRPCRequest is the wire format for outgoing messages.
type jsonRPCRequest struct {
    JSONRPC string `json:"jsonrpc"`
    ID      int64  `json:"id"`
    Method  string `json:"method"`
    Params  any    `json:"params,omitempty"`
}

// jsonRPCResponse is the wire format for incoming messages.
// ID is kept raw because some servers echo ID as a string while we use ints.
type jsonRPCResponse struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id"`
    Result  json.RawMessage `json:"result"`
    Error   *jsonRPCError   `json:"error"`
}

// jsonRPCError represents a JSON-RPC 2.0 error object.
type jsonRPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}

func (e *jsonRPCError) Error() string {
    return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// Initialize performs the MCP initialize handshake and returns the negotiated
// protocol version.
//
// The handshake also records server capabilities and serverInfo on the Client
// for later routing decisions (e.g. only calling tools/list if supported).
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

// ServerInfo returns the server information received during initialization.
func (c *Client) ServerInfo() ServerInfo {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.serverInfo
}

// Capabilities returns the server capabilities received during initialization.
func (c *Client) Capabilities() map[string]any {
    c.mu.Lock()
    defer c.mu.Unlock()
    cp := make(map[string]any, len(c.capabilities))
    for k, v := range c.capabilities {
        cp[k] = v
    }
    return cp
}

// listToolsResult matches the tools/list response shape.
type listToolsResult struct {
    Tools []ToolDefinition `json:"tools"`
}

// ListTools sends tools/list and returns the tools declared by the server.
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

// ToolCallResult contains the parsed output of a tools/call invocation.
type ToolCallResult struct {
    Text    string
    Content []map[string]any
}

// CallTool invokes a remote tool by name with JSON arguments.
//
// It returns the concatenated text content of all returned content items,
// plus the raw content slice for callers that need structured data such as
// images or embedded resources.
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

// request sends a JSON-RPC method call and blocks until a response with the
// matching ID is received. Notifications (messages without an id field) are
// ignored so that concurrent server-to-client messages do not break polling.
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
            // If the server emitted non-JSON noise, keep reading until we find
            // a valid message addressed to us.
            continue
        }

        // Notifications have no id field; ignore them and keep polling.
        if len(resp.ID) == 0 {
            continue
        }

        respID, err := parseJSONRPCID(resp.ID)
        if err != nil {
            return nil, fmt.Errorf("parse response id: %w", err)
        }
        if respID != id {
            // Out-of-order response for a different request; keep reading.
            continue
        }

        if resp.Error != nil {
            return nil, resp.Error
        }

        return &resp, nil
    }
}

// sendNotification writes a one-way JSON-RPC notification (no id).
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

// parseJSONRPCID handles JSON-RPC ids that may be numbers or strings.
func parseJSONRPCID(raw json.RawMessage) (int64, error) {
    if len(raw) == 0 {
        return 0, fmt.Errorf("empty id")
    }
    // JSON number.
    var num int64
    if err := json.Unmarshal(raw, &num); err == nil {
        return num, nil
    }
    // JSON string: we generated numerically, so any string id is unexpected.
    var str string
    if err := json.Unmarshal(raw, &str); err == nil {
        return 0, fmt.Errorf("unexpected string id %q", str)
    }
    return 0, fmt.Errorf("unparseable id %s", raw)
}

// remainingTimeout converts the context deadline into a duration suitable for
// transport.Receive. If no deadline is set, a conservative long timeout is used.
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

// joinTexts concatenates text content items with newline separators.
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
