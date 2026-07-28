package main

import (
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// TestResolveEffectiveModel 验证模型优先级：
// spec.Model > DB agent.Model > cfg.LLMModel。
func TestResolveEffectiveModel(t *testing.T) {
	cfg := &config.Config{LLMModel: "cfg-model"}
	agent := &db.AgentRecord{Model: "agent-model"}

	cases := []struct {
		name     string
		spec     AgentRunSpec
		agent    *db.AgentRecord
		want     string
	}{
		{
			name: "spec model 优先级最高",
			spec: AgentRunSpec{Model: "spec-model"},
			agent: agent,
			want: "spec-model",
		},
		{
			name: "agent model 次之",
			spec: AgentRunSpec{},
			agent: agent,
			want: "agent-model",
		},
		{
			name: "空 agent 回退到 cfg",
			spec: AgentRunSpec{},
			agent: &db.AgentRecord{},
			want: "cfg-model",
		},
		{
			name: "nil agent 回退到 cfg",
			spec: AgentRunSpec{},
			agent: nil,
			want: "cfg-model",
		},
		{
			name: "spec model 空字符串时回退 agent",
			spec: AgentRunSpec{Model: ""},
			agent: agent,
			want: "agent-model",
		},
		{
			name: "agent model 空字符串时回退 cfg",
			spec: AgentRunSpec{},
			agent: &db.AgentRecord{Model: ""},
			want: "cfg-model",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveEffectiveModel(tc.spec, tc.agent, cfg)
			if got != tc.want {
				t.Errorf("resolveEffectiveModel() = %q, want %q", got, tc.want)
			}
		})
	}
}
