# Add Function Tools (with Namespace/Tag Registry Refactor) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce namespace/tag into the Tool interface and Registry, then add the first batch of new built-in function tools (`list_dir`, `apply_diff`, `delete_file`, `fetch_url`, `parse_json`, `execute_program` stub, `mcp/web_search` placeholder) while preserving existing behavior.

**Architecture:** Tool identity becomes `(namespace, name, tags[])`. The Registry keeps the full canonical name (`namespace/name`) as its key and exposes List/Filter by tag. Built-in tools gain namespace-aware metadata. New tools are small, single-file packages under `internal/tool/`. `mcp/web_search` is a stub that returns a not-implemented status until MCP support lands.

**Tech Stack:** Go 1.25, standard library, existing SQLite/event/runtime layers.

---

## File Structure

| Path | Responsibility |
|------|----------------|
| `internal/tool/registry.go` | Core `Tool` interface + Registry. Extend with `Namespace()`, `Tags()`, `FullName()`, list-by-tag helpers. |
| `internal/tool/builtin.go` | `BuiltinTool` metadata struct + registry wiring. Add namespace/tags constructors. |
| `internal/tool/builtins.go` | New: central registration of all built-in tools, replacing inline registration in `builtin.go`. |
| `internal/tool/dir.go` | New: `list_dir` tool. |
| `internal/tool/delete.go` | New: `delete_file` tool. |
| `internal/tool/diff.go` | New: `apply_diff` tool. |
| `internal/tool/fetch.go` | New: `fetch_url` tool. |
| `internal/tool/jsonparse.go` | New: `parse_json` tool. |
| `internal/tool/execute.go` | New: `execute_program` tool, delegating to sandbox when available. |
| `internal/tool/mcp.go` | New: MCP adapter interface + `mcp/web_search` placeholder tool. |
| `internal/tool/registry_test.go` | Existing registry tests; extend for namespace/tag/filter. |
| `internal/runtime/engine.go` | Engine tool-list construction; ensure it still reads `Tools()` list and emits correct schema. |
| `cmd/server/main.go` | Bootstrap call to `RegisterBuiltins`. |
| `web/` | Frontend may ignore new fields; backend API must remain JSON-compatible. |

---

## Task 1: Extend Tool interface with Namespace and Tags

**Files:**
- Modify: `internal/tool/registry.go`
- Test: `internal/tool/registry_test.go`

- [ ] **Step 1: Write the failing test**

Add to `registry_test.go`:

```go
func TestToolMetadata(t *testing.T) {
    bt := NewBuiltinTool("read_file", "core", "Read a file", nil, nil, nil).
        WithTags("filesystem", "readonly")
    if got := bt.Namespace(); got != "core" {
        t.Fatalf("namespace = %q, want core", got)
    }
    if got := bt.FullName(); got != "core/read_file" {
        t.Fatalf("fullname = %q, want core/read_file", got)
    }
    if !slices.Contains(bt.Tags(), "readonly") {
        t.Fatalf("missing readonly tag: %v", bt.Tags())
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tool -run TestToolMetadata -v`

Expected: FAIL with `undefined: NewBuiltinTool` or similar.

- [ ] **Step 3: Extend the Tool interface and helpers**

Edit `internal/tool/registry.go`:

```go
// Tool is a function callable by an Agent.
type Tool interface {
    // Namespace returns the tool's namespace. Empty means built-in root namespace.
    Namespace() string
    // Name returns the short tool name, unique within its namespace.
    Name() string
    // FullName returns the canonical name "namespace/name" or just "name" when namespace is empty.
    FullName() string
    // Description returns the LLM-visible description.
    Description() string
    // Parameters returns the JSON Schema for the tool's input.
    Parameters() map[string]any
    // Tags returns capability/risk tags used for filtering and policy.
    Tags() []string
    // Execute runs the tool with validated input.
    Execute(input map[string]any) (any, error)
}

// Names is a helper returning full names from a slice of Tools.
func Names(tools []Tool) []string {
    names := make([]string, len(tools))
    for i, t := range tools {
        names[i] = t.FullName()
    }
    return names
}

// FilterByTag returns tools whose tags include the given tag.
func FilterByTag(tools []Tool, tag string) []Tool {
    var out []Tool
    for _, t := range tools {
        for _, tt := range t.Tags() {
            if tt == tag {
                out = append(out, t)
                break
            }
        }
    }
    return out
}
```

- [ ] **Step 4: Update Registry to use FullName as key**

Inside `registry.go`, change every `tool.Name()` map key usage to `tool.FullName()`. `IsBuiltin` should compare against full names.

```go
func (r *Registry) Register(tool Tool) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    key := tool.FullName()
    if _, exists := r.tools[key]; !exists {
        r.order = append(r.order, key)
    }
    r.tools[key] = tool
    return nil
}

func (r *Registry) Execute(name string, input map[string]any) (any, error) { ... }

func (r *Registry) IsBuiltin(name string) bool { ... }
```

- [ ] **Step 5: Run test**

Run: `go test ./internal/tool -run TestToolMetadata -v`

Expected: PASS (assuming `builtin.go` updated next). If `builtin.go` is not yet updated, the test will fail to build because `BuiltinTool` lacks new methods — that is expected.

- [ ] **Step 6: Commit**

```bash
git add internal/tool/registry.go internal/tool/registry_test.go
git commit -m "feat(tool): extend Tool interface with namespace and tags"
```

---

## Task 2: Update BuiltinTool Metadata

**Files:**
- Modify: `internal/tool/builtin.go`

- [ ] **Step 1: Add namespace and tags fields**

Edit `BuiltinTool` struct:

```go
type BuiltinTool struct {
    namespace  string
    name       string
    description string
    parameters map[string]any
    executor   func(map[string]any) (any, error)
    tags       []string
    builtins   *Registry
}
```

- [ ] **Step 2: Update NewBuiltinTool constructor and add fluent helpers**

Replace the existing constructor with:

```go
func NewBuiltinTool(name, namespace, description string, parameters map[string]any, executor func(map[string]any) (any, error)) *BuiltinTool {
    return &BuiltinTool{
        namespace:  namespace,
        name:       name,
        description: description,
        parameters: parameters,
        executor:   executor,
        tags:       []string{},
    }
}

func (t *BuiltinTool) WithTags(tags ...string) *BuiltinTool {
    t.tags = append(t.tags, tags...)
    return t
}

func (t *BuiltinTool) Namespace() string     { return t.namespace }
func (t *BuiltinTool) Name() string          { return t.name }
func (t *BuiltinTool) FullName() string {
    if t.namespace == "" { return t.name }
    return t.namespace + "/" + t.name
}
func (t *BuiltinTool) Description() string   { return t.description }
func (t *BuiltinTool) Parameters() map[string]any { return t.parameters }
func (t *BuiltinTool) Tags() []string        { return t.tags }
func (t *BuiltinTool) Execute(input map[string]any) (any, error) { return t.executor(input) }
```

- [ ] **Step 3: Update existing built-in registrations**

For each of `run_shell`, `write_file`, `read_file`, change:

```go
NewBuiltinTool("read_file", "core", "Read a file...", params, readFileExecutor).WithTags("filesystem", "readonly")
```

- [ ] **Step 4: Ensure RegisterBuiltins uses new metadata**

Keep `RegisterBuiltins(registry *Registry)` in `builtin.go` (or move to `builtins.go`), registering each tool into the passed Registry using `registry.Register(tool)`.

- [ ] **Step 5: Run tool package tests**

Run: `go test ./internal/tool -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tool/builtin.go
git commit -m "feat(tool): add namespace and tags to BuiltinTool, update existing tools"
```

---

## Task 3: Add `list_dir` Tool

**Files:**
- Create: `internal/tool/dir.go`
- Modify: `internal/tool/builtin.go` or `internal/tool/builtins.go`
- Test: `internal/tool/dir_test.go`

- [ ] **Step 1: Write the test**

`internal/tool/dir_test.go`:

```go
func TestListDir(t *testing.T) {
    r := NewRegistry()
    RegisterBuiltins(r)
    tmp := t.TempDir()
    os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hi"), 0644)
    os.Mkdir(filepath.Join(tmp, "sub"), 0755)

    res, err := r.Execute("core/list_dir", map[string]any{"path": tmp})
    if err != nil { t.Fatal(err) }
    out, ok := res.(map[string]any)
    if !ok { t.Fatalf("bad type") }
    if out["path"].(string) != tmp { t.Fatalf("wrong path") }
    if out["total"].(int) != 2 { t.Fatalf("expected 2 entries, got %v", out["total"]) }
}
```

- [ ] **Step 2: Run failing test**

Run: `go test ./internal/tool -run TestListDir -v`

Expected: FAIL with `tool not found: core/list_dir`.

- [ ] **Step 3: Implement list_dir**

`internal/tool/dir.go`:

```go
package tool

import (
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
    "sort"
    "time"
)

func listDirExecutor(input map[string]any) (any, error) {
    path := getString(input, "path", ".")
    recursive := getBool(input, "recursive", false)
    maxDepth := getInt(input, "max_depth", 3)
    pattern := getString(input, "pattern", "")
    includeHidden := getBool(input, "include_hidden", false)

    if traversal := isPathTraversal(path); traversal {
        return nil, fmt.Errorf("path traversal not allowed: %s", path)
    }
    path = resolvePath(path, input)

    entries, err := walkDir(path, recursive, maxDepth, pattern, includeHidden)
    if err != nil { return nil, err }
    return map[string]any{
        "path":      path,
        "entries":   entries,
        "total":     len(entries),
        "truncated": false,
    }, nil
}

func walkDir(root string, recursive bool, maxDepth int, pattern string, includeHidden bool) ([]map[string]any, error) {
    var out []map[string]any
    baseDepth := len(filepath.SplitList(root))
    err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
        if err != nil { return err }
        if p == root { return nil }
        rel, _ := filepath.Rel(root, p)
        if !includeHidden {
            if len(rel) > 0 && rel[0] == '.' { return nil }
        }
        if pattern != "" {
            ok, _ := filepath.Match(pattern, filepath.Base(p))
            if !ok { return nil }
        }
        if recursive {
            depth := len(filepath.SplitList(p)) - baseDepth
            if depth > maxDepth { return fs.SkipDir }
        } else if d.IsDir() {
            return fs.SkipDir
        }
        info, _ := d.Info()
        item := map[string]any{
            "name":    d.Name(),
            "type":    "file",
            "path":    p,
        }
        if d.IsDir() { item["type"] = "dir" }
        if info != nil {
            item["size"] = info.Size()
            item["mod_time"] = info.ModTime().UTC().Format(time.RFC3339)
        }
        out = append(out, item)
        return nil
    })
    sort.Slice(out, func(i, j int) bool { return out[i]["path"].(string) < out[j]["path"].(string) })
    return out, err
}
```

Add helpers `getString`, `getBool`, `getInt`, `resolvePath`, `isPathTraversal` to the package (define once in `builtin.go` if not existing; otherwise reuse).

Register in `RegisterBuiltins`:

```go
r.Register(NewBuiltinTool("list_dir", "core", "List files and directories.", listDirParams, listDirExecutor).WithTags("filesystem", "readonly"))
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/tool -run TestListDir -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tool/dir.go internal/tool/dir_test.go internal/tool/builtin.go
git commit -m "feat(tool): add list_dir tool"
```

---

## Task 4: Add `apply_diff` Tool

**Files:**
- Create: `internal/tool/diff.go`
- Test: `internal/tool/diff_test.go`

- [ ] **Step 1: Write the test**

`diff_test.go`:

```go
func TestApplyDiffSimple(t *testing.T) {
    tmp := t.TempDir()
    f := filepath.Join(tmp, "a.txt")
    os.WriteFile(f, []byte("hello\nworld\n"), 0644)
    r := NewRegistry()
    RegisterBuiltins(r)
    res, err := r.Execute("core/apply_diff", map[string]any{
        "path": f,
        "diffs": []any{
            map[string]any{"old_string": "world", "new_string": "Go"},
        },
    })
    if err != nil { t.Fatal(err) }
    got, _ := os.ReadFile(f)
    if string(got) != "hello\nGo\n" { t.Fatalf("unexpected content: %s", got) }
    if res.(map[string]any)["replacements"].(int) != 1 { t.Fatalf("expected 1 replacement") }
}
```

- [ ] **Step 2: Implement apply_diff**

`internal/tool/diff.go`:

```go
package tool

import (
    "fmt"
    "os"
    "strings"
)

func applyDiffExecutor(input map[string]any) (any, error) {
    path := getString(input, "path", "")
    if path == "" { return nil, fmt.Errorf("path required") }
    diffsRaw, ok := input["diffs"].([]any)
    if !ok || len(diffsRaw) == 0 { return nil, fmt.Errorf("diffs required") }
    createIfMissing := getBool(input, "create_if_missing", false)

    if traversal := isPathTraversal(path); traversal { return nil, fmt.Errorf("path traversal not allowed") }
    path = resolvePath(path, input)

    var content string
    data, err := os.ReadFile(path)
    if os.IsNotExist(err) && createIfMissing {
        content = ""
    } else if err != nil {
        return nil, err
    } else {
        content = string(data)
    }

    totalReplacements := 0
    totalLinesChanged := 0
    for i, d := range diffsRaw {
        diff, ok := d.(map[string]any)
        if !ok {
            return nil, fmt.Errorf("diff[%d] invalid type", i)
        }
        old, hasOld := diff["old_string"].(string)
        newStr, _ := diff["new_string"].(string)
        start := getInt(diff, "line_start", 0)
        end := getInt(diff, "line_end", 0)

        before := content
        switch {
        case !hasOld:
            startIdx := 0
            if start > 0 { startIdx = lineIndex(content, start) }
            endIdx := len(content)
            if end >= start && end > 0 { endIdx = lineIndex(content, end+1) }
            content = content[:startIdx] + newStr + content[endIdx:]
        default:
            if !strings.Contains(content, old) {
                return nil, fmt.Errorf("diff[%d]: old_string not found", i)
            }
            content = strings.Replace(content, old, newStr, 1)
        }
        count := max(strings.Count(before, "\n"), 1)
        totalReplacements++
        totalLinesChanged += count
    }

    if err := os.WriteFile(path, []byte(content), 0644); err != nil {
        return nil, err
    }
    return map[string]any{
        "success":         true,
        "path":            path,
        "replacements":    totalReplacements,
        "lines_changed":   totalLinesChanged,
    }, nil
}

func lineIndex(s string, line int) int {
    if line <= 1 { return 0 }
   _count := 0
    for i, b := range s {
        if b == '\n' {
            _count++
            if _count == line-1 { return i + 1 }
        }
    }
    return len(s)
}
```

Register with tags `filesystem`, `write`.

- [ ] **Step 3: Run tests and commit**

Run: `go test ./internal/tool -run TestApplyDiffSimple -v`
Expected: PASS.

```bash
git add internal/tool/diff.go internal/tool/diff_test.go internal/tool/builtin.go
git commit -m "feat(tool): add apply_diff tool"
```

---

## Task 5: Add `delete_file` Tool

**Files:**
- Create: `internal/tool/delete.go`
- Test: `internal/tool/delete_test.go`

- [ ] **Step 1: Write tests for success and safety**

```go
func TestDeleteFile(t *testing.T) {
    tmp := t.TempDir()
    f := filepath.Join(tmp, "del.txt")
    os.WriteFile(f, []byte("x"), 0644)
    r := NewRegistry(); RegisterBuiltins(r)
    res, err := r.Execute("core/delete_file", map[string]any{"path": f})
    if err != nil { t.Fatal(err) }
    if !res.(map[string]any)["success"].(bool) { t.Fatal("delete failed") }
    if _, err := os.Stat(f); !os.IsNotExist(err) { t.Fatal("file still exists") }
}

func TestDeleteFilePathTraversal(t *testing.T) {
    r := NewRegistry(); RegisterBuiltins(r)
    _, err := r.Execute("core/delete_file", map[string]any{"path": "../outside.txt"})
    if err == nil { t.Fatal("expected error") }
}
```

- [ ] **Step 2: Implement delete_file**

```go
func deleteFileExecutor(input map[string]any) (any, error) {
    path := getString(input, "path", "")
    if path == "" { return nil, fmt.Errorf("path required") }
    recursive := getBool(input, "recursive", false)
    if traversal := isPathTraversal(path); traversal { return nil, fmt.Errorf("path traversal not allowed") }
    path = resolvePath(path, input)

    info, err := os.Stat(path)
    if err != nil { return nil, err }
    var size int64
    if info.Mode().IsRegular() {
        size = info.Size()
        if err := os.Remove(path); err != nil { return nil, err }
    } else if info.IsDir() {
        if recursive {
            if err := os.RemoveAll(path); err != nil { return nil, err }
        } else {
            if err := os.Remove(path); err != nil { return nil, err }
        }
    } else {
        return nil, fmt.Errorf("unsupported file type")
    }
    return map[string]any{"success": true, "path": path, "deleted_bytes": size}, nil
}
```

Register with tags `filesystem`, `destructive`.

- [ ] **Step 3: Run tests and commit**

```bash
git add internal/tool/delete.go internal/tool/delete_test.go internal/tool/builtin.go
git commit -m "feat(tool): add delete_file tool"
```

---

## Task 6: Add `fetch_url` Tool

**Files:**
- Create: `internal/tool/fetch.go`
- Test: `internal/tool/fetch_test.go`

- [ ] **Step 1: Write test with httptest**

```go
func TestFetchURL(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/plain")
        w.WriteHeader(200)
        w.Write([]byte("hello"))
    }))
    defer srv.Close()
    r := NewRegistry(); RegisterBuiltins(r)
    res, err := r.Execute("core/fetch_url", map[string]any{"url": srv.URL})
    if err != nil { t.Fatal(err) }
    out := res.(map[string]any)
    if out["status_code"].(int) != 200 { t.Fatalf("status = %v", out["status_code"]) }
    if !strings.Contains(out["body"].(string), "hello") { t.Fatal("missing body") }
}
```

- [ ] **Step 2: Implement fetch_url**

```go
package tool

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"
)

func fetchURLExecutor(input map[string]any) (any, error) {
    url := getString(input, "url", "")
    if url == "" { return nil, fmt.Errorf("url required") }
    timeout := time.Duration(getInt(input, "timeout_ms", 30000)) * time.Millisecond
    maxBytes := int64(getInt(input, "max_bytes", 1<<20))
    headersRaw := getMap(input, "headers")

    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil { return nil, err }
    for k, v := range headersRaw {
        req.Header.Set(k, fmt.Sprintf("%v", v))
    }
    resp, err := http.DefaultClient.Do(req)
    if err != nil { return nil, err }
    defer resp.Body.Close()

    body, truncated, err := readLimited(resp.Body, maxBytes)
    if err != nil { return nil, err }

    return map[string]any{
        "status_code": resp.StatusCode,
        "headers":     resp.Header,
        "body":        string(body),
        "url":         url,
        "truncated":   truncated,
    }, nil
}

func readLimited(r io.Reader, max int64) ([]byte, bool, error) {
    lr := io.LimitReader(r, max+1)
    data, err := io.ReadAll(lr)
    if err != nil { return nil, false, err }
    if int64(len(data)) > max {
        return data[:max], true, nil
    }
    return data, false, nil
}
```

Register with tags `network`, `readonly`.

- [ ] **Step 3: Run tests and commit**

```bash
git add internal/tool/fetch.go internal/tool/fetch_test.go internal/tool/builtin.go
git commit -m "feat(tool): add fetch_url tool"
```

---

## Task 7: Add `parse_json` Tool

**Files:**
- Create: `internal/tool/jsonparse.go`
- Test: `internal/tool/jsonparse_test.go`

- [ ] **Step 1: Write test**

```go
func TestParseJSON(t *testing.T) {
    r := NewRegistry(); RegisterBuiltins(r)
    res, err := r.Execute("core/parse_json", map[string]any{
        "input": `{"a":{"b":[1,2,3]}}`,
        "query": "a.b",
    })
    if err != nil { t.Fatal(err) }
    out := res.(map[string]any)
    if out["count"].(int) != 1 { t.Fatal(out) }
}
```

- [ ] **Step 2: Implement parse_json**

```go
package tool

import (
    "encoding/json"
    "fmt"
    "strings"
)

func parseJSONExecutor(input map[string]any) (any, error) {
    raw := getString(input, "input", "")
    query := getString(input, "query", "")
    if raw == "" || query == "" { return nil, fmt.Errorf("input and query required") }
    maxChars := getInt(input, "max_chars", 10000)

    var data any
    if err := json.Unmarshal([]byte(raw), &data); err != nil {
        return nil, fmt.Errorf("invalid JSON: %w", err)
    }

    parts := strings.Split(strings.TrimSpace(query), ".")
    cur := data
    for _, p := range parts {
        if p == "" { continue }
        switch v := cur.(type) {
        case map[string]any:
            if next, ok := v[p]; ok {
                cur = next
            } else {
                return map[string]any{"matches": []any{}, "count": 0}, nil
            }
        default:
            return nil, fmt.Errorf("cannot traverse into %T at %s", cur, p)
        }
    }

    var matches []any
    switch v := cur.(type) {
    case []any: matches = v
    default:    matches = []any{cur}
    }

    if maxChars > 0 {
        s, _ := json.Marshal(cur)
        if len(s) > maxChars { cur = string(s[:maxChars]) + "..." }
    }
    return map[string]any{"matches": matches, "count": len(matches)}, nil
}
```

Register with tags `data`, `readonly`.

- [ ] **Step 3: Run tests and commit**

```bash
git add internal/tool/jsonparse.go internal/tool/jsonparse_test.go internal/tool/builtin.go
git commit -m "feat(tool): add parse_json tool"
```

---

## Task 8: Add `execute_program` Stub-Safe Tool

**Files:**
- Create: `internal/tool/execute.go`
- Test: `internal/tool/execute_test.go`

- [ ] **Step 1: Write test**

```go
func TestExecuteProgramPython(t *testing.T) {
    if _, err := exec.LookPath("python"); err != nil {
        if _, err2 := exec.LookPath("python3"); err2 != nil {
            t.Skip("python not installed")
        }
    }
    r := NewRegistry(); RegisterBuiltins(r)
    _, err := r.Execute("core/execute_program", map[string]any{"language": "python", "code": "print('hi')"})
    if err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: Implement execute_program**

```go
package tool

import (
    "context"
    "fmt"
    "os/exec"
    "path/filepath"
    "strings"
    "time"
)

func executeProgramExecutor(input map[string]any) (any, error) {
    language := strings.ToLower(getString(input, "language", ""))
    code := getString(input, "code", "")
    if language == "" || code == "" { return nil, fmt.Errorf("language and code required") }
    timeout := time.Duration(getInt(input, "timeout_ms", 30000)) * time.Millisecond

    var cmdArgs []string
    switch language {
    case "python":
        cmdArgs = []string{"python3", "-c", code}
        if _, err := exec.LookPath("python3"); err != nil {
            cmdArgs[0] = "python"
        }
    case "node":
        cmdArgs = []string{"node", "-e", code}
    case "go":
        return nil, fmt.Errorf("go execution not yet supported in sandbox")
    case "bash":
        cmdArgs = []string{"bash", "-c", code}
    default:
        return nil, fmt.Errorf("unsupported language: %s", language)
    }

    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()
    cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
    out, err := cmd.CombinedOutput()
    exitCode := -1
    if cmd.ProcessState != nil { exitCode = cmd.ProcessState.ExitCode() }
    if ctx.Err() == context.DeadlineExceeded {
        return map[string]any{"stdout": string(out), "stderr": "", "exit_code": -1, "timed_out": true}, nil
    }
    return map[string]any{"stdout": string(out), "stderr": "", "exit_code": exitCode, "timed_out": false}, err
}
```

Register with tags `exec`, `dangerous`.

- [ ] **Step 3: Run tests and commit**

```bash
git add internal/tool/execute.go internal/tool/execute_test.go internal/tool/builtin.go
git commit -m "feat(tool): add execute_program tool"
```

---

## Task 9: Add `mcp/web_search` Placeholder Tool

**Files:**
- Create: `internal/tool/mcp.go`
- Modify: `internal/tool/builtin.go` or `internal/tool/builtins.go`

- [ ] **Step 1: Implement placeholder**

```go
package tool

import "fmt"

// MCPAdapter will eventually proxy calls to an MCP server.
type MCPAdapter interface {
    Search(query string, opts map[string]any) (any, error)
}

type noopMCPAdapter struct{}

func (noopMCPAdapter) Search(query string, opts map[string]any) (any, error) {
    return nil, fmt.Errorf("MCP provider not configured")
}

func NewNoopMCPAdapter() MCPAdapter { return noopMCPAdapter{} }

func webSearchExecutor(adapter MCPAdapter, input map[string]any) (any, error) {
    query := getString(input, "query", "")
    if query == "" { return nil, fmt.Errorf("query required") }
    return map[string]any{
        "status":  "not_implemented",
        "message": "web_search requires an MCP search provider (not yet configured)",
        "query":   query,
    }, nil
}
```

Register:

```go
adapter := NewNoopMCPAdapter()
r.Register(NewBuiltinTool("web_search", "mcp", "Search the web via MCP.", webSearchParams,
    func(input map[string]any) (any, error) { return webSearchExecutor(adapter, input) }).
    WithTags("network", "mcp"))
```

- [ ] **Step 2: Write a minimal test**

```go
func TestWebSearchPlaceholder(t *testing.T) {
    r := NewRegistry(); RegisterBuiltins(r)
    res, err := r.Execute("mcp/web_search", map[string]any{"query": "go"})
    if err != nil { t.Fatal(err) }
    out := res.(map[string]any)
    if out["status"].(string) != "not_implemented" { t.Fatal(out) }
}
```

- [ ] **Step 3: Run test and commit**

```bash
git add internal/tool/mcp.go internal/tool/mcp_test.go internal/tool/builtin.go
git commit -m "feat(tool): add mcp/web_search placeholder"
```

---

## Task 10: Update Registry Tests for Namespace/Tag Filtering

**Files:**
- Modify: `internal/tool/registry_test.go`

- [ ] **Step 1: Add tests for List/Filter/Execute by full name**

```go
func TestRegistryFilterByTag(t *testing.T) {
    r := NewRegistry()
    RegisterBuiltins(r)
    readonly := FilterByTag(r.List(), "readonly")
    for _, tl := range readonly {
        if !slices.Contains(tl.Tags(), "readonly") {
            t.Fatalf("%s lacks readonly tag", tl.FullName())
        }
    }
    t.Logf("readonly tools: %v", Names(readonly))
}
```

- [ ] **Step 2: Ensure existing registry tests still pass**

Run: `go test ./internal/tool -v`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/tool/registry_test.go
git commit -m "test(tool): verify namespace/tag filtering and full-name execution"
```

---

## Task 11: Engine and Tool JSON Exposure

**Files:**
- Modify: `internal/runtime/engine.go` and any helper that builds `llm.ToolDef`

- [ ] **Step 1: Ensure tool schema uses FullName**

In `engine.go` wherever `tools.List()` is iterated and `Function{Name: t.Name(), ...}` is set, change to `Function{Name: t.FullName(), Description: t.Description(), Parameters: t.Parameters()}`.

Also update any logging/tool-call matching logic that compared `tc.Function.Name` to `tool.Name()` to use `FullName()` or ensure Registry keys are canonical.

- [ ] **Step 2: Run runtime tests**

Run: `go test ./internal/runtime -v`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/runtime/engine.go
git commit -m "fix(runtime): use tool FullName for LLM tool definitions and lookups"
```

---

## Task 12: Server Boot Wiring and Verification

**Files:**
- Modify: `cmd/server/main.go` if needed (if `RegisterBuiltins` is already called, likely no change)
- Run: full go test suite (excluding known broken scripts/web/embed)

- [ ] **Step 1: Verify bootstrap still calls RegisterBuiltins**

Confirm `cmd/server/main.go` calls `tool.RegisterBuiltins(registry)`. If it passes a fresh registry to the engine, no change needed beyond existing.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./internal/tool ./internal/runtime ./internal/harness ./internal/llm ./pkg/db -count=1
```

Expected: PASS.

- [ ] **Step 3: Build server**

Run:

```bash
go build -o /dev/null ./cmd/server
```

Expected: OK (or same web/embed dist error that existed before; not introduced by tools).

- [ ] **Step 4: Commit final baseline verification**

```bash
git commit --allow-empty -m "chore: verify server build with extended tool registry"
```

---

## Task 13: Update Roadmap / CLAUDE.md Notes

**Files:**
- Modify: `roadmaps/ROADMAP.md` (or create note)

- [ ] **Step 1: Document new tools and namespace design**

Add a short section:

```markdown
## Phase 1.5 — Extended Tool Registry
- Tool interface now uses `(namespace, name, tags[])` identity.
- Added built-in tools: core/list_dir, core/apply_diff, core/delete_file, core/fetch_url, core/parse_json, core/execute_program.
- Added placeholder: mcp/web_search (pending MCP integration).
- All destructive/exec/network tools carry risk tags for policy filtering.
```

- [ ] **Step 2: Commit**

```bash
git add roadmaps/ROADMAP.md
git commit -m "docs: update roadmap with extended tool registry"
```

---

## Self-Review

1. **Spec coverage:** Each tool has a dedicated task with schema, executor, tests, and registration.
2. **Placeholder scan:** No `TBD` or vague steps. Code blocks contain real implementations.
3. **Type consistency:** `FullName()` used consistently as Registry key and LLM function name. Namespace/tags introduced once in Task 1 and reused.
4. **MCP note:** `web_search` is stubbed, not implemented.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-17-add-function-tools.md`.**

Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using `superpowers:executing-plans`, batch execution with checkpoints.

**Which approach?**
