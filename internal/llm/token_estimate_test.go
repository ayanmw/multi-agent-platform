package llm

import (
	"strings"
	"testing"
)

func TestEstimateTokenCount(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		min  int
		max  int
	}{
		{
			name: "empty content",
			msg:  Message{Role: "system", Content: ""},
			min:  0,
			max:  20,
		},
		{
			name: "short system prompt",
			msg:  Message{Role: "system", Content: "You are a helpful assistant."},
			min:  1,
			max:  20,
		},
		{
			name: "reasoning counted",
			msg:  Message{Role: "assistant", Content: "ok", Reasoning: strings.Repeat("a", 40)},
			min:  10,
			max:  40,
		},
		{
			name: "tool message with metadata",
			msg:  Message{Role: "tool", Content: "result", ToolCallID: "call_abc"},
			min:  1,
			max:  20,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := EstimateTokenCount(tc.msg)
			if n < tc.min || n > tc.max {
				t.Fatalf("EstimateTokenCount=%d, want range [%d,%d]", n, tc.min, tc.max)
			}
		})
	}
}

func TestSumEstimatedTokens(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: strings.Repeat("a", 400)}, // ~100 + overhead
		{Role: "user", Content: strings.Repeat("b", 80)},    // ~20 + overhead
	}
	got := SumEstimatedTokens(msgs)
	want := EstimateTokenCount(msgs[0]) + EstimateTokenCount(msgs[1])
	if got != want {
		t.Fatalf("SumEstimatedTokens=%d, want %d", got, want)
	}
}

func TestBuildContextWindowSnapshot(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: strings.Repeat("x", 400)},
		{Role: "user", Content: "hello"},
	}
	snapshot := BuildContextWindowSnapshot("deepseek-v4-flash", 128000, msgs)

	if snapshot.Model != "deepseek-v4-flash" {
		t.Fatalf("model=%s, want deepseek-v4-flash", snapshot.Model)
	}
	if snapshot.MaxContextTokens != 128000 {
		t.Fatalf("max_context_tokens=%d, want 128000", snapshot.MaxContextTokens)
	}
	expectedTotal := SumEstimatedTokens(msgs)
	if snapshot.EstimatedTotalTokens != expectedTotal {
		t.Fatalf("estimated_total_tokens=%d, want %d", snapshot.EstimatedTotalTokens, expectedTotal)
	}
	if snapshot.EstimatedUsageRatio <= 0 {
		t.Fatalf("estimated_usage_ratio should be positive, got %f", snapshot.EstimatedUsageRatio)
	}
	if len(snapshot.Messages) != len(msgs) {
		t.Fatalf("len(messages)=%d, want %d", len(snapshot.Messages), len(msgs))
	}

	// 验证每条消息的 ratio 之和约为 1.0
	totalRatio := 0.0
	for _, m := range snapshot.Messages {
		totalRatio += m.UsageRatio
	}
	if totalRatio < 0.99 || totalRatio > 1.01 {
		t.Fatalf("sum usage_ratio=%f, want ~1.0", totalRatio)
	}
}

func TestBuildContextWindowSnapshotZeroMax(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "x"},
	}
	snapshot := BuildContextWindowSnapshot("unknown", 0, msgs)
	if snapshot.EstimatedUsageRatio != 0 {
		t.Fatalf("usage_ratio with zero max should be 0, got %f", snapshot.EstimatedUsageRatio)
	}
}

func TestBuildContextWindowSnapshotCapsRatio(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: strings.Repeat("x", 10000)},
	}
	snapshot := BuildContextWindowSnapshot("tiny", 10, msgs)
	if snapshot.EstimatedUsageRatio != 1.0 {
		t.Fatalf("usage_ratio should cap at 1.0, got %f", snapshot.EstimatedUsageRatio)
	}
}
