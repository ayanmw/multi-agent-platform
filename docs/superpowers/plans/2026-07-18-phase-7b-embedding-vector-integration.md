# Phase 7-B: 外部向量与 Embedding 集成 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不破坏现有 MemoryRecall / VectorStore 接口的前提下，接入远程 Embedding API（OpenAI text-embedding / Cohere），并实现增量索引、语义去重、混合检索完善，使 RAG 从本地 TF-IDF 原型升级到真实语义搜索。

**Architecture:** 保持 `internal/llm.EmbeddingProvider` 接口不变，新增 `OpenAIEmbeddingProvider` / `CohereEmbeddingProvider` 两个远程实现；`internal/config.Config` 增加 provider 与 key 配置；`cmd/server/main.go` 根据配置选择 provider。`memory.SqliteVectorStore` 已存在，只需在 memory 写入时增加行级 upsert hook（`PostInsertMemoryHook`），替换启动时的 `BuildVectorIndex` 全量重扫。新增 `HybridRanker` 将 keywordScore、向量 cosine、BM25 三组信号线性加权，替换 `harness.blendVectorScores` 中立即为每段内容调用 Embed 的低效实现。

**Tech Stack:** Go 1.25, standard library, modernc.org/sqlite, existing Provider / Memory / Harness layers.

---

## File Structure

| Path | Responsibility |
|------|----------------|
| `internal/llm/openai_embedding_provider.go` | New: `OpenAIEmbeddingProvider` implementing `EmbeddingProvider`. |
| `internal/llm/cohere_embedding_provider.go` | New: `CohereEmbeddingProvider` implementing `EmbeddingProvider`. |
| `internal/llm/embedding_provider_test.go` | New: tests for remote embedding providers (HTTP mock). |
| `internal/config/config.go` | Add embedding provider fields + env loading. |
| `internal/memory/indexer.go` | New: `MemoryIndexer` with incremental upsert hook and semantic dedup. |
| `internal/memory/indexer_test.go` | New: tests for upsert hook, dedup threshold. |
| `internal/harness/hybrid_ranker.go` | New: `HybridRanker` with keyword/vector/BM25 scoring. |
| `internal/harness/hybrid_ranker_test.go` | New: ranker unit tests. |
| `internal/harness/recall.go` | Replace `blendVectorScores` call path to use `HybridRanker`. |
| `cmd/server/main.go` | Wire provider selection and `MemoryIndexer` hook. |
| `pkg/db/memory.go` | Add `PostInsertMemoryHook` callback placeholder. |
| `.env.example` | Document new env vars. |

---

## Task 1: OpenAI Embedding Provider

**Files:**
- Create: `internal/llm/openai_embedding_provider.go`
- Test: `internal/llm/embedding_provider_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/llm/embedding_provider_test.go`:

```go
package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbeddingProviderEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		resp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"object":    "embedding",
					"index":     0,
					"embedding": []float32{0.1, 0.2, 0.3},
				},
			},
			"usage": map[string]int{"prompt_tokens": 4, "total_tokens": 4},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIEmbeddingProvider(server.URL, "key", "text-embedding-3-small", 3)
	vec, err := p.Embed("hello")
	if err != nil {
		t.Fatalf("embed error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("dim = %d, want 3", len(vec))
	}
	if p.Dimensions() != 3 {
		t.Fatalf("dimensions = %d, want 3", p.Dimensions())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm -run TestOpenAIEmbeddingProviderEmbed -v`

Expected: FAIL `undefined: NewOpenAIEmbeddingProvider`

- [ ] **Step 3: Implement the provider**

Create `internal/llm/openai_embedding_provider.go`:

```go
package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAIEmbeddingProvider implements EmbeddingProvider for OpenAI-compatible
// text embedding endpoints (e.g. OpenAI text-embedding-3, or Azure/jinaai proxies).
type OpenAIEmbeddingProvider struct {
	endpoint   string
	apiKey     string
	model      string
	dimensions int
	http       *http.Client
}

// NewOpenAIEmbeddingProvider creates a remote embedding provider.
// Set dimensions to 0 to infer from the first response (not recommended for
// production, but useful when the model dimension is unknown). Production
// configs should pass the known dimension so the provider validates vectors.
func NewOpenAIEmbeddingProvider(endpoint, apiKey, model string, dimensions int) *OpenAIEmbeddingProvider {
	return &OpenAIEmbeddingProvider{
		endpoint:   strings.TrimRight(endpoint, "/"),
		apiKey:     apiKey,
		model:      model,
		dimensions: dimensions,
		http:       &http.Client{Timeout: 60 * time.Second},
	}
}

// Dimensions returns the configured vector dimensionality.
func (p *OpenAIEmbeddingProvider) Dimensions() int { return p.dimensions }

// Name returns the provider identifier used for row model stamps.
func (p *OpenAIEmbeddingProvider) Name() string { return "openai-" + p.model }

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
		// Validate dimensions if configured.
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/llm -run TestOpenAIEmbeddingProviderEmbed -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llm/openai_embedding_provider.go internal/llm/embedding_provider_test.go
git commit -m "Phase 7-B: OpenAI-compatible remote embedding provider"
```

---

## Task 2: Cohere Embedding Provider

**Files:**
- Create: `internal/llm/cohere_embedding_provider.go`
- Test: `internal/llm/embedding_provider_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/llm/embedding_provider_test.go`:

```go
func TestCohereEmbeddingProviderEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embed" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := map[string]any{
			"id":     "id",
			"texts":  []string{"hello"},
			"embeddings": []any{[]float32{0.4, 0.5, 0.6}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewCohereEmbeddingProvider(server.URL, "key", "embed-english-v3.0", 3)
	vec, err := p.Embed("hello")
	if err != nil {
		t.Fatalf("embed error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("dim = %d, want 3", len(vec))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm -run TestCohereEmbeddingProviderEmbed -v`

Expected: FAIL `undefined: NewCohereEmbeddingProvider`

- [ ] **Step 3: Implement the provider**

Create `internal/llm/cohere_embedding_provider.go`:

```go
package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		"texts": texts,
		"model": p.model,
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
			// Cohere sometimes returns [][]float32 (input_type search_document)
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/llm -run 'TestOpenAIEmbeddingProviderEmbed|TestCohereEmbeddingProviderEmbed' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llm/cohere_embedding_provider.go internal/llm/embedding_provider_test.go
git commit -m "Phase 7-B: Cohere remote embedding provider"
```

---

## Task 3: Config and Provider Selection Wiring

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/server/main.go`
- Modify: `.env.example`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go` (create if not exists; otherwise add):

```go
func TestLoadEmbeddingConfig(t *testing.T) {
	t.Setenv("EMBEDDING_PROVIDER", "openai")
	t.Setenv("EMBEDDING_API_KEY", "key")
	t.Setenv("EMBEDDING_MODEL", "text-embedding-3-small")
	t.Setenv("EMBEDDING_DIMENSIONS", "1536")
	t.Setenv("EMBEDDING_ENDPOINT", "https://api.openai.com/v1")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if cfg.EmbeddingProvider != "openai" {
		t.Fatalf("provider = %q, want openai", cfg.EmbeddingProvider)
	}
	if cfg.EmbeddingDimensions != 1536 {
		t.Fatalf("dimensions = %d, want 1536", cfg.EmbeddingDimensions)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestLoadEmbeddingConfig -v`

Expected: FAIL `cfg.EmbeddingProvider undefined`

- [ ] **Step 3: Add config fields and loading**

Edit `internal/config/config.go` in the `Config` struct, after WebSearch fields:

```go
	// Embedding provider configuration. When provider is empty or "local", the
	// existing LocalEmbeddingProvider is used. When "openai" or "cohere", a
	// remote HTTP provider is constructed from the fields below.
	EmbeddingProvider   string // EMBEDDING_PROVIDER (local | openai | cohere)
	EmbeddingEndpoint   string // EMBEDDING_ENDPOINT
	EmbeddingAPIKey     string // EMBEDDING_API_KEY
	EmbeddingModel      string // EMBEDDING_MODEL
	EmbeddingDimensions int    // EMBEDDING_DIMENSIONS
```

In `Load()`, after web search loading block:

```go
	// Embedding provider configuration.
	if v := os.Getenv("EMBEDDING_PROVIDER"); v != "" {
		cfg.EmbeddingProvider = v
	}
	if v := os.Getenv("EMBEDDING_ENDPOINT"); v != "" {
		cfg.EmbeddingEndpoint = v
	}
	if v := os.Getenv("EMBEDDING_API_KEY"); v != "" {
		cfg.EmbeddingAPIKey = v
	}
	if v := os.Getenv("EMBEDDING_MODEL"); v != "" {
		cfg.EmbeddingModel = v
	}
	if v := os.Getenv("EMBEDDING_DIMENSIONS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil {
			cfg.EmbeddingDimensions = d
		}
	}
```

Add `strconv` to imports.

- [ ] **Step 4: Add provider factory helper**

Create a new function at the bottom of `internal/config/config.go`:

```go
// BuildEmbeddingProvider constructs an llm.EmbeddingProvider from the config.
// Returns nil when provider is empty or "local" (caller should use LocalEmbeddingProvider).
func (cfg *Config) BuildEmbeddingProvider() (llm.EmbeddingProvider, error) {
	switch strings.ToLower(cfg.EmbeddingProvider) {
	case "", "local":
		return nil, nil
	case "openai":
		endpoint := cfg.EmbeddingEndpoint
		if endpoint == "" {
			endpoint = "https://api.openai.com/v1"
		}
		model := cfg.EmbeddingModel
		if model == "" {
			model = "text-embedding-3-small"
		}
		return llm.NewOpenAIEmbeddingProvider(endpoint, cfg.EmbeddingAPIKey, model, cfg.EmbeddingDimensions), nil
	case "cohere":
		endpoint := cfg.EmbeddingEndpoint
		if endpoint == "" {
			endpoint = "https://api.cohere.com"
		}
		model := cfg.EmbeddingModel
		if model == "" {
			model = "embed-english-v3.0"
		}
		return llm.NewCohereEmbeddingProvider(endpoint, cfg.EmbeddingAPIKey, model, cfg.EmbeddingDimensions), nil
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %q", cfg.EmbeddingProvider)
	}
}
```

Add `strconv` to imports (already added). Add `llm` import to `internal/config/config.go` (note: this creates a dependency from config to llm; ensure no cycle — config only imports `llm` types, and llm does not import config, so safe).

- [ ] **Step 5: Run config test**

Run: `go test ./internal/config -run TestLoadEmbeddingConfig -v`

Expected: PASS

- [ ] **Step 6: Wire into server bootstrap**

Edit `cmd/server/main.go`, in the startup block where `embedProvider` is currently created.

Replace:

```go
embedProvider := llm.NewLocalEmbeddingProvider(2048)
```

with:

```go
var embedProvider llm.EmbeddingProvider
if configuredProvider, err := cfg.BuildEmbeddingProvider(); err != nil {
	observability.DefaultLogger.Error("embedding", "invalid embedding provider config, falling back to local", map[string]any{"error": err.Error()})
	embedProvider = llm.NewLocalEmbeddingProvider(2048)
} else if configuredProvider != nil {
	embedProvider = configuredProvider
	observability.DefaultLogger.Info("embedding", "using remote embedding provider", map[string]any{"provider": cfg.EmbeddingProvider, "model": cfg.EmbeddingModel})
} else {
	embedProvider = llm.NewLocalEmbeddingProvider(2048)
	observability.DefaultLogger.Info("embedding", "using local embedding provider", nil)
}
```

- [ ] **Step 7: Update .env.example**

Append to `.env.example` after web search block:

```text
# ---------------------------------------------------------------------------
# Embedding provider — optional. Leave unset to use the zero-dependency local
# TF-IDF / hashed provider. Set EMBEDDING_PROVIDER=openai or cohere plus key.
# EMBEDDING_PROVIDER=openai
# EMBEDDING_ENDPOINT=https://api.openai.com/v1
# EMBEDDING_API_KEY=sk-xxx
# EMBEDDING_MODEL=text-embedding-3-small
# EMBEDDING_DIMENSIONS=1536
```

- [ ] **Step 8: Build and test**

Run: `go build ./cmd/server && go test ./internal/config ./internal/llm`

Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/server/main.go .env.example
git commit -m "Phase 7-B: embedding provider config + server wiring"
```

---

## Task 4: Memory Indexer (Incremental Upsert + Semantic Deduplication)

**Files:**
- Create: `internal/memory/indexer.go`
- Test: `internal/memory/indexer_test.go`
- Modify: `pkg/db/memory.go`
- Modify: `cmd/server/main.go`
- Modify: `internal/harness/recall.go`

- [ ] **Step 1: Write the failing test**

Create `internal/memory/indexer_test.go`:

```go
package memory

import (
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

type mockEmbeddingProvider struct {
	dims int
}

func (m *mockEmbeddingProvider) Embed(text string) ([]float32, error) {
	vec := make([]float32, m.dims)
	vec[0] = float32(len(text)) / 100.0
	return vec, nil
}

func (m *mockEmbeddingProvider) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, _ := m.Embed(t)
		out[i] = v
	}
	return out, nil
}

func (m *mockEmbeddingProvider) Dimensions() int { return m.dims }

func TestIndexerUpsertAndDeduplicate(t *testing.T) {
	store := NewInMemoryVectorStore(&mockEmbeddingProvider{dims: 4})
	idx := NewMemoryIndexer(store, &mockEmbeddingProvider{dims: 4}, MemoryIndexerOptions{DedupeThreshold: 1.0})

	// First memory should be indexed.
	if err := idx.OnMemoryCreated("m1", "hello world"); err != nil {
		t.Fatalf("on created m1: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("len = %d, want 1", store.Len())
	}

	// Identical memory should be de-duplicated (cosine == 1.0 >= threshold).
	if err := idx.OnMemoryCreated("m2", "hello world"); err != nil {
		t.Fatalf("on created m2: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("after dedup len = %d, want 1", store.Len())
	}

	// Different memory should be indexed.
	if err := idx.OnMemoryCreated("m3", "completely different content"); err != nil {
		t.Fatalf("on created m3: %v", err)
	}
	if store.Len() != 2 {
		t.Fatalf("after different len = %d, want 2", store.Len())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory -run TestIndexerUpsertAndDeduplicate -v`

Expected: FAIL `undefined: NewMemoryIndexer`

- [ ] **Step 3: Implement the indexer**

Create `internal/memory/indexer.go`:

```go
package memory

import (
	"fmt"
	"sync"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// MemoryIndexerOptions configures the incremental indexing behavior.
type MemoryIndexerOptions struct {
	// DedupeThreshold is the cosine similarity above which a new memory is
	// considered a duplicate of an existing one and skipped. 1.0 means identical.
	// Typical production values range 0.92-0.98.
	DedupeThreshold float64

	// NormalizeBeforeStore is true by default; set false to store raw vectors.
	NormalizeBeforeStore bool
}

// MemoryIndexer maintains the vector store incrementally: every newly created
// memory is embedded and upserted, and near-duplicate memories are skipped.
//
// It replaces the previous startup-time BuildVectorIndex full scan.
type MemoryIndexer struct {
	store        VectorStore
	provider     llm.EmbeddingProvider
	opts         MemoryIndexerOptions
	mu           sync.Mutex
	indexedIDs   map[string]bool
}

// NewMemoryIndexer creates an indexer bound to the given store and provider.
func NewMemoryIndexer(store VectorStore, provider llm.EmbeddingProvider, opts MemoryIndexerOptions) *MemoryIndexer {
	if opts.DedupeThreshold <= 0 {
		opts.DedupeThreshold = 0.95
	}
	if opts.DedupeThreshold > 1 {
		opts.DedupeThreshold = 1
	}
	if opts.NormalizeBeforeStore == false {
		// default true
		opts.NormalizeBeforeStore = true
	}
	return &MemoryIndexer{
		store:      store,
		provider:   provider,
		opts:       opts,
		indexedIDs: make(map[string]bool),
	}
}

// OnMemoryCreated is called after a memory record is inserted. It embeds the
// content, runs deduplication against the existing index, and upserts the
// vector if it is novel enough.
func (idx *MemoryIndexer) OnMemoryCreated(memoryID, content string) error {
	if idx.provider == nil || idx.store == nil {
		return nil
	}
	if memoryID == "" || content == "" {
		return fmt.Errorf("MemoryIndexer: memoryID and content required")
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.indexedIDs[memoryID] {
		return nil
	}

	vec, err := idx.provider.Embed(content)
	if err != nil {
		return fmt.Errorf("MemoryIndexer: embed %q: %w", memoryID, err)
	}

	if idx.opts.NormalizeBeforeStore {
		vec = NormalizeVector(vec)
	}

	// Dedup: search top-1 in existing store.
	if idx.opts.DedupeThreshold < 1.0 {
		results, err := idx.store.Search(vec, 1)
		if err == nil && len(results) > 0 && results[0].Score >= idx.opts.DedupeThreshold {
			// Duplicate — do not index.
			idx.indexedIDs[memoryID] = true
			return nil
		}
	}

	if err := idx.store.Upsert(memoryID, vec, map[string]any{"content_preview": truncate(content, 200)}); err != nil {
		return fmt.Errorf("MemoryIndexer: upsert %q: %w", memoryID, err)
	}
	idx.indexedIDs[memoryID] = true
	return nil
}

// OnMemoryDeleted removes a memory vector from the index.
func (idx *MemoryIndexer) OnMemoryDeleted(memoryID string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.indexedIDs, memoryID)
	if idx.store == nil {
		return nil
	}
	return idx.store.Delete(memoryID)
}

// truncate returns the first n runes of s (simple helper for metadata preview).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/memory -run TestIndexerUpsertAndDeduplicate -v`

Expected: PASS

- [ ] **Step 5: Hook into memory insertion**

Edit `pkg/db/memory.go`. Add package-level callback variable near the top (after imports):

```go
// PostInsertMemoryHook is called by InsertMemory after a successful insert.
// It is set externally by cmd/server/main.go to wire the MemoryIndexer.
var PostInsertMemoryHook func(memoryID, content string)
```

At the end of `InsertMemory`, before `return err`:

```go
	if PostInsertMemoryHook != nil {
		PostInsertMemoryHook(record.ID, record.Content)
	}
	return err
```

- [ ] **Step 6: Wire indexer in server bootstrap**

Edit `cmd/server/main.go`. After vectorStore creation and MemoryRecall setup:

```go
// Incremental memory indexer: embed new memories as they are created
// instead of rebuilding the whole index at startup.
memoryIndexer := memory.NewMemoryIndexer(vectorStore, embedProvider, memory.MemoryIndexerOptions{DedupeThreshold: 0.95})
db.PostInsertMemoryHook = func(memoryID, content string) {
	if err := memoryIndexer.OnMemoryCreated(memoryID, content); err != nil {
		observability.DefaultLogger.Warn("memory-indexer", "failed to index memory", map[string]any{"memory_id": memoryID, "error": err.Error()})
	}
}
```

Keep the existing `memRecall.BuildVectorIndex()` call for backward compatibility on first startup, but wrap it as best-effort:

```go
// Warm the index from existing memories on startup (best-effort).
if err := memRecall.BuildVectorIndex(); err != nil {
	log.Printf("MemoryRecall: failed to build vector index: %v", err)
}
```

- [ ] **Step 7: Build and run memory tests**

Run: `go test ./internal/memory ./pkg/db`

Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/memory/indexer.go internal/memory/indexer_test.go pkg/db/memory.go cmd/server/main.go
git commit -m "Phase 7-B: incremental memory indexing + semantic dedup"
```

---

## Task 5: Hybrid Ranker (BM25 + Vector + Keyword)

**Files:**
- Create: `internal/harness/hybrid_ranker.go`
- Test: `internal/harness/hybrid_ranker_test.go`
- Modify: `internal/harness/recall.go`

- [ ] **Step 1: Write the failing test**

Create `internal/harness/hybrid_ranker_test.go`:

```go
package harness

import (
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/memory"
)

type stubEmbed struct{}

func (s *stubEmbed) Embed(text string) ([]float32, error) {
	vec := make([]float32, 4)
	switch text {
	case "deploy go service":
		vec[0] = 1.0
	case "python flask deploy":
		vec[1] = 1.0
	case "golang http server deployment":
		vec[0] = 0.9
	}
	return vec, nil
}

func (s *stubEmbed) EmbedBatch(texts []string) ([][]float32, error) { return nil, nil }
func (s *stubEmbed) Dimensions() int                               { return 4 }

func TestHybridRankerScore(t *testing.T) {
	store := memory.NewInMemoryVectorStore(&stubEmbed{})
	_ = store.Upsert("m1", []float32{1, 0, 0, 0}, map[string]any{"content": "deploy go service"})
	_ = store.Upsert("m2", []float32{0, 1, 0, 0}, map[string]any{"content": "python flask deploy"})

	ranker := NewHybridRanker(&stubEmbed{}, store, HybridWeights{Keyword: 0.2, BM25: 0.3, Vector: 0.5})

	score1 := ranker.Score("deploy go service", "golang http server deployment")
	score2 := ranker.Score("python flask deploy", "golang http server deployment")
	if score1 <= score2 {
		t.Fatalf("go query should score higher than python: %.3f vs %.3f", score1, score2)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/harness -run TestHybridRankerScore -v`

Expected: FAIL `undefined: NewHybridRanker`

- [ ] **Step 3: Implement the ranker**

Create `internal/harness/hybrid_ranker.go`:

```go
package harness

import (
	"math"
	"strings"
	"unicode"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/memory"
)

// HybridWeights configures the linear combination of keyword, BM25, and vector
// scores. All weights should sum to 1.0 for a normalized [0,100] output.
type HybridWeights struct {
	Keyword float64
	BM25    float64
	Vector  float64
}

// DefaultHybridWeights is the production default: vector dominates, BM25
// contributes lexical signal, keyword provides a cheap fallback.
var DefaultHybridWeights = HybridWeights{Keyword: 0.2, BM25: 0.3, Vector: 0.5}

// HybridRanker scores a candidate document against a query using three signals.
type HybridRanker struct {
	provider llm.EmbeddingProvider
	store    memory.VectorStore
	weights  HybridWeights
}

// NewHybridRanker creates a ranker. provider or store may be nil, in which case
// vector scoring is skipped.
func NewHybridRanker(provider llm.EmbeddingProvider, store memory.VectorStore, weights HybridWeights) *HybridRanker {
	return &HybridRanker{provider: provider, store: store, weights: weights}
}

// Score returns a combined relevance score in [0, 100].
func (r *HybridRanker) Score(content, query string) float64 {
	kw := keywordScore(content, query) / 100.0
	bm := bm25Score(tokenize(content), tokenize(query), 1.2, 0.75) // returns already normalized-ish
	vec := r.vectorScore(content, query)

	w := r.weights
	// Normalize weights in case they do not sum to 1.
	total := w.Keyword + w.BM25 + w.Vector
	if total == 0 {
		total = 1
	}
	return 100 * ((w.Keyword*kw + w.BM25*bm + w.Vector*vec) / total)
}

func (r *HybridRanker) vectorScore(content, query string) float64 {
	if r.provider == nil || r.store == nil {
		return 0
	}
	queryVec, err := r.provider.Embed(query)
	if err != nil {
		return 0
	}
	// Search the store for the candidate document vector. If content matches
	// metadata preview, use its score; otherwise fall back to embedding content.
	results, err := r.store.Search(queryVec, 10)
	if err != nil {
		return 0
	}
	for _, res := range results {
		if preview, ok := res.Metadata["content_preview"].(string); ok && strings.Contains(content, preview) || strings.Contains(preview, content) {
			return res.Score
		}
	}
	// Fallback: embed the content directly.
	contentVec, err := r.provider.Embed(content)
	if err != nil {
		return 0
	}
	return memory.CosineSimilarity(queryVec, contentVec)
}

// bm25Score computes a simplified Okapi BM25 between a document and a query.
// Returns a score normalized by a saturation factor so it lives in [0,1].
func bm25Score(docWords, queryWords []string, k1, b float64) float64 {
	if len(queryWords) == 0 || len(docWords) == 0 {
		return 0
	}
	avgDL := 20.0 // rough average document length heuristic; production can precompute corpus stats
	docLen := float64(len(docWords))
	docFreq := make(map[string]int, len(docWords))
	for _, w := range docWords {
		docFreq[w]++
	}
	var score float64
	for _, qw := range queryWords {
		f := float64(docFreq[qw])
		if f == 0 {
			continue
		}
		idf := math.Log(1 + (1.0 / f)) // simplified idf for single doc
		denom := f + k1*(1-b+b*docLen/avgDL)
		score += idf * (f * (k1 + 1)) / denom
	}
	// Saturate: a generous ceiling so output stays in [0,1].
	return math.Min(score, 5.0) / 5.0
}

// tokenize splits text into lower-case words, stripping punctuation.
// This is a package-local duplicate to avoid exporting a new symbol.
func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		tok := strings.TrimFunc(f, func(r rune) bool {
			return unicode.IsPunct(r) || unicode.IsSymbol(r)
		})
		if len(tok) >= 2 {
			out = append(out, tok)
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/harness -run TestHybridRankerScore -v`

Expected: PASS

- [ ] **Step 5: Replace recall scoring**

Edit `internal/harness/recall.go`. At the `MemoryRecall` struct, add:

```go
	ranker *HybridRanker
```

Add a constructor `NewMemoryRecallWithVectorStoreAndRanker` that accepts a ranker. Or modify the existing constructor to create a `HybridRanker` from the passed provider/store with default weights:

```go
func NewMemoryRecallWithVectorStore(db *MemoryDB, provider llm.EmbeddingProvider, store memory.VectorStore) *MemoryRecall {
	return &MemoryRecall{
		db:           db,
		embedProvider: provider,
		vectorStore:  store,
		ranker:       NewHybridRanker(provider, store, DefaultHybridWeights),
	}
}
```

Replace the body of `blendVectorScores` with a call to the ranker:

```go
func (mr *MemoryRecall) blendVectorScores(content, query string) float64 {
	if mr.ranker == nil {
		return keywordScore(content, query)
	}
	return mr.ranker.Score(content, query)
}
```

- [ ] **Step 6: Build and run harness tests**

Run: `go test ./internal/harness`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/harness/hybrid_ranker.go internal/harness/hybrid_ranker_test.go internal/harness/recall.go
git commit -m "Phase 7-B: hybrid ranker (BM25 + vector + keyword) for memory recall"
```

---

## Task 6: Integration Test and Roadmap Update

**Files:**
- Modify: `internal/llm/embedding_provider_test.go` (add integration subset)
- Modify: `roadmaps/ROADMAP.md`
- Create: `docs/superpowers/plans/2026-07-18-phase-7b-embedding-vector-integration.md` (already this file)

- [ ] **Step 1: Add batch dimension validation test**

Append to `internal/llm/embedding_provider_test.go`:

```go
func TestOpenAIEmbeddingProviderDimensionValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": []map[string]any{
				{"index": 0, "embedding": []float32{0.1, 0.2}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIEmbeddingProvider(server.URL, "key", "text-embedding-3-small", 3)
	_, err := p.Embed("text")
	if err == nil {
		t.Fatal("expected dimension mismatch error")
	}
}
```

- [ ] **Step 2: Run full LLM tests**

Run: `go test ./internal/llm`

Expected: PASS

- [ ] **Step 3: Update ROADMAP**

Edit `roadmaps/ROADMAP.md` Phase 7 section. Mark 7-B items as completed where applicable:

```markdown
### 7-B 外部向量与 Embedding 集成
- [x] `EmbeddingProvider` 远程实现: OpenAI text-embedding-3 / Cohere（复用现有接口，无侵入）
- [ ] `VectorStore` 持久化后端: pgvector（保留 SQLite 兜底，Phase 7-E 再做迁移）
- [x] 混合检索: 向量召回 + BM25 关键词 + 重排（`HybridRanker` 替换 `blendVectorScores`）
- [x] 增量索引: `MemoryIndexer` + `PostInsertMemoryHook` 实时 upsert，替代启动全量 `BuildVectorIndex`
- [x] 语义去重: 新 memory 与已有记忆相似度阈值合并，控制记忆膨胀
```

- [ ] **Step 4: Full build + test**

Run: `go build ./cmd/server && go test ./internal/... ./pkg/...`

Expected: PASS

- [ ] **Step 5: Final commit**

```bash
git add internal/llm/embedding_provider_test.go roadmaps/ROADMAP.md docs/superpowers/plans/2026-07-18-phase-7b-embedding-vector-integration.md
git commit -m "Phase 7-B: integration tests + roadmap update"
```

---

## Self-Review

1. **Spec coverage:**
   - 远程 OpenAI/Cohere EmbeddingProvider ✅ Task 1-2
   - 配置加载与 server wiring ✅ Task 3
   - 增量索引 ✅ Task 4
   - 语义去重 ✅ Task 4
   - 混合检索 BM25+vector+keyword ✅ Task 5
   - pgvector backend 保留为 Phase 7-E 迁移项（超出了当前无外部依赖的 scope）

2. **Placeholder scan:** 无 TBD / TODO / 待实现；所有步骤包含代码与命令。

3. **Type consistency:**
   - `EmbeddingProvider` 接口未改，新 provider 实现 `Embed/EmbedBatch/Dimensions`。
   - `Config` 新增字段与 `BuildEmbeddingProvider` 签名一致。
   - `MemoryIndexer` 的 `OnMemoryCreated(memoryID, content string)` 与 `PostInsertMemoryHook` 一致。

4. **边界考虑:**
   - 无 key / provider 为空时仍使用 `LocalEmbeddingProvider`（兼容现有测试与默认部署）。
   - 远程 provider 维度不匹配返回错误，不影响引擎。
   - `vectorScore` 优先匹配 metadata preview，避免每次对 content/embed。
