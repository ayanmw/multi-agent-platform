package skill

// Skill 生命周期与运行时事件常量。
// 这些事件用于在事件总线上广播 Skill 的发现、加载、启用、禁用、渲染等状态变化，
// 使前端和观测系统能够实时追踪 Skill 的行为。
const (
	// EventSkillDiscovered 表示发现了新的 Skill（例如文件扫描、MCP 推送）。
	EventSkillDiscovered = "skill_discovered"

	// EventSkillValidated 表示 Skill 已通过结构校验，模板和参数完整。
	EventSkillValidated = "skill_validated"

	// EventSkillValidationFailed 表示 Skill 校验失败，通常伴随 reason 字段。
	EventSkillValidationFailed = "skill_validation_failed"

	// EventSkillLoaded 表示 Skill 已加载到内存注册表。
	EventSkillLoaded = "skill_loaded"

	// EventSkillLoadFailed 表示 Skill 加载失败。
	EventSkillLoadFailed = "skill_load_failed"

	// EventSkillEnabled 表示 Skill 已被启用，可被运行时调用。
	EventSkillEnabled = "skill_enabled"

	// EventSkillDisabled 表示 Skill 已被禁用。
	EventSkillDisabled = "skill_disabled"

	// EventSkillUnloaded 表示 Skill 已从内存注册表中移除。
	EventSkillUnloaded = "skill_unloaded"

	// EventSkillRendered 表示 Skill 模板已被渲染（调试用，白盒追踪）。
	EventSkillRendered = "skill_rendered"

	// EventSkillSuggested 表示系统根据上下文向用户推荐了候选 Skill。
	EventSkillSuggested = "skill_suggested"

	// EventSkillChanged 表示 Skill 的元数据或状态发生了变化。
	EventSkillChanged = "skill_changed"
)
