package tool

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Tool 表示 agent 可调用的工具。每个工具属于一个可选的 namespace，
// 并携带一组用于发现与过滤的 tags。Registry 以工具的 FullName
// （namespace/name，或 namespace 为空时仅 name）为键存储工具。
type Tool interface {
	// Namespace 返回工具的 namespace。空表示工具位于全局 namespace，
	// 其 FullName 等于 Name。
	Namespace() string
	// Name 返回工具的短标识符，在其 namespace 内唯一。
	Name() string
	// FullName 返回 Registry 使用的完全限定标识符：
	// namespace 非空时为 "namespace/name"，否则为 "name"。
	FullName() string
	// Aliases 返回应解析到该工具的别名。别名与主 FullName 共享同一
	// namespace，并会被加入 registry，使搜索可以按常见同义词找到工具
	// （例如用 "web_fetch" 指代 "core/fetch_url"）。
	Aliases() []string
	// Description 返回工具用途的人类可读说明。
	Description() string
	// Parameters 返回描述输入形状的 JSON Schema。
	Parameters() map[string]any
	// Tags 返回用于分类与过滤的标签列表。
	Tags() []string
	// Version 返回工具的版本标识符，用于多版本并存。builtin 工具可返回空字符串。
	Version() string
	// Source 返回工具来源，取值 "builtin" / "local_db" / "mcp" / "plugin"。
	Source() string
	// CanonicalName 返回 Registry 使用的唯一键：namespace/name@version（namespace 为空时为 name@version）。
	CanonicalName() string
	// Execute 使用给定输入 map 运行工具并返回结果。
	Execute(input map[string]any) (any, error)
}

// canonicalizeKey 是 Registry 内部对 CanonicalName 的兜底补全：
// 当 CanonicalName() 返回空时回退到 FullName()，避免空键插入 map。
func canonicalizeKey(tool Tool) string {
	if key := tool.CanonicalName(); key != "" {
		return key
	}
	return tool.FullName()
}

// Get 按 registry key 查找工具并返回是否存在。为了兼容 FullName 调用，
// 当精确 key 未命中且只有一个匹配该 FullName 的工具时，返回该工具及 true。
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if t, ok := r.tools[name]; ok {
		return t, true
	}
	return r.getByFullNameLocked(name)
}

// getByFullNameLocked 在持有读锁时按 FullName（不含版本）查找唯一匹配。
// 若同名不同版本存在多个，为避免歧义返回 (nil, false)。
func (r *Registry) getByFullNameLocked(fullName string) (Tool, bool) {
	var matched Tool
	var count int
	for key, t := range r.tools {
		if key == "" {
			continue
		}
		if t.FullName() == fullName {
			matched = t
			count++
		}
	}
	if count == 1 {
		return matched, true
	}
	return nil, false
}

// Registry 管理可用工具。可被多个 goroutine 并发安全使用。
// 内置工具不能在 Registry 层面反注册；调用方可通过 IsBuiltin 先行检查，
// 再决定是否调用 Unregister。
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	// order 保留注册顺序，使 List() 返回确定性的序列。该 slice 仅追加；
	// 重复注册同一工具会保留其原始位置，以保证多次注册调用间工具索引稳定。
	order []string
}

// NewRegistry 创建一个不含任何工具的空 Registry。
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
		order: make([]string, 0),
	}
}

// Clone 返回当前 registry 的浅拷贝。新 registry 拥有独立的 tools map
// 与 order slice，但工具实例本身共享（工具应是无状态闭包，共享安全）。
// 这让调用方可以基于一份基础工具集创建独立的 registry，并为特定任务
// 动态注入只有该任务可见的工具（例如 leader-only 的 dispatch_sub_agent）。
func (r *Registry) Clone() *Registry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := &Registry{
		tools: make(map[string]Tool, len(r.tools)),
		order: make([]string, len(r.order)),
	}
	copy(out.order, r.order)
	for k, v := range r.tools {
		out.tools[k] = v
	}
	return out
}

// Register 将工具加入 registry。若已存在同 FullName 的工具，将被静默覆盖。
// 工具定义的任何 Aliases 也会被注册并指向同一 Tool 实例。
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registerLocked(tool)
}

// registerLocked 在持有 registry 锁的情况下注册工具及其别名。
func (r *Registry) registerLocked(tool Tool) {
	key := canonicalizeKey(tool)
	if key == "" {
		return
	}
	if _, exists := r.tools[key]; !exists {
		r.order = append(r.order, key)
	}
	r.tools[key] = tool
	for _, alias := range tool.Aliases() {
		if alias == "" || alias == key {
			continue
		}
		fullAlias := alias
		if tool.Namespace() != "" && !strings.Contains(alias, "/") {
			fullAlias = tool.Namespace() + "/" + alias
		}
		if _, exists := r.tools[fullAlias]; !exists {
			r.order = append(r.order, fullAlias)
		}
		r.tools[fullAlias] = tool
	}
}

// Execute 以 registry key 或 FullName 标识并使用所提供的输入运行该工具。
// 当传入 FullName 且有多个版本时返回错误，调用方应使用 CanonicalName 精确指定版本。
//
// 此入口不携带 ExecuteContext，workdir 由 input["workdir"] 决定。worktree 隔离
// 路径（per-run holder 注入）请用 ExecuteWithCtx。
func (r *Registry) Execute(name string, input map[string]any) (any, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	if !ok {
		var ambiguous bool
		tool, ambiguous = r.getByFullNameLocked(name)
		if ambiguous {
			r.mu.RUnlock()
			return nil, fmt.Errorf("tool name %q is ambiguous; use canonical name namespace/name@version", name)
		}
	}
	r.mu.RUnlock()
	if tool == nil {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return tool.Execute(input)
}

// CtxTool 是支持 ExecuteContext 的 Tool 扩展。Registry.ExecuteWithCtx 在拿到
// tool 实例后，若其实现 CtxTool 则走带 ctx 的执行路径（worktree 隔离 holder
// 注入）；否则回退到普通 Execute（input["workdir"]）。
type CtxTool interface {
	ExecuteWithCtx(ctx ExecuteContext, input map[string]any) (any, error)
}

// ExecuteWithCtx 与 Execute 同语义，但携带 ExecuteContext。Engine 在 worktree
// 隔离启用时用本入口：把 per-run holder 的当前值经 ctx.Workdir 传入，使其
// 优先于 input["workdir"]，从而 LLM 无法伪造 workdir 逃逸到 worktree 之外。
// 不实现 CtxTool 的 tool（如 skill/todo）透明回退到 Execute，行为不变。
func (r *Registry) ExecuteWithCtx(name string, ctx ExecuteContext, input map[string]any) (any, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	if !ok {
		var ambiguous bool
		tool, ambiguous = r.getByFullNameLocked(name)
		if ambiguous {
			r.mu.RUnlock()
			return nil, fmt.Errorf("tool name %q is ambiguous; use canonical name namespace/name@version", name)
		}
	}
	r.mu.RUnlock()
	if tool == nil {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	if ctxTool, ok := tool.(CtxTool); ok {
		return ctxTool.ExecuteWithCtx(ctx, input)
	}
	return tool.Execute(input)
}

// List 返回所有已注册工具的快照（不含别名）。返回的 slice 是副本，
// 可在不持有 registry 锁的情况下安全迭代。当 includeAliases 为 false
// （LLM 工具定义的默认行为）时，别名会被省略，避免向模型发送重复的
// 函数定义。当为 true 时，调用方会收到包含别名在内的全部注册条目。
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Tool, 0, len(r.tools))
	seen := make(map[Tool]struct{})
	// 按注册顺序迭代，以保证发给 LLM 的工具定义具有确定性。
	// Go 中 map 的迭代顺序刻意被随机化，因此必须使用 order slice，
	// 而不能直接对 r.tools 进行 range。
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			if _, exists := seen[tool]; !exists {
				list = append(list, tool)
				seen[tool] = struct{}{}
			}
		}
	}
	return list
}

// ListAll 返回所有已注册工具条目，包含别名。适用于用户可能按别名搜索的
// 发现 API。
func (r *Registry) ListAll() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			list = append(list, tool)
		}
	}
	return list
}

// Unregister 按 registry key 从 registry 中移除工具。
// 若工具未找到则返回错误；若为内置工具也返回错误（内置工具不能通过
// Registry 移除，可使用 IsBuiltin 先行检查）。
// 注意：反注册主名称也会一并移除指向它的所有别名。
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isBuiltinLocked(name) {
		return fmt.Errorf("cannot unregister built-in tool: %s", name)
	}
	tool, ok := r.tools[name]
	if !ok {
		return fmt.Errorf("tool not found: %s", name)
	}
	// 移除主名称及所有指向该工具的已注册别名。
	key := canonicalizeKey(tool)
	delete(r.tools, key)
	// 若调用方按 alias 反注册，name 本身也要删除。
	if name != key {
		delete(r.tools, name)
	}
	for _, alias := range tool.Aliases() {
		fullAlias := alias
		if tool.Namespace() != "" && !strings.Contains(alias, "/") {
			fullAlias = tool.Namespace() + "/" + alias
		}
		delete(r.tools, fullAlias)
	}
	// order slice 保持不变：过期的名称会被 List() 忽略。
	return nil
}

// isBuiltinLocked 是 IsBuiltin 的无锁版本，调用方必须已持有 r.mu 的写锁。
// 当 registry 中存在该工具时以 Source() 为准；不存在时回退到旧硬编码名单。
func (r *Registry) isBuiltinLocked(name string) bool {
	if t, ok := r.tools[name]; ok {
		return t.Source() == "builtin"
	}
	if t, ok := r.getByFullNameLocked(name); ok {
		return t.Source() == "builtin"
	}
	switch name {
	case "run_shell", "write_file", "read_file":
		return true
	}
	return false
}

// IsBuiltin 当给定工具名对应的工具 Source() 为 "builtin" 时返回 true。
// 内置工具不能通过动态工具注册 API 删除。
func (r *Registry) IsBuiltin(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.isBuiltinLocked(name)
}

// ToJSON 将每个已注册工具序列化为 JSON 数组。每个条目包含工具的
// namespace、name、full name、description、parameters 与 tags。
func (r *Registry) ToJSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	schema := make([]map[string]any, 0, len(r.tools))
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			schema = append(schema, map[string]any{
				"namespace":   tool.Namespace(),
				"name":        tool.Name(),
				"full_name":   tool.FullName(),
				"description": tool.Description(),
				"parameters":  tool.Parameters(),
				"tags":        tool.Tags(),
			})
		}
	}
	return json.Marshal(schema)
}

// ToolTags 返回以给定名称注册的工具的 tags；若工具不存在则返回 nil。
// Harness 的 TagPolicyRule 用它来强制 TaskContract 权限，而无需引入
// 具体的 BuiltinTool 类型。
func (r *Registry) ToolTags(name string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	if !ok {
		tool, _ = r.getByFullNameLocked(name)
	}
	if tool == nil {
		return nil
	}
	return tool.Tags()
}

// ToolMetadata 返回以给定名称注册的工具的 namespace、description 与 tags。
// Engine 用它在 tool_call_started 与 approval 事件中发出权威的工具元数据。
func (r *Registry) ToolMetadata(name string) (namespace, description string, tags []string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, exists := r.tools[name]
	if !exists {
		tool, _ = r.getByFullNameLocked(name)
	}
	if tool == nil {
		return "", "", nil, false
	}
	return tool.Namespace(), tool.Description(), tool.Tags(), true
}

// lookupByCanonicalName 返回指定 CanonicalName 的工具；未找到返回 nil。
func (r *Registry) lookupByCanonicalName(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// Names 返回所提供工具的短 Name() 值，保留原顺序。
func Names(tools []Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name())
	}
	return out
}

// FilterByTag 返回 Tags() 包含给定 tag 的工具子集。
func FilterByTag(tools []Tool, tag string) []Tool {
	out := make([]Tool, 0)
	for _, t := range tools {
		if slices.Contains(t.Tags(), tag) {
			out = append(out, t)
		}
	}
	return out
}
