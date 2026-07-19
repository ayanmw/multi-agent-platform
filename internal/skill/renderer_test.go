package skill

import "testing"

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
