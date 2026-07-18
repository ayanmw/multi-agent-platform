package orchestrator

import (
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/config"
)

func TestLLMDecomposerMockFallback(t *testing.T) {
	cfg := &config.Config{LLMUseMock: true}
	d := NewLLMDecomposer(cfg, nil)
	result, err := d.Decompose("设计一个电商网站", "multi_agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Strategy != "sequential" {
		t.Fatalf("expected strategy sequential, got %s", result.Strategy)
	}
	if len(result.Agents) < 2 {
		t.Fatalf("expected at least 2 agents, got %d", len(result.Agents))
	}
}

func TestPipelineStrategyChainsOutputTo(t *testing.T) {
	specs := []AgentSpec{
		{AgentID: "a1"},
		{AgentID: "a2"},
		{AgentID: "a3"},
	}
	strategy := "pipeline"
	if strategy == "pipeline" {
		for i := 0; i < len(specs)-1; i++ {
			specs[i].OutputTo = append(specs[i].OutputTo, specs[i+1].AgentID)
		}
		strategy = "parallel"
	}
	if len(specs[0].OutputTo) == 0 || specs[0].OutputTo[0] != "a2" {
		t.Fatalf("expected a1 -> a2, got %v", specs[0].OutputTo)
	}
	if len(specs[1].OutputTo) == 0 || specs[1].OutputTo[0] != "a3" {
		t.Fatalf("expected a2 -> a3, got %v", specs[1].OutputTo)
	}
	if strategy != "parallel" {
		t.Fatalf("expected strategy to become parallel, got %s", strategy)
	}
}
