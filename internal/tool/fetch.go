package tool

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// NewFetchURLTool creates an HTTP GET tool named "core/fetch_url".
//
// Parameters:
//   - url           (string,  required): URL to fetch.
//   - timeout_ms    (integer, optional): Request timeout in milliseconds (default 30000).
//   - max_bytes     (integer, optional): Maximum body bytes to read (default 1048576).
//   - headers       (object,  optional): Extra HTTP headers.
//   - extract_text  (boolean, optional): If true and content looks like HTML,
//     convert the body to plain text before returning.
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
				"extract_text": map[string]any{
					"type":        "boolean",
					"description": "If true and content looks like HTML, convert it to plain text",
				},
			},
			"required": []string{"url"},
		},
		fetchURLExecutor,
	).WithTags("network", "readonly").WithAliases("web_fetch")
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
	extractText := getBool(input, "extract_text", false)

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

	bodyStr := string(body)
	if extractText || looksLikeHTML(resp.Header.Get("Content-Type"), bodyStr) {
		bodyStr = htmlToText(bodyStr)
	}

	return map[string]any{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
		"body":        bodyStr,
		"url":         url,
		"truncated":   truncated,
	}, nil
}

// looksLikeHTML guesses whether a response body is HTML based on Content-Type
// or a leading <!doctype>/<html> tag.
func looksLikeHTML(contentType, body string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "text/html") {
		return true
	}
	trim := strings.TrimSpace(strings.ToLower(body))
	return strings.HasPrefix(trim, "<!doctype html") || strings.HasPrefix(trim, "<html")
}

// htmlToText performs a best-effort HTML to plain-text conversion using only
// the Go standard library. It removes tags, collapses whitespace, and decodes
// entities. This is intentionally simple; complex pages will still retain some
// structure noise, but the output is far smaller than raw HTML.
var (
	scriptStyleRe = regexp.MustCompile(`(?i)<(script|style)[^>]*>[\s\S]*?</(script|style)>`)
	tagRe         = regexp.MustCompile(`<[^>]+>`)
	whitespaceRe  = regexp.MustCompile(`\s+`)
)

func htmlToText(htmlStr string) string {
	// Drop <script> and <style> blocks first to avoid leaking JS/CSS text.
	text := scriptStyleRe.ReplaceAllString(htmlStr, " ")
	// Remove remaining tags.
	text = tagRe.ReplaceAllString(text, " ")
	// Decode HTML entities (&lt; → <, &amp; → &, etc.).
	text = html.UnescapeString(text)
	// Collapse runs of whitespace.
	text = whitespaceRe.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
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
