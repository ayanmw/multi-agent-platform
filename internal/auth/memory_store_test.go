package auth

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// memoryAPIKeyStore is an in-memory implementation of APIKeyStore for tests.
type memoryAPIKeyStore struct {
	mu   sync.RWMutex
	keys map[string]*APIKey
	// rawKeys maps key ID to the raw key for verification in tests.
	rawKeys map[string]string
	users   map[string]*User
}

// NewMemoryAPIKeyStore creates an in-memory API key store for testing.
func NewMemoryAPIKeyStore() APIKeyStore {
	return &memoryAPIKeyStore{
		keys:    make(map[string]*APIKey),
		rawKeys: make(map[string]string),
		users:   make(map[string]*User),
	}
}

func (m *memoryAPIKeyStore) Create(userID, name string) (*APIKey, string, error) {
	rawKey, prefix, err := GenerateAPIKey()
	if err != nil {
		return nil, "", err
	}
	hash, err := HashPassword(rawKey)
	if err != nil {
		return nil, "", err
	}
	key := &APIKey{
		ID:        uuid.New().String(),
		UserID:    userID,
		Name:      name,
		KeyHash:   hash,
		Prefix:    prefix,
		CreatedAt: time.Now(),
	}
	m.mu.Lock()
	m.keys[key.ID] = key
	m.rawKeys[key.ID] = rawKey
	if _, ok := m.users[userID]; !ok {
		m.users[userID] = &User{ID: userID, Name: userID, Role: RoleUser}
	}
	m.mu.Unlock()
	return key, rawKey, nil
}

func (m *memoryAPIKeyStore) List(userID string) ([]APIKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []APIKey
	for _, k := range m.keys {
		if k.UserID == userID && !k.IsRevoked() {
			out = append(out, *k)
		}
	}
	return out, nil
}

func (m *memoryAPIKeyStore) Revoke(keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[keyID]
	if !ok {
		return errors.New("key not found")
	}
	now := time.Now()
	k.RevokedAt = &now
	return nil
}

func (m *memoryAPIKeyStore) Verify(rawKey string) (*APIKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, k := range m.keys {
		if !MatchPrefix(rawKey, k.Prefix) {
			continue
		}
		if err := VerifyPassword(rawKey, k.KeyHash); err != nil {
			return nil, err
		}
		now := time.Now()
		k.LastUsedAt = &now
		return k, nil
	}
	return nil, ErrInvalidKey
}
