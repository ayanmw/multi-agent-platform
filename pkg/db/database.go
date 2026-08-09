package db

import (
	"database/sql"
	"fmt"
	"log/slog"
)

// DB 是全局共享的数据库句柄，所有 CRUD（persistence.go / memory.go / cron.go ...）
// 都直接使用它。N3-04c 引入 Backend 抽象后，它的**赋值路径**收敛到
// InitWithBackend 一处，但类型与使用方式保持不变（零 blast radius）。
var DB *sql.DB

// Init 使用默认后端（SQLite）初始化数据库。
//
// 保留原签名与原语义：dataPath 是 SQLite 数据文件路径（或 `:memory:`）。
// 全仓 25+ 个调用点（含所有单测）无需改动。想选择其它后端时用 InitWithBackend。
func Init(dataPath string) error {
	return InitWithBackend(DefaultBackendName, dataPath)
}

// InitWithBackend 按指定后端名初始化数据库（N3-04c / E7）。
//
// 统一的初始化流水线，各后端只负责填充自己的差异：
//
//	NormalizeDSN → Open → Ping → Configure → Bootstrap(建表+迁移)
//
// 关于 Bootstrap 的容错：若后端声明不支持内建 schema 引导
// （errors.Is(err, ErrSchemaBootstrapUnsupported)），这里**不静默跳过**——
// 直接返回错误并附带修复指引。原因是「连上了但没有表」的半初始化状态会在
// 首个请求处以难以定位的方式炸掉，远不如启动即失败清晰。
// 外部迁移已就绪的部署可通过 InitWithBackendOptions 显式跳过。
func InitWithBackend(backendName, dsn string) error {
	return InitWithBackendOptions(backendName, dsn, InitOptions{})
}

// InitOptions 控制初始化流水线中的可选行为。
type InitOptions struct {
	// SkipBootstrap 跳过建表与迁移步骤。
	// 适用于「schema 已由外部迁移工具（golang-migrate / atlas）管理」的部署，
	// 也是非 SQLite 后端目前唯一可用的启动方式。
	SkipBootstrap bool
}

// InitWithBackendOptions 是 InitWithBackend 的完整形式。
func InitWithBackendOptions(backendName, dsn string, opts InitOptions) error {
	backend, err := LookupBackend(backendName)
	if err != nil {
		return err
	}

	normalizedDSN, err := backend.NormalizeDSN(dsn)
	if err != nil {
		return err
	}

	sqlDB, err := backend.Open(normalizedDSN)
	if err != nil {
		return err
	}

	if err := sqlDB.Ping(); err != nil {
		// Open 是惰性的，Ping 才真正建连；失败时必须关闭句柄，避免泄漏。
		_ = sqlDB.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}

	if err := backend.Configure(sqlDB); err != nil {
		_ = sqlDB.Close()
		return err
	}

	// Bootstrap 之前先赋值全局句柄：既有的 createTables/RunMigrations 以及全部
	// CRUD 都依赖 `DB`，这是当前架构的既定契约（sqliteBackend.Bootstrap 会断言一致性）。
	DB = sqlDB
	setActiveBackend(backend)

	if !opts.SkipBootstrap {
		if err := backend.Bootstrap(sqlDB); err != nil {
			return err
		}
	}

	// 横向扩展能力如实告知：单写后端在多副本部署下会成为一致性隐患，
	// 启动期就要让运维看见，而不是等到线上出现写冲突。
	if !backend.Dialect().SupportsConcurrentWriters() {
		slog.Debug("Database backend is single-writer; horizontal scaling requires a concurrent-writer backend",
			"backend", backend.Name())
	}

	slog.Info("Database initialized successfully",
		"backend", backend.Name(),
		"bootstrap", !opts.SkipBootstrap)
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
			preferred_model TEXT,
			preferred_tier TEXT,
			model_mode TEXT DEFAULT 'single_model',
			allow_fallback BOOLEAN DEFAULT 1,
			max_cost_usd REAL DEFAULT 0,
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