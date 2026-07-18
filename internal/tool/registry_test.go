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
// mockTool — minimal Tool implementation for Registry tests
// ---------------------------------------------------------------------------

// mockTool is a minimal, deterministic Tool implementation used to exercise the
// Registry without touching the filesystem or shell. It records each Execute
// call and the last input it received so tests can assert routing and input
// pass-through.
type mockTool struct {
	namespace   string
	name        string
	description string
	params      map[string]any
	tags        []string
	aliases     []string
	execFn      func(input map[string]any) (any, error)
	execCalls   int
	lastInput   map[string]any
}

func (m *mockTool) Namespace() string { return m.namespace }
func (m *mockTool) Name() string      { return m.name }

// FullName returns the fully-qualified tool identifier. When namespace is empty
// it returns the short name; otherwise it returns "namespace/name".
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

// Execute records the call and delegates to execFn if set, otherwise returns a
// deterministic "mock-output:<name>" string.
func (m *mockTool) Execute(input map[string]any) (any, error) {
	m.execCalls++
	m.lastInput = input
	if m.execFn != nil {
		return m.execFn(input)
	}
	return "mock-output:" + m.name, nil
}

// Compile-time assertions that mockTool and the built-in tool types satisfy the
// Tool interface.
var (
	_ Tool = (*mockTool)(nil)
	_ Tool = (*BuiltinTool)(nil)
	_ Tool = (*DynamicTool)(nil)
)

// ---------------------------------------------------------------------------
// helpers
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
// NewRegistry / List basics
// ---------------------------------------------------------------------------

// TestNewRegistryEmpty verifies that a fresh Registry has no tools.
func TestNewRegistryEmpty(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if got := r.List(); len(got) != 0 {
		t.Errorf("new registry List = %v, want empty", got)
	}
}

// TestListContainsAllRegistered verifies that List returns every registered tool.
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
// Register + Execute routing
// ---------------------------------------------------------------------------

// TestRegisterAndExecute verifies that a registered custom tool can be invoked
// via Execute, that Execute is called exactly once, and that the input map is
// passed through unchanged.
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

// TestExecuteInputPassthrough uses a capturing execFn to verify the exact input
// map is forwarded to the underlying Tool.
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

// TestExecuteMissingTool verifies that executing an unregistered tool returns an
// error mentioning "tool not found".
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

// TestExecuteErrorPropagation verifies that errors returned by a Tool's Execute
// are propagated unchanged to the caller.
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
// Namespace / Tags metadata
// ---------------------------------------------------------------------------

// TestToolMetadata verifies the new namespace and tags methods on the Tool
// interface.
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
// Register overwrite semantics
// ---------------------------------------------------------------------------

// TestRegisterOverwritesSilently documents that Register has no return value and
// silently overwrites any existing tool with the same name. This is the actual
// behavior per registry.go (Register just assigns into the map).
func TestRegisterOverwritesSilently(t *testing.T) {
	r := NewRegistry()
	first := &mockTool{name: "dup", description: "first"}
	second := &mockTool{name: "dup", description: "second"}
	r.Register(first)
	r.Register(second) // no error possible — Register returns nothing

	// Execute should call the second (overwriting) tool.
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

	// List must contain exactly one entry for "dup" (no duplicates).
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

// TestUnregisterExisting verifies that unregistering a registered custom tool
// succeeds and makes subsequent Execute calls fail.
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

// TestUnregisterMissing verifies that unregistering a tool that was never
// registered returns an error mentioning "tool not found".
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

// TestUnregisterBuiltinRejected verifies that Unregister refuses to remove a
// built-in tool, returning an error that mentions "built-in". This protects
// run_shell, write_file, and read_file from accidental removal via the Registry.
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
			// Tool should still be registered.
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

// TestIsBuiltin is a table-driven test of the IsBuiltin name check. IsBuiltin
// is a pure string switch over "run_shell"/"write_file"/"read_file" and does
// not consult the registry contents.
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
		{"Run_Shell", false},    // case-sensitive
		{"run_shell ", false},   // exact match only (no trimming)
		{" run_shell", false},   // leading space
		{"run_shell", true},     // duplicate to ensure determinism
		{"custom_tool", false},
		{"my_tool", false},
		{"run_shell_v2", false}, // suffix breaks exact match
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

// TestIsBuiltinIndependentOfRegistration verifies that IsBuiltin returns true
// for built-in names even on an empty registry that never registered them.
func TestIsBuiltinIndependentOfRegistration(t *testing.T) {
	r := NewRegistry() // empty
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

// TestRegisterBuiltins verifies that after RegisterBuiltins, all three built-in
// tools are present in the registry (findable via Execute) and listed by List.
func TestRegisterBuiltins(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	wantNames := []string{"run_shell", "write_file", "read_file"}

	// Each built-in should be findable. We invoke Execute with an empty input
	// map: each built-in's executor validates its required string field and
	// returns its own validation error (e.g. "command must be a string"). The
	// key assertion is that the error is NOT "tool not found", which would
	// indicate the tool was never registered.
	for _, name := range wantNames {
		t.Run("Execute_"+name, func(t *testing.T) {
			_, err := r.Execute(name, map[string]any{})
			if err == nil {
				// Some tools might tolerate empty input; that's fine as long as
				// they were found. Only fail on "tool not found".
				return
			}
			if strings.Contains(err.Error(), "tool not found") {
				t.Fatalf("built-in %s not registered: %v", name, err)
			}
		})
	}

	// List must contain all three built-in names.
	names := toolNames(r.List())
	for _, name := range wantNames {
		if !contains(names, name) {
			t.Errorf("List missing built-in %s, got %v", name, names)
		}
	}
}

// TestBuiltinToolsWriteAndReadInTempDir exercises the write_file and read_file
// built-in executors end-to-end inside a temp directory. This confirms the
// registered built-in tools actually work, not just that they are listed.
// run_shell is intentionally not executed here to avoid shell/platform
// dependencies; its registration is covered by TestRegisterBuiltins.
func TestBuiltinToolsWriteAndReadInTempDir(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	dir := t.TempDir()
	path := filepath.Join(dir, "testfile.txt")
	content := "hello world"

	// write_file should create the file and report success.
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

	// read_file should return the same content we wrote.
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
// Filtering / listing by full name
// ---------------------------------------------------------------------------

// TestRegistryFilterByTag verifies FilterByTag returns only tools with the
// requested tag.
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

// TestRegistryListUsesFullName verifies that tools registered with a namespace
// are keyed by their FullName in the registry, so Execute works with the fully
// qualified name.
func TestRegistryListUsesFullName(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{namespace: "core", name: "reader", tags: []string{"readonly"}})
	r.Register(&mockTool{namespace: "ext", name: "reader", tags: []string{"network"}})
	r.Register(&mockTool{name: "plain"})

	// List should contain all three tools.
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(list))
	}

	// Execute must work using full names.
	if _, err := r.Execute("core/reader", map[string]any{}); err != nil {
		t.Errorf("Execute(core/reader): %v", err)
	}
	if _, err := r.Execute("ext/reader", map[string]any{}); err != nil {
		t.Errorf("Execute(ext/reader): %v", err)
	}
	if _, err := r.Execute("plain", map[string]any{}); err != nil {
		t.Errorf("Execute(plain): %v", err)
	}

	// Shorthand "reader" should not match anything because both readers live in
	// namespaces and the plain tool is named "plain".
	if _, err := r.Execute("reader", map[string]any{}); err == nil {
		t.Errorf("Execute(reader) should fail for namespaced-only registrations")
	}
}

// ---------------------------------------------------------------------------
// ToJSON
// ---------------------------------------------------------------------------

// TestToJSON verifies that ToJSON produces a valid JSON array describing each
// registered tool's namespace, name, full name, description, parameters, and tags.
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

// TestToJSONEmpty verifies that an empty registry serializes to "[]" (not "null"),
// since ToJSON initializes the slice with make(..., 0).
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
// Interface compliance
// ---------------------------------------------------------------------------

// TestBuiltinToolConstructorsImplementsTool is a compile-time check that each
// built-in tool constructor returns something that satisfies the Tool interface.
func TestBuiltinToolConstructorsImplementsTool(t *testing.T) {
	var _ Tool = NewRunShellTool()
	var _ Tool = NewWriteFileTool()
	var _ Tool = NewReadFileTool()
}

// TestDynamicToolImplementsTool is a compile-time check that DynamicTool
// satisfies the Tool interface.
func TestDynamicToolImplementsTool(t *testing.T) {
	var _ Tool = NewDynamicTool("d", "desc", nil, DynamicToolInline)
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

// TestConcurrentReadSafe verifies that concurrent read-only operations (List,
// IsBuiltin, Execute-then-tool-returns) do not panic. Go maps are safe for
// concurrent reads as long as no goroutine is writing.
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
			// Execute triggers a map lookup (read) and then calls the tool's
			// executor, which returns a validation error without mutating the
			// registry. This exercises the read path of Execute.
			_, _ = r.Execute("run_shell", map[string]any{})
		}()
	}
	wg.Wait()
	// Reaching here without panic means concurrent reads are safe.
}

// TestDataRaceThroughMutex verifies that all Registry methods are safe under
// the Go race detector when exercised concurrently. This guards against
// accidental removal of the sync.RWMutex in future changes.
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
	// Race detector will flag any missing RLock/Lock around the tools map.
}
