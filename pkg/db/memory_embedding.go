// memory_embedding.go —— 向量 embedding 的持久化存储。
//
// # 设计理由
//
// embedding 存放在独立的 `memory_embeddings` 表（v16 migration 新增）中，
// 而不是作为 `memories` 表上的一个列。这种解耦是有意为之：
//
//  1. 批量向量 I/O —— 启动时为内存索引预热，只需 SELECT 出
//     (memory_id, embedding) 两列，无需为 memories 行的其它列付出代价。
//  2. 模型轮换 —— embedding model 变更时，只需重新计算匹配旧 `model` 的行，
//     其余行保持有效。
//  3. Schema 隔离 —— 日后新增向量列（例如量化）时无需改动宽表 memories。
//  4. ON DELETE CASCADE 在 memory 被删除时保持本表一致。
//
// # BLOB 编码
//
// `embedding` 列存储长度为 `dims * 4` 字节的小端 []float32 序列化结果。
// 小端序与平台目标的所有现代 CPU（x86_64、arm64、riscv64）的原生字节序一致。
// 编码与解码通过下方 encodeEmbedding / decodeEmbedding 辅助函数使用
// encoding/binary.LittleEndian 完成——禁止手工拼字节。
//
// 一行还携带 embedding 的 model 名（例如 "local-fnv"）与维度 dims，因此本表
// 是自描述的，无需外部元数据即可检测 model 轮换。
package db

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
)

// MemoryEmbeddingRow 是 memory_embeddings 表一行的内存表示。
// Embedding 会被解码回 []float32 供调用方使用（cosine 相似度等）。
type MemoryEmbeddingRow struct {
	MemoryID  string
	Embedding []float32
	Model     string
	Dims      int
}

// encodeEmbedding 将 []float32 序列化为小端字节切片。
//
// 返回切片长度为 4 * len(vec)。每个 float32 以其 IEEE-754 小端字节序的二进制表示存放。
func encodeEmbedding(vec []float32) ([]byte, error) {
	if len(vec) == 0 {
		return nil, fmt.Errorf("encodeEmbedding: vector is empty")
	}
	buf := make([]byte, 4*len(vec))
	for i, v := range vec {
		u := math.Float32bits(v)
		binary.LittleEndian.PutUint32(buf[i*4:], u)
	}
	return buf, nil
}

// decodeEmbedding 是 encodeEmbedding 的逆操作：接收长度为 4*dims 的 BLOB，
// 返回对应的 []float32。
//
// 我们信任存储中的 dims 列（在插入时设定），而不是依据 len(blob)/4 推断，
// 这样调用方始终知道预期维度。
func decodeEmbedding(blob []byte, dims int) ([]float32, error) {
	if dims <= 0 {
		return nil, fmt.Errorf("decodeEmbedding: dims must be positive, got %d", dims)
	}
	expected := 4 * dims
	if len(blob) != expected {
		return nil, fmt.Errorf("decodeEmbedding: blob length %d != 4*dims %d", len(blob), expected)
	}
	out := make([]float32, dims)
	for i := 0; i < dims; i++ {
		bits := binary.LittleEndian.Uint32(blob[i*4:])
		out[i] = math.Float32frombits(bits)
	}
	return out, nil
}

// InsertOrReplaceMemoryEmbedding 写入单条 embedding 行，若该 memory_id 已存在则替换（upsert）。
// 调用方需在调用前通过 encodeEmbedding 完成 embedding 的编码。
//
// 使用 UPSERT（INSERT ... ON CONFLICT），这样既不依赖 UNIQUE 触发器，
// 也不存在"先删后插"带来的竞态。
func InsertOrReplaceMemoryEmbedding(db *sql.DB, memoryID string, emb []byte, model string, dims int) error {
	if db == nil {
		return fmt.Errorf("InsertOrReplaceMemoryEmbedding: db is nil")
	}
	if memoryID == "" {
		return fmt.Errorf("InsertOrReplaceMemoryEmbedding: memoryID is empty")
	}
	if len(emb) == 0 {
		return fmt.Errorf("InsertOrReplaceMemoryEmbedding: embedding blob is empty")
	}
	if model == "" {
		return fmt.Errorf("InsertOrReplaceMemoryEmbedding: model is empty")
	}
	if dims <= 0 {
		return fmt.Errorf("InsertOrReplaceMemoryEmbedding: dims must be positive, got %d", dims)
	}
	if want := 4 * dims; len(emb) != want {
		return fmt.Errorf("InsertOrReplaceMemoryEmbedding: blob length %d != 4*dims %d", len(emb), want)
	}

	_, err := db.Exec(
		`INSERT INTO memory_embeddings (memory_id, embedding, model, dims, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(memory_id) DO UPDATE SET
		   embedding = excluded.embedding,
		   model     = excluded.model,
		   dims      = excluded.dims,
		   updated_at = CURRENT_TIMESTAMP`,
		memoryID, emb, model, dims,
	)
	if err != nil {
		return fmt.Errorf("InsertOrReplaceMemoryEmbedding(%q): %w", memoryID, err)
	}
	return nil
}

// LoadAllMemoryEmbeddings 返回 memory_embeddings 中的全部行，BLOB 被解码回
// []float32。启动时用于为 SqliteVectorStore 的内存向量索引预热。
//
// 结果不保证顺序——需要稳定顺序的调用方应自行排序。
func LoadAllMemoryEmbeddings(db *sql.DB) ([]MemoryEmbeddingRow, error) {
	if db == nil {
		return nil, fmt.Errorf("LoadAllMemoryEmbeddings: db is nil")
	}
	rows, err := db.Query(
		`SELECT memory_id, embedding, model, dims FROM memory_embeddings`,
	)
	if err != nil {
		return nil, fmt.Errorf("LoadAllMemoryEmbeddings: query: %w", err)
	}
	defer rows.Close()

	var out []MemoryEmbeddingRow
	for rows.Next() {
		var (
			id   string
			blob []byte
			mdl  string
			dims int
		)
		if err := rows.Scan(&id, &blob, &mdl, &dims); err != nil {
			return nil, fmt.Errorf("LoadAllMemoryEmbeddings: scan: %w", err)
		}
		vec, err := decodeEmbedding(blob, dims)
		if err != nil {
			// 跳过损坏行而不是中止整个加载——dims/blob 不匹配在启动阶段
			// 属于软错误，调用方（SqliteVectorStore.Reload）仍可服务幸存行。
			continue
		}
		out = append(out, MemoryEmbeddingRow{
			MemoryID:  id,
			Embedding: vec,
			Model:     mdl,
			Dims:      dims,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("LoadAllMemoryEmbeddings: rows.Err: %w", err)
	}
	return out, nil
}

// LoadMemoryEmbeddingsByModel 返回 `model` 列匹配指定 provider 名称的行。
// 在轮换 embedding provider 时用于"找出所有由旧 model 产出的 embedding"这类流程。
//
// model 为空时返回零行（任何行的 model 都不为空，由 InsertOrReplaceMemoryEmbedding 保证）。
func LoadMemoryEmbeddingsByModel(db *sql.DB, model string) ([]MemoryEmbeddingRow, error) {
	if db == nil {
		return nil, fmt.Errorf("LoadMemoryEmbeddingsByModel: db is nil")
	}
	if model == "" {
		return nil, fmt.Errorf("LoadMemoryEmbeddingsByModel: model is empty")
	}
	rows, err := db.Query(
		`SELECT memory_id, embedding, model, dims FROM memory_embeddings WHERE model = ?`,
		model,
	)
	if err != nil {
		return nil, fmt.Errorf("LoadMemoryEmbeddingsByModel(%q): query: %w", model, err)
	}
	defer rows.Close()

	var out []MemoryEmbeddingRow
	for rows.Next() {
		var (
			id   string
			blob []byte
			mdl  string
			dims int
		)
		if err := rows.Scan(&id, &blob, &mdl, &dims); err != nil {
			return nil, fmt.Errorf("LoadMemoryEmbeddingsByModel(%q): scan: %w", model, err)
		}
		vec, err := decodeEmbedding(blob, dims)
		if err != nil {
			continue
		}
		out = append(out, MemoryEmbeddingRow{
			MemoryID:  id,
			Embedding: vec,
			Model:     mdl,
			Dims:      dims,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("LoadMemoryEmbeddingsByModel(%q): rows.Err: %w", model, err)
	}
	return out, nil
}

// DeleteMemoryEmbedding 删除指定 memory ID 对应的 embedding 行。
// 如果父 memory 行被删除，ON DELETE CASCADE 外键也会自动清理本行；
// 本辅助函数存在的意义是：调用方只想删除 embedding（例如用新 model 重新计算前）
// 而不想动 memory 本身时使用。
//
// 行不存在时为 no-op（DELETE 对缺失行不会报错）。
func DeleteMemoryEmbedding(db *sql.DB, memoryID string) error {
	if db == nil {
		return fmt.Errorf("DeleteMemoryEmbedding: db is nil")
	}
	if memoryID == "" {
		return fmt.Errorf("DeleteMemoryEmbedding: memoryID is empty")
	}
	_, err := db.Exec(`DELETE FROM memory_embeddings WHERE memory_id = ?`, memoryID)
	if err != nil {
		return fmt.Errorf("DeleteMemoryEmbedding(%q): %w", memoryID, err)
	}
	return nil
}
