package main

import (
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/orchestrator"
)

func TestIsAllowedScope(t *testing.T) {
	cases := []struct {
		scope   string
		allowed []string
		want    bool
	}{
		{"", []string{"read_only"}, true},
		{"read_only", []string{"read_only", "standard"}, true},
		{"unrestricted", []string{"read_only", "standard"}, false},
		{"anything", nil, true},
	}
	for _, c := range cases {
		if got := isAllowedScope(c.scope, c.allowed); got != c.want {
			t.Errorf("isAllowedScope(%q, %v) = %v, want %v", c.scope, c.allowed, got, c.want)
		}
	}
}

func TestResolveAllowedTools(t *testing.T) {
	// When request provides explicit tools, they win.
	reqTools := []string{"run_shell"}
	got := resolveAllowedTools(reqTools, "any")
	if len(got) != 1 || got[0] != "run_shell" {
		t.Errorf("resolveAllowedTools explicit = %v, want [run_shell]", got)
	}

	// When request provides nothing and agentID is empty, result is nil.
	got = resolveAllowedTools(nil, "")
	if len(got) != 0 {
		t.Errorf("resolveAllowedTools(nil, \"\") = %v, want empty", got)
	}
}

func TestEnrichAgentSpecAllowedTools(t *testing.T) {
	// Spec with explicit AllowedTools is unchanged.
	specs := []orchestrator.AgentSpec{
		{AgentID: "explicit", AllowedTools: []string{"run_shell"}},
	}
	enriched := enrichAgentSpecAllowedTools(specs)
	if len(enriched) != 1 || enriched[0].AllowedTools[0] != "run_shell" {
		t.Fatalf("explicit spec should be preserved")
	}

	// For an unknown agent with no tools, AllowedTools stays empty and
	// contract is untouched.
	specs = []orchestrator.AgentSpec{
		{AgentID: "unknown_agent", Input: "test"},
	}
	enriched = enrichAgentSpecAllowedTools(specs)
	if len(enriched[0].AllowedTools) != 0 {
		t.Errorf("unknown agent should keep empty AllowedTools, got %v", enriched[0].AllowedTools)
	}
	if enriched[0].Contract != nil {
		t.Errorf("unknown agent contract should stay nil, got %+v", enriched[0].Contract)
	}
}

func TestDefaultContractIncludesDefaultScope(t *testing.T) {
	c := harness.DefaultContract("hello")
	if c.Scope != "." {
		t.Errorf("DefaultContract Scope = %q, want '.'", c.Scope)
	}
}
