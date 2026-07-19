package harness

import (
	"testing"
)

// fakeTagLookup 基于一个小的硬编码 map 返回 tag。它模拟 tool.Registry.GetTags 的行为，
// 而不引入 tool package。
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

	// 无网络权限 → 拦截
	_, err := rule.Check(toolName, map[string]any{}, TaskContract{
		Permissions: TaskPermissions{},
	})
	if !isPolicyBlock(err) {
		t.Fatalf("expected block without AllowNetwork, got %v", err)
	}

	// 有网络权限 → 放行
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

	// 即使有文件写权限，破坏性操作仍需删除权限。
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

	// 基础 shell 权限对 exec:dangerous tool 不足。
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

	// 只读 tool 无需任何权限。
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
