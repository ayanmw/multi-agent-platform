package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIEmbeddingProvider 为 OpenAI-compatible 文本 embedding endpoint 实现 EmbeddingProvider。
type OpenAIEmbeddingProvider struct {
	endpoint   string
	apiKey     string
	model      string
	dimensions int
	http       *http.Client
}

func NewOpenAIEmbeddingProvider(endpoint, apiKey, model string, dimensions int) *OpenAIEmbeddingProvider {
	return &OpenAIEmbeddingProvider{
		endpoint:   strings.TrimRight(endpoint, "/"),
		apiKey:     apiKey,
		model:      model,
		dimensions: dimensions,
		http:       &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *OpenAIEmbeddingProvider) Dimensions() int { return p.dimensions }
func (p *OpenAIEmbeddingProvider) Name() string     { return "openai-" + p.model }

func (p *OpenAIEmbeddingProvider) Embed(text string) ([]float32, error) {
	vecs, err := p.EmbedBatch([]string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || vecs[0] == nil {
		return nil, fmt.Errorf("OpenAIEmbeddingProvider: empty embedding response")
	}
	return vecs[0], nil
}

func (p *OpenAIEmbeddingProvider) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("OpenAIEmbeddingProvider: empty input batch")
	}
	for i, t := range texts {
		if t == "" {
			return nil, fmt.Errorf("OpenAIEmbeddingProvider: empty text at index %d", i)
		}
	}
	body, _ := json.Marshal(map[string]any{
		"model": p.model,
		"input": texts,
	})
	req, err := http.NewRequest("POST", p.endpoint+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("OpenAIEmbeddingProvider: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenAIEmbeddingProvider: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAIEmbeddingProvider: API error %d: %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("OpenAIEmbeddingProvider: decode: %w", err)
	}

	out := make([][]float32, len(texts))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("OpenAIEmbeddingProvider: invalid index %d", d.Index)
		}
		if p.dimensions > 0 && len(d.Embedding) != p.dimensions {
			return nil, fmt.Errorf("OpenAIEmbeddingProvider: dimension mismatch: got %d, want %d", len(d.Embedding), p.dimensions)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("OpenAIEmbeddingProvider: missing embedding for index %d", i)
		}
	}
	return out, nil
}
