// Package memory provides vector storage interfaces and in-memory implementation
// for semantic search and RAG pipelines.
//
// # Design Rationale
//
// VectorStore abstracts the storage and retrieval of embedding vectors. The
// InMemoryVectorStore provides a zero-dependency, development-friendly backend
// suitable for prototyping and testing. In Phase 6+ this can be swapped for
// production backends like Qdrant, Weaviate, or pgvector.
//
// Similarity search uses cosine similarity — normalized so that:
//   score = 1.0 → identical vectors
//   score = 0.0 → orthogonal (unrelated)
//   score = -1.0 → opposite (rare in practice)

package memory

import "math"

// SearchResult represents a single result from a vector similarity search.
type SearchResult struct {
	// ID is the unique identifier of the stored vector.
	ID string

	// Score is the cosine similarity score (0.0 to 1.0, higher = more similar).
	Score float64

	// Metadata is the arbitrary key-value data associated with the vector at storage time.
	Metadata map[string]any
}

// VectorStore defines the interface for embedding storage and similarity search.
//
// Implementations must be goroutine-safe — concurrent Upsert/Search/Delete
// calls should not corrupt state.
type VectorStore interface {
	// Upsert stores or updates a vector with associated metadata.
	// If a vector with the same id already exists, it is overwritten.
	// The vector length must match the provider's Dimensions().
	Upsert(id string, vector []float32, metadata map[string]any) error

	// Search finds the top-K vectors most similar to the query vector
	// using cosine similarity. Returns results sorted by score descending.
	// An empty store or no matches returns an empty slice (not an error).
	Search(query []float32, topK int) ([]SearchResult, error)

	// Delete removes a vector by its id. No-op if the id does not exist.
	Delete(id string) error
}

// CosineSimilarity computes the cosine similarity between two vectors.
// Returns 0 if either vector is empty or has zero magnitude.
// The result is in the range [-1, 1], though embeddings are typically
// non-negative so results are in [0, 1].
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}

	var dotProduct, magA, magB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}

	denominator := math.Sqrt(magA) * math.Sqrt(magB)
	if denominator == 0 {
		return 0
	}
	return dotProduct / denominator
}

// NewSearchResult creates a SearchResult with the given fields.
// A convenience constructor used by store implementations.
func NewSearchResult(id string, score float64, metadata map[string]any) SearchResult {
	return SearchResult{
		ID:       id,
		Score:    score,
		Metadata: metadata,
	}
}
