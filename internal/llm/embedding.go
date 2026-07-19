// Package llm —— EmbeddingProvider 接口，用于文本 embedding model。
//
// EmbeddingProvider 将文本转换为稠密向量表示（embedding），
// 适用于语义搜索、聚类和相似度比较。
//
// # 设计理由
//
// 该接口支持单文本和批量操作，调用方可以嵌入单条 query，
// 也可以批量处理文档。Dimensions() 方法让调用方在存储或比较前
// 校验向量尺寸。
//
// 实现可使用：
//   - 本地模型（例如 sentence-transformers、Ollama embeddings）
//   - 远程 API（例如 OpenAI text-embedding-3-small、Cohere embed）
//   - 轻量本地方案（例如用于原型的 TF-IDF / one-hot hashing）
//
// Phase 6 的 RAG 流水线使用该接口，在存储/检索 vector store 之前
// 对文档与 query 进行 embedding。
package llm

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// EmbeddingProvider 定义文本 embedding model 的接口。
//
// 实现将文本转换为浮点向量（embedding），捕获语义信息以用于基于相似度的搜索。
type EmbeddingProvider interface {
	// Embed 将单个文本字符串转换为稠密向量。
	// 若文本为空或 provider 不可用则返回 error。
	Embed(text string) ([]float32, error)

	// EmbedBatch 在一次调用中将多个文本字符串转换为稠密向量。
	// 对大型语料通常比反复调用 Embed() 更高效。
	// 返回 slice 的长度与输入 slice 相同。
	EmbedBatch(texts []string) ([][]float32, error)

	// Dimensions 返回该 provider 产出的 embedding 向量的固定维度
	//（例如 384、768、1536）。
	Dimensions() int
}

// LocalEmbeddingProvider 是一个零依赖的本地 embedding 实现，
// 基于固定大小的哈希词表。
//
// 它对输入文本做分词，用 FNV-1a 哈希将每个 token 映射到确定性的词表槽位，
// 产出稀疏的 one-hot / 词频向量。输出做 L2 归一化，因此 cosine 相似度
// 等于点积。
//
// 该实现刻意简单，不需要外部 model 或 vector DB。
// 适用于 Phase 6 RAG 原型阶段 —— 此时精确的语义质量不如
// 可观测性、零配置与确定性行为重要。
//
// # 工作原理
//
//  1. 分词：转小写、去标点、丢弃短 token 与 stop word。
//  2. 哈希：每个 token 被哈希到 [0, vocabSize) 中的某个槽位。
//  3. 累积：每次出现某 token 就让其槽位计数 +1（词频）。
//  4. 归一化：最终向量被缩放到单位 L2 范数。
//
// 取舍：当 vocabSize 较小时可能出现词表碰撞；使用更大的 vocabSize
//（例如 2048 或 4096）可获得更稠密、碰撞更少的向量。
type LocalEmbeddingProvider struct {
	vocabSize int
	stopWords map[string]bool
}

// localEmbeddingDefaultStopWords 是未提供自定义列表时使用的小型英文 stop word 列表。
// 移除这些常见词可以降低稀疏向量中的噪声。
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

// ErrEmptyText 在 Embed 被空文本或仅空白字符的文本调用时返回。
var ErrEmptyText = errors.New("embedding provider: text is empty")

// NewLocalEmbeddingProvider 以给定词表大小创建 LocalEmbeddingProvider。
// 更大的 vocabSize 可降低哈希碰撞；对短文本 2048 是一个合理默认值，
// 而 4096 可提供更好的区分度。
//
// 返回的 provider 无可变共享状态，可安全并发使用。
func NewLocalEmbeddingProvider(vocabSize int) *LocalEmbeddingProvider {
	if vocabSize <= 0 {
		vocabSize = 2048
	}
	return &LocalEmbeddingProvider{
		vocabSize: vocabSize,
		stopWords: localEmbeddingDefaultStopWords,
	}
}

// Embed 将单个文本字符串转换为归一化的稀疏向量。
//
// 向量长度等于 Dimensions()。文本中每个 token 在其哈希词表槽位处
// 递增词频计数。结果向量做 L2 归一化，调用方可直接用 cosine 相似度
// 或点积。
//
// 若文本在 stop word 与标点过滤后不含任何有意义的 token，
// 则返回 ErrEmptyText。
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

// EmbedBatch 将多个文本转换为归一化的稀疏向量。
//
// 批量 embedding 复用与 Embed 相同的分词与哈希流水线。
// 各文本的错误会被收集并以单个 error 形式返回；
// 对应位置在结果中返回 nil slice。
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

// Dimensions 返回该 provider 使用的固定词表大小。
func (p *LocalEmbeddingProvider) Dimensions() int {
	return p.vocabSize
}

// normalizeVector 将向量缩放为单位长度（L2 norm = 1.0）。
// 本地拷贝以避免与 memory 包产生 import 循环。
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

// tokenize 将文本切分为归一化的 word token。
//
// 步骤：
//  1. 转小写。
//  2. 按 Unicode 空白切分。
//  3. 去除每个 token 的首尾标点。
//  4. 丢弃 stop word 以及短于 2 个字符的 token。
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

// hashToken 将 token 映射到 [0, vocabSize) 中的确定性词表槽位。
// 使用 FNV-1a 32 位哈希；可能发生碰撞但结果确定性。
func (p *LocalEmbeddingProvider) hashToken(token string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(token))
	return int(h.Sum32() % uint32(p.vocabSize))
}
