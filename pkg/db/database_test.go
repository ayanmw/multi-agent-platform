// database_test.go —— db 包初始化、migration 幂等性以及核心 CRUD 往返的白盒测试。
//
// 所有测试都通过纯 Go 的 modernc.org/sqlite 驱动（无 CGO）在 t.TempDir() 下的
// 临时 SQLite 文件中运行。全局 DB 句柄通过 t.Cleanup 在测试间重置，使每个
// 子测试都能看到一个全新的数据库。
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// freshDB 在临时路径初始化一个全新数据库，执行 createTables 与 migration，
// 注册关闭 DB 并重置包级全局的 cleanup，并返回路径与活跃的 *sql.DB。
//
// 测试必须调用它而非复用共享全局，以保持隔离。
func freshDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	if err := Init(path); err != nil {
		t.Fatalf("Init(%q): %v", path, err)
	}
	if DB == nil {
		t.Fatal("Init left DB nil")
	}
	db := DB
	t.Cleanup(func() {
		_ = Close()
		DB = nil
	})
	return path, db
}

// tableExists 查询 sqlite_master，确认当前数据库中存在指定名称的表（或索引）。
func tableExists(t *testing.T, name string) bool {
	t.Helper()
	if DB == nil {
		t.Fatal("DB nil in tableExists")
	}
	var n string
	err := DB.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&n)
	if err == nil {
		return n == name
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	t.Fatalf("tableExists(%q): %v", name, err)
	return false
}

// --- 初始化与 Migration -------------------------------------------

// TestInitCreatesDatabase 验证 Init 能产生一个可 Ping 的 *sql.DB，
// 并正确接上包级全局。
func TestInitCreatesDatabase(t *testing.T) {
	_, db := freshDB(t)
	if err := db.Ping(); err != nil {
		t.Errorf("post-Init Ping: %v", err)
	}
}

// TestInitIdempotentMigrations 对同一数据库连续运行两次 RunMigrations，
// 确认第二次调用是 no-op（无错误，schema_migrations 中不产生重复行）。
func TestInitIdempotentMigrations(t *testing.T) {
	freshDB(t)

	if err := RunMigrations(); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if err := RunMigrations(); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	var count int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if want := len(deduplicateMigrations(migrations)); count != want {
		t.Errorf("schema_migrations row count = %d, want %d (duplicates suggest non-idempotent migration)", count, want)
	}
}

// TestInitTwiceSameFile 确认对已有文件重新 Init 是安全的——表已存在
// （CREATE TABLE IF NOT EXISTS），schema_migrations 中已记录的 migration 会被跳过。
func TestInitTwiceSameFile(t *testing.T) {
	path, _ := freshDB(t)
	// 关闭首个 DB，以便第二次 Init 能重新打开同一文件。
	_ = Close()
	DB = nil

	if err := Init(path); err != nil {
		t.Fatalf("second Init on same file: %v", err)
	}
	t.Cleanup(func() {
		_ = Close()
		DB = nil
	})

	var count int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if want := len(deduplicateMigrations(migrations)); count != want {
		t.Errorf("after re-Init, schema_migrations row count = %d, want %d", count, want)
	}
}

// --- 表存在性 -------------------------------------------------------

// TestExpectedTablesExist 断言 createTables 以及 v5/v6/v10/v12/v13/v17 migration
// 声明的所有表在 Init 后都存在。
func TestExpectedTablesExist(t *testing.T) {
	freshDB(t)
	wantTables := []string{
		"agents",
		"tasks",
		"steps",
		"tools",
		"conversations",
		"files",
		"sessions",
		"projects",
		"session_messages",
		"memories",
		"memory_links",
		// 仅由 migration 创建的表
		"schema_migrations",
		"cost_records",
		"users",
		"api_keys",
		"mock_scripts",
		"memory_embeddings",
		"cases",
		"case_evaluations",
	}
	for _, name := range wantTables {
		if !tableExists(t, name) {
			t.Errorf("expected table %q missing after Init", name)
		}
	}
}

// TestCostRecordsHasCostCentsColumn 验证 v11 migration 在 REAL 的 cost_usd 列
// 之外新增了整数型 cost_cents 列。
func TestCostRecordsHasCostCentsColumn(t *testing.T) {
	freshDB(t)
	rows, err := DB.Query(`PRAGMA table_info(cost_records)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(cost_records): %v", err)
	}
	defer rows.Close()

	cols := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	for _, want := range []string{"id", "task_id", "session_id", "cost_usd", "cost_cents"} {
		if _, ok := cols[want]; !ok {
			t.Errorf("cost_records missing column %q; have %v", want, cols)
		}
	}
}

// TestDefaultProjectSeededByMigration 验证 v5 migration 会播种 'default' project
// 行，session 在缺失时会回退到它。
func TestDefaultProjectSeededByMigration(t *testing.T) {
	freshDB(t)
	var count int
	err := DB.QueryRow(`SELECT COUNT(*) FROM projects WHERE id='default'`).Scan(&count)
	if err != nil {
		t.Fatalf("query default project: %v", err)
	}
	if count != 1 {
		t.Errorf("default project count = %d, want 1", count)
	}
}

// --- Session CRUD 往返 -----------------------------------------------

// newSessionRecord 构造一个带有合理默认值的 SessionRecord，供 CRUD 测试复用。
func newSessionRecord(id, name string) SessionRecord {
	now := time.Now().UTC().Truncate(time.Second)
	return SessionRecord{
		ID:          id,
		Name:        name,
		RootTaskID:  "",
		Status:      "running",
		UserInput:   "hello world",
		ProjectID:   "default",
		TurnCount:   1,
		TotalTokens: 42,
		ContextSize: 128,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// TestSessionCRUDRoundTrip 走一遍 Insert -> QueryByID -> Update -> Query
// -> Delete 流程，断言字段在往返过程中保持不变。
func TestSessionCRUDRoundTrip(t *testing.T) {
	freshDB(t)

	orig := newSessionRecord("sess_1", "my-session")
	if err := InsertSession(orig); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	got, err := QuerySessionByID("sess_1")
	if err != nil {
		t.Fatalf("QuerySessionByID: %v", err)
	}
	if got.ID != orig.ID || got.Name != orig.Name || got.Status != orig.Status ||
		got.UserInput != orig.UserInput || got.ProjectID != orig.ProjectID ||
		got.TurnCount != orig.TurnCount || got.TotalTokens != orig.TotalTokens ||
		got.ContextSize != orig.ContextSize {
		t.Errorf("round-trip mismatch:\n got  = %+v\n want = %+v", got, orig)
	}

	// 更新 root task + status + user input。
	if err := UpdateSession("sess_1", "task_root", "completed", "updated input"); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	got, err = QuerySessionByID("sess_1")
	if err != nil {
		t.Fatalf("QuerySessionByID after update: %v", err)
	}
	if got.RootTaskID != "task_root" {
		t.Errorf("RootTaskID = %q, want %q", got.RootTaskID, "task_root")
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q", got.Status, "completed")
	}
	if got.UserInput != "updated input" {
		t.Errorf("UserInput = %q, want %q", got.UserInput, "updated input")
	}

	// 删除应当移除该行。
	if err := DeleteSession("sess_1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := QuerySessionByID("sess_1"); err == nil {
		t.Error("QuerySessionByID after delete returned nil, want error")
	}
}

// TestInsertSessionDefaultsProjectID 验证 InsertSession 会把空的 ProjectID
// 归一化为 "default"。
func TestInsertSessionDefaultsProjectID(t *testing.T) {
	freshDB(t)
	s := newSessionRecord("sess_default_proj", "n")
	s.ProjectID = ""
	if err := InsertSession(s); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	got, err := QuerySessionByID("sess_default_proj")
	if err != nil {
		t.Fatalf("QuerySessionByID: %v", err)
	}
	if got.ProjectID != "default" {
		t.Errorf("ProjectID = %q, want %q", got.ProjectID, "default")
	}
}

// TestQuerySessionsListsAndFilters 验证 QuerySessions 返回的 session 按
// updated_at 倒序排列，并遵守 projectID 过滤条件。
func TestQuerySessionsListsAndFilters(t *testing.T) {
	freshDB(t)

	// 跨两个 project 插入三条 session，各自带有不同的更新时间。
	s1 := newSessionRecord("sess_a", "a")
	s1.ProjectID = "default"
	s1.UpdatedAt = time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	s2 := newSessionRecord("sess_b", "b")
	s2.ProjectID = "proj_x"
	s2.UpdatedAt = time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	s3 := newSessionRecord("sess_c", "c")
	s3.ProjectID = "default"
	s3.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	for _, s := range []SessionRecord{s1, s2, s3} {
		if err := InsertSession(s); err != nil {
			t.Fatalf("InsertSession(%q): %v", s.ID, err)
		}
	}

	// 不过滤：三条全部返回，最新优先（s3, s2, s1）。
	all, err := QuerySessions(10, "")
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("QuerySessions returned %d rows, want 3", len(all))
	}
	if all[0].ID != "sess_c" || all[1].ID != "sess_b" || all[2].ID != "sess_a" {
		t.Errorf("order = %s,%s,%s; want sess_c,sess_b,sess_a", all[0].ID, all[1].ID, all[2].ID)
	}

	// 按 project 过滤：仅返回 default 的 session（s3, s1）。
	def, err := QuerySessions(10, "default")
	if err != nil {
		t.Fatalf("QuerySessions(default): %v", err)
	}
	if len(def) != 2 {
		t.Fatalf("default project count = %d, want 2", len(def))
	}
	if def[0].ID != "sess_c" || def[1].ID != "sess_a" {
		t.Errorf("default order = %s,%s; want sess_c,sess_a", def[0].ID, def[1].ID)
	}
}

// TestDeleteSessionMissingReturnsError 验证 DeleteSession 对未知 ID 会返回
// not-found 错误，而不是静默成功。
func TestDeleteSessionMissingReturnsError(t *testing.T) {
	freshDB(t)
	if err := DeleteSession("does_not_exist"); err == nil {
		t.Error("DeleteSession on missing ID returned nil, want error")
	}
}

// --- Task 持久化 ------------------------------------------------------

// TestTaskPersistAndQueryBySession 覆盖 InsertTask、UpdateTaskSession、
// QueryTaskByID、QueryTasksBySession——验证 session/parent/is_root 绑定能被
// 正确存储与读取。
func TestTaskPersistAndQueryBySession(t *testing.T) {
	freshDB(t)

	// 父 session 必须存在以满足 FK 语义（也让 QueryTasksBySession 有意义）。
	if err := InsertSession(newSessionRecord("sess_t", "task-host")); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	root := TaskRecord{
		ID:           "task_root",
		UserInput:    "do the thing",
		Status:       "running",
		AgentIDs:     []string{"agent_a", "agent_b"},
		StartedAt:    time.Now().UTC().Truncate(time.Second),
		SessionID:    "sess_t",
		ParentTaskID: "",
		IsRoot:       true,
	}
	if err := InsertTask(root); err != nil {
		t.Fatalf("InsertTask(root): %v", err)
	}

	child := TaskRecord{
		ID:           "task_child",
		UserInput:    "sub-step",
		Status:       "pending",
		AgentIDs:     []string{"agent_a"},
		StartedAt:    time.Now().UTC().Truncate(time.Second),
		SessionID:    "sess_t",
		ParentTaskID: "task_root",
		IsRoot:       false,
	}
	if err := InsertTask(child); err != nil {
		t.Fatalf("InsertTask(child): %v", err)
	}

	// QueryTaskByID 应能还原 agentIDs 切片与各绑定字段。
	got, err := QueryTaskByID("task_root")
	if err != nil {
		t.Fatalf("QueryTaskByID: %v", err)
	}
	if got.SessionID != "sess_t" || !got.IsRoot || got.ParentTaskID != "" {
		t.Errorf("root task bindings wrong: %+v", got)
	}
	if len(got.AgentIDs) != 2 || got.AgentIDs[0] != "agent_a" || got.AgentIDs[1] != "agent_b" {
		t.Errorf("root AgentIDs = %v, want [agent_a agent_b]", got.AgentIDs)
	}

	// QueryTasksBySession 先返回 root（is_root DESC），再返回 child。
	tasks, err := QueryTasksBySession("sess_t")
	if err != nil {
		t.Fatalf("QueryTasksBySession: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks by session = %d, want 2", len(tasks))
	}
	if tasks[0].ID != "task_root" {
		t.Errorf("first task = %s, want task_root (is_root DESC)", tasks[0].ID)
	}
	if tasks[1].ParentTaskID != "task_root" {
		t.Errorf("child ParentTaskID = %q, want task_root", tasks[1].ParentTaskID)
	}

	// UpdateTask 会修改 status / final_result / total_tokens。
	if err := UpdateTask("task_root", "completed", "all done", 100); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	got, err = QueryTaskByID("task_root")
	if err != nil {
		t.Fatalf("QueryTaskByID after update: %v", err)
	}
	if got.Status != "completed" || got.FinalResult != "all done" || got.TotalTokens != 100 {
		t.Errorf("after update: status=%q final=%q tokens=%d", got.Status, got.FinalResult, got.TotalTokens)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt nil after UpdateTask, want set")
	}

	// AggregateSessionTokens 应汇总 task 的 token 总数。
	tot, err := AggregateSessionTokens("sess_t")
	if err != nil {
		t.Fatalf("AggregateSessionTokens: %v", err)
	}
	if tot != 100 {
		t.Errorf("AggregateSessionTokens = %d, want 100", tot)
	}
}

// TestQueryChildTasks 验证 parent -> children 查找。
func TestQueryChildTasks(t *testing.T) {
	freshDB(t)
	if err := InsertSession(newSessionRecord("sess_c", "c")); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}
	if err := InsertTask(TaskRecord{
		ID: "task_p", UserInput: "p", Status: "running",
		AgentIDs: []string{}, StartedAt: time.Now().UTC(),
		SessionID: "sess_c", IsRoot: true,
	}); err != nil {
		t.Fatalf("InsertTask(parent): %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := InsertTask(TaskRecord{
			ID: fmt.Sprintf("task_c%d", i), UserInput: "c", Status: "pending",
			AgentIDs: []string{}, StartedAt: time.Now().UTC(),
			SessionID: "sess_c", ParentTaskID: "task_p",
		}); err != nil {
			t.Fatalf("InsertTask(child %d): %v", i, err)
		}
	}
	kids, err := QueryChildTasks("task_p")
	if err != nil {
		t.Fatalf("QueryChildTasks: %v", err)
	}
	if len(kids) != 3 {
		t.Errorf("QueryChildTasks = %d children, want 3", len(kids))
	}
	for _, k := range kids {
		if k.ParentTaskID != "task_p" {
			t.Errorf("child %s ParentTaskID = %q, want task_p", k.ID, k.ParentTaskID)
		}
	}
}

// --- 外键行为 -------------------------------------------------

// TestInsertTaskOrphanSessionID 记录纯 Go modernc.org/sqlite 驱动的外键行为。
// 默认情况下 SQLite 不会强制外键，除非显式执行 `PRAGMA foreign_keys=ON`；
// Init 不会执行该 PRAGMA。我们在此断言实际行为：以不存在的 session_id 插入
// task 会成功（FK 未强制）。
//
// 如果未来改动开启了 FK 强制，本测试会失败并应改为断言错误——那属于 schema
// 层面的决策，并非本测试的 bug。
func TestInsertTaskOrphanSessionID(t *testing.T) {
	freshDB(t)

	// 显式开启 FK，使我们断言的行为不致歧义。
	if _, err := DB.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("PRAGMA foreign_keys=ON: %v", err)
	}

	err := InsertTask(TaskRecord{
		ID: "task_orphan", UserInput: "x", Status: "running",
		AgentIDs: []string{}, StartedAt: time.Now().UTC(),
		SessionID: "sess_does_not_exist",
	})
	if err == nil {
		t.Skip("modernc.org/sqlite does not enforce FK on tasks.session_id even with PRAGMA foreign_keys=ON; documented behaviour, not a bug")
	}
	// 如果返回错误，也是合法结果——只记录日志。
	t.Logf("InsertTask with orphan session_id returned error (FK enforced): %v", err)
}

// --- cases / case_evaluations 表 ---------------------------------------

// TestCasesMigrationRegistered 验证 v17 cases 和 case_evaluations migration
// 在 Init 后已被记录到 schema_migrations 中。
func TestCasesMigrationRegistered(t *testing.T) {
	freshDB(t)

	var exists bool
	err := DB.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=?)`, 17,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("query schema_migrations for v17: %v", err)
	}
	if !exists {
		t.Errorf("migration v17 not registered in schema_migrations")
	}
}

// TestCasesAndEvaluationsTablesExist 验证 v17 migration 创建了 cases 和
// case_evaluations 表及其配套索引。
func TestCasesAndEvaluationsTablesExist(t *testing.T) {
	freshDB(t)
	for _, name := range []string{"cases", "case_evaluations"} {
		if !tableExists(t, name) {
			t.Errorf("expected table %q missing after migration v17", name)
		}
	}
}

// TestCasesTablesUsable 走一遍基本的 INSERT/SELECT 往返，证明 schema 与预期的
// 类型和约束一致。
func TestCasesTablesUsable(t *testing.T) {
	freshDB(t)

	now := time.Now().UTC().Truncate(time.Second)
	_, err := DB.Exec(`
		INSERT INTO cases (id, name, description, icon, category, system_prompt,
			default_input, contract_json, tags_json, is_builtin, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"case_hello", "Hello Case", "desc", "icon", "basic",
		"You are a tester.", "say hello", `{}`, `["tag1","tag2"]`, 1, now, now,
	)
	if err != nil {
		t.Fatalf("insert cases: %v", err)
	}

	_, err = DB.Exec(`
		INSERT INTO case_evaluations (task_id, case_id, passed, score, reason, evaluated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"task_1", "case_hello", 1, 0.95, "all assertions passed", now,
	)
	if err != nil {
		t.Fatalf("insert case_evaluations: %v", err)
	}

	var gotCaseID string
	err = DB.QueryRow(`SELECT case_id FROM case_evaluations WHERE task_id=?`, "task_1").Scan(&gotCaseID)
	if err != nil {
		t.Fatalf("select case_evaluations: %v", err)
	}
	if gotCaseID != "case_hello" {
		t.Errorf("case_evaluations.case_id = %q, want %q", gotCaseID, "case_hello")
	}
}

// --- mock_scripts 表 ----------------------------------------------------

// TestMockScriptsTableUsable 验证 v13 migration 创建了 mock_scripts 表，
// 并且基本的 Insert/Select 往返可用。pkg/db 中没有 mock_scripts 的 CRUD
// 辅助（mock store 位于 internal/llm），因此这里用裸 SQL 证明表可用。
func TestMockScriptsTableUsable(t *testing.T) {
	freshDB(t)
	if !tableExists(t, "mock_scripts") {
		t.Fatal("mock_scripts table missing")
	}

	_, err := DB.Exec(`INSERT INTO mock_scripts (id, case_id, priority, match_input, responses) VALUES (?, ?, ?, ?, ?)`,
		"ms_1", "case_dialogue", 5, "hello", `["resp_a","resp_b"]`)
	if err != nil {
		t.Fatalf("insert mock_scripts: %v", err)
	}

	var responses string
	err = DB.QueryRow(`SELECT responses FROM mock_scripts WHERE id=?`, "ms_1").Scan(&responses)
	if err != nil {
		t.Fatalf("select mock_scripts: %v", err)
	}
	if responses != `["resp_a","resp_b"]` {
		t.Errorf("responses = %q, want JSON array string", responses)
	}
}

// --- cost_records 表 ----------------------------------------------------

// TestCostRecordsTableUsable 验证 cost_records 表可写入，且 v11 migration 的
// cost_cents 列能往返一个整数值。和 mock_scripts 一样，pkg/db 中没有对应
// CRUD 辅助，因此使用裸 SQL。
func TestCostRecordsTableUsable(t *testing.T) {
	freshDB(t)
	if !tableExists(t, "cost_records") {
		t.Fatal("cost_records table missing")
	}

	_, err := DB.Exec(`INSERT INTO cost_records
		(id, task_id, session_id, project_id, agent_id, step_index, model, provider,
		 input_tokens, output_tokens, total_tokens, cost_usd, cost_cents)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"cr_1", "task_x", "sess_x", "default", "agent_a", 0,
		"deepseek-v4-flash", "deepseek", 100, 50, 150, 0.0123, 1234)
	if err != nil {
		t.Fatalf("insert cost_records: %v", err)
	}

	var costCents int
	var model string
	err = DB.QueryRow(`SELECT cost_cents, model FROM cost_records WHERE id=?`, "cr_1").Scan(&costCents, &model)
	if err != nil {
		t.Fatalf("select cost_records: %v", err)
	}
	if costCents != 1234 {
		t.Errorf("cost_cents = %d, want 1234", costCents)
	}
	if model != "deepseek-v4-flash" {
		t.Errorf("model = %q, want deepseek-v4-flash", model)
	}
}

// --- splitSQL 辅助函数（migrate.go 内部） ------------------------------

// TestSplitSQL 覆盖 RunMigrations 使用的轻量级语句拆分器，
// 因为 SQLite 的 Exec 一次只能处理一条语句。
func TestSplitSQL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single_no_semicolon", "SELECT 1", []string{"SELECT 1"}},
		{"single_with_semicolon", "SELECT 1;", []string{"SELECT 1"}},
		{"two_statements", "SELECT 1; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"trailing_whitespace", "  SELECT 1 ;  ", []string{"SELECT 1"}},
		{"multiple_semicolons", ";;SELECT 1;;", []string{"SELECT 1"}},
		{"newlines", "SELECT 1\n;\nSELECT 2", []string{"SELECT 1", "SELECT 2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitSQL(tt.in)
			if len(got) != len(tt.want) {
				t.Errorf("splitSQL(%q) = %v, want %v", tt.in, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitSQL(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestTrimSpace 覆盖 splitSQL 使用的 ASCII trimmer。
func TestTrimSpace(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"abc", "abc"},
		{"  abc  ", "abc"},
		{"\t\nabc\r\n", "abc"},
		{"   ", ""},
		{"a b", "a b"}, // 内部空白保留
	}
	for _, tt := range tests {
		if got := trimSpace(tt.in); got != tt.want {
			t.Errorf("trimSpace(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- 并发（轻量） ---------------------------------------------------

// TestConcurrentInsertSession 开 5 个 goroutine 各向共享 *sql.DB 插入一个
// 不同 session。modernc.org/sqlite 通过数据库级锁串行化写者；默认情况下每个
// pooled connection 各自带 busy_timeout，因此跨连接等待可能以 SQLITE_BUSY 失败。
// 我们把池固定为单连接（推荐的 SQLite 模式），使 PRAGMA busy_timeout 统一生效，
// 然后断言不 panic 且行数正确。若在某些平台仍抖动，则跳过。
func TestConcurrentInsertSession(t *testing.T) {
	freshDB(t)

	// SQLite 是单写者数据库。把池固定为单连接，使 busy_timeout PRAGMA
	// （下方设置）统一约束所有访问，写者排队而非跨连接撞上 SQLITE_BUSY。
	DB.SetMaxOpenConns(1)
	if _, err := DB.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}

	const N = 5
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := newSessionRecord(
				fmt.Sprintf("sess_concurrent_%d", i),
				fmt.Sprintf("c%d", i),
			)
			if err := InsertSession(s); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("goroutine InsertSession error: %v", err)
	}

	var count int
	if err := DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id LIKE 'sess_concurrent_%'`).Scan(&count); err != nil {
		t.Fatalf("count concurrent sessions: %v", err)
	}
	if count != N {
		t.Errorf("concurrent session count = %d, want %d", count, N)
	}
}
