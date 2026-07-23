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
	// 确保父目录存在
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

	// modernc.org/sqlite推荐使用单一连接模型，避免并发写导致的BUSY/LOCKED错误。
	// 设置MaxOpenConns(1)让所有数据库操作串行化，配合WAL和busy_timeout进一步提升并发容忍度。
	DB.SetMaxOpenConns(1)

	// 配置SQLite并发写行为：5秒busy_timeout + WAL日志
	// 注意：foreign_keys 不在此处全局开启，因为历史代码（包括 tests 和 orchestrator）
	// 在插入 task 前并不总是保证 session 已存在，开启 FK 会导致这些路径失败。
	// 外键一致性由应用层保证；如需强制 FK，应在已知 session 存在的特定事务内开启。
	pragmas := []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	}
	for _, pragma := range pragmas {
		if _, err := DB.Exec(pragma); err != nil {
			return fmt.Errorf("failed to execute %s: %w", pragma, err)
		}
	}

	if err := createTables(); err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	// 为已存在的数据库运行自动 schema migration
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
			duration_ms INTEGER DEFAULT 0,
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
			project_id TEXT DEFAULT 'default',
			turn_count INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0,
			context_size INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			workspace_dir TEXT DEFAULT '',
			workspace_auto BOOLEAN DEFAULT 1,
			active_worktree_id TEXT DEFAULT NULL
		)`,
		// Phase 5-A：项目管理和多轮对话相关表
		//   projects — 顶层组织单元，用于分组 session
		//   session_messages — 多轮对话的按轮次消息历史
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			working_directory TEXT DEFAULT '',
			config JSON DEFAULT '{}',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS session_messages (
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
		// Phase 6：Memory 基础设施相关表
		//   memories — 合并后的 episodic 摘要以及 semantic/policy 规则
		//   memory_links — memory 记录之间的关系
		`CREATE TABLE IF NOT EXISTS memories (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL DEFAULT 'default',
			type TEXT NOT NULL,
			tier TEXT NOT NULL,
			content TEXT NOT NULL,
			embedding BLOB,
			confidence REAL DEFAULT 1.0,
			status TEXT DEFAULT 'active',
			scope TEXT DEFAULT 'project',
			session_id TEXT DEFAULT '',
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
		// Phase todo: 待办事项表，用于在多轮会话/多 Agent 任务中跟踪子任务。
		`CREATE TABLE IF NOT EXISTS todos (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			created_by_task_id TEXT NOT NULL,
			active_task_id TEXT,
			parent_todo_id TEXT,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			priority INTEGER DEFAULT 0,
			sort_order INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			completed_at INTEGER
		)`,
	}

	for _, schema := range schemas {
		if _, err := DB.Exec(schema); err != nil {
			return err
		}
	}

	// Phase todo: todos 表索引
	todoIndexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_todos_session_id ON todos(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_todos_status ON todos(status)`,
		`CREATE INDEX IF NOT EXISTS idx_todos_created_by_task_id ON todos(created_by_task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_todos_active_task_id ON todos(active_task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_todos_parent_todo_id ON todos(parent_todo_id)`,
		`CREATE INDEX IF NOT EXISTS idx_todos_priority_sort_order_created_at ON todos(priority DESC, sort_order ASC, created_at ASC)`,
	}
	for _, idx := range todoIndexes {
		if _, err := DB.Exec(idx); err != nil {
			return err
		}
	}

	// Phase 5-A：用于多轮对话查询的 session_messages 索引
		phase5AIndexes := []string{
			`CREATE INDEX IF NOT EXISTS idx_session_messages_session_id ON session_messages(session_id)`,
			`CREATE INDEX IF NOT EXISTS idx_session_messages_task_id ON session_messages(task_id)`,
			`CREATE INDEX IF NOT EXISTS idx_session_messages_turn_index ON session_messages(session_id, turn_index)`,
			`CREATE INDEX IF NOT EXISTS idx_projects_updated_at ON projects(updated_at DESC)`,
		}
		for _, idx := range phase5AIndexes {
			if _, err := DB.Exec(idx); err != nil {
				return err
			}
		}

		// Phase 5：用于 session 和 task 层级查询的索引
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

	// Phase 6：Memory 基础设施索引
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

	// MCP servers：动态 + marketplace server 配置的持久化
	mcpSchemas := []string{
		`CREATE TABLE IF NOT EXISTS mcp_servers (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT 'db',
			name TEXT NOT NULL,
			transport TEXT NOT NULL DEFAULT 'stdio',
			command TEXT,
			args JSON DEFAULT '[]',
			endpoint TEXT,
			environment JSON DEFAULT '{}',
			enabled BOOLEAN DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_servers_enabled ON mcp_servers(enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_mcp_servers_source ON mcp_servers(source)`,
	}
	for _, schema := range mcpSchemas {
		if _, err := DB.Exec(schema); err != nil {
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