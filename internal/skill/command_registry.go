package skill

import (
	"path/filepath"
	"strings"
	"sync"
)

// CommandRegistry 是内存中的 SkillCommand 注册表，按 workdir 隔离项目级命令。
type CommandRegistry struct {
	mu   sync.RWMutex
	byID map[string]*SkillCommand
}

// NewCommandRegistry 创建 CommandRegistry。
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{byID: make(map[string]*SkillCommand)}
}

// Register 注册一个 command。
func (r *CommandRegistry) Register(cmd SkillCommand) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[cmd.ID] = &cmd
	return cmd.ID
}

// Unregister 按 ID 注销 command。
func (r *CommandRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
}

// Get 按 ID 获取 command。
func (r *CommandRegistry) Get(id string) (SkillCommand, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cmd, ok := r.byID[id]
	if !ok || cmd == nil {
		return SkillCommand{}, false
	}
	return *cmd, true
}

// List 返回所有 command，可选过滤 workdir。
func (r *CommandRegistry) List(workdir string) []SkillCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []SkillCommand
	for _, cmd := range r.byID {
		if workdir != "" && cmd.WorkspaceDir != workdir {
			continue
		}
		result = append(result, *cmd)
	}
	return result
}

// ListForWorkdir 返回与指定 workdir 匹配的 project scope commands，以及所有 global commands。
func (r *CommandRegistry) ListForWorkdir(workdir string) []SkillCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []SkillCommand
	for _, cmd := range r.byID {
		if cmd.Scope == SkillCommandScopeGlobal {
			result = append(result, *cmd)
			continue
		}
		if workdir != "" && cmd.WorkspaceDir != "" && isSubDirOrEqual(workdir, cmd.WorkspaceDir) {
			result = append(result, *cmd)
		}
	}
	return result
}

// Clear 清空所有 commands。
func (r *CommandRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID = make(map[string]*SkillCommand)
}

// Exists 判断 ID 是否存在。
func (r *CommandRegistry) Exists(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.byID[id]
	return ok
}

// isValidCommandID 检查命令 ID 是否只包含允许字符，避免路径遍历或特殊字符。
func isValidCommandID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == ':' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// commandScopeFromWorkdir 根据 workdir 和 projectID 判断 scope。
func commandScopeFromWorkdir(workdir, projectID string) SkillCommandScope {
	if projectID == "" && workdir == "" {
		return SkillCommandScopeGlobal
	}
	return SkillCommandScopeProject
}

// commandWorkdirPrefix 返回 workdir 前缀路径（统一分隔符）。
func commandWorkdirPrefix(workdir string) string {
	if workdir == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.ToSlash(filepath.Clean(workdir)), "/") + "/"
}
