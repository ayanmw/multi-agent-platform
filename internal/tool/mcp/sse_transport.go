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

// sseTransport speaks MCP over HTTP Server-Sent Events.
//
// The transport connects to a remote SSE endpoint, waits for an "endpoint"
// event that tells us where to POST outgoing JSON-RPC messages, and then
// routes incoming JSON-RPC responses (delivered as SSE "message" events)
// back to the caller. Notifications without an id are ignored.
//
// This implementation targets the 2024-11-05 MCP protocol over SSE.
type sseTransport struct {
	cfg        ServerConfig
	baseURL    *url.URL
	httpClient *http.Client

	// endpoint is the URL where outgoing JSON-RPC messages are POSTed.
	endpoint string

	mu       sync.Mutex
	closed   bool
	closeCh  chan struct{}
	body     io.ReadCloser
	readerWg sync.WaitGroup

	// responses carries JSON-RPC response lines returned by the server.
	responses chan []byte

	// initDone is closed once the endpoint URL has been received.
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

// Start opens the SSE stream and waits for the endpoint event.
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

	// Read the first endpoint event to discover where to POST messages.
	if err := t.readEndpointEvent(ctx, resp.Body); err != nil {
		resp.Body.Close()
		return err
	}

	t.readerWg.Add(1)
	go t.readLoop(resp.Body)

	return nil
}

// readEndpointEvent reads SSE events until it finds an "endpoint" event whose
// data contains the POST URL. The context deadline bounds the handshake.
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

// resolvePostEndpoint converts an endpoint event payload into an absolute URL.
func (t *sseTransport) resolvePostEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	return t.baseURL.ResolveReference(u).String(), nil
}

// readLoop drains the SSE stream and forwards JSON-RPC response messages.
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
		// Forward responses delivered as explicit "message" events or the
		// default event type (no event field). Ignore other events such as
		// "endpoint" which only occurs during handshake.
		if eventType != "" && eventType != "message" {
			return
		}
		// Ignore notifications (no id) and invalid JSON.
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

	// If the scanner exits because the body closed, signal any pending receive.
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

// Send POSTs a JSON-RPC message to the endpoint discovered during Start.
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

// Receive returns the next JSON-RPC response message delivered over SSE.
// Notifications and response lines for other requests are filtered by the
// caller (Client.request); this transport just supplies the next response line.
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

// Close shuts down the SSE connection and reader goroutine.
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
	// Ensure initDone is closed so blocked Send/Receive can return.
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
