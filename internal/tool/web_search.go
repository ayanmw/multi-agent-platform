package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// WebSearchConfig tells NewWebSearchTool how to reach the search providers.
// A zero value enables the DuckDuckGo fallback (no API key). When DisableDDG
// is true and no other provider is configured, the executor returns a friendly
// "not configured" status.
//
// Provider identifiers (use with WEBSEARCH_PROVIDER):
//   "exa", "parallel", "bing", "google", "tavily", "brave",
//   "kimi_search", "glm_search", "duckduckgo"
//
// Provider priority when WEBSEARCH_PROVIDER is empty:
//   brave -> bing -> google -> tavily -> parallel -> exa -> duckduckgo
//
// All providers except DuckDuckGo default to off; enable them via WEBSEARCH_ENABLE_*
// and provide the corresponding API key / credentials.
type WebSearchConfig struct {
	Provider string // explicit override; empty means auto-select

	// MCP providers
	EnableExa      bool
	EnableParallel bool
	ExaAPIKey      string
	ParallelAPIKey string

	// Bing Web Search API (Azure)
	EnableBing   bool
	BingAPIKey   string
	BingEndpoint string

	// Google Custom Search JSON API
	EnableGoogle   bool
	GoogleAPIKey   string
	GoogleCX       string // search engine ID
	GoogleEndpoint string

	// Tavily Search API (developer-friendly, LLM-ready results)
	EnableTavily        bool
	TavilyAPIKey        string
	TavilyEndpoint      string
	TavilySearchDepth   string // "basic" or "advanced"
	TavilyIncludeAnswer bool

	// Brave Search API
	EnableBrave   bool
	BraveAPIKey   string
	BraveEndpoint string

	// Placeholder providers (public endpoints not yet known)
	EnableKimiSearch bool
	EnableGlmSearch  bool

	// DisableDDG turns off the DuckDuckGo fallback. Useful in environments where
	// external search must be disabled or only allowed via configured providers.
	DisableDDG bool

	// HTTPClient is used for provider requests. nil uses http.DefaultClient.
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
//   - num_results          (integer, optional): Number of results (1-30 for API/
//     MCP providers, 1-20 for DuckDuckGo; default 8).
//   - livecrawl            (string,  optional): "fallback" or "preferred" (default
//     "fallback"). Only used by Exa.
//   - search_type          (string,  optional): "auto", "fast", or "deep" (default
//     "auto"). Only used by Exa.
//   - context_max_chars    (integer, optional): Max context chars per result
//     (default 10000). Only used by Exa.
//   - session_context_id   (string,  optional): Stable ID reserved for future use.
//
// Provider selection:
//   1. If WEBSEARCH_PROVIDER is set, that provider is used (if configured).
//   2. Otherwise auto-select the first configured provider in priority order.
//   3. If nothing is configured, fall back to DuckDuckGo (unless disabled).
func NewWebSearchTool(cfg WebSearchConfig) *BuiltinTool {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 25 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "multi-agent-platform/0.1.0"
	}
	if cfg.BingEndpoint == "" {
		cfg.BingEndpoint = "https://api.bing.microsoft.com/v7.0/search"
	}
	if cfg.GoogleEndpoint == "" {
		cfg.GoogleEndpoint = "https://www.googleapis.com/customsearch/v1"
	}
	if cfg.TavilyEndpoint == "" {
		cfg.TavilyEndpoint = "https://api.tavily.com/search"
	}
	if cfg.TavilySearchDepth == "" {
		cfg.TavilySearchDepth = "basic"
	}
	if cfg.TavilySearchDepth != "basic" && cfg.TavilySearchDepth != "advanced" {
		cfg.TavilySearchDepth = "basic"
	}
	if cfg.BraveEndpoint == "" {
		cfg.BraveEndpoint = "https://api.search.brave.com/res/v1/web/search"
	}
	return NewBuiltinTool(
		"web_search",
		"core",
		"Search the web using Bing, Google, Tavily, Brave, Exa, Parallel, or DuckDuckGo (no API key). Returns a text summary of search results. Use for recent/current information beyond the model's knowledge cutoff.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"num_results": map[string]any{
					"type":        "integer",
					"description": "Number of search results to return (default: 8)",
				},
				"livecrawl": map[string]any{
					"type":        "string",
					"description": "Live crawl mode - 'fallback': use live crawling as backup if cached content unavailable, 'preferred': prioritize live crawling (default: 'fallback'). Exa only.",
				},
				"search_type": map[string]any{
					"type":        "string",
					"description": "Search type - 'auto': balanced search (default), 'fast': quick results, 'deep': comprehensive search. Exa only.",
				},
				"context_max_chars": map[string]any{
					"type":        "integer",
					"description": "Maximum characters for context string optimized for LLMs (default: 10000). Exa only.",
				},
				"session_context_id": map[string]any{
					"type":        "string",
					"description": "Optional stable ID reserved for future provider pinning",
				},
			},
			"required": []string{"query"},
		},
		func(input map[string]any) (any, error) { return webSearchExecutor(cfg, input) },
	).WithTags("network", "websearch")
}

// webSearchExecutor dispatches to the selected provider and returns the
// provider name plus the search result text.
//
// When no API provider is configured, the executor automatically falls back to
// DuckDuckGo HTML/lite search, which requires no API key. If a configured
// provider fails, DuckDuckGo is used as a last resort (unless disabled).
func webSearchExecutor(cfg WebSearchConfig, input map[string]any) (any, error) {
	query := getString(input, "query", "")
	if query == "" {
		return nil, fmt.Errorf("query required")
	}

	// Normalize optional parameters with sane defaults.
	numResults := clampInt(getInt(input, "num_results", 8), 1, 30)
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
	_ = sessionID // reserved for future stable provider pinning; currently unused

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	provider := selectWebSearchProvider(cfg)

	// If nothing is configured and DuckDuckGo is disabled, return early.
	if provider == "" && cfg.DisableDDG {
		return map[string]any{
			"status":  "not_configured",
			"message": "web_search is not configured: set WEBSEARCH_PROVIDER=<name> and the matching WEBSEARCH_*_API_KEY / enable flag, or unset WEBSEARCH_DISABLE_DDG to use the DuckDuckGo fallback.",
			"query":   query,
		}, nil
	}

	var text string
	var err error
	if provider == "" {
		// No API provider configured — go straight to DuckDuckGo.
		text, err = callDuckDuckGo(ctx, cfg, query, numResults)
		provider = "duckduckgo"
	} else {
		switch provider {
		case "parallel":
			text, err = callParallel(ctx, cfg, query)
		case "exa":
			text, err = callExa(ctx, cfg, exaSearchArgs{
				Query:                query,
				Type:                 searchType,
				NumResults:           numResults,
				Livecrawl:            livecrawl,
				ContextMaxCharacters: contextMaxChars,
			})
		case "bing":
			text, err = callBing(ctx, cfg, query, numResults)
		case "google":
			text, err = callGoogle(ctx, cfg, query, numResults)
		case "tavily":
			text, err = callTavily(ctx, cfg, query, numResults)
		case "brave":
			text, err = callBrave(ctx, cfg, query, numResults)
		case "kimi_search", "glm_search":
			text, err = "", fmt.Errorf("provider %s is not yet implemented", provider)
		default:
			text, err = "", fmt.Errorf("unknown web search provider: %s", provider)
		}
	}

	// If the chosen provider failed, try DuckDuckGo as a robust fallback
	// when the fallback is not disabled and the provider was a real API.
	if err != nil && provider != "duckduckgo" && !cfg.DisableDDG && !isPlaceholderProvider(provider) {
		dgText, dgErr := callDuckDuckGo(ctx, cfg, query, numResults)
		if dgErr == nil {
			provider = "duckduckgo"
			text = dgText
			err = nil
		}
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

// selectWebSearchProvider picks a configured provider deterministically.
// Priority order: brave -> bing -> google -> tavily -> parallel -> exa.
// When no provider is configured, it returns "" so the caller uses DuckDuckGo.
func selectWebSearchProvider(cfg WebSearchConfig) string {
	if cfg.Provider != "" {
		return cfg.Provider
	}
	if cfg.EnableBrave || cfg.BraveAPIKey != "" {
		return "brave"
	}
	if cfg.EnableBing || cfg.BingAPIKey != "" {
		return "bing"
	}
	if cfg.EnableGoogle || (cfg.GoogleAPIKey != "" && cfg.GoogleCX != "") {
		return "google"
	}
	if cfg.EnableTavily || cfg.TavilyAPIKey != "" {
		return "tavily"
	}
	if cfg.EnableParallel || cfg.ParallelAPIKey != "" {
		return "parallel"
	}
	if cfg.EnableExa || cfg.ExaAPIKey != "" {
		return "exa"
	}
	return ""
}

// isPlaceholderProvider reports providers that have no public API endpoint yet.
func isPlaceholderProvider(provider string) bool {
	switch provider {
	case "kimi_search", "glm_search":
		return true
	}
	return false
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

// searchResult is a provider-agnostic search result used by the unified
// formatter.
type searchResult struct {
	Title string
	URL   string
	Desc  string
}

// formatSearchResults converts parsed results into a concise text summary
// suitable for inclusion in an LLM context.
func formatSearchResults(results []searchResult) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Search results (%d):\n\n", len(results))
	for i, r := range results {
		_, _ = fmt.Fprintf(&b, "%d. %s\n   URL: %s\n   %s\n\n", i+1, r.Title, r.URL, r.Desc)
	}
	return strings.TrimSpace(b.String())
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
// Bing Web Search API provider
// ---------------------------------------------------------------------------

// bingResponse is the subset of the Bing Web Search API v7 response we need.
type bingResponse struct {
	WebPages struct {
		Value []struct {
			Name    string `json:"name"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		} `json:"value"`
	} `json:"webPages"`
}

func callBing(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	u, err := url.Parse(cfg.BingEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid bing endpoint: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", clampInt(numResults, 1, 50)))
	q.Set("setLang", "en-US")
	q.Set("textDecorations", "false")
	q.Set("responseFilter", "Webpages")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", cfg.BingAPIKey)
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	raw, err := doHTTPRead(ctx, cfg, req)
	if err != nil {
		return "", err
	}

	var resp bingResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("parse bing response: %w", err)
	}
	results := make([]searchResult, 0, len(resp.WebPages.Value))
	for _, v := range resp.WebPages.Value {
		results = append(results, searchResult{Title: v.Name, URL: v.URL, Desc: v.Snippet})
	}
	return formatSearchResults(results), nil
}

// ---------------------------------------------------------------------------
// Google Custom Search JSON API provider
// ---------------------------------------------------------------------------

// googleResponse is the subset of the Google Custom Search JSON API response.
type googleResponse struct {
	Items []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"items"`
}

func callGoogle(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	u, err := url.Parse(cfg.GoogleEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid google endpoint: %w", err)
	}
	q := u.Query()
	q.Set("key", cfg.GoogleAPIKey)
	q.Set("cx", cfg.GoogleCX)
	q.Set("q", query)
	q.Set("num", fmt.Sprintf("%d", clampInt(numResults, 1, 10)))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	raw, err := doHTTPRead(ctx, cfg, req)
	if err != nil {
		return "", err
	}

	var resp googleResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("parse google response: %w", err)
	}
	results := make([]searchResult, 0, len(resp.Items))
	for _, item := range resp.Items {
		results = append(results, searchResult{Title: item.Title, URL: item.Link, Desc: item.Snippet})
	}
	return formatSearchResults(results), nil
}

// ---------------------------------------------------------------------------
// Tavily Search API provider
// ---------------------------------------------------------------------------

// tavilyResponse is the subset of the Tavily search response we need.
type tavilyResponse struct {
	Answer  string `json:"answer"`
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// tavilyRequest is the JSON body sent to Tavily.
type tavilyRequest struct {
	Query         string `json:"query"`
	MaxResults    int    `json:"max_results"`
	SearchDepth   string `json:"search_depth"`
	IncludeAnswer bool   `json:"include_answer"`
}

func callTavily(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	body, err := json.Marshal(tavilyRequest{
		Query:         query,
		MaxResults:    clampInt(numResults, 1, 20),
		SearchDepth:   cfg.TavilySearchDepth,
		IncludeAnswer: cfg.TavilyIncludeAnswer,
	})
	if err != nil {
		return "", fmt.Errorf("marshal tavily request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TavilyEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.TavilyAPIKey)
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	raw, err := doHTTPRead(ctx, cfg, req)
	if err != nil {
		return "", err
	}

	var resp tavilyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("parse tavily response: %w", err)
	}

	var b strings.Builder
	if cfg.TavilyIncludeAnswer && resp.Answer != "" {
		_, _ = fmt.Fprintf(&b, "Answer: %s\n\n", resp.Answer)
	}
	results := make([]searchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, searchResult{Title: r.Title, URL: r.URL, Desc: r.Content})
	}
	_, _ = b.WriteString(formatSearchResults(results))
	return strings.TrimSpace(b.String()), nil
}

// ---------------------------------------------------------------------------
// Brave Search API provider
// ---------------------------------------------------------------------------

// braveResponse is the subset of the Brave Web Search API response we need.
type braveResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

func callBrave(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	u, err := url.Parse(cfg.BraveEndpoint)
	if err != nil {
		return "", fmt.Errorf("invalid brave endpoint: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", clampInt(numResults, 1, 20)))
	q.Set("offset", "0")
	q.Set("result_filter", "web")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Subscription-Token", cfg.BraveAPIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", cfg.UserAgent)

	raw, err := doHTTPRead(ctx, cfg, req)
	if err != nil {
		return "", err
	}

	var resp braveResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("parse brave response: %w", err)
	}
	results := make([]searchResult, 0, len(resp.Web.Results))
	for _, r := range resp.Web.Results {
		results = append(results, searchResult{Title: r.Title, URL: r.URL, Desc: r.Description})
	}
	return formatSearchResults(results), nil
}

// ---------------------------------------------------------------------------
// Shared HTTP helper
// ---------------------------------------------------------------------------

// doHTTPRead executes req using cfg.HTTPClient (or http.DefaultClient) and
// returns the response body capped at 1 MB. It returns an error for non-2xx
// status codes. The request must already have its context set by the caller.
func doHTTPRead(_ context.Context, cfg WebSearchConfig, req *http.Request) ([]byte, error) {
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	const maxBody = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBody {
		return nil, fmt.Errorf("response exceeded %d bytes", maxBody)
	}
	return raw, nil
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

// urlEncode is a tiny helper to percent-encode query values for Exa.
// DuckDuckGo uses net/url directly.
func urlEncode(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_.~-]`)
	return re.ReplaceAllStringFunc(s, func(c string) string {
		return fmt.Sprintf("%%%02X", c[0])
	})
}

// ---------------------------------------------------------------------------
// DuckDuckGo fallback provider
// ---------------------------------------------------------------------------

// duckDuckGoResult is the internal shape of an organic DuckDuckGo result.
// Kept separate from searchResult because the parser extracts raw HTML text.
type duckDuckGoResult struct {
	Title       string
	URL         string
	Description string
}

// callDuckDuckGo searches DuckDuckGo via the lite/html endpoint and returns a
// plain-text summary of the top results. It requires no API key and acts as the
// zero-config fallback for core/web_search.
//
// It first tries the JavaScript-friendly HTML endpoint, then falls back to the
// lite endpoint if the first returns no parseable results.
func callDuckDuckGo(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	if numResults <= 0 {
		numResults = 8
	}
	if numResults > 30 {
		numResults = 30
	}

	// Try the main HTML search endpoint first.
	text, err := callDuckDuckGoHTML(ctx, cfg, query, numResults)
	if err == nil && text != "" {
		return text, nil
	}

	// Fall back to the lite endpoint, which is simpler and often less protected.
	return callDuckDuckGoLite(ctx, cfg, query, numResults)
}

func callDuckDuckGoHTML(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	u := "https://html.duckduckgo.com/html/"
	form := url.Values{}
	form.Set("q", query)
	form.Set("kl", "us-en")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "text/html")

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
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return "", err
	}
	if len(raw) > 1<<20 {
		return "", fmt.Errorf("response exceeded %d bytes", 1<<20)
	}

	results := parseDuckDuckGoHTML(string(raw), numResults)
	if len(results) == 0 {
		return "", fmt.Errorf("no results parsed")
	}
	return formatDuckDuckGoResults(results), nil
}

func callDuckDuckGoLite(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	u := fmt.Sprintf("https://lite.duckduckgo.com/lite/?q=%s&kl=us-en", url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "text/html")

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
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return "", err
	}
	if len(raw) > 1<<20 {
		return "", fmt.Errorf("response exceeded %d bytes", 1<<20)
	}

	results := parseDuckDuckGoLiteHTML(string(raw), numResults)
	if len(results) == 0 {
		return "", fmt.Errorf("no results parsed")
	}
	return formatDuckDuckGoResults(results), nil
}

// duckResultTitleRe matches result titles in DuckDuckGo HTML.
var duckResultTitleRe = regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>`)

// duckResultSnippetRe matches result snippets in DuckDuckGo HTML.
var duckResultSnippetRe = regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>([\s\S]*?)</a>`)

func parseDuckDuckGoHTML(body string, limit int) []duckDuckGoResult {
	return parseDuckDuckGoBody(body, duckResultTitleRe, duckResultSnippetRe, limit)
}

// liteTitleRe matches result titles in DuckDuckGo lite.
var liteTitleRe = regexp.MustCompile(`<a[^>]*href="([^"]*)"[^>]*class="[^"]*result-link[^"]*"[^>]*>([\s\S]*?)</a>`)

// liteSnippetRe matches snippets in DuckDuckGo lite.
var liteSnippetRe = regexp.MustCompile(`<td[^>]*class="result-snippet"[^>]*>([\s\S]*?)</td>`)

func parseDuckDuckGoLiteHTML(body string, limit int) []duckDuckGoResult {
	return parseDuckDuckGoBody(body, liteTitleRe, liteSnippetRe, limit)
}

// parseDuckDuckGoBody extracts organic results from DuckDuckGo HTML using the
// provided title and snippet regexes. It strips tags, decodes entities, and
// returns up to limit results.
func parseDuckDuckGoBody(body string, titleRe, snippetRe *regexp.Regexp, limit int) []duckDuckGoResult {
	var results []duckDuckGoResult
	titleMatches := titleRe.FindAllStringSubmatch(body, -1)
	snippetMatches := snippetRe.FindAllStringSubmatch(body, -1)
	for i, m := range titleMatches {
		if len(m) < 3 {
			continue
		}
		if i >= limit {
			break
		}
		link := htmlUnescape(strings.TrimSpace(m[1]))
		title := htmlUnescape(stripTags(strings.TrimSpace(m[2])))
		desc := ""
		if i < len(snippetMatches) && len(snippetMatches[i]) >= 2 {
			desc = htmlUnescape(stripTags(strings.TrimSpace(snippetMatches[i][1])))
		}
		results = append(results, duckDuckGoResult{
			Title:       title,
			URL:         link,
			Description: desc,
		})
	}
	return results
}

var duckTagRe = regexp.MustCompile(`<[^>]+>`)

// stripTags removes HTML tags.
func stripTags(s string) string {
	return duckTagRe.ReplaceAllString(s, " ")
}

// htmlUnescape performs a minimal set of HTML entity replacements.
func htmlUnescape(s string) string {
	replacements := map[string]string{
		"&nbsp;": " ",
		"&amp;":  "&",
		"&lt;":   "<",
		"&gt;":   ">",
		"&quot;": "\"",
		"&#39;":  "'",
	}
	for old, new := range replacements {
		s = strings.ReplaceAll(s, old, new)
	}
	return s
}

// formatDuckDuckGoResults converts parsed DuckDuckGo results into text.
func formatDuckDuckGoResults(results []duckDuckGoResult) string {
	out := make([]searchResult, 0, len(results))
	for _, r := range results {
		out = append(out, searchResult{Title: r.Title, URL: r.URL, Desc: r.Description})
	}
	return formatSearchResults(out)
}
