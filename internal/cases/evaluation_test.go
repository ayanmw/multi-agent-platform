package cases

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupEvalTestDB 创建一个带 case_evaluations schema 的临时 SQLite 数据库。
func setupEvalTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS case_evaluations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			case_id TEXT NOT NULL,
			passed INTEGER NOT NULL,
			score REAL,
			reason TEXT,
			evaluated_at DATETIME NOT NULL
		)`); err != nil {
		t.Fatalf("create case_evaluations table: %v", err)
	}
	return d
}

func TestRepositorySaveAndGetEvaluation(t *testing.T) {
	d := setupEvalTestDB(t)
	defer d.Close()

	repo := NewRepository(d)
	eval := CaseEvaluation{
		TaskID:      "task-abc",
		CaseID:      "case-xyz",
		Passed:      true,
		Score:       0.91,
		Reason:      "all good",
		EvaluatedAt: time.Now(),
	}
	if err := repo.SaveEvaluation(eval); err != nil {
		t.Fatalf("save evaluation: %v", err)
	}

	got, err := repo.GetEvaluation(eval.TaskID, eval.CaseID)
	if err != nil {
		t.Fatalf("get evaluation: %v", err)
	}
	if got.TaskID != eval.TaskID || got.CaseID != eval.CaseID {
		t.Fatalf("ids mismatch: got %s/%s", got.TaskID, got.CaseID)
	}
	if !got.Passed || got.Score != eval.Score || got.Reason != eval.Reason {
		t.Fatalf("evaluation mismatch: got %+v", got)
	}
}

func TestRepositoryGetEvaluationMissing(t *testing.T) {
	d := setupEvalTestDB(t)
	defer d.Close()

	repo := NewRepository(d)
	_, err := repo.GetEvaluation("no-such-task", "no-such-case")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestRepositoryGetEvaluationReturnsMostRecent(t *testing.T) {
	d := setupEvalTestDB(t)
	defer d.Close()

	repo := NewRepository(d)
	now := time.Now()
	if err := repo.SaveEvaluation(CaseEvaluation{TaskID: "t1", CaseID: "c1", Passed: false, Score: 0.1, Reason: "old", EvaluatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("save old: %v", err)
	}
	if err := repo.SaveEvaluation(CaseEvaluation{TaskID: "t1", CaseID: "c1", Passed: true, Score: 0.9, Reason: "new", EvaluatedAt: now}); err != nil {
		t.Fatalf("save new: %v", err)
	}

	got, err := repo.GetEvaluation("t1", "c1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Passed || got.Score != 0.9 || got.Reason != "new" {
		t.Fatalf("expected newest evaluation, got %+v", got)
	}
}
