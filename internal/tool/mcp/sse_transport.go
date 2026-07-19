package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// sseTransport 通过 HTTP Server-Sent Events 承载 MCP 协议。
//
// transport 连接到远端 SSE endpoint，等待一个 "endpoint" 事件——它告知我们
// 要把发出的 JSON-RPC 消息 POST 到哪里——然后把到达的 JSON-RPC 响应（以 SSE
// "message" 事件形式投递）路由回调用方。没有 id 的 notification 会被忽略。
//
// 本实现面向 2024-11-05 MCP 协议 over SSE。
type sseTransport struct {
	cfg        ServerConfig
	baseURL    *url.URL
	httpClient *http.Client

	// endpoint 是发出 JSON-RPC 消息时 POST 的目标 URL。
	endpoint string

	mu       sync.Mutex
	closed   bool
	closeCh  chan struct{}
	body     io.ReadCloser
	readerWg sync.WaitGroup

	// responses 承载 server 返回的 JSON-RPC 响应行。
	responses chan []byte

	// initDone 在收到 endpoint URL 后被关闭。
	initDone chan struct{}
	initErr  error
}

func newSSETransport(cfg ServerConfig) *sseTransport {
	return &sseTransport{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 0},
		closeCh:    make(chan struct{}),
		responses:  make(chan []byte, 16),
		initDone:   make(chan struct{}),
	}
}

// Start 打开 SSE 流并等待 endpoint 事件。
func (t *sseTransport) Start(ctx context.Context) error {
	if t.cfg.Endpoint == "" {
		return fmt.Errorf("sse transport requires endpoint")
	}

	base, err := url.Parse(t.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}
	t.baseURL = base

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.cfg.Endpoint, nil)
	if err != nil {
		return fmt.Errorf("build sse request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect sse endpoint: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		resp.Body.Close()
		return fmt.Errorf("sse endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		resp.Body.Close()
		return fmt.Errorf("unexpected sse content-type %q", ct)
	}

	t.body = resp.Body

	// 读取第一个 endpoint 事件以发现消息要 POST 到哪里。
	if err := t.readEndpointEvent(ctx, resp.Body); err != nil {
		resp.Body.Close()
		return err
	}

	t.readerWg.Add(1)
	go t.readLoop(resp.Body)

	return nil
}

// readEndpointEvent 持续读取 SSE 事件，直到找到 data 中包含 POST URL 的
// "endpoint" 事件。context 的 deadline 限制握手时长。
func (t *sseTransport) readEndpointEvent(ctx context.Context, body io.Reader) error {
	deadline, hasDeadline := ctx.Deadline()
	scanTimeout := 30 * time.Second
	if hasDeadline {
		if d := time.Until(deadline); d > 0 && d < scanTimeout {
			scanTimeout = d
		}
	}

	scanner := bufio.NewScanner(body)
	var eventType string
	var data strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			dataStr := strings.TrimSuffix(data.String(), "\n")
			if eventType == "endpoint" && dataStr != "" {
				endpointURL, err := t.resolvePostEndpoint(dataStr)
				if err != nil {
					return fmt.Errorf("resolve post endpoint: %w", err)
				}
				t.endpoint = endpointURL
				close(t.initDone)
				return nil
			}
			eventType = ""
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon == -1 {
			continue
		}
		field := line[:colon]
		value := line[colon+1:]
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			eventType = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sse handshake read: %w", err)
	}
	return fmt.Errorf("sse stream closed before endpoint event")
}

// resolvePostEndpoint 把 endpoint 事件 payload 转换为绝对 URL。
func (t *sseTransport) resolvePostEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	return t.baseURL.ResolveReference(u).String(), nil
}

// readLoop 排空 SSE 流并转发 JSON-RPC 响应消息。
func (t *sseTransport) readLoop(body io.Reader) {
	defer t.readerWg.Done()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var eventType string
	var data strings.Builder

	flush := func() {
		dataStr := strings.TrimSuffix(data.String(), "\n")
		if dataStr == "" {
			return
		}
		// 转发以显式 "message" 事件或默认事件类型（无 event 字段）投递的
		// 响应。忽略其它事件，比如只在握手中出现的 "endpoint"。
		if eventType != "" && eventType != "message" {
			return
		}
		// 忽略 notification（无 id）和无效 JSON。
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal([]byte(dataStr), &msg); err != nil {
			return
		}
		if len(msg.ID) == 0 {
			return
		}
		select {
		case t.responses <- []byte(dataStr):
		case <-t.closeCh:
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			eventType = ""
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon == -1 {
			continue
		}
		field := line[:colon]
		value := line[colon+1:]
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "event":
			eventType = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}

	flush()

	// 若 scanner 因 body 关闭而退出，则通知所有阻塞中的 receive。
	select {
	case <-t.closeCh:
	default:
		t.mu.Lock()
		if !t.closed {
			t.initErr = fmt.Errorf("sse stream closed")
			select {
			case <-t.initDone:
			default:
				close(t.initDone)
			}
			_ = t.closeLocked()
		}
		t.mu.Unlock()
	}
}

// Send 把 JSON-RPC 消息 POST 到 Start 中发现的 endpoint。
func (t *sseTransport) Send(message []byte) error {
	<-t.initDone

	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return fmt.Errorf("transport closed")
	}

	if t.endpoint == "" {
		return fmt.Errorf("sse endpoint not ready")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(message))
	if err != nil {
		return fmt.Errorf("build post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("post message returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}

// Receive 返回下一条通过 SSE 投递的 JSON-RPC 响应消息。
// notification 以及针对其它请求的响应行由调用方（Client.request）负责过滤；
// 本 transport 仅负责提供下一条响应行。
func (t *sseTransport) Receive(timeout time.Duration) ([]byte, error) {
	<-t.initDone

	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		if t.initErr != nil {
			return nil, t.initErr
		}
		return nil, fmt.Errorf("transport closed")
	}

	if t.endpoint == "" {
		return nil, fmt.Errorf("sse endpoint not ready")
	}

	select {
	case line := <-t.responses:
		return line, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("receive timeout")
	case <-t.closeCh:
		return nil, fmt.Errorf("transport closed")
	}
}

// Close 关闭 SSE 连接和 reader goroutine。
func (t *sseTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closeLocked()
}

func (t *sseTransport) closeLocked() error {
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.closeCh)
	if t.body != nil {
		_ = t.body.Close()
	}
	// 确保 initDone 已关闭，以便阻塞的 Send/Receive 能返回。
	select {
	case <-t.initDone:
	default:
		if t.initErr == nil {
			t.initErr = fmt.Errorf("transport closed")
		}
		close(t.initDone)
	}
	t.readerWg.Wait()
	return nil
}
