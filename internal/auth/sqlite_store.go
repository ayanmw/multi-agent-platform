// Package auth provides user, role, and API key management for the platform.
package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SqliteAPIKeyStore is a SQLite-backed implementation of APIKeyStore.
// It stores only bcrypt key hashes, never raw keys, and uses prefix-based
// pre-filtering to avoid expensive password comparisons when possible.
type SqliteAPIKeyStore struct {
	db *sql.DB
}

// NewSqliteAPIKeyStore returns a SQLite-backed API key store.
func NewSqliteAPIKeyStore(db *sql.DB) *SqliteAPIKeyStore {
	return &SqliteAPIKeyStore{db: db}
}

// Create generates and stores a new API key for the given user.
// It returns the persisted APIKey record, the raw key (shown to the user
// exactly once), and any error encountered.
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

// List returns all API keys owned by the given user, including revoked keys.
// The key_hash column is intentionally omitted so the list response never
// carries even a hashed credential in memory/JSON.
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

// Revoke marks the API key with the given ID as revoked.
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

// Verify checks a raw API key against stored hashes.
// It first queries keys matching the raw key's prefix, uses MatchPrefix for a
// fast pre-filter, then performs a bcrypt verification on the candidates.
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

		// Update last used timestamp on successful verification (best-effort).
		now := time.Now().UTC().Format(time.RFC3339)
		if _, updateErr := s.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now, key.ID); updateErr != nil {
			// Non-fatal; don't fail authentication.
		}

		return key, nil
	}

	return nil, ErrInvalidKey
}

// AddUser creates a new user record with the given name and role.
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

// GetUser wraps GetUserByID to satisfy the APIKeyStore interface.
// It is the canonical way for the auth middleware to resolve a user's role.
func (s *SqliteAPIKeyStore) GetUser(userID string) (*User, error) {
	return s.GetUserByID(userID)
}

// GetUserByID loads a user record by its ID.
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

// CountUsers returns the total number of users in the database.
func (s *SqliteAPIKeyStore) CountUsers() (int, error) {
	row := s.db.QueryRow(`SELECT COUNT(*) FROM users`)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

// GetFirstUser returns the first user record by creation order.
// Used to establish a stable fallback user ID when REQUIRE_AUTH is disabled.
// Returns an error if no users exist.
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

// GetFirstAdmin returns the first admin-role user by creation order.
// Used to establish a stable fallback user ID for admin operations.
// Returns an error if no admin users exist.
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

// GetUserByName loads a user record by name. Returns an error if not found.
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

// parseTime parses an RFC3339 timestamp, returning zero time on error.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// parseTimePtr parses an optional RFC3339 timestamp.
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

// Compile-time check that SqliteAPIKeyStore implements APIKeyStore.
var _ APIKeyStore = (*SqliteAPIKeyStore)(nil)

// Ensure uuid import is used when helper functions above are referenced.
var _ = uuid.New
