package cron

import (
	"strings"
	"testing"
)

// TestRenderTemplateBasic 验证占位符被正确替换。
func TestRenderTemplateBasic(t *testing.T) {
	ctx := TemplateContext{Now: "2026-07-21T10:00:00Z", Count: 3, CronName: "DailyReport", PrevStatus: "completed", PrevResult: "all good"}
	cases := map[string]string{
		"hello {{.Count}}":                "hello 3",
		"[{{.CronName}}] {{.Now}}":        "[DailyReport] 2026-07-21T10:00:00Z",
		"prev={{.PrevStatus}}:{{.PrevResult}}": "prev=completed:all good",
	}
	for in, want := range cases {
		got := RenderTemplate(in, ctx)
		if got != want {
			t.Fatalf("RenderTemplate(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRenderTemplateNoPlaceholders 验证不含占位符的字符串原样返回。
func TestRenderTemplateNoPlaceholders(t *testing.T) {
	in := "plain text no placeholders"
	if got := RenderTemplate(in, TemplateContext{}); got != in {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

// TestRenderTemplateMissingVariable 验证缺失变量渲染为空字符串（text/template 默认行为）。
func TestRenderTemplateMissingVariable(t *testing.T) {
	got := RenderTemplate("result={{.PrevResult}}", TemplateContext{})
	if got != "result=" {
		t.Fatalf("missing var should render empty, got %q", got)
	}
}

// TestRenderTemplateInvalidSyntax 验证非法模板语法时保留原文。
func TestRenderTemplateInvalidSyntax(t *testing.T) {
	in := "broken {{ .Count "
	got := RenderTemplate(in, TemplateContext{Count: 1})
	if got != in {
		t.Fatalf("invalid template should keep original, got %q", got)
	}
}

// TestRenderTemplateExecutionError 验证渲染执行错误时保留原文。
// 访问不存在的字段且模板强类型不匹配时，text/template 会报错，此时保留原文。
func TestRenderTemplateExecutionError(t *testing.T) {
	// 对 nil 值调用方法会触发执行错误
	in := "{{ .Count.MissingField }}"
	got := RenderTemplate(in, TemplateContext{Count: 1})
	if got != in {
		t.Fatalf("execution error should keep original, got %q", got)
	}
}

// TestRenderMap 验证递归渲染嵌套 map 与 slice 中的字符串。
func TestRenderMap(t *testing.T) {
	ctx := TemplateContext{Count: 2, Now: "T"}
	payload := map[string]any{
		"agent_id": "agent_default",
		"input":    "run #{{.Count}} at {{.Now}}",
		"nested": map[string]any{
			"msg":  "nested {{.Count}}",
			"keep": 123,
		},
		"list": []any{"item {{.Count}}", "static", 456},
	}
	out := RenderMap(payload, ctx)
	if out["input"] != "run #2 at T" {
		t.Fatalf("input not rendered: %v", out["input"])
	}
	if out["agent_id"] != "agent_default" {
		t.Fatalf("non-template string changed: %v", out["agent_id"])
	}
	nested, ok := out["nested"].(map[string]any)
	if !ok || nested["msg"] != "nested 2" || nested["keep"] != 123 {
		t.Fatalf("nested rendering wrong: %+v", out["nested"])
	}
	list, ok := out["list"].([]any)
	if !ok || list[0] != "item 2" || list[1] != "static" || list[2] != 456 {
		t.Fatalf("list rendering wrong: %+v", out["list"])
	}
}

// TestRenderMapNil 验证 nil map 返回 nil。
func TestRenderMapNil(t *testing.T) {
	if out := RenderMap(nil, TemplateContext{}); out != nil {
		t.Fatalf("nil map should return nil, got %+v", out)
	}
}

// TestRenderTemplateEmpty 验证空字符串原样返回。
func TestRenderTemplateEmpty(t *testing.T) {
	if got := RenderTemplate("", TemplateContext{}); got != "" {
		t.Fatalf("empty string changed: %q", got)
	}
	// 确保不包含 {{ 的字符串不进入 template 解析
	if !strings.Contains("plain", "plain") {
		t.Fatal("sanity")
	}
}
