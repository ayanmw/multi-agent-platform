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
//   - Lightweight local schemes (e.g., TF-IDF / one-hot hashing for prototyping)
//
// The Phase 6 RAG pipeline uses this interface to embed documents and queries
// before storing/searching the vector store.
package llm

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

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

// LocalEmbeddingProvider is a zero-dependency, local embedding implementation
// based on a fixed-size hashed vocabulary.
//
// It tokenizes input text, maps each token to a deterministic vocabulary slot
// using FNV-1a hashing, and produces a sparse one-hot / term-frequency vector.
// The output is L2-normalized so that cosine similarity equals dot product.
//
// This is intentionally simple and requires no external model or vector DB.
// It is suitable for Phase 6 RAG prototyping, where exact semantic quality is
// less important than observability, zero setup, and deterministic behavior.
//
// # How it works
//
//  1. Tokenize: lower-case, strip punctuation, drop short tokens and stop words.
//  2. Hash: each token is hashed to a slot in [0, vocabSize).
//  3. Accumulate: each occurrence of a token increments its slot (term frequency).
//  4. Normalize: the final vector is scaled to unit L2 norm.
//
// Trade-offs: vocabulary collisions are possible when vocabSize is small; use
// a larger vocabSize (e.g., 2048 or 4096) for denser, lower-collision vectors.
type LocalEmbeddingProvider struct {
	vocabSize int
	stopWords map[string]bool
}

// localEmbeddingDefaultStopWords is a small English stop-word list used when
// no custom list is provided. Removing these common words reduces noise in
// sparse vectors.
var localEmbeddingDefaultStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "must": true, "shall": true, "can": true, "need": true,
	"dare": true, "ought": true, "used": true, "to": true, "of": true,
	"in": true, "for": true, "on": true, "with": true, "at": true,
	"by": true, "from": true, "as": true, "into": true, "through": true,
	"during": true, "before": true, "after": true, "above": true, "below": true,
	"between": true, "under": true, "and": true, "but": true, "or": true,
	"yet": true, "so": true, "if": true, "because": true, "although": true,
	"though": true, "while": true, "where": true, "when": true, "that": true,
	"this": true, "these": true, "those": true, "i": true, "you": true,
	"he": true, "she": true, "it": true, "we": true, "they": true,
	"me": true, "him": true, "her": true, "us": true, "them": true,
	"my": true, "your": true, "his": true, "its": true, "our": true,
	"their": true, "what": true, "which": true, "who": true, "whom": true,
	"whose": true, "how": true, "why": true, "not": true, "no": true,
	"nor": true, "only": true, "own": true, "same": true, "such": true,
	"than": true, "too": true, "very": true, "just": true, "now": true,
	"then": true, "here": true, "there": true, "all": true, "any": true,
	"both": true, "each": true, "few": true, "more": true, "most": true,
	"other": true, "some": true, "one": true, "two": true, "three": true,
}

// ErrEmptyText is returned when Embed is called with empty or whitespace-only text.
var ErrEmptyText = errors.New("embedding provider: text is empty")

// NewLocalEmbeddingProvider creates a LocalEmbeddingProvider with the given
// vocabulary size. A larger vocabSize reduces hash collisions; 2048 is a
// reasonable default for short texts, while 4096 yields better differentiation.
//
// The returned provider shares no mutable state and is safe for concurrent use.
func NewLocalEmbeddingProvider(vocabSize int) *LocalEmbeddingProvider {
	if vocabSize <= 0 {
		vocabSize = 2048
	}
	return &LocalEmbeddingProvider{
		vocabSize: vocabSize,
		stopWords: localEmbeddingDefaultStopWords,
	}
}

// Embed converts a single text string into a normalized sparse vector.
//
// The vector length equals Dimensions(). Each token in the text increments
// the frequency count at its hashed vocabulary slot. The resulting vector is
// L2-normalized so callers can use cosine similarity or dot product directly.
//
// Returns ErrEmptyText if the text contains no meaningful tokens after
// stop-word and punctuation filtering.
func (p *LocalEmbeddingProvider) Embed(text string) ([]float32, error) {
	tokens := p.tokenize(text)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrEmptyText, text)
	}

	vec := make([]float32, p.vocabSize)
	for _, tok := range tokens {
		slot := p.hashToken(tok)
		vec[slot]++
	}

	return normalizeVector(vec), nil
}

// EmbedBatch converts multiple texts into normalized sparse vectors.
//
// Batch embedding reuses the same tokenization and hashing pipeline as Embed.
// Errors for individual texts are collected and returned as a single error;
// nil slices are returned in the result for those positions.
func (p *LocalEmbeddingProvider) EmbedBatch(texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	var errs []error
	for i, text := range texts {
		vec, err := p.Embed(text)
		if err != nil {
			errs = append(errs, fmt.Errorf("index %d: %w", i, err))
		}
		results[i] = vec
	}
	if len(errs) > 0 {
		return results, errors.Join(errs...)
	}
	return results, nil
}

// Dimensions returns the fixed vocabulary size used by this provider.
func (p *LocalEmbeddingProvider) Dimensions() int {
	return p.vocabSize
}

// normalizeVector scales a vector to unit length (L2 norm = 1.0).
// Local copy avoids an import cycle with the memory package.
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

// tokenize splits text into normalized word tokens.
//
// Steps:
//  1. Convert to lower case.
//  2. Split on Unicode whitespace.
//  3. Trim punctuation from each token.
//  4. Drop stop words and tokens shorter than 2 characters.
func (p *LocalEmbeddingProvider) tokenize(text string) []string {
	fields := strings.Fields(strings.ToLower(text))
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		tok := strings.TrimFunc(f, func(r rune) bool {
			return unicode.IsPunct(r) || unicode.IsSymbol(r)
		})
		if len(tok) < 2 {
			continue
		}
		if p.stopWords[tok] {
			continue
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

// hashToken maps a token to a deterministic vocabulary slot in [0, vocabSize).
// Uses FNV-1a 32-bit hashing; collisions are possible but deterministic.
func (p *LocalEmbeddingProvider) hashToken(token string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(token))
	return int(h.Sum32() % uint32(p.vocabSize))
}
