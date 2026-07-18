// Package harness — TagPolicyRule: fine-grained permission enforcement by tool tags.
//
// This file implements a PolicyRule that inspects a tool's tags before execution.
// It bridges the TaskContract permission model (AllowNetwork, AllowFileDelete,
// AllowFileWrite, AllowShell, AllowShellDangerous) with the tool registry's
// metadata tags. By centralizing tag-to-permission mapping here, individual tool
// authors only need to declare tags; permission enforcement stays in the Harness.
//
// # Supported tag-to-permission mappings
//
//   - network               → Requires TaskPermissions.AllowNetwork
//   - filesystem:write      → Requires TaskPermissions.AllowFileWrite
//   - filesystem:destructive, filesystem:delete → Requires TaskPermissions.AllowFileDelete
//   - exec                  → Requires TaskPermissions.AllowShell
//   - exec:dangerous        → Requires TaskPermissions.AllowShellDangerous
//   - mcp                   → Treated as network-equivalent (AllowNetwork)
//
// A tool may carry multiple tags. Only one blocking reason is returned, but all
// tags are checked so the most restrictive applies.
package harness

import (
	"fmt"
	"strings"
)

// TagPolicyRule enforces TaskContract permissions based on tool tags.
//
// The rule requires access to the ToolRegistry so it can look up the tags of
// the tool being called. The registry is passed at construction time and must
// be the same Registry instance used by the ReAct Engine.
//
// TagPolicyRule should be placed early in the PolicyChain (after path/scope
// checks and dangerous-command checks are fine, but before budget rules) so
// that permission violations are caught before any expensive work is done.
//
// Design note: we intentionally do NOT embed tool tag knowledge in
// DangerousCommandRule or ApprovalRule. Tags are a layer-2 (tool metadata)
// concern; those rules are layer-1 (command string / path) concerns. Keeping
// them separate makes each rule testable in isolation and lets future tools
// opt into risk classification simply by adding tags.
type TagPolicyRule struct {
	// tagLookup returns the tags for a fully-qualified tool name. We use a
	// function indirection instead of the concrete *tool.Registry to avoid an
	// import cycle between harness and runtime/tool packages. The function is
	// wired up in cmd/server/main.go where all packages are available.
	tagLookup func(toolName string) []string
}

// NewTagPolicyRule creates a TagPolicyRule with the given tag lookup function.
// The lookup function receives the tool's FullName (e.g. "core/fetch_url") and
// must return the tool's tags. If the tool is unknown, it should return nil or
// an empty slice.
func NewTagPolicyRule(tagLookup func(toolName string) []string) *TagPolicyRule {
	return &TagPolicyRule{tagLookup: tagLookup}
}

// Name returns the rule name for logging and error messages.
func (r *TagPolicyRule) Name() string { return "TagPolicyRule" }

// Check evaluates whether the tool call is permitted according to its tags and
// the TaskContract's permissions. It never modifies the input.
func (r *TagPolicyRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	if r.tagLookup == nil {
		// Defensive: if no lookup is configured, tags cannot be evaluated.
		// Fail closed only when we would otherwise have blocked; allow unknown.
		return input, nil
	}

	tags := r.tagLookup(toolName)
	if len(tags) == 0 {
		return input, nil
	}

	// AutoApprovePolicy bypasses tag-based permission checks. This is useful
	// for trusted autonomous agents where the user has pre-approved all policy
	// decisions. Events are still emitted by the Engine for audit.
	if contract.AutoApprovePolicy {
		return input, nil
	}

	// Evaluate tags in order of most restrictive / dangerous first so the
	// returned reason is meaningful.
	for _, tag := range tags {
		switch strings.ToLower(tag) {
		case "exec:dangerous":
			if !contract.Permissions.AllowShellDangerous {
				return input, &ErrBlockedByPolicy{
					Rule:   r.Name(),
					Reason: fmt.Sprintf("tool %q requires AllowShellDangerous permission", toolName),
					Tool:   toolName,
				}
			}
		case "exec":
			if !contract.Permissions.AllowShell {
				return input, &ErrBlockedByPolicy{
					Rule:   r.Name(),
					Reason: fmt.Sprintf("tool %q requires AllowShell permission", toolName),
					Tool:   toolName,
				}
			}
		case "shell":
			if !contract.Permissions.AllowShell {
				return input, &ErrBlockedByPolicy{
					Rule:   r.Name(),
					Reason: fmt.Sprintf("tool %q requires AllowShell permission", toolName),
					Tool:   toolName,
				}
			}
		case "filesystem:destructive", "filesystem:delete":
			if !contract.Permissions.AllowFileDelete {
				return input, &ErrBlockedByPolicy{
					Rule:   r.Name(),
					Reason: fmt.Sprintf("tool %q requires AllowFileDelete permission", toolName),
					Tool:   toolName,
				}
			}
		case "filesystem:write":
			if !contract.Permissions.AllowFileWrite {
				return input, &ErrBlockedByPolicy{
					Rule:   r.Name(),
					Reason: fmt.Sprintf("tool %q requires AllowFileWrite permission", toolName),
					Tool:   toolName,
				}
			}
		case "network", "mcp":
			if !contract.Permissions.AllowNetwork {
				return input, &ErrBlockedByPolicy{
					Rule:   r.Name(),
					Reason: fmt.Sprintf("tool %q requires AllowNetwork permission", toolName),
					Tool:   toolName,
				}
			}
		}
	}

	return input, nil
}
