package tool

import (
	"fmt"
	"path/filepath"
	"strings"
)

// validateWorkdirScope 校验 targetWorkdir 是否落在允许的 scope 内。
// 允许的 scope 是 session workspace 目录（由 input["workdir"] 在 Engine
// 注入时提供）或 active worktree 路径（由 ctx.Workdir 提供）。
//
// 参数说明：
//   - targetWorkdir：实际要设置给 cmd.Dir 的目录（已归一化绝对路径）。
//   - ctxWorkdir：ExecuteContext.Workdir 注入值。
//   - input：原始 tool 输入，从中读取 input["workdir"] 作为 session workspace。
//
// 当 ctx.Workdir 为空时本函数不会被调用，以保持旧路径行为不变。
func validateWorkdirScope(targetWorkdir, ctxWorkdir string, input map[string]any) error {
	if targetWorkdir == "" {
		return nil
	}

	target, err := filepath.Abs(targetWorkdir)
	if err != nil {
		return fmt.Errorf("invalid workdir %q: %w", targetWorkdir, err)
	}

	// 允许范围 1：ctx.Workdir（worktree 路径或其子目录）。
	if ctxWorkdir != "" {
		allowed, err := filepath.Abs(ctxWorkdir)
		if err == nil && isSubPath(target, allowed) {
			return nil
		}
	}

	// 允许范围 2：input["workdir"] 指定的 session workspace。
	if sessionWorkdir, ok := input["workdir"].(string); ok && sessionWorkdir != "" {
		allowed, err := filepath.Abs(sessionWorkdir)
		if err == nil && isSubPath(target, allowed) {
			return nil
		}
	}

	return fmt.Errorf("workdir %q is outside allowed scope (session workspace or active worktree)", targetWorkdir)
}

// isSubPath 判断 child 是否等于或在 parent 之下。
// 两个路径都应已 Abs/归一化。使用 strings 前缀比较防止 filepath.Rel
// 对不相关路径返回跨越 ".." 的相对路径。
func isSubPath(child, parent string) bool {
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)
	if child == parent {
		return true
	}
	if !strings.HasPrefix(child, parent) {
		return false
	}
	// 确保 parent 边界是路径分隔符，避免 /foo 误匹配 /foobar。
	next := child[len(parent)]
	return next == filepath.Separator || next == '/'
}
