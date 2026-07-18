package observability

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// HistogramCollector tracks latency samples in explicit buckets and supports
// Prometheus exposition without an external SDK.
type HistogramCollector struct {
	mu      sync.RWMutex
	buckets []float64 // upper bounds in ms
	counts  []uint64
	total   uint64
	sum     float64
}

// NewHistogramCollector creates a histogram. Buckets must be sorted ascending
// and are interpreted as milliseconds upper bounds.
func NewHistogramCollector(bucketsMS []float64) *HistogramCollector {
	b := make([]float64, len(bucketsMS))
	copy(b, bucketsMS)
	sort.Float64s(b)
	return &HistogramCollector{
		buckets: b,
		counts:  make([]uint64, len(b)),
	}
}

// Record adds a latency sample.
func (h *HistogramCollector) Record(d time.Duration) {
	ms := float64(d.Milliseconds())
	h.mu.Lock()
	defer h.mu.Unlock()
	h.total++
	h.sum += ms
	for i, upper := range h.buckets {
		if ms <= upper {
			h.counts[i]++
			return
		}
	}
	// If larger than all buckets, increment the last (overflow) bucket.
	if len(h.counts) > 0 {
		h.counts[len(h.counts)-1]++
	}
}

// Quantile returns an approximate latency in milliseconds for the given
// quantile (0-1). Uses linear interpolation within the bucket where the
// quantile falls, matching Prometheus histogram_quantile semantics.
func (h *HistogramCollector) Quantile(q float64) float64 {
	if q <= 0 || q > 1 || len(h.buckets) == 0 {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.total == 0 {
		return 0
	}
	// Prometheus 使用 rank = q * (total - 1)；当 total 较大时与 q*total 差异很小。
	target := q * float64(h.total-1)
	var cumulative uint64
	for i, c := range h.counts {
		if c == 0 {
			continue
		}
		prevCumulative := cumulative
		cumulative += c
		if float64(cumulative) > target {
			bucketStart := float64(0)
			if i > 0 {
				bucketStart = h.buckets[i-1]
			}
			bucketEnd := h.buckets[i]
			// 在该 bucket 内做线性插值。
			position := (target - float64(prevCumulative)) / float64(c)
			return bucketStart + (bucketEnd-bucketStart)*position
		}
	}
	return h.buckets[len(h.buckets)-1]
}

// PrometheusHistogram returns the bucket lines in Prometheus format.
func (h *HistogramCollector) PrometheusHistogram(name, help string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := fmt.Sprintf("# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	var cumulative uint64
	for i, upper := range h.buckets {
		cumulative += h.counts[i]
		out += fmt.Sprintf("%s_bucket{le=\"%.3f\"} %d\n", name, upper, cumulative)
	}
	out += fmt.Sprintf("%s_bucket{le=\"+Inf\"} %d\n", name, h.total)
	out += fmt.Sprintf("%s_sum %.3f\n", name, h.sum)
	out += fmt.Sprintf("%s_count %d\n", name, h.total)
	return out
}

func (h *HistogramCollector) Total() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.total
}
