package config

import (
	"os"
	"testing"
)

func TestLoadLLMProviderConfigFromJSON(t *testing.T) {
	t.Setenv("LLM_PROVIDERS", `[{"name":"deepseek","type":"openai","endpoint":"https://api.deepseek.com/v1","api_key":"sk-xxx"}]`)
	cfg := &Config{}
	cfg.LoadLLMProviderConfig()

	if len(cfg.LLMProviders) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.LLMProviders))
	}
	p := cfg.LLMProviders[0]
	if p.Name != "deepseek" || p.Type != "openai" || p.Endpoint != "https://api.deepseek.com/v1" || p.APIKey != "sk-xxx" {
		t.Fatalf("unexpected provider: %+v", p)
	}
}

func TestLoadLLMProviderConfigFallbackDefault(t *testing.T) {
	// 确保 LLM_PROVIDERS 未设置
	os.Unsetenv("LLM_PROVIDERS")
	cfg := &Config{
		LLMEndpoint: "https://api.deepseek.com/v1",
		LLMAPIKey:   "sk-default",
	}
	cfg.LoadLLMProviderConfig()

	if len(cfg.LLMProviders) != 1 {
		t.Fatalf("expected 1 fallback provider, got %d", len(cfg.LLMProviders))
	}
	p := cfg.LLMProviders[0]
	if p.Name != "default" || p.Type != "openai" || p.Endpoint != "https://api.deepseek.com/v1" || p.APIKey != "sk-default" {
		t.Fatalf("unexpected fallback provider: %+v", p)
	}
}

func TestLoadLLMProviderConfigNormalizeDeepSeek(t *testing.T) {
	t.Setenv("LLM_PROVIDERS", `[{"name":"ds","type":"deepseek","endpoint":"https://api.deepseek.com/v1","api_key":"k"}]`)
	cfg := &Config{}
	cfg.LoadLLMProviderConfig()

	if len(cfg.LLMProviders) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.LLMProviders))
	}
	if cfg.LLMProviders[0].Type != "openai" {
		t.Fatalf("expected deepseek normalized to openai, got %q", cfg.LLMProviders[0].Type)
	}
}

func TestLoadModelTierMapping(t *testing.T) {
	t.Setenv("MODEL_TIER_claude-opus-*", "premium")
	t.Setenv("MODEL_TIER_deepseek-*", "efficient")

	cfg := &Config{}
	cfg.LoadModelTierMapping()

	if len(cfg.ModelTierMapping) != 2 {
		t.Fatalf("expected 2 tier mappings, got %d", len(cfg.ModelTierMapping))
	}
	if cfg.ModelTierMapping["claude-opus-*"] != "premium" {
		t.Fatalf("expected claude-opus-*=premium, got %q", cfg.ModelTierMapping["claude-opus-*"])
	}
	if cfg.ModelTierMapping["deepseek-*"] != "efficient" {
		t.Fatalf("expected deepseek-*=efficient, got %q", cfg.ModelTierMapping["deepseek-*"])
	}
}
