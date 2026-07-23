package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// mockTool — 用于 Registry 测试的最小 Tool 实现
// ---------------------------------------------------------------------------

// mockTool 是一个最小且确定性的 Tool 实现，用于在不触碰文件系统或 shell 的
// 情况下测试 Registry。它会记录每次 Execute 调用以及最后接收到的输入，
// 以便测试断言路由与输入透传的正确性。
type mockTool struct {
	namespace   string
	name        string
	description string
	params      map[string]any
	tags        []string
	aliases     []string
	source      string
	execFn      func(input map[string]any) (any, error)
	execCalls   int
	lastInput   map[string]any
}

func (m *mockTool) Namespace() string { return m.namespace }
func (m *mockTool) Name() string      { return m.name }

// FullName 返回工具的完全限定标识符。当 namespace 为空时返回短名，
// 否则返回 "namespace/name"。
func (m *mockTool) FullName() string {
	if m.namespace == "" {
		return m.name
	}
	return m.namespace + "/" + m.name
}

func (m *mockTool) Description() string        { return m.description }
func (m *mockTool) Parameters() map[string]any { return m.params }
func (m *mockTool) Tags() []string             { return m.tags }
func (m *mockTool) Aliases() []string          { return m.aliases }

// Version 返回 mock 工具的版本。测试中默认无版本。
func (m *mockTool) Version() string { return "" }

// mockToolSource 返回用于测试的 mock tool source。默认不是 builtin，
// 以便反注册测试可以正常移除工具。
func (m *mockTool) Source() string { return m.source }

// CanonicalName 返回 Registry 使用的唯一键。无版本时等于 FullName()。
func (m *mockTool) CanonicalName() string {
	if v := m.Version(); v != "" {
		return fmt.Sprintf("%s@%s", m.FullName(), v)
	}
	return m.FullName()
}

// Execute 记录本次调用，并在设置了 execFn 时委托给它；否则返回确定性的
// "mock-output:<name>" 字符串。
func (m *mockTool) Execute(input map[string]any) (any, error) {
	m.execCalls++
	m.lastInput = input
	if m.execFn != nil {
		return m.execFn(input)
	}
	return "mock-output:" + m.name, nil
}

// 编译期断言：mockTool 与内置工具类型均满足 Tool 接口。
var (
	_ Tool = (*mockTool)(nil)
	_ Tool = (*BuiltinTool)(nil)
	_ Tool = (*DynamicTool)(nil)
)

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func toolNames(tools []Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name())
	}
	return out
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// NewRegistry / List 基础
// ---------------------------------------------------------------------------

// TestNewRegistryEmpty 验证新创建的 Registry 不含任何工具。
func TestNewRegistryEmpty(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if got := r.List(); len(got) != 0 {
		t.Errorf("new registry List = %v, want empty", got)
	}
}

// TestListContainsAllRegistered 验证 List 会返回每个已注册工具。
func TestListContainsAllRegistered(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "a"})
	r.Register(&mockTool{name: "b"})
	r.Register(&mockTool{name: "c"})

	names := toolNames(r.List())
	if len(names) != 3 {
		t.Fatalf("expected 3 tools, got %d (%v)", len(names), names)
	}
	for _, want := range []string{"a", "b", "c"} {
		if !contains(names, want) {
			t.Errorf("List missing %s, got %v", want, names)
		}
	}
}

// ---------------------------------------------------------------------------
// Register + Execute 路由
// ---------------------------------------------------------------------------

// TestRegisterAndExecute 验证已注册的自定义工具可通过 Execute 调用，
// Execute 恰好被调用一次，且输入 map 原样透传。
func TestRegisterAndExecute(t *testing.T) {
	r := NewRegistry()
	mt := &mockTool{
		name:        "custom",
		description: "a custom tool",
		params:      map[string]any{"type": "object"},
	}
	r.Register(mt)

	in := map[string]any{"k": "v", "n": 42}
	out, err := r.Execute("custom", in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "mock-output:custom" {
		t.Errorf("Execute output = %v, want mock-output:custom", out)
	}
	if mt.execCalls != 1 {
		t.Errorf("execCalls = %d, want 1", mt.execCalls)
	}
	if mt.lastInput["k"] != "v" || mt.lastInput["n"] != 42 {
		t.Errorf("lastInput = %v, want original input", mt.lastInput)
	}
}

// TestExecuteInputPassthrough 使用捕获式 execFn 验证确切的输入 map
// 被转发到底层 Tool。
func TestExecuteInputPassthrough(t *testing.T) {
	r := NewRegistry()
	var captured map[string]any
	r.Register(&mockTool{
		name: "capture",
		execFn: func(input map[string]any) (any, error) {
			captured = input
			return "ok", nil
		},
	})

	in := map[string]any{"x": 1, "y": "hello"}
	if _, err := r.Execute("capture", in); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if captured["x"] != 1 || captured["y"] != "hello" {
		t.Errorf("input not passed through: %v", captured)
	}
}

// TestExecuteMissingTool 验证执行未注册的工具会返回包含 "tool not found" 的错误。
func TestExecuteMissingTool(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute("nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for missing tool")
	}
	if !strings.Contains(err.Error(), "tool not found") {
		t.Errorf("error should mention 'tool not found', got %q", err.Error())
	}
}

// TestExecuteErrorPropagation 验证 Tool 的 Execute 返回的错误会原样
// 传播给调用方。
func TestExecuteErrorPropagation(t *testing.T) {
	r := NewRegistry()
	sentinel := errors.New("boom")
	r.Register(&mockTool{
		name: "fail",
		execFn: func(input map[string]any) (any, error) {
			return nil, sentinel
		},
	})
	_, err := r.Execute("fail", nil)
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Namespace / Tags 元数据
// ---------------------------------------------------------------------------

// TestToolMetadata 验证 Tool 接口上新增的 namespace 与 tags 方法。
func TestToolMetadata(t *testing.T) {
	mt := &mockTool{
		namespace: "core",
		name:      "meta_reader",
		tags:      []string{"readonly", "metadata"},
	}
	if got := mt.Namespace(); got != "core" {
		t.Errorf("Namespace() = %q, want core", got)
	}
	if got := mt.Name(); got != "meta_reader" {
		t.Errorf("Name() = %q, want meta_reader", got)
	}
	if got := mt.FullName(); got != "core/meta_reader" {
		t.Errorf("FullName() = %q, want core/meta_reader", got)
	}
	if got := Names([]Tool{mt})[0]; got != "meta_reader" {
		t.Errorf("Names returned %q, want meta_reader", got)
	}
	wantTags := []string{"readonly", "metadata"}
	if len(mt.Tags()) != len(wantTags) {
		t.Fatalf("Tags() = %v, want %v", mt.Tags(), wantTags)
	}
	for i, want := range wantTags {
		if mt.Tags()[i] != want {
			t.Errorf("Tags()[%d] = %q, want %q", i, mt.Tags()[i], want)
		}
	}
}

// ---------------------------------------------------------------------------
// Register 覆盖语义
// ---------------------------------------------------------------------------

// TestRegisterOverwritesSilently 文档化说明 Register 没有返回值，会静默
// 覆盖同名的任何已存在工具。这是 registry.go 中 Register 的实际行为
// （Register 只是直接写入 map）。
func TestRegisterOverwritesSilently(t *testing.T) {
	r := NewRegistry()
	first := &mockTool{name: "dup", description: "first"}
	second := &mockTool{name: "dup", description: "second"}
	r.Register(first)
	r.Register(second) // 不可能返回错误 —— Register 不返回任何值

	// Execute 应当调用第二个（覆盖）工具。
	out, err := r.Execute("dup", nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "mock-output:dup" {
		t.Fatalf("got %v", out)
	}
	if first.execCalls != 0 {
		t.Errorf("first tool should not be called, execCalls=%d", first.execCalls)
	}
	if second.execCalls != 1 {
		t.Errorf("second tool should be called once, execCalls=%d", second.execCalls)
	}

	// List 必须恰好含有一个名为 "dup" 的条目（无重复）。
	count := 0
	for _, tl := range r.List() {
		if tl.Name() == "dup" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 entry named dup in List, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Unregister
// ---------------------------------------------------------------------------

// TestUnregisterExisting 验证反注册一个已注册的自定义工具会成功，并使
// 后续 Execute 调用失败。
func TestUnregisterExisting(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "temp"})

	if err := r.Unregister("temp"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if _, err := r.Execute("temp", nil); err == nil {
		t.Fatal("expected Execute to fail after Unregister")
	}
}

// TestUnregisterMissing 验证反注册从未注册过的工具会返回包含
// "tool not found" 的错误。
func TestUnregisterMissing(t *testing.T) {
	r := NewRegistry()
	err := r.Unregister("nope")
	if err == nil {
		t.Fatal("expected error for unregistering missing tool")
	}
	if !strings.Contains(err.Error(), "tool not found") {
		t.Errorf("error should mention 'tool not found', got %q", err.Error())
	}
}

// TestUnregisterBuiltinRejected 验证 Unregister 拒绝移除内置工具，并返回
// 包含 "built-in" 的错误。这可以防止 run_shell、write_file、read_file 被
// 通过 Registry 意外移除。
func TestUnregisterBuiltinRejected(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	for _, name := range []string{"run_shell", "write_file", "read_file"} {
		t.Run(name, func(t *testing.T) {
			if !r.IsBuiltin(name) {
				t.Fatalf("precondition: IsBuiltin(%q) should be true", name)
			}
			err := r.Unregister(name)
			if err == nil {
				t.Fatalf("Unregister(%q) = nil, expected error", name)
			}
			if !strings.Contains(err.Error(), "built-in") {
				t.Errorf("error should mention 'built-in', got %q", err.Error())
			}
			// 工具应当仍然处于注册状态。
			if _, err := r.Execute(name, map[string]any{}); err != nil {
				if strings.Contains(err.Error(), "tool not found") {
					t.Errorf("built-in tool was removed despite error: %v", err)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsBuiltin
// ---------------------------------------------------------------------------

// TestIsBuiltin 是对 IsBuiltin 名称检查的表驱动测试。IsBuiltin 是对
// "run_shell"/"write_file"/"read_file" 的纯字符串 switch，不查询
// registry 的内容。
func TestIsBuiltin(t *testing.T) {
	r := NewRegistry()
	tests := []struct {
		name string
		want bool
	}{
		{"run_shell", true},
		{"write_file", true},
		{"read_file", true},
		{"", false},
		{"Run_Shell", false},  // 大小写敏感
		{"run_shell ", false}, // 仅精确匹配（不做 trim）
		{" run_shell", false}, // 前导空格
		{"run_shell", true},   // 重复一次以保证确定性
		{"custom_tool", false},
		{"my_tool", false},
		{"run_shell_v2", false}, // 后缀破坏精确匹配
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.IsBuiltin(tc.name)
			if got != tc.want {
				t.Errorf("IsBuiltin(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestIsBuiltinIndependentOfRegistration 验证即使在一个从未注册过内置工具的
// 空 registry 上，IsBuiltin 对内置名称仍返回 true。
func TestIsBuiltinIndependentOfRegistration(t *testing.T) {
	r := NewRegistry() // 空
	for _, name := range []string{"run_shell", "write_file", "read_file"} {
		if !r.IsBuiltin(name) {
			t.Errorf("IsBuiltin(%q) = false on empty registry, want true", name)
		}
	}
	if r.IsBuiltin("custom") {
		t.Errorf("IsBuiltin(custom) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// RegisterBuiltins
// ---------------------------------------------------------------------------

// TestRegisterBuiltins 验证 RegisterBuiltins 后，三个内置工具都存在于
// registry 中（可通过 Execute 找到），并被 List 列出。
func TestRegisterBuiltins(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	wantNames := []string{"run_shell", "write_file", "read_file"}

	// 每个内置工具都应可被找到。我们用空输入 map 调用 Execute：
	// 每个内置工具的 executor 会校验自己必填的 string 字段并返回自身的
	// 校验错误（例如 "command must be a string"）。关键断言是错误不是
	// "tool not found" —— 那表示工具从未被注册。
	for _, name := range wantNames {
		t.Run("Execute_"+name, func(t *testing.T) {
			_, err := r.Execute(name, map[string]any{})
			if err == nil {
				// 某些工具可能容忍空输入；只要工具能被找到就没问题。
				// 仅在出现 "tool not found" 时判为失败。
				return
			}
			if strings.Contains(err.Error(), "tool not found") {
				t.Fatalf("built-in %s not registered: %v", name, err)
			}
		})
	}

	// List 必须包含全部三个内置名称。
	names := toolNames(r.List())
	for _, name := range wantNames {
		if !contains(names, name) {
			t.Errorf("List missing built-in %s, got %v", name, names)
		}
	}
}

// TestBuiltinToolsWriteAndReadInTempDir 在临时目录中端到端演练 write_file 与
// read_file 内置 executor。这确认了已注册的内置工具确实可工作，而不仅仅是
// 被列出。这里刻意不执行 run_shell 以避免 shell/平台依赖；其注册由
// TestRegisterBuiltins 覆盖。
func TestBuiltinToolsWriteAndReadInTempDir(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	dir := t.TempDir()
	path := filepath.Join(dir, "testfile.txt")
	content := "hello world"

	// write_file 应创建文件并报告成功。
	out, err := r.Execute("write_file", map[string]any{"path": path, "content": content})
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("write_file output not map: %T", out)
	}
	if m["success"] != true {
		t.Errorf("write_file success = %v, want true", m["success"])
	}

	// read_file 应返回我们写入的相同内容。
	out, err = r.Execute("read_file", map[string]any{"path": path})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	m, ok = out.(map[string]any)
	if !ok {
		t.Fatalf("read_file output not map: %T", out)
	}
	if m["content"] != content {
		t.Errorf("read_file content = %q, want %q", m["content"], content)
	}
}

// ---------------------------------------------------------------------------
// 按 full name 过滤 / 列举
// ---------------------------------------------------------------------------

// TestRegistryFilterByTag 验证 FilterByTag 只返回带有所请求 tag 的工具。
func TestRegistryFilterByTag(t *testing.T) {
	tools := []Tool{
		&mockTool{name: "a", tags: []string{"readonly"}},
		&mockTool{name: "b", tags: []string{"write", "dangerous"}},
		&mockTool{name: "c", tags: []string{"readonly", "filesystem"}},
		&mockTool{name: "d", tags: nil},
	}

	readonly := FilterByTag(tools, "readonly")
	if len(readonly) != 2 {
		t.Fatalf("expected 2 readonly tools, got %d", len(readonly))
	}
	got := toolNames(readonly)
	for _, want := range []string{"a", "c"} {
		if !contains(got, want) {
			t.Errorf("FilterByTag(readonly) missing %s, got %v", want, got)
		}
	}

	dangerous := FilterByTag(tools, "dangerous")
	if len(dangerous) != 1 || dangerous[0].Name() != "b" {
		t.Errorf("FilterByTag(dangerous) = %v, want [b]", toolNames(dangerous))
	}

	missing := FilterByTag(tools, "network")
	if len(missing) != 0 {
		t.Errorf("FilterByTag(network) = %v, want empty", toolNames(missing))
	}
}

// TestRegistryListUsesFullName 验证带 namespace 注册的工具以 FullName 为键
// 存入 registry，因此 Execute 可用完全限定名工作。
func TestRegistryListUsesFullName(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{namespace: "core", name: "reader", tags: []string{"readonly"}})
	r.Register(&mockTool{namespace: "ext", name: "reader", tags: []string{"network"}})
	r.Register(&mockTool{name: "plain"})

	// List 应包含全部三个工具。
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(list))
	}

	// Execute 必须能使用 full name 工作。
	if _, err := r.Execute("core/reader", map[string]any{}); err != nil {
		t.Errorf("Execute(core/reader): %v", err)
	}
	if _, err := r.Execute("ext/reader", map[string]any{}); err != nil {
		t.Errorf("Execute(ext/reader): %v", err)
	}
	if _, err := r.Execute("plain", map[string]any{}); err != nil {
		t.Errorf("Execute(plain): %v", err)
	}

	// 简写 "reader" 不应匹配任何工具，因为两个 reader 都在 namespace 中，
	// 而 plain 工具名为 "plain"。
	if _, err := r.Execute("reader", map[string]any{}); err == nil {
		t.Errorf("Execute(reader) should fail for namespaced-only registrations")
	}
}

// ---------------------------------------------------------------------------
// ToJSON
// ---------------------------------------------------------------------------

// TestToJSON 验证 ToJSON 生成一个有效的 JSON 数组，描述每个已注册工具的
// namespace、name、full name、description、parameters 与 tags。
func TestToJSON(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{
		namespace:   "core",
		name:        "t1",
		description: "desc1",
		params:      map[string]any{"type": "object"},
		tags:        []string{"readonly"},
	})
	data, err := r.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	var parsed []map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(parsed))
	}
	if parsed[0]["namespace"] != "core" {
		t.Errorf("namespace = %v, want core", parsed[0]["namespace"])
	}
	if parsed[0]["name"] != "t1" {
		t.Errorf("name = %v, want t1", parsed[0]["name"])
	}
	if parsed[0]["full_name"] != "core/t1" {
		t.Errorf("full_name = %v, want core/t1", parsed[0]["full_name"])
	}
	if parsed[0]["description"] != "desc1" {
		t.Errorf("description = %v, want desc1", parsed[0]["description"])
	}
	if parsed[0]["parameters"] == nil {
		t.Errorf("parameters should be present")
	}
	if parsed[0]["tags"] == nil {
		t.Errorf("tags should be present")
	}
}

// TestToJSONEmpty 验证空 registry 序列化为 "[]"（而非 "null"），
// 因为 ToJSON 使用 make(..., 0) 初始化 slice。
func TestToJSONEmpty(t *testing.T) {
	r := NewRegistry()
	data, err := r.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("empty ToJSON = %s, want []", string(data))
	}
}

// ---------------------------------------------------------------------------
// 接口合规性
// ---------------------------------------------------------------------------

// TestBuiltinToolConstructorsImplementsTool 是编译期检查，确保每个内置工具
// 构造器返回的对象都满足 Tool 接口。
func TestBuiltinToolConstructorsImplementsTool(t *testing.T) {
	var _ Tool = NewRunShellTool()
	var _ Tool = NewWriteFileTool()
	var _ Tool = NewReadFileTool()
}

// TestDynamicToolImplementsTool 是编译期检查，确保 DynamicTool 满足 Tool 接口。
func TestDynamicToolImplementsTool(t *testing.T) {
	var _ Tool = NewDynamicTool("d", "desc", nil, DynamicToolInline)
}

// ---------------------------------------------------------------------------
// 并发
// ---------------------------------------------------------------------------

// TestConcurrentReadSafe 验证并发只读操作（List、IsBuiltin、Execute-then-tool-
// returns）不会 panic。只要没有 goroutine 在写，Go map 即可被并发安全读取。
func TestConcurrentReadSafe(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)
	r.Register(&mockTool{name: "custom"})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.List()
			_ = r.IsBuiltin("run_shell")
			_ = r.IsBuiltin("custom")
			// Execute 触发一次 map 查找（读），然后调用工具的 executor，
			// 后者返回一个校验错误，且不会修改 registry。这覆盖了
			// Execute 的读路径。
			_, _ = r.Execute("run_shell", map[string]any{})
		}()
	}
	wg.Wait()
	// 走到这里未 panic 即说明并发读是安全的。
}

// TestDataRaceThroughMutex 验证 Registry 的所有方法在并发调用下对 Go
// race detector 是安全的。这可以防止未来修改中意外移除 sync.RWMutex。
func TestDataRaceThroughMutex(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	var wg sync.WaitGroup
	ops := 200
	for i := 0; i < ops; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("dynamic-%d", idx%10)
			switch idx % 5 {
			case 0:
				r.Register(&mockTool{name: name})
			case 1:
				_ = r.Unregister(name)
			case 2:
				_ = r.List()
			case 3:
				_, _ = r.Execute("run_shell", map[string]any{})
			case 4:
				_ = r.IsBuiltin("run_shell")
			}
		}(i)
	}
	wg.Wait()
	// race detector 会标记出任何围绕 tools map 缺失的 RLock/Lock。
}
