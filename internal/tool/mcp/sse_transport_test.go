package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeSSEEvent 向 w 写入一条格式化的 SSE 事件。
func writeSSEEvent(w ioStringWriter, event, data string) {
	fmt.Fprintf(w, "event: %s\n", event)
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// ioStringWriter 匹配我们从 http.ResponseWriter 所需的方法集合。
type ioStringWriter interface {
	WriteString(string) (int, error)
	Write([]byte) (int, error)
	Flush()
}

// TestSSETransportEndpointDiscovery verifies the handshake: connect SSE, get endpoint event.
func TestSSETransportEndpointDiscovery(t *testing.T) {
	postPath := "/messages/123"
	var connected bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response writer does not support flushing")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			writeSSEEvent(struct {
				ioStringWriter
			}{w.(ioStringWriter)}, "endpoint", postPath)
			flusher.Flush()
			// 保持连接打开，直到测试结束。
			<-r.Context().Done()
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == postPath {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tr := newSSETransport(ServerConfig{Endpoint: server.URL + "/sse"})
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	if !strings.HasSuffix(tr.endpoint, postPath) {
		t.Fatalf("expected endpoint to end with %s, got %s", postPath, tr.endpoint)
	}
	if !connected {
		t.Log("transport connected successfully")
	}
}

// TestSSETransportRoundTrip 验证我们能 POST 一条请求并收到匹配的 JSON-RPC 响应。
func TestSSETransportRoundTrip(t *testing.T) {
	postPath := "/messages/session-abc"
	type pending struct {
		id   int64
		done chan struct{}
	}

	var mu sync.Mutex
	var pendingReq *pending
	messageArrived := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("no flusher")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			writeSSEEvent(w.(ioStringWriter), "endpoint", postPath)
			flusher.Flush()

			// 等到有消息被 POST 进来，再发送响应。
			<-messageArrived
			// 给 POST goroutine 一点时间开始等待。
			time.Sleep(20 * time.Millisecond)

			mu.Lock()
			id := pendingReq.id
			mu.Unlock()
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result":  map[string]any{"ok": true},
			}
			body, _ := json.Marshal(resp)
			writeSSEEvent(w.(ioStringWriter), "message", string(body))
			flusher.Flush()
			<-r.Context().Done()
		case http.MethodPost:
			var req jsonRPCRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode post: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			pendingReq = &pending{id: req.ID, done: make(chan struct{})}
			mu.Unlock()
			close(messageArrived)
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	tr := newSSETransport(ServerConfig{Endpoint: server.URL + "/sse"})
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	msg, _ := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: map[string]any{}})
	if err := tr.Send(msg); err != nil {
		t.Fatalf("send: %v", err)
	}

	got, err := tr.Receive(3 * time.Second)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(got, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	id, err := parseJSONRPCID(resp.ID)
	if err != nil {
		t.Fatalf("parse id: %v", err)
	}
	if id != 1 {
		t.Fatalf("expected id 1, got %d", id)
	}
}

// TestSSETransportInitialize 验证 MCP Client 能通过 SSE 完成 initialize。
func TestSSETransportInitialize(t *testing.T) {
	postPath := "/messages/init"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			flusher, _ := w.(http.Flusher)
			w.Header().Set("Content-Type", "text/event-stream")
			writeSSEEvent(w.(ioStringWriter), "endpoint", postPath)
			flusher.Flush()
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]any{"name": "sse-server", "version": "1.0.0"},
					"capabilities":    map[string]any{},
				},
			})
			writeSSEEvent(w.(ioStringWriter), "message", string(resp))
			flusher.Flush()
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	cfg := ServerConfig{Name: "test-sse", Transport: "sse", Endpoint: server.URL + "/sse", Enabled: true}
	tr, err := newTransport(cfg)
	if err != nil {
		t.Fatalf("newTransport: %v", err)
	}
	defer tr.Close()

	client := NewClient(tr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// newTransport 返回的是一个未初始化的 transport；loader/manager 会
	// 调用 Start，但单元测试必须在使用前手动 start。
	if err := tr.Start(ctx); err != nil {
		t.Fatalf("start transport: %v", err)
	}
	version, err := client.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if version != "2024-11-05" {
		t.Fatalf("unexpected version %s", version)
	}
	if client.ServerInfo().Name != "sse-server" {
		t.Fatalf("unexpected server info %+v", client.ServerInfo())
	}
}
