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

// CohereEmbeddingProvider implements EmbeddingProvider for Cohere /v1/embed.
type CohereEmbeddingProvider struct {
	endpoint   string
	apiKey     string
	model      string
	dimensions int
	http       *http.Client
}

func NewCohereEmbeddingProvider(endpoint, apiKey, model string, dimensions int) *CohereEmbeddingProvider {
	return &CohereEmbeddingProvider{
		endpoint:   strings.TrimRight(endpoint, "/"),
		apiKey:     apiKey,
		model:      model,
		dimensions: dimensions,
		http:       &http.Client{Timeout: 60 * time.Second},
	}
}

func (p *CohereEmbeddingProvider) Dimensions() int { return p.dimensions }
func (p *CohereEmbeddingProvider) Name() string     { return "cohere-" + p.model }

func (p *CohereEmbeddingProvider) Embed(text string) ([]float32, error) {
	vecs, err := p.EmbedBatch([]string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || vecs[0] == nil {
		return nil, fmt.Errorf("CohereEmbeddingProvider: empty embedding response")
	}
	return vecs[0], nil
}

func (p *CohereEmbeddingProvider) EmbedBatch(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("CohereEmbeddingProvider: empty input batch")
	}
	body, _ := json.Marshal(map[string]any{
		"texts":      texts,
		"model":      p.model,
		"input_type": "search_document",
	})
	req, err := http.NewRequest("POST", p.endpoint+"/v1/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("CohereEmbeddingProvider: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("CohereEmbeddingProvider: http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CohereEmbeddingProvider: API error %d: %s", resp.StatusCode, string(b))
	}

	var parsed struct {
		Embeddings []json.RawMessage `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("CohereEmbeddingProvider: decode: %w", err)
	}
	if len(parsed.Embeddings) != len(texts) {
		return nil, fmt.Errorf("CohereEmbeddingProvider: embedding count %d != input count %d", len(parsed.Embeddings), len(texts))
	}
	out := make([][]float32, len(texts))
	for i, raw := range parsed.Embeddings {
		var vec []float32
		if err := json.Unmarshal(raw, &vec); err != nil {
			var mat [][]float32
			if err2 := json.Unmarshal(raw, &mat); err2 != nil || len(mat) == 0 {
				return nil, fmt.Errorf("CohereEmbeddingProvider: unmarshal embedding %d: %w", i, err)
			}
			vec = mat[0]
		}
		if p.dimensions > 0 && len(vec) != p.dimensions {
			return nil, fmt.Errorf("CohereEmbeddingProvider: dimension mismatch at %d", i)
		}
		out[i] = vec
	}
	return out, nil
}
