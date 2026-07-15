package llm

import "testing"

func TestEstimateModelContextWindow(t *testing.T) {
	r := NewModelRegistry()
	r.Register(&ModelProfile{
		Name:             "deepseek-v4-flash",
		Provider:         "deepseek",
		MaxContextWindow: 128000,
	})

	if got := EstimateModelContextWindow(r, "deepseek-v4-flash"); got != 128000 {
		t.Fatalf("got %d, want 128000", got)
	}
	if got := EstimateModelContextWindow(r, "unknown"); got != 64000 {
		t.Fatalf("got %d, want 64000 for unknown model", got)
	}
	if got := EstimateModelContextWindow(nil, "deepseek-v4-flash"); got != 64000 {
		t.Fatalf("got %d, want 64000 for nil registry", got)
	}
}
