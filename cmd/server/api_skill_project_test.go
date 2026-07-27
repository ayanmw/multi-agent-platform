package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
)

// TestSkillProjectScopeAPI 验证 project scope skill 的创建、查询过滤与运行期解析逻辑。
func TestSkillProjectScopeAPI(t *testing.T) {
	ts, registry, _ := newSkillTestHarness(t)
	client := ts.Client()

	// 创建一个 project scope skill。
	createPayload, _ := json.Marshal(map[string]any{
		"id":            "proj/go-helper",
		"display_name":  "Go Helper",
		"description":   "project scoped go helper",
		"content":       "You are a Go expert for project {{project_id}} at {{workspace_dir}}.",
		"scope":         "project",
		"project_id":    "proj-go",
		"workspace_dir": "/home/proj-go",
	})
	resp, err := client.Post(ts.URL+"/api/skills", "application/json", bytes.NewReader(createPayload))
	if err != nil {
		t.Fatalf("create project skill: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.StatusCode, body)
	}
	var created skill.Skill
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Scope != skill.SkillScopeProject || created.ProjectID != "proj-go" || created.WorkspaceDir != "/home/proj-go" {
		t.Fatalf("unexpected project skill fields: %+v", created)
	}

	// GET /api/skills?scope=project 应返回 project skill。
	resp, err = client.Get(ts.URL + "/api/skills?scope=project")
	if err != nil {
		t.Fatalf("list project skills: %v", err)
	}
	body = readBody(t, resp)
	var listed []skill.Skill
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, s := range listed {
		if s.ID == "proj/go-helper" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected project skill in list, got %v", listed)
	}

	// GET /api/skills?project_id=proj-go 过滤。
	resp, err = client.Get(ts.URL + "/api/skills?project_id=proj-go")
	if err != nil {
		t.Fatalf("filter by project_id: %v", err)
	}
	body = readBody(t, resp)
	listed = nil
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "proj/go-helper" {
		t.Fatalf("expected exactly proj/go-helper, got %v", listed)
	}

	// PUT 修改 scope 相关字段。
	updatePayload, _ := json.Marshal(map[string]any{
		"project_id": "proj-go-v2",
	})
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/skills/proj/go-helper", bytes.NewReader(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("update scope: %v", err)
	}
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	var updated skill.Skill
	if err := json.Unmarshal([]byte(body), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.ProjectID != "proj-go-v2" {
		t.Fatalf("expected project_id updated, got %s", updated.ProjectID)
	}

	// 验证运行期解析：project 匹配时应激活。
	ids := skill.ResolveActiveSkills(registry, "proj-go-v2", "/home/proj-go/sub")
	if !slices.Contains(ids, "proj/go-helper") {
		t.Fatalf("expected proj/go-helper active for matching project/workdir, got %v", ids)
	}

	// 不匹配的 project 不应激活。
	ids = skill.ResolveActiveSkills(registry, "other-proj", "")
	if slices.Contains(ids, "proj/go-helper") {
		t.Fatalf("project skill should not be active for other project")
	}
}
