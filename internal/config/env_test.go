package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGetenvDotEnvPriority 验证默认 .env 优先策略。
func TestGetenvDotEnvPriority(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "FOO=fromfile\nBAR=fromfile\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	// 系统环境变量设置 FOO，.env 中 FOO 应优先。
	t.Setenv("FOO", "fromenv")
	ResetEnvCache()
	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}

	if got := Getenv("FOO"); got != "fromfile" {
		t.Errorf("FOO: got %q, want fromfile (dotenv priority)", got)
	}
	if got := Getenv("BAR"); got != "fromfile" {
		t.Errorf("BAR: got %q, want fromfile", got)
	}
	if got := Getenv("UNKNOWN"); got != "" {
		t.Errorf("UNKNOWN: got %q, want empty", got)
	}
}

// TestGetenvOSPriority 验证 SetOSFirst 后系统环境变量优先。
func TestGetenvOSPriority(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("FOO=fromfile\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("FOO", "fromenv")
	ResetEnvCache()
	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	SetOSFirst()

	if got := Getenv("FOO"); got != "fromenv" {
		t.Errorf("FOO: got %q, want fromenv (os priority)", got)
	}
}

// TestLookupEnvReportsSources 验证 LookupEnv 正确报告两个来源的存在性。
func TestLookupEnvReportsSources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("FOO=fromfile\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	// 先清理 BAR 系统环境变量，避免跨测试残留影响存在性判断。
	os.Unsetenv("BAR")

	t.Setenv("FOO", "fromenv")
	ResetEnvCache()
	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}

	res := LookupEnv("FOO")
	if res.Value != "fromfile" {
		t.Errorf("Value = %q, want fromfile", res.Value)
	}
	if !res.InDotEnv {
		t.Error("expected InDotEnv=true")
	}
	if !res.InOS {
		t.Error("expected InOS=true")
	}

	res2 := LookupEnv("BAR")
	if res2.Value != "" || res2.InDotEnv || res2.InOS {
		t.Errorf("BAR should be absent everywhere, got %+v", res2)
	}
}

// TestResetEnvCache 验证 ResetEnvCache 可清空 .env 缓存。
func TestResetEnvCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("FOO=fromfile\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	t.Setenv("FOO", "fromenv")
	ResetEnvCache()
	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if got := Getenv("FOO"); got != "fromfile" {
		t.Fatalf("before reset: got %q, want fromfile", got)
	}

	ResetEnvCache()
	if got := Getenv("FOO"); got != "fromenv" {
		t.Errorf("after reset: got %q, want fromenv", got)
	}
}
