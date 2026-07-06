package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func Init(dataPath string) error {
	// Ensure parent directory exists
	dir := filepath.Dir(dataPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}
	var err error
	DB, err = sql.Open("sqlite", dataPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err := DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	if err := createTables(); err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	// Run automatic schema migrations for existing databases
	if err := RunMigrations(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Println("Database initialized successfully")
	return nil
}

func createTables() error {
	schemas := []string{
		`CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			system_prompt TEXT,
			model TEXT,
			temperature REAL DEFAULT 0.7,
			max_tokens INTEGER DEFAULT 4096,
			api_endpoint TEXT,
			api_key TEXT,
			tools JSON DEFAULT '[]',
			config JSON DEFAULT '{}',
			is_default BOOLEAN DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			user_input TEXT DEFAULT '',
			status TEXT DEFAULT 'empty',
			agent_ids JSON DEFAULT '[]',
			final_result TEXT,
			total_tokens INTEGER DEFAULT 0,
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME,
			session_id TEXT,
			parent_task_id TEXT,
			is_root BOOLEAN DEFAULT 0,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			FOREIGN KEY (parent_task_id) REFERENCES tasks(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS steps (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			type TEXT NOT NULL,
			status TEXT DEFAULT 'running',
			content TEXT,
			tool_name TEXT,
			tool_input JSON,
			tool_output TEXT,
			duration_ms INTEGER DEFAULT 0,
			token_used INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (task_id) REFERENCES tasks(id)
		)`,
		`CREATE TABLE IF NOT EXISTS tools (
			name TEXT PRIMARY KEY,
			description TEXT,
			schema JSON,
			enabled BOOLEAN DEFAULT true,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (task_id) REFERENCES tasks(id)
		)`,
		`CREATE TABLE IF NOT EXISTS files (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			filename TEXT NOT NULL,
			path TEXT NOT NULL,
			size_bytes INTEGER,
			mime_type TEXT,
			metadata JSON DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			root_task_id TEXT,
			status TEXT NOT NULL DEFAULT 'empty',
			user_input TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// Phase 6: Memory infrastructure tables
		//   memories — consolidated episodic summaries and semantic/policy rules
		//   memory_links — relationships between memory records
		`CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL DEFAULT 'default',
			type TEXT NOT NULL,
			tier TEXT NOT NULL,
			content TEXT NOT NULL,
			embedding BLOB,
			confidence REAL DEFAULT 1.0,
			status TEXT DEFAULT 'active',
			source_task_ids JSON,
			source_event_ids JSON,
			promotion_reason TEXT,
			access_count INT DEFAULT 0,
			last_accessed DATETIME,
			last_reviewed DATETIME,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS memory_links (
			source_id TEXT NOT NULL,
			target_id TEXT NOT NULL,
			relation TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (source_id, target_id),
			FOREIGN KEY (source_id) REFERENCES memories(id),
			FOREIGN KEY (target_id) REFERENCES memories(id)
		)`,
	}

	for _, schema := range schemas {
		if _, err := DB.Exec(schema); err != nil {
			return err
		}
	}

	// Phase 5: indexes for session and task hierarchy queries
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_tasks_session_id ON tasks(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_parent_task_id ON tasks(parent_task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions(status)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_root_task_id ON sessions(root_task_id)`,
	}
	for _, idx := range indexes {
		if _, err := DB.Exec(idx); err != nil {
			return err
		}
	}

	// Phase 6: Memory infrastructure indexes
	memoryIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_memories_project_id ON memories(project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_tier ON memories(tier)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_status ON memories(status)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_updated_at ON memories(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_links_source ON memory_links(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_memory_links_target ON memory_links(target_id)`,
	}
	for _, idx := range memoryIndexes {
		if _, err := DB.Exec(idx); err != nil {
			return err
		}
	}

	return nil
}

func Close() error {
	if DB != nil {
		return DB.Close()
	}
	return nil
}