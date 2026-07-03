package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Config holds application configuration loaded from environment and .env
type Config struct {
	LLMEndpoint string
	LLMAPIKey   string
	LLMModel    string
	DBPath      string
	ServerPort  string
}

// Load reads .env file and environment variables to populate Config
func Load() (*Config, error) {
	cfg := &Config{
		LLMEndpoint: "https://aicoding.dobest.com/v1",
		LLMModel:    "deepseek-v4-flash",
		DBPath:      "data/app.db",
		ServerPort:  "8080",
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

	return cfg, nil
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

// GetAgentConfig loads an agent's configuration from the database
// Returns nil if agent not found
func GetAgentConfig(agentID string) (*AgentConfig, error) {
	// TODO: Implement DB query when persistence layer is ready
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