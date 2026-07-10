package llm

import (
	"fmt"
	"sync"
	"time"
)

// MockResponseType identifies the kind of response entry in a mock script.
type MockResponseType string

const (
	// MockResponseText emits a single text response with no tool calls.
	MockResponseText MockResponseType = "text"
	// MockResponseToolCall emits a tool call response.
	MockResponseToolCall MockResponseType = "tool_call"
)

// MockResponse is a single step in a mock script response sequence.
// When Type is "tool_call", ToolCalls carries the requested tool calls.
type MockResponse struct {
	Type      MockResponseType `json:"type"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []ToolCall       `json:"tool_calls,omitempty"`
	DelayMs   int              `json:"delay_ms,omitempty"`
}

// MockScript describes a deterministic LLM response sequence used by MockProvider.
// It can be matched by caseID or by keywords in the last user message.
type MockScript struct {
	ID         string         `json:"id"`
	CaseID     string         `json:"case_id"`
	Priority   int            `json:"priority"`
	MatchInput []string       `json:"match_input"`
	Responses  []MockResponse `json:"responses"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// MockScriptStore defines persistence operations for MockScript records.
// DB-backed implementations can be plugged in without changing MockProvider.
type MockScriptStore interface {
	// List returns all stored mock scripts.
	List() ([]MockScript, error)
	// Get returns a single script by ID.
	Get(id string) (MockScript, error)
	// Save persists a script, assigning an ID if needed.
	Save(script MockScript) (MockScript, error)
	// Delete removes a script by ID.
	Delete(id string) error
	// LoadBuiltin seeds the store with built-in scripts.
	LoadBuiltin(scripts []MockScript) error
}

// InMemoryMockScriptStore is a thread-safe in-memory implementation of MockScriptStore.
// It is used when no DB is available or as a cache layer on top of a DB store.
type InMemoryMockScriptStore struct {
	mu      sync.RWMutex
	scripts map[string]MockScript
}

// NewInMemoryMockScriptStore creates an empty in-memory mock script store.
func NewInMemoryMockScriptStore() *InMemoryMockScriptStore {
	return &InMemoryMockScriptStore{
		scripts: make(map[string]MockScript),
	}
}

// List returns all stored mock scripts.
func (s *InMemoryMockScriptStore) List() ([]MockScript, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]MockScript, 0, len(s.scripts))
	for _, script := range s.scripts {
		list = append(list, script)
	}
	return list, nil
}

// Get returns a single script by ID.
func (s *InMemoryMockScriptStore) Get(id string) (MockScript, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	script, ok := s.scripts[id]
	if !ok {
		return MockScript{}, fmt.Errorf("mock script %q not found", id)
	}
	return script, nil
}

// Save persists a script, assigning a random ID if empty.
func (s *InMemoryMockScriptStore) Save(script MockScript) (MockScript, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if script.ID == "" {
		script.ID = fmt.Sprintf("mock_%d", time.Now().UnixNano())
	}
	script.UpdatedAt = time.Now()
	if script.CreatedAt.IsZero() {
		script.CreatedAt = script.UpdatedAt
	}
	s.scripts[script.ID] = script
	return script, nil
}

// Delete removes a script by ID.
func (s *InMemoryMockScriptStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.scripts[id]; !ok {
		return fmt.Errorf("mock script %q not found", id)
	}
	delete(s.scripts, id)
	return nil
}

// LoadBuiltin seeds the store with built-in scripts, overwriting any existing scripts with the same ID.
func (s *InMemoryMockScriptStore) LoadBuiltin(scripts []MockScript) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, script := range scripts {
		if script.ID == "" {
			continue
		}
		script.UpdatedAt = time.Now()
		if script.CreatedAt.IsZero() {
			script.CreatedAt = script.UpdatedAt
		}
		s.scripts[script.ID] = script
	}
	return nil
}

// DefaultMockStore is the process-wide default in-memory mock script store.
// It is used by the mock provider and by the mock management API so both share
// the same set of scripts.
var DefaultMockStore = NewInMemoryMockScriptStore()
