package harness

import (
	"math"
	"strings"
	"unicode"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/memory"
)

// HybridWeights 配置 keyword、BM25 与 vector 分数的线性组合。所有权重应求和为 1.0
// 以归一化输出到 [0,100]。
type HybridWeights struct {
	Keyword float64
	BM25    float64
	Vector  float64
}

// DefaultHybridWeights 是生产默认值：vector 为主，BM25 提供词法信号，keyword 作为
// 廉价兜底。
var DefaultHybridWeights = HybridWeights{Keyword: 0.2, BM25: 0.3, Vector: 0.5}

// HybridRanker 使用三种信号对候选文档相对查询的相关性打分。
type HybridRanker struct {
	provider llm.EmbeddingProvider
	store    memory.VectorStore
	weights  HybridWeights
}

// NewHybridRanker 创建一个 ranker。provider 或 store 可为 nil，此时跳过 vector 打分。
func NewHybridRanker(provider llm.EmbeddingProvider, store memory.VectorStore, weights HybridWeights) *HybridRanker {
	return &HybridRanker{provider: provider, store: store, weights: weights}
}

// Score 返回 [0, 100] 区间的组合相关性分数。
func (r *HybridRanker) Score(content, query string) float64 {
	kw := keywordScore(content, query) / 100.0
	bm := bm25Score(tokenizeForRanker(content), tokenizeForRanker(query), 1.2, 0.75) // 已近似归一化
	vec := r.vectorScore(content, query)

	w := r.weights
	// 归一化权重，以防它们不等于 1。
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
	// 在 store 中搜索候选文档向量。若 content 匹配 metadata preview，使用其分数；
	// 否则回退到对 content 进行 embedding。
	results, err := r.store.Search(queryVec, 10)
	if err != nil {
		return 0
	}
	for _, res := range results {
		if preview, ok := res.Metadata["content_preview"].(string); ok && (strings.Contains(content, preview) || strings.Contains(preview, content)) {
			return res.Score
		}
	}
	// 回退：直接 embed content。
	contentVec, err := r.provider.Embed(content)
	if err != nil {
		return 0
	}
	return memory.CosineSimilarity(queryVec, contentVec)
}

// bm25Score 计算文档与查询之间简化的 Okapi BM25。返回按饱和因子归一化的分数，
// 使其落在 [0,1]。
func bm25Score(docWords, queryWords []string, k1, b float64) float64 {
	if len(queryWords) == 0 || len(docWords) == 0 {
		return 0
	}
	avgDL := 20.0 // 粗略的平均文档长度启发式；生产可预计算语料统计
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
		idf := math.Log(1 + (1.0 / f)) // 单文档的简化 idf
		denom := f + k1*(1-b+b*docLen/avgDL)
		score += idf * (f * (k1 + 1)) / denom
	}
	// 饱和：一个宽松的上界，使输出保持在 [0,1]。
	return math.Min(score, 5.0) / 5.0
}

// tokenizeForRanker 将文本切分为小写单词，剥离标点。这是 package 内部的副本，以避免
// 导出新符号并保持 ranker 自包含。
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
