// cron_test.go — crons / cron_executions 表 CRUD 与过滤的测试。
package db

import (
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/cron"
)

// makeCron 构造一个合法的测试 Cron 对象。
func makeCron(id string) cron.Cron {
	return cron.Cron{
		ID:           id,
		Name:         "Test Cron " + id,
		Description:  "for testing",
		ScheduleType: cron.ScheduleCron,
		CronExpr:     "*/30 * * * * *",
		DisplayType:  "cron",
		Timezone:     "UTC",
		ActionType:   cron.ActionStartTask,
		ActionPayload: map[string]any{
			"agent_id": "agent_default",
			"input":    "hello {{.Count}}",
		},
		Status:   cron.StatusEnabled,
		Source:   "user",
	}
}

// TestCronTablesCreated 验证 migration 创建了 crons 与 cron_executions 两表及关键索引。
func TestCronTablesCreated(t *testing.T) {
	freshDB(t)
	for _, name := range []string{"crons", "cron_executions"} {
		if !tableExists(t, name) {
			t.Fatalf("table %q not created", name)
		}
	}
	// tableExists 只查 type='table'，索引需直接查 sqlite_master。
	for _, idx := range []string{"idx_crons_status", "idx_crons_next_trigger", "idx_cron_executions_cron_id"} {
		var n string
		err := DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n)
		if err != nil || n != idx {
			t.Fatalf("index %q not created", idx)
		}
	}
}

// TestCronCRUD 验证 Cron 的增删改查往返。
func TestCronCRUD(t *testing.T) {
	freshDB(t)
	c := makeCron("cron_crud_1")
	if err := InsertCron(c); err != nil {
		t.Fatalf("InsertCron: %v", err)
	}

	got, err := GetCron(c.ID)
	if err != nil {
		t.Fatalf("GetCron: %v", err)
	}
	if got.Name != c.Name || got.ScheduleType != c.ScheduleType {
		t.Fatalf("GetCron mismatch: %+v", got)
	}
	if got.ActionPayload["agent_id"] != "agent_default" {
		t.Fatalf("ActionPayload not decoded: %+v", got.ActionPayload)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", got)
	}

	// Update：改名 + 改 status
	got.Name = "Renamed"
	got.Status = cron.StatusPaused
	got.AllowConcurrent = true
	if err := UpdateCron(got); err != nil {
		t.Fatalf("UpdateCron: %v", err)
	}
	got2, _ := GetCron(c.ID)
	if got2.Name != "Renamed" || got2.Status != cron.StatusPaused || !got2.AllowConcurrent {
		t.Fatalf("UpdateCron not applied: %+v", got2)
	}

	// 不存在的 id 更新应返回 not found
	if err := UpdateCron(cron.Cron{ID: "nope"}); err != ErrCronNotFound {
		t.Fatalf("expected ErrCronNotFound, got %v", err)
	}

	// 删除
	if err := DeleteCron(c.ID); err != nil {
		t.Fatalf("DeleteCron: %v", err)
	}
	if _, err := GetCron(c.ID); err != ErrCronNotFound {
		t.Fatalf("expected ErrCronNotFound after delete, got %v", err)
	}
	if err := DeleteCron(c.ID); err != ErrCronNotFound {
		t.Fatalf("double delete expected ErrCronNotFound, got %v", err)
	}
}

// TestCronScheduleMeta 验证 UpdateCronScheduleMeta 只更新调度元数据，不动 action_payload。
func TestCronScheduleMeta(t *testing.T) {
	freshDB(t)
	c := makeCron("cron_meta_1")
	InsertCron(c)

	now := time.Now()
	triggerCount := 5
	c.Status = cron.StatusEnabled
	c.LastTriggeredAt = &now
	c.TriggerCount = triggerCount
	c.LastExecutionID = "cronexec_x"
	if err := UpdateCronScheduleMeta(c); err != nil {
		t.Fatalf("UpdateCronScheduleMeta: %v", err)
	}
	got, _ := GetCron(c.ID)
	if got.TriggerCount != triggerCount {
		t.Fatalf("trigger_count = %d, want %d", got.TriggerCount, triggerCount)
	}
	if got.LastExecutionID != "cronexec_x" {
		t.Fatalf("last_execution_id = %q", got.LastExecutionID)
	}
	if got.LastTriggeredAt == nil {
		t.Fatalf("last_triggered_at nil")
	}
	// action_payload 不应被清空
	if got.ActionPayload["agent_id"] != "agent_default" {
		t.Fatalf("action_payload lost after schedule meta update: %+v", got.ActionPayload)
	}
}

// TestListCronsFilter 验证 ListCrons 的过滤条件。
func TestListCronsFilter(t *testing.T) {
	freshDB(t)
	c1 := makeCron("cron_f_1"); c1.Status = cron.StatusEnabled; c1.ActionType = cron.ActionStartTask
	c2 := makeCron("cron_f_2"); c2.Status = cron.StatusDisabled; c2.ActionType = cron.ActionScript
	c3 := makeCron("cron_f_3"); c3.Name = "DailyReport"; c3.Status = cron.StatusEnabled
	InsertCron(c1); InsertCron(c2); InsertCron(c3)

	// 全量
	all, _ := ListCrons(cron.ListFilter{})
	if len(all) != 3 {
		t.Fatalf("ListCrons all = %d, want 3", len(all))
	}

	// 按 status
	enabled, _ := ListCrons(cron.ListFilter{Status: string(cron.StatusEnabled)})
	if len(enabled) != 2 {
		t.Fatalf("enabled = %d, want 2", len(enabled))
	}

	// 按 action_type
	scripts, _ := ListCrons(cron.ListFilter{ActionType: string(cron.ActionScript)})
	if len(scripts) != 1 || scripts[0].ID != "cron_f_2" {
		t.Fatalf("script filter wrong: %+v", scripts)
	}

	// 模糊查询 name
	q, _ := ListCrons(cron.ListFilter{Query: "Daily"})
	if len(q) != 1 || q[0].ID != "cron_f_3" {
		t.Fatalf("query filter wrong: %+v", q)
	}
}

// TestExecutionCRUD 验证执行记录的增改查与按 cron 过滤。
func TestExecutionCRUD(t *testing.T) {
	freshDB(t)
	c := makeCron("cron_exec_1")
	InsertCron(c)

	e := cron.Execution{
		ID:          "cronexec_e1",
		CronID:      c.ID,
		TriggeredAt: time.Now(),
		Status:      cron.ExecRunning,
	}
	if err := InsertExecution(e); err != nil {
		t.Fatalf("InsertExecution: %v", err)
	}

	// 更新为 completed
	e.Status = cron.ExecCompleted
	e.TaskID = "task_123"
	e.SessionID = "sess_123"
	e.DurationMS = 1500
	e.ResultSummary = "done"
	if err := UpdateExecution(e); err != nil {
		t.Fatalf("UpdateExecution: %v", err)
	}

	got, _ := GetExecution(e.ID)
	if got.Status != cron.ExecCompleted || got.TaskID != "task_123" || got.DurationMS != 1500 {
		t.Fatalf("execution not updated: %+v", got)
	}

	// 第二条 failed
	InsertExecution(cron.Execution{ID: "cronexec_e2", CronID: c.ID, TriggeredAt: time.Now().Add(-time.Minute), Status: cron.ExecFailed})

	// 按 cron 列
	list, _ := ListExecutions(cron.ExecListFilter{CronID: c.ID})
	if len(list) != 2 {
		t.Fatalf("list by cron = %d, want 2", len(list))
	}
	// 按 triggered_at DESC，e1 应在前
	if list[0].ID != "cronexec_e1" {
		t.Fatalf("order wrong: %+v", list)
	}

	// 按 status 过滤
	completed, _ := ListExecutions(cron.ExecListFilter{CronID: c.ID, Status: string(cron.ExecCompleted)})
	if len(completed) != 1 {
		t.Fatalf("completed filter = %d, want 1", len(completed))
	}

	// 不存在
	if _, err := GetExecution("nope"); err != ErrCronExecutionNotFound {
		t.Fatalf("expected ErrCronExecutionNotFound, got %v", err)
	}
}

// TestCleanExecutions 验证清理逻辑：全空 filter 不删；按 cron+before 删除。
func TestCleanExecutions(t *testing.T) {
	freshDB(t)
	c := makeCron("cron_clean_1")
	InsertCron(c)
	InsertExecution(cron.Execution{ID: "ex1", CronID: c.ID, TriggeredAt: time.Now().Add(-2 * time.Hour), Status: cron.ExecCompleted})
	InsertExecution(cron.Execution{ID: "ex2", CronID: c.ID, TriggeredAt: time.Now(), Status: cron.ExecCompleted})

	// 全空 → 不删
	n, _ := CleanExecutions(cron.CleanFilter{})
	if n != 0 {
		t.Fatalf("empty filter deleted %d, want 0", n)
	}
	list, _ := ListExecutions(cron.ExecListFilter{CronID: c.ID})
	if len(list) != 2 {
		t.Fatalf("should still have 2, got %d", len(list))
	}

	// 按 before 删除 1 小时前
	n, _ = CleanExecutions(cron.CleanFilter{CronID: c.ID, Before: time.Now().Add(-time.Hour)})
	if n != 1 {
		t.Fatalf("clean before deleted %d, want 1", n)
	}
	list, _ = ListExecutions(cron.ExecListFilter{CronID: c.ID})
	if len(list) != 1 || list[0].ID != "ex2" {
		t.Fatalf("after clean wrong: %+v", list)
	}
}

// TestDeleteCronCascadesExecutions 验证删除 cron 时 executions 被 FK cascade 清理。
func TestDeleteCronCascadesExecutions(t *testing.T) {
	freshDB(t)
	c := makeCron("cron_cascade_1")
	InsertCron(c)
	InsertExecution(cron.Execution{ID: "ex_c1", CronID: c.ID, TriggeredAt: time.Now(), Status: cron.ExecCompleted})

	if err := DeleteCron(c.ID); err != nil {
		t.Fatalf("DeleteCron: %v", err)
	}
	list, _ := ListExecutions(cron.ExecListFilter{CronID: c.ID})
	if len(list) != 0 {
		t.Fatalf("executions not cascaded, left %d", len(list))
	}
}
