// migrate.go — 自动数据库 schema migration
//
// SQLite 的 CREATE TABLE IF NOT EXISTS 是幂等的，但无法为已存在的表新增列。
// 我们实现了一套轻量级 migration 系统：
//  1. 在 `schema_migrations` 表中记录已应用的 migration
//  2. 启动时按顺序执行待应用的 migration
//  3. migration 以 {version, description, sql} 列表形式定义
//
// 这模仿了 GORM 的 AutoMigrate 行为——为既有表新增列只需追加一条 migration
// 条目，无需手写 DDL。
package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
)

// Migration 表示一次单一的 schema 变更。
type Migration struct {
	Version     int    // 单调递增的版本号
	Description string // 人类可读的描述
	SQL         string // 待执行的 DDL 语句
	// Pre 在执行 SQL 语句之前调用，可访问底层 *sql.DB 做数据备份/打印等副作用。
	// 为 nil 时跳过。典型用途：DROP 重建表前把旧记录打印到日志，方便用户手动恢复。
	Pre func(*sql.DB) error
}

// 所有 migration，按时间顺序排列。
// 新的 migration 必须追加到列表末尾，禁止重排或删除已有条目。
var migrations =deduplicateMigrations([]Migration{
	// v1：初始 schema —— 即 database.go createTables() 中定义的所有表。
	// 本 migration 是 no-op，因为 createTables() 已负责初始建表。
	// 仅用于给 schema_migrations 表播种，以便后续 migration 能正常执行。
	{
		Version:     1,
		Description: "Initial schema (createTables handles table creation)",
		SQL:         `SELECT 1`, // no-op, createTables() already ran
	},

	// v2：为 agents 表新增 is_default 列
	{
		Version:     2,
		Description: "Add is_default BOOLEAN column to agents table",
		SQL:         `ALTER TABLE agents ADD COLUMN is_default BOOLEAN DEFAULT 0`,
	},

	// v3：为 tasks 表新增 session_id、parent_task_id、is_root 列
	{
		Version:     3,
		Description: "Add session_id, parent_task_id, is_root columns to tasks table",
		SQL: `ALTER TABLE tasks ADD COLUMN session_id TEXT;
ALTER TABLE tasks ADD COLUMN parent_task_id TEXT;
ALTER TABLE tasks ADD COLUMN is_root BOOLEAN DEFAULT 0`,
	},

	// v4：为已有 session 中存在 root task 的行回填 root_task_id
	{
		Version:     4,
		Description: "Backfill root_task_id for existing sessions from their root tasks",
		SQL:         `UPDATE sessions SET root_task_id = (SELECT id FROM tasks WHERE tasks.session_id = sessions.id AND tasks.is_root = 1 LIMIT 1) WHERE (root_task_id = '' OR root_task_id IS NULL)`,
	},

	// v5：创建 projects 表并播种 default project。
	// projects 作为顶层组织单元，用于对 session 进行分组。
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

	// v6：创建 session_messages 表，用于多轮对话追踪。
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

	// v7：扩展 sessions、tasks、memories 表，新增用于项目关联、
	// 轮次追踪以及 memory 作用域的列。
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

	// v8：占位 migration（no-op）—— v8 保留给未来的 schema 变更
	{
		Version:     8,
		Description: "Placeholder migration (no-op) — v8 reserved for future schema change",
		SQL:         `SELECT 1`,
	},

	// v9：为 memories 表新增 session_id 列，用于 session 作用域的 memory
	{
		Version:     9,
		Description: "Add session_id column to memories table for session-scoped memories",
		SQL:         `ALTER TABLE memories ADD COLUMN session_id TEXT DEFAULT ''`,
	},

	// v10：创建 cost_records 表，用于 LLM 成本追踪。
	// 记录每次 LLM 调用的 token 消耗与 USD 成本，按 task、session、project
	// 建立索引以支持多维成本报表。
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

	// v11：为 cost_records 新增整数型 cost_cents 列，用于精确记账。
	//
	// 旧行会从 cost_usd*100 回填，以便 CostRepository 读取所有行时
	// cost_cents 都不为空。SQLite 没有 ALTER ADD COLUMN IF NOT EXISTS，
	// 因此当列已存在时失败会被 RunMigrations 静默忽略（只有非
	// "duplicate column name" 的错误才会被打印，避免每次启动刷屏）。
	{
		Version:     11,
		Description: "Add cost_cents column to cost_records for integer precision",
		SQL: `ALTER TABLE cost_records ADD COLUMN cost_cents INTEGER DEFAULT 0;
		UPDATE cost_records SET cost_cents = CAST(ROUND(cost_usd * 100) AS INTEGER) WHERE cost_cents = 0 AND cost_usd <> 0;`,
	},

	// v12：创建 users 和 api_keys 表，用于 API key 鉴权。
	// users 存储用户身份与角色；api_keys 存储经 bcrypt 哈希的 key，
	// 并保留 prefix 以便校验时快速查找。
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

	// v13：创建 mock_scripts 表，用于确定性的 LLM mock 响应。
	// mock scripts 存储 case 专属的响应序列，以及可选的输入关键词匹配规则，
	// 便于在不调用真实 provider 的情况下测试 LLM。
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

	// v14：为 tasks 表新增 duration_ms 列，用于任务级耗时追踪。
	{
		Version:     14,
		Description: "Add duration_ms column to tasks table",
		SQL:         `ALTER TABLE tasks ADD COLUMN duration_ms INTEGER DEFAULT 0`,
	},

	// v15：为 sessions 表新增 workspace_dir 和 workspace_auto 列。
	{
		Version:     15,
		Description: "Add workspace_dir and workspace_auto columns to sessions table",
		SQL:         `ALTER TABLE sessions ADD COLUMN workspace_dir TEXT DEFAULT ''; ALTER TABLE sessions ADD COLUMN workspace_auto BOOLEAN DEFAULT 1`,
	},

	// v16：为 SqliteVectorStore 创建 memory_embeddings 表（Phase 6-F）。
	//
	// 将向量存储与 memories 表本身解耦：embedding 行存放在独立的、按 key 访问
	// 的表中，这样 (a) 向量 I/O 可以批量执行而无需扫描完整的 memories 行；
	// (b) 更换 embedding model 时只需让子集失效（按 model 列过滤）；
	// (c) ON DELETE CASCADE 在 memory 被删除时保持表一致性。
	//
	// embedding BLOB 采用小端 float32 序列化（长度 = dims * 4 字节）。
	// 详见 pkg/db/memory_embedding.go 中的 encode/decode 辅助函数与设计理由。
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

	// v17：为 Case 管理功能创建 cases 和 case_evaluations 表。
	//
	// cases 存储内置或用户自定义的测试 case（contract、prompts、tags、
	// category）；case_evaluations 存储将某个 case 在某个 task 上执行的结果，
	// 支持 pass/fail 追踪与可选的打分。两张表均以幂等方式创建，并在后续
	// 任务中 repository 层使用的查询路径上建立索引。
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

	// v18：为 AgentBus 持久化功能创建 agent_messages 表。
	//
	// 每条经由 AgentBus 路由的 inter-agent 消息都会被持久化到这里，前端可通过
	// GET /api/tasks/:id/agent-messages 拉取某个 task 的完整消息历史。task_id
	// 列建立了索引，因为主要查询路径是"给定 task 的全部消息，按时间升序"。
	// metadata 以 JSON 文本存储——保持 opaque 可以在新增 metadata key 时
	// 避免 schema 频繁变动。
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

	// v19：创建 approvals 表，用于 leader 代理审批决策。
	//
	// 每行记录一个需要审批的高风险 tool call，包括该请求是否被委托给 leader
	// 以及 leader 的最终决策。用于支持可审计性、回放以及前端 dashboard。
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

	// v20: 补全 agent_messages 表的子任务路由字段。
	//
	// Phase 7-I 起 AgentBus 消息支持按 (agentID, subTaskID) 精确路由，持久化层
	// 需要记录 sub_task_id（目标子任务）和 from_sub_task_id（发送方子任务）。
	// 使用 ALTER TABLE ADD COLUMN 保证旧数据库平滑升级；新库在 v18 创建表样例
	// 后由本迁移补齐列（RunMigrations 对重复列错误静默跳过）。
	{
		Version:     20,
		Description: "Add sub_task_id and from_sub_task_id to agent_messages",
		SQL: `ALTER TABLE agent_messages ADD COLUMN sub_task_id TEXT DEFAULT '';
		ALTER TABLE agent_messages ADD COLUMN from_sub_task_id TEXT DEFAULT '';`,
	},

	// v21：audit_records 表，用于合规与取证（Phase 7-C）。
	{
		Version:     21,
		Description: "Create audit_records table",
		SQL: `CREATE TABLE IF NOT EXISTS audit_records (
			id TEXT PRIMARY KEY,
			timestamp DATETIME NOT NULL,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			target TEXT NOT NULL,
			before_json TEXT DEFAULT '{}',
			after_json TEXT DEFAULT '{}',
			reason TEXT DEFAULT '',
			ip TEXT DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_audit_records_actor ON audit_records(actor);
		CREATE INDEX IF NOT EXISTS idx_audit_records_target ON audit_records(target);
		CREATE INDEX IF NOT EXISTS idx_audit_records_timestamp ON audit_records(timestamp DESC);`,
		},

		// v22：
		// agent_default 的 tools 白名单从写死 3 个修复为“空 = 全部允许”，
		// 避免新工具（core/*、skill/*、mcp__*、dispatch_sub_agent 等）默认被隐藏。
		{
			Version:     22,
			Description: "Reset agent_default tools whitelist to allow all tools",
			SQL:         `UPDATE agents SET tools='[]' WHERE id='agent_default';`,
		},

		// v23：由 pkg/db/todo.go init() 注册，创建 todos 表。此处保留空位避免版本冲突。
		{
			Version:     23,
			Description: "Placeholder for todos table migration (registered in pkg/db/todo.go)",
			SQL:         `SELECT 1`,
		},

		// v24：由 pkg/db/skill.go init() 注册，创建 skills 表。此处保留空位避免版本冲突。
		{
			Version:     24,
			Description: "Placeholder for skills table migration (registered in pkg/db/skill.go)",
			SQL:         `SELECT 1`,
		},

		// v25：展开 todos 表 parent_todo_id 外键能力，确保有索引。
		// v23 创建表时已包含 parent_todo_id 列，本迁移用于在旧数据库上补建索引。
		{
			Version:     25,
			Description: "Ensure todos parent_todo_id index exists for nested subtasks",
			SQL: `CREATE INDEX IF NOT EXISTS idx_todos_parent_todo_id ON todos(parent_todo_id);
CREATE INDEX IF NOT EXISTS idx_todos_priority_sort_order_created_at ON todos(priority DESC, sort_order ASC, created_at ASC);`,
		},
	})

// deduplicateMigrations 按 version 去重，保留第一次出现的条目。
// 用于处理多个 init() 从不同包向 migrations 切片追加同版本 placeholder 的情况。
// 注意：真实 migration（SQL 非 SELECT 1）优先于 placeholder；
// 因此先按 version 分组，若存在真实 migration 则取用真实项，否则保留第一项。
func deduplicateMigrations(list []Migration) []Migration {
	seen := make(map[int]bool)
	byVersion := make(map[int]Migration)
	for _, m := range list {
		existing, ok := byVersion[m.Version]
		if !ok || (m.SQL != "SELECT 1" && existing.SQL == "SELECT 1") {
			byVersion[m.Version] = m
		}
	}
	var cleaned []Migration
	for _, m := range list {
		if seen[m.Version] {
			continue
		}
		if best, ok := byVersion[m.Version]; ok {
			cleaned = append(cleaned, best)
			seen[m.Version] = true
		}
	}
	return cleaned
}

// createMigrationsTable 确保 schema_migrations 追踪表存在。
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

// getAppliedMigrations 返回已应用的 migration 版本集合。
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

// RunMigrations 执行所有待应用的 migration。
// 在 Init() 中于 createTables() 之后调用，此时表已存在。
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

	// 对 migrations 切片按 version 去重，防止多个 init() 注册同版本号。
	uniqueMigrations := deduplicateMigrations(migrations)

	for _, m := range uniqueMigrations {
		if applied[m.Version] {
			continue // 已应用
		}

		log.Printf("[Migration] v%d: %s", m.Version, m.Description)

		// 执行可选的 Pre 钩子（DROP 重建前打印旧数据等副作用）。
		// Pre 失败只记录日志，不阻断迁移——数据备份是尽力而为，
		// 不应让 schema 演进因打印日志失败而卡住。
		if m.Pre != nil {
			if err := m.Pre(DB); err != nil {
				log.Printf("[Migration] v%d: pre-hook failed (continuing): %v", m.Version, err)
			}
		}

		// SQLite 在单次 Exec 调用中不支持多条语句，
		// 因此按分号拆分后逐条执行。
		// 注意：这种简单拆分对我们的 DDL 场景足够。如果未来需要在字符串
		// 字面量内包含分号的多语句，则需要改用真正的 SQL parser。
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

		// 将该 migration 标记为已应用
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

// splitSQL 按分号拆分 SQL 字符串并去除空白。
// 之所以需要它，是因为 SQLite 的 Exec() 一次只能处理一条语句。
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
	// 别忘了最后一条语句（没有结尾分号）
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
