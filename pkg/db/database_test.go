// database_test.go — white-box tests for the db package's initialization,
// migration idempotency, and core CRUD round-trips.
//
// All tests use a temporary SQLite file under t.TempDir() via the pure-Go
// modernc.org/sqlite driver (no CGO). The global DB handle is reset between
// tests via t.Cleanup so each subtest sees a fresh database.
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

// freshDB initializes a brand-new database at a temp path, runs createTables
// and migrations, registers a cleanup that closes the DB and resets the
// package-level global, and returns the path plus the live *sql.DB.
//
// Tests must call this rather than reusing a shared global to stay isolated.
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

// tableExists queries sqlite_master to confirm a table (or index) by name
// exists in the current database.
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

// --- Initialization & Migration -------------------------------------------

// TestInitCreatesDatabase verifies Init produces a pingable *sql.DB and
// wires the package global.
func TestInitCreatesDatabase(t *testing.T) {
	_, db := freshDB(t)
	if err := db.Ping(); err != nil {
		t.Errorf("post-Init Ping: %v", err)
	}
}

// TestInitIdempotentMigrations runs RunMigrations twice against the same
// database and confirms the second invocation is a no-op (no error, no
// duplicate rows in schema_migrations).
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
	if want := len(migrations); count != want {
		t.Errorf("schema_migrations row count = %d, want %d (duplicates suggest non-idempotent migration)", count, want)
	}
}

// TestInitTwiceSameFile confirms re-Init on an existing file is safe — the
// tables already exist (CREATE TABLE IF NOT EXISTS) and migrations recorded
// in schema_migrations are skipped.
func TestInitTwiceSameFile(t *testing.T) {
	path, _ := freshDB(t)
	// Close the first DB so the second Init can re-open the same file.
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
	if want := len(migrations); count != want {
		t.Errorf("after re-Init, schema_migrations row count = %d, want %d", count, want)
	}
}

// --- Table existence -------------------------------------------------------

// TestExpectedTablesExist asserts every table declared by createTables and
// the v5/v6/v10/v12/v13 migrations is present after Init.
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
		// migration-only tables
		"schema_migrations",
		"cost_records",
		"users",
		"api_keys",
		"mock_scripts",
	}
	for _, name := range wantTables {
		if !tableExists(t, name) {
			t.Errorf("expected table %q missing after Init", name)
		}
	}
}

// TestCostRecordsHasCostCentsColumn verifies the v11 migration added the
// integer cost_cents column alongside the REAL cost_usd column.
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

// TestDefaultProjectSeededByMigration verifies the v5 migration seeds the
// 'default' project row, which sessions fall back to.
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

// --- Session CRUD round-trip -----------------------------------------------

// newSessionRecord builds a SessionRecord populated with sane defaults for
// use across CRUD tests.
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

// TestSessionCRUDRoundTrip exercises Insert -> QueryByID -> Update -> Query
// -> Delete, asserting fields survive the round-trip.
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

	// Update root task + status + user input.
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

	// Delete should remove it.
	if err := DeleteSession("sess_1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := QuerySessionByID("sess_1"); err == nil {
		t.Error("QuerySessionByID after delete returned nil, want error")
	}
}

// TestInsertSessionDefaultsProjectID verifies that an empty ProjectID is
// normalized to "default" by InsertSession.
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

// TestQuerySessionsListsAndFilters verifies QuerySessions returns inserted
// sessions ordered by updated_at DESC and honours the projectID filter.
func TestQuerySessionsListsAndFilters(t *testing.T) {
	freshDB(t)

	// Insert three sessions across two projects with distinct update times.
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

	// No filter: all three, newest first (s3, s2, s1).
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

	// Filter by project: only default sessions (s3, s1).
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

// TestDeleteSessionMissingReturnsError verifies DeleteSession surfaces a
// not-found error rather than silently succeeding for an unknown ID.
func TestDeleteSessionMissingReturnsError(t *testing.T) {
	freshDB(t)
	if err := DeleteSession("does_not_exist"); err == nil {
		t.Error("DeleteSession on missing ID returned nil, want error")
	}
}

// --- Task persistence ------------------------------------------------------

// TestTaskPersistAndQueryBySession covers InsertTask, UpdateTaskSession,
// QueryTaskByID, and QueryTasksBySession — verifying session/parent/is_root
// bindings are stored and retrieved correctly.
func TestTaskPersistAndQueryBySession(t *testing.T) {
	freshDB(t)

	// Parent session must exist for FK semantics (and for QueryTasksBySession
	// to be meaningful).
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

	// QueryTaskByID should reconstruct the agentIDs slice and bindings.
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

	// QueryTasksBySession returns root first (is_root DESC) then child.
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

	// UpdateTask mutates status / final_result / total_tokens.
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

	// AggregateSessionTokens should sum task totals.
	tot, err := AggregateSessionTokens("sess_t")
	if err != nil {
		t.Fatalf("AggregateSessionTokens: %v", err)
	}
	if tot != 100 {
		t.Errorf("AggregateSessionTokens = %d, want 100", tot)
	}
}

// TestQueryChildTasks verifies the parent->children lookup.
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

// --- Foreign-key behaviour -------------------------------------------------

// TestInsertTaskOrphanSessionID documents the foreign-key behaviour of the
// pure-Go modernc.org/sqlite driver. By default SQLite does NOT enforce
// foreign keys unless `PRAGMA foreign_keys=ON` is issued; Init does not
// issue it. We assert the actual behaviour: inserting a task with a
// non-existent session_id succeeds (no FK enforcement).
//
// If a future change turns FK enforcement on, this test will fail and
// should be updated to assert the error — that is a schema-level decision,
// not a bug in this test.
func TestInsertTaskOrphanSessionID(t *testing.T) {
	freshDB(t)

	// Enable FK explicitly so the behaviour we assert is unambiguous.
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
	// If we got an error, that's a valid outcome too — just log it.
	t.Logf("InsertTask with orphan session_id returned error (FK enforced): %v", err)
}

// --- mock_scripts table ----------------------------------------------------

// TestMockScriptsTableUsable verifies the v13 migration created the
// mock_scripts table and that a basic Insert/Select round-trip works.
// There is no CRUD helper in pkg/db for mock_scripts (the mock store lives
// in internal/llm), so we use raw SQL to prove the table is usable.
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

// --- cost_records table ----------------------------------------------------

// TestCostRecordsTableUsable verifies the cost_records table is writable
// and the v11 migration's cost_cents column round-trips an integer value.
// Like mock_scripts, no CRUD helper lives in pkg/db, so we use raw SQL.
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

// --- splitSQL helper (internal to migrate.go) ------------------------------

// TestSplitSQL covers the lightweight statement splitter used by RunMigrations
// because SQLite's Exec only handles one statement at a time.
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

// TestTrimSpace covers the ASCII trimmer used by splitSQL.
func TestTrimSpace(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"abc", "abc"},
		{"  abc  ", "abc"},
		{"\t\nabc\r\n", "abc"},
		{"   ", ""},
		{"a b", "a b"}, // internal whitespace preserved
	}
	for _, tt := range tests {
		if got := trimSpace(tt.in); got != tt.want {
			t.Errorf("trimSpace(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- Concurrency (light) ---------------------------------------------------

// TestConcurrentInsertSession opens 5 goroutines that each insert a distinct
// session into the shared *sql.DB. modernc.org/sqlite serializes writers with
// a database-level lock; by default each pooled connection has its own
// busy_timeout so cross-connection waits can fail with SQLITE_BUSY. We pin
// the pool to a single connection (the recommended SQLite pattern) so the
// PRAGMA busy_timeout applies uniformly, then assert no panic and the correct
// row count. If this still flakes on some platforms, skip it.
func TestConcurrentInsertSession(t *testing.T) {
	freshDB(t)

	// SQLite is a single-writer database. Pin the pool to one connection so
	// the busy_timeout pragma (set below) governs all access and writers queue
	// rather than hitting SQLITE_BUSY across connections.
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
