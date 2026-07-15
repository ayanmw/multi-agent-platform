// Package cases provides preset Task Cases for one-click task execution.
// Each case is a pre-configured TaskContract with a specific goal, system prompt,
// and acceptance criteria. Cases are designed to demonstrate different agent
// capabilities: code generation, research, multi-agent collaboration, dialogue,
// and long-running tasks.
//
// # Usage
//
//	cases := cases.All()
//	for _, c := range cases {
//	    fmt.Println(c.Name, c.Description)
//	}
//
//	// Start a task with a specific case
//	c := cases.Get("code-gen")
//	taskID := startTask(c)
package cases

import (
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
)

// Case represents a preset task configuration that users can launch with one click.
// Each case has a name, description, system prompt, default input, and contract
// that defines the task's scope, permissions, and acceptance criteria.
//
// 内置用例（IsBuiltin=true）不可被修改或删除，仅作为种子数据存在；
// 用户自定义用例通过 Repository 持久化到 SQLite，可通过 Service 进行 CRUD。
type Case struct {
	// ID is a unique slug for the case (e.g., "code-gen", "research")
	ID string `json:"id"`

	// Name is the human-readable display name
	Name string `json:"name"`

	// Description explains what the case does and what it demonstrates
	Description string `json:"description"`

	// Icon is a single emoji or icon identifier for the case card
	Icon string `json:"icon"`

	// Category groups related cases (e.g., "generation", "research", "interaction")
	Category string `json:"category"`

	// SystemPrompt is the agent's system prompt for this case
	SystemPrompt string `json:"system_prompt"`

	// DefaultInput is the pre-filled user input (can be overridden by the user)
	DefaultInput string `json:"default_input"`

	// Contract is the TaskContract that defines scope, permissions, and acceptance criteria
	Contract harness.TaskContract `json:"contract"`

	// Tags for filtering in the UI
	Tags []string `json:"tags"`

	// IsBuiltin marks whether this case is a built-in preset.
	// Builtin cases are seeded on an empty database and are immutable.
	IsBuiltin bool `json:"is_builtin"`

	// CreatedAt is the creation timestamp
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is the last update timestamp
	UpdatedAt time.Time `json:"updated_at"`
}

// All returns all preset task cases. Add new cases here as they are designed.
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

// Get returns a case by ID, or nil if not found.
func Get(id string) *Case {
	for _, c := range All() {
		if c.ID == id {
			return &c
		}
	}
	return nil
}

// CodeGenCase demonstrates code generation with tool execution and self-fix loop.
// The agent generates code, writes it to a file, runs tests, and fixes any failures.
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

// ResearchCase demonstrates multi-step reasoning and report generation.
// The agent breaks down a research question, writes findings to a report.
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

// MultiAgentCase demonstrates multi-agent coordination (Phase 4 readiness).
// In Phase 3, this runs as a single agent simulating the multi-agent workflow.
// In Phase 4+, this will spawn multiple agents in parallel.
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

// DialogueCase demonstrates pure LLM conversation without tool calls.
// This tests the streaming rendering (TypeWriter) and Markdown display.
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
				// No tool permissions — pure dialogue
			},
		},
		Tags: []string{"dialogue", "streaming", "markdown", "baseline"},
	}
}

// LongTaskCase demonstrates multi-step task with progress tracking.
// The agent performs a series of related operations, demonstrating the
// Progress file and checkpoint capabilities.
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
