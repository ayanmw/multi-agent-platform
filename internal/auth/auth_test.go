// auth_test.go — auth 包纯逻辑部分的白盒单元测试。
//
// 覆盖范围聚焦于加密 / 哈希辅助函数以及与 DB、HTTP 无依赖的值类型方法。
// bcrypt cost=12 会让每次 HashPassword 调用约耗时 100ms,因此测试会复用
// 哈希值,将整个文件中的 bcrypt 调用控制在少数几次。
package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// --- GenerateAPIKey --------------------------------------------------------

// TestGenerateAPIKeyFormat 断言一个新生成 key 的结构不变式:
// "sk_" 前缀、预期长度(46 字符 = 3 + 43 base64url),
// 以及 prefix == 原始 key 的前 12 个字符。
func TestGenerateAPIKeyFormat(t *testing.T) {
	raw, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey returned error: %v", err)
	}
	if !strings.HasPrefix(raw, "sk_") {
		t.Errorf("raw key %q missing sk_ prefix", raw)
	}
	// 32 字节 -> base64url 无 padding = 43 字符;再加 "sk_" 的 3 字符 = 46。
	if got := len(raw); got != 46 {
		t.Errorf("raw key length = %d, want 46", got)
	}
	if prefix != raw[:12] {
		t.Errorf("prefix %q != raw[:12] %q", prefix, raw[:12])
	}
}

// TestGenerateAPIKeyUniqueness 断言两次调用会生成不同的 key,
// 从而证明 crypto/rand 的熵已被正确接入。
func TestGenerateAPIKeyUniqueness(t *testing.T) {
	raw1, _, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("first GenerateAPIKey: %v", err)
	}
	raw2, _, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("second GenerateAPIKey: %v", err)
	}
	if raw1 == raw2 {
		t.Errorf("two generated keys are identical: %q", raw1)
	}
}

// TestGenerateAPIKeyManySamples 确保在更大样本量上的唯一性,
// 且每个 key 都满足格式契约。
func TestGenerateAPIKeyManySamples(t *testing.T) {
	const n = 16
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		raw, _, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if !strings.HasPrefix(raw, "sk_") || len(raw) != 46 {
			t.Errorf("iteration %d: bad key %q", i, raw)
		}
		if _, dup := seen[raw]; dup {
			t.Errorf("iteration %d: duplicate key %q", i, raw)
		}
		seen[raw] = struct{}{}
	}
}

// --- HashPassword + VerifyPassword -----------------------------------------

// TestHashPasswordProducesBcryptHash 验证返回的哈希是一个非空的
// bcrypt 摘要,与输入不同,且能被 bcrypt 直接消费。
func TestHashPasswordProducesBcryptHash(t *testing.T) {
	// bcrypt cost=12,预期约 100ms/hash
	raw := "sk_test_key_with_enough_entropy_to_be_reasonable"
	hash, err := HashPassword(raw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if len(hash) == 0 {
		t.Fatal("hash is empty")
	}
	if string(hash) == raw {
		t.Fatal("hash equals raw input")
	}
	// bcrypt.CompareHashAndPassword 匹配时返回 nil。
	if err := bcrypt.CompareHashAndPassword(hash, []byte(raw)); err != nil {
		t.Errorf("bcrypt.CompareHashAndPassword: %v", err)
	}
}

// TestVerifyPasswordRoundTrip 验证 VerifyPassword 对匹配的原始 key 成功,
// 对错误的 key 失败(且错误为 ErrInvalidKey 标识)。
func TestVerifyPasswordRoundTrip(t *testing.T) {
	// bcrypt cost=12,预期约 100ms/hash
	raw := "sk_the_correct_key_value_here"
	hash, err := HashPassword(raw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := VerifyPassword(raw, hash); err != nil {
		t.Errorf("VerifyPassword(correct) = %v, want nil", err)
	}
	wrong := "sk_a_totally_different_wrong_key"
	err = VerifyPassword(wrong, hash)
	if err == nil {
		t.Fatal("VerifyPassword(wrong) = nil, want error")
	}
	if !errors.Is(err, ErrInvalidKey) {
		t.Errorf("VerifyPassword(wrong) err = %v, want errors.Is ErrInvalidKey", err)
	}
}

// TestVerifyPasswordEmptyHash 断言空哈希切片会被拒绝
// (VerifyPassword 中的守卫子句)。
func TestVerifyPasswordEmptyHash(t *testing.T) {
	if err := VerifyPassword("sk_anything", nil); err == nil {
		t.Fatal("VerifyPassword with nil hash returned nil, want error")
	}
	if err := VerifyPassword("sk_anything", []byte{}); err == nil {
		t.Fatal("VerifyPassword with empty hash returned nil, want error")
	}
}

// TestVerifyPasswordErrorIdentityTable 用多个错误 key 输入驱动错误标识检查,
// 确保我们绝不泄露 bcrypt 的内部错误类型 — 调用方只能看到 ErrInvalidKey。
func TestVerifyPasswordErrorIdentityTable(t *testing.T) {
	// bcrypt cost=12,预期约 100ms/hash(只生成一次哈希并复用)
	raw := "sk_master_key_for_identity_tests"
	hash, err := HashPassword(raw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	wrongs := []string{
		"",
		"sk_wrong",
		"sk_" + strings.Repeat("x", 60),
		"sk_" + strings.Repeat("y", 60),
		"not even sk prefixed",
	}
	for _, w := range wrongs {
		err := VerifyPassword(w, hash)
		if err == nil {
			t.Errorf("VerifyPassword(%q) = nil, want error", w)
			continue
		}
		if !errors.Is(err, ErrInvalidKey) {
			t.Errorf("VerifyPassword(%q) err = %v, want errors.Is ErrInvalidKey", w, err)
		}
	}
}

// TestGenerateHashVerifyIntegration 演练 SqliteAPIKeyStore 使用的完整 key 生命周期:
// GenerateAPIKey -> HashPassword -> VerifyPassword,确保各阶段相互一致。
func TestGenerateHashVerifyIntegration(t *testing.T) {
	// bcrypt cost=12,预期约 100ms/hash
	raw, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if !MatchPrefix(raw, prefix) {
		t.Fatalf("MatchPrefix(raw, prefix) = false, want true (raw=%q prefix=%q)", raw, prefix)
	}
	hash, err := HashPassword(raw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := VerifyPassword(raw, hash); err != nil {
		t.Errorf("VerifyPassword on generated key = %v, want nil", err)
	}
	if err := VerifyPassword(raw+"_tampered", hash); err == nil {
		t.Error("VerifyPassword on tampered key = nil, want error")
	}
}

// --- MatchPrefix -----------------------------------------------------------

// TestMatchPrefix 以表驱动方式测试恒定时间前缀比较。
func TestMatchPrefix(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		prefix string
		want   bool
	}{
		{"exact_match", "sk_abcdef", "sk_abcdef", true},
		{"prefix_match", "sk_abcdef1234", "sk_abcdef", true},
		{"mismatch_same_length", "sk_abcdef", "sk_xyzxyz", false},
		{"mismatch_longer_raw", "sk_abcdef1234", "sk_xyzxyz", false},
		{"raw_shorter_than_prefix", "sk_", "sk_abcdef", false},
		{"empty_prefix_matches_anything", "sk_abcdef", "", true},
		{"both_empty", "", "", true},
		{"raw_empty_prefix_nonempty", "", "sk_", false},
		{"single_char_diff", "sk_aaa", "sk_baa", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchPrefix(tt.raw, tt.prefix); got != tt.want {
				t.Errorf("MatchPrefix(%q, %q) = %v, want %v", tt.raw, tt.prefix, got, tt.want)
			}
		})
	}
}

// --- StripPrefix -----------------------------------------------------------

// TestStripPrefix 以表驱动方式测试 "sk_" 前缀移除辅助函数。
func TestStripPrefix(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"removes_prefix", "sk_abcdef", "abcdef"},
		{"no_prefix_unchanged", "abcdef", "abcdef"},
		{"empty_string", "", ""},
		{"only_prefix", "sk_", ""},
		{"double_prefix_only_first_stripped", "sk_sk_abc", "sk_abc"},
		{"prefix_in_middle_not_stripped", "abcdesk_efg", "abcdesk_efg"},
		{"different_prefix_not_stripped", "pk_abcdef", "pk_abcdef"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripPrefix(tt.in); got != tt.want {
				t.Errorf("StripPrefix(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// --- Role.IsValid ----------------------------------------------------------

// TestRoleIsValid 以表驱动方式测试 Role.IsValid 在合法与非法 role 上的行为。
func TestRoleIsValid(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{"admin", RoleAdmin, true},
		{"user", RoleUser, true},
		{"viewer", RoleViewer, true},
		{"empty", "", false},
		{"unknown", "superadmin", false},
		{"case_sensitive_admin_upper", "ADMIN", false},
		{"case_sensitive_user_mixed", "User", false},
		{"random_string", "not_a_role", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.role.IsValid(); got != tt.want {
				t.Errorf("Role(%q).IsValid() = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}

// --- APIKey.IsRevoked ------------------------------------------------------

// TestAPIKeyIsRevoked 断言 IsRevoked 反映 RevokedAt 指针的状态。
func TestAPIKeyIsRevoked(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	zero := time.Time{}

	tests := []struct {
		name      string
		revokedAt *time.Time
		want      bool
	}{
		{"nil_not_revoked", nil, false},
		{"set_revoked", &past, true},
		{"zero_time_set_revoked", &zero, true}, // 非 nil 指针即视为已吊销
		{"future_time_revoked", &future, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &APIKey{RevokedAt: tt.revokedAt}
			if got := k.IsRevoked(); got != tt.want {
				t.Errorf("IsRevoked = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- ID 生成器 -------------------------------------------------------------

// TestNewAPIKeyIDFormat 断言 ID 带有 "ak_" 命名空间前缀且彼此唯一。
func TestNewAPIKeyIDFormat(t *testing.T) {
	id := NewAPIKeyID()
	if !strings.HasPrefix(id, "ak_") {
		t.Errorf("NewAPIKeyID() = %q, missing ak_ prefix", id)
	}
	if len(id) <= len("ak_") {
		t.Errorf("NewAPIKeyID() = %q, too short", id)
	}
	id2 := NewAPIKeyID()
	if id == id2 {
		t.Errorf("NewAPIKeyID() returned duplicate %q", id)
	}
}

// TestNewUserIDFormat 断言 ID 带有 "usr_" 命名空间前缀且彼此唯一。
func TestNewUserIDFormat(t *testing.T) {
	id := NewUserID()
	if !strings.HasPrefix(id, "usr_") {
		t.Errorf("NewUserID() = %q, missing usr_ prefix", id)
	}
	if len(id) <= len("usr_") {
		t.Errorf("NewUserID() = %q, too short", id)
	}
	id2 := NewUserID()
	if id == id2 {
		t.Errorf("NewUserID() returned duplicate %q", id)
	}
}

// TestNewAPIKeyIDAndUserIDDistinctNamespaces 确保两个生成器
// 不会意外共享同一个前缀。
func TestNewAPIKeyIDAndUserIDDistinctNamespaces(t *testing.T) {
	ak := NewAPIKeyID()
	usr := NewUserID()
	if strings.HasPrefix(ak, "usr_") {
		t.Errorf("API key ID %q has usr_ prefix", ak)
	}
	if strings.HasPrefix(usr, "ak_") {
		t.Errorf("User ID %q has ak_ prefix", usr)
	}
}

// TestMaskAPIKeyFormat 验证 API key prefix 在列表响应中被遮蔽,
// 从而枚举 key 时不会暴露可用的凭据 prefix。
func TestMaskAPIKeyFormat(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   string
	}{
		{"typical_12_char_prefix", "sk_aBcDeFgHiJk", "sk_a****HiJk"},
		{"short_prefix", "sk_12", "sk_1****"},
		{"exactly_8_prefix", "sk_abcdef", "sk_a****cdef"},
		{"empty", "", "****"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskAPIKey(tt.prefix); got != tt.want {
				t.Errorf("maskAPIKey(%q) = %q, want %q", tt.prefix, got, tt.want)
			}
		})
	}
}

// TestMemoryStoreGetUser 检查 in-memory store 会返回由 Create
// 隐式创建的用户。
func TestMemoryStoreGetUser(t *testing.T) {
	store := NewMemoryAPIKeyStore()
	userID := "usr_test_1"
	_, _, err := store.Create(userID, "memory-test")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	user, err := store.GetUser(userID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.ID != userID {
		t.Errorf("user.ID = %q, want %q", user.ID, userID)
	}
	if user.Role != RoleUser {
		t.Errorf("user.Role = %q, want %q", user.Role, RoleUser)
	}
}


// TestNewAuthAPIDefaults 验证构造函数会正确接入 store,
// 并且 seed-user-ID 的访问器表现为普通的 getter/setter。
// 我们传入 nil store,因为这些访问器从不接触它。
func TestNewAuthAPIDefaults(t *testing.T) {
	a := NewAuthAPI(nil)
	if a == nil {
		t.Fatal("NewAuthAPI returned nil")
	}
	if a.GetStore() != nil {
		t.Errorf("GetStore = %v, want nil", a.GetStore())
	}
	if a.SeedUserID() != "" {
		t.Errorf("SeedUserID = %q, want empty by default", a.SeedUserID())
	}
	a.SetSeedUserID("usr_seed")
	if got := a.SeedUserID(); got != "usr_seed" {
		t.Errorf("SeedUserID after set = %q, want %q", got, "usr_seed")
	}
}
