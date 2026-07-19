// Package memory 提供 vector storage 接口与内存实现,
// 用于语义搜索与 RAG 管线。
//
// # 设计理由
//
// VectorStore 抽象了 embedding vector 的存储与检索。InMemoryVectorStore
// 提供了一个零依赖、便于开发的后端,适合原型开发与测试。在 Phase 6+ 可以
// 替换为生产级后端,如 Qdrant、Weaviate 或 pgvector。
//
// 相似度搜索使用 cosine similarity —— 归一化后:
//   score = 1.0 → 完全相同的 vector
//   score = 0.0 → 正交(无关联)
//   score = -1.0 → 相反(实践中罕见)

package memory

import "math"

// SearchResult 表示一次 vector similarity 搜索中的单条结果。
type SearchResult struct {
	// ID 是所存储 vector 的唯一标识。
	ID string

	// Score 是 cosine similarity 分数(0.0 到 1.0,越高越相似)。
	Score float64

	// Metadata 是存储 vector 时关联的任意键值数据。
	Metadata map[string]any
}

// VectorStore 定义了 embedding 存储与 similarity 搜索的接口。
//
// 实现必须是 goroutine 安全的 —— 并发的 Upsert/Search/Delete
// 调用不应破坏内部状态。
type VectorStore interface {
	// Upsert 存储或更新一个 vector 及其关联 metadata。
	// 若相同 id 的 vector 已存在,会被覆盖。
	// vector 长度必须匹配 provider 的 Dimensions()。
	Upsert(id string, vector []float32, metadata map[string]any) error

	// Search 使用 cosine similarity 查找与 query 最相似的 top-K 个 vector。
	// 返回结果按 score 降序排序。
	// store 为空或无匹配时返回空 slice(而非错误)。
	Search(query []float32, topK int) ([]SearchResult, error)

	// Delete 按 id 移除一个 vector。若 id 不存在则为 no-op。
	Delete(id string) error
}

// CosineSimilarity 计算两个 vector 之间的 cosine similarity。
// 若任一 vector 为空或模长为零,返回 0。
// 结果范围为 [-1, 1],但 embedding 通常非负,因此实际结果一般在 [0, 1]。
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

// NewSearchResult 用给定字段创建一个 SearchResult。
// 供 store 实现使用的便捷构造函数。
func NewSearchResult(id string, score float64, metadata map[string]any) SearchResult {
	return SearchResult{
		ID:       id,
		Score:    score,
		Metadata: metadata,
	}
}
