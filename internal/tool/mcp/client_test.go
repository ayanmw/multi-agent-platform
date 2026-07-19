package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// mockTransport 基于一对 io.Pipe 实现 Transport 接口。
// server 端从一个 pipe 读请求，再向另一个 pipe 写响应。
type mockTransport struct {
	inR  *io.PipeReader
	inW  *io.PipeWriter
	outR *io.PipeReader
	outW *io.PipeWriter

	closed bool
	mu     sync.Mutex
}

func newMockTransport() *mockTransport {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	return &mockTransport{
		inR:  inR,
		inW:  inW,
		outR: outR,
		outW: outW,
	}
}

func (m *mockTransport) Start(ctx context.Context) error { return nil }

func (m *mockTransport) Send(message []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("transport closed")
	}
	_, err := m.inW.Write(append(message, '\n'))
	return err
}

func (m *mockTransport) Receive(timeout time.Duration) ([]byte, error) {
	m.mu.Lock()
	r := m.outR
	m.mu.Unlock()
	if r == nil {
		return nil, errors.New("transport not started")
	}

	done := make(chan struct{})
	var line []byte
	var err error
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		if scanner.Scan() {
			line = scanner.Bytes()
		} else {
			if sErr := scanner.Err(); sErr != nil {
				err = sErr
			} else {
				err = io.EOF
			}
		}
	}()

	select {
	case <-done:
		return line, err
	case <-time.After(timeout):
		return nil, errors.New("receive timeout")
	}
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	_ = m.inW.Close()
	_ = m.outW.Close()
	return nil
}

// serverWriter 让测试直接访问响应 pipe。
func (m *mockTransport) serverWriter() io.Writer { return m.outW }

// requestReader 让测试直接访问请求 pipe。
func (m *mockTransport) requestReader() io.Reader { return m.inR }

// readLine 从 server 端 reader 读取一条 JSON-RPC 请求。
func readLine(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			t.Fatalf("read line: %v", err)
		}
		t.Fatal("unexpected EOF reading request")
	}
	var req map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return req
}

// writeResponse 向 client 端 reader 写入一条 JSON-RPC 响应。
func writeResponse(t *testing.T, w io.Writer, id any, result any) {
	t.Helper()
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	fmt.Fprintf(w, "%s\n", body)
}

func TestClientInitializeHandshake(t *testing.T) {
	tr := newMockTransport()
	defer tr.Close()

	c := NewClient(tr)

	go func() {
		req := readLine(t, tr.requestReader())
		if got := req["method"]; got != "initialize" {
			t.Errorf("expected initialize, got %v", got)
		}
		if got, want := req["id"], float64(1); got != want {
			t.Errorf("expected id %v, got %v", want, got)
		}
		writeResponse(t, tr.serverWriter(), 1, map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]any{
				"name":    "test-server",
				"version": "1.0.0",
			},
			"capabilities": map[string]any{"tools": map[string]any{}},
		})

		// client 应在下一步发送 initialized notification。
		notif := readLine(t, tr.requestReader())
		if got := notif["method"]; got != "notifications/initialized" {
			t.Errorf("expected notifications/initialized, got %v", got)
		}
	}()

	version, err := c.Initialize(context.Background())
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if version != "2024-11-05" {
		t.Errorf("expected version 2024-11-05, got %s", version)
	}

	info := c.ServerInfo()
	if info.Name != "test-server" || info.Version != "1.0.0" {
		t.Errorf("unexpected server info: %+v", info)
	}

	caps := c.Capabilities()
	if _, ok := caps["tools"]; !ok {
		t.Errorf("expected tools capability, got %v", caps)
	}
}

func TestClientListTools(t *testing.T) {
	tr := newMockTransport()
	defer tr.Close()

	c := NewClient(tr)

	go func() {
		req := readLine(t, tr.requestReader())
		if got := req["method"]; got != "initialize" {
			t.Errorf("expected initialize, got %v", got)
		}
		writeResponse(t, tr.serverWriter(), 1, map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": "test-server", "version": "1.0.0"},
			"capabilities":    map[string]any{},
		})
		readLine(t, tr.requestReader()) // initialized notification

		req = readLine(t, tr.requestReader())
		if got := req["method"]; got != "tools/list" {
			t.Errorf("expected tools/list, got %v", got)
		}
		writeResponse(t, tr.serverWriter(), 2, map[string]any{
			"tools": []any{
				map[string]any{
					"name":        "read_file",
					"description": "Reads a file",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"path": map[string]any{"type": "string"}},
					},
				},
			},
		})
	}()

	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "read_file" {
		t.Errorf("expected read_file, got %s", tools[0].Name)
	}
}

func TestClientCallTool(t *testing.T) {
	tr := newMockTransport()
	defer tr.Close()

	c := NewClient(tr)

	go func() {
		req := readLine(t, tr.requestReader())
		if got := req["method"]; got != "initialize" {
			t.Errorf("expected initialize, got %v", got)
		}
		writeResponse(t, tr.serverWriter(), 1, map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": "test-server", "version": "1.0.0"},
			"capabilities":    map[string]any{},
		})
		readLine(t, tr.requestReader()) // initialized notification

		req = readLine(t, tr.requestReader())
		if got := req["method"]; got != "tools/call" {
			t.Errorf("expected tools/call, got %v", got)
		}
		params, ok := req["params"].(map[string]any)
		if !ok {
			panic(fmt.Sprintf("expected params map, got %T", req["params"]))
		}
		if got := params["name"]; got != "greet" {
			t.Errorf("expected tool name greet, got %v", got)
		}
		writeResponse(t, tr.serverWriter(), 2, map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "hello world"},
			},
		})
	}()

	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	result, err := c.CallTool(context.Background(), "greet", map[string]any{"name": "world"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.Text != "hello world" {
		t.Errorf("expected hello world, got %q", result.Text)
	}
}
