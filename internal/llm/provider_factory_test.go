package llm

import (
	"strings"
	"testing"

	"github.com/ayanmw/multi-agent-platform/internal/config"
)

// TestNewProvider_OpenAI 验证工厂为 "openai" provider 创建 OpenAIProvider。
func TestNewProvider_OpenAI(t *testing.T) {
	p, err := NewProvider(ProviderConfig{
		Name:     "openai",
		Endpoint: "https://api.openai.com/v1",
		APIKey:   "sk-test",
		Model:    "gpt-4o",
	})
	if err != nil {
		t.Fatalf("NewProvider(openai) failed: %v", err)
	}
	if p.Name() != "openai" {
		t.Fatalf("expected provider name openai, got %q", p.Name())
	}
}

// TestNewProvider_DeepSeek 验证 "deepseek" 复用 OpenAIProvider 实现。
func TestNewProvider_DeepSeek(t *testing.T) {
	p, err := NewProvider(ProviderConfig{
		Name:     "deepseek",
		Endpoint: "https://api.deepseek.com/v1",
		APIKey:   "sk-test",
		Model:    "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatalf("NewProvider(deepseek) failed: %v", err)
	}
	if p.Name() != "deepseek" {
		t.Fatalf("expected provider name deepseek, got %q", p.Name())
	}
}

// TestNewProvider_Anthropic 验证工厂为 "anthropic" 创建 AnthropicProvider。
func TestNewProvider_Anthropic(t *testing.T) {
	p, err := NewProvider(ProviderConfig{
		Name:     "anthropic",
		Endpoint: "https://api.anthropic.com",
		APIKey:   "sk-ant-test",
		Model:    "claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("NewProvider(anthropic) failed: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Fatalf("expected provider name anthropic, got %q", p.Name())
	}
}

// TestNewProvider_Gemini 验证工厂为 "gemini" 创建 GeminiProvider。
func TestNewProvider_Gemini(t *testing.T) {
	p, err := NewProvider(ProviderConfig{
		Name:     "gemini",
		Endpoint: "https://generativelanguage.googleapis.com/v1beta",
		APIKey:   "AIza-test",
		Model:    "gemini-3.1-pro-preview",
	})
	if err != nil {
		t.Fatalf("NewProvider(gemini) failed: %v", err)
	}
	if p.Name() != "gemini" {
		t.Fatalf("expected provider name gemini, got %q", p.Name())
	}
}

// TestNewProvider_Mock 验证 mock provider 创建及默认 model 命名。
func TestNewProvider_Mock(t *testing.T) {
	p, err := NewProvider(ProviderConfig{
		Name:   "mock",
		CaseID: "code-gen",
	})
	if err != nil {
		t.Fatalf("NewProvider(mock) failed: %v", err)
	}
	if !strings.HasPrefix(p.Name(), "mock/") {
		t.Fatalf("expected mock provider name to start with mock/, got %q", p.Name())
	}
}

// TestNewProvider_UnknownDefaultsToOpenAI 验证未识别 provider 名回退到 OpenAI-compatible。
func TestNewProvider_UnknownDefaultsToOpenAI(t *testing.T) {
	p, err := NewProvider(ProviderConfig{
		Name:     "together",
		Endpoint: "https://api.together.xyz/v1",
		APIKey:   "sk-test",
		Model:    "llama-3-70b",
	})
	if err != nil {
		t.Fatalf("NewProvider(together) failed: %v", err)
	}
	if p.Name() != "together" {
		t.Fatalf("expected provider name together, got %q", p.Name())
	}
}

// TestCreateProviderFromConfig_MockMode 验证 ShouldMock 为 true 时走 MockProvider。
func TestCreateProviderFromConfig_MockMode(t *testing.T) {
	cfg := &config.Config{LLMUseMock: true}
	p, err := CreateProviderFromConfig(cfg, "deepseek-v4-flash", "dialogue")
	if err != nil {
		t.Fatalf("CreateProviderFromConfig mock mode failed: %v", err)
	}
	if !strings.HasPrefix(p.Name(), "mock/") {
		t.Fatalf("expected mock provider in mock mode, got %q", p.Name())
	}
}

// TestCreateProviderFromConfig_RealModeUsesModelConfig 验证从 cfg.Models 匹配 model 配置。
func TestCreateProviderFromConfig_RealModeUsesModelConfig(t *testing.T) {
	cfg := &config.Config{
		LLMUseMock: false,
		Models: []config.ModelConfig{
			{Name: "claude-sonnet-4-6", Provider: "anthropic", Endpoint: "https://api.anthropic.com", APIKey: "sk-ant"},
		},
	}
	p, err := CreateProviderFromConfig(cfg, "claude-sonnet-4-6", "code-gen")
	if err != nil {
		t.Fatalf("CreateProviderFromConfig real mode failed: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Fatalf("expected anthropic provider, got %q", p.Name())
	}
}

// TestCreateProviderFromConfig_FallbackToDefaultFields 验证未匹配 model 时回退到全局 LLM 配置。
func TestCreateProviderFromConfig_FallbackToDefaultFields(t *testing.T) {
	cfg := &config.Config{
		LLMUseMock:  false,
		LLMEndpoint: "https://aicoding.dobest.com/v1",
		LLMAPIKey:   "sk-test",
		LLMModel:    "deepseek-v4-flash",
	}
	p, err := CreateProviderFromConfig(cfg, "unknown-model", "dialogue")
	if err != nil {
		t.Fatalf("CreateProviderFromConfig fallback failed: %v", err)
	}
	if p.Name() != "default" {
		t.Fatalf("expected default fallback provider, got %q", p.Name())
	}
}

// TestProviderRegistry_RegisterAndGet 验证 ProviderRegistry 注册与查找。
func TestProviderRegistry_RegisterAndGet(t *testing.T) {
	reg := NewProviderRegistry()
	cfg := ProviderConfig{
		Name:     "openai",
		Endpoint: "https://api.openai.com/v1",
		APIKey:   "sk-test",
		Model:    "gpt-4o",
	}
	if _, err := reg.Register(cfg); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if reg.Get("openai") == nil {
		t.Fatal("expected registered provider openai")
	}
	if got := reg.List(); len(got) != 1 || got[0] != "openai" {
		t.Fatalf("unexpected provider list: %v", got)
	}
}
