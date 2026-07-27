package skill

import (
	"path/filepath"
	"strings"
	"sync"
)

type Registry struct {
	mu     sync.RWMutex
	skills map[string]Skill
}

func NewRegistry() *Registry {
	return &Registry{skills: make(map[string]Skill)}
}

func (r *Registry) Register(s Skill) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.ID] = s
	return s.ID
}

func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.skills, id)
}

func (r *Registry) Get(id string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[id]
	return s, ok
}

func (r *Registry) List(source *SkillSource) []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []Skill
	for _, s := range r.skills {
		if source != nil && s.Source != *source {
			continue
		}
		result = append(result, s)
	}
	return result
}

func (r *Registry) Exists(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.skills[id]
	return ok
}

func (r *Registry) UpdateState(id string, state SkillState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.skills[id]
	if !ok {
		return false
	}
	s.State = state
	r.skills[id] = s
	return true
}

// ResolveActiveSkills 返回在指定 project/workdir 下应被注入 Engine 的启用 skill ID 列表。
//
// 规则：
//   - global scope 的 skill 始终入选。
//   - project scope 的 skill 在 projectID 匹配，或 workspaceDir 是其子目录时入选。
//   - session scope 本次预留，暂不注入。
//   - 同 ID 去重：project 覆盖 global，session 覆盖 project；按 scope 优先级保留。
//   - 仅返回 State == enabled 的 skill。
func ResolveActiveSkills(registry *Registry, projectID, workspaceDir string) []string {
	if registry == nil {
		return nil
	}
	all := registry.List(nil)

	// 按 ID 分组并保留最高优先级 scope 的 skill。
	// priority: session=3, project=2, global=1
	picked := make(map[string]Skill)
	for _, s := range all {
		if s.State != SkillStateEnabled {
			continue
		}
		if !skillMatchesScope(s, projectID, workspaceDir) {
			continue
		}
		cur, exists := picked[s.ID]
		if !exists || scopePriority(s.Scope) > scopePriority(cur.Scope) {
			picked[s.ID] = s
		}
	}

	var ids []string
	for _, s := range picked {
		ids = append(ids, s.ID)
	}
	return ids
}

// skillMatchesScope 判断单个 skill 在指定 projectID/workspaceDir 下是否可见。
func skillMatchesScope(s Skill, projectID, workspaceDir string) bool {
	switch s.Scope {
	case SkillScopeGlobal, "":
		return true
	case SkillScopeProject:
		return projectID != "" && s.ProjectID != "" && s.ProjectID == projectID ||
			workspaceDir != "" && s.WorkspaceDir != "" && isSubDirOrEqual(workspaceDir, s.WorkspaceDir)
	case SkillScopeSession:
		// 本次预留，不注入。
		return false
	default:
		return false
	}
}

// scopePriority 返回 scope 的优先级数字，数字越大优先级越高。
func scopePriority(scope SkillScope) int {
	switch scope {
	case SkillScopeSession:
		return 3
	case SkillScopeProject:
		return 2
	case SkillScopeGlobal, "":
		return 1
	default:
		return 0
	}
}

// isSubDirOrEqual 判断 child 是否等于 parent 或其子目录（路径前缀匹配）。
func isSubDirOrEqual(child, parent string) bool {
	if child == "" || parent == "" {
		return false
	}
	child = filepath.Clean(child)
	parent = filepath.Clean(parent)
	if child == parent {
		return true
	}
	prefix := parent
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(child+string(filepath.Separator), prefix)
}
