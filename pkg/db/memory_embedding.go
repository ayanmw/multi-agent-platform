// memory_embedding.go — persistent storage for vector embeddings.
//
// # Design Rationale
//
// Embeddings live in a separate `memory_embeddings` table (added in the v16
// migration) rather than as a column on `memories`. The decoupling is intentional:
//
//  1. Batch vector I/O — we can SELECT only the (memory_id, embedding) columns
//     when warming the in-memory index at startup, without paying for the
//     other columns of the memories row.
//  2. Model rotation — when the embedding model changes, only rows matching
//     the old `model` need to be recomputed; the rest stay valid.
//  3. Schema isolation — adding vector columns later (e.g. quantization) does
//     not require touching the wide memories table.
//  4. ON DELETE CASCADE keeps the table consistent when a memory is removed.
//
// # BLOB Encoding
//
// The `embedding` column stores a little-endian []float32 serialization of
// length `dims * 4` bytes. Little-endian matches the native byte order of
// every modern CPU the platform targets (x86_64, arm64, riscv64). Encoding
// and decoding use encoding/binary.LittleEndian via the encodeEmbedding /
// decodeEmbedding helpers below — never hand-roll byte manipulation.
//
// A row also carries the embedding model name (e.g. "local-fnv") and the
// dimensionality so the table is self-describing and model rotation can be
// detected without external metadata.
package db

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
)

// MemoryEmbeddingRow is the in-memory representation of one row from the
// memory_embeddings table. Embedding is decoded back to []float32 for
// callers (cosine similarity etc.).
type MemoryEmbeddingRow struct {
	MemoryID  string
	Embedding []float32
	Model     string
	Dims      int
}

// encodeEmbedding serializes a []float32 into a little-endian byte slice.
//
// The length of the returned slice is 4 * len(vec). Each float32 is laid out
// as its IEEE-754 binary representation in little-endian byte order.
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

// decodeEmbedding reverses encodeEmbedding: takes a BLOB of length 4*dims
// and returns the corresponding []float32.
//
// We trust the stored dims column (set at insertion time) rather than
// inferring from len(blob)/4 so callers always know what to expect.
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

// InsertOrReplaceMemoryEmbedding writes a single embedding row, replacing any
// existing row with the same memory_id (upsert). The caller is responsible
// for encoding the embedding via encodeEmbedding before calling.
//
// Uses an UPSERT (INSERT ... ON CONFLICT) so we don't depend on UNIQUE
// triggers or pre-delete-then-insert races.
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

// LoadAllMemoryEmbeddings returns every row in memory_embeddings, with the
// BLOB decoded back into []float32. Used at startup to warm the in-memory
// vector index of SqliteVectorStore.
//
// Results are not ordered — callers that need a stable order should sort.
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
			// Skip corrupt rows rather than aborting the whole load — a
			// mismatched dims/blob is a soft error at startup and the caller
			// (SqliteVectorStore.Reload) can still serve the surviving rows.
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

// LoadMemoryEmbeddingsByModel returns rows whose `model` column matches the
// given provider name. Useful for "find all embeddings that were produced by
// the old model" workflows when rotating the embedding provider.
//
// An empty model returns zero rows (no row has an empty model — enforced by
// InsertOrReplaceMemoryEmbedding).
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

// DeleteMemoryEmbedding removes the embedding row for the given memory ID.
// The ON DELETE CASCADE foreign key would also clear the row if the parent
// memory row were deleted; this helper exists for callers that want to drop
// just the embedding (e.g. before recomputing with a new model) without
// touching the memory itself.
//
// No-op if the row does not exist (DELETE does not error on missing rows).
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
