package llm

import (
	"fmt"
	"sync"
	"time"
)

// MockResponseType 标识 mock 脚本中响应条目的类型。
type MockResponseType string

const (
	// MockResponseText 发出单条文本响应，无 tool call。
	MockResponseText MockResponseType = "text"
	// MockResponseToolCall 发出 tool call 响应。
	MockResponseToolCall MockResponseType = "tool_call"
)

// MockResponse 是 mock 脚本响应序列中的单步。
// 当 Type 为 "tool_call" 时，ToolCalls 携带所请求的 tool call。
type MockResponse struct {
	Type      MockResponseType `json:"type"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []ToolCall       `json:"tool_calls,omitempty"`
	DelayMs   int              `json:"delay_ms,omitempty"`
}

// MockScript 描述 MockProvider 使用的确定性 LLM 响应序列。
// 可通过 caseID 或最后一条 user 消息中的关键字进行匹配。
type MockScript struct {
	ID         string         `json:"id"`
	CaseID     string         `json:"case_id"`
	Priority   int            `json:"priority"`
	MatchInput []string       `json:"match_input"`
	Responses  []MockResponse `json:"responses"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// MockScriptStore 定义 MockScript 记录的持久化操作。
// 可插入 DB 后端实现而无需改动 MockProvider。
type MockScriptStore interface {
	// List 返回所有已存储的 mock 脚本。
	List() ([]MockScript, error)
	// Get 按 ID 返回单个脚本。
	Get(id string) (MockScript, error)
	// Save 持久化一个脚本，必要时分配 ID。
	Save(script MockScript) (MockScript, error)
	// Delete 按 ID 删除脚本。
	Delete(id string) error
	// LoadBuiltin 用内置脚本初始化 store。
	LoadBuiltin(scripts []MockScript) error
}

// InMemoryMockScriptStore 是 MockScriptStore 的线程安全内存实现。
// 在无可用 DB 或作为 DB store 之上的缓存层时使用。
type InMemoryMockScriptStore struct {
	mu      sync.RWMutex
	scripts map[string]MockScript
}

// NewInMemoryMockScriptStore 创建一个空的内存 mock 脚本 store。
func NewInMemoryMockScriptStore() *InMemoryMockScriptStore {
	return &InMemoryMockScriptStore{
		scripts: make(map[string]MockScript),
	}
}

// List 返回所有已存储的 mock 脚本。
func (s *InMemoryMockScriptStore) List() ([]MockScript, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]MockScript, 0, len(s.scripts))
	for _, script := range s.scripts {
		list = append(list, script)
	}
	return list, nil
}

// Get 按 ID 返回单个脚本。
func (s *InMemoryMockScriptStore) Get(id string) (MockScript, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	script, ok := s.scripts[id]
	if !ok {
		return MockScript{}, fmt.Errorf("mock script %q not found", id)
	}
	return script, nil
}

// Save 持久化一个脚本，ID 为空时分配随机 ID。
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

// Delete 按 ID 删除脚本。
func (s *InMemoryMockScriptStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.scripts[id]; !ok {
		return fmt.Errorf("mock script %q not found", id)
	}
	delete(s.scripts, id)
	return nil
}

// LoadBuiltin 用内置脚本初始化 store，相同 ID 的脚本会被覆盖。
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

// DefaultMockStore 是进程级默认内存 mock 脚本 store。
// mock provider 与 mock 管理 API 共用同一组脚本。
var DefaultMockStore = NewInMemoryMockScriptStore()

// RegisterMockScriptForTest 把一条 mock script 注入 DefaultMockStore 并返回一个
// 清理函数，调用清理函数会从 store 中删除该 script。
//
// 为什么需要这个辅助函数：DefaultMockStore 是进程级全局变量，直接在测试中
// 调用 Save 会让脚本残留，污染后续用例（例如其它测试默认走 builtin 脚本时
// 可能被误命中）。用本函数注册的脚本，测试结束后通过返回的 cleanup 删除，
// 保证 DefaultMockStore 回到测试前状态。
//
// 用法：
//
//	cleanup := llm.RegisterMockScriptForTest(llm.MockScript{...})
//	defer cleanup()
func RegisterMockScriptForTest(script MockScript) func() {
	saved, err := DefaultMockStore.Save(script)
	if err != nil {
		// Save 在 InMemoryMockScriptStore 实现中永远不会失败；这里仅做防御性处理。
		return func() {}
	}
	id := saved.ID
	return func() { _ = DefaultMockStore.Delete(id) }
}
