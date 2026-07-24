package tool

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// ---------------------------------------------------------------------------
// Baidu mobile provider
// ---------------------------------------------------------------------------

// baiduResult 是 Baidu 移动搜索结果内部结构。
type baiduResult struct {
	Title string
	URL   string
	Desc  string
}

// callBaiduMobile 抓取 m.baidu.com 搜索结果，返回文本摘要。
func callBaiduMobile(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	u := "https://m.baidu.com/s?word=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	raw, err := doHTTPRead(ctx, cfg, req)
	if err != nil {
		return "", err
	}
	results := parseBaiduMobileHTML(string(raw), numResults)
	if len(results) == 0 {
		return "", fmt.Errorf("no baidu results")
	}
	return formatBaiduResults(results), nil
}

var (
	// 百度结果块：找到 class 包含 result 的 div。
	baiduBlockRe    = regexp.MustCompile(`<div[^>]*class="result"[^>]*>[\s\S]*?</div>`)
	baiduTitleSubRe = regexp.MustCompile(`<a[^>]*class="(?:c-font-medium|c-color-text|title)"[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>`)
	baiduSnippetSubRe = regexp.MustCompile(`<span[^>]*class="(?:content-right_8Zs40|content[^"]*|c-line-clamp[^"]*)"[^>]*>([\s\S]*?)</span>`)
	// 备用：较新的百度移动 HTML 往往用更简单的结构。
	baiduSimpleRe = regexp.MustCompile(`<a[^>]*class="([^"]*c-font-medium[^"]*)"[^>]*?data-click[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>`)
)

func parseBaiduMobileHTML(body string, limit int) []baiduResult {
	var results []baiduResult
	// 先尝试通用块解析。
	if m := baiduBlockRe.FindAllStringSubmatch(body, -1); len(m) > 0 {
		for i, sm := range m {
			if i >= limit {
				break
			}
			block := sm[0]
			tm := baiduTitleSubRe.FindStringSubmatch(block)
			if len(tm) < 3 {
				continue
			}
			link := stripWhitespace(baiduResolveURL(tm[1]))
			if strings.HasPrefix(link, "/") {
				link = "https://m.baidu.com" + link
			}
			smSnippet := baiduSnippetSubRe.FindStringSubmatch(block)
			desc := ""
			if len(smSnippet) >= 2 {
				desc = stripTagsAndUnescape(smSnippet[1])
			}
			results = append(results, baiduResult{
				Title: stripTagsAndUnescape(tm[2]),
				URL:   link,
				Desc:  desc,
			})
		}
	}
	// 若未解析到再通过简单模式兜底。
	if len(results) == 0 {
		if m := baiduSimpleRe.FindAllStringSubmatch(body, -1); len(m) > 0 {
			for i, sm := range m {
				if i >= limit {
					break
				}
				link := stripWhitespace(baiduResolveURL(sm[2]))
				if strings.HasPrefix(link, "/") {
					link = "https://m.baidu.com" + link
				}
				results = append(results, baiduResult{
					Title: stripTagsAndUnescape(sm[3]),
					URL:   link,
					Desc:  "",
				})
			}
		}
	}
	return results
}

// baiduResolveURL 处理百度跳转链接：若 href 是 /from=... 跳转，则尝试提取
// 真实 URL；否则直接返回。
func baiduResolveURL(href string) string {
	if u, err := url.Parse(href); err == nil && u.Scheme == "http" || u.Scheme == "https" {
		return u.String()
	}
	return href
}

func formatBaiduResults(results []baiduResult) string {
	out := make([]searchResult, 0, len(results))
	for _, r := range results {
		out = append(out, searchResult{Title: r.Title, URL: r.URL, Desc: r.Desc})
	}
	return formatSearchResults(out)
}

// ---------------------------------------------------------------------------
// Sogou provider
// ---------------------------------------------------------------------------

// sogouResult 是搜狗搜索结果内部结构。
type sogouResult struct {
	Title string
	URL   string
	Desc  string
}

func callSogou(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	u := "https://www.sogou.com/web?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	raw, err := doHTTPRead(ctx, cfg, req)
	if err != nil {
		return "", err
	}
	results := parseSogouHTML(string(raw), numResults)
	if len(results) == 0 {
		return "", fmt.Errorf("no sogou results")
	}
	return formatSogouResults(results), nil
}

var (
	sogouResultRe = regexp.MustCompile(`<div[^>]*class="vrwrap"[^>]*>.*?<h3[^>]*class="vr-title"[^>]*>.*?<a[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>.*?</h3>.*?<div[^>]*class="(?:str-text-info|str_info)"[^>]*>([\s\S]*?)</div>.*?</div>`)
	sogouSimpleRe = regexp.MustCompile(`<h3[^>]*class="vr-title"[^>]*>.*?<a[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>.*?</h3>`)
)

func parseSogouHTML(body string, limit int) []sogouResult {
	var results []sogouResult
	if m := sogouResultRe.FindAllStringSubmatch(body, -1); len(m) > 0 {
		for i, sm := range m {
			if i >= limit {
				break
			}
			results = append(results, sogouResult{
				Title: stripTagsAndUnescape(sm[2]),
				URL:   sogouResolveURL(sm[1]),
				Desc:  stripTagsAndUnescape(sm[3]),
			})
		}
	}
	if len(results) == 0 {
		if m := sogouSimpleRe.FindAllStringSubmatch(body, -1); len(m) > 0 {
			for i, sm := range m {
				if i >= limit {
					break
				}
				results = append(results, sogouResult{
					Title: stripTagsAndUnescape(sm[2]),
					URL:   sogouResolveURL(sm[1]),
					Desc:  "",
				})
			}
		}
	}
	return results
}

func sogouResolveURL(href string) string {
	if strings.HasPrefix(href, "/") {
		return "https://www.sogou.com" + href
	}
	return href
}

func formatSogouResults(results []sogouResult) string {
	out := make([]searchResult, 0, len(results))
	for _, r := range results {
		out = append(out, searchResult{Title: r.Title, URL: r.URL, Desc: r.Desc})
	}
	return formatSearchResults(out)
}

// ---------------------------------------------------------------------------
// Bing China HTML provider
// ---------------------------------------------------------------------------

// bingCnResult 是必应中国 HTML 搜索结果内部结构。
type bingCnResult struct {
	Title string
	URL   string
	Desc  string
}

func callBingCnHTML(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	u := "https://cn.bing.com/search?ensearch=0&q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	raw, err := doHTTPRead(ctx, cfg, req)
	if err != nil {
		return "", err
	}
	results := parseBingCnHTML(string(raw), numResults)
	if len(results) == 0 {
		return "", fmt.Errorf("no bing cn results")
	}
	return formatBingCnResults(results), nil
}

var (
	bingCnResultRe = regexp.MustCompile(`<li[^>]*class="b_algo"[^>]*>[\s\S]*?<h2[^>]*>[\s\S]*?<a[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>[\s\S]*?</h2>[\s\S]*?<div[^>]*class="b_caption"[^>]*>[\s\S]*?<p>([\s\S]*?)</p>[\s\S]*?</div>[\s\S]*?</li>`)
	bingCnSimpleRe = regexp.MustCompile(`<li[^>]*class="b_algo"[^>]*>[\s\S]*?<a[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>[\s\S]*?</li>`)
)

func parseBingCnHTML(body string, limit int) []bingCnResult {
	var results []bingCnResult
	if m := bingCnResultRe.FindAllStringSubmatch(body, -1); len(m) > 0 {
		for i, sm := range m {
			if i >= limit {
				break
			}
			results = append(results, bingCnResult{
				Title: stripTagsAndUnescape(sm[2]),
				URL:   sm[1],
				Desc:  stripTagsAndUnescape(sm[3]),
			})
		}
	}
	if len(results) == 0 {
		if m := bingCnSimpleRe.FindAllStringSubmatch(body, -1); len(m) > 0 {
			for i, sm := range m {
				if i >= limit {
					break
				}
				results = append(results, bingCnResult{
					Title: stripTagsAndUnescape(sm[2]),
					URL:   sm[1],
					Desc:  "",
				})
			}
		}
	}
	return results
}

func formatBingCnResults(results []bingCnResult) string {
	out := make([]searchResult, 0, len(results))
	for _, r := range results {
		out = append(out, searchResult{Title: r.Title, URL: r.URL, Desc: r.Desc})
	}
	return formatSearchResults(out)
}

// ---------------------------------------------------------------------------
// 通用 HTML 辅助
// ---------------------------------------------------------------------------

// stripTagsAndUnescape 移除 HTML 标签并解码常见 HTML 实体。
func stripTagsAndUnescape(s string) string {
	s = stripTags(strings.TrimSpace(s))
	s = htmlUnescape(s)
	return stripWhitespace(s)
}

// stripWhitespace 折叠连续空白并去除首尾空白。
func stripWhitespace(s string) string {
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(s, " "))
}

// fetchPageText 用 GET 抓取页面并转为纯文本，失败时返回空字符串。
func fetchPageText(ctx context.Context, cfg WebSearchConfig, pageURL string, maxBytes int64) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "text/html")
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	body, _, err := readLimited(resp.Body, maxBytes)
	if err != nil {
		return ""
	}
	return htmlToText(string(body))
}

// ---------------------------------------------------------------------------
// web_research 内部使用：LLM 调用辅助
// ---------------------------------------------------------------------------

// chatResponse 是 OpenAI-compatible 非流式 chat 响应的最小子集。
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// chatContent 从 LLMProvider.Chat 返回的 *response 中提取 content。
func chatContent(resp any) string {
	r, ok := resp.(*chatResponse)
	if !ok {
		return ""
	}
	if len(r.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(r.Choices[0].Message.Content)
}

// chatUsage 从 LLMProvider.Chat 返回的 *response 中提取 usage。
func chatUsage(resp any) llmUsageForTool {
	var u llmUsageForTool
	r, ok := resp.(*chatResponse)
	if !ok {
		return u
	}
	u.PromptTokens = r.Usage.PromptTokens
	u.CompletionTokens = r.Usage.CompletionTokens
	u.TotalTokens = r.Usage.TotalTokens
	return u
}

// llmUsageForTool 是 tool 内部 LLM 调用的 usage 结构，与 llm.Usage 字段相同。
type llmUsageForTool struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
