package tool

import (
	"fmt"
	"strings"
)

// NamedPrompt 是一个带唯一 name 的 prompt 模板。
// name 用于索引、日志、测试以及未来 db 化 prompt 时的主键。
type NamedPrompt struct {
	Name    string
	Content string
}

// WebResearchSummarizePrompt 是 core/web_research 内部 LLM 摘要的系统 prompt。
// 它要求模型基于搜索结果和页面正文生成简洁摘要，并输出 JSON 或 markdown。
var WebResearchSummarizePrompt = NamedPrompt{
	Name: "web-research-summarize-system",
	Content: `You are a research assistant. Your job is to synthesize search results into a concise, accurate summary in the same language as the user's query.

You will be given:
- The user's query and optional intent (what aspect to focus on).
- A list of search results. Each result has a title, URL, snippet, and optionally extracted plain text from the page.

Instructions:
1. Read all provided results carefully.
2. Produce a final answer that directly addresses the query, and if an intent is provided, emphasize that angle.
3. When stating facts, prefer information from the provided extracts. Do not hallucinate.
4. Preserve the original source URLs so the user can verify. When referencing a source inline, use Markdown link format: [title](url) or [1](url).
5. If you cannot find relevant information, say so explicitly.
6. Output format: respond with valid JSON only, no markdown code fences, no extra commentary.

Required JSON schema:
{
  "summary": "Your concise markdown summary here. Include inline source links where appropriate.",
  "sources": [
    {"title": "Source Title", "url": "https://example.com/page"}
  ]
}

The "summary" field should be one to three paragraphs. The "sources" array must contain every URL you used, deduplicated, with a readable title.

If you are absolutely unable to produce valid JSON, fall back to plain markdown with a "Sources:" section containing bullet links, but JSON is strongly preferred.`,
}

// WebResearchSummarizeUserPrompt 根据查询与结果构造 user prompt。
// 返回的是可直接送入 LLM 的字符串，包含 intent、query 和 sources。
func WebResearchSummarizeUserPrompt(query, intent string, sources []searchResultWithExtract) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Query: %s\n", query)
	if intent != "" {
		fmt.Fprintf(&b, "Intent: %s\n", intent)
	}
	b.WriteString("\nSearch results:\n")
	for i, s := range sources {
		fmt.Fprintf(&b, "\n[%d] %s\nURL: %s\n", i+1, s.Title, s.URL)
		if s.Desc != "" {
			fmt.Fprintf(&b, "Snippet: %s\n", s.Desc)
		}
		if s.Extract != "" {
			fmt.Fprintf(&b, "Extract:\n%s\n", truncate(s.Extract, 4000))
		}
	}
	return b.String()
}

// searchResultWithExtract 在搜索结果基础上附加页面正文提取。
type searchResultWithExtract struct {
	Title   string
	URL     string
	Desc    string
	Extract string
}

// truncate 截断字符串至 max 字符，避免摘要 prompt 过长。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
