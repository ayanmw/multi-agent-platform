package skill

import "testing"

// TestRenderNilSafe 验证 Renderer.Render 在边界输入下不 panic：
//   (1) 模板 content 为空串；
//   (2) vars 为 nil；
//   (3) 模板无占位符原样返回。
//
// 已知限制：本实现签名仅接收 map[string]any variables，不接收 SkillParameter 列表，
// 因此 "preferring SkillParameter.Default" 回退暂未实现。若后续扩展签名，
// 需补充 Default 回退相关用例。
func TestRenderNilSafe(t *testing.T) {
	r := NewRenderer()

	// (1) 空模板不 panic，返回空串。
	got := r.Render(SkillTemplate{Content: ""}, nil)
	if got != "" {
		t.Fatalf("empty content Render = %q, want empty", got)
	}

	// (2) vars 为 nil 不 panic，变量未匹配时保留占位符。
	got = r.Render(SkillTemplate{Content: "{{greeting}} {{name}}"}, nil)
	if got != "{{greeting}} {{name}}" {
		t.Fatalf("nil vars Render = %q, want placeholders preserved", got)
	}

	// (3) 无占位符原样返回。
	got = r.Render(SkillTemplate{Content: "plain text, no placeholders"}, map[string]any{
		"greeting": "hi",
	})
	if got != "plain text, no placeholders" {
		t.Fatalf("no placeholder Render = %q, want original", got)
	}
}

func TestRendererRender(t *testing.T) {
	r := NewRenderer()
	tmpl := SkillTemplate{
		Content: "Hello {{name}}, your score is {{ score }} out of {{total}}.",
	}

	got := r.Render(tmpl, map[string]any{
		"name":  "Alice",
		"score": 42,
		"total": 100,
	})
	want := "Hello Alice, your score is 42 out of 100."
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}

	vars := r.ExtractVariables(tmpl.Content)
	if len(vars) != 3 {
		t.Fatalf("ExtractVariables len = %d, want 3", len(vars))
	}
	if vars[0] != "name" || vars[1] != "score" || vars[2] != "total" {
		t.Fatalf("ExtractVariables = %v, want [name score total]", vars)
	}

	// 缺失变量时保留原始占位符
	tmpl2 := SkillTemplate{Content: "Hi {{name}}, missing {{foo}}"}
	got2 := r.Render(tmpl2, map[string]any{"name": "Bob"})
	want2 := "Hi Bob, missing {{foo}}"
	if got2 != want2 {
		t.Fatalf("missing var Render = %q, want %q", got2, want2)
	}
}
