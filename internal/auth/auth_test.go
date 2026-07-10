// auth_test.go — white-box unit tests for the auth package's pure logic.
//
// Coverage focuses on the cryptographic / hashing helpers and value-type
// methods that have no DB or HTTP dependency. bcrypt cost=12 makes each
// HashPassword call ~100ms, so tests reuse hashes and keep bcrypt
// invocations to a handful across the whole file.
package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// --- GenerateAPIKey --------------------------------------------------------

// TestGenerateAPIKeyFormat asserts the structural invariants of a freshly
// generated key: "sk_" prefix, expected length (46 chars = 3 + 43 base64url),
// and prefix == first 12 chars of the raw key.
func TestGenerateAPIKeyFormat(t *testing.T) {
	raw, prefix, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey returned error: %v", err)
	}
	if !strings.HasPrefix(raw, "sk_") {
		t.Errorf("raw key %q missing sk_ prefix", raw)
	}
	// 32 bytes -> base64url without padding = 43 chars; +3 for "sk_" = 46.
	if got := len(raw); got != 46 {
		t.Errorf("raw key length = %d, want 46", got)
	}
	if prefix != raw[:12] {
		t.Errorf("prefix %q != raw[:12] %q", prefix, raw[:12])
	}
}

// TestGenerateAPIKeyUniqueness asserts two calls produce different keys,
// proving crypto/rand entropy is wired up.
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

// TestGenerateAPIKeyManySamples ensures uniqueness across a larger sample
// and that every key meets the format contract.
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

// TestHashPasswordProducesBcryptHash verifies the returned hash is a non-empty
// bcrypt digest distinct from the input and consumable by bcrypt directly.
func TestHashPasswordProducesBcryptHash(t *testing.T) {
	// bcrypt cost=12, expected ~100ms/hash
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
	// bcrypt.CompareHashAndPassword returns nil on a match.
	if err := bcrypt.CompareHashAndPassword(hash, []byte(raw)); err != nil {
		t.Errorf("bcrypt.CompareHashAndPassword: %v", err)
	}
}

// TestVerifyPasswordRoundTrip verifies VerifyPassword succeeds for the
// matching raw key and fails (with ErrInvalidKey identity) for a wrong one.
func TestVerifyPasswordRoundTrip(t *testing.T) {
	// bcrypt cost=12, expected ~100ms/hash
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

// TestVerifyPasswordEmptyHash asserts an empty hash slice is rejected
// (guard clause in VerifyPassword).
func TestVerifyPasswordEmptyHash(t *testing.T) {
	if err := VerifyPassword("sk_anything", nil); err == nil {
		t.Fatal("VerifyPassword with nil hash returned nil, want error")
	}
	if err := VerifyPassword("sk_anything", []byte{}); err == nil {
		t.Fatal("VerifyPassword with empty hash returned nil, want error")
	}
}

// TestVerifyPasswordErrorIdentityTable drives the error-identity check
// across multiple wrong-key inputs to ensure we never leak bcrypt's
// internal error type — callers must only see ErrInvalidKey.
func TestVerifyPasswordErrorIdentityTable(t *testing.T) {
	// bcrypt cost=12, expected ~100ms/hash (one hash, reused)
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

// TestGenerateHashVerifyIntegration exercises the full key lifecycle used by
// the SqliteAPIKeyStore: GenerateAPIKey -> HashPassword -> VerifyPassword,
// ensuring every stage agrees.
func TestGenerateHashVerifyIntegration(t *testing.T) {
	// bcrypt cost=12, expected ~100ms/hash
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

// TestMatchPrefix table-drives the constant-time prefix comparison.
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

// TestStripPrefix table-drives the "sk_" prefix removal helper.
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

// TestRoleIsValid table-drives Role.IsValid across valid and invalid roles.
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

// TestAPIKeyIsRevoked asserts IsRevoked reflects the RevokedAt pointer.
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
		{"zero_time_set_revoked", &zero, true}, // non-nil pointer means revoked
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

// --- ID generators ---------------------------------------------------------

// TestNewAPIKeyIDFormat asserts IDs carry the "ak_" namespace and are unique.
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

// TestNewUserIDFormat asserts IDs carry the "usr_" namespace and are unique.
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

// TestNewAPIKeyIDAndUserIDDistinctNamespaces ensures the two generators
// don't accidentally share a prefix.
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

// --- AuthAPI accessors -----------------------------------------------------

// TestNewAuthAPIDefaults verifies the constructor wires the store and that
// the seed-user-ID accessors behave as plain getters/setters. We pass a nil
// store because these accessors never touch it.
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
