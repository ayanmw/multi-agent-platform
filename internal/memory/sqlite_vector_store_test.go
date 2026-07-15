// sqlite_vector_store_test.go — tests for the persistent VectorStore.
//
// All tests use a fresh temp-file SQLite DB (no in-memory :memory: because
// modernc.org/sqlite uses a separate connection model) so we exercise the
// real persistence path end-to-end. Each subtest resets the package-level
// db.DB via t.Cleanup.
//
// Test layout (table-driven):
//
//   - TestSqliteVectorStoreUpsertSearchDelete — happy path + edge cases
//     (empty query, topK clamp, dimension mismatch, missing id on Delete)
//   - TestSqliteVectorStorePersistence         — Upsert -> reload (new
//     SqliteVectorStore on the same file) -> data still queryable
//   - TestSqliteVectorStoreDimensionValidation — Upsert rejects wrong dims
//     when provider reports non-zero Dimensions()
//   - TestSqliteVectorStoreLoadAllEmbeddings   — Upsert loop + LoadAll matches
//   - TestSqliteVectorStoreReloadAndClear      — Reload after external write;
//     Clear empties in-memory only

package memory

import (
	"path/filepath"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// freshSqliteDB creates a temp SQLite database, returns the live *db.DB
// wrapper's underlying *sql handle. Tests must call this to keep state
// isolated — the package global db.DB is reset between subtests via cleanup.
func freshSqliteDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vec.db")
	if err := db.Init(path); err != nil {
		t.Fatalf("db.Init(%q): %v", path, err)
	}
	if db.DB == nil {
		t.Fatal("db.Init left DB nil")
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
	return path
}

// ============================================================================
// Upsert / Search / Delete
// ============================================================================

func TestSqliteVectorStoreUpsertSearchDelete(t *testing.T) {
	freshSqliteDB(t)
	provider := llm.NewLocalEmbeddingProvider(8)
	store, err := NewSqliteVectorStore(db.DB, provider)
	if err != nil {
		t.Fatalf("NewSqliteVectorStore: %v", err)
	}

	// Upsert three vectors.
	vecs := map[string][]float32{
		"alpha": {1, 0, 0, 0, 0, 0, 0, 0},
		"beta":  {0, 1, 0, 0, 0, 0, 0, 0},
		"gamma": {1, 1, 0, 0, 0, 0, 0, 0},
	}
	for id, v := range vecs {
		if err := store.Upsert(id, v, map[string]any{"label": id}); err != nil {
			t.Fatalf("Upsert(%q): %v", id, err)
		}
	}
	if got := store.Len(); got != 3 {
		t.Errorf("Len = %d, want 3", got)
	}

	// Search with the alpha vector — alpha should be the top hit with score 1.0.
	results, err := store.Search([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}
	if results[0].ID != "alpha" {
		t.Errorf("top hit = %q, want alpha", results[0].ID)
	}
	if !approxEq(results[0].Score, 1.0) {
		t.Errorf("alpha score = %f, want 1.0", results[0].Score)
	}
	// Sorted descending by score.
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted desc at i=%d: %f > %f", i, results[i].Score, results[i-1].Score)
		}
	}

	// Delete alpha and verify it's gone.
	if err := store.Delete("alpha"); err != nil {
		t.Fatalf("Delete(alpha): %v", err)
	}
	if got := store.Len(); got != 2 {
		t.Errorf("Len after delete = %d, want 2", got)
	}
	results, _ = store.Search([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 3)
	for _, r := range results {
		if r.ID == "alpha" {
			t.Errorf("alpha still present after Delete")
		}
	}

	// Delete unknown id — no error, no panic.
	if err := store.Delete("does_not_exist"); err != nil {
		t.Errorf("Delete(missing) returned %v, want nil", err)
	}
}

func TestSqliteVectorStoreSearchEmptyQuery(t *testing.T) {
	freshSqliteDB(t)
	provider := llm.NewLocalEmbeddingProvider(4)
	store, _ := NewSqliteVectorStore(db.DB, provider)
	if err := store.Upsert("a", []float32{1, 0, 0, 0}, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := store.Search(nil, 1); err != ErrEmptyVector {
		t.Errorf("empty query error = %v, want ErrEmptyVector", err)
	}
	if _, err := store.Search([]float32{}, 1); err != ErrEmptyVector {
		t.Errorf("empty slice query error = %v, want ErrEmptyVector", err)
	}
	// topK <= 0 should default to 10 — verify by hitting an empty store.
	if results, _ := store.Search([]float32{1, 0, 0, 0}, 0); len(results) == 0 {
		t.Error("non-empty store returned 0 results with topK=0 (should default to 10)")
	}
}

func TestSqliteVectorStoreSearchEmptyStore(t *testing.T) {
	freshSqliteDB(t)
	provider := llm.NewLocalEmbeddingProvider(4)
	store, _ := NewSqliteVectorStore(db.DB, provider)
	results, err := store.Search([]float32{1, 0, 0, 0}, 5)
	if err != nil {
		t.Errorf("Search on empty store returned error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("empty store returned %d results, want 0", len(results))
	}
}

// ============================================================================
// Persistence: write, drop store, rebuild from disk
// ============================================================================

func TestSqliteVectorStorePersistence(t *testing.T) {
	path := freshSqliteDB(t)
	provider := llm.NewLocalEmbeddingProvider(4)

	// First instance — Upsert two vectors.
	store1, err := NewSqliteVectorStore(db.DB, provider)
	if err != nil {
		t.Fatalf("NewSqliteVectorStore(1): %v", err)
	}
	if err := store1.Upsert("p1", []float32{1, 0, 0, 0}, map[string]any{"v": "p1"}); err != nil {
		t.Fatalf("Upsert(p1): %v", err)
	}
	if err := store1.Upsert("p2", []float32{0, 1, 0, 0}, map[string]any{"v": "p2"}); err != nil {
		t.Fatalf("Upsert(p2): %v", err)
	}

	// Drop the in-memory mirror; close the DB. Reopen and rebuild.
	store1.Clear()
	if got := store1.Len(); got != 0 {
		t.Errorf("after Clear, Len = %d, want 0", got)
	}
	// The SQLite rows still exist — verify directly via db helper.
	rows, err := db.LoadAllMemoryEmbeddings(db.DB)
	if err != nil {
		t.Fatalf("LoadAllMemoryEmbeddings: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("persisted rows = %d, want 2", len(rows))
	}

	// Now construct a fresh store pointing at the same path. It should
	// auto-load the two persisted embeddings.
	_ = db.Close()
	db.DB = nil
	if err := db.Init(path); err != nil {
		t.Fatalf("db.Init(second): %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
	store2, err := NewSqliteVectorStore(db.DB, provider)
	if err != nil {
		t.Fatalf("NewSqliteVectorStore(2): %v", err)
	}
	if got := store2.Len(); got != 2 {
		t.Errorf("reloaded store Len = %d, want 2", got)
	}
	results, err := store2.Search([]float32{1, 0, 0, 0}, 1)
	if err != nil {
		t.Fatalf("Search after reload: %v", err)
	}
	if len(results) != 1 || results[0].ID != "p1" {
		t.Errorf("top hit after reload = %+v, want p1", results)
	}
	if !approxEq(results[0].Score, 1.0) {
		t.Errorf("p1 score after reload = %f, want 1.0", results[0].Score)
	}
}

// ============================================================================
// Dimension validation
// ============================================================================

func TestSqliteVectorStoreDimensionValidation(t *testing.T) {
	freshSqliteDB(t)
	// Provider expects 4 dims; we try to upsert a 3-dim vector.
	provider := llm.NewLocalEmbeddingProvider(4)
	store, _ := NewSqliteVectorStore(db.DB, provider)

	if err := store.Upsert("a", []float32{1, 0, 0}, nil); err != ErrDimensionMismatch {
		t.Errorf("Upsert wrong-dim = %v, want ErrDimensionMismatch", err)
	}
	// Correct dims should succeed.
	if err := store.Upsert("a", []float32{1, 0, 0, 0}, nil); err != nil {
		t.Errorf("Upsert correct-dim: %v", err)
	}
}

func TestSqliteVectorStoreNoProviderSkipsValidation(t *testing.T) {
	freshSqliteDB(t)
	store, _ := NewSqliteVectorStore(db.DB, nil)
	// Any length should be accepted when no provider is set.
	for _, v := range [][]float32{{1, 0}, {1, 0, 0}, {1, 0, 0, 0, 0}} {
		if err := store.Upsert("a", v, nil); err != nil {
			t.Errorf("Upsert(%v) with nil provider: %v", v, err)
		}
	}
}

// ============================================================================
// LoadAllMemoryEmbeddings interaction
// ============================================================================

func TestSqliteVectorStoreLoadAllEmbeddingsMatches(t *testing.T) {
	freshSqliteDB(t)
	provider := llm.NewLocalEmbeddingProvider(4)
	store, _ := NewSqliteVectorStore(db.DB, provider)

	// Upsert a handful of vectors.
	expected := map[string][]float32{
		"a": {1, 0, 0, 0},
		"b": {0, 1, 0, 0},
		"c": {0, 0, 1, 0},
		"d": {0, 0, 0, 1},
	}
	for id, v := range expected {
		if err := store.Upsert(id, v, nil); err != nil {
			t.Fatalf("Upsert(%q): %v", id, err)
		}
	}
	// Load via the db helper — must match exactly.
	rows, err := db.LoadAllMemoryEmbeddings(db.DB)
	if err != nil {
		t.Fatalf("LoadAllMemoryEmbeddings: %v", err)
	}
	if len(rows) != len(expected) {
		t.Fatalf("LoadAll returned %d rows, want %d", len(rows), len(expected))
	}
	for _, r := range rows {
		want, ok := expected[r.MemoryID]
		if !ok {
			t.Errorf("unexpected row id %q", r.MemoryID)
			continue
		}
		if len(r.Embedding) != len(want) {
			t.Errorf("%s dims = %d, want %d", r.MemoryID, len(r.Embedding), len(want))
			continue
		}
		for i := range want {
			if !approxEq(float64(r.Embedding[i]), float64(want[i])) {
				t.Errorf("%s[%d] = %f, want %f", r.MemoryID, i, r.Embedding[i], want[i])
			}
		}
		if r.Dims != len(want) {
			t.Errorf("%s stored dims = %d, want %d", r.MemoryID, r.Dims, len(want))
		}
	}
}

// ============================================================================
// Reload + Clear
// ============================================================================

func TestSqliteVectorStoreReloadAndClear(t *testing.T) {
	freshSqliteDB(t)
	provider := llm.NewLocalEmbeddingProvider(4)
	store, _ := NewSqliteVectorStore(db.DB, provider)

	if err := store.Upsert("x", []float32{1, 0, 0, 0}, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := store.Upsert("y", []float32{0, 1, 0, 0}, nil); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Clear in-memory only.
	store.Clear()
	if got := store.Len(); got != 0 {
		t.Errorf("Len after Clear = %d, want 0", got)
	}
	// SQLite rows remain.
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM memory_embeddings`).Scan(&count); err != nil {
		t.Fatalf("count memory_embeddings: %v", err)
	}
	if count != 2 {
		t.Errorf("memory_embeddings rows after Clear = %d, want 2 (Clear must not touch DB)", count)
	}

	// Reload rebuilds from SQLite.
	if err := store.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := store.Len(); got != 2 {
		t.Errorf("Len after Reload = %d, want 2", got)
	}
	results, _ := store.Search([]float32{1, 0, 0, 0}, 2)
	if len(results) != 2 {
		t.Errorf("after Reload, search returned %d, want 2", len(results))
	}
}

// ============================================================================
// Empty id rejection
// ============================================================================

func TestSqliteVectorStoreEmptyIDRejected(t *testing.T) {
	freshSqliteDB(t)
	provider := llm.NewLocalEmbeddingProvider(2)
	store, _ := NewSqliteVectorStore(db.DB, provider)
	if err := store.Upsert("", []float32{1, 0}, nil); err != ErrEmptyID {
		t.Errorf("Upsert empty id = %v, want ErrEmptyID", err)
	}
	if err := store.Delete(""); err != ErrEmptyID {
		t.Errorf("Delete empty id = %v, want ErrEmptyID", err)
	}
}
