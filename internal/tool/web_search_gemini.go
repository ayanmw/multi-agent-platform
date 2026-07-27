package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ---------------------------------------------------------------------------
// Gemini provider (generateContent + google_search tool)
// ---------------------------------------------------------------------------

// geminiRequest 是 Gemini generateContent 的请求体。
// 通过 tools["google_search":{}] 启用 Google 搜索接地。
type geminiRequest struct {
	Contents []geminiContent `json:"contents"`
	Tools    []geminiTool    `json:"tools"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiTool struct {
	GoogleSearch struct{} `json:"google_search"`
}

// geminiResponse 是 Gemini generateContent 响应中我们需要的子集。
// Google Search grounding 元数据位于 groundingMetadata。
type geminiResponse struct {
	Candidates []struct {
		Content           geminiContent        `json:"content"`
		GroundingMetadata *geminiGroundingMeta `json:"groundingMetadata"`
	} `json:"candidates"`
}

// geminiGroundingMeta 包含 grounding 来源与引用。
type geminiGroundingMeta struct {
	WebSearchQueries  []string              `json:"webSearchQueries"`
	GroundingChunks   []geminiGroundingChunk `json:"groundingChunks"`
	GroundingSupports []struct {
		Segment *struct {
			Text string `json:"text"`
		} `json:"segment"`
		GroundingChunkIndices []int `json:"groundingChunkIndices"`
	} `json:"groundingSupports"`
}

// geminiGroundingChunk 描述 grounding 来源（目前是 web）。
type geminiGroundingChunk struct {
	Web *struct {
		URI   string `json:"uri"`
		Title string `json:"title"`
	} `json:"web"`
}

// callGemini 调用 Gemini generateContent REST 端点，启用 google_search 工具，
// 并解析 grounding 元数据为统一搜索结果格式。
func callGemini(ctx context.Context, cfg WebSearchConfig, query string, numResults int) (string, error) {
	if cfg.GeminiAPIKey == "" {
		return "", fmt.Errorf("gemini api key required")
	}

	endpoint := cfg.GeminiEndpoint
	model := cfg.GeminiModel
	if model == "" {
		model = "gemini-2.0-flash"
	}
	if !strings.HasPrefix(model, "models/") {
		model = "models/" + model
	}
	u := fmt.Sprintf("%s/%s:generateContent?key=%s", strings.TrimRight(endpoint, "/"), model, cfg.GeminiAPIKey)

	reqBody, err := json.Marshal(geminiRequest{
		Contents: []geminiContent{{
			Role:  "user",
			Parts: []geminiPart{{Text: query}},
		}},
		Tools: []geminiTool{{GoogleSearch: struct{}{}}},
	})
	if err != nil {
		return "", fmt.Errorf("marshal gemini request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	raw, err := doHTTPRead(ctx, cfg, req)
	if err != nil {
		return "", err
	}

	var resp geminiResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("parse gemini response: %w", err)
	}

	if len(resp.Candidates) == 0 {
		return "", fmt.Errorf("no candidates in gemini response")
	}

	cand := resp.Candidates[0]
	plainText := ""
	if len(cand.Content.Parts) > 0 {
		plainText = strings.TrimSpace(cand.Content.Parts[0].Text)
	}

	if cand.GroundingMetadata == nil || len(cand.GroundingMetadata.GroundingChunks) == 0 {
		if plainText == "" {
			return "", fmt.Errorf("empty gemini response")
		}
		return plainText, nil
	}

	results := make([]searchResult, 0, len(cand.GroundingMetadata.GroundingChunks))
	for _, chunk := range cand.GroundingMetadata.GroundingChunks {
		if chunk.Web == nil {
			continue
		}
		results = append(results, searchResult{
			Title: chunk.Web.Title,
			URL:   chunk.Web.URI,
			Desc:  "",
		})
	}

	// 用 groundingSupports 中的 segment text 作为对应 chunk 的 snippet。
	for _, support := range cand.GroundingMetadata.GroundingSupports {
		if support.Segment == nil || support.Segment.Text == "" {
			continue
		}
		for _, idx := range support.GroundingChunkIndices {
			if idx >= 0 && idx < len(results) {
				if results[idx].Desc == "" {
					results[idx].Desc = support.Segment.Text
				} else {
					results[idx].Desc += " " + support.Segment.Text
				}
			}
		}
	}

	// 过滤空条目。
	cleaned := make([]searchResult, 0, len(results))
	for _, r := range results {
		if r.Title == "" && r.URL == "" {
			continue
		}
		cleaned = append(cleaned, r)
	}
	if len(cleaned) == 0 {
		if plainText == "" {
			return "", fmt.Errorf("no search results in gemini response")
		}
		return plainText, nil
	}

	if numResults > 0 && len(cleaned) > numResults {
		cleaned = cleaned[:numResults]
	}

	var b strings.Builder
	if plainText != "" {
		_, _ = fmt.Fprintf(&b, "Answer: %s\n\n", plainText)
	}
	_, _ = b.WriteString(formatSearchResults(cleaned))
	return strings.TrimSpace(b.String()), nil
}
