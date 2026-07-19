package skill

// DefaultBuiltins 返回平台内置 Skill 列表。
// 内置 Skill 随版本发布，Source 为 built_in，默认启用，不可本地修改。
func DefaultBuiltins() []*Skill {
	return []*Skill{
		{
			ID:              "builtin-code-helper",
			Version:         "1.0.0",
			DisplayName:     "代码助手",
			Description:     "解释、重构或生成代码片段，辅助开发者快速理解代码意图。",
			Authors:         []string{"platform"},
			Tags:            []string{"code", "refactor", "explain"},
			Source:          SkillSourceBuiltIn,
			IsLocalEditable: false,
			State:           SkillStateEnabled,
			Templates: []SkillTemplate{
				{
					Name:       "system_prompt",
					Content:    "你是一个经验丰富的程序员。用户使用的编程语言是 {{language}}。请用简洁、准确的中文回答代码相关问题，必要时给出可运行示例。",
					Variables:  []string{"language"},
					IsRequired: true,
				},
			},
			Parameters: []SkillParameter{
				{
					Name:        "language",
					Type:        "string",
					Required:    true,
					Default:     "Go",
					Description: "目标编程语言",
				},
			},
			SuggestedTools: []string{"read_file", "write_file"},
			Permissions:    []string{"file_read"},
		},
		{
			ID:              "builtin-error-diagnosis",
			Version:         "1.0.0",
			DisplayName:     "错误诊断",
			Description:     "根据错误日志和上下文定位问题根因，并提供修复建议。",
			Authors:         []string{"platform"},
			Tags:            []string{"debug", "error", "troubleshooting"},
			Source:          SkillSourceBuiltIn,
			IsLocalEditable: false,
			State:           SkillStateEnabled,
			Templates: []SkillTemplate{
				{
					Name:       "system_prompt",
					Content:    "你是一名系统调试专家。请分析以下错误信息及其上下文，给出最可能的根因、定位步骤和修复方案。错误信息：\n{{error_message}}",
					Variables:  []string{"error_message"},
					IsRequired: true,
				},
			},
			Parameters: []SkillParameter{
				{
					Name:        "error_message",
					Type:        "string",
					Required:    true,
					Description: "需要诊断的错误日志或异常信息",
				},
			},
			SuggestedTools: []string{"read_file", "run_shell"},
			Permissions:    []string{"file_read", "shell_read"},
		},
	}
}
