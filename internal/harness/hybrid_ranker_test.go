package harness

import (
	"testing"

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
func (s *stubEmbed) Dimensions() int                                 { return 4 }

func TestHybridRankerScore(t *testing.T) {
	store := memory.NewInMemoryVectorStore(&stubEmbed{})
	_ = store.Upsert("m1", []float32{1, 0, 0, 0}, map[string]any{"content_preview": "deploy go service"})
	_ = store.Upsert("m2", []float32{0, 1, 0, 0}, map[string]any{"content_preview": "python flask deploy"})

	ranker := NewHybridRanker(&stubEmbed{}, store, HybridWeights{Keyword: 0.2, BM25: 0.3, Vector: 0.5})

	score1 := ranker.Score("deploy go service", "golang http server deployment")
	score2 := ranker.Score("python flask deploy", "golang http server deployment")
	if score1 <= score2 {
		t.Fatalf("go query should score higher than python: %.3f vs %.3f", score1, score2)
	}
}

func TestBM25Score(t *testing.T) {
	score := bm25Score(tokenizeForRanker("the quick brown fox"), tokenizeForRanker("quick fox"), 1.2, 0.75)
	if score < 0 || score > 1 {
		t.Fatalf("bm25 score = %v, want [0,1]", score)
	}
}

func TestKeywordScore(t *testing.T) {
	score := keywordScore("hello world example", "hello world")
	if score < 0 || score > 100 {
		t.Fatalf("keyword score = %v, want [0,100]", score)
	}
}

func TestHybridRankerNilProvider(t *testing.T) {
	ranker := NewHybridRanker(nil, nil, DefaultHybridWeights)
	score := ranker.Score("hello world", "hello")
	if score < 0 || score > 100 {
		t.Fatalf("score = %v, want [0,100]", score)
	}
}
