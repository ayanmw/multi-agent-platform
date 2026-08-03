package tool

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestRealSogouSearch 用真实网络请求搜狗，验证 provider 能拿到自然结果。
// 默认 Skip，避免 CI 不稳定；手动运行时加 -run TestRealSogouSearch。
func TestRealSogouSearch(t *testing.T) {
	requireRealNetwork(t)
	cfg := WebSearchConfig{Timeout: 25 * time.Second, UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"}
	ctx := t.Context()
	text, err := callSogou(ctx, cfg, "Go 语言", 3)
	if err != nil {
		t.Fatalf("sogou failed: %v", err)
	}
	if text == "" || !strings.Contains(text, "http") {
		t.Fatalf("expected results, got: %s", text)
	}
	fmt.Printf("Sogou results:\n%s\n", text)
}

// TestRealBingCnSearch 用真实网络请求必应中国，验证 provider 能拿到自然结果。
func TestRealBingCnSearch(t *testing.T) {
	requireRealNetwork(t)
	cfg := WebSearchConfig{Timeout: 25 * time.Second, UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"}
	ctx := t.Context()
	text, err := callBingCnHTML(ctx, cfg, "Go 语言", 3)
	if err != nil {
		t.Fatalf("bing cn failed: %v", err)
	}
	if text == "" || !strings.Contains(text, "http") {
		t.Fatalf("expected results, got: %s", text)
	}
	fmt.Printf("Bing CN results:\n%s\n", text)
}

// TestRealBaiduSearch 用真实网络请求百度移动版，验证 provider 能拿到自然结果。
// 百度对未登录请求较容易跳验证码，本测试只验证函数不 panic，并打印返回内容。
func TestRealBaiduSearch(t *testing.T) {
	requireRealNetwork(t)
	cfg := WebSearchConfig{Timeout: 25 * time.Second, UserAgent: "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36"}
	ctx := t.Context()
	text, err := callBaiduMobile(ctx, cfg, "Go 语言", 3)
	if err != nil {
		t.Skipf("baidu mobile failed (likely captcha): %v", err)
	}
	fmt.Printf("Baidu results:\n%s\n", text)
}
