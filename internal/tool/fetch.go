package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// NewFetchURLTool creates an HTTP GET tool named "core/fetch_url".
//
// Parameters:
//   - url        (string,  required): URL to fetch.
//   - timeout_ms (integer, optional): Request timeout in milliseconds (default 30000).
//   - max_bytes  (integer, optional): Maximum body bytes to read (default 1048576).
//   - headers    (object,  optional): Extra HTTP headers.
func NewFetchURLTool() *BuiltinTool {
	return NewBuiltinTool(
		"fetch_url",
		"core",
		"Fetch a URL via HTTP GET. Returns status code, headers, body, and truncated flag.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "URL to fetch",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Request timeout in milliseconds (default 30000)",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "Maximum body bytes to read (default 1048576)",
				},
				"headers": map[string]any{
					"type":        "object",
					"description": "Extra HTTP headers",
				},
			},
			"required": []string{"url"},
		},
		fetchURLExecutor,
	).WithTags("network", "readonly")
}

// fetchURLExecutor performs an HTTP GET with timeout and byte limits.
func fetchURLExecutor(input map[string]any) (any, error) {
	url := getString(input, "url", "")
	if url == "" {
		return nil, fmt.Errorf("url required")
	}
	timeout := time.Duration(getInt(input, "timeout_ms", 30000)) * time.Millisecond
	maxBytes := int64(getInt(input, "max_bytes", 1<<20))
	headers := getMap(input, "headers")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, fmt.Sprintf("%v", v))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, truncated, err := readLimited(resp.Body, maxBytes)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        string(body),
		"url":         url,
		"truncated":   truncated,
	}, nil
}

// readLimited reads up to max bytes from r and reports whether the data was
// truncated.
func readLimited(r io.Reader, max int64) ([]byte, bool, error) {
	lr := io.LimitReader(r, max+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > max {
		return data[:max], true, nil
	}
	return data, false, nil
}
