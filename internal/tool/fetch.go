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

// NewFetchURLTool 创建名为 "core/fetch_url" 的 HTTP GET 工具。
//
// 参数：
//   - url           (string,  required)：要抓取的 URL。
//   - timeout_ms    (integer, optional)：请求超时，单位毫秒（默认 30000）。
//   - max_bytes     (integer, optional)：最多读取的响应体字节数（默认 1048576）。
//   - headers       (object,  optional)：额外的 HTTP headers。
//   - extract_text  (boolean, optional)：为 true 且内容看起来像 HTML 时，
//     在返回前将响应体转为纯文本。
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

// fetchURLExecutor 执行带超时与字节上限的 HTTP GET。
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

// looksLikeHTML 根据 Content-Type 或开头的 <!doctype>/<html> 标签猜测
// 响应体是否为 HTML。
func looksLikeHTML(contentType, body string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "text/html") {
		return true
	}
	trim := strings.TrimSpace(strings.ToLower(body))
	return strings.HasPrefix(trim, "<!doctype html") || strings.HasPrefix(trim, "<html")
}

// htmlToText 仅使用 Go 标准库进行尽力而为的 HTML 到纯文本转换。
// 它移除标签、折叠空白并解码实体。这里刻意保持简单；复杂页面仍会残留
// 一些结构噪声，但输出已远小于原始 HTML。
var (
	scriptStyleRe = regexp.MustCompile(`(?i)<(script|style)[^>]*>[\s\S]*?</(script|style)>`)
	tagRe         = regexp.MustCompile(`<[^>]+>`)
	whitespaceRe  = regexp.MustCompile(`\s+`)
)

func htmlToText(htmlStr string) string {
	// 先丢弃 <script> 与 <style> 块，避免泄露 JS/CSS 文本。
	text := scriptStyleRe.ReplaceAllString(htmlStr, " ")
	// 移除剩余标签。
	text = tagRe.ReplaceAllString(text, " ")
	// 解码 HTML 实体（&lt; → <、&amp; → & 等）。
	text = html.UnescapeString(text)
	// 折叠连续空白。
	text = whitespaceRe.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

// readLimited 从 r 最多读取 max 字节，并报告数据是否被截断。
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
