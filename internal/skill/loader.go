package skill

// Loader 负责将内置 Skill 和持久化 Skill 加载到内存注册表。
// 它是 Skill 系统的初始化入口：启动时先注册 builtins，再加载 store 中所有记录。
type Loader struct {
	store    *Store
	registry *Registry
	builtins []*Skill
}

// NewLoader 创建一个 Loader，自动填充 DefaultBuiltins()。
func NewLoader(store *Store, registry *Registry) *Loader {
	return &Loader{
		store:    store,
		registry: registry,
		builtins: DefaultBuiltins(),
	}
}

// LoadAll 将所有内置 Skill 和 store 中的 Skill 注册到注册表。
// 内置 Skill 先注册，持久化 Skill 随后加载；同 ID 的持久化 Skill 会覆盖内置版本。
func (l *Loader) LoadAll() error {
	for _, s := range l.builtins {
		l.registry.Register(*s)
	}

	if l.store == nil {
		return nil
	}
	skills, err := l.store.ListAll()
	if err != nil {
		return err
	}
	for _, s := range skills {
		l.registry.Register(s)
	}
	return nil
}

// Reload 清空注册表中所有非内置 Skill，并重新从 store 加载。
// 内置 Skill 始终保留，避免版本升级后丢失。
func (l *Loader) Reload() error {
	// 先移除所有非内置条目
	for _, s := range l.registry.List(nil) {
		if s.Source != SkillSourceBuiltIn {
			l.registry.Unregister(s.ID)
		}
	}

	if l.store == nil {
		return nil
	}
	skills, err := l.store.ListAll()
	if err != nil {
		return err
	}
	for _, s := range skills {
		l.registry.Register(s)
	}
	return nil
}
