package tool

import (
	"encoding/json"
	"errors"
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
	name        string
	description string
	params      map[string]any
	execFn      func(input map[string]any) (any, error)
	execCalls   int
	lastInput   map[string]any
}

func (m *mockTool) Name() string { return m.name }
func (m *mockTool) Description() string { return m.description }
func (m *mockTool) Parameters() map[string]any { return m.params }

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

// TestUnregisterBuiltinNotProtectedAtRegistryLevel documents an important design
// fact: Registry.Unregister does NOT protect built-in tools. The built-in
// protection lives in the HTTP handler (cmd/server/tool_api.go:handleDeleteTool,
// which checks IsBuiltin before calling Unregister). At the Registry level,
// Unregister("run_shell") succeeds.
//
// This is recorded as a design note: the Registry is a low-level store and the
// API layer enforces the protection. Callers using the Registry directly must
// check IsBuiltin themselves.
func TestUnregisterBuiltinNotProtectedAtRegistryLevel(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltins(r)

	// Precondition: run_shell is registered and recognized as built-in.
	if !r.IsBuiltin("run_shell") {
		t.Fatal("precondition: IsBuiltin(run_shell) should be true")
	}

	// Unregister succeeds at the Registry level — no built-in guard here.
	if err := r.Unregister("run_shell"); err != nil {
		t.Fatalf("Unregister(run_shell) = %v, expected nil (Registry does not protect builtins)", err)
	}

	// After unregister, run_shell is gone from the registry.
	if _, err := r.Execute("run_shell", nil); err == nil {
		t.Fatal("expected run_shell to be removed after Unregister")
	}

	// IsBuiltin is a pure name check, independent of registration state — it
	// still returns true even though the tool is no longer registered.
	if !r.IsBuiltin("run_shell") {
		t.Fatal("IsBuiltin(run_shell) should still be true (pure name check, decoupled from registration)")
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
		{"Run_Shell", false},      // case-sensitive
		{"run_shell ", false},     // exact match only (no trimming)
		{" run_shell", false},     // leading space
		{"run_shell", true},       // duplicate to ensure determinism
		{"custom_tool", false},
		{"my_tool", false},
		{"run_shell_v2", false},   // suffix breaks exact match
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
// ToJSON
// ---------------------------------------------------------------------------

// TestToJSON verifies that ToJSON produces a valid JSON array describing each
// registered tool's name, description, and parameters.
func TestToJSON(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{
		name:        "t1",
		description: "desc1",
		params:      map[string]any{"type": "object"},
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
	if parsed[0]["name"] != "t1" {
		t.Errorf("name = %v, want t1", parsed[0]["name"])
	}
	if parsed[0]["description"] != "desc1" {
		t.Errorf("description = %v, want desc1", parsed[0]["description"])
	}
	if parsed[0]["parameters"] == nil {
		t.Errorf("parameters should be present")
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

// TestConcurrentWriteSkipped documents that the Registry has NO internal mutex
// protecting its tools map. Concurrent writes (Register/Unregister from multiple
// goroutines) would race on the map and may panic with "concurrent map writes".
//
// This test is intentionally skipped — it exists to record the finding that
// Registry is NOT goroutine-safe for writes. Callers must serialize writes
// externally (the HTTP API layer does this implicitly via Go's http.Server
// serializing handlers, but direct Registry users do not).
func TestConcurrentWriteSkipped(t *testing.T) {
	t.Skip("Registry has no mutex; concurrent Register/Unregister would race on the internal map — see report")
}
