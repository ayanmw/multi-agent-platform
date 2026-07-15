package cases

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/harness"

	_ "modernc.org/sqlite"
)

// setupTestDB creates an in-memory SQLite database and applies the cases table schema.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS cases (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			icon TEXT,
			category TEXT,
			system_prompt TEXT,
			default_input TEXT,
			contract_json TEXT NOT NULL,
			tags_json TEXT,
			is_builtin INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`); err != nil {
		t.Fatalf("create cases table: %v", err)
	}
	return d
}

// TestAll returns five builtin cases.
func TestAll(t *testing.T) {
	cases := All()
	if len(cases) != 5 {
		t.Fatalf("expected 5 builtin cases, got %d", len(cases))
	}
	ids := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" {
			t.Errorf("case ID is empty")
		}
		if !c.IsBuiltin {
			t.Errorf("case %s should be builtin", c.ID)
		}
		ids[c.ID] = true
	}
	expected := []string{"code-gen", "research", "multi-agent", "dialogue", "long-task"}
	for _, id := range expected {
		if !ids[id] {
			t.Errorf("missing builtin case %s", id)
		}
	}
}

// TestGetBuiltin returns a builtin case by id.
func TestGetBuiltin(t *testing.T) {
	c := Get("code-gen")
	if c == nil {
		t.Fatal("expected code-gen case")
	}
	if c.ID != "code-gen" {
		t.Errorf("expected code-gen, got %s", c.ID)
	}
	if c := Get("not-exist"); c != nil {
		t.Errorf("expected nil for non-existent case")
	}
}

// TestServiceSeedsBuiltins initializes an empty DB and verifies seeding.
func TestServiceSeedsBuiltins(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}

	count, err := svc.repo.CountAll()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 seeded cases, got %d", count)
	}

	c, err := svc.Get("code-gen")
	if err != nil {
		t.Fatalf("get code-gen: %v", err)
	}
	if c.ID != "code-gen" {
		t.Errorf("expected code-gen, got %s", c.ID)
	}
	if !c.IsBuiltin {
		t.Errorf("service.Get should return builtin case with IsBuiltin=true")
	}

	// 同时校验数据库中的种子行 is_builtin 位正确写入。
	seeded, err := svc.repo.GetByID("code-gen")
	if err != nil {
		t.Fatalf("get seeded code-gen from repo: %v", err)
	}
	if !seeded.IsBuiltin {
		t.Errorf("seeded builtin case should have IsBuiltin=true")
	}
}

// TestServiceDoesNotReseedWhenNotEmpty ensures re-Init does not duplicate cases.
func TestServiceDoesNotReseedWhenNotEmpty(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	if _, err := svc.Create(newValidCreate()); err != nil {
		t.Fatalf("create case: %v", err)
	}

	// Re-init on non-empty DB should keep existing rows (6 total).
	svc2, err := Init(d)
	if err != nil {
		t.Fatalf("re-init service: %v", err)
	}
	count, err := svc2.repo.CountAll()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 6 {
		t.Fatalf("expected 6 cases, got %d", count)
	}
}

// TestCreateCustomCase validates creation and that it appears in listings.
func TestCreateCustomCase(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}

	req := newValidCreate()
	created, err := svc.Create(req)
	if err != nil {
		t.Fatalf("create custom case: %v", err)
	}
	if created.ID == "" || !strings.HasPrefix(created.ID, "case-") {
		t.Errorf("expected case id to start with case-, got %s", created.ID)
	}
	if created.IsBuiltin {
		t.Errorf("custom case should not be builtin")
	}

	got, err := svc.Get(created.ID)
	if err != nil {
		t.Fatalf("get created case: %v", err)
	}
	if got.Name != req.Name {
		t.Errorf("expected name %q, got %q", req.Name, got.Name)
	}
	if got.Category != req.Category {
		t.Errorf("expected category %q, got %q", req.Category, got.Category)
	}
	if got.Contract.MaxSteps != req.Contract.MaxSteps {
		t.Errorf("expected max steps %d, got %d", req.Contract.MaxSteps, got.Contract.MaxSteps)
	}
}

// TestCreateValidation checks validation rules for creation.
func TestCreateValidation(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}

	bad := newValidCreate()
	bad.Name = "   "
	if _, err := svc.Create(bad); err == nil {
		t.Errorf("expected error for empty name")
	}

	bad = newValidCreate()
	bad.Category = ""
	if _, err := svc.Create(bad); err == nil {
		t.Errorf("expected error for empty category")
	}

	bad = newValidCreate()
	bad.Contract.MaxSteps = 0
	if _, err := svc.Create(bad); err == nil {
		t.Errorf("expected error for max_steps=0")
	}

	bad = newValidCreate()
	bad.Contract.MaxSteps = 101
	if _, err := svc.Create(bad); err == nil {
		t.Errorf("expected error for max_steps=101")
	}
}

// TestUpdateCustomCase checks update behavior for custom cases.
func TestUpdateCustomCase(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	created, err := svc.Create(newValidCreate())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newName := "Updated Name"
	newCategory := "updated-category"
	newSteps := 42
	updated, err := svc.Update(created.ID, UpdateCaseRequest{
		Name:     &newName,
		Category: &newCategory,
		Contract: &harness.TaskContract{MaxSteps: newSteps, Permissions: harness.TaskPermissions{AllowFileWrite: true}},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != newName {
		t.Errorf("expected name %q, got %q", newName, updated.Name)
	}
	if updated.Category != newCategory {
		t.Errorf("expected category %q, got %q", newCategory, updated.Category)
	}
	if updated.Contract.MaxSteps != newSteps {
		t.Errorf("expected max steps %d, got %d", newSteps, updated.Contract.MaxSteps)
	}
}

// TestUpdateBuiltinRejected ensures builtin cases cannot be updated.
func TestUpdateBuiltinRejected(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	newName := "Hacked"
	if _, err := svc.Update("code-gen", UpdateCaseRequest{Name: &newName}); err == nil {
		t.Errorf("expected error updating builtin case")
	}
}

// TestDeleteCustomCase removes a custom case and verifies it is gone.
func TestDeleteCustomCase(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	created, err := svc.Create(newValidCreate())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Delete(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(created.ID); err == nil {
		t.Errorf("expected error getting deleted case")
	}
}

// TestDeleteBuiltinRejected ensures builtin cases cannot be deleted.
func TestDeleteBuiltinRejected(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	if err := svc.Delete("dialogue"); err == nil {
		t.Errorf("expected error deleting builtin case")
	}
}

// TestListFiltering verifies tag and category filtering.
func TestListFiltering(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}

	all, err := svc.List(nil, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) < 5 {
		t.Fatalf("expected at least 5 cases, got %d", len(all))
	}

	// Builtin code-gen has category "generation".
	generation, err := svc.List(nil, "generation")
	if err != nil {
		t.Fatalf("list generation: %v", err)
	}
	found := false
	for _, c := range generation {
		if c.ID == "code-gen" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected code-gen in generation category")
	}

	// Builtin dialogue has tag "dialogue".
	dialogue, err := svc.List([]string{"dialogue"}, "")
	if err != nil {
		t.Fatalf("list by tag: %v", err)
	}
	found = false
	for _, c := range dialogue {
		if c.ID == "dialogue" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected dialogue case by tag")
	}

	// Combined filter: category interaction + tag baseline should match dialogue.
	combined, err := svc.List([]string{"baseline"}, "interaction")
	if err != nil {
		t.Fatalf("list combined: %v", err)
	}
	found = false
	for _, c := range combined {
		if c.ID == "dialogue" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected dialogue with combined filter")
	}
}

// TestListWithNoMatchTag returns empty when tags don't match.
func TestListWithNoMatchTag(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	svc, err := Init(d)
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	res, err := svc.List([]string{"nonexistent-tag"}, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected no matches, got %d", len(res))
	}
}

// TestRepositoryListExcludesBuiltins verifies repo.List filters out builtin rows.
func TestRepositoryListExcludesBuiltins(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepository(d)
	builtinLike := Case{
		ID:           "builtin-like",
		Name:         "Builtin Like",
		Category:     "test",
		SystemPrompt: "prompt",
		DefaultInput: "input",
		Contract:     harness.TaskContract{MaxSteps: 5},
		IsBuiltin:    true,
	}
	if _, err := repo.Create(builtinLike); err != nil {
		t.Fatalf("create builtin-like: %v", err)
	}

	custom := Case{
		Name:         "Custom Case",
		Category:     "test",
		SystemPrompt: "prompt",
		DefaultInput: "input",
		Contract:     harness.TaskContract{MaxSteps: 5},
		IsBuiltin:    false,
	}
	if _, err := repo.Create(custom); err != nil {
		t.Fatalf("create custom: %v", err)
	}

	all, err := repo.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 custom case, got %d", len(all))
	}
	if all[0].IsBuiltin {
		t.Errorf("repo.List should not return builtin rows")
	}
}

// TestRepositoryCRUD directly tests the repository layer.
func TestRepositoryCRUD(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	repo := NewRepository(d)
	c := Case{
		Name:         "Repo Case",
		Description:  "from repo",
		Category:     "test",
		SystemPrompt: "repo prompt",
		DefaultInput: "repo input",
		Contract:     harness.TaskContract{MaxSteps: 5},
		Tags:         []string{"repo", "test"},
	}
	created, err := repo.Create(c)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected generated id")
	}

	got, err := repo.GetByID(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != c.Name {
		t.Errorf("expected name %q, got %q", c.Name, got.Name)
	}

	all, err := repo.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("expected 1 case, got %d", len(all))
	}

	got.Name = "Updated Repo Case"
	updated, err := repo.Update(*got)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Updated Repo Case" {
		t.Errorf("expected updated name")
	}

	if err := repo.Delete(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	count, err := repo.CountAll()
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 after delete, got %d", count)
	}
}

// newValidCreate returns a valid CreateCaseRequest for tests.
func newValidCreate() CreateCaseRequest {
	return CreateCaseRequest{
		Name:         "My Custom Case",
		Description:  "A test case",
		Icon:         "🧪",
		Category:     "test",
		SystemPrompt: "You are a test agent.",
		DefaultInput: "Run the test.",
		Contract: &harness.TaskContract{
			MaxSteps:    10,
			Permissions: harness.TaskPermissions{AllowFileWrite: true},
		},
		Tags: []string{"test", "custom"},
	}
}

