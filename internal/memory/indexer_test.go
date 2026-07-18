package memory

import (
	"math"
	"testing"
)

type mockEmbeddingProvider struct {
	dims int
}

func (m *mockEmbeddingProvider) Embed(text string) ([]float32, error) {
	vec := make([]float32, m.dims)
	for i := 0; i < m.dims && i < len(text); i++ {
		vec[i] = float32(text[i]) / 255.0
	}
	return vec, nil
}

func (m *mockEmbeddingProvider) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, _ := m.Embed(t)
		out[i] = v
	}
	return out, nil
}

func (m *mockEmbeddingProvider) Dimensions() int { return m.dims }

func TestIndexerUpsertAndDeduplicate(t *testing.T) {
	store := NewInMemoryVectorStore(&mockEmbeddingProvider{dims: 4})
	idx := NewMemoryIndexer(store, &mockEmbeddingProvider{dims: 4}, MemoryIndexerOptions{DedupeThreshold: 1.0})

	// First memory should be indexed.
	if err := idx.OnMemoryCreated("m1", "hello world"); err != nil {
		t.Fatalf("on created m1: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("len = %d, want 1", store.Len())
	}

	// Identical memory should be de-duplicated (cosine == 1.0 >= threshold).
	if err := idx.OnMemoryCreated("m2", "hello world"); err != nil {
		t.Fatalf("on created m2: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("after dedup len = %d, want 1", store.Len())
	}

	// Different memory should be indexed.
	if err := idx.OnMemoryCreated("m3", "completely different content"); err != nil {
		t.Fatalf("on created m3: %v", err)
	}
	if store.Len() != 2 {
		t.Fatalf("after different len = %d, want 2", store.Len())
	}
}

func TestIndexerOnMemoryDeleted(t *testing.T) {
	store := NewInMemoryVectorStore(&mockEmbeddingProvider{dims: 4})
	idx := NewMemoryIndexer(store, &mockEmbeddingProvider{dims: 4}, MemoryIndexerOptions{})

	if err := idx.OnMemoryCreated("m1", "hello"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := idx.OnMemoryDeleted("m1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if store.Len() != 0 {
		t.Fatalf("len = %d, want 0", store.Len())
	}
}

func TestIndexerNormalizeVector(t *testing.T) {
	v := []float32{3, 4}
	n := NormalizeVector(v)
	if len(n) != 2 {
		t.Fatalf("len = %d, want 2", len(n))
	}
	var sum float64
	for _, x := range n {
		sum += float64(x) * float64(x)
	}
	if math.Abs(sum-1.0) > 1e-6 {
		t.Fatalf("norm = %v, want 1", sum)
	}
}
