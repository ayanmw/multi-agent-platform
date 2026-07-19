// sqlite_vector_store_test.go —— 持久化 VectorStore 的测试。
//
// 所有测试都使用全新的临时文件 SQLite DB(不使用内存 :memory:,因为
// modernc.org/sqlite 的连接模型不同),以端到端地验证真实的持久化路径。
// 每个子测试通过 t.Cleanup 重置包级别的 db.DB。
//
// 测试布局(表驱动):
//
//   - TestSqliteVectorStoreUpsertSearchDelete —— 正常路径 + 边界情况
//     (空 query、topK 截断、维度不匹配、Delete 不存在的 id)
//   - TestSqliteVectorStorePersistence         —— Upsert -> reload(在同一文件上
//     新建 SqliteVectorStore)-> 数据仍可查询
//   - TestSqliteVectorStoreDimensionValidation —— provider 报告非零 Dimensions()
//     时 Upsert 拒绝错误维度
//   - TestSqliteVectorStoreLoadAllEmbeddings   —— Upsert 循环 + LoadAll 匹配
//   - TestSqliteVectorStoreReloadAndClear      —— 外部写入后 Reload;
//     Clear 仅清空内存

package memory

import (
	"path/filepath"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// freshSqliteDB 创建一个临时 SQLite 数据库,返回 live 的 *db.DB wrapper 底层
// 的 *sql 句柄。测试必须调用此函数以保持状态隔离 —— 包全局 db.DB 通过
// cleanup 在子测试之间被重置。
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

	// Upsert 三个 vector。
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

	// 用 alpha vector 搜索 —— alpha 应以 score 1.0 排在第一位。
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
	// 按 score 降序排序。
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted desc at i=%d: %f > %f", i, results[i].Score, results[i-1].Score)
		}
	}

	// 删除 alpha 并验证它已消失。
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

	// 删除未知 id —— 无错误,无 panic。
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
	// topK <= 0 应默认为 10 —— 通过命中空 store 来验证。
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
// 持久化:写入、丢弃 store、从磁盘重建
// ============================================================================

func TestSqliteVectorStorePersistence(t *testing.T) {
	path := freshSqliteDB(t)
	provider := llm.NewLocalEmbeddingProvider(4)

	// 第一个实例 —— Upsert 两个 vector。
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

	// 丢弃内存镜像;关闭 DB。重新打开并重建。
	store1.Clear()
	if got := store1.Len(); got != 0 {
		t.Errorf("after Clear, Len = %d, want 0", got)
	}
	// SQLite 行仍然存在 —— 通过 db helper 直接验证。
	rows, err := db.LoadAllMemoryEmbeddings(db.DB)
	if err != nil {
		t.Fatalf("LoadAllMemoryEmbeddings: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("persisted rows = %d, want 2", len(rows))
	}

	// 现在构造一个指向同一路径的新 store。它应该自动加载两条已持久化的
	// embedding。
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
// 维度校验
// ============================================================================

func TestSqliteVectorStoreDimensionValidation(t *testing.T) {
	freshSqliteDB(t)
	// provider 期望 4 维;我们尝试 upsert 一个 3 维 vector。
	provider := llm.NewLocalEmbeddingProvider(4)
	store, _ := NewSqliteVectorStore(db.DB, provider)

	if err := store.Upsert("a", []float32{1, 0, 0}, nil); err != ErrDimensionMismatch {
		t.Errorf("Upsert wrong-dim = %v, want ErrDimensionMismatch", err)
	}
	// 维度正确应成功。
	if err := store.Upsert("a", []float32{1, 0, 0, 0}, nil); err != nil {
		t.Errorf("Upsert correct-dim: %v", err)
	}
}

func TestSqliteVectorStoreNoProviderSkipsValidation(t *testing.T) {
	freshSqliteDB(t)
	store, _ := NewSqliteVectorStore(db.DB, nil)
	// 未设置 provider 时任意长度都应被接受。
	for _, v := range [][]float32{{1, 0}, {1, 0, 0}, {1, 0, 0, 0, 0}} {
		if err := store.Upsert("a", v, nil); err != nil {
			t.Errorf("Upsert(%v) with nil provider: %v", v, err)
		}
	}
}

// ============================================================================
// LoadAllMemoryEmbeddings 交互
// ============================================================================

func TestSqliteVectorStoreLoadAllEmbeddingsMatches(t *testing.T) {
	freshSqliteDB(t)
	provider := llm.NewLocalEmbeddingProvider(4)
	store, _ := NewSqliteVectorStore(db.DB, provider)

	// Upsert 一批 vector。
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
	// 通过 db helper 加载 —— 必须完全匹配。
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

	// 仅清空内存。
	store.Clear()
	if got := store.Len(); got != 0 {
		t.Errorf("Len after Clear = %d, want 0", got)
	}
	// SQLite 行保留。
	var count int
	if err := db.DB.QueryRow(`SELECT COUNT(*) FROM memory_embeddings`).Scan(&count); err != nil {
		t.Fatalf("count memory_embeddings: %v", err)
	}
	if count != 2 {
		t.Errorf("memory_embeddings rows after Clear = %d, want 2 (Clear must not touch DB)", count)
	}

	// Reload 从 SQLite 重建。
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
// 空 id 被拒绝
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
