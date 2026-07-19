// sqlite_vector_store.go —— 基于 SQLite 的 VectorStore 实现。
//
// # 设计理由
//
// SqliteVectorStore 将每条 embedding 持久化到 `memory_embeddings` 表
// (在 v16 migration 中新增),同时在内存中维护镜像,用于快速的
// cosine similarity 搜索。这一组合(内存 map + SQLite)使我们能够:
//
//  1. 重启后恢复 —— 内存 index 在启动时通过 LoadAllMemoryEmbeddings
//     从磁盘重建,因此进程退出时不会"丢失"任何 vector。
//  2. 搜索保持快速 —— cosine similarity 仍在内存 map 上执行
//     (SQLite BLOB 仅在启动时读取一次,而非每次查询都读)。
//  3. 保持简单 —— 无需外部 vector DB(Qdrant/Weaviate/pgvector)。
//     当候选集较小(< ~10k vector)时该实现足够合适。当规模超出时,
//     VectorStore 接口允许我们替换为 ANN index 而无需改动调用方。
//
// # 线程安全
//
// 所有公开方法(Upsert / Search / Delete / Reload / Len / Clear)均由
// sync.RWMutex 保护。读(Search、Len)持读锁;写(Upsert、Delete、
// Reload、Clear)持写锁。
//
// # 失败模式
//
//   - 维度不匹配(provider.Dimensions() > 0 且 len(vec) != dims):
//     返回 ErrDimensionMismatch;不写入任何内容。
//   - 空 id 或空 vector:返回 ErrEmptyID / ErrEmptyVector。
//   - upsert/delete 期间的 SQLite 错误:通过 fmt.Errorf("...: %w", err) 包装。
//     内存状态会在 SQLite 写入之前被修改,因此短暂的写入失败会让内存
//     状态领先于磁盘;可通过 Reload() 从磁盘重新对齐。
//
// # 边界情况
//
//   - embedProvider 为 nil:跳过维度校验(与 InMemory store 行为一致)。
//   - provider.Dimensions() == 0:同上 —— 不校验。
//   - 对空表调用 Reload:会清空内存状态。
//   - Clear():仅清空内存状态(调用方若需要可另行从 SQLite 删除行)。
package memory

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// SqliteVectorStore 是基于 SQLite 的持久化 VectorStore。
//
// 它将每个 vector 镜像到以 id 为键的内存 map 中,同时在 SQLite 中以
// memory_id 为键存储一行。每次写入时两侧同步(先写内存,再写 SQLite)。
// 启动时 Reload() 读取整个 SQLite 表来预热内存 map。
type SqliteVectorStore struct {
	db            *sql.DB
	embedProvider llm.EmbeddingProvider

	mu       sync.RWMutex
	vectors  map[string][]float32      // id → embedding(深拷贝)
	metadata map[string]map[string]any // id → metadata(深拷贝)

	// model 是戳记到每条持久化行上的 embedding provider 名称。
	// 用于 LoadMemoryEmbeddingsByModel 以支持模型轮换工作流。当
	// provider 为 nil 时回退为 "unknown",以保持该列仍有值
	//(schema 要求 NOT NULL)。
	model string
}

// NewSqliteVectorStore 创建一个 SqliteVectorStore,并自动将 memory_embeddings
// 表中所有已存在的 embedding 加载到内存。
//
// embedProvider 是可选的 —— 非 nil 且 Dimensions() > 0 时,Upsert 会据此校验
// vector 长度。provider 的具体类型名会通过 fmt.Sprintf("%T", ...) 戳记到
// 每条持久化行上,以便将来模型轮换时能找到旧模型生成的行。
//
// 初始加载失败时返回错误;调用方应将其视为启动失败(该 DB 对 vector 搜索不可用)。
func NewSqliteVectorStore(sqlDB *sql.DB, provider llm.EmbeddingProvider) (*SqliteVectorStore, error) {
	if sqlDB == nil {
		return nil, fmt.Errorf("NewSqliteVectorStore: db is nil")
	}
	s := &SqliteVectorStore{
		db:            sqlDB,
		embedProvider: provider,
		vectors:       make(map[string][]float32),
		metadata:      make(map[string]map[string]any),
		model:         resolveProviderName(provider),
	}
	if err := s.Reload(); err != nil {
		return nil, fmt.Errorf("NewSqliteVectorStore: initial load: %w", err)
	}
	return s, nil
}

// resolveProviderName 返回 embedding provider 的稳定标识符。
// 可用时使用具体类型名(例如 "*llm.LocalEmbeddingProvider"),
// provider 为 nil 时回退为 "unknown" —— model 列是 NOT NULL。
func resolveProviderName(provider llm.EmbeddingProvider) string {
	if provider == nil {
		return "unknown"
	}
	return fmt.Sprintf("%T", provider)
}

// Reload 从 SQLite 表重建内存中的 vector 与 metadata map。
// 当外部进程(管理工具、批处理脚本)直接修改了 memory_embeddings,
// 或在模型轮换之后使用此方法。
//
// 可与搜索并发调用(reload 期间持写锁)。
func (s *SqliteVectorStore) Reload() error {
	rows, err := db.LoadAllMemoryEmbeddings(s.db)
	if err != nil {
		return fmt.Errorf("SqliteVectorStore.Reload: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 原地重置 map —— 保留同一 map header,使调用方中已有的引用在
	// Reload 之后仍然有效(在切换期间他们会短暂看到空数据,这对
	// 管理操作来说是可接受的)。
	s.vectors = make(map[string][]float32, len(rows))
	s.metadata = make(map[string]map[string]any, len(rows))

	for _, r := range rows {
		// 深拷贝 vector,防止调用方修改破坏我们的 index。
		vec := make([]float32, len(r.Embedding))
		copy(vec, r.Embedding)
		s.vectors[r.MemoryID] = vec
		// memory_embeddings 不持久化 metadata(只存 embedding/model/dims);
		// 这里加载一个空占位,使检查 len(metadata) 的调用方不会因为
		// 缺键而 panic。真正的 metadata 往返是未来的增强
		//(需要单独的列或 JSON blob)。
		s.metadata[r.MemoryID] = map[string]any{}
	}
	return nil
}

// Upsert 存储或更新一个 vector 及其关联 metadata。写入是双侧的:
// 先写内存 map,再通过 INSERT ... ON CONFLICT 写入 SQLite。
// 校验失败时返回 ErrEmptyID / ErrEmptyVector / ErrDimensionMismatch;
// SQLite 错误会带上上下文包装。
func (s *SqliteVectorStore) Upsert(id string, vector []float32, metadata map[string]any) error {
	if id == "" {
		return ErrEmptyID
	}
	if len(vector) == 0 {
		return ErrEmptyVector
	}
	// 维度校验 —— 与 InMemoryVectorStore 相同规则。
	if s.embedProvider != nil {
		expected := s.embedProvider.Dimensions()
		if expected > 0 && len(vector) != expected {
			return ErrDimensionMismatch
		}
	}
	// 深拷贝 vector 与 metadata,防止调用方修改破坏我们的数据。
	vecCopy := make([]float32, len(vector))
	copy(vecCopy, vector)
	metaCopy := make(map[string]any, len(metadata))
	for k, v := range metadata {
		metaCopy[k] = v
	}
	// 先更新内存状态,这样即使 SQLite 写入短暂失败,Search 也能看到新 vector。
	// 若持续失败,调用方可以调用 Reload() 从磁盘重新对齐。
	s.mu.Lock()
	s.vectors[id] = vecCopy
	s.metadata[id] = metaCopy
	s.mu.Unlock()

	// 持久化到 SQLite。编码为 little-endian float32 —— 与 pkg/db 的
	// encodeEmbedding 相同方案。我们在此复制编码(而非导入私有 helper),
	// 以保持本文件的 import 表干净,并使编码与其唯一调用方放在一起。
	blob := encodeFloat32LittleEndian(vecCopy)
	if err := db.InsertOrReplaceMemoryEmbedding(s.db, id, blob, s.model, len(vecCopy)); err != nil {
		return fmt.Errorf("SqliteVectorStore.Upsert(%q): persist: %w", id, err)
	}
	return nil
}

// Search 使用 cosine similarity 查找与 query 最相似的 top-K 个 vector。
// 返回结果按 score 降序排序。store 为空或 query 为空时返回空 slice
//(query 为空时返回 ErrEmptyVector,与 InMemoryVectorStore 行为一致)。
func (s *SqliteVectorStore) Search(query []float32, topK int) ([]SearchResult, error) {
	if len(query) == 0 {
		return nil, ErrEmptyVector
	}
	if topK <= 0 {
		topK = 10
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	type scored struct {
		id    string
		score float64
		meta  map[string]any
	}
	scores := make([]scored, 0, len(s.vectors))
	for id, vec := range s.vectors {
		score := CosineSimilarity(query, vec)
		scores = append(scores, scored{id: id, score: score, meta: s.metadata[id]})
	}
	if len(scores) == 0 {
		return []SearchResult{}, nil
	}
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

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

// Delete 按 id 从内存 map 与 SQLite 表中同时移除一个 vector 及其 metadata。
// 若两处都不存在该 id 则为 no-op。
func (s *SqliteVectorStore) Delete(id string) error {
	if id == "" {
		return ErrEmptyID
	}
	s.mu.Lock()
	delete(s.vectors, id)
	delete(s.metadata, id)
	s.mu.Unlock()

	if err := db.DeleteMemoryEmbedding(s.db, id); err != nil {
		return fmt.Errorf("SqliteVectorStore.Delete(%q): %w", id, err)
	}
	return nil
}

// Len 返回当前内存镜像中的 vector 数量。
// 在读锁下读取;可安全并发调用。
func (s *SqliteVectorStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.vectors)
}

// Clear 仅清空内存状态 —— SQLite 表保持不变。
// 用于测试场景,需要在不清空磁盘 embedding 的情况下重置内存 index。
// 若需要完全清空,调用方还应直接从 memory_embeddings 删除行。
func (s *SqliteVectorStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vectors = make(map[string][]float32)
	s.metadata = make(map[string]map[string]any)
}

// encodeFloat32LittleEndian 将 []float32 序列化为 little-endian 字节 slice,
// 长度为 4*len(v)。每个 float32 以其 IEEE-754 二进制表示按 little-endian
// 字节序排列。这与 pkg/db/memory_embedding.go 中的编码器保持一致,
// 以保证两条写入路径产生的磁盘 BLOB 相互兼容。
func encodeFloat32LittleEndian(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}
