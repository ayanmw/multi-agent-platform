// Package skill 定义 Skill 系统的核心类型。
//
// Skill 是可复用的 Agent 能力单元，包含模板、参数、触发器、工具依赖和元数据。
// 它支持多种来源：内置（built_in）、本地文件（local_file）、本地数据库（local_db）、
// 集市（market）以及 MCP 服务器（mcp）。状态机在 discovered → validated → loaded →
// enabled / disabled / invalid 之间流转，由管理器在加载和启用阶段维护。
package skill

// SkillSource 表示 Skill 的来源类型。
// 来源决定了 Skill 是否可本地编辑、如何更新以及由谁负责验证。
type SkillSource string

const (
	// SkillSourceBuiltIn 是平台内置 Skill，随版本发布，不可本地修改。
	SkillSourceBuiltIn SkillSource = "built_in"
	// SkillSourceLocalFile 是从本地文件系统（如 ~/.claude/skills/）加载的 Skill。
	SkillSourceLocalFile SkillSource = "local_file"
	// SkillSourceLocalDB 是用户通过平台 UI 或 API 在数据库中创建/编辑的 Skill。
	SkillSourceLocalDB SkillSource = "local_db"
	// SkillSourceMarket 是从 Skill 集市下载的 Skill。
	SkillSourceMarket SkillSource = "market"
	// SkillSourceMCP 是通过 MCP 服务器动态发现的 Skill。
	SkillSourceMCP SkillSource = "mcp"
)

// SkillState 表示 Skill 在生命周期中的状态。
type SkillState string

const (
	// SkillStateDiscovered 表示 Skill 刚被发现，尚未经过验证。
	SkillStateDiscovered SkillState = "discovered"
	// SkillStateValidated 表示 Skill 已通过结构校验（模板、参数完整）。
	SkillStateValidated SkillState = "validated"
	// SkillStateLoaded 表示 Skill 已加载到内存注册表，但尚未启用。
	SkillStateLoaded SkillState = "loaded"
	// SkillStateEnabled 表示 Skill 已启用，可被 Agent 在运行时使用。
	SkillStateEnabled SkillState = "enabled"
	// SkillStateDisabled 表示 Skill 已被显式禁用。
	SkillStateDisabled SkillState = "disabled"
	// SkillStateInvalid 表示 Skill 校验失败，InvalidReason 字段记录原因。
	SkillStateInvalid SkillState = "invalid"
)

// Skill 是平台可调用的可复用能力单元。
// 它既作为数据库持久化模型，也作为运行时 Skill 注册表的内存对象。
type Skill struct {
	// ID 是 Skill 的全局唯一标识，为了人类可读，通常使用 "author/name" 或文件名形式。
	ID string `json:"id"`
	// Version 是 Skill 语义化版本，如 "1.0.0"。
	Version string `json:"version"`
	// DisplayName 是展示给用户的友好名称。
	DisplayName string `json:"display_name"`
	// Description 是 Skill 的简短描述，用于列表和搜索展示。
	Description string `json:"description"`
	// Authors 是作者列表。
	Authors []string `json:"authors"`
	// Tags 是用于分类和检索的标签。
	Tags []string `json:"tags"`
	// Source 表示 Skill 来源。
	Source SkillSource `json:"source"`
	// SourceURL 是来源地址，如本地路径、集市 URL 或 MCP 服务器名。
	SourceURL string `json:"source_url"`
	// IsLocalEditable 表示是否允许在本地编辑（built_in/market 一般为 false）。
	IsLocalEditable bool `json:"is_local_editable"`
	// Templates 是 Skill 包含的 prompt 模板列表。
	Templates []SkillTemplate `json:"templates"`
	// Parameters 是 Skill 接受的参数定义。
	Parameters []SkillParameter `json:"parameters"`
	// RequiredTools 是运行该 Skill 必须存在的工具名列表。
	RequiredTools []string `json:"required_tools"`
	// SuggestedTools 是运行该 Skill 建议配套使用的工具名列表。
	SuggestedTools []string `json:"suggested_tools"`
	// Permissions 是运行该 Skill 需要的权限声明。
	Permissions []string `json:"permissions"`
	// Triggers 定义 Skill 如何被自动触发。
	Triggers SkillTriggers `json:"triggers"`
	// State 是 Skill 当前在生命周期中的状态。
	State SkillState `json:"state"`
	// InvalidReason 当 State 为 invalid 时记录失败原因。
	InvalidReason string `json:"invalid_reason"`
	// CreatedAt 是创建时间戳（Unix 秒）。
	CreatedAt int64 `json:"created_at"`
	// UpdatedAt 是最后更新时间戳（Unix 秒）。
	UpdatedAt int64 `json:"updated_at"`
}

// SkillTemplate 是 Skill 内的一个 prompt 模板。
// 模板使用 {{variable}} 风格占位符，Variables 显式列出可调用的变量名。
type SkillTemplate struct {
	// Name 是模板名，如 "system"、"user"、"summary"。
	Name string `json:"name"`
	// Content 是模板内容。
	Content string `json:"content"`
	// Variables 是模板中使用的变量名列表。
	Variables []string `json:"variables"`
	// IsRequired 表示该模板是否必须在渲染时提供。
	IsRequired bool `json:"is_required"`
}

// SkillParameter 描述 Skill 接受的一个参数。
type SkillParameter struct {
	// Name 是参数名，用于在模板变量和 API 中引用。
	Name string `json:"name"`
	// Type 是参数类型，如 string、number、boolean、array、object。
	Type string `json:"type"`
	// Required 表示参数是否必填。
	Required bool `json:"required"`
	// Default 是参数的默认值，可为任意 JSON 值。
	Default any `json:"default"`
	// Description 是参数描述。
	Description string `json:"description"`
}

// SkillTriggers 定义 Skill 的自动触发规则。
// 任意一条规则命中即可认为该 Skill 可能适用于当前上下文（最终由调度器裁决）。
type SkillTriggers struct {
	// Keywords 是关键词列表，当用户输入包含这些词时触发候选。
	Keywords []string `json:"keywords"`
	// Intents 是语义意图列表，匹配到意图时触发候选。
	Intents []string `json:"intents"`
	// FilePatterns 是文件通配符列表，当工作区出现匹配文件时触发候选。
	FilePatterns []string `json:"file_patterns"`
}
