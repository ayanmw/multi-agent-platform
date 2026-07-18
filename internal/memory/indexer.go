package memory

import (
	"fmt"
	"math"
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
	store      VectorStore
	provider   llm.EmbeddingProvider
	opts       MemoryIndexerOptions
	mu         sync.Mutex
	indexedIDs map[string]bool
}

// NewMemoryIndexer creates an indexer bound to the given store and provider.
func NewMemoryIndexer(store VectorStore, provider llm.EmbeddingProvider, opts MemoryIndexerOptions) *MemoryIndexer {
	if opts.DedupeThreshold <= 0 {
		opts.DedupeThreshold = 0.95
	}
	if opts.DedupeThreshold > 1 {
		opts.DedupeThreshold = 1
	}
	if !opts.NormalizeBeforeStore {
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
		vec = normalizeVector(vec)
	}

	// Dedup: search top-1 in existing store.
	if idx.opts.DedupeThreshold <= 1.0 {
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

// normalizeVector scales a vector to unit length (L2 norm = 1.0).
func normalizeVector(v []float32) []float32 {
	var sumSquares float64
	for _, f := range v {
		sumSquares += float64(f) * float64(f)
	}
	mag := math.Sqrt(sumSquares)
	if mag == 0 {
		return v
	}

	normalized := make([]float32, len(v))
	for i, f := range v {
		normalized[i] = float32(float64(f) / mag)
	}
	return normalized
}

// truncate returns the first n bytes of s (simple helper for metadata preview).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
