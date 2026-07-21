package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp/marketplace"
)

// TestShouldMockPriority 使用表驱动子测试验证三层 mock 开关的优先级。
// 每个子测试直接构造 Config(不调用 Load)并演练 ShouldMock。
//
// 优先级(从高到低):
//  1) LLMMockEndpoints 包含 caseID 或 endpointHint → 强制 mock
//  2) LLMRealCases 包含 caseID → 强制 real
//  3) LLMUseMock == true → mock
//  4) 其他情况 → real
func TestShouldMockPriority(t *testing.T) {
	tests := []struct {
		name         string
		useMock      bool
		realCases    []string
		mockEndpoints []string
		caseID       string
		endpointHint string
		want         bool
	}{
		// 第 1 层:mock endpoints 强制 mock,与 useMock 和 realCases 无关。
		{
			name:          "mock_endpoint_caseID_hit_forces_mock_even_if_useMock_false_and_realCases_hit",
			useMock:       false,
			realCases:     []string{"dialogue"},
			mockEndpoints: []string{"dialogue"},
			caseID:        "dialogue",
			endpointHint:  "",
			want:          true,
		},
		{
			name:          "mock_endpoint_endpointHint_hit_forces_mock",
			useMock:       false,
			mockEndpoints: []string{"deepseek-v4"},
			caseID:        "",
			endpointHint:  "deepseek-v4",
			want:          true,
		},
		{
			name:          "mock_endpoint_miss_does_not_force_mock",
			useMock:       false,
			mockEndpoints: []string{"other"},
			caseID:        "dialogue",
			endpointHint:  "deepseek-v4",
			want:          false,
		},

		// 第 2 层:real cases 强制 real,与 useMock=true 无关。
		{
			name:       "real_case_hit_forces_real_even_if_useMock_true",
			useMock:    true,
			realCases:  []string{"research"},
			caseID:     "research",
			want:       false,
		},
		{
			name:       "real_case_miss_falls_through_to_useMock",
			useMock:    true,
			realCases:  []string{"research"},
			caseID:     "dialogue",
			want:       true,
		},

		// 第 3、4 层:useMock 默认值。
		{
			name:    "useMock_true_no_overrides_returns_true",
			useMock: true,
			caseID:  "anything",
			want:    true,
		},
		{
			name:    "useMock_false_no_overrides_returns_false",
			useMock: false,
			caseID:  "anything",
			want:    false,
		},

		// 边界:caseID 与 endpointHint 均为空。
		{
			name:          "empty_caseID_and_endpointHint_with_useMock_true",
			useMock:       true,
			caseID:        "",
			endpointHint:  "",
			want:          true,
		},
		{
			name:          "empty_caseID_with_realCases_set_no_match",
			useMock:       true,
			realCases:     []string{"research"},
			caseID:        "",
			want:          true,
		},
		{
			name:          "empty_caseID_in_mockEndpoints_does_not_match",
			useMock:       false,
			mockEndpoints: []string{""}, // 空元素 — contains() 会对 value != "" 做守卫
			caseID:        "",
			want:          false,
		},

		// 大小写不敏感:源码使用 strings.EqualFold。
		{
			name:          "real_case_case_insensitive_match",
			useMock:       true,
			realCases:     []string{"Research"},
			caseID:        "research",
			want:          false,
		},
		{
			name:          "mock_endpoint_case_insensitive_match",
			useMock:       false,
			mockEndpoints: []string{"Dialogue"},
			caseID:        "dialogue",
			want:          true,
		},

		// nil 与空切片等价。
		{
			name:       "nil_realCases_behaves_like_empty",
			useMock:    true,
			realCases:  nil,
			caseID:     "research",
			want:       true,
		},
		{
			name:          "nil_mockEndpoints_behaves_like_empty",
			useMock:       false,
			mockEndpoints: nil,
			caseID:        "dialogue",
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				LLMUseMock:       tt.useMock,
				LLMRealCases:     tt.realCases,
				LLMMockEndpoints: tt.mockEndpoints,
			}
			if got := cfg.ShouldMock(tt.caseID, tt.endpointHint); got != tt.want {
				t.Fatalf("ShouldMock(%q, %q) = %v, want %v", tt.caseID, tt.endpointHint, got, tt.want)
			}
		})
	}
}

// TestLoadEnvParsing 覆盖 Load 的环境变量解析路径。
// 每个子测试 chdir 到一个没有 .env 文件的临时目录,从而只有
// t.Setenv 设置的值会影响结果。
func TestLoadEnvParsing(t *testing.T) {
	// 辅助函数:在一个清空了相关 env 变量的临时工作目录中运行 fn,
	// 使每个子测试都从一个已知基线开始。
	withCleanEnv := func(t *testing.T, fn func(t *testing.T)) {
		t.Helper()
		// 使用临时目录,使 loadEnvFile(".env") 找不到文件。
		dir := t.TempDir()
		chdir(t, dir)
		// 清空所有 LLM_* / DB_PATH / SERVER_PORT 变量以获得确定性基线。
		clearEnv := []string{
			"LLM_ENDPOINT", "LLM_API_KEY", "LLM_MODEL", "DB_PATH", "SERVER_PORT",
			"LLM_USE_MOCK", "LLM_REAL_CASES", "LLM_MOCK_ENDPOINTS",
			"LLM_MODELS", "LLM_PROVIDER_DEFAULT",
		}
		for _, k := range clearEnv {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
		fn(t)
	}

	t.Run("LLM_USE_MOCK_true", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_USE_MOCK", "true")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !cfg.LLMUseMock {
				t.Fatalf("expected LLMUseMock=true, got false")
			}
		})
	})

	t.Run("LLM_USE_MOCK_false", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_USE_MOCK", "false")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.LLMUseMock {
				t.Fatalf("expected LLMUseMock=false, got true")
			}
		})
	})

	t.Run("LLM_USE_MOCK_1_treated_as_true", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_USE_MOCK", "1")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !cfg.LLMUseMock {
				t.Fatalf("expected LLMUseMock=true for '1', got false")
			}
		})
	})

	t.Run("LLM_USE_MOCK_unset_defaults_to_true", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !cfg.LLMUseMock {
				t.Fatalf("expected default LLMUseMock=true, got false")
			}
		})
	})

	t.Run("LLM_REAL_CASES_simple", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_REAL_CASES", "research,dialogue")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			want := []string{"research", "dialogue"}
			if !reflect.DeepEqual(cfg.LLMRealCases, want) {
				t.Fatalf("LLMRealCases: got %v want %v", cfg.LLMRealCases, want)
			}
		})
	})

	t.Run("LLM_MOCK_ENDPOINTS_simple", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_MOCK_ENDPOINTS", "a,b,c")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			want := []string{"a", "b", "c"}
			if !reflect.DeepEqual(cfg.LLMMockEndpoints, want) {
				t.Fatalf("LLMMockEndpoints: got %v want %v", cfg.LLMMockEndpoints, want)
			}
		})
	})

	t.Run("LLM_REAL_CASES_trims_and_drops_empty", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_REAL_CASES", " a , , b ")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			want := []string{"a", "b"}
			if !reflect.DeepEqual(cfg.LLMRealCases, want) {
				t.Fatalf("LLMRealCases: got %v want %v", cfg.LLMRealCases, want)
			}
		})
	})

	t.Run("LLM_MOCK_ENDPOINTS_trims_and_drops_empty", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_MOCK_ENDPOINTS", " x ,, y ")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			want := []string{"x", "y"}
			if !reflect.DeepEqual(cfg.LLMMockEndpoints, want) {
				t.Fatalf("LLMMockEndpoints: got %v want %v", cfg.LLMMockEndpoints, want)
			}
		})
	})

	t.Run("LLM_REAL_CASES_unset_yields_nil_slice", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.LLMRealCases != nil {
				t.Fatalf("expected nil LLMRealCases, got %v", cfg.LLMRealCases)
			}
		})
	})

	t.Run("LLM_ENDPOINT_overrides_default", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_ENDPOINT", "https://example.com/v1")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.LLMEndpoint != "https://example.com/v1" {
				t.Fatalf("LLMEndpoint: got %q", cfg.LLMEndpoint)
			}
		})
	})

	t.Run("DB_PATH_and_SERVER_PORT_overrides", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("DB_PATH", "/tmp/test.db")
			t.Setenv("SERVER_PORT", "9090")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.DBPath != "/tmp/test.db" {
				t.Fatalf("DBPath: got %q", cfg.DBPath)
			}
			if cfg.ServerPort != "9090" {
				t.Fatalf("ServerPort: got %q", cfg.ServerPort)
			}
		})
	})

	t.Run("LLM_MODELS_invalid_JSON_returns_error", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_MODELS", "{not-json")
			_, err := Load()
			if err == nil {
				t.Fatal("expected error for invalid LLM_MODELS JSON")
			}
		})
	})

	t.Run("LLM_MODELS_valid_JSON_populates_Models", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_MODELS", `[{"name":"m1","provider":"openai","endpoint":"e","api_key":"k"}]`)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.Models) != 1 {
				t.Fatalf("expected 1 model, got %d", len(cfg.Models))
			}
			if cfg.Models[0].Name != "m1" {
				t.Fatalf("model name: got %q", cfg.Models[0].Name)
			}
			if cfg.ProviderDefault != "m1" {
				t.Fatalf("ProviderDefault: got %q", cfg.ProviderDefault)
			}
		})
	})

	t.Run("LLM_PROVIDER_DEFAULT_overrides_first_model_default", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("LLM_MODELS", `[{"name":"m1","provider":"openai"}]`)
			t.Setenv("LLM_PROVIDER_DEFAULT", "custom-default")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.ProviderDefault != "custom-default" {
				t.Fatalf("ProviderDefault: got %q", cfg.ProviderDefault)
			}
		})
	})

	// Cron 子系统默认值：未设置任何 CRON_* 环境变量时使用默认值。
	t.Run("CRON_defaults", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			// withCleanEnv 不清 CRON_*，这里显式清掉以保证基线。
			for _, k := range []string{"CRON_ENABLED", "CRON_ALLOWED_TOOLS", "CRON_WEBHOOK_TIMEOUT_SECONDS", "CRON_MAX_EXECUTION_RESULT_CHARS"} {
				t.Setenv(k, "")
				os.Unsetenv(k)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !cfg.CronEnabled {
				t.Fatalf("expected CronEnabled=true, got false")
			}
			wantTools := []string{"run_shell", "read_file", "write_file", "fetch_url"}
			if !reflect.DeepEqual(cfg.CronAllowedTools, wantTools) {
				t.Fatalf("CronAllowedTools: got %v want %v", cfg.CronAllowedTools, wantTools)
			}
			if cfg.CronWebhookTimeoutSeconds != 10 {
				t.Fatalf("CronWebhookTimeoutSeconds: got %d want 10", cfg.CronWebhookTimeoutSeconds)
			}
			if cfg.CronMaxResultChars != 2000 {
				t.Fatalf("CronMaxResultChars: got %d want 2000", cfg.CronMaxResultChars)
			}
		})
	})

	t.Run("CRON_ENABLED_false", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("CRON_ENABLED", "false")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.CronEnabled {
				t.Fatalf("expected CronEnabled=false, got true")
			}
		})
	})

	t.Run("CRON_ALLOWED_TOOLS_overrides", func(t *testing.T) {
		withCleanEnv(t, func(t *testing.T) {
			t.Setenv("CRON_ALLOWED_TOOLS", "run_shell, fetch_url ")
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			want := []string{"run_shell", "fetch_url"}
			if !reflect.DeepEqual(cfg.CronAllowedTools, want) {
				t.Fatalf("CronAllowedTools: got %v want %v", cfg.CronAllowedTools, want)
			}
		})
	})
}

func TestLoadMCPPreinstallConfig(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	for _, k := range []string{"MCP_PREINSTALL", "MCP_SERVERS", "MCP_MARKETS"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	t.Run("unset_returns_nil", func(t *testing.T) {
		cfg := &Config{}
		if err := cfg.LoadMCPPreinstallConfig(); err != nil {
			t.Fatalf("LoadMCPPreinstallConfig: %v", err)
		}
		if cfg.MCPPreinstall != nil {
			t.Fatalf("expected nil, got %v", cfg.MCPPreinstall)
		}
	})

	t.Run("mixed_string_and_object_entries", func(t *testing.T) {
		cfg := &Config{}
		t.Setenv("MCP_PREINSTALL", `["default/time-server", {"market":"opencode","package":"github"}]`)
		if err := cfg.LoadMCPPreinstallConfig(); err != nil {
			t.Fatalf("LoadMCPPreinstallConfig: %v", err)
		}
		want := []marketplace.MCPPreinstallEntry{
			{Market: "default", Package: "time-server"},
			{Market: "opencode", Package: "github"},
		}
		if !reflect.DeepEqual(cfg.MCPPreinstall, want) {
			t.Fatalf("MCPPreinstall: got %+v, want %+v", cfg.MCPPreinstall, want)
		}
	})

	t.Run("bare_package_defaults_market", func(t *testing.T) {
		cfg := &Config{}
		t.Setenv("MCP_PREINSTALL", `["github"]`)
		if err := cfg.LoadMCPPreinstallConfig(); err != nil {
			t.Fatalf("LoadMCPPreinstallConfig: %v", err)
		}
		if len(cfg.MCPPreinstall) != 1 || cfg.MCPPreinstall[0].Market != "default" || cfg.MCPPreinstall[0].Package != "github" {
			t.Fatalf("MCPPreinstall: got %+v", cfg.MCPPreinstall)
		}
	})

	t.Run("object_missing_package_returns_error", func(t *testing.T) {
		cfg := &Config{}
		t.Setenv("MCP_PREINSTALL", `[{"market":"default"}]`)
		if err := cfg.LoadMCPPreinstallConfig(); err == nil {
			t.Fatal("expected error for missing package")
		}
	})

	t.Run("invalid_JSON_returns_error", func(t *testing.T) {
		cfg := &Config{}
		t.Setenv("MCP_PREINSTALL", `{not-json`)
		if err := cfg.LoadMCPPreinstallConfig(); err == nil {
			t.Fatal("expected error for invalid JSON")
		}
	})
}

// TestSplitAndTrim 直接(白盒)覆盖未导出的辅助函数。
func TestSplitAndTrim(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , , b ", []string{"a", "b"}},
		{",,,", nil},
		{"  spaced  ,  more  ", []string{"spaced", "more"}},
	}
	for _, tt := range tests {
		got := splitAndTrim(tt.in)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitAndTrim(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestLoadEmbeddingConfig(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	for _, k := range []string{"EMBEDDING_PROVIDER", "EMBEDDING_API_KEY", "EMBEDDING_MODEL", "EMBEDDING_DIMENSIONS", "EMBEDDING_ENDPOINT"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("EMBEDDING_PROVIDER", "openai")
	t.Setenv("EMBEDDING_API_KEY", "key")
	t.Setenv("EMBEDDING_MODEL", "text-embedding-3-small")
	t.Setenv("EMBEDDING_DIMENSIONS", "1536")
	t.Setenv("EMBEDDING_ENDPOINT", "https://api.openai.com/v1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if cfg.EmbeddingProvider != "openai" {
		t.Fatalf("provider = %q, want openai", cfg.EmbeddingProvider)
	}
	if cfg.EmbeddingDimensions != 1536 {
		t.Fatalf("dimensions = %d, want 1536", cfg.EmbeddingDimensions)
	}
}

// 仅在环境变量中尚未存在的 key 才会被加载。
func TestLoadEnvFileLoadsVarsAndDoesNotOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "FOO=fromfile\nBAR=fromfile\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	// 预先在环境变量中设置 FOO;它绝不能被文件覆盖。
	t.Setenv("FOO", "fromenv")
	// BAR 应从文件加载。
	os.Unsetenv("BAR")

	if err := loadEnvFile(path); err != nil {
		t.Fatalf("loadEnvFile: %v", err)
	}
	if got := os.Getenv("FOO"); got != "fromenv" {
		t.Errorf("FOO: got %q, want fromenv (existing env wins)", got)
	}
	if got := os.Getenv("BAR"); got != "fromfile" {
		t.Errorf("BAR: got %q, want fromfile", got)
	}
}

// TestLoadEnvFileMissingReturnsError 验证缺失的 .env 文件会产生错误
//(调用方应将其视为非致命错误)。
func TestLoadEnvFileMissingReturnsError(t *testing.T) {
	if err := loadEnvFile(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestGetAgentConfigNotImplemented 验证 stub 返回一个错误。
func TestGetAgentConfigNotImplemented(t *testing.T) {
	_, err := GetAgentConfig("any")
	if err == nil {
		t.Fatal("expected error from unimplemented GetAgentConfig")
	}
}

// TestLoadContractLimits 验证 Config.LoadContractLimits 能从环境变量加载
// server 端强制执行的任务合约边界,并提供安全默认值。
func TestLoadContractLimits(t *testing.T) {
	defaultLimits := ContractLimits{
		MaxSteps:          200,
		MaxTokensPerStep:  4096,
		MaxTimeoutSeconds: 7200,
		MaxSubAgents:      10,
		MaxInputLength:    10000,
		Scopes:            []string{"read_only", "standard", "unrestricted"},
	}

	tests := []struct {
		name     string
		env      map[string]string
		expected ContractLimits
	}{
		{
			name:     "defaults when no env vars set",
			env:      map[string]string{},
			expected: defaultLimits,
		},
		{
			name: "CONTRACT_LIMIT_MAX_STEPS overrides default",
			env:  map[string]string{"CONTRACT_LIMIT_MAX_STEPS": "50"},
			expected: ContractLimits{
				MaxSteps:          50,
				MaxTokensPerStep:  4096,
				MaxTimeoutSeconds: 7200,
				MaxSubAgents:      10,
				MaxInputLength:    10000,
				Scopes:            []string{"read_only", "standard", "unrestricted"},
			},
		},
		{
			name: "invalid CONTRACT_LIMIT_MAX_TIMEOUT_SECONDS falls back to default",
			env:  map[string]string{"CONTRACT_LIMIT_MAX_TIMEOUT_SECONDS": "abc"},
			expected: ContractLimits{
				MaxSteps:          200,
				MaxTokensPerStep:  4096,
				MaxTimeoutSeconds: 7200,
				MaxSubAgents:      10,
				MaxInputLength:    10000,
				Scopes:            []string{"read_only", "standard", "unrestricted"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 为该子测试设置 env 变量,并 defer 清理以避免副作用。
			for k, v := range tt.env {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			cfg := &Config{}
			cfg.LoadContractLimits()

			if cfg.ContractLimits.MaxSteps != tt.expected.MaxSteps {
				t.Errorf("MaxSteps = %d, want %d", cfg.ContractLimits.MaxSteps, tt.expected.MaxSteps)
			}
			if cfg.ContractLimits.MaxTokensPerStep != tt.expected.MaxTokensPerStep {
				t.Errorf("MaxTokensPerStep = %d, want %d", cfg.ContractLimits.MaxTokensPerStep, tt.expected.MaxTokensPerStep)
			}
			if cfg.ContractLimits.MaxTimeoutSeconds != tt.expected.MaxTimeoutSeconds {
				t.Errorf("MaxTimeoutSeconds = %d, want %d", cfg.ContractLimits.MaxTimeoutSeconds, tt.expected.MaxTimeoutSeconds)
			}
			if cfg.ContractLimits.MaxSubAgents != tt.expected.MaxSubAgents {
				t.Errorf("MaxSubAgents = %d, want %d", cfg.ContractLimits.MaxSubAgents, tt.expected.MaxSubAgents)
			}
			if cfg.ContractLimits.MaxInputLength != tt.expected.MaxInputLength {
				t.Errorf("MaxInputLength = %d, want %d", cfg.ContractLimits.MaxInputLength, tt.expected.MaxInputLength)
			}
			if len(cfg.ContractLimits.Scopes) != len(tt.expected.Scopes) {
				t.Fatalf("Scopes length = %d, want %d", len(cfg.ContractLimits.Scopes), len(tt.expected.Scopes))
			}
			for i, want := range tt.expected.Scopes {
				if cfg.ContractLimits.Scopes[i] != want {
					t.Errorf("Scopes[%d] = %q, want %q", i, cfg.ContractLimits.Scopes[i], want)
				}
			}
		})
	}
}

// chdir 在测试期间切换工作目录。通过 t.Cleanup 自动恢复。
func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}
