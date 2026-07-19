package observability

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// HistogramCollector 在显式 bucket 中跟踪延迟样本，并支持
// 不依赖外部 SDK 的 Prometheus exposition 格式输出。
type HistogramCollector struct {
	mu      sync.RWMutex
	buckets []float64 // upper bounds in ms（以毫秒为单位的上界）
	counts  []uint64
	total   uint64
	sum     float64
}

// NewHistogramCollector 创建一个 histogram。buckets 必须按升序排序，
// 并被解释为以毫秒为单位的上界。
func NewHistogramCollector(bucketsMS []float64) *HistogramCollector {
	b := make([]float64, len(bucketsMS))
	copy(b, bucketsMS)
	sort.Float64s(b)
	return &HistogramCollector{
		buckets: b,
		counts:  make([]uint64, len(b)),
	}
}

// Record 添加一个延迟样本。
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
	// 若大于所有 bucket，则累加到最后一个（溢出）bucket。
	if len(h.counts) > 0 {
		h.counts[len(h.counts)-1]++
	}
}

// Quantile 返回给定分位数（0-1）对应的近似延迟（毫秒）。
// 在分位数落入的 bucket 内使用线性插值，与 Prometheus 的
// histogram_quantile 语义保持一致。
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

// PrometheusHistogram 返回 Prometheus 格式的 bucket 行。
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
