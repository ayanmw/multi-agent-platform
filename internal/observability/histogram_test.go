package observability

import (
	"testing"
	"time"
)

func TestHistogramQuantiles(t *testing.T) {
	h := NewHistogramCollector([]float64{1, 10, 100, 1000}) // ms buckets
	for i := 0; i < 100; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}
	p50 := h.Quantile(0.5)
	p95 := h.Quantile(0.95)
	if p50 < 45 || p50 > 55 {
		t.Fatalf("p50 out of range: %v", p50)
	}
	if p95 < 90 {
		t.Fatalf("p95 too low: %v", p95)
	}
}
