package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// SqliteRepository persists ManagedServer records in the mcp_servers table.
//
// It implements the mcp.Repository interface and is the default persistence
// backend for dynamic MCP servers. The zero value is not usable; use
// NewSqliteRepository.
type SqliteRepository struct {
	db *sql.DB
}

// NewSqliteRepository creates a repository backed by db.DB.
func NewSqliteRepository(database *sql.DB) *SqliteRepository {
	return &SqliteRepository{db: database}
}

// Save inserts or replaces a managed server row.
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
	// Persist the top-level enabled flag into the config as well so the
	// Loader can make consistent decisions when starting from a persisted row.
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

// Delete removes a server by ID.
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

// ListEnabled returns servers with enabled = 1.
func (r *SqliteRepository) ListEnabled(ctx context.Context) ([]ManagedServer, error) {
	return r.list(ctx, `SELECT id, source, name, transport, command, args, endpoint, environment, enabled, created_at, updated_at
		FROM mcp_servers WHERE enabled = 1`)
}

// ListAll returns every persisted server.
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
		// Keep the persisted top-level enabled flag in sync with the nested config
		// so downstream consumers (especially the Loader) see one source of truth.
		ms.Config.Enabled = ms.Enabled
		servers = append(servers, ms)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mcp servers: %w", err)
	}
	return servers, nil
}

// Ensure SqliteRepository implements Repository.
var _ Repository = (*SqliteRepository)(nil)

// EmptyRepository is a no-op Repository useful for tests and for deployments
// that do not need DB persistence for MCP servers.
type EmptyRepository struct{}

func (EmptyRepository) Save(ctx context.Context, ms ManagedServer) error { return nil }
func (EmptyRepository) Delete(ctx context.Context, id string) error      { return nil }
func (EmptyRepository) ListEnabled(ctx context.Context) ([]ManagedServer, error) {
	return nil, nil
}
func (EmptyRepository) ListAll(ctx context.Context) ([]ManagedServer, error) { return nil, nil }

// Ensure EmptyRepository implements Repository.
var _ Repository = EmptyRepository{}

// DefaultRepository returns the sqlite repository using the package-level db.DB.
// If db.DB is nil it returns an EmptyRepository.
func DefaultRepository() Repository {
	if db.DB != nil {
		return NewSqliteRepository(db.DB)
	}
	return EmptyRepository{}
}
