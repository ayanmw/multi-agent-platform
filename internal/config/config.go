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

// Config 持有从环境变量与 .env 加载的应用配置。
//
// 向后兼容设计:已有的单 model 字段(LLMEndpoint、LLMAPIKey、LLMModel)
// 在简单场景下保留。多 model 支持通过 Models 切片与 ProviderDefault 字段
// 提供,分别从 LLM_MODELS(JSON)与 LLM_PROVIDER_DEFAULT 环境变量加载。
type Config struct {
	LLMEndpoint   string
	LLMAPIKey     string
	LLMModel      string
	DBPath        string
	ServerPort    string
	ProviderDefault string           // 多 model 路由的默认 provider 名称
	Models        []ModelConfig     // 多 model 配置列表

	// LLM mock 开关:全局默认、按 case 的真实例外,以及 endpoint/hint 覆盖。
	// 用于在测试 / demo 时将 LLM 调用路由到 MockProvider 而非真实 provider。
	// LLMUseMock 默认为 true,使新部署在显式配置为其他值(或 case 被列入 LLMRealCases)
	// 之前以确定性 mock 模式运行。
	LLMUseMock      bool     // 全局默认:true → mock,false → real
	LLMRealCases    []string // 始终使用真实 provider 的 case ID(来自 LLM_REAL_CASES,逗号分隔)
	LLMMockEndpoints []string // 始终使用 mock 的 case ID 或 endpoint hint(来自 LLM_MOCK_ENDPOINTS,逗号分隔)

	// MCPServers 列出从 MCP_SERVERS 加载的静态配置 MCP server。
	// 运行时动态添加的 server 由 MCP manager 单独加载。
	MCPServers []mcp.ServerConfig

	// MCPMarkets 列出从 MCP_MARKETS 加载的远程 marketplace catalog。
	// 每条记录在启动时被拉取并注册为一个 marketplace provider。
	MCPMarkets []MCPMarketConfig

	// MCPPreinstall 列出应在 server 启动时自动安装的 market 包。
	// 从 MCP_PREINSTALL 加载;失败会被记录但不会阻断启动,
	// 因为包可能依赖外部命令。
	MCPPreinstall []marketplace.MCPPreinstallEntry

	// Sandbox(execute_program,Phase 5 预览)配置。
	// 当 EnableSandbox 为 false(默认)时,execute_program 在本地运行。
	// 当为 true 时,runner 使用 Docker 与配置的镜像。
	EnableSandbox bool   // SANDBOX_ENABLE
	SandboxImage  string // SANDBOX_IMAGE

	// Cron 子系统配置。
	// CronEnabled 总开关，false 时 scheduler 不启动（仍可通过 REST/API 创建 cron，只是不会自动触发）。
	// CronAllowedTools 限制 script action 可调用的 tool 名白名单，复用现有 run_shell 等 tool 的 sandbox/policy。
	// CronWebhookTimeoutSeconds webhook action 的 HTTP 超时。
	// CronMaxResultChars execution.result_summary 的截断长度，避免长结果撑爆存储与前端。
	CronEnabled               bool
	CronAllowedTools          []string
	CronWebhookTimeoutSeconds int
	CronMaxResultChars        int

	// Workspace worktree 隔离配置。
	// WorktreeEnabled 总开关，默认 true：worktree 是主动触发的叠加能力，
	// 不触发则系统零感知，故默认开启不影响存量。false 时 worktree/create
	// Agent Tool 与 REST create 返回错误，run 沿用普通 session WorkspaceDir。
	// 不设 session 结束钩子、不引入 WORKTREE_DEFAULT_EXIT（见 design D8）。
	WorktreeEnabled bool

	// WebSearch 配置映射到用于选择 provider 与 API key 的环境变量。
	// 在 Load() 中加载,以便 server 能接入 core/web_search 工具。
	// DuckDuckGo 是零 key 的兜底方案;其他所有 provider 默认关闭。
	WebSearchProvider       string // WEBSEARCH_PROVIDER
	WebSearchDisableDDG     bool   // WEBSEARCH_DISABLE_DDG
	WebSearchEnableExa      bool   // WEBSEARCH_ENABLE_EXA
	WebSearchEnableParallel bool   // WEBSEARCH_ENABLE_PARALLEL
	WebSearchExaAPIKey      string // WEBSEARCH_EXA_API_KEY
	WebSearchParallelAPIKey string // WEBSEARCH_PARALLEL_API_KEY

	// Bing Web Search API(Azure)配置。
	WebSearchEnableBing   bool   // WEBSEARCH_ENABLE_BING
	WebSearchBingAPIKey   string // WEBSEARCH_BING_API_KEY
	WebSearchBingEndpoint string // WEBSEARCH_BING_ENDPOINT

	// Google Custom Search JSON API 配置。
	WebSearchEnableGoogle   bool   // WEBSEARCH_ENABLE_GOOGLE
	WebSearchGoogleAPIKey   string // WEBSEARCH_GOOGLE_API_KEY
	WebSearchGoogleCX       string // WEBSEARCH_GOOGLE_CX
	WebSearchGoogleEndpoint string // WEBSEARCH_GOOGLE_ENDPOINT

	// Tavily Search API 配置。
	WebSearchEnableTavily        bool   // WEBSEARCH_ENABLE_TAVILY
	WebSearchTavilyAPIKey        string // WEBSEARCH_TAVILY_API_KEY
	WebSearchTavilyEndpoint      string // WEBSEARCH_TAVILY_ENDPOINT
	WebSearchTavilySearchDepth   string // WEBSEARCH_TAVILY_SEARCH_DEPTH
	WebSearchTavilyIncludeAnswer bool   // WEBSEARCH_TAVILY_INCLUDE_ANSWER

	// Brave Search API 配置。
	WebSearchEnableBrave   bool   // WEBSEARCH_ENABLE_BRAVE
	WebSearchBraveAPIKey   string // WEBSEARCH_BRAVE_API_KEY
	WebSearchBraveEndpoint string // WEBSEARCH_BRAVE_ENDPOINT

	// 用于未来 kimi_search 和 glm_search 支持的占位 provider。
	WebSearchEnableKimiSearch bool // WEBSEARCH_ENABLE_KIMI_SEARCH
	WebSearchEnableGlmSearch  bool // WEBSEARCH_ENABLE_GLM_SEARCH

	// Embedding provider 配置。当 provider 为空或 "local" 时,
	// 使用现有的 LocalEmbeddingProvider。当为 "openai" 或 "cohere" 时,
	// 根据下列字段构造一个远程 HTTP provider。
	EmbeddingProvider   string // EMBEDDING_PROVIDER (local | openai | cohere)
	EmbeddingEndpoint   string // EMBEDDING_ENDPOINT
	EmbeddingAPIKey     string // EMBEDDING_API_KEY
	EmbeddingModel      string // EMBEDDING_MODEL
	EmbeddingDimensions int    // EMBEDDING_DIMENSIONS

	// ContractLimits 定义 server 端强制执行的任务合约上限。
	// 从 CONTRACT_LIMIT_* 环境变量加载,并通过 /api/contract-limits 端点暴露,
	// 以便前端可以据此约束用户输入。
	ContractLimits ContractLimits
}

// ContractLimits 存储 server 端强制执行的任务合约上限。
// 值从 CONTRACT_LIMIT_* 环境变量加载,由 HTTP handler 消费,
// 用于校验 / 约束用户提供的任务参数。
type ContractLimits struct {
	MaxSteps          int      `json:"max_steps"`
	MaxTokensPerStep  int      `json:"max_tokens_per_step"`
	MaxTimeoutSeconds int      `json:"max_timeout_seconds"`
	MaxSubAgents      int      `json:"max_sub_agents"`
	MaxInputLength    int      `json:"max_input_length"`
	Scopes            []string `json:"scopes"`
}

// ModelConfig 描述多 model 设置中单个 model 的配置。
// 每个 model 关联一个 provider 类型,并拥有自己的 endpoint / 凭据。
type ModelConfig struct {
	Name     string // model 标识(例如 "deepseek-v4-flash"、"claude-sonnet-4-6")
	Provider string // provider 类型:"openai"、"anthropic"、"deepseek"
	Endpoint string // provider API base URL
	APIKey   string // provider API key
}

// MCPMarketConfig 描述一个远程 MCP marketplace catalog。
// Name 字段会成为 provider 名称;URL 是 JSON catalog 端点。
type MCPMarketConfig struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Load 读取 .env 文件与环境变量以填充 Config
func Load() (*Config, error) {
	cfg := &Config{
		LLMEndpoint:  "https://aicoding.dobest.com/v1",
		LLMModel:     "deepseek-v4-flash",
		DBPath:       "data/app.db",
		ServerPort:   "8080",
		LLMUseMock:   true,
		SandboxImage: "python:3.11-slim",
	}

	// 加载 .env 文件(优先级最低)
	if err := loadEnvFile(".env"); err != nil {
		// .env 是可选的 — 缺失不应导致失败
		fmt.Fprintf(os.Stderr, "Warning: .env file not found or unreadable: %v\n", err)
	}

	// 用环境变量覆盖(优先级更高)
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

	// Sandbox 配置:默认关闭,通过 SANDBOX_ENABLE=true 启用。
	if v := os.Getenv("SANDBOX_ENABLE"); v != "" {
		cfg.EnableSandbox = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("SANDBOX_IMAGE"); v != "" {
		cfg.SandboxImage = v
	}

	// Cron 子系统配置：默认启用，白名单含常用只读/执行 tool。
	cfg.CronEnabled = true
	if v := os.Getenv("CRON_ENABLED"); v != "" {
		cfg.CronEnabled = strings.EqualFold(v, "true") || v == "1"
	}
	cfg.CronAllowedTools = []string{"run_shell", "read_file", "write_file", "fetch_url"}
	if v := os.Getenv("CRON_ALLOWED_TOOLS"); v != "" {
		cfg.CronAllowedTools = splitAndTrim(v)
	}
	cfg.CronWebhookTimeoutSeconds = parseEnvIntDefault("CRON_WEBHOOK_TIMEOUT_SECONDS", 10)
	cfg.CronMaxResultChars = parseEnvIntDefault("CRON_MAX_EXECUTION_RESULT_CHARS", 2000)

	// Workspace worktree 隔离：默认启用（主动触发的叠加能力，不触发则零感知）。
	cfg.WorktreeEnabled = true
	if v := os.Getenv("WORKTREE_ENABLED"); v != "" {
		cfg.WorktreeEnabled = strings.EqualFold(v, "true") || v == "1"
	}

	// WebSearch provider 配置。
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

	// Bing Web Search API 配置。
	if v := os.Getenv("WEBSEARCH_ENABLE_BING"); v != "" {
		cfg.WebSearchEnableBing = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_BING_API_KEY"); v != "" {
		cfg.WebSearchBingAPIKey = v
	}
	if v := os.Getenv("WEBSEARCH_BING_ENDPOINT"); v != "" {
		cfg.WebSearchBingEndpoint = v
	}

	// Google Custom Search JSON API 配置。
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

	// Tavily Search API 配置。
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

	// Brave Search API 配置。
	if v := os.Getenv("WEBSEARCH_ENABLE_BRAVE"); v != "" {
		cfg.WebSearchEnableBrave = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_BRAVE_API_KEY"); v != "" {
		cfg.WebSearchBraveAPIKey = v
	}
	if v := os.Getenv("WEBSEARCH_BRAVE_ENDPOINT"); v != "" {
		cfg.WebSearchBraveEndpoint = v
	}

	// 占位的 kimi_search / glm_search 配置。
	if v := os.Getenv("WEBSEARCH_ENABLE_KIMI_SEARCH"); v != "" {
		cfg.WebSearchEnableKimiSearch = strings.EqualFold(v, "true") || v == "1"
	}
	if v := os.Getenv("WEBSEARCH_ENABLE_GLM_SEARCH"); v != "" {
		cfg.WebSearchEnableGlmSearch = strings.EqualFold(v, "true") || v == "1"
	}

	// Embedding provider 配置。
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

	// 加载多 model 配置
	if err := cfg.LoadMultiModelConfig(); err != nil {
		return nil, fmt.Errorf("load multi-model config: %w", err)
	}

	// 加载静态 MCP server 配置
	if err := cfg.LoadMCPConfig(); err != nil {
		return nil, fmt.Errorf("load mcp config: %w", err)
	}

	// 加载远程 MCP marketplace 配置
	if err := cfg.LoadMCPMarketConfig(); err != nil {
		return nil, fmt.Errorf("load mcp market config: %w", err)
	}

	// 加载 MCP preinstall 配置
	if err := cfg.LoadMCPPreinstallConfig(); err != nil {
		return nil, fmt.Errorf("load mcp preinstall config: %w", err)
	}

	// 从环境变量加载 server 端强制执行的合约上限。
	cfg.LoadContractLimits()

	return cfg, nil
}

// LoadMCPConfig 从 MCP_SERVERS 环境变量加载静态 MCP server 配置。
// 值必须是一个 mcp.ServerConfig 对象的 JSON 数组。
//
// 示例:
//   MCP_SERVERS=[{"name":"time","transport":"stdio","command":"node","args":["mcp/time.js"],"enabled":true}]
//
// 被禁用的 server 仍保留在列表中,但会被 MCP manager 跳过;这使得配置文件
// 可以声明默认关闭的 server。
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

// LoadMCPMarketConfig 从 MCP_MARKETS 环境变量加载远程 MCP marketplace catalog。
// 值必须是一个 MCPMarketConfig 对象的 JSON 数组。拉取失败的 provider 会被记录日志,
// 但不会阻止其余配置的加载。
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

// LoadMCPPreinstallConfig 从 MCP_PREINSTALL 加载应在启动时自动安装的
// market 包列表。
//
// 值是一个 JSON 数组,每个元素要么是简写字符串 "market/package"
//(省略 market 时默认为 "default"),要么是对象
// {"market":"...","package":"..."}。解析错误会被返回,以便 server
// 决定是记录日志后继续还是直接失败。
func (cfg *Config) LoadMCPPreinstallConfig() error {
	jsonStr := os.Getenv("MCP_PREINSTALL")
	if jsonStr == "" {
		return nil
	}

	// 先尝试异构数组:字符串与对象混合。
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

// loadEnvFile 解析简单的 KEY=VALUE .env 文件(无引号、无插值)
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 跳过空行与注释
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// 仅在环境变量尚未存在时才设置
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

// splitAndTrim 拆分逗号分隔的字符串,并对每个元素做 trim 去除空白。
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

// parseEnvIntDefault 读取一个整数类型的环境变量,当变量缺失、为空或
// 不是合法整数时返回默认值。
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

// LoadContractLimits 从 CONTRACT_LIMIT_* 环境变量加载 server 端强制执行的
// 任务合约边界。任何缺失或非法的值都会回退到一个安全默认值,使 server
// 无需手动调参即可启动。
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

// ShouldMock 基于三层 mock 开关,判断对给定 case / endpoint 的请求是否应
// 路由到 MockProvider。
//
// 优先级(从高到低):
//  1) LLMMockEndpoints 包含 caseID 或 endpointHint → 强制 mock
//  2) LLMRealCases 包含 caseID → 强制 real
//  3) LLMUseMock == true → mock
//  4) 其他情况 → real
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

// GetAgentConfig 从数据库加载某个 agent 的配置。
// 注意:DB 持久化已实现(pkg/db 中的 agents 表),但本函数当前返回
// not-implemented 错误,以镜像旧 in-memory 配置路径的行为。
func GetAgentConfig(agentID string) (*AgentConfig, error) {
	_ = agentID
	return nil, fmt.Errorf("agent config DB loading not yet implemented")
}

// BuildEmbeddingProviderParams 持有 server 从配置构造 embedding provider
// 所需的参数。将此结构体保留在 config 中(而非直接返回具体 provider)
// 可避免 internal/config 与 internal/llm 之间的 import cycle(循环依赖)。
type BuildEmbeddingProviderParams struct {
	Provider   string // EMBEDDING_PROVIDER (local | openai | cohere)
	Endpoint   string // EMBEDDING_ENDPOINT
	APIKey     string // EMBEDDING_API_KEY
	Model      string // EMBEDDING_MODEL
	Dimensions int    // EMBEDDING_DIMENSIONS
}

// EmbeddingProviderParams 返回构造 embedding provider 所需的参数。
// 由 server 层构造具体 provider,以避免 internal/config 与 internal/llm
// 之间的 import cycle。
func (cfg *Config) EmbeddingProviderParams() BuildEmbeddingProviderParams {
	return BuildEmbeddingProviderParams{
		Provider:   cfg.EmbeddingProvider,
		Endpoint:   cfg.EmbeddingEndpoint,
		APIKey:     cfg.EmbeddingAPIKey,
		Model:      cfg.EmbeddingModel,
		Dimensions: cfg.EmbeddingDimensions,
	}
}

// AgentConfig 镜像数据库中的 agent 配置
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

// LoadMultiModelConfig 从环境变量加载多 model 配置。
//
// 支持的方法(按优先级顺序):
//  1. LLM_MODELS — ModelConfig 对象的 JSON 数组(复杂配置推荐)
//  2. LLM_MODEL_<INDEX>_PROVIDER / ENDPOINT / API_KEY — 带索引的环境变量,
//     适合无需 JSON 的简单声明式配置
//
// LLM_MODELS 示例:
//  LLM_MODELS=[
//    {"name":"deepseek-v4-flash","provider":"deepseek","endpoint":"https://aicoding.dobest.com/v1","api_key":"sk-xxx"},
//    {"name":"gpt-4o","provider":"openai","endpoint":"https://api.openai.com/v1","api_key":"sk-yyy"}
//  ]
//
// 带索引变量示例:
//  LLM_MODEL_0_PROVIDER=deepseek
//  LLM_MODEL_0_ENDPOINT=https://aicoding.dobest.com/v1
//  LLM_MODEL_0_API_KEY=sk-xxx
//  LLM_MODEL_0_NAME=deepseek-v4-flash
//
// LLM_PROVIDER_DEFAULT 设置默认 provider 名称(默认为第一个 model 的 name)。
func (cfg *Config) LoadMultiModelConfig() error {
	// 方法 1:从 LLM_MODELS 尝试 JSON 数组
	if jsonStr := os.Getenv("LLM_MODELS"); jsonStr != "" {
		var models []ModelConfig
		if err := json.Unmarshal([]byte(jsonStr), &models); err != nil {
			return fmt.Errorf("parse LLM_MODELS JSON: %w", err)
		}
		cfg.Models = models
	} else {
		// 方法 2:尝试带索引的环境变量
		cfg.Models = loadIndexedModelConfigs()
	}

	// 设置默认 provider 名称
	if v := os.Getenv("LLM_PROVIDER_DEFAULT"); v != "" {
		cfg.ProviderDefault = v
	} else if len(cfg.Models) > 0 {
		// 默认使用第一个 model 的 name
		cfg.ProviderDefault = cfg.Models[0].Name
	}

	return nil
}

// loadIndexedModelConfigs 扫描环境变量中 LLM_MODEL_<INDEX>_* 前缀,
// 构造一个 ModelConfig 切片。这样无需 JSON 即可配置多 model。
//
// 变量按整数索引分组:LLM_MODEL_0_PROVIDER、LLM_MODEL_0_NAME 等。
// 索引扫描在遇到第一个空缺时停止(例如索引 2 缺失,则只加载 0 和 1)。
func loadIndexedModelConfigs() []ModelConfig {
	var models []ModelConfig
	for i := 0; ; i++ {
		provider := os.Getenv(fmt.Sprintf("LLM_MODEL_%d_PROVIDER", i))
		if provider == "" {
			break // 遇到第一个空缺即停止
		}
		name := os.Getenv(fmt.Sprintf("LLM_MODEL_%d_NAME", i))
		if name == "" {
			name = fmt.Sprintf("model-%d", i)
		}
		endpoint := os.Getenv(fmt.Sprintf("LLM_MODEL_%d_ENDPOINT", i))
		apiKey := os.Getenv(fmt.Sprintf("LLM_MODEL_%d_API_KEY", i))
		if endpoint == "" {
			endpoint = os.Getenv("LLM_ENDPOINT") // 回退到默认值
		}
		if apiKey == "" {
			apiKey = os.Getenv("LLM_API_KEY") // 回退到默认值
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