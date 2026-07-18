package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestShouldMockPriority verifies the three-layer mock switch priority using
// table-driven subtests. Each subtest builds a Config directly (no Load) and
// exercises ShouldMock.
//
// Priority (highest first):
//  1) LLMMockEndpoints contains caseID or endpointHint → force mock
//  2) LLMRealCases contains caseID → force real
//  3) LLMUseMock == true → mock
//  4) otherwise → real
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
		// Layer 1: mock endpoints force mock, regardless of useMock and realCases.
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

		// Layer 2: real cases force real, regardless of useMock=true.
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

		// Layer 3 & 4: useMock default.
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

		// Boundary: empty caseID and empty endpointHint.
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
			mockEndpoints: []string{""}, // empty element — contains() guards on value != ""
			caseID:        "",
			want:          false,
		},

		// Case-insensitivity: source uses strings.EqualFold.
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

		// Nil vs empty slice equivalence.
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

// TestLoadEnvParsing covers the environment-variable parsing paths of Load.
// Each subtest chdir's into a temp dir without a .env file so that only
// t.Setenv values influence the result.
func TestLoadEnvParsing(t *testing.T) {
	// Helper that runs fn inside a clean temp working directory with all
	// relevant env vars cleared, so each subtest starts from a known baseline.
	withCleanEnv := func(t *testing.T, fn func(t *testing.T)) {
		t.Helper()
		// Use a temp dir so loadEnvFile(".env") finds no file.
		dir := t.TempDir()
		chdir(t, dir)
		// Clear all LLM_* / DB_PATH / SERVER_PORT vars for a deterministic base.
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
}

// TestSplitAndTrim covers the unexported helper directly (white-box).
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

// only keys that are not already present in the environment.
func TestLoadEnvFileLoadsVarsAndDoesNotOverrideExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "FOO=fromfile\nBAR=fromfile\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	// Pre-set FOO in environment; it must NOT be overridden by the file.
	t.Setenv("FOO", "fromenv")
	// BAR should be loaded from file.
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

// TestLoadEnvFileMissingReturnsError verifies that a missing .env file yields
// an error (caller is expected to treat it as non-fatal).
func TestLoadEnvFileMissingReturnsError(t *testing.T) {
	if err := loadEnvFile(filepath.Join(t.TempDir(), "missing.env")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestGetAgentConfigNotImplemented verifies the stub returns an error.
func TestGetAgentConfigNotImplemented(t *testing.T) {
	_, err := GetAgentConfig("any")
	if err == nil {
		t.Fatal("expected error from unimplemented GetAgentConfig")
	}
}

// chdir changes the working directory for the duration of the test. Restored
// automatically via t.Cleanup.
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
