// Package cases 提供用于一键执行 task 的预设 Task Case。
// 每个 case 都是一个预配置的 TaskContract，带有特定的 goal、system prompt 与
// acceptance criteria。这些 case 用于演示 agent 的不同能力：代码生成、研究、
// 多 Agent 协作、对话以及长任务。
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

	// Tags 用于 UI 中的过滤
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
		CodeGenCase(),
		ResearchCase(),
		MultiAgentCase(),
		DialogueCase(),
		LongTaskCase(),
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
			MaxSteps: 8,
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
			},
		},
		Tags: []string{"code", "testing", "react-loop", "tools"},
	}
}

// ResearchCase 演示多步推理与报告生成。
// agent 拆解一个研究问题，将结论写入报告。
func ResearchCase() Case {
	return Case{
		ID:          "research",
		Name:        "研究任务",
		Description: "拆分子问题 → 分析 → 汇总 → 写入研究报告。演示多步推理和文件生成能力。",
		Icon:        "🔬",
		Category:    "research",
		IsBuiltin:   true,
		SystemPrompt: `You are a research analyst. When given a research topic:
1. Break the topic down into 3-5 sub-questions
2. Think through each sub-question systematically
3. Write your findings to a well-structured Markdown report using write_file
4. The report should include: Executive Summary, Background, Analysis, Key Findings, and References
5. Use clear headings and bullet points`,
		DefaultInput: "Research the current state of AI agent frameworks in 2026. Compare the top 3 frameworks on architecture, tool support, and multi-agent capabilities. Save the report to research/ai-agents-2026.md.",
		Contract: harness.TaskContract{
			Goal:     "Produce a structured research report",
			Scope:    ".",
			MaxSteps: 6,
			Permissions: harness.TaskPermissions{
				AllowFileWrite: true,
			},
			AcceptanceCriteria: []harness.AcceptanceCriterion{
				{
					Type:        harness.AcceptFileExists,
					Target:      "research/ai-agents-2026.md",
					Description: "Research report file exists",
				},
			},
		},
		Tags: []string{"research", "report", "analysis", "markdown"},
	}
}

// MultiAgentCase 演示多 Agent 协作（Phase 4 就绪）。
// 在 Phase 3 中，它作为单 agent 运行，模拟多 agent 工作流。
// 在 Phase 4+ 中，它将并行派生多个 agent。
func MultiAgentCase() Case {
	return Case{
		ID:          "multi-agent",
		Name:        "多 Agent 协作",
		Description: "模拟多 Agent 分工：分析 → 设计 → 实现。当前 Phase 3 单 Agent 模拟，Phase 4+ 多 Agent 并行。",
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
			Goal:     "Simulate multi-agent workflow with clear role separation",
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
		Tags: []string{"multi-agent", "collaboration", "design", "phase4"},
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
			MaxSteps: 2,
			Permissions: harness.TaskPermissions{
				// 无任何 tool 权限——纯对话
			},
		},
		Tags: []string{"dialogue", "streaming", "markdown", "baseline"},
	}
}

// LongTaskCase 演示带进度追踪的多步任务。
// agent 执行一系列相关操作，演示 Progress 文件与 checkpoint 能力。
func LongTaskCase() Case {
	return Case{
		ID:          "long-task",
		Name:        "长任务 + 进度追踪",
		Description: "多步复杂任务，演示 Progress 文件写入和关键节点里程碑。适合测试长时间运行的 Agent 稳定性。",
		Icon:        "⏳",
		Category:    "interaction",
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
		DefaultInput: "Set up a new Go project called 'task-scheduler' with standard Go project layout (cmd/, internal/, pkg/), README.md, .gitignore, Makefile, and initialize git. The project should be a simple task scheduler library.",
		Contract: harness.TaskContract{
			Goal:     "Set up a complete Go project from scratch",
			Scope:    ".",
			MaxSteps: 12,
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
			},
			Metadata: map[string]string{
				"case":     "long-task",
				"category": "devops",
			},
		},
		Tags: []string{"long-task", "devops", "progress", "multi-step"},
	}
}
