package memory

import (
	"fmt"
	"math"
	"sync"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// MemoryIndexerOptions 用于配置增量索引的行为。
type MemoryIndexerOptions struct {
	// DedupeThreshold 是判定为新记忆重复已有记忆并跳过的 cosine similarity 阈值,
	// 1.0 表示完全相同。生产环境典型取值范围为 0.92-0.98。
	DedupeThreshold float64

	// NormalizeBeforeStore 默认为 true;设为 false 则存储原始 vector。
	NormalizeBeforeStore bool
}

// MemoryIndexer 负责维护 vector store 的增量更新:每条新建记忆都会被 embed
// 并 upsert,近似重复的记忆会被跳过。
//
// 它取代了原先在启动阶段进行的 BuildVectorIndex 全量扫描。
type MemoryIndexer struct {
	store      VectorStore
	provider   llm.EmbeddingProvider
	opts       MemoryIndexerOptions
	mu         sync.Mutex
	indexedIDs map[string]bool
}

// NewMemoryIndexer 创建一个绑定到给定 store 与 provider 的 indexer。
func NewMemoryIndexer(store VectorStore, provider llm.EmbeddingProvider, opts MemoryIndexerOptions) *MemoryIndexer {
	if opts.DedupeThreshold <= 0 {
		opts.DedupeThreshold = 0.95
	}
	if opts.DedupeThreshold > 1 {
		opts.DedupeThreshold = 1
	}
	if !opts.NormalizeBeforeStore {
		// 默认为 true
		opts.NormalizeBeforeStore = true
	}
	return &MemoryIndexer{
		store:      store,
		provider:   provider,
		opts:       opts,
		indexedIDs: make(map[string]bool),
	}
}

// OnMemoryCreated 在一条记忆记录插入后被调用。它会 embed 内容,
// 对已有 index 进行去重,若足够新颖则 upsert 该 vector。
func (idx *MemoryIndexer) OnMemoryCreated(memoryID, content string) error {
	if idx.provider == nil || idx.store == nil {
		return nil
	}
	if memoryID == "" || content == "" {
		return fmt.Errorf("MemoryIndexer: memoryID and content required")
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.indexedIDs[memoryID] {
		return nil
	}

	vec, err := idx.provider.Embed(content)
	if err != nil {
		return fmt.Errorf("MemoryIndexer: embed %q: %w", memoryID, err)
	}

	if idx.opts.NormalizeBeforeStore {
		vec = normalizeVector(vec)
	}

	// Dedup:在已有 store 中检索 top-1。
	if idx.opts.DedupeThreshold <= 1.0 {
		results, err := idx.store.Search(vec, 1)
		if err == nil && len(results) > 0 && results[0].Score >= idx.opts.DedupeThreshold {
			// 重复 —— 不索引。
			idx.indexedIDs[memoryID] = true
			return nil
		}
	}

	if err := idx.store.Upsert(memoryID, vec, map[string]any{"content_preview": truncate(content, 200)}); err != nil {
		return fmt.Errorf("MemoryIndexer: upsert %q: %w", memoryID, err)
	}
	idx.indexedIDs[memoryID] = true
	return nil
}

// OnMemoryDeleted 从 index 中移除一条记忆的 vector。
func (idx *MemoryIndexer) OnMemoryDeleted(memoryID string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	delete(idx.indexedIDs, memoryID)
	if idx.store == nil {
		return nil
	}
	return idx.store.Delete(memoryID)
}

// normalizeVector 将 vector 缩放为单位长度(L2 norm = 1.0)。
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

// truncate 返回 s 的前 n 个字节(用于 metadata 预览的简单 helper)。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
