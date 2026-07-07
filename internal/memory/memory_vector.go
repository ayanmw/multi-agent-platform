// Package memory provides vector storage interfaces and in-memory implementation
// for semantic search and RAG pipelines.
//
// # Design Rationale
//
// VectorStore abstracts the storage and retrieval of embedding vectors. The
// InMemoryVectorStore provides a zero-dependency, development-friendly backend
// suitable for prototyping and testing. In Phase 6+ this can be swapped for
// production backends like Qdrant, Weaviate, or pgvector (via the VectorStore interface).
//
// Similarity search uses cosine similarity — normalized so that:
//   score = 1.0 → identical vectors
//   score = 0.0 → orthogonal (unrelated)
//   score = -1.0 → opposite (rare in practice)
//
// # Thread Safety
//
// InMemoryVectorStore uses sync.RWMutex for concurrent access. Read operations
// (Search) use read locks (concurrent), while write operations (Upsert, Delete)
// use write locks (serialized).
package memory

import (
	"errors"
	"math"
	"sort"
	"sync"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// Sentinel errors for vector store operations.
var (
	ErrEmptyID         = errors.New("vector store: id cannot be empty")
	ErrEmptyVector     = errors.New("vector store: vector cannot be empty")
	ErrDimensionMismatch = errors.New("vector store: vector dimension does not match embedding provider")
)

// InMemoryVectorStore is a goroutine-safe, in-memory implementation of VectorStore.
//
// It uses a plain map with RWMutex for concurrent access. Suitable for development
// and testing; for production use, implement VectorStore with Qdrant, Weaviate,
// or pgvector.
type InMemoryVectorStore struct {
	mu       sync.RWMutex
	vectors  map[string][]float32    // id → embedding vector (copies)
	metadata map[string]map[string]any // id → metadata copy

	// embedProvider is the optional provider used to validate vector dimensions
	// on Upsert. When nil, dimension validation is skipped.
	embedProvider llm.EmbeddingProvider
}

// NewInMemoryVectorStore creates a new InMemoryVectorStore.
//
// Optionally pass an EmbeddingProvider to enable dimension validation on Upsert.
// If provider is nil, Upsert accepts vectors of any length.
func NewInMemoryVectorStore(provider llm.EmbeddingProvider) *InMemoryVectorStore {
	return &InMemoryVectorStore{
		vectors:       make(map[string][]float32),
		metadata:      make(map[string]map[string]any),
		embedProvider: provider,
	}
}

// Upsert stores or updates a vector with associated metadata.
// If a vector with the same id already exists, it is overwritten.
// The vector length must match the embedding provider's Dimensions() when
// a provider is configured; otherwise any length is accepted.
func (s *InMemoryVectorStore) Upsert(id string, vector []float32, metadata map[string]any) error {
	if id == "" {
		return ErrEmptyID
	}
	if len(vector) == 0 {
		return ErrEmptyVector
	}

	// Validate dimensions when a provider is configured.
	if s.embedProvider != nil {
		expected := s.embedProvider.Dimensions()
		if len(vector) != expected {
			return ErrDimensionMismatch
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Deep-copy the vector to prevent caller mutation.
	vectorCopy := make([]float32, len(vector))
	copy(vectorCopy, vector)

	// Deep-copy metadata map.
	metaCopy := make(map[string]any, len(metadata))
	for k, v := range metadata {
		metaCopy[k] = v
	}

	s.vectors[id] = vectorCopy
	s.metadata[id] = metaCopy
	return nil
}

// Search finds the top-K vectors most similar to the query vector
// using cosine similarity. Returns results sorted by score descending (highest first).
// An empty store or no matches returns an empty slice (not an error).
func (s *InMemoryVectorStore) Search(query []float32, topK int) ([]SearchResult, error) {
	if len(query) == 0 {
		return nil, ErrEmptyVector
	}
	if topK <= 0 {
		topK = 10 // sensible default
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect all similarity scores.
	type scored struct {
		id    string
		score float64
		meta  map[string]any
	}
	scores := make([]scored, 0, len(s.vectors))
	for id, vec := range s.vectors {
		score := CosineSimilarity(query, vec)
		scores = append(scores, scored{
			id:    id,
			score: score,
			meta:  s.metadata[id],
		})
	}

	if len(scores) == 0 {
		return []SearchResult{}, nil
	}

	// Sort by score descending.
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// Take top-K (or all if fewer results).
	if topK > len(scores) {
		topK = len(scores)
	}

	results := make([]SearchResult, topK)
	for i := 0; i < topK; i++ {
		results[i] = SearchResult{
			ID:       scores[i].id,
			Score:    scores[i].score,
			Metadata: scores[i].meta,
		}
	}
	return results, nil
}

// Delete removes a vector and its metadata by id.
// No-op if the id does not exist (no error returned).
func (s *InMemoryVectorStore) Delete(id string) error {
	if id == "" {
		return ErrEmptyID
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.vectors, id)
	delete(s.metadata, id)
	return nil
}

// Len returns the number of vectors currently stored.
// Primarily used for testing and metrics.
func (s *InMemoryVectorStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.vectors)
}

// Clear removes all vectors and metadata from the store.
func (s *InMemoryVectorStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.vectors = make(map[string][]float32)
	s.metadata = make(map[string]map[string]any)
}

// NormalizeVector scales a vector to unit length (L2 norm = 1.0).
// Cosine similarity is equivalent to dot product for unit vectors,
// so normalizing at storage time speeds up repeated comparisons.
// Returns the original vector unchanged if its magnitude is zero.
func NormalizeVector(v []float32) []float32 {
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
