// sqlite_vector_store.go — SQLite-backed VectorStore implementation.
//
// # Design Rationale
//
// SqliteVectorStore persists every embedding to the `memory_embeddings` table
// (added in the v16 migration) while keeping an in-memory mirror for fast
// cosine-similarity search. The pair (memory map, SQLite) lets us:
//
//  1. Survive restarts — the in-memory index is rebuilt from disk at startup
//     via LoadAllMemoryEmbeddings, so no vector is "lost" when the process
//     exits.
//  2. Keep search fast — cosine similarity still runs over the in-memory map
//     (the SQLite BLOB is read once at startup, not per query).
//  3. Stay simple — no external vector DB (Qdrant/Weaviate/pgvector) required.
//     This implementation is appropriate when the candidate set is small
//     (< ~10k vectors). When we outgrow that, the VectorStore interface lets
//     us swap in an ANN index without touching callers.
//
// # Thread Safety
//
// All public methods (Upsert / Search / Delete / Reload / Len / Clear) are
// guarded by a sync.RWMutex. Reads (Search, Len) take the read lock; writes
// (Upsert, Delete, Reload, Clear) take the write lock.
//
// # Failure Modes
//
//   - Dimension mismatch (provider.Dimensions() > 0 and len(vec) != dims):
//     returned as ErrDimensionMismatch; nothing is written.
//   - Empty id or empty vector: ErrEmptyID / ErrEmptyVector.
//   - SQLite errors during upsert/delete: wrapped via fmt.Errorf("...: %w", err).
//     The in-memory state is mutated BEFORE the SQLite write so a transient
//     write failure leaves the in-memory state ahead of disk; Reload()
//     can be used to reconcile from disk.
//
// # Boundary Cases
//
//   - nil embedProvider: dimension validation is skipped (matches InMemory store).
//   - provider.Dimensions() == 0: same — no validation.
//   - Reload on an empty table: clears the in-memory state.
//   - Clear(): clears in-memory state only (callers can delete rows from
//     SQLite separately if desired).
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

// SqliteVectorStore is a persistent VectorStore backed by SQLite.
//
// It mirrors every vector in an in-memory map keyed by id, plus a SQLite row
// keyed by memory_id. The two are kept in sync on every write (memory first,
// then SQLite). On startup, Reload() reads the full SQLite table to warm
// the in-memory map.
type SqliteVectorStore struct {
	db            *sql.DB
	embedProvider llm.EmbeddingProvider

	mu       sync.RWMutex
	vectors  map[string][]float32      // id → embedding (deep-copied)
	metadata map[string]map[string]any // id → metadata (deep-copied)

	// model is the embedding provider name stamped onto every persisted row.
	// Used by LoadMemoryEmbeddingsByModel for model-rotation workflows. When
	// the provider is nil we fall back to "unknown" so the column is still
	// populated (the schema requires NOT NULL).
	model string
}

// NewSqliteVectorStore creates a SqliteVectorStore, automatically loading all
// existing embeddings from the memory_embeddings table into memory.
//
// The embedProvider is optional — when non-nil and reporting Dimensions() > 0,
// Upsert will validate vector lengths against it. The provider's concrete
// type name is stamped onto every persisted row (via fmt.Sprintf("%T", ...))
// so a future model rotation can find rows produced by the old model.
//
// Returns an error if the initial load fails; callers should treat this as
// a startup failure (the DB is unusable for vector search).
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

// resolveProviderName returns a stable identifier for the embedding provider.
// We use the concrete type name (e.g. "*llm.LocalEmbeddingProvider") when
// available and fall back to "unknown" when the provider is nil — the
// model column is NOT NULL.
func resolveProviderName(provider llm.EmbeddingProvider) string {
	if provider == nil {
		return "unknown"
	}
	return fmt.Sprintf("%T", provider)
}

// Reload rebuilds the in-memory vector and metadata maps from the SQLite
// table. Useful after an external process (admin tool, batch script) has
// modified memory_embeddings directly, or after a model rotation.
//
// Safe to call concurrently with searches (write lock is held during reload).
func (s *SqliteVectorStore) Reload() error {
	rows, err := db.LoadAllMemoryEmbeddings(s.db)
	if err != nil {
		return fmt.Errorf("SqliteVectorStore.Reload: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Reset maps in place — keeps the same map header so existing references
	// in callers remain valid after a Reload (they will just see empty data
	// briefly during the swap, which is acceptable for an admin operation).
	s.vectors = make(map[string][]float32, len(rows))
	s.metadata = make(map[string]map[string]any, len(rows))

	for _, r := range rows {
		// Deep-copy the vector so caller mutation cannot corrupt our index.
		vec := make([]float32, len(r.Embedding))
		copy(vec, r.Embedding)
		s.vectors[r.MemoryID] = vec
		// memory_embeddings does not persist metadata (only embedding/model/dims);
		// load an empty placeholder so callers that inspect len(metadata) don't
		// panic on a missing key. Real metadata round-tripping is a future
		// enhancement (would need a separate column or JSON blob).
		s.metadata[r.MemoryID] = map[string]any{}
	}
	return nil
}

// Upsert stores or updates a vector with associated metadata. The write is
// double-sided: in-memory map first, then SQLite via INSERT ... ON CONFLICT.
// Returns ErrEmptyID / ErrEmptyVector / ErrDimensionMismatch on validation
// failures; SQLite errors are wrapped with context.
func (s *SqliteVectorStore) Upsert(id string, vector []float32, metadata map[string]any) error {
	if id == "" {
		return ErrEmptyID
	}
	if len(vector) == 0 {
		return ErrEmptyVector
	}
	// Dimension validation — same rule as InMemoryVectorStore.
	if s.embedProvider != nil {
		expected := s.embedProvider.Dimensions()
		if expected > 0 && len(vector) != expected {
			return ErrDimensionMismatch
		}
	}
	// Deep-copy the vector and metadata so caller mutation cannot corrupt us.
	vecCopy := make([]float32, len(vector))
	copy(vecCopy, vector)
	metaCopy := make(map[string]any, len(metadata))
	for k, v := range metadata {
		metaCopy[k] = v
	}
	// Update in-memory state first so Search sees the new vector even if the
	// SQLite write fails transiently. On persistent failure, the caller can
	// invoke Reload() to reconcile from disk.
	s.mu.Lock()
	s.vectors[id] = vecCopy
	s.metadata[id] = metaCopy
	s.mu.Unlock()

	// Persist to SQLite. Encoding is little-endian float32 — the same scheme
	// as pkg/db's encodeEmbedding. We replicate the encoding here (rather than
	// importing the private helper) so this file's import surface stays clean
	// and the encoding stays alongside its only caller.
	blob := encodeFloat32LittleEndian(vecCopy)
	if err := db.InsertOrReplaceMemoryEmbedding(s.db, id, blob, s.model, len(vecCopy)); err != nil {
		return fmt.Errorf("SqliteVectorStore.Upsert(%q): persist: %w", id, err)
	}
	return nil
}

// Search finds the top-K vectors most similar to the query vector using
// cosine similarity. Returns results sorted by score descending. An empty
// store or empty query returns an empty slice (or ErrEmptyVector for an
// empty query, matching InMemoryVectorStore's behaviour).
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

// Delete removes a vector and its metadata by id, from both the in-memory
// map and the SQLite table. No-op if the id does not exist in either.
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

// Len returns the number of vectors currently in the in-memory mirror.
// Reads under a read lock; safe to call concurrently.
func (s *SqliteVectorStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.vectors)
}

// Clear empties the in-memory state only — the SQLite table is left intact.
// Use this for tests that need to reset the in-memory index without losing
// the on-disk embeddings. Callers that want a full wipe should also delete
// rows from memory_embeddings directly.
func (s *SqliteVectorStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vectors = make(map[string][]float32)
	s.metadata = make(map[string]map[string]any)
}

// encodeFloat32LittleEndian serializes a []float32 into a little-endian byte
// slice of length 4*len(v). Each float32 is laid out as its IEEE-754 binary
// representation in little-endian byte order. This mirrors the encoder in
// pkg/db/memory_embedding.go so on-disk BLOBs are compatible across both
// write paths.
func encodeFloat32LittleEndian(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}
