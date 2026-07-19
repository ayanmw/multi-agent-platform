// Package auth provides user, role, and API key management for the platform.
package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SqliteAPIKeyStore 是 APIKeyStore 的 SQLite 实现。
// 它只存储 bcrypt key 哈希,绝不存储原始 key,并使用基于 prefix 的
// 预过滤以尽量避免昂贵的密码比较。
type SqliteAPIKeyStore struct {
	db *sql.DB
}

// NewSqliteAPIKeyStore 返回一个基于 SQLite 的 API key store。
func NewSqliteAPIKeyStore(db *sql.DB) *SqliteAPIKeyStore {
	return &SqliteAPIKeyStore{db: db}
}

// Create 为指定用户生成并存储一个新的 API key。
// 它返回持久化后的 APIKey 记录、原始 key(对用户只展示一次)
// 以及遇到的任何 error。
func (s *SqliteAPIKeyStore) Create(userID, name string) (*APIKey, string, error) {
	if userID == "" {
		return nil, "", errors.New("userID is required")
	}

	rawKey, prefix, err := GenerateAPIKey()
	if err != nil {
		return nil, "", fmt.Errorf("generate api key: %w", err)
	}

	hash, err := HashPassword(rawKey)
	if err != nil {
		return nil, "", fmt.Errorf("hash api key: %w", err)
	}

	keyID := NewAPIKeyID()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err = s.db.Exec(`
		INSERT INTO api_keys (id, user_id, name, prefix, key_hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, keyID, userID, name, prefix, string(hash), now)
	if err != nil {
		return nil, "", fmt.Errorf("insert api key: %w", err)
	}

	apiKey := &APIKey{
		ID:        keyID,
		UserID:    userID,
		Name:      name,
		KeyHash:   hash,
		Prefix:    prefix,
		CreatedAt: time.Now().UTC(),
	}
	return apiKey, rawKey, nil
}

// List 返回指定用户拥有的所有 API key,包括已吊销的 key。
// 故意省略 key_hash 列,使列表响应绝不会在内存 / JSON 中
// 携带哪怕已哈希的凭据。
func (s *SqliteAPIKeyStore) List(userID string) ([]APIKey, error) {
	rows, err := s.db.Query(`
		SELECT id, user_id, name, prefix, created_at, last_used_at, revoked_at
		FROM api_keys
		WHERE user_id = ?
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var key APIKey
		var createdAtStr string
		var lastUsedStr, revokedStr *string
		if err := rows.Scan(&key.ID, &key.UserID, &key.Name, &key.Prefix,
			&createdAtStr, &lastUsedStr, &revokedStr); err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		key.KeyHash = nil
		key.CreatedAt = parseTime(createdAtStr)
		key.LastUsedAt = parseTimePtr(lastUsedStr)
		key.RevokedAt = parseTimePtr(revokedStr)
		keys = append(keys, key)
	}

	if keys == nil {
		keys = []APIKey{}
	}
	return keys, rows.Err()
}

// Revoke 将指定 ID 的 API key 标记为已吊销。
func (s *SqliteAPIKeyStore) Revoke(keyID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`
		UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL
	`, now, keyID)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke api key rows affected: %w", err)
	}
	if affected == 0 {
		return errors.New("api key not found or already revoked")
	}
	return nil
}

// Verify 用已存储的哈希校验原始 API key。
// 它先查询 prefix 与原始 key 匹配的 key,用 MatchPrefix 做快速预过滤,
// 然后对候选 key 执行 bcrypt 校验。
func (s *SqliteAPIKeyStore) Verify(rawKey string) (*APIKey, error) {
	if rawKey == "" {
		return nil, ErrInvalidKey
	}

	prefix := rawKey
	if len(rawKey) > 12 {
		prefix = rawKey[:12]
	}

	rows, err := s.db.Query(`
		SELECT id, user_id, name, prefix, key_hash, created_at, last_used_at, revoked_at
		FROM api_keys
		WHERE prefix = ? AND revoked_at IS NULL
	`, prefix)
	if err != nil {
		return nil, fmt.Errorf("query api key by prefix: %w", err)
	}
	defer rows.Close()

	var candidates []APIKey
	for rows.Next() {
		var key APIKey
		var hashStr string
		var createdAtStr string
		var lastUsedStr, revokedStr *string
		if err := rows.Scan(&key.ID, &key.UserID, &key.Name, &key.Prefix, &hashStr,
			&createdAtStr, &lastUsedStr, &revokedStr); err != nil {
			return nil, fmt.Errorf("scan api key candidate: %w", err)
		}
		key.KeyHash = []byte(hashStr)
		key.CreatedAt = parseTime(createdAtStr)
		key.LastUsedAt = parseTimePtr(lastUsedStr)
		key.RevokedAt = parseTimePtr(revokedStr)
		candidates = append(candidates, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range candidates {
		key := &candidates[i]
		if !MatchPrefix(rawKey, key.Prefix) {
			continue
		}
		if err := VerifyPassword(rawKey, key.KeyHash); err != nil {
			continue
		}

		// 校验成功时更新 last used 时间戳(尽力而为)。
		now := time.Now().UTC().Format(time.RFC3339)
		if _, updateErr := s.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now, key.ID); updateErr != nil {
			// 非致命;不要因此让认证失败。
		}

		return key, nil
	}

	return nil, ErrInvalidKey
}

// AddUser 用指定的 name 和 role 创建一个新的用户记录。
func (s *SqliteAPIKeyStore) AddUser(name string, role Role) (*User, error) {
	if name == "" {
		return nil, errors.New("name is required")
	}
	if !role.IsValid() {
		return nil, fmt.Errorf("invalid role: %s", role)
	}

	userID := NewUserID()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.Exec(`
		INSERT INTO users (id, name, role, created_at)
		VALUES (?, ?, ?, ?)
	`, userID, name, string(role), now)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	return &User{
		ID:        userID,
		Name:      name,
		Role:      role,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// GetUser 包装 GetUserByID 以满足 APIKeyStore 接口。
// 它是 auth middleware 解析用户 role 的标准入口。
func (s *SqliteAPIKeyStore) GetUser(userID string) (*User, error) {
	return s.GetUserByID(userID)
}

// GetUserByID 按 ID 加载用户记录。
func (s *SqliteAPIKeyStore) GetUserByID(userID string) (*User, error) {
	row := s.db.QueryRow(`
		SELECT id, name, role, created_at FROM users WHERE id = ?
	`, userID)

	var user User
	var roleStr string
	var createdAtStr string
	if err := row.Scan(&user.ID, &user.Name, &roleStr, &createdAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("user not found")
		}
		return nil, fmt.Errorf("scan user: %w", err)
	}
	user.Role = Role(roleStr)
	user.CreatedAt = parseTime(createdAtStr)
	return &user, nil
}

// CountUsers 返回数据库中的用户总数。
func (s *SqliteAPIKeyStore) CountUsers() (int, error) {
	row := s.db.QueryRow(`SELECT COUNT(*) FROM users`)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

// GetFirstUser 按创建顺序返回第一个用户记录。
// 用于在 REQUIRE_AUTH 关闭时建立稳定的兜底用户 ID。
// 如果不存在任何用户则返回错误。
func (s *SqliteAPIKeyStore) GetFirstUser() (*User, error) {
	row := s.db.QueryRow(`
		SELECT id, name, role, created_at FROM users
		ORDER BY created_at ASC LIMIT 1
	`)
	var user User
	var roleStr string
	var createdAtStr string
	if err := row.Scan(&user.ID, &user.Name, &roleStr, &createdAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("no users found")
		}
		return nil, fmt.Errorf("scan first user: %w", err)
	}
	user.Role = Role(roleStr)
	user.CreatedAt = parseTime(createdAtStr)
	return &user, nil
}

// GetFirstAdmin 按创建顺序返回第一个 admin role 的用户。
// 用于为 admin 操作建立稳定的兜底用户 ID。
// 如果不存在任何 admin 用户则返回错误。
func (s *SqliteAPIKeyStore) GetFirstAdmin() (*User, error) {
	row := s.db.QueryRow(`
		SELECT id, name, role, created_at FROM users
		WHERE role = 'admin'
		ORDER BY created_at ASC LIMIT 1
	`)
	var user User
	var roleStr string
	var createdAtStr string
	if err := row.Scan(&user.ID, &user.Name, &roleStr, &createdAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("no admin users found")
		}
		return nil, fmt.Errorf("scan first admin: %w", err)
	}
	user.Role = Role(roleStr)
	user.CreatedAt = parseTime(createdAtStr)
	return &user, nil
}

// GetUserByName 按 name 加载用户记录。未找到则返回错误。
func (s *SqliteAPIKeyStore) GetUserByName(name string) (*User, error) {
	row := s.db.QueryRow(`
		SELECT id, name, role, created_at FROM users WHERE name = ?
	`, name)
	var user User
	var roleStr string
	var createdAtStr string
	if err := row.Scan(&user.ID, &user.Name, &roleStr, &createdAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("user not found")
		}
		return nil, fmt.Errorf("scan user by name: %w", err)
	}
	user.Role = Role(roleStr)
	user.CreatedAt = parseTime(createdAtStr)
	return &user, nil
}

// parseTime 解析 RFC3339 时间戳,出错时返回零值时间。
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// parseTimePtr 解析可选的 RFC3339 时间戳。
func parseTimePtr(s *string) *time.Time {
	if s == nil || *s == "" {
		return nil
	}
	t := parseTime(*s)
	if t.IsZero() {
		return nil
	}
	return &t
}

// 编译期检查 SqliteAPIKeyStore 实现了 APIKeyStore。
var _ APIKeyStore = (*SqliteAPIKeyStore)(nil)

// 确保 uuid import 在上述辅助函数被引用时仍被使用。
var _ = uuid.New
