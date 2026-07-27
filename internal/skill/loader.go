package skill

import (
	"time"
)

// Loader 负责将内置 Skill、持久化 Skill 与文件系统 Skill 加载到内存注册表。
// 它是 Skill 系统的初始化入口：启动时先注册 builtins，再加载 store 中记录，
// 最后扫描文件系统（全局 + 项目级）以发现本地 SKILL.md。
type Loader struct {
	store      *Store
	registry   *Registry
	builtins   []*Skill
	fileLoader *FileLoader
	globalDir  string
}

// NewLoader 创建一个 Loader，自动填充 DefaultBuiltins()。
// fileLoader 在 LoadAll 之前通过 SetFileLoader 注入；未注入时文件扫描不执行。
func NewLoader(store *Store, registry *Registry) *Loader {
	return &Loader{
		store:    store,
		registry: registry,
		builtins: DefaultBuiltins(),
	}
}

// SetFileLoader 注入文件系统扫描器与全局扫描基准目录。
// globalDir 通常是 server 进程的当前工作目录。
func (l *Loader) SetFileLoader(fl *FileLoader, globalDir string) {
	l.fileLoader = fl
	l.globalDir = globalDir
}

// LoadAll 将所有内置 Skill、store 中的 Skill 与全局文件系统 Skill 注册到注册表。
// 内置 Skill 先注册，持久化 Skill 随后加载，文件系统 Skill 最后加载；
// 同 ID 时来源权威顺序为：local_db > local_file > built_in。
func (l *Loader) LoadAll() error {
	for _, s := range l.builtins {
		l.registry.Register(*s)
	}

	if l.store != nil {
		skills, err := l.store.ListAll()
		if err != nil {
			return err
		}
		for _, s := range skills {
			if s.Scope == "" {
				s.Scope = SkillScopeGlobal
			}
			l.registry.Register(s)
		}
	}

	if l.fileLoader != nil && l.globalDir != "" {
		if err := l.fileLoader.LoadGlobal(l.globalDir); err != nil {
			return err
		}
	}

	return nil
}

// LoadForWorkdir 为指定 session workdir 加载文件系统 Skill。
// 先卸载 source=local_file 且 WorkspaceDir 匹配的旧 skill，避免重复或残留。
func (l *Loader) LoadForWorkdir(workdir, projectID string) error {
	if l.fileLoader == nil || workdir == "" {
		return nil
	}

	// 清理该 workdir 下旧的 local_file skill，保证删除/重命名后重扫不会残留。
	for _, s := range l.registry.List(nil) {
		if s.Source == SkillSourceLocalFile && s.WorkspaceDir == workdir {
			l.registry.Unregister(s.ID)
		}
	}

	return l.fileLoader.LoadForWorkdir(workdir, projectID)
}

// RefreshAll 全量刷新文件系统 Skill：卸载所有 local_file，再重扫全局 + 所有已知 workdirs。
// workdirProjectIDs 把 workdir 映射到 project_id；缺失时传空 map。
func (l *Loader) RefreshAll(workdirs []string, workdirProjectIDs map[string]string) error {
	if l.fileLoader == nil {
		return nil
	}
	return l.fileLoader.RefreshAll(l.globalDir, workdirs, workdirProjectIDs)
}

// Reload 清空注册表中所有非内置 Skill，并重新从 store 与全局文件系统加载。
// 内置 Skill 始终保留，避免版本升级后丢失。
func (l *Loader) Reload() error {
	// 先移除所有非内置条目
	for _, s := range l.registry.List(nil) {
		if s.Source != SkillSourceBuiltIn {
			l.registry.Unregister(s.ID)
		}
	}

	if l.store != nil {
		skills, err := l.store.ListAll()
		if err != nil {
			return err
		}
		for _, s := range skills {
			if s.Scope == "" {
				s.Scope = SkillScopeGlobal
			}
			l.registry.Register(s)
		}
	}

	if l.fileLoader != nil && l.globalDir != "" {
		if err := l.fileLoader.LoadGlobal(l.globalDir); err != nil {
			return err
		}
	}

	return nil
}

// WaitForDBRetry 是 Loader 内部重试等待时间（未使用，保留以兼容旧调用）。
var WaitForDBRetry = 100 * time.Millisecond

// Registry 返回 loader 持有的 registry；若 loader 为 nil 则返回 nil。
// 外部 handler 可用它做只读统计，不破坏封装。
func (l *Loader) Registry() *Registry {
	if l == nil {
		return nil
	}
	return l.registry
}

// GlobalDir 返回 loader 配置的全局扫描基准目录。
func (l *Loader) GlobalDir() string {
	if l == nil {
		return ""
	}
	return l.globalDir
}
