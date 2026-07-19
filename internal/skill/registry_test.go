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
