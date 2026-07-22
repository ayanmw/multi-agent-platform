// cron.go — Cron 子系统的 SQLite 持久化层。
//
// 本文件做两件事：
//   1. 在 init() 中注册 v26 migration，创建 crons 与 cron_executions 两表
//      及其索引。沿用 skill.go 的 init() 注册模式，使 database.go 无需改动。
//   2. 实现 cron.DBStore 接口（internal/cron 包定义）所需的全部 CRUD 函数。
//
// 时间字段统一存储为 INTEGER unix 秒（与 skill.go 一致），避免 DATETIME
// 在不同驱动下的解析差异；应用层负责 *time.Time ↔ int64 的转换。
// ActionPayload 存储为 JSON 文本。
package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/cron"
)

// init 注册 crons / cron_executions 表的 schema 迁移（v26）。
// 使用 init() 让 database.go 在 Init 时自动跑迁移，无需显式修改。
func init() {
	migrations = append(migrations, Migration{
		Version:     26,
		Description: "Create crons and cron_executions tables for cron subsystem",
		SQL: `CREATE TABLE IF NOT EXISTS crons (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT DEFAULT '',
			schedule_type TEXT NOT NULL,
			cron_expr TEXT NOT NULL DEFAULT '',
			display_type TEXT DEFAULT 'cron',
			timezone TEXT DEFAULT 'UTC',
			once_at TEXT DEFAULT '',
			action_type TEXT NOT NULL,
			action_payload TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'enabled',
			allow_concurrent INTEGER DEFAULT 0,
			source TEXT DEFAULT 'user',
			owner TEXT DEFAULT '',
			last_triggered_at INTEGER,
			next_trigger_at INTEGER,
			last_execution_id TEXT DEFAULT '',
			trigger_count INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_crons_status ON crons(status);
		CREATE INDEX IF NOT EXISTS idx_crons_next_trigger ON crons(next_trigger_at);
		CREATE TABLE IF NOT EXISTS cron_executions (
			id TEXT PRIMARY KEY,
			cron_id TEXT NOT NULL,
			triggered_at INTEGER NOT NULL,
			status TEXT NOT NULL,
			reason TEXT DEFAULT '',
			rendered_input TEXT DEFAULT '',
			result_summary TEXT DEFAULT '',
			task_id TEXT DEFAULT '',
			session_id TEXT DEFAULT '',
			duration_ms INTEGER DEFAULT 0,
			error TEXT DEFAULT '',
			created_at INTEGER NOT NULL,
			FOREIGN KEY (cron_id) REFERENCES crons(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_cron_executions_cron_id ON cron_executions(cron_id);
		CREATE INDEX IF NOT EXISTS idx_cron_executions_triggered_at ON cron_executions(triggered_at DESC);
		CREATE INDEX IF NOT EXISTS idx_cron_executions_status ON cron_executions(status);`,
	})
}

// ErrCronNotFound 表示指定的 cron 记录不存在。
var ErrCronNotFound = errors.New("cron not found")

// ErrCronExecutionNotFound 表示指定的 execution 记录不存在。
var ErrCronExecutionNotFound = errors.New("cron execution not found")

// rowScanner 抽象 *sql.Row 与 *sql.Rows 共有的 Scan 方法，让扫描逻辑复用。
type rowScanner interface {
	Scan(dest ...any) error
}

// scanCron 从一行扫描出 cron.Cron。时间列以 sql.NullInt64 读取后转 *time.Time。
func scanCron(s rowScanner, c *cron.Cron) error {
	var lastTrig, nextTrig sql.NullInt64
	var lastExecID sql.NullString
	var allowConcurrent int
	var createdAt, updatedAt int64
	err := s.Scan(
		&c.ID, &c.Name, &c.Description,
		&c.ScheduleType, &c.CronExpr, &c.DisplayType, &c.Timezone, &c.OnceAt,
		&c.ActionType, &c.ActionPayloadRaw,
		&c.Status, &allowConcurrent,
		&c.Source, &c.Owner,
		&lastTrig, &nextTrig, &lastExecID,
		&c.TriggerCount,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return err
	}
	c.AllowConcurrent = allowConcurrent != 0
	c.LastExecutionID = lastExecID.String
	c.LastTriggeredAt = nullIntToTime(lastTrig)
	c.NextTriggerAt = nullIntToTime(nextTrig)
	c.CreatedAt = time.Unix(createdAt, 0)
	c.UpdatedAt = time.Unix(updatedAt, 0)
	return nil
}

// nullIntToTime 把 sql.NullInt64（unix 秒）转成 *time.Time；NULL → nil。
func nullIntToTime(n sql.NullInt64) *time.Time {
	if !n.Valid || n.Int64 == 0 {
		return nil
	}
	t := time.Unix(n.Int64, 0)
	return &t
}

// timeToNullInt 把 *time.Time 转成 sql.NullInt64（unix 秒）；nil → NULL。
func timeToNullInt(t *time.Time) sql.NullInt64 {
	if t == nil || t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Valid: true, Int64: t.Unix()}
}

// marshalPayload 把 action_payload map 序列化为 JSON 文本。
// ActionPayloadRaw 是 cron.Cron 中供持久化使用的原始 JSON 字段；
// ActionPayload（map）由应用层在 Get 后解码填充。
func marshalPayload(p map[string]any) string {
	if p == nil {
		return "{}"
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// decodePayload 把 JSON 文本解码回 map[string]any。
func decodePayload(raw string) map[string]any {
	if raw == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{}
	}
	if m == nil {
		return map[string]any{}
	}
	return m
}

// InsertCron 插入一条新的 cron 记录。created_at/updated_at 由本函数置为当前时间。
func InsertCron(c cron.Cron) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now().Unix()
	_, err := DB.Exec(`INSERT INTO crons (
		id, name, description, schedule_type, cron_expr, display_type, timezone, once_at,
		action_type, action_payload, status, allow_concurrent, source, owner,
		last_triggered_at, next_trigger_at, last_execution_id, trigger_count,
		created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Name, c.Description, c.ScheduleType, c.CronExpr, c.DisplayType, c.Timezone, c.OnceAt,
		c.ActionType, marshalPayload(c.ActionPayload), c.Status, boolToInt(c.AllowConcurrent), c.Source, c.Owner,
		timeToNullInt(c.LastTriggeredAt), timeToNullInt(c.NextTriggerAt), c.LastExecutionID, c.TriggerCount,
		now, now,
	)
	return err
}

// boolToInt 把布尔转成 0/1，匹配 SQLite BOOLEAN 列。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UpdateCron 覆盖更新一条 cron 记录（除 trigger_count / last_* 外的字段）。
// 调用方若需更新调度状态字段，请用 UpdateCronScheduleMeta。
func UpdateCron(c cron.Cron) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now().Unix()
	res, err := DB.Exec(`UPDATE crons SET
		name=?, description=?, schedule_type=?, cron_expr=?, display_type=?, timezone=?, once_at=?,
		action_type=?, action_payload=?, status=?, allow_concurrent=?, source=?, owner=?,
		updated_at=?
	WHERE id=?`,
		c.Name, c.Description, c.ScheduleType, c.CronExpr, c.DisplayType, c.Timezone, c.OnceAt,
		c.ActionType, marshalPayload(c.ActionPayload), c.Status, boolToInt(c.AllowConcurrent), c.Source, c.Owner,
		now, c.ID,
	)
	if err != nil {
		return err
	}
	return ensureAffected(res, "cron", c.ID)
}

// UpdateCronScheduleMeta 仅更新调度相关的元数据：
// status、last_triggered_at、next_trigger_at、last_execution_id、trigger_count。
// 与触发执行链路解耦，避免每次触发都全量覆盖 action_payload。
func UpdateCronScheduleMeta(c cron.Cron) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now().Unix()
	res, err := DB.Exec(`UPDATE crons SET
		status=?, last_triggered_at=?, next_trigger_at=?, last_execution_id=?, trigger_count=?, updated_at=?
	WHERE id=?`,
		c.Status, timeToNullInt(c.LastTriggeredAt), timeToNullInt(c.NextTriggerAt),
		c.LastExecutionID, c.TriggerCount, now, c.ID,
	)
	if err != nil {
		return err
	}
	return ensureAffected(res, "cron", c.ID)
}

// ensureAffected 校验 UPDATE/DELETE 至少影响了 1 行，否则返回 not found 错误。
// kind 为 "cron" 时返回 ErrCronNotFound，否则返回 ErrCronExecutionNotFound。
// id 仅用于将来可能的日志上下文，当前未使用。
func ensureAffected(res sql.Result, kind, _ string) error {
	n, _ := res.RowsAffected()
	if n == 0 {
		if kind == "cron" {
			return ErrCronNotFound
		}
		return ErrCronExecutionNotFound
	}
	return nil
}

// DeleteCron 删除一条 cron 记录。关联的 executions 由 FK ON DELETE CASCADE 清理。
func DeleteCron(id string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	// modernc.org/sqlite 默认未开启 foreign_keys pragma，ON DELETE CASCADE
	// 不会自动生效，因此这里手动先删 executions 再删 cron，保证级联语义。
	if _, err := DB.Exec(`DELETE FROM cron_executions WHERE cron_id=?`, id); err != nil {
		return err
	}
	res, err := DB.Exec(`DELETE FROM crons WHERE id=?`, id)
	if err != nil {
		return err
	}
	return ensureAffected(res, "cron", id)
}

// GetCron 按 id 读取单条 cron；不存在时返回 ErrCronNotFound。
func GetCron(id string) (cron.Cron, error) {
	if DB == nil {
		return cron.Cron{}, fmt.Errorf("db not initialized")
	}
	row := DB.QueryRow(`SELECT
		id, name, description, schedule_type, cron_expr, display_type, timezone, once_at,
		action_type, action_payload, status, allow_concurrent, source, owner,
		last_triggered_at, next_trigger_at, last_execution_id, trigger_count,
		created_at, updated_at
	FROM crons WHERE id=?`, id)
	var c cron.Cron
	if err := scanCron(row, &c); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cron.Cron{}, ErrCronNotFound
		}
		return cron.Cron{}, err
	}
	c.ActionPayload = decodePayload(c.ActionPayloadRaw)
	return c, nil
}

// ListCrons 按过滤条件列出 cron 记录，按 updated_at DESC 排序。
// filter 各字段为零值时表示不过滤。
func ListCrons(filter cron.ListFilter) ([]cron.Cron, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var (
		conds []string
		args  []any
	)
	if filter.Status != "" {
		conds = append(conds, "status=?")
		args = append(args, filter.Status)
	}
	if filter.ActionType != "" {
		conds = append(conds, "action_type=?")
		args = append(args, filter.ActionType)
	}
	if filter.Source != "" {
		conds = append(conds, "source=?")
		args = append(args, filter.Source)
	}
	if filter.Query != "" {
		conds = append(conds, "(name LIKE ? OR description LIKE ? OR id LIKE ?)")
		like := "%" + filter.Query + "%"
		args = append(args, like, like, like)
	}
	q := `SELECT
		id, name, description, schedule_type, cron_expr, display_type, timezone, once_at,
		action_type, action_payload, status, allow_concurrent, source, owner,
		last_triggered_at, next_trigger_at, last_execution_id, trigger_count,
		created_at, updated_at
	FROM crons`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY updated_at DESC"
	if filter.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	rows, err := DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cron.Cron
	for rows.Next() {
		var c cron.Cron
		if err := scanCron(rows, &c); err != nil {
			return nil, err
		}
		c.ActionPayload = decodePayload(c.ActionPayloadRaw)
		out = append(out, c)
	}
	return out, rows.Err()
}

// scanExecution 从一行扫描出 cron.Execution。
func scanExecution(s rowScanner, e *cron.Execution) error {
	var triggeredAt, createdAt int64
	err := s.Scan(
		&e.ID, &e.CronID, &triggeredAt, &e.Status, &e.Reason,
		&e.RenderedInput, &e.ResultSummary, &e.TaskID, &e.SessionID,
		&e.DurationMS, &e.Error, &createdAt,
	)
	if err != nil {
		return err
	}
	e.TriggeredAt = time.Unix(triggeredAt, 0)
	e.CreatedAt = time.Unix(createdAt, 0)
	return nil
}

// InsertExecution 插入一条执行记录。created_at 置为当前时间。
func InsertExecution(e cron.Execution) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	now := time.Now().Unix()
	_, err := DB.Exec(`INSERT INTO cron_executions (
		id, cron_id, triggered_at, status, reason,
		rendered_input, result_summary, task_id, session_id,
		duration_ms, error, created_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.CronID, e.TriggeredAt.Unix(), e.Status, e.Reason,
		e.RenderedInput, e.ResultSummary, e.TaskID, e.SessionID,
		e.DurationMS, e.Error, now,
	)
	return err
}

// UpdateExecution 覆盖更新一条执行记录的可变字段（status/reason/summary/task_id/session_id/duration/error）。
func UpdateExecution(e cron.Execution) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	res, err := DB.Exec(`UPDATE cron_executions SET
		status=?, reason=?, result_summary=?, task_id=?, session_id=?, duration_ms=?, error=?
	WHERE id=?`,
		e.Status, e.Reason, e.ResultSummary, e.TaskID, e.SessionID, e.DurationMS, e.Error, e.ID,
	)
	if err != nil {
		return err
	}
	return ensureAffected(res, "execution", e.ID)
}

// GetExecution 按 id 读取单条 execution；不存在返回 ErrCronExecutionNotFound。
func GetExecution(id string) (cron.Execution, error) {
	if DB == nil {
		return cron.Execution{}, fmt.Errorf("db not initialized")
	}
	row := DB.QueryRow(`SELECT
		id, cron_id, triggered_at, status, reason,
		rendered_input, result_summary, task_id, session_id,
		duration_ms, error, created_at
	FROM cron_executions WHERE id=?`, id)
	var e cron.Execution
	if err := scanExecution(row, &e); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return cron.Execution{}, ErrCronExecutionNotFound
		}
		return cron.Execution{}, err
	}
	return e, nil
}

// ListExecutions 按过滤条件列出执行记录，按 triggered_at DESC 排序。
func ListExecutions(filter cron.ExecListFilter) ([]cron.Execution, error) {
	if DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	var (
		conds []string
		args  []any
	)
	if filter.CronID != "" {
		conds = append(conds, "cron_id=?")
		args = append(args, filter.CronID)
	}
	if filter.Status != "" {
		conds = append(conds, "status=?")
		args = append(args, filter.Status)
	}
	if !filter.Before.IsZero() {
		conds = append(conds, "triggered_at<?")
		args = append(args, filter.Before.Unix())
	}
	if !filter.After.IsZero() {
		conds = append(conds, "triggered_at>=?")
		args = append(args, filter.After.Unix())
	}
	q := `SELECT
		id, cron_id, triggered_at, status, reason,
		rendered_input, result_summary, task_id, session_id,
		duration_ms, error, created_at
	FROM cron_executions`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY triggered_at DESC"
	if filter.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, filter.Limit)
	} else {
		q += " LIMIT 200"
	}
	if filter.Offset > 0 {
		q += " OFFSET ?"
		args = append(args, filter.Offset)
	}
	rows, err := DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cron.Execution
	for rows.Next() {
		var e cron.Execution
		if err := scanExecution(rows, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CleanExecutions 按过滤条件删除执行记录，返回删除行数。
// filter.CronID/Status/Before 任一非零时参与过滤；全为零值时不删任何记录（返回 0），
// 避免误清空。
func CleanExecutions(filter cron.CleanFilter) (int, error) {
	if DB == nil {
		return 0, fmt.Errorf("db not initialized")
	}
	var (
		conds []string
		args  []any
	)
	if filter.CronID != "" {
		conds = append(conds, "cron_id=?")
		args = append(args, filter.CronID)
	}
	if filter.Status != "" {
		conds = append(conds, "status=?")
		args = append(args, filter.Status)
	}
	if !filter.Before.IsZero() {
		conds = append(conds, "triggered_at<?")
		args = append(args, filter.Before.Unix())
	}
	if len(conds) == 0 {
		return 0, nil
	}
	q := "DELETE FROM cron_executions WHERE " + strings.Join(conds, " AND ")
	res, err := DB.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
