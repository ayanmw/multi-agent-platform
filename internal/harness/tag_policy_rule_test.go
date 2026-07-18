package harness

import (
	"testing"
)

// fakeTagLookup returns tags based on a tiny hard-coded map. It simulates the
// behavior of tool.Registry.GetTags without importing the tool package.
func fakeTagLookup(toolName string) []string {
	switch toolName {
	case "core/read_file", "core/list_dir", "filesystem:readonly":
		return []string{"filesystem", "readonly"}
	case "core/write_file", "core/apply_diff":
		return []string{"filesystem", "filesystem:write"}
	case "core/delete_file":
		return []string{"filesystem", "filesystem:destructive"}
	case "core/fetch_url":
		return []string{"network", "readonly"}
	case "core/execute_program":
		return []string{"exec", "exec:dangerous"}
	case "mcp/web_search":
		return []string{"network", "mcp"}
	case "run_shell":
		return []string{"shell", "exec", "exec:dangerous"}
	}
	return nil
}

func TestTagPolicyRuleBlocksByTag(t *testing.T) {
	const toolName = "core/fetch_url"
	rule := NewTagPolicyRule(fakeTagLookup)

	// No network permission → blocked
	_, err := rule.Check(toolName, map[string]any{}, TaskContract{
		Permissions: TaskPermissions{},
	})
	if !isPolicyBlock(err) {
		t.Fatalf("expected block without AllowNetwork, got %v", err)
	}

	// With network permission → allowed
	_, err = rule.Check(toolName, map[string]any{}, TaskContract{
		Permissions: TaskPermissions{AllowNetwork: true},
	})
	if err != nil {
		t.Fatalf("expected allow with AllowNetwork, got %v", err)
	}
}

func TestTagPolicyRuleFileWrite(t *testing.T) {
	rule := NewTagPolicyRule(fakeTagLookup)

	_, err := rule.Check("core/write_file", map[string]any{}, TaskContract{
		Permissions: TaskPermissions{AllowNetwork: true},
	})
	if !isPolicyBlock(err) {
		t.Fatalf("expected block without AllowFileWrite, got %v", err)
	}

	_, err = rule.Check("core/write_file", map[string]any{}, TaskContract{
		Permissions: TaskPermissions{AllowFileWrite: true},
	})
	if err != nil {
		t.Fatalf("expected allow with AllowFileWrite, got %v", err)
	}
}

func TestTagPolicyRuleFileDelete(t *testing.T) {
	rule := NewTagPolicyRule(fakeTagLookup)

	// Even with file write permission, destructive operations require delete.
	_, err := rule.Check("core/delete_file", map[string]any{}, TaskContract{
		Permissions: TaskPermissions{AllowFileWrite: true},
	})
	if !isPolicyBlock(err) {
		t.Fatalf("expected block without AllowFileDelete, got %v", err)
	}

	_, err = rule.Check("core/delete_file", map[string]any{}, TaskContract{
		Permissions: TaskPermissions{AllowFileDelete: true},
	})
	if err != nil {
		t.Fatalf("expected allow with AllowFileDelete, got %v", err)
	}
}

func TestTagPolicyRuleExecDangerous(t *testing.T) {
	rule := NewTagPolicyRule(fakeTagLookup)

	// Basic shell permission is not enough for exec:dangerous tools.
	_, err := rule.Check("core/execute_program", map[string]any{}, TaskContract{
		Permissions: TaskPermissions{AllowShell: true},
	})
	if !isPolicyBlock(err) {
		t.Fatalf("expected block without AllowShellDangerous, got %v", err)
	}

	_, err = rule.Check("core/execute_program", map[string]any{}, TaskContract{
		Permissions: TaskPermissions{AllowShell: true, AllowShellDangerous: true},
	})
	if err != nil {
		t.Fatalf("expected allow with AllowShellDangerous, got %v", err)
	}
}

func TestTagPolicyRuleReadonly(t *testing.T) {
	rule := NewTagPolicyRule(fakeTagLookup)

	// Readonly tools do not require any permissions.
	_, err := rule.Check("core/read_file", map[string]any{}, TaskContract{
		Permissions: TaskPermissions{},
	})
	if err != nil {
		t.Fatalf("expected readonly tool to pass, got %v", err)
	}
}

func TestTagPolicyRuleUnknownTool(t *testing.T) {
	rule := NewTagPolicyRule(fakeTagLookup)

	_, err := rule.Check("unknown/tool", map[string]any{}, TaskContract{})
	if err != nil {
		t.Fatalf("unknown untagged tool should pass, got %v", err)
	}
}

func TestTagPolicyRuleNilLookup(t *testing.T) {
	rule := NewTagPolicyRule(nil)

	_, err := rule.Check("core/fetch_url", map[string]any{}, TaskContract{})
	if err != nil {
		t.Fatalf("nil lookup should fail open when no rule applies, got %v", err)
	}
}

func TestTagPolicyRuleAutoApprove(t *testing.T) {
	rule := NewTagPolicyRule(fakeTagLookup)

	_, err := rule.Check("core/delete_file", map[string]any{}, TaskContract{
		Permissions:       TaskPermissions{},
		AutoApprovePolicy: true,
	})
	if err != nil {
		t.Fatalf("AutoApprovePolicy should bypass tag policy, got %v", err)
	}
}

func TestTagPolicyRuleMCPRequiresNetwork(t *testing.T) {
	rule := NewTagPolicyRule(fakeTagLookup)

	_, err := rule.Check("mcp/web_search", map[string]any{}, TaskContract{
		Permissions: TaskPermissions{},
	})
	if !isPolicyBlock(err) {
		t.Fatalf("expected MCP tool to require AllowNetwork, got %v", err)
	}
}
