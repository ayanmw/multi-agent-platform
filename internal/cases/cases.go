// Package cases 提供用于一键执行 task 的预设 Task Case。
// 每个 case 都是一个预配置的 TaskContract，带有特定的 goal、system prompt 与
// acceptance criteria。Case 按 L1-L5 复杂度阶梯组织：
//
//   - L1 单 Agent 基线：基础 ReAct Loop / 纯对话 / 多步 shell
//   - L2 单 Agent + 子系统：todo / web_search / Skill / Cron / llm_judge
//   - L3 单 Agent + Harness 治理：PolicyGate 拦截 / 审批 / max_steps 失败 /
//     context 压缩 / checkpoint resume
//   - L4 多 Agent 静态编排：parallel / sequential / DAG
//   - L5 多 Agent 动态编排：leader-driven dispatch / agent 互评 / 故障容忍
//
// 这些 case 用于演示 agent 的不同能力，并作为 mock 回归与真实 LLM 冒烟的入口。
//
// # 用法
//
//	cases := cases.All()
//	for _, c := range cases {
//	    fmt.Println(c.Name, c.Description)
//	}
//
//	// 以特定 case 启动一个 task
//	c := cases.Get("code-gen")
//	taskID := startTask(c)
package cases

import (
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
)

// Case 表示用户可一键启动的预设 task 配置。
// 每个 case 都有 name、description、system prompt、default input 以及定义该 task
// 范围、权限和 acceptance criteria 的 contract。
//
// 内置用例（IsBuiltin=true）不可被修改或删除，仅作为种子数据存在；
// 用户自定义用例通过 Repository 持久化到 SQLite，可通过 Service 进行 CRUD。
type Case struct {
	// ID 是 case 的唯一 slug（例如 "code-gen"、"research"）
	ID string `json:"id"`

	// Name 是人类可读的展示名称
	Name string `json:"name"`

	// Description 说明该 case 做什么以及演示了什么
	Description string `json:"description"`

	// Icon 是用于 case 卡片的单个 emoji 或图标标识
	Icon string `json:"icon"`

	// Category 对相关 case 进行分组（例如 "generation"、"research"、"interaction"）
	Category string `json:"category"`

	// SystemPrompt 是该 case 下 agent 的 system prompt
	SystemPrompt string `json:"system_prompt"`

	// DefaultInput 是预填写的用户输入（可被用户覆盖）
	DefaultInput string `json:"default_input"`

	// Contract 是定义范围、权限和 acceptance criteria 的 TaskContract
	Contract harness.TaskContract `json:"contract"`

	// Tags 用于 UI 中的过滤；必须包含 L1-L5 阶梯标识与能力维度标签
	Tags []string `json:"tags"`

	// IsBuiltin 标记该 case 是否为内置预设。
	// Builtin case 在空数据库上被种子化，且不可变。
	IsBuiltin bool `json:"is_builtin"`

	// CreatedAt 是创建时间戳
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt 是最后更新时间戳
	UpdatedAt time.Time `json:"updated_at"`
}

// All 返回所有预设 task case。新设计的 case 在此处添加。
// 返回内置用例列表，供 Service 在空库时种子初始化，也供前端展示内置卡片。
func All() []Case {
	return []Case{
		// L1 单 Agent 基线
		CodeGenCase(),
		ResearchCase(),
		DialogueCase(),
		LongTaskCase(),

		// L2 单 Agent + 子系统
		TodoDrivenCase(),
		WebResearchCase(),
		SkillCodeHelperCase(),
		CronNotifyCase(),
		LLMJudgeQACase(),

		// L3 单 Agent + Harness 治理
		PolicyEnforcementCase(),
		ApprovalFlowCase(),
		MaxStepsExhaustionCase(),
		ContextCompressionCase(),
		CheckpointResumeCase(),

		// L4 多 Agent 静态编排
		MultiAgentLegacyCase(),
		MultiAgentParallelCase(),
		MultiAgentSequentialCase(),
		MultiAgentDAGCase(),

		// L5 多 Agent 动态编排
		MultiAgentLeaderDispatchCase(),
		MultiAgentReviewCase(),
		MultiAgentFaultToleranceCase(),
	}
}

// Get 按 ID 返回 case，找不到则返回 nil。
func Get(id string) *Case {
	for _, c := range All() {
		if c.ID == id {
			return &c
		}
	}
	return nil
}

// ============================================================================
// L1：单 Agent 基线
// ============================================================================

// CodeGenCase 演示带 tool 执行与 self-fix loop 的代码生成。
// agent 生成代码、写入文件、运行测试，并修复任何失败。
func CodeGenCase() Case {
	return Case{
		ID:          "code-gen",
		Name:        "代码生成 + 执行验证",
		Description: "LLM 生成代码 → write_file 保存 → run_shell 测试 → 回 Loop 修复。演示完整的 ReAct Loop 能力。",
		Icon:        "💻",
		Category:    "generation",
		IsBuiltin:   true,
		SystemPrompt: `You are a senior software engineer. When given a coding task:
1. Write clean, well-documented code
2. Save it to a file using write_file
3. Run tests using run_shell
4. If tests fail, read the error output and fix the code
5. Repeat until all tests pass
6. Summarize what you built and the test results`,
		DefaultInput: "Write a Go function that calculates the Fibonacci sequence up to n, with a test file that verifies the first 10 numbers. Save to fib/ directory.",
		Contract: harness.TaskContract{
			Goal:     "Generate code and verify it passes tests",
			Scope:    ".",
			MaxSteps: 10,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
				AllowShell:     true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptFileExists,
					Target:      "fib/fib.go",
					Description: "Fibonacci function file exists",
				},
				{
					Type:        harness.AcceptFileExists,
					Target:      "fib/fib_test.go",
					Description: "Fibonacci test file exists",
				},
				{
					Type:        harness.AcceptTestPass,
					Target:      "go test ./fib",
					Description: "Fibonacci package passes go test (executed when shell evaluation is enabled)",
				},
			},
		},
		Tags: []string{"L1", "code", "testing", "react-loop", "tools", "generation", "tools:run_shell"},
	}
}

// ResearchCase 演示多步推理与报告生成。
// agent 拆解一个研究问题，将结论写入结构化报告，并校验关键章节。
func ResearchCase() Case {
	return Case{
		ID:          "research",
		Name:        "研究任务",
		Description: "拆分子问题 → 分析 → 汇总 → 写入研究报告。演示多步推理、结构化报告与 content_contains 验收。",
		Icon:        "🔬",
		Category:    "research",
		IsBuiltin:   true,
		SystemPrompt: `You are a research analyst. When given a research topic:
1. Break the topic down into 3-5 sub-questions
2. Think through each sub-question systematically
3. Write your findings to a well-structured Markdown report using write_file
4. The report MUST include all of the following sections with these exact headings:
   - Executive Summary
   - Background
   - Analysis
   - Key Findings
   - References
5. Use clear headings and bullet points`,
		DefaultInput: "Research the current state of AI agent frameworks in 2026. Compare the top 3 frameworks on architecture, tool support, and multi-agent capabilities. Save the report to research/ai-agents-2026.md.",
		Contract: harness.TaskContract{
			Goal:     "Produce a structured research report with required sections",
			Scope:    ".",
			MaxSteps: 8,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptFileExists,
					Target:      "research/ai-agents-2026.md",
					Description: "Research report file exists",
				},
				{
					Type:        harness.AcceptContentContains,
					Target:      "research/ai-agents-2026.md",
					Expected:    "Executive Summary",
					Description: "Report contains Executive Summary section",
				},
				{
					Type:        harness.AcceptContentContains,
					Target:      "research/ai-agents-2026.md",
					Expected:    "References",
					Description: "Report contains References section",
				},
			},
		},
		Tags: []string{"L1", "research", "report", "analysis", "markdown", "tools:write_file"},
	}
}

// DialogueCase 演示无 tool 调用的纯 LLM 对话。
// 用于测试流式渲染（TypeWriter）与 Markdown 显示。
func DialogueCase() Case {
	return Case{
		ID:          "dialogue",
		Name:        "交互式对话",
		Description: "纯 LLM 对话，无工具调用。验证流式渲染 (TypeWriter) 和 Markdown 显示效果。",
		Icon:        "💬",
		Category:    "interaction",
		IsBuiltin:   true,
		SystemPrompt: `You are a knowledgeable and engaging AI assistant. Respond to the user's question with:
1. A clear, structured answer with headings
2. Code examples where relevant (in fenced code blocks)
3. Bullet points for key takeaways
4. A summary at the end

Do NOT use any tools — this is a pure conversation.`,
		DefaultInput: "Explain the difference between WebSocket and Server-Sent Events (SSE). When should I use each one? Include code examples.",
		Contract: harness.TaskContract{
			Goal:     "Engage in a pure dialogue without tool calls",
			Scope:    ".",
			MaxSteps: 3,
			Permissions: harness.TaskPermissions{
				// 无任何 tool 权限——纯对话
			},
		},
		Tags: []string{"L1", "dialogue", "streaming", "markdown", "baseline"},
	}
}

// LongTaskCase 演示带进度追踪的多步任务。
// agent 执行一系列相关操作，演示 Progress 文件、git 副作用与长任务稳定性。
func LongTaskCase() Case {
	return Case{
		ID:          "long-task",
		Name:        "长任务 + 进度追踪",
		Description: "多步复杂任务，演示 Progress 文件写入、git 真实提交与关键节点里程碑。适合测试长时间运行的 Agent 稳定性。",
		Icon:        "⏳",
		Category:    "automation",
		IsBuiltin:   true,
		SystemPrompt: `You are a DevOps engineer setting up a project. Complete the following steps in order:
1. Create the project directory structure (use run_shell mkdir)
2. Write a README.md with project overview (use write_file)
3. Write a .gitignore file appropriate for the project type (use write_file)
4. Write a Makefile or build script (use write_file)
5. Initialize a git repository and make the first commit (use run_shell)
6. Verify the project structure with ls or tree (use run_shell)
7. Summarize what was created

Report progress at each step. This is a long-running task designed to test multi-step agent execution.`,
		DefaultInput: "Set up a new Go project called 'task-scheduler' with standard Go project layout (cmd/, internal/, pkg/), README.md, .gitignore, Makefile, and initialize git with a real commit. The project should be a simple task scheduler library.",
		Contract: harness.TaskContract{
			Goal:     "Set up a complete Go project from scratch with a real git commit",
			Scope:    ".",
			MaxSteps: 14,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
				AllowShell:     true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptFileExists,
					Target:      "task-scheduler/README.md",
					Description: "README exists",
				},
				{
					Type:        harness.AcceptFileExists,
					Target:      "task-scheduler/.gitignore",
					Description: ".gitignore exists",
				},
				{
					Type:        harness.AcceptFileExists,
					Target:      "task-scheduler/Makefile",
					Description: "Makefile exists",
				},
				{
					Type:        harness.AcceptShellExitZero,
					Target:      "cd task-scheduler && git log --oneline -1",
					Description: "Git repository has at least one commit (executed when shell evaluation is enabled)",
				},
			},
			Metadata: map[string]string{
				"case":     "long-task",
				"category": "devops",
			},
		},
		Tags: []string{"L1", "long-task", "devops", "progress", "multi-step", "tools:run_shell"},
	}
}

// ============================================================================
// L2：单 Agent + 子系统
// ============================================================================

// TodoDrivenCase 演示 agent 通过 todo 工具管理任务清单。
// agent 将用户目标拆分为 todo、逐个完成并更新状态。
func TodoDrivenCase() Case {
	return Case{
		ID:          "todo-driven",
		Name:        "Todo 驱动任务",
		Description: "Agent 使用 todo/create、todo/update_status、todo/list 管理工作项，演示任务拆解与状态跟踪。",
		Icon:        "☑️",
		Category:    "automation",
		IsBuiltin:   true,
		SystemPrompt: `You are a task-oriented assistant. When given a goal:
1. Break it into 3-5 actionable subtasks
2. Create each subtask using todo/create with the current session_id and task_id
3. Work through them one by one
4. Mark each completed todo using todo/update_status with status "done"
5. Use todo/list to verify all todos are done
6. Summarize the completed work

You must use the todo tools. Do not keep subtasks only in your head.`,
		DefaultInput: "Plan and execute a small research task: find out what the top 3 Go web frameworks are, list their strengths, and mark todos as you go. Use todo tools for each step.",
		Contract: harness.TaskContract{
			Goal:     "Use todo tools to manage and complete a multi-step task",
			Scope:    ".",
			MaxSteps: 12,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "completed",
					Description: "Agent reports task completion",
				},
			},
		},
		Tags: []string{"L2", "todo", "task-management", "tools:todo", "subsystem"},
	}
}

// WebResearchCase 演示 agent 使用 web_search + fetch_url + parse_json 组合获取外部信息。
func WebResearchCase() Case {
	return Case{
		ID:          "web-research",
		Name:        "Web 调研",
		Description: "Agent 使用 web_search 搜索、fetch_url 抓取、parse_json 提取结构，产出带 URL 引用的调研报告。",
		Icon:        "🌐",
		Category:    "research",
		IsBuiltin:   true,
		SystemPrompt: `You are an internet research assistant. When asked about current or public information:
1. Use core/web_search to find relevant sources
2. Use core/fetch_url on the most promising result to read details
3. Use core/parse_json if the page returns JSON (e.g. API endpoints)
4. Write a concise report to web-research/report.md with URLs cited
5. If the web search is not available, state the limitation clearly`,
		DefaultInput: "Research the current top 3 cloud providers' market share in 2026. Cite sources and save a short report to web-research/report.md.",
		Contract: harness.TaskContract{
			Goal:     "Use web_search, fetch_url and parse_json to research a topic",
			Scope:    ".",
			MaxSteps: 10,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
				AllowNetwork:   true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptFileExists,
					Target:      "web-research/report.md",
					Description: "Web research report file exists",
				},
				{
					Type:        harness.AcceptContentContains,
					Target:      "web-research/report.md",
					Expected:    "http",
					Description: "Report cites at least one URL",
				},
			},
		},
		Tags: []string{"L2", "web-search", "research", "tools:web_search", "tools:fetch_url", "tools:parse_json", "network"},
	}
}

// SkillCodeHelperCase 演示启用 builtin-code-helper Skill 后 Agent 的代码辅助行为。
// Skill 模板会自动注入到 engine system prompt 中。
func SkillCodeHelperCase() Case {
	return Case{
		ID:          "skill-code-helper",
		Name:        "Skill：代码助手",
		Description: "启用 builtin-code-helper Skill，让 Agent 在生成代码时遵循 Skill 注入的额外指令。",
		Icon:        "🧩",
		Category:    "generation",
		IsBuiltin:   true,
		SystemPrompt: `You are a software engineer with the builtin-code-helper Skill enabled.
When given a coding request:
1. Explain the approach briefly
2. Generate the code
3. Save it to a file using write_file
4. Provide a short usage example`,
		DefaultInput: "Implement a Go function that reverses a string (runes-safe) and save it to skill-demo/reverse.go.",
		Contract: harness.TaskContract{
			Goal:     "Generate code while the builtin-code-helper Skill is active",
			Scope:    ".",
			MaxSteps: 8,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptFileExists,
					Target:      "skill-demo/reverse.go",
					Description: "Skill code helper demo file exists",
				},
				{
					Type:        harness.AcceptContentContains,
					Target:      "skill-demo/reverse.go",
					Expected:    "func Reverse",
					Description: "File declares a Reverse function",
				},
			},
		},
		Tags: []string{"L2", "skill", "code", "tools:write_file", "skill:builtin-code-helper"},
	}
}

// CronNotifyCase 演示 Agent 在运行时通过 cron/create 创建定时器，并验证 cron 事件回流。
func CronNotifyCase() Case {
	return Case{
		ID:          "cron-notify",
		Name:        "Cron 定时通知",
		Description: "Agent 使用 cron/create 定时器触发一次 session 通知，演示 Cron Agent Tool 与事件回流。",
		Icon:        "⏰",
		Category:    "automation",
		IsBuiltin:   true,
		SystemPrompt: `You are a workflow automation assistant. When asked to schedule a notification:
1. Create a cron timer using cron/create with schedule_type "once" and action_type "notify_session"
2. The action_payload should include the session_id and a message
3. Wait briefly or use cron/trigger to manually trigger it
4. Report the cron ID and confirmation

Use the current session_id from the conversation context.`,
		DefaultInput: "Schedule a one-time notification in 10 seconds that says 'Hello from cron'. Trigger it manually with cron/trigger after creation and report the cron ID.",
		Contract: harness.TaskContract{
			Goal:     "Create and trigger a cron notification timer",
			Scope:    ".",
			MaxSteps: 10,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "cron",
					Description: "Agent mentions cron in final summary",
				},
			},
		},
		Tags: []string{"L2", "cron", "scheduling", "tools:cron", "automation"},
	}
}

// LLMJudgeQACase 演示开放问答使用 llm_judge 验收。
func LLMJudgeQACase() Case {
	return Case{
		ID:          "llm-judge-qa",
		Name:        "LLM Judge 开放问答",
		Description: "纯对话开放问答 case，使用 llm_judge 验收评估回答是否满足 rubric。",
		Icon:        "⚖️",
		Category:    "interaction",
		IsBuiltin:   true,
		SystemPrompt: `You are a helpful expert. Answer the user's open-ended question thoroughly, with clear reasoning and a concise summary. Do not use tools unless asked.`,
		DefaultInput: "Explain the trade-offs between monolithic and microservice architectures for a small team. Include when to choose each.",
		Contract: harness.TaskContract{
			Goal:     "Answer an open-ended question to be evaluated by an LLM judge",
			Scope:    ".",
			MaxSteps: 4,
			Permissions: harness.TaskPermissions{
				// 纯对话
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptLLMJudge,
					Target:      "The answer explains trade-offs between monolithic and microservice architectures and gives guidance on when to choose each.",
					Description: "LLM judge evaluates answer quality",
				},
			},
		},
		Tags: []string{"L2", "llm-judge", "qa", "judge"},
	}
}

// ============================================================================
// L3：单 Agent + Harness 治理
// ============================================================================

// PolicyEnforcementCase 演示 PolicyGate 拦截越界操作。
// 由于 case 的 scope 被设为 safe-zone， agent 写入其外路径时会被拦截。
func PolicyEnforcementCase() Case {
	return Case{
		ID:          "policy-enforcement",
		Name:        "策略拦截",
		Description: "Agent 尝试向 scope 外写入，PolicyGate 拦截并返回错误，任务不应以 completed 逾越策略。",
		Icon:        "🛡️",
		Category:    "governance",
		IsBuiltin:   true,
		SystemPrompt: `You are a file-writing assistant. Your user asked you to write a file.
Try to write the file to ../outside.txt using write_file. If you are blocked by a policy, report the exact error and stop. Do not attempt alternative paths without user permission.`,
		DefaultInput: "Try to write a file outside the allowed working directory to ../outside.txt and report what happens.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate PolicyGate blocking an out-of-scope write",
			Scope:    "safe-zone",
			MaxSteps: 5,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			// 治理类 case 不强制 completed；回归脚本会识别requires_not_completed元数据。
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "blocked",
					Description: "Agent final result mentions being blocked",
				},
			},
			Metadata: map[string]string{
				"expected_status": "intercepted",
				"harness":         "policy",
			},
		},
		Tags: []string{"L3", "harness", "policy", "governance", "harness:policy"},
	}
}

// ApprovalFlowCase 演示需要审批的场景。
// Agent 的危险 shell 命令触发审批请求，回归脚本通过 API 批准或拒绝验证两条路径。
func ApprovalFlowCase() Case {
	return Case{
		ID:          "approval-flow",
		Name:        "审批流程",
		Description: "Agent 执行高风险 shell 命令时触发审批链，演示 Harness 的审批治理能力。",
		Icon:        "✅",
		Category:    "governance",
		IsBuiltin:   true,
		SystemPrompt: `You are an assistant that needs to run a sensitive command. The current task is approved.
Use run_shell to execute: echo "sensitive-action-confirmed". The command should be allowed after approval. If rejected, report the rejection reason and stop.`,
		DefaultInput: "Run the sensitive command echo 'sensitive-action-confirmed' and report whether it was approved.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate Harness approval workflow",
			Scope:    ".",
			MaxSteps: 6,
			Permissions: harness.TaskPermissions{
				AllowShell:          true,
				AllowShellDangerous: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptShellExitZero,
					Target:      "echo sensitive-action-confirmed",
					Description: "Sensitive command output confirmed (executed when shell evaluation is enabled)",
				},
			},
			Metadata: map[string]string{
				"harness": "approval",
			},
		},
		Tags: []string{"L3", "harness", "approval", "governance", "tools:run_shell", "harness:approval"},
	}
}

// MaxStepsExhaustionCase 演示 max_steps_exceeded 失败路径。
func MaxStepsExhaustionCase() Case {
	return Case{
		ID:          "max-steps-exhaustion",
		Name:        "Max Steps 耗尽",
		Description: "构造一个需要多步但 MaxSteps 很紧的任务，验证 Engine 以 max_steps_exceeded 失败终止。",
		Icon:        "🛑",
		Category:    "governance",
		IsBuiltin:   true,
		SystemPrompt: `You are a helpful assistant. The user wants you to write a long poem with 10 verses, one verse per step, saving each verse to a separate file (verse_1.txt ... verse_10.txt). Do not stop early.`,
		DefaultInput: "Write a 10-verse poem, saving each verse to its own file. Do not stop until all 10 are written.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate max_steps_exceeded termination",
			Scope:    ".",
			MaxSteps: 3,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "max_steps",
					Description: "Agent result mentions max_steps exhaustion",
				},
			},
			Metadata: map[string]string{
				"expected_status": "failed",
				"reason":          "max_steps_exceeded",
				"harness":         "max_steps",
			},
		},
		Tags: []string{"L3", "harness", "max_steps", "governance", "harness:max_steps"},
	}
}

// ContextCompressionCase 演示在长上下文场景下 compressor 触发后任务仍能完成。
func ContextCompressionCase() Case {
	return Case{
		ID:          "context-compression",
		Name:        "上下文压缩续跑",
		Description: "通过 TokenBudget 触发 context compressor，验证任务在压缩后仍能到达 completed 终态。",
		Icon:        "🗜️",
		Category:    "governance",
		IsBuiltin:   true,
		SystemPrompt: `You are a summarization assistant.
1. Generate a long list of 20 numbered facts about Go programming using write_file or direct output
2. Then condense them into a 3-bullet summary
3. Save the summary to compression/summary.md`,
		DefaultInput: "Generate 20 facts about Go, then compress them into 3 bullets and save to compression/summary.md.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate context compression while still completing",
			Scope:    ".",
			MaxSteps: 10,
			// 设置一个相对紧的 token 预算以触发 compressor。
			TokenBudget: 800,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptFileExists,
					Target:      "compression/summary.md",
					Description: "Compressed summary file exists",
				},
			},
			Metadata: map[string]string{
				"harness": "compressor",
			},
		},
		Tags: []string{"L3", "harness", "context-compression", "governance", "harness:compressor"},
	}
}

// CheckpointResumeCase 演示 Pause/Resume 续跑能力。
// 回归脚本会主动调用 pause/resume API 验证任务最终完成。
func CheckpointResumeCase() Case {
	return Case{
		ID:          "checkpoint-resume",
		Name:        "Checkpoint 暂停恢复",
		Description: "Agent 写入多步进度文件，回归脚本中途 pause/resume，验证 checkpoint 续跑后任务完成。",
		Icon:        "💾",
		Category:    "governance",
		IsBuiltin:   true,
		SystemPrompt: `You are a checkpoint-aware assistant. When given a multi-step writing task:
1. Create checkpoint/steps.md
2. Write step 1, then step 2, then step 3
3. After each step, update the file with current progress
4. When all steps are written, mark the task complete`,
		DefaultInput: "Write three progress entries to checkpoint/steps.md and confirm completion.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate checkpoint pause/resume recovery",
			Scope:    ".",
			MaxSteps: 12,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptFileExists,
					Target:      "checkpoint/steps.md",
					Description: "Checkpoint progress file exists",
				},
				{
					Type:        harness.AcceptContentContains,
					Target:      "checkpoint/steps.md",
					Expected:    "step 3",
					Description: "Checkpoint file contains step 3 progress",
				},
			},
			Metadata: map[string]string{
				"harness": "checkpoint",
			},
		},
		Tags: []string{"L3", "harness", "checkpoint", "governance", "harness:checkpoint"},
	}
}

// ============================================================================
// L4：多 Agent 静态编排
// ============================================================================

// MultiAgentLegacyCase 是 Phase 3 遗留的“单 LLM 扮演三角色” case。
// 保留作为 legacy 对照，避免破坏旧回归脚本；Description 中明确标注。
func MultiAgentLegacyCase() Case {
	return Case{
		ID:          "multi-agent",
		Name:        "多 Agent 协作 (legacy 模拟)",
		Description: "[Legacy] Phase 3 单 Agent 模拟多角色的对照 case。真实多 Agent 编排见 multi-agent-parallel / sequential / dag / leader-dispatch。",
		Icon:        "🤝",
		Category:    "collaboration",
		IsBuiltin:   true,
		SystemPrompt: `You are simulating a multi-agent workflow. You will act as three different roles:
1. ANALYST: Understand the requirements and produce a specification
2. DESIGNER: Create an architecture plan based on the specification
3. IMPLEMENTER: Write the code based on the design

For each role, clearly label your output with "## [ROLE: Analyst]" etc.
Write all outputs to files in the output/ directory.`,
		DefaultInput: "Design and implement a simple REST API rate limiter. The Analyst should specify requirements, the Designer should create the architecture, and the Implementer should write the code. Save to output/rate-limiter/.",
		Contract: harness.TaskContract{
			Goal:     "Simulate multi-agent workflow with clear role separation (legacy)",
			Scope:    ".",
			MaxSteps: 10,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptFileExists,
					Target:      "output/rate-limiter",
					Description: "Output directory exists",
				},
			},
		},
		Tags: []string{"L4", "multi-agent", "collaboration", "legacy", "multi-agent:legacy"},
	}
}

// MultiAgentParallelCase 演示 3 worker 并行执行。
func MultiAgentParallelCase() Case {
	return Case{
		ID:          "multi-agent-parallel",
		Name:        "多 Agent 并行",
		Description: "Orchestrator 并行派发 3 个 worker 分析不同维度，最后由 leader 汇总。",
		Icon:        "🔀",
		Category:    "collaboration",
		IsBuiltin:   true,
		SystemPrompt: `You are a leader coordinator. Your team has 3 parallel workers.
Use dispatch_sub_agent with strategy "parallel" to assign:
- agent_reviewer: review scalability aspects
- agent_security: review security aspects
- agent_usability: review usability aspects

Each worker should write a short paragraph of findings. After they return, summarize the combined review into a final answer.`,
		DefaultInput: "Review a simple REST API design for a task scheduler. Dispatch parallel workers for scalability, security, and usability reviews.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate parallel multi-agent orchestration",
			Scope:    ".",
			MaxSteps: 12,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "review",
					Description: "Final answer mentions review summary",
				},
			},
			Metadata: map[string]string{
				"multi_agent": "parallel",
				"worker_count": "3",
			},
		},
		Tags: []string{"L4", "multi-agent", "parallel", "collaboration", "multi-agent:parallel"},
	}
}

// MultiAgentSequentialCase 演示 researcher → writer 顺序链式编排。
func MultiAgentSequentialCase() Case {
	return Case{
		ID:          "multi-agent-sequential",
		Name:        "多 Agent 顺序链",
		Description: "Orchestrator 顺序执行 researcher → writer，前序结果经 AgentBus 转发为后序输入。",
		Icon:        "➡️",
		Category:    "collaboration",
		IsBuiltin:   true,
		SystemPrompt: `You are an orchestration assistant. Use dispatch_sub_agent with strategy "sequential" to run:
1. agent_researcher: research the topic and produce structured findings
2. agent_writer: take the research findings and write a final report

The orchestrator will forward researcher output to writer automatically. Summarize the final report.`,
		DefaultInput: "Research the pros and cons of serverless vs containers, then write a report based on the research findings.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate sequential multi-agent orchestration",
			Scope:    ".",
			MaxSteps: 12,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "report",
					Description: "Final answer mentions report",
				},
			},
			Metadata: map[string]string{
				"multi_agent": "sequential",
				"worker_count": "2",
			},
		},
		Tags: []string{"L4", "multi-agent", "sequential", "collaboration", "multi-agent:sequential"},
	}
}

// MultiAgentDAGCase 演示 A → B → C 依赖链的 DAG 编排。
func MultiAgentDAGCase() Case {
	return Case{
		ID:          "multi-agent-dag",
		Name:        "多 Agent DAG 编排",
		Description: "Orchestrator 按 DAG 依赖顺序调度 A→B→C，验证依赖完成才启动下游。",
		Icon:        "🕸️",
		Category:    "collaboration",
		IsBuiltin:   true,
		SystemPrompt: `You are an orchestration assistant. Use dispatch_sub_agent with strategy "pipeline" to run a 3-stage DAG:
1. agent_analyst: analyze the requirements
2. agent_designer: design the architecture (depends on analyst)
3. agent_implementer: implement the code (depends on designer)

The pipeline strategy will forward outputs down the chain. Summarize the final implementation.`,
		DefaultInput: "Build a tiny URL shortener service. Run analyst → designer → implementer in a DAG pipeline.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate DAG multi-agent orchestration",
			Scope:    ".",
			MaxSteps: 14,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "implement",
					Description: "Final answer mentions implementation",
				},
			},
			Metadata: map[string]string{
				"multi_agent": "dag",
				"worker_count": "3",
			},
		},
		Tags: []string{"L4", "multi-agent", "dag", "collaboration", "multi-agent:dag"},
	}
}

// ============================================================================
// L5：多 Agent 动态编排
// ============================================================================

// MultiAgentLeaderDispatchCase 演示 leader 运行时通过 dispatch_sub_agent 决定派发对象。
func MultiAgentLeaderDispatchCase() Case {
	return Case{
		ID:          "multi-agent-leader-dispatch",
		Name:        "Leader 动态派发",
		Description: "Leader Agent 在 ReAct Loop 中运行时调用 dispatch_sub_agent，按当前状态决定派谁。",
		Icon:        "🎯",
		Category:    "collaboration",
		IsBuiltin:   true,
		SystemPrompt: `You are a leader agent. You decide at runtime how to delegate work.
Given the user's task, analyze it once and then use dispatch_sub_agent to delegate to the most relevant worker:
- agent_coder for code generation tasks
- agent_researcher for information gathering
- agent_reviewer for review tasks

After the worker returns, provide a final summary.`,
		DefaultInput: "I need a Python function that calculates moving averages. Delegate to the appropriate worker and return the code plus a short explanation.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate leader-driven dynamic dispatch",
			Scope:    ".",
			MaxSteps: 10,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "moving average",
					Description: "Final answer addresses the moving average task",
				},
			},
			Metadata: map[string]string{
				"multi_agent": "leader-dispatch",
				"worker_count": "1",
			},
		},
		Tags: []string{"L5", "multi-agent", "leader-dispatch", "collaboration", "multi-agent:dispatch"},
	}
}

// MultiAgentReviewCase 演示 writer + reviewer 互评 + leader 裁决。
func MultiAgentReviewCase() Case {
	return Case{
		ID:          "multi-agent-review",
		Name:        "Agent 互评",
		Description: "Writer 生成文档，Reviewer 评阅并反馈，Leader 汇总裁决；验证 AgentBus 消息往返。",
		Icon:        "👥",
		Category:    "collaboration",
		IsBuiltin:   true,
		SystemPrompt: `You are a leader agent overseeing a review process.
Use dispatch_sub_agent to run:
1. agent_writer: write a short design doc for the requested feature
2. agent_reviewer: review the design doc and provide feedback

Run them sequentially so reviewer sees writer output. After both return, summarize the design and the feedback, then give a final verdict (approve / revise / reject).`,
		DefaultInput: "Design a feature that adds retry logic to our task scheduler. Writer produces the design, reviewer critiques it, leader gives a verdict.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate multi-agent review loop with leader verdict",
			Scope:    ".",
			MaxSteps: 14,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "verdict",
					Description: "Leader provides a verdict",
				},
			},
			Metadata: map[string]string{
				"multi_agent": "review",
				"worker_count": "2",
			},
		},
		Tags: []string{"L5", "multi-agent", "review", "collaboration", "multi-agent:review"},
	}
}

// MultiAgentFaultToleranceCase 演示 leader 对 worker 失败的降级处理。
// 当前能力边界：mock/真实 worker 不会真崩溃，case 验证 leader 能处理 worker 返回 error 结果。
func MultiAgentFaultToleranceCase() Case {
	return Case{
		ID:          "multi-agent-fault-tolerance",
		Name:        "多 Agent 故障容忍",
		Description: "[Known Limitation] 当前主要验证 leader 不因单个 worker 失败而整体崩溃，并能产出降级结论。真注入 worker 崩溃待后续 change。",
		Icon:        "🛡️",
		Category:    "collaboration",
		IsBuiltin:   true,
		SystemPrompt: `You are a resilient leader agent. Dispatch a primary worker and a fallback worker using dispatch_sub_agent with strategy "parallel":
- agent_primary: attempt the main task
- agent_fallback: produce a safe baseline answer

If the primary fails, use the fallback result and explain the degradation. Do not crash.`,
		DefaultInput: "Attempt a complex optimization for our scheduler. If the primary worker fails, fall back to a simple FIFO baseline and explain the degradation.",
		Contract: harness.TaskContract{
			Goal:     "Demonstrate leader fault tolerance when a worker fails or returns error",
			Scope:    ".",
			MaxSteps: 12,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptContentContains,
					Target:      "final_result",
					Expected:    "fallback",
					Description: "Final answer mentions fallback or degradation",
				},
			},
			Metadata: map[string]string{
				"multi_agent":  "fault-tolerance",
				"worker_count": "2",
				"limitation":   "不能真正注入崩溃，仅验证错误结果处理",
			},
		},
		Tags: []string{"L5", "multi-agent", "fault-tolerance", "collaboration", "multi-agent:fault-tolerance"},
	}
}
