package db

import (
	"testing"
)

// TestInsertAndQueryToolsV2 验证 v27 tools 表的写入与读回。
// 覆盖 namespace/version/source/execution_config 等新字段，以及默认值回填。
func TestInsertAndQueryToolsV2(t *testing.T) {
	freshDB(t)

	tr := ToolRecord{
		Namespace:       "test",
		Name:            "hello",
		Version:         "1.0.0",
		Source:          "local_db",
		Description:     "say hello",
		Schema:          map[string]any{"type": "object"},
		ExecutionConfig: map[string]any{"type": "shell", "command": "echo hello"},
		Enabled:         true,
	}
	if err := InsertToolV2(tr); err != nil {
		t.Fatalf("InsertToolV2: %v", err)
	}

	tools, err := QueryToolsV2()
	if err != nil {
		t.Fatalf("QueryToolsV2: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	got := tools[0]
	if got.Namespace != "test" || got.Name != "hello" || got.Version != "1.0.0" {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.Source != "local_db" {
		t.Fatalf("source mismatch: %q", got.Source)
	}
	if got.Schema["type"] != "object" {
		t.Fatalf("schema mismatch: %+v", got.Schema)
	}
	if got.ExecutionConfig["command"] != "echo hello" {
		t.Fatalf("execution_config mismatch: %+v", got.ExecutionConfig)
	}
	if !got.Enabled {
		t.Fatalf("enabled mismatch: %v", got.Enabled)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
}

// TestInsertToolV2Defaults 验证缺省 version/source 时回填默认值。
func TestInsertToolV2Defaults(t *testing.T) {
	freshDB(t)

	if err := InsertToolV2(ToolRecord{
		Name:    "plain",
		Enabled: true,
	}); err != nil {
		t.Fatalf("InsertToolV2: %v", err)
	}

	got, err := GetToolV2("", "plain", "")
	if err != nil {
		t.Fatalf("GetToolV2: %v", err)
	}
	if got.Version != "1.0.0" {
		t.Fatalf("default version: want 1.0.0, got %q", got.Version)
	}
	if got.Source != "local_db" {
		t.Fatalf("default source: want local_db, got %q", got.Source)
	}
}

// TestUpdateAndDeleteToolV2 验证按复合主键更新与删除。
func TestUpdateAndDeleteToolV2(t *testing.T) {
	freshDB(t)

	if err := InsertToolV2(ToolRecord{
		Namespace:   "ns",
		Name:        "t",
		Version:     "1.0.0",
		Description: "old",
		Enabled:     true,
	}); err != nil {
		t.Fatalf("InsertToolV2: %v", err)
	}

	if err := UpdateToolV2(ToolRecord{
		Namespace:   "ns",
		Name:        "t",
		Version:     "1.0.0",
		Description: "new",
		Enabled:     false,
	}); err != nil {
		t.Fatalf("UpdateToolV2: %v", err)
	}

	got, err := GetToolV2("ns", "t", "1.0.0")
	if err != nil {
		t.Fatalf("GetToolV2 after update: %v", err)
	}
	if got.Description != "new" {
		t.Fatalf("description not updated: %q", got.Description)
	}
	if got.Enabled {
		t.Fatalf("enabled not updated: %v", got.Enabled)
	}

	if err := DeleteToolV2("ns", "t", "1.0.0"); err != nil {
		t.Fatalf("DeleteToolV2: %v", err)
	}
	if _, err := GetToolV2("ns", "t", "1.0.0"); err == nil {
		t.Fatal("expected error after delete, got nil")
	}
}

// TestInsertToolLegacyWrapper 验证旧 InsertTool wrapper 以 local_db 来源、
// 默认版本写入，QueryTools wrapper 能读回。
func TestInsertToolLegacyWrapper(t *testing.T) {
	freshDB(t)

	if err := InsertTool("legacy", "legacy tool", map[string]any{"type": "object"}, true); err != nil {
		t.Fatalf("InsertTool: %v", err)
	}

	tools, err := QueryTools()
	if err != nil {
		t.Fatalf("QueryTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "legacy" || tools[0].Source != "local_db" || tools[0].Version != "1.0.0" {
		t.Fatalf("unexpected legacy record: %+v", tools[0])
	}
}

// TestDeleteToolLegacyWrapper 验证旧 DeleteTool(name) 按 name 删除全部版本。
func TestDeleteToolLegacyWrapper(t *testing.T) {
	freshDB(t)

	// 同名不同版本两条记录。
	if err := InsertToolV2(ToolRecord{Name: "multi", Version: "1.0.0", Enabled: true}); err != nil {
		t.Fatalf("InsertToolV2 v1: %v", err)
	}
	if err := InsertToolV2(ToolRecord{Name: "multi", Version: "2.0.0", Enabled: true}); err != nil {
		t.Fatalf("InsertToolV2 v2: %v", err)
	}

	if err := DeleteTool("multi"); err != nil {
		t.Fatalf("DeleteTool: %v", err)
	}
	tools, err := QueryTools()
	if err != nil {
		t.Fatalf("QueryTools: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools after legacy delete, got %d", len(tools))
	}
}

// TestToolV27MigrationIndexes 验证 v27 创建的索引存在。
func TestToolV27MigrationIndexes(t *testing.T) {
	freshDB(t)

	for _, idx := range []string{"idx_tools_source", "idx_tools_enabled"} {
		var n string
		err := DB.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&n)
		if err != nil {
			t.Fatalf("index %q missing: %v", idx, err)
		}
		if n != idx {
			t.Fatalf("index name mismatch: want %q, got %q", idx, n)
		}
	}
}
