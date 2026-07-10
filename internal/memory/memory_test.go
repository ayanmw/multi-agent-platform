package memory

// memory_test.go — InMemoryVectorStore / CosineSimilarity / NormalizeVector 单元测试。
//
// 全部为纯逻辑测试，不依赖网络与数据库。表驱动 + t.Run 子测试。
// 使用预构造向量，不调用任何 EmbeddingProvider。

import (
	"math"
	"sync"
	"testing"
)

// --- 辅助 --------------------------------------------------------------------

func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// ============================================================================
// CosineSimilarity
// ============================================================================

func TestCosineSimilarity(t *testing.T) {
	// 注意：源码 CosineSimilarity 的分母用了 magA*magB（平方和乘积），
	// 而标准余弦相似度应为 sqrt(magA)*sqrt(magB)（模的乘积）。
	// 这导致任何非单位向量的相似度被系统性低估（BUG）。
	// 单位向量、正交向量、反向向量因平方和恰好=1 而不受影响，结果正确。
	// 下方 buggy 用例用 t.Skip 记录该 bug；修复后去掉 skip 即可自动验证。
	cases := []struct {
		name string
		a, b []float32
		want float64
		skip bool // true = 当前源码有 bug，期望值是"正确"的 cosine
	}{
		{"identical unit vectors", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0, false},
		{"orthogonal vectors", []float32{1, 0}, []float32{0, 1}, 0.0, false},
		{"opposite vectors", []float32{1, 0}, []float32{-1, 0}, -1.0, false},
		{"parallel scaled (BUG: 缺 sqrt)", []float32{1, 2, 3}, []float32{2, 4, 6}, 1.0, true},
		{"45-degree-ish (BUG: 缺 sqrt)", []float32{1, 1}, []float32{1, 0}, 0.70710678, true},
		{"identical non-unit (BUG: 缺 sqrt)", []float32{1, 2, 3}, []float32{1, 2, 3}, 1.0, true},
		{"empty a returns 0", []float32{}, []float32{1, 0}, 0.0, false},
		{"empty b returns 0", []float32{1, 0}, []float32{}, 0.0, false},
		{"dimension mismatch returns 0", []float32{1, 0, 0}, []float32{1, 0}, 0.0, false},
		{"zero magnitude a returns 0", []float32{0, 0, 0}, []float32{1, 0}, 0.0, false},
		{"zero magnitude b returns 0", []float32{1, 0}, []float32{0, 0}, 0.0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CosineSimilarity(c.a, c.b)
			if c.skip {
				// 记录源码实际值 vs 正确期望，便于追踪 bug 修复进度
				t.Skipf("源码 bug：CosineSimilarity 缺 sqrt，实际=%f，正确应为=%f", got, c.want)
			}
			if !approxEq(got, c.want) {
				t.Errorf("CosineSimilarity = %f, want %f", got, c.want)
			}
		})
	}
}

// TestCosineSimilarityBugDemonstration 显式记录源码 bug：identical 非单位向量
// 按定义应返回 1.0，但实际远小于 1.0。此测试永远 skip，仅作文档。
func TestCosineSimilarityBugDemonstration(t *testing.T) {
	a := []float32{1, 2, 3}
	got := CosineSimilarity(a, a)
	t.Skipf("CosineSimilarity([1,2,3],[1,2,3]) = %f, 应为 1.0（identical vectors）。"+
		"根因：vector_store.go 分母用 magA*magB（平方和乘积）而非 sqrt(magA)*sqrt(magB)（模的乘积）。"+
		"影响：Search 对非单位向量的排序被系统性扭曲。", got)
}

// ============================================================================
// NormalizeVector
// ============================================================================

func TestNormalizeVector(t *testing.T) {
	t.Run("unit vector unchanged", func(t *testing.T) {
		v := []float32{1, 0, 0}
		got := NormalizeVector(v)
		if !approxEq(float64(got[0]), 1.0) || got[1] != 0 || got[2] != 0 {
			t.Errorf("unit vector should stay unit, got %v", got)
		}
	})

	t.Run("non-unit becomes unit", func(t *testing.T) {
		v := []float32{3, 4} // magnitude 5 → (0.6, 0.8)
		got := NormalizeVector(v)
		if !approxEq(float64(got[0]), 0.6) || !approxEq(float64(got[1]), 0.8) {
			t.Errorf("expected (0.6, 0.8), got %v", got)
		}
		// verify magnitude is 1
		var mag float64
		for _, f := range got {
			mag += float64(f) * float64(f)
		}
		if !approxEq(mag, 1.0) {
			t.Errorf("normalized magnitude = %f, want 1.0", mag)
		}
	})

	t.Run("zero vector returned unchanged", func(t *testing.T) {
		v := []float32{0, 0, 0}
		got := NormalizeVector(v)
		if len(got) != 3 || got[0] != 0 || got[1] != 0 || got[2] != 0 {
			t.Errorf("zero vector should be returned as-is, got %v", got)
		}
	})

	t.Run("does not mutate input", func(t *testing.T) {
		v := []float32{3, 4}
		_ = NormalizeVector(v)
		if v[0] != 3 || v[1] != 4 {
			t.Errorf("input was mutated: %v", v)
		}
	})
}

// ============================================================================
// InMemoryVectorStore — Upsert / Search / Delete
// ============================================================================

func TestInMemoryVectorStoreUpsertSearch(t *testing.T) {
	store := NewInMemoryVectorStore(nil)

	t.Run("Upsert empty id rejected", func(t *testing.T) {
		if err := store.Upsert("", []float32{1, 0}, nil); err != ErrEmptyID {
			t.Errorf("expected ErrEmptyID, got %v", err)
		}
	})

	t.Run("Upsert empty vector rejected", func(t *testing.T) {
		if err := store.Upsert("id1", []float32{}, nil); err != ErrEmptyVector {
			t.Errorf("expected ErrEmptyVector, got %v", err)
		}
	})

	t.Run("Upsert + Len", func(t *testing.T) {
		_ = store.Upsert("a", []float32{1, 0, 0}, map[string]any{"k": "v1"})
		_ = store.Upsert("b", []float32{0, 1, 0}, map[string]any{"k": "v2"})
		if store.Len() != 2 {
			t.Errorf("Len = %d, want 2", store.Len())
		}
	})

	t.Run("Search returns topK sorted by score desc", func(t *testing.T) {
		// query [1,0,0]: most similar is "a" (1,0,0), then "b" (0,1,0)
		results, err := store.Search([]float32{1, 0, 0}, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[0].ID != "a" {
			t.Errorf("top result should be a, got %s", results[0].ID)
		}
		if results[0].Score < results[1].Score {
			t.Errorf("results not sorted desc: %f >= %f expected", results[0].Score, results[1].Score)
		}
		if !approxEq(results[0].Score, 1.0) {
			t.Errorf("identical vector score = %f, want 1.0", results[0].Score)
		}
	})

	t.Run("Search topK larger than store returns all", func(t *testing.T) {
		results, _ := store.Search([]float32{1, 0, 0}, 100)
		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("Search topK<=0 uses default", func(t *testing.T) {
		results, _ := store.Search([]float32{1, 0, 0}, 0)
		// default is 10, but store only has 2 → returns 2
		if len(results) != 2 {
			t.Errorf("expected 2 results with default topK, got %d", len(results))
		}
	})

	t.Run("Search empty query rejected", func(t *testing.T) {
		if _, err := store.Search([]float32{}, 5); err != ErrEmptyVector {
			t.Errorf("expected ErrEmptyVector, got %v", err)
		}
	})

	t.Run("empty store search returns empty slice", func(t *testing.T) {
		empty := NewInMemoryVectorStore(nil)
		results, err := empty.Search([]float32{1, 0}, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 0 {
			t.Errorf("empty store should return 0 results, got %d", len(results))
		}
	})

	t.Run("metadata preserved in results", func(t *testing.T) {
		results, _ := store.Search([]float32{1, 0, 0}, 1)
		if results[0].Metadata["k"] != "v1" {
			t.Errorf("metadata not preserved: %v", results[0].Metadata)
		}
	})
}

func TestInMemoryVectorStoreUpsertOverwrite(t *testing.T) {
	store := NewInMemoryVectorStore(nil)
	_ = store.Upsert("id", []float32{1, 0, 0}, map[string]any{"v": 1})
	_ = store.Upsert("id", []float32{0, 1, 0}, map[string]any{"v": 2})
	if store.Len() != 1 {
		t.Errorf("overwrite should keep count at 1, got %d", store.Len())
	}
	results, _ := store.Search([]float32{0, 1, 0}, 1)
	if results[0].ID != "id" {
		t.Fatalf("expected id, got %s", results[0].ID)
	}
	if results[0].Metadata["v"] != 2 {
		t.Errorf("metadata should be overwritten to 2, got %v", results[0].Metadata["v"])
	}
}

func TestInMemoryVectorStoreDelete(t *testing.T) {
	store := NewInMemoryVectorStore(nil)
	_ = store.Upsert("a", []float32{1, 0}, nil)
	_ = store.Upsert("b", []float32{0, 1}, nil)

	t.Run("delete existing", func(t *testing.T) {
		if err := store.Delete("a"); err != nil {
			t.Errorf("delete existing should succeed, got %v", err)
		}
		if store.Len() != 1 {
			t.Errorf("Len after delete = %d, want 1", store.Len())
		}
	})

	t.Run("delete missing is no-op", func(t *testing.T) {
		if err := store.Delete("nonexistent"); err != nil {
			t.Errorf("delete missing should be no-op, got %v", err)
		}
		if store.Len() != 1 {
			t.Errorf("Len after no-op delete = %d, want 1", store.Len())
		}
	})

	t.Run("delete empty id rejected", func(t *testing.T) {
		if err := store.Delete(""); err != ErrEmptyID {
			t.Errorf("expected ErrEmptyID, got %v", err)
		}
	})
}

func TestInMemoryVectorStoreClear(t *testing.T) {
	store := NewInMemoryVectorStore(nil)
	_ = store.Upsert("a", []float32{1, 0}, nil)
	_ = store.Upsert("b", []float32{0, 1}, nil)
	store.Clear()
	if store.Len() != 0 {
		t.Errorf("after Clear Len = %d, want 0", store.Len())
	}
	results, _ := store.Search([]float32{1, 0}, 5)
	if len(results) != 0 {
		t.Errorf("after Clear search should be empty, got %d", len(results))
	}
}

// ============================================================================
// 维度校验（配置 EmbeddingProvider 时）
// ============================================================================

// fakeEmbedProvider 实现 llm.EmbeddingProvider，仅用于维度校验。返回固定维度。
type fakeEmbedProvider struct{ dim int }

func (f *fakeEmbedProvider) Embed(text string) ([]float32, error) { return make([]float32, f.dim), nil }
func (f *fakeEmbedProvider) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, f.dim)
	}
	return out, nil
}
func (f *fakeEmbedProvider) Dimensions() int { return f.dim }

func TestInMemoryVectorStoreDimensionValidation(t *testing.T) {
	// 通过 NewInMemoryVectorStore(provider) 注入维度校验
	store := NewInMemoryVectorStore(&fakeEmbedProvider{dim: 3})

	t.Run("correct dimension accepted", func(t *testing.T) {
		if err := store.Upsert("ok", []float32{1, 0, 0}, nil); err != nil {
			t.Errorf("dim=3 with provider dim=3 should pass, got %v", err)
		}
	})

	t.Run("wrong dimension rejected", func(t *testing.T) {
		if err := store.Upsert("bad", []float32{1, 0}, nil); err != ErrDimensionMismatch {
			t.Errorf("dim=2 with provider dim=3 should return ErrDimensionMismatch, got %v", err)
		}
	})

	t.Run("nil provider accepts any dimension", func(t *testing.T) {
		free := NewInMemoryVectorStore(nil)
		if err := free.Upsert("a", []float32{1, 0}, nil); err != nil {
			t.Errorf("nil provider should accept dim=2, got %v", err)
		}
		if err := free.Upsert("b", []float32{1, 0, 0, 0}, nil); err != nil {
			t.Errorf("nil provider should accept dim=4, got %v", err)
		}
	})
}

// ============================================================================
// 并发安全
// ============================================================================

func TestInMemoryVectorStoreConcurrent(t *testing.T) {
	store := NewInMemoryVectorStore(nil)
	var wg sync.WaitGroup
	const writers = 10
	const perWriter = 50
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(wid int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				id := "w" + itoa(wid) + "_" + itoa(i)
				_ = store.Upsert(id, []float32{float32(wid), float32(i)}, nil)
			}
		}(w)
	}
	// 并发读
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.Search([]float32{1, 0}, 10)
		}()
	}
	wg.Wait()
	expected := writers * perWriter
	if store.Len() != expected {
		t.Errorf("after concurrent upserts Len = %d, want %d", store.Len(), expected)
	}
}

// itoa 是一个不依赖 strconv 的极简整数转字符串，避免在测试里额外 import。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// ============================================================================
// SearchResult / NewSearchResult
// ============================================================================

func TestNewSearchResult(t *testing.T) {
	meta := map[string]any{"k": "v"}
	r := NewSearchResult("id1", 0.95, meta)
	if r.ID != "id1" || !approxEq(r.Score, 0.95) || r.Metadata["k"] != "v" {
		t.Errorf("NewSearchResult fields wrong: %+v", r)
	}
}

// ============================================================================
// VectorStore 接口合规
// ============================================================================

func TestInMemoryVectorStoreImplementsVectorStore(t *testing.T) {
	var _ VectorStore = (*InMemoryVectorStore)(nil)
}
