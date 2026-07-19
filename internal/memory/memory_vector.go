// Package memory 提供 vector storage 接口与内存实现,
// 用于语义搜索与 RAG 管线。
//
// # 设计理由
//
// VectorStore 抽象了 embedding vector 的存储与检索。InMemoryVectorStore
// 提供了一个零依赖、便于开发的后端,适合原型开发与测试。在 Phase 6+ 可以
// 通过 VectorStore 接口替换为生产级后端,如 Qdrant、Weaviate 或 pgvector。
//
// 相似度搜索使用 cosine similarity —— 归一化后:
//   score = 1.0 → 完全相同的 vector
//   score = 0.0 → 正交(无关联)
//   score = -1.0 → 相反(实践中罕见)
//
// # 线程安全
//
// InMemoryVectorStore 使用 sync.RWMutex 实现并发访问。读操作(Search)
// 使用读锁(可并发),写操作(Upsert、Delete)使用写锁(串行)。
package memory

import (
	"errors"
	"math"
	"sort"
	"sync"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// vector store 操作的哨兵错误。
var (
	ErrEmptyID         = errors.New("vector store: id cannot be empty")
	ErrEmptyVector     = errors.New("vector store: vector cannot be empty")
	ErrDimensionMismatch = errors.New("vector store: vector dimension does not match embedding provider")
)

// InMemoryVectorStore 是 goroutine 安全的、内存版的 VectorStore 实现。
//
// 它使用普通 map 加 RWMutex 实现并发访问。适合开发与测试;
// 生产环境请通过 Qdrant、Weaviate 或 pgvector 实现 VectorStore。
type InMemoryVectorStore struct {
	mu       sync.RWMutex
	vectors  map[string][]float32    // id → embedding vector(副本)
	metadata map[string]map[string]any // id → metadata 副本

	// embedProvider 是可选的 provider,用于在 Upsert 时校验 vector 维度。
	// 为 nil 时跳过维度校验。
	embedProvider llm.EmbeddingProvider
}

// NewInMemoryVectorStore 创建一个新的 InMemoryVectorStore。
//
// 可选传入 EmbeddingProvider 以在 Upsert 时启用维度校验。
// 若 provider 为 nil,Upsert 接受任意长度的 vector。
func NewInMemoryVectorStore(provider llm.EmbeddingProvider) *InMemoryVectorStore {
	return &InMemoryVectorStore{
		vectors:       make(map[string][]float32),
		metadata:      make(map[string]map[string]any),
		embedProvider: provider,
	}
}

// Upsert 存储或更新一个 vector 及其关联 metadata。
// 若相同 id 的 vector 已存在,会被覆盖。
// 配置了 provider 时,vector 长度必须匹配该 provider 的 Dimensions();
// 否则任意长度都被接受。
func (s *InMemoryVectorStore) Upsert(id string, vector []float32, metadata map[string]any) error {
	if id == "" {
		return ErrEmptyID
	}
	if len(vector) == 0 {
		return ErrEmptyVector
	}

	// 配置了 provider 时校验维度。
	if s.embedProvider != nil {
		expected := s.embedProvider.Dimensions()
		if len(vector) != expected {
			return ErrDimensionMismatch
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 深拷贝 vector,防止调用方修改。
	vectorCopy := make([]float32, len(vector))
	copy(vectorCopy, vector)

	// 深拷贝 metadata map。
	metaCopy := make(map[string]any, len(metadata))
	for k, v := range metadata {
		metaCopy[k] = v
	}

	s.vectors[id] = vectorCopy
	s.metadata[id] = metaCopy
	return nil
}

// Search 使用 cosine similarity 查找与 query 最相似的 top-K 个 vector。
// 返回结果按 score 降序(最高分在前)排序。
// store 为空或无匹配时返回空 slice(而非错误)。
func (s *InMemoryVectorStore) Search(query []float32, topK int) ([]SearchResult, error) {
	if len(query) == 0 {
		return nil, ErrEmptyVector
	}
	if topK <= 0 {
		topK = 10 // 合理的默认值
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// 收集所有 similarity score。
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

	// 按 score 降序排序。
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// 取 top-K(若结果更少则全部返回)。
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

// Delete 按 id 移除一个 vector 及其 metadata。
// 若 id 不存在则为 no-op(不返回错误)。
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

// Len 返回当前存储的 vector 数量。
// 主要用于测试与 metrics。
func (s *InMemoryVectorStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.vectors)
}

// Clear 移除 store 中的所有 vector 与 metadata。
func (s *InMemoryVectorStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.vectors = make(map[string][]float32)
	s.metadata = make(map[string]map[string]any)
}

// NormalizeVector 将 vector 缩放为单位长度(L2 norm = 1.0)。
// 对单位向量而言 cosine similarity 等价于点积,
// 因此在存储时归一化可加速重复比较。
// 若 vector 模长为零,原样返回。
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
