package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
)

// Config holds application configuration loaded from environment and .env
//
// Backward-compatible design: existing single-model fields (LLMEndpoint, LLMAPIKey,
// LLMModel) remain for the simple case. Multi-model support is added via the Models
// slice and ProviderDefault field, loaded from LLM_MODELS (JSON) and
// LLM_PROVIDER_DEFAULT environment variables.
type Config struct {
	LLMEndpoint   string
	LLMAPIKey     string
	LLMModel      string
	DBPath        string
	ServerPort    string
	ProviderDefault string           // default provider name for multi-model routing
	Models        []ModelConfig     // multi-model configuration list

	// LLM mock switch: global default, per-case real outliers, and endpoint/hint overrides.
	// Used to route LLM calls to MockProvider instead of real providers during testing/demo.
	// LLMUseMock defaults to true so new deployments run in deterministic mock mode unless
	// explicitly configured otherwise (or the case is listed in LLMRealCases).
	LLMUseMock      bool     // global default: true → mock, false → real
	LLMRealCases    []string // case IDs that always use real providers (comma-separated from LLM_REAL_CASES)
	LLMMockEndpoints []string // case IDs or endpoint hints that always use mock (comma-separated from LLM_MOCK_ENDPOINTS)

	// MCPServers lists statically configured MCP servers loaded from MCP_SERVERS.
	// Dynamic servers added at runtime are loaded separately by the MCP manager.
	MCPServers []mcp.ServerConfig

	// MCPMarkets lists remote marketplace catalogs loaded from MCP_MARKETS.
	// Each entry is fetched at startup and registered as a marketplace provider.
	MCPMarkets []MCPMarketConfig

	// Sandbox configuration for execute_program (Phase 5 preview).
	// When EnableSandbox is false (default), execute_program runs locally.
	// When true, the runner uses Docker with the configured image.
	EnableSandbox bool   // SANDBOX_ENABLE
	SandboxImage  string // SANDBOX_IMAGE

	// WebSearch configuration maps to environment variables for provider selection
	// and API keys. Loaded in Load() so the server can wire the core/web_search tool.
	WebSearchProvider       string // WEBSEARCH_PROVIDER
	WebSearchEnableExa      bool   // WEBSEARCH_ENABLE_EXA
	WebSearchEnableParallel bool   // WEBSEARCH_ENABLE_PARALLEL
	WebSearchExaAPIKey      string // WEBSEARCH_EXA_API_KEY
	WebSearchParallelAPIKey string // WEBSEARCH_PARALLEL_API_KEY
}

// ModelConfig describes a single model's configuration for multi-model setups.
// Each model is associated with a provider type and its own endpoint/credentials.
type ModelConfig struct {
	Name     string // model identifier (e.g., "deepseek-v4-flash", "claude-sonnet-4-6")
	Provider string // provider type: "openai", "anthropic", "deepseek"
	Endpoint string // provider API base URL
	APIKey   string // provider API key
}

// MCPMarketConfig describes a remote MCP marketplace catalog.
// The Name field becomes the provider name; URL is the JSON catalog endpoint.
type MCPMarketConfig struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Load reads .env file and environment variables to populate Config
func Load() (*Config, error) {
	cfg := &Config{
		LLMEndpoint:  "https://aicoding.dobest.com/v1",
		LLMModel:     "deepseek-v4-flash",
		DBPath:       "data/app.db",
		ServerPort:   "8080",
		LLMUseMock:   true,
		SandboxImage: "python:3.11-slim",
	}

	// Load .env file (lowest priority)
	if err := loadEnvFile(".env"); err != nil {
		// .env is optional — don't fail if missing
		fmt.Fprintf(os.Stderr, "Warning: .env file not found or unreadable: %v\n", err)
	}

	// Override with environment variables (higher priority)
	if v := os.Getenv("LLM_ENDPOINT"); v != "" {
		cfg.LLMEndpoint = v
	}
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		cfg.LLMAPIKey = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		cfg.LLMModel = v
	}
	if v := os.Getenv("DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("SERVER_PORT"); v != "" {
		cfg.ServerPort = v
	}
	if v := os.Getenv("LLM_USE_MOCK"); v != "" {
		cfg.LLMUseMock = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("LLM_REAL_CASES"); v != "" {
		cfg.LLMRealCases = splitAndTrim(v)
	}
	if v := os.Getenv("LLM_MOCK_ENDPOINTS"); v != "" {
		cfg.LLMMockEndpoints = splitAndTrim(v)
	}

	// Sandbox configuration: disabled by default, enabled via SANDBOX_ENABLE=true.
	if v := os.Getenv("SANDBOX_ENABLE"); v != "" {
		cfg.EnableSandbox = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("SANDBOX_IMAGE"); v != "" {
		cfg.SandboxImage = v
	}

	// WebSearch provider configuration.
	if v := os.Getenv("WEBSEARCH_PROVIDER"); v != "" {
		cfg.WebSearchProvider = v
	}
	if v := os.Getenv("WEBSEARCH_ENABLE_EXA"); v != "" {
		cfg.WebSearchEnableExa = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_ENABLE_PARALLEL"); v != "" {
		cfg.WebSearchEnableParallel = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_EXA_API_KEY"); v != "" {
		cfg.WebSearchExaAPIKey = v
	}
	if v := os.Getenv("WEBSEARCH_PARALLEL_API_KEY"); v != "" {
		cfg.WebSearchParallelAPIKey = v
	}

	// Load multi-model configuration
	if err := cfg.LoadMultiModelConfig(); err != nil {
		return nil, fmt.Errorf("load multi-model config: %w", err)
	}

	// Load static MCP server configuration
	if err := cfg.LoadMCPConfig(); err != nil {
		return nil, fmt.Errorf("load mcp config: %w", err)
	}

	// Load remote MCP marketplace configuration
	if err := cfg.LoadMCPMarketConfig(); err != nil {
		return nil, fmt.Errorf("load mcp market config: %w", err)
	}

	return cfg, nil
}

// LoadMCPConfig loads static MCP server configuration from the MCP_SERVERS
// environment variable. The value must be a JSON array of mcp.ServerConfig objects.
//
// Example:
//   MCP_SERVERS=[{"name":"time","transport":"stdio","command":"node","args":["mcp/time.js"],"enabled":true}]
//
// Disabled servers are kept in the list but skipped by the MCP manager; this allows
// configuration files to declare servers that are off by default.
func (cfg *Config) LoadMCPConfig() error {
	jsonStr := os.Getenv("MCP_SERVERS")
	if jsonStr == "" {
		return nil
	}
	var servers []mcp.ServerConfig
	if err := json.Unmarshal([]byte(jsonStr), &servers); err != nil {
		return fmt.Errorf("parse MCP_SERVERS JSON: %w", err)
	}
	cfg.MCPServers = servers
	return nil
}

// LoadMCPMarketConfig loads remote MCP marketplace catalogs from the
// MCP_MARKETS environment variable. The value must be a JSON array of
// MCPMarketConfig objects. Providers that fail to fetch are logged but do not
// prevent the rest of the configuration from loading.
func (cfg *Config) LoadMCPMarketConfig() error {
	jsonStr := os.Getenv("MCP_MARKETS")
	if jsonStr == "" {
		return nil
	}
	var markets []MCPMarketConfig
	if err := json.Unmarshal([]byte(jsonStr), &markets); err != nil {
		return fmt.Errorf("parse MCP_MARKETS JSON: %w", err)
	}
	cfg.MCPMarkets = markets
	return nil
}

// loadEnvFile parses a simple KEY=VALUE .env file (no quotes, no interpolation)
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Only set if not already in environment
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

// splitAndTrim splits a comma-separated string and trims whitespace from each element.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// ShouldMock decides whether a request for the given case/endpoint should be routed
// to MockProvider based on the three-layer mock switch.
//
// Priority (highest first):
//  1) LLMMockEndpoints contains caseID or endpointHint → force mock
//  2) LLMRealCases contains caseID → force real
//  3) LLMUseMock == true → mock
//  4) otherwise → real
func (cfg *Config) ShouldMock(caseID string, endpointHint string) bool {
	contains := func(list []string, value string) bool {
		for _, item := range list {
			if strings.EqualFold(item, value) && value != "" {
				return true
			}
		}
		return false
	}

	if contains(cfg.LLMMockEndpoints, caseID) || contains(cfg.LLMMockEndpoints, endpointHint) {
		return true
	}
	if contains(cfg.LLMRealCases, caseID) {
		return false
	}
	return cfg.LLMUseMock
}

// GetAgentConfig loads an agent's configuration from the database.
// Note: DB persistence is implemented (pkg/db with agents table), but this
// function currently returns a not-implemented error, mirroring the
// behavior of the legacy in-memory config path.
func GetAgentConfig(agentID string) (*AgentConfig, error) {
	_ = agentID
	return nil, fmt.Errorf("agent config DB loading not yet implemented")
}

// AgentConfig mirrors the agent configuration from the database
type AgentConfig struct {
	ID           string
	Name         string
	SystemPrompt string
	Model        string
	Endpoint     string
	APIKey       string
	Temperature  float32
	MaxTokens    int
	Tools        []string
}

// LoadMultiModelConfig loads multi-model configuration from environment variables.
//
// Supported methods (in priority order):
//  1. LLM_MODELS — JSON array of ModelConfig objects (preferred for complex setups)
//  2. LLM_MODEL_<INDEX>_PROVIDER / ENDPOINT / API_KEY — indexed environment variables
//     for simpler, declarative configuration without JSON
//
// Example LLM_MODELS:
//  LLM_MODELS=[
//    {"name":"deepseek-v4-flash","provider":"deepseek","endpoint":"https://aicoding.dobest.com/v1","api_key":"sk-xxx"},
//    {"name":"gpt-4o","provider":"openai","endpoint":"https://api.openai.com/v1","api_key":"sk-yyy"}
//  ]
//
// Example indexed vars:
//  LLM_MODEL_0_PROVIDER=deepseek
//  LLM_MODEL_0_ENDPOINT=https://aicoding.dobest.com/v1
//  LLM_MODEL_0_API_KEY=sk-xxx
//  LLM_MODEL_0_NAME=deepseek-v4-flash
//
// LLM_PROVIDER_DEFAULT sets the default provider name (defaults to the first model's name).
func (cfg *Config) LoadMultiModelConfig() error {
	// Method 1: Try JSON array from LLM_MODELS
	if jsonStr := os.Getenv("LLM_MODELS"); jsonStr != "" {
		var models []ModelConfig
		if err := json.Unmarshal([]byte(jsonStr), &models); err != nil {
			return fmt.Errorf("parse LLM_MODELS JSON: %w", err)
		}
		cfg.Models = models
	} else {
		// Method 2: Try indexed environment variables
		cfg.Models = loadIndexedModelConfigs()
	}

	// Set default provider name
	if v := os.Getenv("LLM_PROVIDER_DEFAULT"); v != "" {
		cfg.ProviderDefault = v
	} else if len(cfg.Models) > 0 {
		// Default to the first model's name
		cfg.ProviderDefault = cfg.Models[0].Name
	}

	return nil
}

// loadIndexedModelConfigs scans environment variables for LLM_MODEL_<INDEX>_* prefix
// to build a slice of ModelConfig. This allows multi-model configuration without JSON.
//
// Variables are grouped by integer index: LLM_MODEL_0_PROVIDER, LLM_MODEL_0_NAME, etc.
// Index scanning stops at the first gap (e.g., if index 2 is missing, only 0 and 1 are loaded).
func loadIndexedModelConfigs() []ModelConfig {
	var models []ModelConfig
	for i := 0; ; i++ {
		provider := os.Getenv(fmt.Sprintf("LLM_MODEL_%d_PROVIDER", i))
		if provider == "" {
			break // stop at first gap
		}
		name := os.Getenv(fmt.Sprintf("LLM_MODEL_%d_NAME", i))
		if name == "" {
			name = fmt.Sprintf("model-%d", i)
		}
		endpoint := os.Getenv(fmt.Sprintf("LLM_MODEL_%d_ENDPOINT", i))
		apiKey := os.Getenv(fmt.Sprintf("LLM_MODEL_%d_API_KEY", i))
		if endpoint == "" {
			endpoint = os.Getenv("LLM_ENDPOINT") // fall back to default
		}
		if apiKey == "" {
			apiKey = os.Getenv("LLM_API_KEY") // fall back to default
		}
		models = append(models, ModelConfig{
			Name:     name,
			Provider: provider,
			Endpoint: endpoint,
			APIKey:   apiKey,
		})
	}
	return models
}