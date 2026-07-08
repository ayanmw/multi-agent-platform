// Package auth provides user, role, and API key management for the platform.
//
// # Auth Model
//
// The platform uses API key authentication (no passwords in Phase 6). Each user
// can have multiple API keys; keys are stored as bcrypt hashes. Three RBAC roles
// are supported: admin, user, viewer.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// --- Model Types ----------------------------------------------------------

// Role represents a user's permission level.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleUser   Role = "user"
	RoleViewer Role = "viewer"
)

// IsValid checks whether the role is one of the defined constants.
func (r Role) IsValid() bool {
	switch r {
	case RoleAdmin, RoleUser, RoleViewer:
		return true
	}
	return false
}

// User represents a registered platform user.
type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// APIKey represents a key used for programmatic authentication.
// The raw key is NEVER stored — only the bcrypt hash.
// KeyHash is tagged with json:"-" so it is never accidentally marshalled.
type APIKey struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	KeyHash    []byte     `json:"-"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// IsRevoked reports whether this key has been revoked.
func (k *APIKey) IsRevoked() bool {
	return k.RevokedAt != nil
}

// --- DB Record Types (for migrate.go and persistence) -----------------------

// UserRecordDB is the persisted form of a User (matches users table schema).
type UserRecordDB struct {
	ID        string
	Name      string
	Role      Role
	CreatedAt string // ISO 8601 for SQLite
}

// APIKeyRecordDB is the persisted form of an APIKey (matches api_keys table).
type APIKeyRecordDB struct {
	ID         string
	UserID     string
	Name       string
	Prefix     string
	KeyHash    string
	CreatedAt  string
	LastUsedAt *string
	RevokedAt  *string
}

// --- Key Operations --------------------------------------------------------

// DefaultBcryptCost is the bcrypt cost factor used for key hashing.
const DefaultBcryptCost = 12

// GenerateAPIKey creates a new cryptographically random API key.
// Format: "sk_" + base64url(32 random bytes) = 44-char string.
// Returns the raw key (shown to user exactly once) and the display prefix.
func GenerateAPIKey() (rawKey, prefix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	rawKey = "sk_" + base64.RawURLEncoding.EncodeToString(b)
	prefix = rawKey[:12]
	return rawKey, prefix, nil
}

// HashPassword hashes a raw key using bcrypt. Only the hash should be stored.
func HashPassword(rawKey string) ([]byte, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(rawKey), DefaultBcryptCost)
	if err != nil {
		return nil, fmt.Errorf("bcrypt hash: %w", err)
	}
	return hash, nil
}

// VerifyPassword compares a raw key against a stored bcrypt hash.
func VerifyPassword(rawKey string, hash []byte) error {
	if len(hash) == 0 {
		return errors.New("no key hash stored")
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(rawKey)); err != nil {
		return ErrInvalidKey
	}
	return nil
}

// MatchPrefix checks whether rawKey starts with prefix using constant-time compare.
// Used for fast pre-filtering before the expensive bcrypt check.
func MatchPrefix(rawKey, prefix string) bool {
	if len(rawKey) < len(prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(rawKey[:len(prefix)]), []byte(prefix)) == 1
}

// StripPrefix removes the "sk_" prefix if present.
func StripPrefix(key string) string {
	return strings.TrimPrefix(key, "sk_")
}

// --- Store Abstraction -----------------------------------------------------

// APIKeyStore abstracts the persistence operations for API keys.
// Implementations must store only bcrypt hashes, never raw keys.
type APIKeyStore interface {
	Create(userID, name string) (*APIKey, string, error)
	List(userID string) ([]APIKey, error)
	Revoke(keyID string) error
	Verify(rawKey string) (*APIKey, error)
}

// --- HTTP API --------------------------------------------------------------

// AuthAPI is the HTTP handler group for auth endpoints.
// It exposes user-facing API key lifecycle operations under /api/auth/api-keys.
type AuthAPI struct {
	store      APIKeyStore
	seedUserID string
}

// NewAuthAPI returns an AuthAPI ready for route registration.
func NewAuthAPI(store APIKeyStore) *AuthAPI {
	return &AuthAPI{store: store}
}

// GetStore returns the underlying APIKeyStore for use by middleware.
func (a *AuthAPI) GetStore() APIKeyStore {
	return a.store
}

// SetSeedUserID sets the fallback user ID used when authentication is disabled.
func (a *AuthAPI) SetSeedUserID(userID string) {
	a.seedUserID = userID
}

// SeedUserID returns the configured fallback user ID.
func (a *AuthAPI) SeedUserID() string {
	return a.seedUserID
}

// RegisterRoutes mounts auth endpoints on mux.
func (a *AuthAPI) RegisterRoutes(mux any) {
	mux_, ok := mux.(*http.ServeMux)
	if !ok {
		return
	}
	mux_.HandleFunc("/api/auth/api-keys", a.handleAPIKeys)
	mux_.HandleFunc("/api/auth/api-keys/", a.handleAPIKeyByID)
}

// NewAPIKeyID generates a new unique ID for an API key record.
func NewAPIKeyID() string {
	return "ak_" + uuid.New().String()
}

// NewUserID generates a new unique ID for a user.
func NewUserID() string {
	return "usr_" + uuid.New().String()
}

// --- Sentinel Errors -------------------------------------------------------

var (
	ErrInvalidKey = errors.New("invalid API key")
	ErrRevokedKey = errors.New("API key has been revoked")
)
