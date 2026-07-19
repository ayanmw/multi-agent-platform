// Package auth 提供平台的用户、role 与 API key 管理功能。
//
// # Auth 模型
//
// 平台使用 API key 认证(Phase 6 不使用密码)。每个用户可以拥有多个 API key;
// key 以 bcrypt 哈希形式存储。支持三种 RBAC role:admin、user、viewer。
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

// --- 模型类型 ----------------------------------------------------------

// Role 表示用户的 permission 级别。
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleUser   Role = "user"
	RoleViewer Role = "viewer"
)

// IsValid 检查 role 是否为已定义的常量之一。
func (r Role) IsValid() bool {
	switch r {
	case RoleAdmin, RoleUser, RoleViewer:
		return true
	}
	return false
}

// User 表示一个已注册的平台用户。
type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// APIKey 表示用于程序化认证的 key。
// 原始 key 绝不存储 — 只存储 bcrypt 哈希。
// KeyHash 标记为 json:"-",因此绝不会被意外序列化。
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

// IsRevoked 报告该 key 是否已被吊销。
func (k *APIKey) IsRevoked() bool {
	return k.RevokedAt != nil
}

// --- DB 记录类型(用于 migrate.go 与持久化) -----------------------------

// UserRecordDB 是 User 的持久化形式(对应 users 表 schema)。
type UserRecordDB struct {
	ID        string
	Name      string
	Role      Role
	CreatedAt string // ISO 8601 格式,用于 SQLite
}

// APIKeyRecordDB 是 APIKey 的持久化形式(对应 api_keys 表)。
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

// --- Key 操作 ------------------------------------------------------------

// DefaultBcryptCost 是用于 key 哈希的 bcrypt cost 因子。
const DefaultBcryptCost = 12

// GenerateAPIKey 创建一个新的密码学随机 API key。
// 格式:"sk_" + base64url(32 个随机字节) = 44 字符的字符串。
// 返回原始 key(对用户只展示一次)以及用于显示的 prefix。
func GenerateAPIKey() (rawKey, prefix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	rawKey = "sk_" + base64.RawURLEncoding.EncodeToString(b)
	prefix = rawKey[:12]
	return rawKey, prefix, nil
}

// HashPassword 使用 bcrypt 对原始 key 进行哈希。只应存储哈希值。
func HashPassword(rawKey string) ([]byte, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(rawKey), DefaultBcryptCost)
	if err != nil {
		return nil, fmt.Errorf("bcrypt hash: %w", err)
	}
	return hash, nil
}

// VerifyPassword 将原始 key 与已存储的 bcrypt 哈希进行比较。
func VerifyPassword(rawKey string, hash []byte) error {
	if len(hash) == 0 {
		return errors.New("no key hash stored")
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(rawKey)); err != nil {
		return ErrInvalidKey
	}
	return nil
}

// MatchPrefix 使用恒定时间比较检查 rawKey 是否以 prefix 开头。
// 用于在昂贵的 bcrypt 校验之前做快速预过滤。
func MatchPrefix(rawKey, prefix string) bool {
	if len(rawKey) < len(prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(rawKey[:len(prefix)]), []byte(prefix)) == 1
}

// StripPrefix 移除 "sk_" 前缀(如果存在)。
func StripPrefix(key string) string {
	return strings.TrimPrefix(key, "sk_")
}

// --- Store 抽象 -----------------------------------------------------------

// APIKeyStore 抽象了 API key 的持久化操作。
// 实现必须只存储 bcrypt 哈希,绝不存储原始 key。
type APIKeyStore interface {
	Create(userID, name string) (*APIKey, string, error)
	List(userID string) ([]APIKey, error)
	Revoke(keyID string) error
	Verify(rawKey string) (*APIKey, error)
	// GetUser 加载指定 ID 的用户记录。auth middleware
	// 需要它来将用户的 RBAC role 注入到请求 context 中。
	GetUser(userID string) (*User, error)
}

// --- HTTP API --------------------------------------------------------------

// AuthAPI 是 auth 相关端点的 HTTP handler 组。
// 它在 /api/auth/api-keys 下暴露面向用户的 API key 生命周期操作。
type AuthAPI struct {
	store      APIKeyStore
	seedUserID string
}

// NewAuthAPI 返回一个可用于路由注册的 AuthAPI。
func NewAuthAPI(store APIKeyStore) *AuthAPI {
	return &AuthAPI{store: store}
}

// GetStore 返回底层的 APIKeyStore,供 middleware 使用。
func (a *AuthAPI) GetStore() APIKeyStore {
	return a.store
}

// SetSeedUserID 设置当认证被关闭时使用的兜底用户 ID。
func (a *AuthAPI) SetSeedUserID(userID string) {
	a.seedUserID = userID
}

// SeedUserID 返回已配置的兜底用户 ID。
func (a *AuthAPI) SeedUserID() string {
	return a.seedUserID
}

// RegisterRoutes 将 auth 端点挂载到 mux 上。
func (a *AuthAPI) RegisterRoutes(mux any) {
	mux_, ok := mux.(*http.ServeMux)
	if !ok {
		return
	}
	mux_.HandleFunc("/api/auth/api-keys", a.handleAPIKeys)
	mux_.HandleFunc("/api/auth/api-keys/", a.handleAPIKeyByID)
}

// NewAPIKeyID 为 API key 记录生成一个新的唯一 ID。
func NewAPIKeyID() string {
	return "ak_" + uuid.New().String()
}

// NewUserID 为用户生成一个新的唯一 ID。
func NewUserID() string {
	return "usr_" + uuid.New().String()
}

// --- 哨兵错误(Sentinel Errors) ------------------------------------------

var (
	ErrInvalidKey = errors.New("invalid API key")
	ErrRevokedKey = errors.New("API key has been revoked")
)
