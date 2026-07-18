package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// WebSearchConfig tells NewWebSearchTool how to reach the search providers.
// A zero value disables real search and the executor returns a friendly
// "not configured" status.
//
// Priority (first truthy wins):
//  1. Provider override
//  2. EnableParallel
//  3. EnableExa
//  4. Stable hash of a session/context ID so repeated runs pick the same provider
//
// The default free endpoints are hosted MCP-over-HTTP services:
//   - Exa    : https://mcp.exa.ai/mcp
//   - Parallel: https://search.parallel.ai/mcp
//
// API keys are optional for Exa (append ?exaApiKey=...) and required for
// Parallel (sent as Authorization: Bearer).
type WebSearchConfig struct {
	Provider string // "exa" or "parallel"; empty means auto-select

	EnableExa      bool
	EnableParallel bool

	ExaAPIKey      string
	ParallelAPIKey string

	// HTTPClient is used for MCP requests. nil uses http.DefaultClient.
	HTTPClient *http.Client

	// UserAgent sent to providers. Defaults to "multi-agent-platform/0.1.0".
	UserAgent string

	// Timeout for a single provider call. Defaults to 25s.
	Timeout time.Duration
}

// NewWebSearchTool creates a web search tool named "core/web_search".
//
// Parameters:
//   - query                (string, required): Search query.
//   - num_results          (integer, optional): Number of results (1-20, default 8).
//   - livecrawl            (string,  optional): "fallback" or "preferred" (default "fallback").
//   - search_type          (string,  optional): "auto", "fast", or "deep" (default "auto").
//   - context_max_chars    (integer, optional): Max context chars per result (default 10000).
//   - session_context_id   (string,  optional): Stable ID used to pin provider selection.
func NewWebSearchTool(cfg WebSearchConfig) *BuiltinTool {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 25 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "multi-agent-platform/0.1.0"
	}
	return NewBuiltinTool(
		"web_search",
		"core",
		"Search the web using Exa or Parallel. Returns a text summary of search results. Use for recent/current information beyond the model's knowledge cutoff.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"num_results": map[string]any{
					"type":        "integer",
					"description": "Number of search results to return (default: 8, maximum: 20)",
				},
				"livecrawl": map[string]any{
					"type":        "string",
					"description": "Live crawl mode - 'fallback': use live crawling as backup if cached content unavailable, 'preferred': prioritize live crawling (default: 'fallback')",
				},
				"search_type": map[string]any{
					"type":        "string",
					"description": "Search type - 'auto': balanced search (default), 'fast': quick results, 'deep': comprehensive search",
				},
				"context_max_chars": map[string]any{
					"type":        "integer",
					"description": "Maximum characters for context string optimized for LLMs (default: 10000)",
				},
				"session_context_id": map[string]any{
					"type":        "string",
					"description": "Optional stable ID used to pin provider selection across repeated calls",
				},
			},
			"required": []string{"query"},
		},
		func(input map[string]any) (any, error) { return webSearchExecutor(cfg, input) },
	).WithTags("network", "websearch")
}

// webSearchExecutor dispatches to the selected provider and returns the
// provider name plus the search result text.
func webSearchExecutor(cfg WebSearchConfig, input map[string]any) (any, error) {
	query := getString(input, "query", "")
	if query == "" {
		return nil, fmt.Errorf("query required")
	}

	// Normalize optional parameters with sane defaults.
	numResults := clampInt(getInt(input, "num_results", 8), 1, 20)
	livecrawl := getString(input, "livecrawl", "fallback")
	if livecrawl != "fallback" && livecrawl != "preferred" {
		livecrawl = "fallback"
	}
	searchType := getString(input, "search_type", "auto")
	if searchType != "auto" && searchType != "fast" && searchType != "deep" {
		searchType = "auto"
	}
	contextMaxChars := getInt(input, "context_max_chars", 10000)
	if contextMaxChars <= 0 {
		contextMaxChars = 10000
	}
	sessionID := getString(input, "session_context_id", "")

	// If no provider is available at all, return a deterministic status so
	// callers see a friendly message instead of an opaque error.
	if !cfg.EnableExa && !cfg.EnableParallel && cfg.Provider == "" && cfg.ExaAPIKey == "" && cfg.ParallelAPIKey == "" {
		return map[string]any{
			"status":  "not_configured",
			"message": "web_search is not configured: set WEBSEARCH_PROVIDER=exa|parallel, or supply WEBSEARCH_ENABLE_EXA/ENABLE_PARALLEL and an API key.",
			"query":   query,
		}, nil
	}

	provider := selectWebSearchProvider(sessionID, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	var text string
	var err error
	if provider == "parallel" {
		text, err = callParallel(ctx, cfg, query)
	} else {
		text, err = callExa(ctx, cfg, exaSearchArgs{
			Query:                query,
			Type:                 searchType,
			NumResults:           numResults,
			Livecrawl:            livecrawl,
			ContextMaxCharacters: contextMaxChars,
		})
	}

	if err != nil {
		return nil, fmt.Errorf("web_search %s failed: %w", provider, err)
	}
	if text == "" {
		text = "No search results found. Please try a different query."
	}

	return map[string]any{
		"provider": provider,
		"text":     text,
		"query":    query,
	}, nil
}

// selectWebSearchProvider picks a provider deterministically. The stable hash
// lets two calls with the same session_context_id hit the same provider.
func selectWebSearchProvider(sessionID string, cfg WebSearchConfig) string {
	if cfg.Provider == "exa" || cfg.Provider == "parallel" {
		return cfg.Provider
	}
	if cfg.EnableParallel {
		return "parallel"
	}
	if cfg.EnableExa {
		return "exa"
	}
	if cfg.ParallelAPIKey != "" {
		return "parallel"
	}
	if cfg.ExaAPIKey != "" {
		return "exa"
	}
	// Free tier fallback: stable pseudo-random choice.
	if stableEven(sessionID) {
		return "exa"
	}
	return "parallel"
}

// stableEven returns a stable boolean based on a hash of the input string.
func stableEven(s string) bool {
	if s == "" {
		s = "default"
	}
	sum := 0
	for _, r := range s {
		sum += int(r)
	}
	return sum%2 == 0
}

// clampInt bounds v to [min, max].
func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// ---------------------------------------------------------------------------
// Exa provider
// ---------------------------------------------------------------------------

const exaURL = "https://mcp.exa.ai/mcp"

// exaSearchArgs mirrors the Exa web_search_exa tool arguments.
type exaSearchArgs struct {
	Query                string `json:"query"`
	Type                 string `json:"type"`
	NumResults           int    `json:"numResults"`
	Livecrawl            string `json:"livecrawl"`
	ContextMaxCharacters int    `json:"contextMaxCharacters,omitempty"`
}

func callExa(ctx context.Context, cfg WebSearchConfig, args exaSearchArgs) (string, error) {
	url := exaURL
	if cfg.ExaAPIKey != "" {
		url = fmt.Sprintf("%s?exaApiKey=%s", exaURL, urlEncode(cfg.ExaAPIKey))
	}
	return callMCPHTTP(ctx, cfg, url, "web_search_exa", args)
}

// ---------------------------------------------------------------------------
// Parallel provider
// ---------------------------------------------------------------------------

const parallelURL = "https://search.parallel.ai/mcp"

// parallelSearchArgs mirrors the Parallel web_search tool arguments.
type parallelSearchArgs struct {
	Objective     string   `json:"objective"`
	SearchQueries []string `json:"search_queries"`
	SessionID     string   `json:"session_id,omitempty"`
}

func callParallel(ctx context.Context, cfg WebSearchConfig, query string) (string, error) {
	headers := map[string]string{
		"User-Agent": cfg.UserAgent,
	}
	if cfg.ParallelAPIKey != "" {
		headers["Authorization"] = "Bearer " + cfg.ParallelAPIKey
	}
	args := parallelSearchArgs{
		Objective:     query,
		SearchQueries: []string{query},
	}
	return callMCPHTTP(ctx, cfg, parallelURL, "web_search", args, headers)
}

// ---------------------------------------------------------------------------
// Shared MCP-over-HTTP helper
// ---------------------------------------------------------------------------

// mcpRequest is a JSON-RPC 2.0 tools/call request body.
type mcpRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// mcpResponse is the shape returned by tools/call, with content as an array of
// typed items. We extract the first text item.
type mcpResponse struct {
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// callMCPHTTP sends a JSON-RPC tools/call POST to a remote MCP endpoint using
// plain HTTP (transport-level MCP). It accepts optional extra headers and
// parses both direct JSON and SSE-style "data: ..." frames.
func callMCPHTTP(ctx context.Context, cfg WebSearchConfig, url, toolName string, arguments any, extraHeaders ...map[string]string) (string, error) {
	params := map[string]any{
		"name":      toolName,
		"arguments": arguments,
	}
	body, err := json.Marshal(mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  params,
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", cfg.UserAgent)
	for _, h := range extraHeaders {
		for k, v := range h {
			req.Header.Set(k, v)
		}
	}

	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Best-effort read of error body for diagnostics.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	const maxBody = 256 * 1024
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxBody {
		return "", fmt.Errorf("response exceeded %d bytes", maxBody)
	}

	text, err := parseMCPResponse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	return text, nil
}

// parseMCPResponse extracts the first text content from either a plain
// JSON-RPC response or an SSE stream with "data: ..." frames.
func parseMCPResponse(body string) (string, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", nil
	}
	if text, ok := tryExtractText(trimmed); ok {
		return text, nil
	}
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if text, ok := tryExtractText(strings.TrimPrefix(line, "data: ")); ok {
			return text, nil
		}
	}
	return "", fmt.Errorf("could not find text content in response")
}

func tryExtractText(payload string) (string, bool) {
	payload = strings.TrimSpace(payload)
	if payload == "" || payload == "[DONE]" {
		return "", false
	}
	if !strings.HasPrefix(payload, "{") {
		return "", false
	}
	var rpc mcpResponse
	if err := json.Unmarshal([]byte(payload), &rpc); err != nil {
		return "", false
	}
	if rpc.Error != nil {
		return "", false
	}
	for _, item := range rpc.Result.Content {
		if item.Type == "text" {
			return item.Text, true
		}
	}
	return "", true
}

// urlEncode is a tiny helper to percent-encode query values without importing
// net/url only for this single call.
func urlEncode(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_.~-]`)
	return re.ReplaceAllStringFunc(s, func(c string) string {
		return fmt.Sprintf("%%%02X", c[0])
	})
}
