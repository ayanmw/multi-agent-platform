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

// WebSearchConfig 告知 NewWebSearchTool 如何连接各搜索 provider。
// 零值会启用 DuckDuckGo 回退（无需 API key）。当 DisableDDG 为 true 且
// 没有配置其他 provider 时，executor 返回友好的 "not configured" 状态。
//
// Provider 标识符（配合 WEBSEARCH_PROVIDER 使用）：
//   "exa", "parallel", "bing", "google", "tavily", "brave",
//   "kimi_search", "glm_search", "duckduckgo"
//
// WEBSEARCH_PROVIDER 为空时的 provider 优先级：
//   brave -> bing -> google -> tavily -> parallel -> exa -> duckduckgo
//
// 除 DuckDuckGo 外的所有 provider 默认关闭；通过 WEBSEARCH_ENABLE_* 启用
// 并提供对应的 API key / 凭证。
type WebSearchConfig struct {
	Provider string // 显式覆盖；空表示自动选择

	// MCP provider
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
	GoogleCX       string // 搜索引擎 ID
	GoogleEndpoint string

	// Tavily Search API（对开发者友好、LLM-ready 的结果）
	EnableTavily        bool
	TavilyAPIKey        string
	TavilyEndpoint      string
	TavilySearchDepth   string // "basic" 或 "advanced"
	TavilyIncludeAnswer bool

	// Brave Search API
	EnableBrave   bool
	BraveAPIKey   string
	BraveEndpoint string

	// 占位 provider（公开端点尚未知）
	EnableKimiSearch bool
	EnableGlmSearch  bool

	// DisableDDG 关闭 DuckDuckGo 回退。适用于必须禁用外部搜索、
	// 或仅允许通过已配置 provider 进行搜索的环境。
	DisableDDG bool

	// HTTPClient 用于 provider 请求。nil 表示使用 http.DefaultClient。
	HTTPClient *http.Client

	// 发送给 provider 的 UserAgent。默认为 "multi-agent-platform/0.1.0"。
	UserAgent string

	// 单次 provider 调用的超时。默认 25s。
	Timeout time.Duration
}

// NewWebSearchTool 创建名为 "core/web_search" 的 web 搜索工具。
//
// 参数：
//   - query                (string, required)：搜索查询。
//   - num_results          (integer, optional)：结果数量（API/MCP provider
//     为 1-30，DuckDuckGo 为 1-20；默认 8）。
//   - livecrawl            (string,  optional)："fallback" 或 "preferred"
//     （默认 "fallback"）。仅 Exa 使用。
//   - search_type          (string,  optional)："auto"、"fast" 或 "deep"
//     （默认 "auto"）。仅 Exa 使用。
//   - context_max_chars    (integer, optional)：每条结果的最大上下文字符数
//     （默认 10000）。仅 Exa 使用。
//   - session_context_id   (string,  optional)：为未来使用预留的稳定 ID。
//
// Provider 选择：
//  1. 若设置了 WEBSEARCH_PROVIDER，则使用该 provider（前提是已配置）。
//  2. 否则按优先级顺序自动选择第一个已配置的 provider。
//  3. 若都未配置，则回退到 DuckDuckGo（除非被禁用）。
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

// webSearchExecutor 分派到所选的 provider，返回 provider 名称与搜索结果文本。
//
// 当未配置任何 API provider 时，executor 自动回退到无需 API key 的
// DuckDuckGo HTML/lite 搜索。若已配置的 provider 失败，则把 DuckDuckGo
// 作为最后手段使用（除非被禁用）。
func webSearchExecutor(cfg WebSearchConfig, input map[string]any) (any, error) {
	query := getString(input, "query", "")
	if query == "" {
		return nil, fmt.Errorf("query required")
	}

	// 用合理的默认值规整可选参数。
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
	_ = sessionID // 为未来稳定 provider 绑定预留；当前未使用

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	provider := selectWebSearchProvider(cfg)

	// 若都未配置且 DuckDuckGo 被禁用，则提前返回。
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
		// 未配置任何 API provider —— 直接走 DuckDuckGo。
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

	// 若所选 provider 失败，且回退未被禁用、该 provider 是真实 API 时，
	// 尝试用 DuckDuckGo 作为稳健的回退。
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

// selectWebSearchProvider 以确定性方式挑选已配置的 provider。
// 优先级顺序：brave -> bing -> google -> tavily -> parallel -> exa。
// 当没有任何 provider 被配置时返回 ""，使调用方转而使用 DuckDuckGo。
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

// isPlaceholderProvider 报告哪些 provider 尚未有公开 API 端点。
func isPlaceholderProvider(provider string) bool {
	switch provider {
	case "kimi_search", "glm_search":
		return true
	}
	return false
}

// clampInt 将 v 限制在 [min, max] 区间内。
func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// searchResult 是与 provider 无关的搜索结果，供统一格式化器使用。
type searchResult struct {
	Title string
	URL   string
	Desc  string
}

// formatSearchResults 将解析后的结果转换为适合放入 LLM 上下文的简洁文本摘要。
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

// exaSearchArgs 与 Exa 的 web_search_exa 工具参数对应。
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

// parallelSearchArgs 与 Parallel 的 web_search 工具参数对应。
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

// bingResponse 是 Bing Web Search API v7 响应中我们需要的子集。
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

// googleResponse 是 Google Custom Search JSON API 响应的子集。
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

// tavilyResponse 是 Tavily 搜索响应中我们需要的子集。
type tavilyResponse struct {
	Answer  string `json:"answer"`
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// tavilyRequest 是发送给 Tavily 的 JSON body。
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

// braveResponse 是 Brave Web Search API 响应中我们需要的子集。
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
// 共享 HTTP 辅助函数
// ---------------------------------------------------------------------------

// doHTTPRead 使用 cfg.HTTPClient（或 http.DefaultClient）执行 req，并返回
// 最多 1 MB 的响应体。对非 2xx 状态码返回错误。请求的 context 必须由
// 调用方事先设置。
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
// 共享的 MCP-over-HTTP 辅助函数
// ---------------------------------------------------------------------------

// mcpRequest 是 JSON-RPC 2.0 tools/call 请求体。
type mcpRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// mcpResponse 是 tools/call 返回的响应结构，content 为带类型的条目数组。
// 我们提取其中第一个 text 条目。
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

// callMCPHTTP 通过普通 HTTP（传输层 MCP）向远端 MCP 端点发送
// JSON-RPC tools/call POST 请求。它接受可选的额外 header，并支持解析
// 直接 JSON 与 SSE 风格的 "data: ..." 帧。
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
		// 尽力读取错误响应体以便诊断。
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

// parseMCPResponse 从普通 JSON-RPC 响应或带 "data: ..." 帧的 SSE 流中
// 提取第一个 text 内容。
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

// urlEncode 是为 Exa 的 query 值做百分号编码的小辅助函数。
// DuckDuckGo 直接使用 net/url。
func urlEncode(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_.~-]`)
	return re.ReplaceAllStringFunc(s, func(c string) string {
		return fmt.Sprintf("%%%02X", c[0])
	})
}

// ---------------------------------------------------------------------------
// DuckDuckGo 回退 provider
// ---------------------------------------------------------------------------

// duckDuckGoResult 是 DuckDuckGo 自然结果的内部结构。
// 与 searchResult 分开保留，因为解析器提取的是原始 HTML 文本。
type duckDuckGoResult struct {
	Title       string
	URL         string
	Description string
}

// callDuckDuckGo 通过 lite/html 端点搜索 DuckDuckGo，返回前几条结果的
// 纯文本摘要。无需 API key，作为 core/web_search 的零配置回退。
//
// 它会先尝试对 JavaScript 友好的 HTML 端点；若无可解析结果，再回退到
// lite 端点。
func callDuckDuckGo(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	if numResults <= 0 {
		numResults = 8
	}
	if numResults > 30 {
		numResults = 30
	}

	// 先尝试主 HTML 搜索端点。
	text, err := callDuckDuckGoHTML(ctx, cfg, query, numResults)
	if err == nil && text != "" {
		return text, nil
	}

	// 回退到 lite 端点，它更简单，且通常受保护更少。
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

// duckResultTitleRe 匹配 DuckDuckGo HTML 中的结果标题。
var duckResultTitleRe = regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>`)

// duckResultSnippetRe 匹配 DuckDuckGo HTML 中的结果摘要。
var duckResultSnippetRe = regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>([\s\S]*?)</a>`)

func parseDuckDuckGoHTML(body string, limit int) []duckDuckGoResult {
	return parseDuckDuckGoBody(body, duckResultTitleRe, duckResultSnippetRe, limit)
}

// liteTitleRe 匹配 DuckDuckGo lite 中的结果标题。
var liteTitleRe = regexp.MustCompile(`<a[^>]*href="([^"]*)"[^>]*class="[^"]*result-link[^"]*"[^>]*>([\s\S]*?)</a>`)

// liteSnippetRe 匹配 DuckDuckGo lite 中的摘要。
var liteSnippetRe = regexp.MustCompile(`<td[^>]*class="result-snippet"[^>]*>([\s\S]*?)</td>`)

func parseDuckDuckGoLiteHTML(body string, limit int) []duckDuckGoResult {
	return parseDuckDuckGoBody(body, liteTitleRe, liteSnippetRe, limit)
}

// parseDuckDuckGoBody 使用所提供的标题与摘要正则，从 DuckDuckGo HTML 中
// 提取自然结果。它剥离标签、解码实体，并最多返回 limit 条结果。
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

// stripTags 移除 HTML 标签。
func stripTags(s string) string {
	return duckTagRe.ReplaceAllString(s, " ")
}

// htmlUnescape 执行最小集合的 HTML 实体替换。
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

// formatDuckDuckGoResults 将解析后的 DuckDuckGo 结果转换为文本。
func formatDuckDuckGoResults(results []duckDuckGoResult) string {
	out := make([]searchResult, 0, len(results))
	for _, r := range results {
		out = append(out, searchResult{Title: r.Title, URL: r.URL, Desc: r.Description})
	}
	return formatSearchResults(out)
}
