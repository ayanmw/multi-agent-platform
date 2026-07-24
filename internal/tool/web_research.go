package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// NewWebResearchTool 创建名为 "core/web_research" 的深度研究工具。
//
// 参数：
//   - query          (string, required)：研究问题。
//   - num_results    (integer, optional)：搜索结果数量（默认 8）。
//   - fetch_top      (integer, optional)：最多抓取几条结果正文参与摘要（默认 3）。
//   - intent         (string, optional)：希望侧重的角度。
//
// 执行流程：
//  1. 调用 web_search 获取搜索结果；
//  2. 抓取前 fetch_top 条结果的正文（失败则用 search snippet 兜底）；
//  3. 调用 LLM 生成 JSON 摘要；
//  4. 将内部 LLM usage 通过 _llm_usage 字段回传 engine。
func NewWebResearchTool(cfg WebSearchConfig) *BuiltinTool {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "multi-agent-platform/0.1.0"
	}
	return NewBuiltinTool(
		"web_research",
		"core",
		"Deep web research: search, fetch top pages, and return an LLM-synthesized summary with sources. Use when you need a compact answer rather than raw search results.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Research question",
				},
				"num_results": map[string]any{
					"type":        "integer",
					"description": "Number of search results to retrieve (default: 8)",
				},
				"fetch_top": map[string]any{
					"type":        "integer",
					"description": "Number of top results to fetch and summarize (default: 3)",
				},
				"intent": map[string]any{
					"type":        "string",
					"description": "Optional angle or aspect to emphasize",
				},
			},
			"required": []string{"query"},
		},
		func(ctx ExecuteContext, input map[string]any) (any, error) {
			return webResearchExecutor(cfg, ctx, input)
		},
	).WithTags("network", "websearch", "llm")
}

// webResearchResult 是 web_research 返回的固定结构。
type webResearchResult struct {
	Summary   string              `json:"summary"`
	Sources   []webResearchSource `json:"sources"`
	Provider  string              `json:"provider"`
	Query     string              `json:"query"`
	LLMUsage  llmUsageForTool     `json:"_llm_usage"`
	FetchHits int                 `json:"fetch_hits"`
}

type webResearchSource struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

func webResearchExecutor(cfg WebSearchConfig, ctx ExecuteContext, input map[string]any) (any, error) {
	query := getString(input, "query", "")
	if query == "" {
		return nil, fmt.Errorf("query required")
	}
	numResults := clampInt(getInt(input, "num_results", 8), 1, 30)
	fetchTop := clampInt(getInt(input, "fetch_top", 3), 1, 10)
	intent := getString(input, "intent", "")

	searchCtx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	// 1. 搜索：先用配置 provider，失败走国内链回退。
	provider := selectWebSearchProvider(cfg)
	if provider == "" {
		provider = "baidu"
	}
	searchText, err := performWebSearch(searchCtx, cfg, query, numResults, provider)
	if err != nil {
		// 再次尝试国内链完全兜底。
		searchText, err = fallbackChinaProvider(searchCtx, cfg, query, numResults, provider)
		if err != nil {
			return nil, fmt.Errorf("web_research search failed: %w", err)
		}
		provider = "baidu"
	}

	// 2. 解析搜索结果，提取 title/url/desc。
	results := extractSearchResults(searchText)
	if len(results) == 0 {
		return webResearchResult{
			Summary:  "No relevant search results were found for the query.",
			Sources:  nil,
			Provider: provider,
			Query:    query,
		}, nil
	}
	if fetchTop > len(results) {
		fetchTop = len(results)
	}

	// 3. 串行抓取页面正文，降低反爬风险；失败保留 snippet。
	sourcesWithExtract := make([]searchResultWithExtract, 0, fetchTop)
	fetchHits := 0
	for i := 0; i < fetchTop && i < len(results); i++ {
		r := results[i]
		src := searchResultWithExtract{Title: r.Title, URL: r.URL, Desc: r.Desc}
		if extract := fetchPageText(searchCtx, cfg, r.URL, 256*1024); extract != "" {
			src.Extract = extract
			fetchHits++
		}
		sourcesWithExtract = append(sourcesWithExtract, src)
	}
	for i := fetchTop; i < len(results); i++ {
		r := results[i]
		sourcesWithExtract = append(sourcesWithExtract, searchResultWithExtract{Title: r.Title, URL: r.URL, Desc: r.Desc})
	}

	// 4. 调用 LLM 生成摘要。
	var summary string
	var sources []webResearchSource
	var usage llmUsageForTool
	if ctx.LLMProvider != nil {
		summary, sources, usage = summarizeWithLLM(ctx, query, intent, sourcesWithExtract)
	} else {
		summary = fallbackMarkdownSummary(sourcesWithExtract)
		sources = collectSources(sourcesWithExtract)
	}

	return webResearchResult{
		Summary:   summary,
		Sources:   sources,
		Provider:  provider,
		Query:     query,
		LLMUsage:  usage,
		FetchHits: fetchHits,
	}, nil
}

// performWebSearch 根据 provider 调用对应搜索函数。
func performWebSearch(ctx context.Context, cfg WebSearchConfig, query string, numResults int, provider string) (string, error) {
	switch provider {
	case "parallel":
		return callParallel(ctx, cfg, query)
	case "exa":
		return callExa(ctx, cfg, exaSearchArgs{Query: query, Type: "auto", NumResults: numResults, Livecrawl: "fallback", ContextMaxCharacters: 10000})
	case "bing":
		return callBing(ctx, cfg, query, numResults)
	case "google":
		return callGoogle(ctx, cfg, query, numResults)
	case "tavily":
		return callTavily(ctx, cfg, query, numResults)
	case "brave":
		return callBrave(ctx, cfg, query, numResults)
	case "baidu":
		return callBaiduMobile(ctx, cfg, query, numResults)
	case "sogou":
		return callSogou(ctx, cfg, query, numResults)
	case "bing_cn_html":
		return callBingCnHTML(ctx, cfg, query, numResults)
	case "duckduckgo":
		return callDuckDuckGo(ctx, cfg, query, numResults)
	default:
		return "", fmt.Errorf("unknown provider: %s", provider)
	}
}

// extractSearchResults 从 formatSearchResults 生成的文本中反向解析出结果列表。
// 这是 web_research 与 web_search 共享数据结构的最简做法；如果未来 web_search
// 返回结构化数据可直接复用。
func extractSearchResults(text string) []searchResult {
	var results []searchResult
	lines := strings.Split(text, "\n")
	var current *searchResult
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 匹配 "N. Title"
		if strings.Contains(line, ". ") {
			parts := strings.SplitN(line, ". ", 2)
			if current != nil {
				results = append(results, *current)
			}
			current = &searchResult{Title: parts[1]}
			continue
		}
		if current != nil {
			if strings.HasPrefix(line, "URL: ") {
				current.URL = strings.TrimPrefix(line, "URL: ")
			} else if strings.HasPrefix(line, "Snippet: ") {
				current.Desc = strings.TrimPrefix(line, "Snippet: ")
			} else {
				current.Desc += " " + line
			}
		}
	}
	if current != nil {
		results = append(results, *current)
	}
	return results
}

// summarizeWithLLM 使用 ctx.LLMProvider 做一次性非流式总结。
func summarizeWithLLM(ctx ExecuteContext, query, intent string, sources []searchResultWithExtract) (string, []webResearchSource, llmUsageForTool) {
	if ctx.EventBus != nil {
		ctx.EventBus.SendEvent(event.NewEventWithSubTask(
			event.EventWebResearchSummarizeStarted,
			ctx.TaskID,
			"",
			ctx.AgentID,
			ctx.StepIdx,
			map[string]any{
				"query":       query,
				"num_sources": len(sources),
			},
		))
	}

	userPrompt := WebResearchSummarizeUserPrompt(query, intent, sources)
	req := map[string]any{
		"model": "deepseek-v4-flash",
		"messages": []map[string]any{
			{"role": "system", "content": WebResearchSummarizePrompt.Content},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.3,
	}

	raw, err := ctx.LLMProvider.Chat(req)
	if err != nil {
		return fallbackMarkdownSummary(sources), collectSources(sources), llmUsageForTool{}
	}

	content := chatContent(raw)
	usage := chatUsage(raw)

	summary, parsedSources := parseResearchJSON(content)
	if len(parsedSources) == 0 {
		parsedSources = collectSources(sources)
	}

	if ctx.EventBus != nil {
		ctx.EventBus.SendEvent(event.NewEventWithSubTask(
			event.EventWebResearchSummarizeCompleted,
			ctx.TaskID,
			"",
			ctx.AgentID,
			ctx.StepIdx,
			map[string]any{
				"query":        query,
				"num_sources":  len(parsedSources),
				"prompt_tokens": usage.PromptTokens,
				"completion_tokens": usage.CompletionTokens,
				"total_tokens": usage.TotalTokens,
			},
		))
	}

	return summary, parsedSources, usage
}

// parseResearchJSON 尝试解析 JSON 摘要；失败则回退 markdown。
func parseResearchJSON(content string) (string, []webResearchSource) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```json") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	} else if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}

	var parsed struct {
		Summary string              `json:"summary"`
		Sources []webResearchSource `json:"sources"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return fallbackMarkdown([]webResearchSource{}, content), nil
	}
	if parsed.Summary == "" {
		return fallbackMarkdown(parsed.Sources, content), parsed.Sources
	}
	return parsed.Summary, parsed.Sources
}

// fallbackMarkdownSummary 在未调 LLM 或 LLM 失败时，把 sources 拼接成 markdown。
func fallbackMarkdownSummary(sources []searchResultWithExtract) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Research Summary\n\nBased on %d search results:\n\n", len(sources))
	for i, s := range sources {
		snippet := s.Desc
		if s.Extract != "" {
			snippet = truncate(s.Extract, 600)
		}
		fmt.Fprintf(&b, "%d. **%s**\n   <%s>\n   %s\n\n", i+1, s.Title, s.URL, snippet)
	}
	return strings.TrimSpace(b.String())
}

// fallbackMarkdown 在 LLM 返回无法解析但非空的内容时，原样返回并附加 sources。
func fallbackMarkdown(sources []webResearchSource, content string) string {
	if content != "" {
		return content
	}
	var b strings.Builder
	b.WriteString("## Sources\n")
	for _, s := range sources {
		fmt.Fprintf(&b, "- [%s](%s)\n", s.Title, s.URL)
	}
	return strings.TrimSpace(b.String())
}

// collectSources 从搜索结果中收集 sources 列表。
func collectSources(sources []searchResultWithExtract) []webResearchSource {
	seen := make(map[string]bool)
	out := make([]webResearchSource, 0, len(sources))
	for _, s := range sources {
		if s.URL == "" || seen[s.URL] {
			continue
		}
		seen[s.URL] = true
		out = append(out, webResearchSource{Title: s.Title, URL: s.URL})
	}
	return out
}
