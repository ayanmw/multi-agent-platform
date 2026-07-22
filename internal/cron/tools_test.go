package cron

import (
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/tool"
)

// toolTestSvc 用真实 Service + fake 依赖，便于工具调用真实校验/事件路径。
func toolTestSvc(t *testing.T) (*Service, *fakeStore, *fakeSched, *fakeExec2, *fakeEventBus) {
	return newSvc(t)
}

// TestCronToolsRegistered 验证 4 个工具都注册成功。
func TestCronToolsRegistered(t *testing.T) {
	svc, _, _, _, _ := toolTestSvc(t)
	reg := tool.NewRegistry()
	RegisterCronTools(reg, svc)
	want := []string{"cron/create", "cron/list", "cron/delete", "cron/trigger"}
	for _, name := range want {
		if _, err := reg.Execute(name, map[string]any{}); err != nil && err.Error() == "tool not found: "+name {
			t.Fatalf("tool %s not registered", name)
		}
	}
	// list 无参应能执行（返回空数组）
	res, err := reg.Execute("cron/list", map[string]any{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if res == nil {
		t.Fatal("list returned nil")
	}
}

// TestCronCreateTool 验证 create 工具创建成功。
func TestCronCreateTool(t *testing.T) {
	svc, store, sched, _, _ := toolTestSvc(t)
	reg := tool.NewRegistry()
	RegisterCronTools(reg, svc)

	res, err := reg.Execute("cron/create", map[string]any{
		"name":          "DailyCheck",
		"schedule_type": "interval",
		"cron_expr":     "1h",
		"action_type":   "start_task",
		"action_payload": map[string]any{"agent_id": "agent_default", "input": "check"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	c, ok := res.(*Cron)
	if !ok {
		t.Fatalf("expected *Cron, got %T", res)
	}
	if c.ID == "" || c.Source != "agent" {
		t.Fatalf("created cron wrong: %+v", c)
	}
	if _, err := store.GetCron(c.ID); err != nil {
		t.Fatalf("not persisted: %v", err)
	}
	if len(sched.adds) != 1 {
		t.Fatalf("scheduler not add-ed: %v", sched.adds)
	}
}

// TestCronCreateToolValidation 验证 create 工具透传 Service 校验。
func TestCronCreateToolValidation(t *testing.T) {
	svc, _, _, _, _ := toolTestSvc(t)
	reg := tool.NewRegistry()
	RegisterCronTools(reg, svc)

	// 缺 agent_id
	_, err := reg.Execute("cron/create", map[string]any{
		"name": "x", "schedule_type": "cron", "cron_expr": "* * * * * *",
		"action_type": "start_task", "action_payload": map[string]any{},
	})
	if err == nil {
		t.Fatal("expected validation error for missing agent_id")
	}
	// 非法 cron_expr
	_, err = reg.Execute("cron/create", map[string]any{
		"name": "x", "schedule_type": "cron", "cron_expr": "zzz",
		"action_type": "start_task", "action_payload": map[string]any{"agent_id": "a"},
	})
	if err == nil {
		t.Fatal("expected validation error for bad cron_expr")
	}
}

// TestCronDeleteTool 验证 delete 工具。
func TestCronDeleteTool(t *testing.T) {
	svc, _, _, _, _ := toolTestSvc(t)
	reg := tool.NewRegistry()
	RegisterCronTools(reg, svc)

	res, _ := reg.Execute("cron/create", map[string]any{
		"name": "x", "schedule_type": "cron", "cron_expr": "* * * * * *",
		"action_type": "start_task", "action_payload": map[string]any{"agent_id": "a"},
	})
	c := res.(*Cron)

	delRes, err := reg.Execute("cron/delete", map[string]any{"id": c.ID})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	m, ok := delRes.(map[string]any)
	if !ok || m["deleted"] != c.ID {
		t.Fatalf("delete result wrong: %+v", delRes)
	}
	// 再删应报错
	if _, err := reg.Execute("cron/delete", map[string]any{"id": c.ID}); err == nil {
		t.Fatal("expected error deleting missing cron")
	}
}

// TestCronTriggerTool 验证 trigger 工具。
func TestCronTriggerTool(t *testing.T) {
	svc, _, _, exec, _ := toolTestSvc(t)
	reg := tool.NewRegistry()
	RegisterCronTools(reg, svc)

	res, _ := reg.Execute("cron/create", map[string]any{
		"name": "x", "schedule_type": "cron", "cron_expr": "* * * * * *",
		"action_type": "start_task", "action_payload": map[string]any{"agent_id": "a"},
	})
	c := res.(*Cron)

	if _, err := reg.Execute("cron/trigger", map[string]any{"id": c.ID, "override_input": "custom"}); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if exec.calls.Load() != 1 || exec.override != "custom" {
		t.Fatalf("trigger did not invoke executor: calls=%d override=%q", exec.calls.Load(), exec.override)
	}
}

// TestCronListToolFilter 验证 list 工具 status 过滤。
func TestCronListToolFilter(t *testing.T) {
	svc, _, _, _, _ := toolTestSvc(t)
	reg := tool.NewRegistry()
	RegisterCronTools(reg, svc)

	reg.Execute("cron/create", map[string]any{
		"name": "a", "schedule_type": "cron", "cron_expr": "* * * * * *",
		"action_type": "start_task", "action_payload": map[string]any{"agent_id": "a"},
	})
	// list enabled
	res, _ := reg.Execute("cron/list", map[string]any{"status": "enabled"})
	list, ok := res.([]Cron)
	if !ok {
		t.Fatalf("expected []Cron, got %T", res)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 enabled, got %d", len(list))
	}
}
