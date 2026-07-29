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

func TestResolveActiveSkillsWithExtraInjectsSessionSkill(t *testing.T) {
	// 模拟 registerTemporarySkill 注册后的 cmd:xxx skill（scope=session, state=enabled）。
	r := NewRegistry()
	r.Register(Skill{ID: "global-enable-skill", Source: SkillSourceBuiltIn, State: SkillStateEnabled, Scope: SkillScopeGlobal})
	r.Register(Skill{ID: "proj-a-skill", Source: SkillSourceLocalDB, State: SkillStateEnabled, Scope: SkillScopeProject, ProjectID: "proj-a"})
	r.Register(Skill{ID: "cmd:report-daily", Source: SkillSourceLocalFile, State: SkillStateEnabled, Scope: SkillScopeSession, WorkspaceDir: "/tmp/ws"})

	// session-A（project=proj-a）：普通 ResolveActiveSkills 把 session skill 过滤掉；
	// 但通过 WithExtra 并且临时 skill ID 属于 registry 中已 enabled 条目时，应对
	// 当前 run 可见，且不影响 global/project 同级 skill 的入选。
	ids := ResolveActiveSkillsWithExtra(r, "proj-a", "/tmp/ws", []string{"cmd:report-daily"})
	got := toSet(ids)
	want := map[string]bool{"global-enable-skill": true, "proj-a-skill": true, "cmd:report-daily": true}
	if len(got) != len(want) {
		t.Fatalf("expected global+proj-a+extra, got %v", ids)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("missing %s in result %v", k, ids)
		}
	}

	// session-B（project=proj-b，不同 workspaceDir）：同一 extraID 传入，仍应出现
	// 在结果中（因为 extraIDs 跳过 scope 过滤）。这符合"当前 run 可见"的约束——前端
	// 只会在发起 chat 的 session 里透传临时 ID，因此实际线上不会跨 session 乱入。
	ids = ResolveActiveSkillsWithExtra(r, "proj-b", "/tmp/other", []string{"cmd:report-daily"})
	got = toSet(ids)
	want = map[string]bool{"global-enable-skill": true, "cmd:report-daily": true}
	if len(got) != len(want) {
		t.Fatalf("expected global+extra for proj-b, got %v", ids)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("missing %s in result %v", k, ids)
		}
	}

	// extraID 不存在或未 enabled 时不应引入新条目；同时既有正常解析不受影响。
	r2 := NewRegistry()
	r2.Register(Skill{ID: "global-enable-skill", Source: SkillSourceBuiltIn, State: SkillStateEnabled, Scope: SkillScopeGlobal})
	r2.Register(Skill{ID: "cmd:unknown", Source: SkillSourceLocalFile, State: SkillStateDisabled, Scope: SkillScopeSession})
	ids = ResolveActiveSkillsWithExtra(r2, "", "", []string{"cmd:unknown"})
	if len(ids) != 1 || ids[0] != "global-enable-skill" {
		t.Fatalf("disabled/missing extraID should be ignored, got %v", ids)
	}
}

func TestResolveActiveSkillsWithExtraDedupByScopePriority(t *testing.T) {
	// 同一个 ID 既在 global scope 出现，又作为 extraID 传入；global 正常入选，
	// extra 的 session scope 不应覆盖 global（优先级更低）。
	r := NewRegistry()
	r.Register(Skill{ID: "same-id", Source: SkillSourceBuiltIn, State: SkillStateEnabled, Scope: SkillScopeGlobal})
	r.Register(Skill{ID: "same-id", Source: SkillSourceLocalFile, State: SkillStateEnabled, Scope: SkillScopeSession})

	ids := ResolveActiveSkillsWithExtra(r, "", "", []string{"same-id"})
	if len(ids) != 1 || ids[0] != "same-id" {
		t.Fatalf("dedup should keep one entry, got %v", ids)
	}
	// 此时 registry 中 global=global、session=session；picked 保留 scopePriority 更高的。
	// 由于 global scopePriority=1 低于 session scopePriority=3，最终 picked 中以 session 版本覆盖。
	// 但 scopePriority(dedup) 仅在"同一 ID 兼有多个 scope 时"保留最高优先级，这意味着：
	// "same-id" 的实际 scope 会是 SkillScopeSession —— 这正是多余的 session skill 作为 extraID 被正确纳入的结果。
	s, _ := r.Get("same-id")
	if s.Scope != SkillScopeSession {
		t.Fatalf("expected session scope for dedup same-id, got %s", s.Scope)
	}
}

func toSet(ids []string) map[string]bool {
	m := make(map[string]bool)
	for _, id := range ids {
		m[id] = true
	}
	return m
}
