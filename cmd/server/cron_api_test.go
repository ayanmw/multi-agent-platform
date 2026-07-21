// cron_api_test.go — Cron REST API 集成测试。
//
// 用 httptest + 真实临时 SQLite（含 v26 migration）+ 内存 mock EventSink，
// 覆盖 /api/crons* 的核心端到端流程：
//   创建 → 列表 → 详情 → 启用/禁用 → 手动 trigger（notify_session，避免依赖 LLM）→
//   执行历史查询 → 清理 → 删除 → 404。
//
// 不接真实 LLM：start_task action 需要 TaskStarter + runAgentLoop，这里用
// notify_session（只写 session_messages + 广播事件）验证整条触发链路。
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/cron"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
	_ "modernc.org/sqlite"
)

// cronTestBus 记录所有广播的 cron_* 事件，供断言"事件已发"。
// 实现 cron.EventBus（SendEvent(event.Event)），直接转发 event.Event。
type cronTestBus struct {
	events []event.Event
}

func (b *cronTestBus) SendEvent(e event.Event) {
	b.events = append(b.events, e)
}

// setupCronTestDB 初始化临时 SQLite 并跑完整迁移（含 v26 crons 表）。
func setupCronTestDB(t *testing.T) {
	t.Helper()
	if err := db.Init(filepath.Join(t.TempDir(), "test_crons.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		db.DB = nil
	})
}

// newCronTestHarness 构造 (Service + Adapter + REST mux + httptest.Server)。
// 返回 server 与记录事件的 bus，供用例调用。
func newCronTestHarness(t *testing.T) (*httptest.Server, *cronTestBus) {
	t.Helper()
	setupCronTestDB(t)

	store := cron.NewStore(&cronDBStoreAdapter{})
	bus := &cronTestBus{}

	// 用 notify_session 作为可验证的 action；MessageWriter 写真实 session_messages。
	// 需要一个有效 session 让 InsertSystemMessage 不报错——测试内先建 session。
	runner := cron.NewActionRunner(cron.ActionRunnerConfig{
		MessageWriter: &cronSessionMsgWriter{},
		Bus:           bus,
		MaxResultChars: 500,
	})
	executor := cron.NewExecutor(store, runner, bus, 500)
	execAdapter := &cronExecutorAdapter{exec: executor}
	svc := cron.NewService(store, nil, execAdapter, bus)

	mux := http.NewServeMux()
	RegisterCronAPI(mux, svc)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, bus
}

// ensureSession 创建一条 session 记录供 notify_session 写消息用。
// 已存在则跳过（用例间可能复用同一 session_id）。
func ensureSession(t *testing.T, sessionID string) {
	t.Helper()
	if existing, err := db.QuerySessionByID(sessionID); err == nil && existing != nil {
		return
	}
	if err := db.InsertSession(db.SessionRecord{
		ID:        sessionID,
		Name:      "cron-test",
		Status:    "active",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

// TestCronAPICreateListGet 覆盖 创建 → 列表 → 详情。
func TestCronAPICreateListGet(t *testing.T) {
	ts, _ := newCronTestHarness(t)
	client := ts.Client()

	body, _ := json.Marshal(map[string]any{
		"name":          "每分钟提醒",
		"schedule_type": "interval",
		"cron_expr":     "1h",
		"action_type":   "notify_session",
		"action_payload": map[string]any{
			"session_id": "sess_cron_1",
			"message":    "hello from cron",
		},
	})
	resp, err := client.Post(ts.URL+"/api/crons", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 create, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var created cron.Cron
	if err := json.Unmarshal([]byte(readBody(t, resp)), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" || created.Status != cron.StatusEnabled {
		t.Fatalf("bad created: %+v", created)
	}

	// 列表应包含它。
	resp, err = client.Get(ts.URL + "/api/crons")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var list []cron.Cron
	if err := json.Unmarshal([]byte(readBody(t, resp)), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list mismatch: %+v", list)
	}

	// 详情。
	resp, err = client.Get(ts.URL + "/api/crons/" + created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var got cron.Cron
	if err := json.Unmarshal([]byte(readBody(t, resp)), &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("get id mismatch: %s", got.ID)
	}
}

// TestCronAPIStatusToggle 覆盖 disable → enable → pause → resume。
func TestCronAPIStatusToggle(t *testing.T) {
	ts, _ := newCronTestHarness(t)
	client := ts.Client()
	id := createCron(t, ts, "toggle-cron")

	for _, op := range []struct {
		path   string
		expect cron.Status
	}{
		{"disable", cron.StatusDisabled},
		{"enable", cron.StatusEnabled},
		{"pause", cron.StatusPaused},
		{"resume", cron.StatusEnabled},
	} {
		resp, err := client.Post(ts.URL+"/api/crons/"+id+"/"+op.path, "", nil)
		if err != nil {
			t.Fatalf("%s: %v", op.path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", op.path, resp.StatusCode, readBody(t, resp))
		}
		var c cron.Cron
		if err := json.Unmarshal([]byte(readBody(t, resp)), &c); err != nil {
			t.Fatalf("decode %s: %v", op.path, err)
		}
		if c.Status != op.expect {
			t.Fatalf("%s: expected status %s, got %s", op.path, op.expect, c.Status)
		}
	}
}

// TestCronAPITriggerAndExecutions 覆盖 手动 trigger notify_session → 执行历史查询 → 清理。
func TestCronAPITriggerAndExecutions(t *testing.T) {
	ts, _ := newCronTestHarness(t)
	client := ts.Client()
	ensureSession(t, "sess_cron_trig")
	id := createCronWithPayload(t, ts, "trigger-cron", map[string]any{
		"session_id": "sess_cron_trig",
		"message":    "ping {{.Count}}",
	})

	// 手动触发。
	resp, err := client.Post(ts.URL+"/api/crons/"+id+"/trigger", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trigger: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var exec cron.Execution
	if err := json.Unmarshal([]byte(readBody(t, resp)), &exec); err != nil {
		t.Fatalf("decode exec: %v", err)
	}
	if exec.Status != cron.ExecCompleted {
		t.Fatalf("expected completed execution, got %s (err=%s)", exec.Status, exec.Error)
	}

	// 执行历史（按 cron）。
	resp, err = client.Get(ts.URL + "/api/crons/" + id + "/executions")
	if err != nil {
		t.Fatalf("list exec: %v", err)
	}
	var execs []cron.Execution
	if err := json.Unmarshal([]byte(readBody(t, resp)), &execs); err != nil {
		t.Fatalf("decode execs: %v", err)
	}
	if len(execs) != 1 || execs[0].ID != exec.ID {
		t.Fatalf("executions mismatch: %+v", execs)
	}

	// 全局执行历史端点。
	resp, err = client.Get(ts.URL + "/api/crons/executions?cron_id=" + id)
	if err != nil {
		t.Fatalf("global exec: %v", err)
	}
	var global []cron.Execution
	if err := json.Unmarshal([]byte(readBody(t, resp)), &global); err != nil {
		t.Fatalf("decode global exec: %v", err)
	}
	if len(global) != 1 {
		t.Fatalf("global executions mismatch: %+v", global)
	}

	// 清理。
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/crons/executions?cron_id="+id, nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	var cleanResp struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(readBody(t, resp)), &cleanResp); err != nil {
		t.Fatalf("decode clean: %v", err)
	}
	if cleanResp.Deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", cleanResp.Deleted)
	}
}

// TestCronAPIDelete 覆盖 删除 + 404。
func TestCronAPIDelete(t *testing.T) {
	ts, _ := newCronTestHarness(t)
	client := ts.Client()
	id := createCron(t, ts, "delete-cron")

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/crons/"+id, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// 再次 GET 应 404。
	resp, err = client.Get(ts.URL + "/api/crons/" + id)
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

// TestCronAPICreateValidation 覆盖 校验失败返回 400。
func TestCronAPICreateValidation(t *testing.T) {
	ts, _ := newCronTestHarness(t)
	client := ts.Client()

	// 缺 name。
	body, _ := json.Marshal(map[string]any{
		"schedule_type": "interval",
		"cron_expr":     "1h",
		"action_type":   "notify_session",
		"action_payload": map[string]any{"session_id": "s", "message": "m"},
	})
	resp, err := client.Post(ts.URL+"/api/crons", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	// 非法 cron_expr。
	body, _ = json.Marshal(map[string]any{
		"name":          "bad",
		"schedule_type": "cron",
		"cron_expr":     "not-a-cron",
		"action_type":   "notify_session",
		"action_payload": map[string]any{"session_id": "s", "message": "m"},
	})
	resp, err = client.Post(ts.URL+"/api/crons", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad cron_expr, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
}

// createCron 创建一个默认 notify_session cron，返回其 ID。
func createCron(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	return createCronWithPayload(t, ts, name, map[string]any{
		"session_id": "sess_cron_default",
		"message":    "hi",
	})
}

// createCronWithPayload 用指定 action_payload 创建 cron。
func createCronWithPayload(t *testing.T, ts *httptest.Server, name string, payload map[string]any) string {
	t.Helper()
	ensureSession(t, "sess_cron_default")
	ensureSession(t, "sess_cron_trig")
	body, _ := json.Marshal(map[string]any{
		"name":           name,
		"schedule_type":  "interval",
		"cron_expr":      "1h",
		"action_type":    "notify_session",
		"action_payload": payload,
	})
	resp, err := ts.Client().Post(ts.URL+"/api/crons", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var c cron.Cron
	if err := json.Unmarshal([]byte(readBody(t, resp)), &c); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	return c.ID
}

// 静态断言：cronTestBus 实现 cron.EventBus（method set 包含 SendEvent）。
var _ cron.EventBus = (*cronTestBus)(nil)

// 静态断言：cronExecutorAdapter 实现 cron.ExecutorPort2。
var _ cron.ExecutorPort2 = (*cronExecutorAdapter)(nil)

// 静态断言：cronDBStoreAdapter 实现 cron.DBStore。
var _ cron.DBStore = (*cronDBStoreAdapter)(nil)

// 引用 sql 包避免在某些构建配置下被误判未使用（db adapter 用到 sql.Null* 类型）。
var _ = sql.ErrNoRows
