// Package llm — EmbeddingProvider interface for text embedding models.
//
// An EmbeddingProvider converts text into dense vector representations (embeddings)
// suitable for semantic search, clustering, and similarity comparison.
//
// # Design Rationale
//
// The interface supports single-text and batch operations, allowing callers to
// embed individual queries or bulk-process documents. The Dimensions() method
// lets callers validate vector size before storage or comparison.
//
// Implementations may use:
//   - Local models (e.g., sentence-transformers, Ollama embeddings)
//   - Remote APIs (e.g., OpenAI text-embedding-3-small, Cohere embed)
//
// The Phase 6 RAG pipeline uses this interface to embed documents and queries
// before storing/searching the vector store.
package llm

// EmbeddingProvider defines the interface for text embedding models.
//
// Implementations convert text into floating-point vectors (embeddings)
// that capture semantic meaning for similarity-based search.
type EmbeddingProvider interface {
	// Embed converts a single text string into a dense vector.
	// Returns an error if the text is empty or the provider is unavailable.
	Embed(text string) ([]float32, error)

	// EmbedBatch converts multiple text strings into dense vectors in one call.
	// Typically more efficient than calling Embed() repeatedly for large corpora.
	// The returned slice has the same length as the input slice.
	EmbedBatch(texts []string) ([][]float32, error)

	// Dimensions returns the fixed dimensionality of the embedding vectors
	// produced by this provider (e.g., 384, 768, 1536).
	Dimensions() int
}
