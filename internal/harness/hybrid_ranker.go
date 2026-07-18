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
	bm := bm25Score(tokenizeForRanker(content), tokenizeForRanker(query), 1.2, 0.75) // returns already normalized-ish
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
		if preview, ok := res.Metadata["content_preview"].(string); ok && (strings.Contains(content, preview) || strings.Contains(preview, content)) {
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

// tokenizeForRanker splits text into lower-case words, stripping punctuation.
// This is a package-local duplicate to avoid exporting a new symbol and to keep
// the ranker self-contained.
func tokenizeForRanker(s string) []string {
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
