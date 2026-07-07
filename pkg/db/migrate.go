// migrate.go — automatic database schema migration
//
// SQLite's CREATE TABLE IF NOT EXISTS is idempotent but doesn't add new columns
// to existing tables. We implement a lightweight migration system that:
//   1. Tracks applied migrations in a `schema_migrations` table
//   2. Runs pending migrations in order on startup
//   3. Migrations are defined as a list of {version, description, sql} entries
//
// This mimics GORM's AutoMigrate behavior — add a column to an existing table
// by adding a new migration entry. No manual DDL needed.
package db

import (
	"fmt"
	"log"
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
		SQL: `ALTER TABLE agents ADD COLUMN is_default BOOLEAN DEFAULT 0`,
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
		SQL: `UPDATE sessions SET root_task_id = (SELECT id FROM tasks WHERE tasks.session_id = sessions.id AND tasks.is_root = 1 LIMIT 1) WHERE (root_task_id = '' OR root_task_id IS NULL)`,
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

	// v8: Backfill existing sessions and memories with default values.
	// Ensures pre-existing data conforms to the new schema expectations.
	{
		Version:     8,
		Description: "Backfill project_id for sessions and scope for memories",
		SQL: `UPDATE sessions SET project_id = 'default' WHERE project_id = '' OR project_id IS NULL;
		UPDATE memories SET scope = 'project' WHERE scope = '' OR scope IS NULL`,
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
				// SQLite ALTER TABLE ADD COLUMN errors if column already exists.
				// This can happen if the DB was partially migrated or manually fixed.
				// Log and continue — this is a best-effort migration.
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