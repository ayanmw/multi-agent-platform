// migrate.go — automatic database schema migration
//
// SQLite's CREATE TABLE IF NOT EXISTS is idempotent but doesn't add new columns
// to existing tables. We implement a lightweight migration system that:
//  1. Tracks applied migrations in a `schema_migrations` table
//  2. Runs pending migrations in order on startup
//  3. Migrations are defined as a list of {version, description, sql} entries
//
// This mimics GORM's AutoMigrate behavior — add a column to an existing table
// by adding a new migration entry. No manual DDL needed.
package db

import (
	"fmt"
	"log"
	"strings"
)

// Migration represents a single schema change.
type Migration struct {
	Version     int    // monotonically increasing version number
	Description string // human-readable description
	SQL         string // the DDL statement to execute
}

// All migrations in chronological order.
// Add new migrations at the END of the list. Never reorder or delete existing entries.
var migrations = []Migration{
	// v1: Initial schema — all tables as defined in database.go createTables()
	// This migration is a no-op because createTables() handles the initial creation.
	// We just seed the schema_migrations table so future migrations can run.
	{
		Version:     1,
		Description: "Initial schema (createTables handles table creation)",
		SQL:         `SELECT 1`, // no-op, createTables() already ran
	},

	// v2: Add is_default column to agents table
	{
		Version:     2,
		Description: "Add is_default BOOLEAN column to agents table",
		SQL:         `ALTER TABLE agents ADD COLUMN is_default BOOLEAN DEFAULT 0`,
	},

	// v3: Add session_id, parent_task_id, is_root columns to tasks table
	{
		Version:     3,
		Description: "Add session_id, parent_task_id, is_root columns to tasks table",
		SQL: `ALTER TABLE tasks ADD COLUMN session_id TEXT;
ALTER TABLE tasks ADD COLUMN parent_task_id TEXT;
ALTER TABLE tasks ADD COLUMN is_root BOOLEAN DEFAULT 0`,
	},

	// v4: Backfill root_task_id for existing sessions that have root tasks
	{
		Version:     4,
		Description: "Backfill root_task_id for existing sessions from their root tasks",
		SQL:         `UPDATE sessions SET root_task_id = (SELECT id FROM tasks WHERE tasks.session_id = sessions.id AND tasks.is_root = 1 LIMIT 1) WHERE (root_task_id = '' OR root_task_id IS NULL)`,
	},

	// v5: Create projects table and seed default project.
	// Projects serve as the top-level organizational unit for grouping sessions.
	{
		Version:     5,
		Description: "Create projects table and seed default project",
		SQL: `CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			working_directory TEXT DEFAULT '',
			config JSON DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT OR IGNORE INTO projects (id, name, description) VALUES ('default', 'Default Project', 'Default project created during migration')`,
	},

	// v6: Create session_messages table for multi-turn conversation tracking.
	{
		Version:     6,
		Description: "Create session_messages table for multi-turn conversations",
		SQL: `CREATE TABLE IF NOT EXISTS session_messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			turn_index INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			tool_call_id TEXT,
			tool_calls JSON,
			token_count INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (session_id) REFERENCES sessions(id),
			FOREIGN KEY (task_id) REFERENCES tasks(id)
		)`,
	},

	// v7: Extend sessions, tasks, and memories tables with new columns
	// for project association, turn tracking, and memory scoping.
	{
		Version:     7,
		Description: "Add project_id, turn_count, total_tokens, context_size to sessions; turn_index to tasks; scope to memories",
		SQL: `ALTER TABLE sessions ADD COLUMN project_id TEXT DEFAULT 'default';
		ALTER TABLE sessions ADD COLUMN turn_count INTEGER DEFAULT 0;
		ALTER TABLE sessions ADD COLUMN total_tokens INTEGER DEFAULT 0;
		ALTER TABLE sessions ADD COLUMN context_size INTEGER DEFAULT 0;
		ALTER TABLE tasks ADD COLUMN turn_index INTEGER DEFAULT 0;
		ALTER TABLE memories ADD COLUMN scope TEXT DEFAULT 'project'`,
	},

	// v8: Placeholder migration (no-op) — v8 reserved for future schema change
	{
		Version:     8,
		Description: "Placeholder migration (no-op) — v8 reserved for future schema change",
		SQL:         `SELECT 1`,
	},

	// v9: Add session_id column to memories table for session-scoped memories
	{
		Version:     9,
		Description: "Add session_id column to memories table for session-scoped memories",
		SQL:         `ALTER TABLE memories ADD COLUMN session_id TEXT DEFAULT ''`,
	},

	// v10: Create cost_records table for LLM cost tracking.
	// Tracks every LLM call's token consumption and USD cost, indexed by
	// task, session, and project for multi-dimensional cost reporting.
	{
		Version:     10,
		Description: "Create cost_records table for LLM call cost tracking",
		SQL: `CREATE TABLE IF NOT EXISTS cost_records (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			session_id TEXT DEFAULT '',
			project_id TEXT DEFAULT 'default',
			agent_id TEXT NOT NULL,
			step_index INTEGER DEFAULT 0,
			model TEXT NOT NULL,
			provider TEXT NOT NULL,
			tier TEXT DEFAULT 'standard',
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0,
			cost_usd REAL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_cost_records_task ON cost_records(task_id);
		CREATE INDEX IF NOT EXISTS idx_cost_records_session ON cost_records(session_id);
		CREATE INDEX IF NOT EXISTS idx_cost_records_project ON cost_records(project_id);`,
	},

	// v11: Add integer cost_cents column to cost_records for precise accounting.
	//
	// Existing rows are backfilled from cost_usd*100 so the CostRepository can
	// read all rows with a non-null cost_cents value. SQLite does not have ALTER
	// ADD COLUMN IF NOT EXISTS, so failures because the column already exists
	// are silently ignored by RunMigrations (only non-"duplicate column name"
	// errors are logged, to avoid noise on every startup).
	{
		Version:     11,
		Description: "Add cost_cents column to cost_records for integer precision",
		SQL: `ALTER TABLE cost_records ADD COLUMN cost_cents INTEGER DEFAULT 0;
		UPDATE cost_records SET cost_cents = CAST(ROUND(cost_usd * 100) AS INTEGER) WHERE cost_cents = 0 AND cost_usd <> 0;`,
	},

	// v12: Create users and api_keys tables for API key authentication.
	// users stores user identity and role; api_keys stores bcrypt-hashed keys
	// with prefix for fast lookup during verification.
	{
		Version:     12,
		Description: "Create users and api_keys tables for API key auth",
		SQL: `CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_used_at DATETIME,
			revoked_at DATETIME,
			FOREIGN KEY (user_id) REFERENCES users(id)
		);
		CREATE INDEX IF NOT EXISTS idx_api_keys_user_id ON api_keys(user_id);
		CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(prefix);`,
	},

	// v13: Create mock_scripts table for deterministic LLM mock responses.
	// Mock scripts store case-specific response sequences and optional input
	// keyword matching rules for LLM testing without calling real providers.
	{
		Version:     13,
		Description: "Create mock_scripts table for LLM mock response sequences",
		SQL: `CREATE TABLE IF NOT EXISTS mock_scripts (
			id TEXT PRIMARY KEY,
			case_id TEXT,
			priority INTEGER DEFAULT 0,
			match_input TEXT,
			responses TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_mock_scripts_case_id ON mock_scripts(case_id);`,
	},

	// v14: Add duration_ms column to tasks table for task-level elapsed time tracking.
	{
		Version:     14,
		Description: "Add duration_ms column to tasks table",
		SQL:         `ALTER TABLE tasks ADD COLUMN duration_ms INTEGER DEFAULT 0`,
	},

	// v15: Add workspace_dir and workspace_auto columns to sessions table.
	{
		Version:     15,
		Description: "Add workspace_dir and workspace_auto columns to sessions table",
		SQL:         `ALTER TABLE sessions ADD COLUMN workspace_dir TEXT DEFAULT ''; ALTER TABLE sessions ADD COLUMN workspace_auto BOOLEAN DEFAULT 1`,
	},

	// v16: Create memory_embeddings table for the SqliteVectorStore (Phase 6-F).
	//
	// Decouples vector storage from the memories table itself: embedding rows
	// live in their own keyed table so that (a) vector I/O can be batched
	// without scanning the full memories row, (b) swapping the embedding model
	// only invalidates a subset (filtered by the model column), and (c) the
	// ON DELETE CASCADE keeps the table consistent when a memory is removed.
	//
	// The embedding BLOB is a little-endian float32 serialization (length =
	// dims * 4 bytes). See pkg/db/memory_embedding.go for the encode/decode
	// helpers and the design rationale.
	{
		Version:     16,
		Description: "Create memory_embeddings table for SqliteVectorStore persistence",
		SQL: `CREATE TABLE IF NOT EXISTS memory_embeddings (
			memory_id  TEXT PRIMARY KEY,
			embedding  BLOB NOT NULL,
			model      TEXT NOT NULL,
			dims       INTEGER NOT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(memory_id) REFERENCES memories(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_memory_embeddings_model ON memory_embeddings(model);`,
	},

	// v17: Create cases and case_evaluations tables for the Case management feature.
	//
	// cases store built-in or user-defined test cases (contract, prompts, tags,
	// category). case_evaluations store the result of executing a case against
	// a task, supporting pass/fail tracking and optional scoring. Both tables
	// are created idempotently and indexed on the query paths used by the
	// repository layer in subsequent tasks.
	{
		Version:     17,
		Description: "Create cases and case_evaluations tables",
		SQL: `CREATE TABLE IF NOT EXISTS cases (
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
		);
		CREATE INDEX IF NOT EXISTS idx_cases_category ON cases(category);
		CREATE INDEX IF NOT EXISTS idx_cases_is_builtin ON cases(is_builtin);

		CREATE TABLE IF NOT EXISTS case_evaluations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			case_id TEXT NOT NULL,
			passed INTEGER NOT NULL,
			score REAL,
			reason TEXT,
			evaluated_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_eval_task ON case_evaluations(task_id);`,
	},

	// v18: Create agent_messages table for the AgentBus persistence feature.
	//
	// Every inter-agent message routed through the AgentBus is persisted here so
	// the frontend can fetch the full message history for a task via
	// GET /api/tasks/:id/agent-messages. The task_id column is indexed because
	// the primary query path is "all messages for a given task, oldest first".
	// Metadata is stored as JSON text — keeping it opaque avoids schema churn
	// when new metadata keys are added.
	{
		Version:     18,
		Description: "Create agent_messages table for AgentBus persistence",
		SQL: `CREATE TABLE IF NOT EXISTS agent_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			from_agent_id TEXT NOT NULL,
			to_agent_id TEXT NOT NULL,
			msg_type TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_agent_messages_task_id ON agent_messages(task_id);`,
	},

	// v19: Create approvals table for leader delegation of approval decisions.
	//
	// Each row records a high-risk tool call that required approval, including
	// whether the request was delegated to the leader and the leader's final
	// decision. This supports auditability, replay, and frontend dashboards.
	{
		Version:     19,
		Description: "Create approvals table for leader delegated approvals",
		SQL: `CREATE TABLE IF NOT EXISTS approvals (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			sub_task_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			tool TEXT NOT NULL,
			reason TEXT,
			input JSON,
			delegated_to_leader BOOLEAN DEFAULT 0,
			leader_sub_task_id TEXT,
			leader_decision_step_id TEXT,
			approved BOOLEAN,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			decided_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_approvals_task_id ON approvals(task_id);
		CREATE INDEX IF NOT EXISTS idx_approvals_sub_task_id ON approvals(sub_task_id);`,
	},
}

// createMigrationsTable ensures the schema_migrations tracking table exists.
func createMigrationsTable() error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		description TEXT NOT NULL,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	return err
}

// getAppliedMigrations returns the set of migration versions already applied.
func getAppliedMigrations() (map[int]bool, error) {
	rows, err := DB.Query("SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// RunMigrations executes all pending migrations.
// Called after createTables() in Init() so that tables already exist.
func RunMigrations() error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}

	if err := createMigrationsTable(); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	applied, err := getAppliedMigrations()
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue // already applied
		}

		log.Printf("[Migration] v%d: %s", m.Version, m.Description)

		// SQLite doesn't support multiple statements in one Exec call,
		// so we split on semicolons and execute each statement separately.
		// Note: this simple split works for our DDL use cases. If we ever
		// need multi-statement with semicolons inside strings, we'd need a parser.
		statements := splitSQL(m.SQL)

		for _, stmt := range statements {
			if stmt == "" {
				continue
			}
			if _, err := DB.Exec(stmt); err != nil {
				// SQLite 没有 ALTER TABLE ADD COLUMN IF NOT EXISTS，因此对已有列
				// 重复执行 ALTER 会返回 "duplicate column name" 错误。
				//
				// 这类错误是预期行为：当 createTables() 已在新库里建好该列、或
				// 迁移曾部分应用过、或 DB 被手工修过时，列已存在就会触发。它不是
				// 真实故障，迁移仍应继续并把该版本标记为已应用。
				//
				// 为避免这些预期内的"已存在"日志在每次启动时刷屏、淹没真实错误，
				// 这里对 "duplicate column name" 错误静默跳过（不打印）；其它真正
				// 意料之外的执行错误才按原逻辑打印，便于排查。
				if strings.Contains(err.Error(), "duplicate column name") {
					continue
				}
				log.Printf("[Migration] v%d: statement failed (may already exist): %v", m.Version, err)
				continue
			}
		}

		// Record the migration as applied
		_, err := DB.Exec(
			"INSERT INTO schema_migrations (version, description) VALUES (?, ?)",
			m.Version, m.Description,
		)
		if err != nil {
			return fmt.Errorf("record migration v%d: %w", m.Version, err)
		}

		log.Printf("[Migration] v%d: applied successfully", m.Version)
	}

	return nil
}

// splitSQL splits a SQL string by semicolons, trimming whitespace.
// Used because SQLite's Exec() handles one statement at a time.
func splitSQL(s string) []string {
	var result []string
	var current []byte
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			stmt := trimSpace(string(current))
			if stmt != "" {
				result = append(result, stmt)
			}
			current = nil
		} else {
			current = append(current, s[i])
		}
	}
	// Don't forget the last statement (no trailing semicolon)
	stmt := trimSpace(string(current))
	if stmt != "" {
		result = append(result, stmt)
	}
	return result
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
