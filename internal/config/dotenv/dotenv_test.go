package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

// newTestDotenv 创建一个独立 Dotenv 实例，避免污染全局默认实例。
func newTestDotenv(t *testing.T) *Dotenv {
	t.Helper()
	d := New()
	d.SetDotEnvFirst()
	return d
}

// TestDotEnvPriority 验证默认 .env 优先策略。
func TestDotEnvPriority(t *testing.T) {
	d := newTestDotenv(t)
	path := writeEnv(t, "FOO=fromfile\nBAR=fromfile\n")
	t.Setenv("FOO", "fromenv")
	if err := d.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if got := d.Getenv("FOO"); got != "fromfile" {
		t.Errorf("FOO: got %q, want fromfile (dotenv priority)", got)
	}
	if got := d.Getenv("BAR"); got != "fromfile" {
		t.Errorf("BAR: got %q, want fromfile", got)
	}
	if got := d.Getenv("UNKNOWN"); got != "" {
		t.Errorf("UNKNOWN: got %q, want empty", got)
	}
}

// TestOSPriority 验证 SetOSFirst 后系统环境变量优先。
func TestOSPriority(t *testing.T) {
	d := newTestDotenv(t)
	path := writeEnv(t, "FOO=fromfile\n")
	t.Setenv("FOO", "fromenv")
	if err := d.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	d.SetOSFirst()

	if got := d.Getenv("FOO"); got != "fromenv" {
		t.Errorf("FOO: got %q, want fromenv (os priority)", got)
	}
}

// TestLookupEnvReportsSources 验证 LookupEnv 正确报告两个来源的存在性。
func TestLookupEnvReportsSources(t *testing.T) {
	d := newTestDotenv(t)
	path := writeEnv(t, "FOO=fromfile\n")
	os.Unsetenv("BAR")
	t.Setenv("FOO", "fromenv")
	if err := d.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	res := d.LookupEnv("FOO")
	if res.Value != "fromfile" {
		t.Errorf("Value = %q, want fromfile", res.Value)
	}
	if !res.InDotEnv {
		t.Error("expected InDotEnv=true")
	}
	if !res.InOS {
		t.Error("expected InOS=true")
	}

	res2 := d.LookupEnv("BAR")
	if res2.Value != "" || res2.InDotEnv || res2.InOS {
		t.Errorf("BAR should be absent everywhere, got %+v", res2)
	}
}

// TestResetCache 验证 ResetCache 可清空 .env 缓存。
func TestResetCache(t *testing.T) {
	d := newTestDotenv(t)
	path := writeEnv(t, "FOO=fromfile\n")
	t.Setenv("FOO", "fromenv")
	if err := d.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got := d.Getenv("FOO"); got != "fromfile" {
		t.Fatalf("before reset: got %q, want fromfile", got)
	}

	d.ResetCache()
	if got := d.Getenv("FOO"); got != "fromenv" {
		t.Errorf("after reset: got %q, want fromenv", got)
	}
}

// TestGodotenvQuotedValue 验证 godotenv 正确处理带引号的值。
func TestGodotenvQuotedValue(t *testing.T) {
	d := newTestDotenv(t)
	path := writeEnv(t, `FOO="bar"
BAZ='qux'
`)
	if err := d.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got := d.Getenv("FOO"); got != "bar" {
		t.Errorf("FOO: got %q, want bar", got)
	}
	if got := d.Getenv("BAZ"); got != "qux" {
		t.Errorf("BAZ: got %q, want qux", got)
	}
}

// TestGodotenvInlineComment 验证行内注释被忽略。
func TestGodotenvInlineComment(t *testing.T) {
	d := newTestDotenv(t)
	path := writeEnv(t, "FOO=bar # comment\n")
	if err := d.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got := d.Getenv("FOO"); got != "bar" {
		t.Errorf("FOO: got %q, want bar", got)
	}
}

// TestGodotenvExportPrefix 验证 export 前缀支持。
func TestGodotenvExportPrefix(t *testing.T) {
	d := newTestDotenv(t)
	path := writeEnv(t, "export FOO=bar\n")
	if err := d.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got := d.Getenv("FOO"); got != "bar" {
		t.Errorf("FOO: got %q, want bar", got)
	}
}

// TestHelpers 验证便捷辅助函数。
func TestHelpers(t *testing.T) {
	d := newTestDotenv(t)
	path := writeEnv(t, "FLAG=true\nNUM=42\nEMPTY=\n")
	if err := d.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	if !d.MustBool("FLAG", false) {
		t.Error("FLAG should be true")
	}
	if d.MustBool("MISSING", true) != true {
		t.Error("MISSING bool should fall back to default true")
	}
	if d.MustInt("NUM", 0) != 42 {
		t.Errorf("NUM: got %d, want 42", d.MustInt("NUM", 0))
	}
	if d.MustInt("MISSING", 7) != 7 {
		t.Errorf("MISSING int should fall back to 7")
	}
	if d.GetenvWithDefault("MISSING", "def") != "def" {
		t.Error("GetenvWithDefault fallback failed")
	}
	if d.GetenvWithDefault("FLAG", "def") != "true" {
		t.Error("GetenvWithDefault should return raw value")
	}
}

// TestLoadFileMissingIsNotError 验证加载不存在的 .env 不报错且清空缓存。
func TestLoadFileMissingIsNotError(t *testing.T) {
	d := newTestDotenv(t)
	if err := d.LoadFile(filepath.Join(t.TempDir(), "missing.env")); err != nil {
		t.Fatalf("LoadFile on missing file: %v", err)
	}
	if got := d.Getenv("FOO"); got != "" {
		t.Errorf("FOO should be empty after missing load, got %q", got)
	}
}

// TestApplyEnvFileToOS 验证 legacy ApplyEnvFileToOS 行为。
func TestApplyEnvFileToOS(t *testing.T) {
	d := newTestDotenv(t)
	path := writeEnv(t, "FOO=fromfile\nBAR=fromfile\n")
	t.Setenv("FOO", "fromenv")
	if err := d.ApplyEnvFileToOS(path); err != nil {
		t.Fatalf("ApplyEnvFileToOS: %v", err)
	}
	if got := os.Getenv("FOO"); got != "fromenv" {
		t.Errorf("FOO should remain fromenv, got %q", got)
	}
	if got := os.Getenv("BAR"); got != "fromfile" {
		t.Errorf("BAR should be fromfile, got %q", got)
	}
}

// TestNewWithPath 验证 NewWithPath 构造并加载实例。
func TestNewWithPath(t *testing.T) {
	path := writeEnv(t, "FOO=bar\n")
	t.Setenv("FOO", "fromenv")
	d, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath: %v", err)
	}
	if got := d.Getenv("FOO"); got != "bar" {
		t.Errorf("FOO: got %q, want bar", got)
	}
	// 独立实例不应影响默认实例。
	if got := Default().Getenv("FOO"); got != "fromenv" {
		t.Errorf("default instance FOO should remain fromenv, got %q", got)
	}
}

// TestDefaultInstanceAutoReload 验证默认实例在 init 时自动加载默认 .env。
// 由于测试 CWD 是项目根，应能读到 GEMINI_API_KEY（若 .env 存在）。
// 该测试只验证默认实例非空且行为正常，不依赖具体 key。
func TestDefaultInstanceAutoReload(t *testing.T) {
	// 用 Set 修改默认实例的缓存不影响 os env；这里仅确认默认实例可访问。
	Default().Set("DOTENV_TEST_MARKER", "ok")
	if got := Default().Getenv("DOTENV_TEST_MARKER"); got != "ok" {
		t.Errorf("default instance marker: got %q, want ok", got)
	}
}

// writeEnv 是测试辅助函数，生成临时 .env 文件并返回其路径。
func writeEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	return path
}
