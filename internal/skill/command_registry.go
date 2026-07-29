package skill

import (
	"strings"
	"sync"
)

// CommandRegistry 是内存中的 SkillCommand 注册表，按 (scope, workdir) 命名空间隔离。
//
// 同名命令隔离（review H4）：全局 `.claude/commands/greet.md` 产生 ID=greet(scope=global)，
// 项目 `/proj/.claude/commands/greet.md` 也产生 ID=greet(scope=project)。若用单维 byID map，
// 后注册者会覆盖前者导致 global 版本永久丢失。这里用复合 key `global:<id>` /
// `project:<workdir>:<id>` 存储，对外仍按裸 ID 暴露（Get/List），但内部隔离。
type CommandRegistry struct {
	mu   sync.RWMutex
	byID map[string]*SkillCommand // key = namespacedKey(cmd)
}

// NewCommandRegistry 创建 CommandRegistry。
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{byID: make(map[string]*SkillCommand)}
}

// commandNamespaceKey 生成内部存储 key：global scope 用 `global:<id>`，
// project scope 用 `project:<workdir>:<id>`，保证同名命令不互相覆盖。
func commandNamespaceKey(cmd SkillCommand) string {
	if cmd.Scope == SkillCommandScopeProject && cmd.WorkspaceDir != "" {
		return "project:" + cmd.WorkspaceDir + ":" + cmd.ID
	}
	return "global:" + cmd.ID
}

// Register 注册一个 command；同名命令按 scope+workdir 命名空间隔离，不互相覆盖。
// 返回对外裸 ID（cmd.ID）。
func (r *CommandRegistry) Register(cmd SkillCommand) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[commandNamespaceKey(cmd)] = &cmd
	return cmd.ID
}

// Unregister 按 (scope, workdir, id) 注销 command。
// 若只传入 id（scope/workdir 为空），按 global 命名空间注销以保持向后兼容。
func (r *CommandRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 向后兼容：旧调用只传裸 id，默认按 global 命名空间注销。
	delete(r.byID, "global:"+id)
}

// UnregisterScoped 按 scope+workdir 命名空间精确注销。
func (r *CommandRegistry) UnregisterScoped(cmd SkillCommand) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, commandNamespaceKey(cmd))
}

// Get 按 裸 ID 获取 command；若多个命名空间存在同名，优先返回 project scope，
// 其次 global（与 ListForWorkdir 的"project 覆盖 global"语义一致）。
func (r *CommandRegistry) Get(id string) (SkillCommand, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// 优先 project scope：扫描所有 project:<wd>:<id> 命名空间。
	for _, cmd := range r.byID {
		if cmd == nil {
			continue
		}
		if cmd.Scope == SkillCommandScopeProject && cmd.ID == id {
			return *cmd, true
		}
	}
	if cmd, ok := r.byID["global:"+id]; ok && cmd != nil {
		return *cmd, true
	}
	return SkillCommand{}, false
}

// List 返回所有 command，可选按精确 workdir 过滤（仅 project scope 命中）。
// workdir 为空时返回全部（含所有命名空间）。
func (r *CommandRegistry) List(workdir string) []SkillCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []SkillCommand
	for _, cmd := range r.byID {
		if cmd == nil {
			continue
		}
		if workdir != "" && cmd.WorkspaceDir != workdir {
			continue
		}
		result = append(result, *cmd)
	}
	return result
}

// ListForWorkdir 返回与指定 workdir 匹配的 project scope commands，以及所有 global commands。
// 同名命令同时存在 global 与匹配 project 时，project 优先（覆盖 global），与 Get 语义一致。
func (r *CommandRegistry) ListForWorkdir(workdir string) []SkillCommand {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// 先按裸 ID 收集，project 覆盖 global（同名时）。
	byBareID := make(map[string]SkillCommand)
	for _, cmd := range r.byID {
		if cmd == nil {
			continue
		}
		switch cmd.Scope {
		case SkillCommandScopeGlobal:
			// 仅当尚无 project 版本覆盖时纳入 global。
			if cur, exists := byBareID[cmd.ID]; !exists || cur.Scope != SkillCommandScopeProject {
				byBareID[cmd.ID] = *cmd
			}
		case SkillCommandScopeProject:
			if workdir != "" && cmd.WorkspaceDir != "" && isSubDirOrEqual(workdir, cmd.WorkspaceDir) {
				// project 始终覆盖 global（同名时）。
				byBareID[cmd.ID] = *cmd
			}
		}
	}
	result := make([]SkillCommand, 0, len(byBareID))
	for _, cmd := range byBareID {
		result = append(result, cmd)
	}
	return result
}

// Clear 清空所有 commands。
func (r *CommandRegistry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID = make(map[string]*SkillCommand)
}

// Exists 判断 裸 ID 是否存在（任一命名空间）。
func (r *CommandRegistry) Exists(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, cmd := range r.byID {
		if cmd != nil && cmd.ID == id {
			return true
		}
	}
	return false
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
	return strings.TrimSuffix(toSlashClean(workdir), "/") + "/"
}

// toSlashClean 归一化 workdir：反斜杠转正斜杠并去除末尾分隔符。
// 与 filepath.ToSlash(filepath.Clean(workdir)) 等价的轻量实现，避免本文件 import path/filepath。
func toSlashClean(workdir string) string {
	cleaned := strings.ReplaceAll(workdir, "\\", "/")
	return strings.TrimSuffix(cleaned, "/")
}
