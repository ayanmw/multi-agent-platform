package skill

import (
	"testing"
)

func TestRegistryRegisterAndList(t *testing.T) {
	r := NewRegistry()
	s := Skill{ID: "foo/bar", Source: SkillSourceLocalDB, State: SkillStateLoaded}
	r.Register(s)

	if got, ok := r.Get("foo/bar"); !ok || got.ID != "foo/bar" {
		t.Fatalf("expected to retrieve registered skill")
	}
	if r.Exists("foo/bar") != true {
		t.Fatalf("expected skill to exist")
	}

	l := r.List(nil)
	if len(l) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(l))
	}
	src := SkillSourceLocalDB
	l2 := r.List(&src)
	if len(l2) != 1 {
		t.Fatalf("expected 1 skill filtered by source, got %d", len(l2))
	}
	srcOther := SkillSourceBuiltIn
	l3 := r.List(&srcOther)
	if len(l3) != 0 {
		t.Fatalf("expected 0 skills with wrong source filter, got %d", len(l3))
	}

	if ok := r.UpdateState("foo/bar", SkillStateEnabled); !ok {
		t.Fatalf("UpdateState should succeed")
	}
	if got, _ := r.Get("foo/bar"); got.State != SkillStateEnabled {
		t.Fatalf("expected state to be enabled, got %s", got.State)
	}

	r.Unregister("foo/bar")
	if l := r.List(nil); len(l) != 0 {
		t.Fatalf("expected 0 skills after unregister, got %d", len(l))
	}
}

func TestResolveActiveSkills(t *testing.T) {
	r := NewRegistry()
	r.Register(Skill{ID: "global-skill", Source: SkillSourceBuiltIn, State: SkillStateEnabled, Scope: SkillScopeGlobal})
	r.Register(Skill{ID: "proj-a-skill", Source: SkillSourceLocalDB, State: SkillStateEnabled, Scope: SkillScopeProject, ProjectID: "proj-a"})
	r.Register(Skill{ID: "proj-b-skill", Source: SkillSourceLocalDB, State: SkillStateEnabled, Scope: SkillScopeProject, ProjectID: "proj-b"})
	r.Register(Skill{ID: "disabled-skill", Source: SkillSourceLocalDB, State: SkillStateDisabled, Scope: SkillScopeGlobal})
	r.Register(Skill{ID: "session-skill", Source: SkillSourceLocalDB, State: SkillStateEnabled, Scope: SkillScopeSession})

	// session 无 project：只应拿到 global skill。
	ids := ResolveActiveSkills(r, "", "/tmp/ws")
	if len(ids) != 1 || ids[0] != "global-skill" {
		t.Fatalf("expected [global-skill] for empty project, got %v", ids)
	}

	// session 属于 proj-a：拿到 global + proj-a。
	ids = ResolveActiveSkills(r, "proj-a", "/tmp/ws")
	got := toSet(ids)
	want := map[string]bool{"global-skill": true, "proj-a-skill": true}
	if len(got) != len(want) {
		t.Fatalf("expected global+proj-a for proj-a, got %v", ids)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("missing %s in result %v", k, ids)
		}
	}
}

func TestResolveActiveSkillsByWorkspaceDir(t *testing.T) {
	r := NewRegistry()
	r.Register(Skill{ID: "global-skill", Source: SkillSourceBuiltIn, State: SkillStateEnabled, Scope: SkillScopeGlobal})
	r.Register(Skill{ID: "ws-skill", Source: SkillSourceLocalDB, State: SkillStateEnabled, Scope: SkillScopeProject, WorkspaceDir: "/home/proj/src"})

	ids := ResolveActiveSkills(r, "", "/home/proj/src/sub")
	got := toSet(ids)
	want := map[string]bool{"global-skill": true, "ws-skill": true}
	for k := range want {
		if !got[k] {
			t.Fatalf("missing %s in result %v", k, ids)
		}
	}

	// 不匹配的 workdir 不应入选。
	ids = ResolveActiveSkills(r, "", "/other/path")
	if len(ids) != 1 || ids[0] != "global-skill" {
		t.Fatalf("expected [global-skill] for unrelated workdir, got %v", ids)
	}
}

func TestResolveActiveSkillsProjectOverridesGlobal(t *testing.T) {
	r := NewRegistry()
	r.Register(Skill{ID: "same-id", Source: SkillSourceBuiltIn, State: SkillStateEnabled, Scope: SkillScopeGlobal})
	r.Register(Skill{ID: "same-id", Source: SkillSourceLocalDB, State: SkillStateEnabled, Scope: SkillScopeProject, ProjectID: "proj-x"})

	ids := ResolveActiveSkills(r, "proj-x", "")
	if len(ids) != 1 {
		t.Fatalf("expected 1 after dedup, got %v", ids)
	}
	if ids[0] != "same-id" {
		t.Fatalf("expected id same-id, got %s", ids[0])
	}
	// 无法通过返回值直接判断来源，但 project 必须覆盖 global（engine 只会渲染一个）。
}

func TestSkillMatchesScope(t *testing.T) {
	s := Skill{ID: "g", Scope: SkillScopeGlobal, State: SkillStateEnabled}
	if !skillMatchesScope(s, "", "") {
		t.Fatalf("global skill should always match")
	}

	s = Skill{ID: "p", Scope: SkillScopeProject, ProjectID: "p1", State: SkillStateEnabled}
	if !skillMatchesScope(s, "p1", "") {
		t.Fatalf("project skill should match by projectID")
	}
	if skillMatchesScope(s, "p2", "") {
		t.Fatalf("project skill should not match wrong projectID")
	}

	s = Skill{ID: "ws", Scope: SkillScopeProject, WorkspaceDir: "/a/b", State: SkillStateEnabled}
	if !skillMatchesScope(s, "", "/a/b/c") {
		t.Fatalf("project skill should match by workspace subdir")
	}
	if skillMatchesScope(s, "", "/a/bc") {
		t.Fatalf("project skill should not match false prefix")
	}

	s = Skill{ID: "sess", Scope: SkillScopeSession, State: SkillStateEnabled}
	if skillMatchesScope(s, "", "") {
		t.Fatalf("session scope should not be injected yet")
	}
}

func toSet(ids []string) map[string]bool {
	m := make(map[string]bool)
	for _, id := range ids {
		m[id] = true
	}
	return m
}
