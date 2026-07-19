package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp/marketplace"
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

	// MCPPreinstall lists market packages that should be installed automatically
	// at server startup. Loaded from MCP_PREINSTALL; failures are logged but do
	// not block startup because packages may depend on external commands.
	MCPPreinstall []marketplace.MCPPreinstallEntry

	// Sandbox configuration for execute_program (Phase 5 preview).
	// When EnableSandbox is false (default), execute_program runs locally.
	// When true, the runner uses Docker with the configured image.
	EnableSandbox bool   // SANDBOX_ENABLE
	SandboxImage  string // SANDBOX_IMAGE

	// WebSearch configuration maps to environment variables for provider selection
	// and API keys. Loaded in Load() so the server can wire the core/web_search tool.
	// DuckDuckGo is the zero-key fallback; all other providers default to off.
	WebSearchProvider       string // WEBSEARCH_PROVIDER
	WebSearchDisableDDG     bool   // WEBSEARCH_DISABLE_DDG
	WebSearchEnableExa      bool   // WEBSEARCH_ENABLE_EXA
	WebSearchEnableParallel bool   // WEBSEARCH_ENABLE_PARALLEL
	WebSearchExaAPIKey      string // WEBSEARCH_EXA_API_KEY
	WebSearchParallelAPIKey string // WEBSEARCH_PARALLEL_API_KEY

	// Bing Web Search API (Azure) configuration.
	WebSearchEnableBing   bool   // WEBSEARCH_ENABLE_BING
	WebSearchBingAPIKey   string // WEBSEARCH_BING_API_KEY
	WebSearchBingEndpoint string // WEBSEARCH_BING_ENDPOINT

	// Google Custom Search JSON API configuration.
	WebSearchEnableGoogle   bool   // WEBSEARCH_ENABLE_GOOGLE
	WebSearchGoogleAPIKey   string // WEBSEARCH_GOOGLE_API_KEY
	WebSearchGoogleCX       string // WEBSEARCH_GOOGLE_CX
	WebSearchGoogleEndpoint string // WEBSEARCH_GOOGLE_ENDPOINT

	// Tavily Search API configuration.
	WebSearchEnableTavily        bool   // WEBSEARCH_ENABLE_TAVILY
	WebSearchTavilyAPIKey        string // WEBSEARCH_TAVILY_API_KEY
	WebSearchTavilyEndpoint      string // WEBSEARCH_TAVILY_ENDPOINT
	WebSearchTavilySearchDepth   string // WEBSEARCH_TAVILY_SEARCH_DEPTH
	WebSearchTavilyIncludeAnswer bool   // WEBSEARCH_TAVILY_INCLUDE_ANSWER

	// Brave Search API configuration.
	WebSearchEnableBrave   bool   // WEBSEARCH_ENABLE_BRAVE
	WebSearchBraveAPIKey   string // WEBSEARCH_BRAVE_API_KEY
	WebSearchBraveEndpoint string // WEBSEARCH_BRAVE_ENDPOINT

	// Placeholder providers for future kimi_search and glm_search support.
	WebSearchEnableKimiSearch bool // WEBSEARCH_ENABLE_KIMI_SEARCH
	WebSearchEnableGlmSearch  bool // WEBSEARCH_ENABLE_GLM_SEARCH

	// Embedding provider configuration. When provider is empty or "local", the
	// existing LocalEmbeddingProvider is used. When "openai" or "cohere", a
	// remote HTTP provider is constructed from the fields below.
	EmbeddingProvider   string // EMBEDDING_PROVIDER (local | openai | cohere)
	EmbeddingEndpoint   string // EMBEDDING_ENDPOINT
	EmbeddingAPIKey     string // EMBEDDING_API_KEY
	EmbeddingModel      string // EMBEDDING_MODEL
	EmbeddingDimensions int    // EMBEDDING_DIMENSIONS

	// ContractLimits defines server-enforced upper bounds for task contracts.
	// Loaded from CONTRACT_LIMIT_* environment variables and exposed via the
	// /api/contract-limits endpoint so frontends can clamp user inputs.
	ContractLimits ContractLimits
}

// ContractLimits stores server-enforced upper bounds for task contracts.
// Values are loaded from CONTRACT_LIMIT_* environment variables and consumed
// by HTTP handlers to validate / clamp user-provided task parameters.
type ContractLimits struct {
	MaxSteps          int      `json:"max_steps"`
	MaxTokensPerStep  int      `json:"max_tokens_per_step"`
	MaxTimeoutSeconds int      `json:"max_timeout_seconds"`
	MaxSubAgents      int      `json:"max_sub_agents"`
	MaxInputLength    int      `json:"max_input_length"`
	Scopes            []string `json:"scopes"`
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
	if v := os.Getenv("WEBSEARCH_DISABLE_DDG"); v != "" {
		cfg.WebSearchDisableDDG = strings.EqualFold(v, "true") || v == "1"
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

	// Bing Web Search API configuration.
	if v := os.Getenv("WEBSEARCH_ENABLE_BING"); v != "" {
		cfg.WebSearchEnableBing = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_BING_API_KEY"); v != "" {
		cfg.WebSearchBingAPIKey = v
	}
	if v := os.Getenv("WEBSEARCH_BING_ENDPOINT"); v != "" {
		cfg.WebSearchBingEndpoint = v
	}

	// Google Custom Search JSON API configuration.
	if v := os.Getenv("WEBSEARCH_ENABLE_GOOGLE"); v != "" {
		cfg.WebSearchEnableGoogle = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_GOOGLE_API_KEY"); v != "" {
		cfg.WebSearchGoogleAPIKey = v
	}
	if v := os.Getenv("WEBSEARCH_GOOGLE_CX"); v != "" {
		cfg.WebSearchGoogleCX = v
	}
	if v := os.Getenv("WEBSEARCH_GOOGLE_ENDPOINT"); v != "" {
		cfg.WebSearchGoogleEndpoint = v
	}

	// Tavily Search API configuration.
	if v := os.Getenv("WEBSEARCH_ENABLE_TAVILY"); v != "" {
		cfg.WebSearchEnableTavily = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_TAVILY_API_KEY"); v != "" {
		cfg.WebSearchTavilyAPIKey = v
	}
	if v := os.Getenv("WEBSEARCH_TAVILY_ENDPOINT"); v != "" {
		cfg.WebSearchTavilyEndpoint = v
	}
	if v := os.Getenv("WEBSEARCH_TAVILY_SEARCH_DEPTH"); v != "" {
		cfg.WebSearchTavilySearchDepth = v
	}
	if v := os.Getenv("WEBSEARCH_TAVILY_INCLUDE_ANSWER"); v != "" {
		cfg.WebSearchTavilyIncludeAnswer = strings.EqualFold(v, "true") || v == "1"
	}

	// Brave Search API configuration.
	if v := os.Getenv("WEBSEARCH_ENABLE_BRAVE"); v != "" {
		cfg.WebSearchEnableBrave = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_BRAVE_API_KEY"); v != "" {
		cfg.WebSearchBraveAPIKey = v
	}
	if v := os.Getenv("WEBSEARCH_BRAVE_ENDPOINT"); v != "" {
		cfg.WebSearchBraveEndpoint = v
	}

	// Placeholder kimi_search / glm_search configuration.
	if v := os.Getenv("WEBSEARCH_ENABLE_KIMI_SEARCH"); v != "" {
		cfg.WebSearchEnableKimiSearch = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_ENABLE_GLM_SEARCH"); v != "" {
		cfg.WebSearchEnableGlmSearch = strings.EqualFold(v, "true") || v == "1"
	}

	// Embedding provider configuration.
	if v := os.Getenv("EMBEDDING_PROVIDER"); v != "" {
		cfg.EmbeddingProvider = v
	}
	if v := os.Getenv("EMBEDDING_ENDPOINT"); v != "" {
		cfg.EmbeddingEndpoint = v
	}
	if v := os.Getenv("EMBEDDING_API_KEY"); v != "" {
		cfg.EmbeddingAPIKey = v
	}
	if v := os.Getenv("EMBEDDING_MODEL"); v != "" {
		cfg.EmbeddingModel = v
	}
	if v := os.Getenv("EMBEDDING_DIMENSIONS"); v != "" {
		if d, err := strconv.Atoi(v); err == nil {
			cfg.EmbeddingDimensions = d
		}
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

	// Load MCP preinstall configuration
	if err := cfg.LoadMCPPreinstallConfig(); err != nil {
		return nil, fmt.Errorf("load mcp preinstall config: %w", err)
	}

	// Load server-enforced contract limits from environment variables.
	cfg.LoadContractLimits()

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

// LoadMCPPreinstallConfig loads the list of market packages that should be
// installed automatically at startup from MCP_PREINSTALL.
//
// The value is a JSON array where each element is either a shorthand string
// "market/package" (market defaults to "default" if omitted) or an object
// {"market":"...","package":"..."}. Parse errors are returned so the server
// can decide whether to log and continue or fail fast.
func (cfg *Config) LoadMCPPreinstallConfig() error {
	jsonStr := os.Getenv("MCP_PREINSTALL")
	if jsonStr == "" {
		return nil
	}

	// First try the heterogeneous array: strings and objects.
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return fmt.Errorf("parse MCP_PREINSTALL JSON: %w", err)
	}

	entries := make([]marketplace.MCPPreinstallEntry, 0, len(raw))
	for _, r := range raw {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			entry, err := marketplace.ParsePreinstallEntry(s)
			if err != nil {
				return fmt.Errorf("parse MCP_PREINSTALL entry %q: %w", s, err)
			}
			entries = append(entries, entry)
			continue
		}

		var entry marketplace.MCPPreinstallEntry
		if err := json.Unmarshal(r, &entry); err != nil {
			return fmt.Errorf("parse MCP_PREINSTALL entry: %w", err)
		}
		if entry.Package == "" {
			return fmt.Errorf("MCP_PREINSTALL entry missing package: %s", string(r))
		}
		if entry.Market == "" {
			entry.Market = "default"
		}
		entries = append(entries, entry)
	}

	cfg.MCPPreinstall = entries
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

// parseEnvIntDefault reads an integer environment variable and returns the
// default value when the variable is missing, empty, or not a valid integer.
func parseEnvIntDefault(key string, defaultValue int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: invalid %s value %q, using default %d: %v\n", key, v, defaultValue, err)
		return defaultValue
	}
	return n
}

// LoadContractLimits loads server-enforced task contract bounds from
// CONTRACT_LIMIT_* environment variables. Any missing or invalid value falls
// back to a safe default so the server can start without manual tuning.
func (cfg *Config) LoadContractLimits() {
	cfg.ContractLimits = ContractLimits{
		MaxSteps:          parseEnvIntDefault("CONTRACT_LIMIT_MAX_STEPS", 200),
		MaxTokensPerStep:  parseEnvIntDefault("CONTRACT_LIMIT_MAX_TOKENS_PER_STEP", 4096),
		MaxTimeoutSeconds: parseEnvIntDefault("CONTRACT_LIMIT_MAX_TIMEOUT_SECONDS", 7200),
		MaxSubAgents:      parseEnvIntDefault("CONTRACT_LIMIT_MAX_SUB_AGENTS", 10),
		MaxInputLength:    parseEnvIntDefault("CONTRACT_LIMIT_MAX_INPUT_LENGTH", 10000),
		Scopes:            []string{"read_only", "standard", "unrestricted"},
	}
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

// BuildEmbeddingProviderParams holds the parameters needed by the server to
// construct an embedding provider from configuration. Keeping this struct in
// config (instead of returning a concrete provider directly) avoids an
// import cycle between internal/config and internal/llm.
type BuildEmbeddingProviderParams struct {
	Provider   string // EMBEDDING_PROVIDER (local | openai | cohere)
	Endpoint   string // EMBEDDING_ENDPOINT
	APIKey     string // EMBEDDING_API_KEY
	Model      string // EMBEDDING_MODEL
	Dimensions int    // EMBEDDING_DIMENSIONS
}

// EmbeddingProviderParams returns the parameters needed to build an embedding
// provider. The server layer constructs the concrete provider to avoid an
// import cycle between internal/config and internal/llm.
func (cfg *Config) EmbeddingProviderParams() BuildEmbeddingProviderParams {
	return BuildEmbeddingProviderParams{
		Provider:   cfg.EmbeddingProvider,
		Endpoint:   cfg.EmbeddingEndpoint,
		APIKey:     cfg.EmbeddingAPIKey,
		Model:      cfg.EmbeddingModel,
		Dimensions: cfg.EmbeddingDimensions,
	}
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