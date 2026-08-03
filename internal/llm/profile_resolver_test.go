// Package llm —— ProfileResolver 单测。
package llm

import (
	"testing"

	"github.com/ayanmw/multi-agent-platform/internal/config"
)

func TestProfileResolver_ResolveTier(t *testing.T) {
	cfg := &config.Config{
		ModelTierMapping: map[string]string{
			"claude-opus-*": "premium",
			"deepseek-*":    "efficient",
		},
	}
	r := NewProfileResolver(cfg)

	if got := r.ResolveTier("claude-opus-4-6"); got != "premium" {
		t.Fatalf("expected premium for claude-opus-4-6, got %q", got)
	}
	if got := r.ResolveTier("deepseek-v4-flash"); got != "efficient" {
		t.Fatalf("expected efficient for deepseek-v4-flash, got %q", got)
	}
	if got := r.ResolveTier("unknown-model"); got != "standard" {
		t.Fatalf("expected standard fallback, got %q", got)
	}
}

func TestProfileResolver_ResolveProviderForModel_FullName(t *testing.T) {
	cfg := &config.Config{
		LLMProviders: []config.LLMProviderConfig{
			{Name: "deepseek", Type: "openai", Endpoint: "https://api.deepseek.com/v1", APIKey: "sk-ds"},
		},
	}
	r := NewProfileResolver(cfg)
	pc := r.ResolveProviderForModel("deepseek/deepseek-v4-flash")

	if pc.Name != "openai" {
		t.Fatalf("expected provider type openai, got %q", pc.Name)
	}
	if pc.Endpoint != "https://api.deepseek.com/v1" {
		t.Fatalf("expected deepseek endpoint, got %q", pc.Endpoint)
	}
	if pc.APIKey != "sk-ds" {
		t.Fatalf("expected deepseek api key, got %q", pc.APIKey)
	}
	if pc.Model != "deepseek-v4-flash" {
		t.Fatalf("expected model deepseek-v4-flash, got %q", pc.Model)
	}
}

func TestProfileResolver_ResolveProviderForModel_ShortNameUsesModelConfig(t *testing.T) {
	cfg := &config.Config{
		Models: []config.ModelConfig{
			{Name: "claude-sonnet-4-6", Provider: "anthropic", Endpoint: "https://api.anthropic.com", APIKey: "sk-ant"},
		},
	}
	r := NewProfileResolver(cfg)
	pc := r.ResolveProviderForModel("claude-sonnet-4-6")

	if pc.Name != "anthropic" {
		t.Fatalf("expected provider anthropic, got %q", pc.Name)
	}
	if pc.Endpoint != "https://api.anthropic.com" {
		t.Fatalf("expected anthropic endpoint, got %q", pc.Endpoint)
	}
	if pc.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected model claude-sonnet-4-6, got %q", pc.Model)
	}
}

func TestProfileResolver_ResolveProviderForModel_FallbackToLegacy(t *testing.T) {
	cfg := &config.Config{
		LLMEndpoint: "https://api.deepseek.com/v1",
		LLMAPIKey:   "sk-fallback",
	}
	r := NewProfileResolver(cfg)
	pc := r.ResolveProviderForModel("unknown-model")

	if pc.Endpoint != "https://api.deepseek.com/v1" {
		t.Fatalf("expected legacy endpoint, got %q", pc.Endpoint)
	}
	if pc.APIKey != "sk-fallback" {
		t.Fatalf("expected legacy api key, got %q", pc.APIKey)
	}
}
