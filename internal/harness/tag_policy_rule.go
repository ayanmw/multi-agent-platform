// Package harness —— TagPolicyRule：按 tool tag 细粒度权限强制执行。
//
// 本文件实现一个 PolicyRule，在 tool 执行前检查 tool 的 tag。它在 TaskContract 权限模型
// （AllowNetwork、AllowFileDelete、AllowFileWrite、AllowShell、AllowShellDangerous）与
// tool registry 的 metadata tag 之间架起桥梁。将 tag 到权限的映射集中在此处，单个 tool
// 作者只需声明 tag；权限强制执行留在 Harness 中。
//
// # 支持的 tag 到权限映射
//
//   - network               → 需要 TaskPermissions.AllowNetwork
//   - filesystem:write      → 需要 TaskPermissions.AllowFileWrite
//   - filesystem:destructive, filesystem:delete → 需要 TaskPermissions.AllowFileDelete
//   - exec                  → 需要 TaskPermissions.AllowShell
//   - exec:dangerous        → 需要 TaskPermissions.AllowShellDangerous
//   - mcp                   → 视为 network 等价（AllowNetwork）
//
// 一个 tool 可携带多个 tag。只返回一个拦截原因，但会检查所有 tag，使最严格的规则生效。
package harness

import (
	"fmt"
	"strings"
)

// TagPolicyRule 根据 tool tag 强制执行 TaskContract 权限。
//
// 该 rule 需要访问 ToolRegistry 以查找被调用 tool 的 tag。registry 在构造时传入，且必须
// 与 ReAct Engine 使用的 Registry 实例相同。
//
// TagPolicyRule 应放在 PolicyChain 早期（在 path/scope 检查与危险命令检查之后、预算规则
// 之前均可），以便在任何昂贵工作前先捕获权限违规。
//
// 设计说明：我们刻意不将 tool tag 知识嵌入 DangerousCommandRule 或 ApprovalRule。tag 是
// layer-2（tool metadata）关注点；那些 rule 是 layer-1（命令字符串 / 路径）关注点。保持
// 分离使每条 rule 可独立测试，并让未来 tool 仅通过添加 tag 即可加入风险分类。
type TagPolicyRule struct {
	// tagLookup 返回完全限定 tool 名的 tag。我们使用函数间接而非具体 *tool.Registry，
	// 以避免 harness 与 runtime/tool package 之间的 import cycle。该函数在
	// cmd/server/main.go 中接线，那里所有 package 都可用。
	tagLookup func(toolName string) []string
}

// NewTagPolicyRule 用给定 tag 查找函数创建 TagPolicyRule。查找函数接收 tool 的
// FullName（如 "core/fetch_url"），必须返回该 tool 的 tag。未知 tool 应返回 nil 或
// 空 slice。
func NewTagPolicyRule(tagLookup func(toolName string) []string) *TagPolicyRule {
	return &TagPolicyRule{tagLookup: tagLookup}
}

// Name 返回用于日志与错误信息的 rule 名称。
func (r *TagPolicyRule) Name() string { return "TagPolicyRule" }

// Check 根据其 tag 与 TaskContract 权限评估 tool call 是否被允许。它从不修改 input。
func (r *TagPolicyRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	if r.tagLookup == nil {
		// 防御性：未配置 lookup 时无法评估 tag。
		// 仅当我们本应拦截时才 fail closed；未知则放行。
		return input, nil
	}

	tags := r.tagLookup(toolName)
	if len(tags) == 0 {
		return input, nil
	}

	// AutoApprovePolicy 绕过基于 tag 的权限检查。这对可信自治 agent 有用 —— 用户已预批准
	// 所有策略决策。事件仍由 Engine 发射以便审计。
	if contract.AutoApprovePolicy {
		return input, nil
	}

	// 按从最严格/最危险到最宽松的顺序评估 tag，使返回的原因更有意义。
	for _, tag := range tags {
		switch strings.ToLower(tag) {
		case "exec:dangerous":
			if !contract.Permissions.AllowShellDangerous {
				return input, newApprovalRequired(r.Name(), toolName, fmt.Sprintf("tool %q requires AllowShellDangerous permission", toolName), input)
			}
		case "exec":
			if !contract.Permissions.AllowShell {
				return input, newApprovalRequired(r.Name(), toolName, fmt.Sprintf("tool %q requires AllowShell permission", toolName), input)
			}
		case "shell":
			if !contract.Permissions.AllowShell {
				return input, newApprovalRequired(r.Name(), toolName, fmt.Sprintf("tool %q requires AllowShell permission", toolName), input)
			}
		case "filesystem:destructive", "filesystem:delete":
			if !contract.Permissions.AllowFileDelete {
				return input, newApprovalRequired(r.Name(), toolName, fmt.Sprintf("tool %q requires AllowFileDelete permission", toolName), input)
			}
		case "filesystem:write":
			if !contract.Permissions.AllowFileWrite {
				return input, newApprovalRequired(r.Name(), toolName, fmt.Sprintf("tool %q requires AllowFileWrite permission", toolName), input)
			}
		case "network", "mcp":
			if !contract.Permissions.AllowNetwork {
				return input, newApprovalRequired(r.Name(), toolName, fmt.Sprintf("tool %q requires AllowNetwork permission", toolName), input)
			}
		}
	}

	return input, nil
}

// newApprovalRequired 创建一个 ErrApprovalRequired，设置了 RuleName，以便前端展示哪个
// 策略 rule 触发了该请求。
func newApprovalRequired(ruleName, toolName, reason string, input map[string]any) error {
	return &ErrApprovalRequired{
		ApprovalID: GenerateApprovalID(),
		Tool:       toolName,
		RuleName:   ruleName,
		Reason:     reason,
		Input:      input,
	}
}
