package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// SqliteRepository 将 ManagedServer 记录持久化到 mcp_servers 表中。
//
// 它实现了 mcp.Repository 接口，是动态 MCP server 的默认持久化后端。
// 零值不可用；请使用 NewSqliteRepository。
type SqliteRepository struct {
	db *sql.DB
}

// NewSqliteRepository 创建一个由 db.DB 支持的 repository。
func NewSqliteRepository(database *sql.DB) *SqliteRepository {
	return &SqliteRepository{db: database}
}

// Save 插入或替换一行 managed server 记录。
func (r *SqliteRepository) Save(ctx context.Context, ms ManagedServer) error {
	if r.db == nil {
		return fmt.Errorf("sqlite repository not initialized")
	}
	cp := CloneManagedServer(ms)
	argsJSON, err := json.Marshal(cp.Config.Args)
	if err != nil {
		return fmt.Errorf("marshal args: %w", err)
	}
	envJSON, err := json.Marshal(cp.Config.Environment)
	if err != nil {
		return fmt.Errorf("marshal environment: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if cp.CreatedAt == "" {
		cp.CreatedAt = now
	}
	if cp.UpdatedAt == "" {
		cp.UpdatedAt = now
	}
	// 同时把顶层的 enabled 标志持久化进 config，以便 Loader 在从持久化行
	// 启动时能做出一致的决策。
	cp.Config.Enabled = cp.Enabled

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO mcp_servers (
			id, source, name, transport, command, args, endpoint, environment, enabled, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source,
			name=excluded.name,
			transport=excluded.transport,
			command=excluded.command,
			args=excluded.args,
			endpoint=excluded.endpoint,
			environment=excluded.environment,
			enabled=excluded.enabled,
			updated_at=excluded.updated_at
	`, cp.ID, string(cp.Source), cp.Config.Name, cp.Config.Transport, cp.Config.Command,
		argsJSON, cp.Config.Endpoint, envJSON, cp.Enabled, cp.CreatedAt, cp.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert mcp server: %w", err)
	}
	return nil
}

// Delete 按 ID 删除一个 server。
func (r *SqliteRepository) Delete(ctx context.Context, id string) error {
	if r.db == nil {
		return fmt.Errorf("sqlite repository not initialized")
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete mcp server: %w", err)
	}
	return nil
}

// ListEnabled 返回 enabled = 1 的 server。
func (r *SqliteRepository) ListEnabled(ctx context.Context) ([]ManagedServer, error) {
	return r.list(ctx, `SELECT id, source, name, transport, command, args, endpoint, environment, enabled, created_at, updated_at
		FROM mcp_servers WHERE enabled = 1`)
}

// ListAll 返回所有已持久化的 server。
func (r *SqliteRepository) ListAll(ctx context.Context) ([]ManagedServer, error) {
	return r.list(ctx, `SELECT id, source, name, transport, command, args, endpoint, environment, enabled, created_at, updated_at
		FROM mcp_servers ORDER BY created_at`)
}

func (r *SqliteRepository) list(ctx context.Context, query string) ([]ManagedServer, error) {
	if r.db == nil {
		return nil, fmt.Errorf("sqlite repository not initialized")
	}
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query mcp servers: %w", err)
	}
	defer rows.Close()

	var servers []ManagedServer
	for rows.Next() {
		var ms ManagedServer
		var source string
		var argsJSON, envJSON []byte
		if err := rows.Scan(
			&ms.ID, &source, &ms.Config.Name, &ms.Config.Transport, &ms.Config.Command,
			&argsJSON, &ms.Config.Endpoint, &envJSON, &ms.Enabled,
			&ms.CreatedAt, &ms.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan mcp server: %w", err)
		}
		ms.Source = Source(source)
		if err := json.Unmarshal(argsJSON, &ms.Config.Args); err != nil {
			return nil, fmt.Errorf("unmarshal args: %w", err)
		}
		if err := json.Unmarshal(envJSON, &ms.Config.Environment); err != nil {
			return nil, fmt.Errorf("unmarshal environment: %w", err)
		}
		// 让持久化的顶层 enabled 标志与嵌套 config 保持同步，以便下游
		// 消费者（尤其是 Loader）只看到唯一来源的真相。
		ms.Config.Enabled = ms.Enabled
		servers = append(servers, ms)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mcp servers: %w", err)
	}
	return servers, nil
}

// 确保 SqliteRepository 实现了 Repository。
var _ Repository = (*SqliteRepository)(nil)

// EmptyRepository 是一个空操作的 Repository，适用于测试以及不需要为 MCP
// server 做 DB 持久化的部署场景。
type EmptyRepository struct{}

func (EmptyRepository) Save(ctx context.Context, ms ManagedServer) error { return nil }
func (EmptyRepository) Delete(ctx context.Context, id string) error      { return nil }
func (EmptyRepository) ListEnabled(ctx context.Context) ([]ManagedServer, error) {
	return nil, nil
}
func (EmptyRepository) ListAll(ctx context.Context) ([]ManagedServer, error) { return nil, nil }

// 确保 EmptyRepository 实现了 Repository。
var _ Repository = EmptyRepository{}

// DefaultRepository 返回使用 package 级 db.DB 的 sqlite repository。
// 若 db.DB 为 nil，则返回 EmptyRepository。
func DefaultRepository() Repository {
	if db.DB != nil {
		return NewSqliteRepository(db.DB)
	}
	return EmptyRepository{}
}
