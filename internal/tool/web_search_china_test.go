package tool

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCallBaiduMobileParseResults(t *testing.T) {
	html := `
<!DOCTYPE html>
<html><body>
<div class="result">
  <a class="c-font-medium" href="https://example.com/1">Go 语言官网</a>
  <span class="content-right_8Zs40">Go 是 Google 开源的静态类型语言。</span>
</div>
<div class="result">
  <a class="c-font-medium" href="https://example.com/2">Go 入门</a>
  <span class="content-right_8Zs40">快速开始学习 Go。</span>
</div>
</body></html>`
	results := parseBaiduMobileHTML(html, 5)
	fmt.Printf("DEBUG orig results=%+v\n", results)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !strings.Contains(results[0].Title, "Go 语言官网") {
		t.Fatalf("unexpected title: %s", results[0].Title)
	}
}

func TestCallSogouParseResults(t *testing.T) {
	html := `
<!DOCTYPE html>
<html><body>
<div class="vrwrap">
  <h3 class="vr-title"><a href="/link?url=example1">Go 语言</a></h3>
  <div class="str-text-info">Go 是 Google 开源语言。</div>
</div>
<div class="vrwrap">
  <h3 class="vr-title"><a href="/link?url=example2">Go 教程</a></h3>
  <div class="str-text-info">Go 入门指南。</div>
</div>
</body></html>`
	results := parseSogouHTML(html, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].URL != "https://www.sogou.com/link?url=example1" {
		t.Fatalf("unexpected url: %s", results[0].URL)
	}
}

func TestCallBingCnHTMLParseResults(t *testing.T) {
	html := `
<!DOCTYPE html>
<html><body>
<li class="b_algo">
  <h2><a href="https://example.com/1">Go 语言</a></h2>
  <div class="b_caption"><p>Go is an open source programming language.</p></div>
</li>
</body></html>`
	results := parseBingCnHTML(html, 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0].Desc, "open source") {
		t.Fatalf("unexpected desc: %s", results[0].Desc)
	}
}

func TestBaiduMobileZeroResults(t *testing.T) {
	results := parseBaiduMobileHTML("<html><body></body></html>", 5)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestWebSearchExplicitProviderBaidu(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`
<!DOCTYPE html>
<html><body>
<div class="result">
  <a class="c-font-medium" href="https://go.dev/">Go</a>
  <span class="content-right_8Zs40">Go programming language</span>
</div>
</body></html>`))
	}))
	defer srv.Close()

	client := srv.Client()
	client.Transport = &rewriteHostTransport{base: client.Transport, host: srv.URL}
	cfg := WebSearchConfig{Provider: "baidu", HTTPClient: client, Timeout: 5 * time.Second}

	res, err := webSearchExecutor(cfg, map[string]any{"query": "go", "num_results": 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := res.(map[string]any)
	if out["provider"] != "baidu" {
		t.Fatalf("expected baidu provider, got %v", out["provider"])
	}
	text := out["text"].(string)
	if !strings.Contains(text, "Go programming language") {
		t.Fatalf("expected parsed baidu result, got:\n%s", text)
	}
}
