// Package runtime 实现多 Agent 平台的核心 Agent 执行引擎——整个平台的心脏。
// 它编排驱动每个 agent 的 ReAct (Reasoning + Acting) loop。
//
// # 架构概览
//
// runtime 包位于系统中心，连接三个关键子系统：
//
//  1. LLM Client (internal/llm) — 向 AI 模型发送 chat 请求，接收流式 SSE
//     响应，包含文本内容和 tool_call delta。
//  2. Tool Registry (internal/tool) — 管理可用 tool；engine 据此为 LLM 构建
//     tool 定义，并把 tool call 派发给 registry。
//  3. Event Bus (pkg/event) — 通过 WebSocket 与前端通信的实时通道。每个
//     状态转换（thinking、tool call、observation、completion、failure）都
//     作为类型化事件广播，UI 可实时渲染 agent 的内部状态。
//
// ## ReAct Loop
//
// ReAct (Reasoning + Acting) loop 是每个 agent 遵循的决策循环。它是一个
// 包含三个阶段的状态机：
//
//	┌──────────────────────────────────────────────────┐
//	│                   ReAct Loop                      │
//	│                                                   │
//	│  ┌──────────┐    tool_calls?    ┌──────────────┐ │
//	│  │  THINK   │──────────────────>│ EXECUTE_TOOL │ │
//	│  │ (LLM)    │                   │ (Registry)   │ │
//	│  └──────────┘                   └──────────────┘ │
//	│       ^                                │         │
//	│       │       observation             │          │
//	│       └────────────────────────────────┘          │
//	│                                                   │
//	│  No tool_calls? → final answer → task_completed   │
//	└──────────────────────────────────────────────────┘
//
// Phase 1 — THINK：engine 把对话历史（system prompt + user message +
// assistant response + tool result）发给 LLM。LLM 以流式方式返回文本 token
// （以打字机效果展示给用户），并可能发出 tool_call delta。如果 LLM 只返回
// 文本而没有 tool_calls，该文本就是最终答案——任务完成。
//
// Phase 2 — EXECUTE_TOOL：如果 LLM 请求一个或多个 tool call，engine 把
// 它们逐个派发给 Tool Registry。tool 的结果被格式化为 JSON，并以 "tool"
// 角色消息追加到对话中。engine 发出 tool_call_started、tool_call_output
// 和 observation 事件，让 UI 渲染 tool 执行进度。
//
// Phase 3 — OBSERVE：tool 结果被回填到对话历史。loop 回到 Phase 1
// (THINK)，LLM 看到 observation 后决定是继续调用 tool 还是给出最终答案。
// 该循环重复，直到 LLM 产出最终答案或超过 MaxSteps。
//
// # Event-Driven Transparency（白盒 Agent）
//
// engine 面向完全可观测设计——每个内部状态变化都以事件形式发出。这就是
// "白盒"哲学：前端可以清楚地看到 agent 在想什么、调用什么 tool、观察到
// 什么结果。事件类型：
//
//	agent_ready          — agent 初始化完成，可处理输入
//	step_started         — 一个新的 think 或 tool_call step 已开始
//	llm_thinking         — LLM 正在处理（token 到达前）
//	llm_delta            — LLM 输出的单个文本 token（流式）
//	llm_message_complete — LLM 已完成本轮生成
//	tool_call_started    — 一次 tool 执行已开始
//	tool_call_output     — tool 的原始结果
//	tool_call_complete   — tool 执行成功结束
//	tool_call_failed     — tool 执行失败
//	observation          — 结果回填给 LLM
//	task_completed       — agent 给出了最终答案
//	task_failed          — agent 失败（错误、取消、超过最大步数）
//	step_complete        — 一个 think 或 tool_call step 已结束
//
// # Persistence（可选）
//
// 当提供 Persistence 实现（如 SQLite 后端）时，engine 在每个阶段后保存
// task 记录、step 记录和对话消息。当 Persistence 为 nil 时，持久化被静默
// 跳过——这对测试或临时性 agent 运行是安全的。
//
// # Multi-Agent 支持
//
// 每个 Engine 实例只运行单个 agent 的 ReAct loop。多 agent 编排在更高层
// （cmd/server）完成，那里会创建多个 Engine 实例并协调其执行。Engine 有意
// 保持单 agent，以使 ReAct loop 简单且可测试。
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/cases"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/observability"
	"github.com/anmingwei/multi-agent-platform/internal/skill"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// regexpRequestID 匹配嵌入在错误信息中的 OpenAI 风格 request id token，例如
// "request id: 20260714033318937918881aclHY4az" 或
// "request_id: req_abc123"。前缀 "request" / "request_id" / "request-id"
// 被捕获，使替换保留 key 同时对易变的 value 做脱敏。供 normalizeErrorFingerprint
// 使用，使重复错误在多次调用间比较相等（见 isRepeatingError）。
//
// 这是 Router-activation 403 死循环的修复方案：provider 返回
// "This token has no access to model deepseek-v4-flash (request id: <opaque>)"
// 每次调用都带新的 opaque id，导致 isRepeatingError 的精确比较永远不匹配，
// engine 在 90s 轮询超时前空转约 1347 次。脱敏 id 使连续 403 比较相等，
// "feedback first, fail on repeat" 策略在第 2 次尝试时即可终止 loop。
var regexpRequestID = regexp.MustCompile(`(?i)(request[-_ ]?id)[:=]\s*[A-Za-z0-9_-]+`)

// resolveProvider 先按 provider 名查、再按 model 名查 provider。
// 它与 Router 路径使用的查找顺序一致，使 fallback 路径和其他调用方可以
// 共享同一套解析逻辑，避免重复。
func resolveProvider(providers map[string]llm.Provider, providerName, modelName string) llm.Provider {
	if providers == nil {
		return nil
	}
	if p, ok := providers[providerName]; ok {
		return p
	}
	if p, ok := providers[modelName]; ok {
		return p
	}
	return nil
}

// EventBus 是连接 Engine 与前端 WebSocket client 的实时事件传输层。
// ReAct loop 中的每个状态变化都通过该 interface 发布，使 UI 能实时渲染
// agent 思考、tool 执行和结果。
//
// EventBus 有意被设计成只含 SendEvent 的最小 interface——这让 engine 可以
// 与任何传输层（WebSocket、gRPC stream、测试用的内存 channel）协同工作，
// 不与具体协议耦合。
//
// 当前架构中，WebSocket hub（internal/ws）实现该 interface，并把事件
// 广播给所有已连接的前端 client。
type EventBus interface {
	// SendEvent 向所有已连接 client 发布一个事件。
	// 事件是类型化的（完整列表见 event 包），携带 task/agent/step 元数据
	// 供前端路由到正确的 UI 面板。
	SendEvent(event.Event)
}

// AgentRole 表示 runtime Engine 在分布式任务中的角色。
// 与 internal/agent.AgentRole 等价，重复定义以避免 runtime 依赖 agent 包。
type AgentRole string

const (
	AgentRoleLeader AgentRole = "leader"
	AgentRoleWorker AgentRole = "worker"
)

// EngineConfig 持有创建并运行一个 Engine 所需的全部配置。
// 它是 agent 身份、模型设置、安全限制和持久化后端的唯一真理来源。
//
// 设计理由：所有配置都是显式的，在构造期传入，而非从全局状态读取。这使
// engine 可以独立测试，并支持每个 agent 拥有不同配置的多 agent 场景。
type EngineConfig struct {
	// AgentID 是该 agent 的可读标识（例如 "code-reviewer"）。
	// 它出现在所有事件中，前端用它给 agent 打标签。
	AgentID string

	// SystemPrompt 是定义 agent 性格、能力和约束的 system 级指令。
	// 它作为每次对话的第一条消息发出，且永远不会从 context 中裁剪。
	SystemPrompt string

	// Model 是 LLM 模型名（例如 "deepseek-v4-flash"）。它被直接传给 API，
	// 必须是配置 endpoint 所支持的模型。
	Model string

	// Endpoint 是 OpenAI-compatible API 的 base URL（例如
	// "https://aicoding.dobest.com/v1"）。Engine 会在其后追加
	// "/chat/completions" 用于 chat 请求。
	Endpoint string

	// APIKey 是用于向 LLM API 认证的 Bearer token。
	// 每次请求都以 Authorization header 发送。
	//
	// Deprecated：优先使用 Provider。当 Provider 为 nil 时，Endpoint 和
	// APIKey 用于创建默认的 OpenAIProvider。在 Phase 6+ 中，这些字段将
	// 被 Provider 抽象取代。
	APIKey string

	// Provider 是 LLM Provider 实现。设置后优先于
	// Endpoint/APIKey/Model。这支持多 provider 场景——不同 agent 使用不同
	// provider（OpenAI、Anthropic、DeepSeek 等）。为 nil 时，会从 Endpoint、
	// APIKey 和 Model 创建一个 OpenAIProvider。
	// 于 Phase 5 引入。
	Provider llm.Provider

	// CaseID 是可选的提示，传递给 llm.ChatRequest。MockProvider 用它做
	// 确定性脚本匹配（先精确匹配 case，再按关键字回退）。真实 provider
	// 完全忽略此字段。为空时，MockProvider 回退到按 user input 做关键字
	// 匹配。
	// 于 Phase 6 mock 集成引入。
	CaseID string

	// Temperature 控制 LLM 输出的随机性（0.0–2.0）。
	// 值越低输出越确定性；值越高输出越有创造性/多样性。未设置时默认 0.7。
	Temperature float32

	// MaxTokens 是 LLM 单次响应最多可生成的 token 数。
	// 作为安全限制防止 token 失控消耗。未设置时默认 4096。
	MaxTokens int

	// MaxSteps 是 ReAct loop 迭代次数上限，超过即强制终止。这能防止 LLM
	// 一直调用 tool 却从不给出最终答案的死循环。未设置时默认 10。
	MaxSteps int

	// Persistence 是可选的 task/step/conversation 记录持久化后端。
	// 为 nil 时静默跳过持久化——适合测试或临时性运行。
	// 设置后（如 SQLite 后端），每条 step 和 message 都会持久存储，便于
	// 后续审计和回放。
	Persistence Persistence

	// PolicyGate 是可选的 Harness policy 强制层。设置后，每次 tool call 在
	// 执行前都会经过 policy chain 检查。为 nil 时跳过 policy 强制——所有
	// tool call 都允许。完整 PolicyGate 实现见 internal/harness。
	PolicyGate *harness.PolicyGate

	// ProgressManager 是可选的 Harness 进度跟踪。设置后，关键里程碑
	// （tool call、step 完成、任务完成）会写入外部 progress 文件，跨崩溃
	// 保留。
	Progress *harness.ProgressManager

	// TaskContract 是结构化的任务定义，定义本任务的作用域、权限、预算和
	// 验收标准。供 PolicyGate 强制执行、Progress 跟踪使用。
	Contract harness.TaskContract

	// SessionID 标识本 task 所属的 session。
	// 尚未关联到 session 的 task 为空。
	SessionID string

	// WorkspaceDir 是 session 级的工作目录。
	// 当用户未显式提供 workdir 时，它会被注入到 tool call 输入（作为
	// "workdir"），使 run_shell 之类的 tool 无需 LLM 显式传递即可在正确
	// CWD 下执行。
	WorkspaceDir string `json:"-"`

	// ParentTaskID 标识由 agent 派生的子任务的父任务。
	// root task 为空。
	ParentTaskID string

	// SubTaskID 标识本 agent 的具体执行实例。
	// leader agent 的 SubTaskID 等于 TaskID；子 agent 的 SubTaskID 由
	// root task 加上子 agent ID 派生。事件和 context-window snapshot 以
	// SubTaskID 为键，使每个 agent 实例都可独立观测。
	SubTaskID string

	// IsRoot 表示本 task 是否为其 session 的 root task。
	// root task 代表主要的用户请求；child task 代表从 root 委派出去的
	// 子 agent 工作。
	IsRoot bool

	// ApprovalHandler 是可选的 Harness 审批 handler。设置后，Engine 可以
	// 处理来自 PolicyGate 的 ErrApprovalRequired 错误——向前端发送审批
	// 请求并等待用户决定。为 nil 时，ErrApprovalRequired 错误立即导致
	// 任务失败。ApprovalHandler interface 见 internal/harness。
	// 于 Phase 5 引入。
	ApprovalHandler harness.ApprovalHandler

	// WorkingMemory 是来自先前任务的可选上下文，在 agent 启动前注入到
	// system prompt 中。它由 MemoryRecall（internal/harness/recall.go）
	// 在 engine 创建前构建。设置后会前置到 system prompt 中，使 agent
	// 无需用户重复即可访问过往经验和稳定的语义规则。
	WorkingMemory string

	// AgentBus 是 agent 间通信通道。设置后，agent 可以在 ReAct loop 期间
	// 向其他 agent 发送消息或接收消息。为 nil 时禁用 agent 间通信。
	//
	// AgentBus 必须是 goroutine 安全的。具体实现位于
	// internal/orchestrator；interface 定义在 runtime/agentbus.go 以避免
	// 循环引用。
	//
	// 于 Phase 5 引入。
	AgentBus AgentBus

	// CheckpointManager 是可选的 checkpoint/recovery 管理器。设置后，
	// engine 在每次 ReAct loop 迭代结束（tool 执行后）保存一个 checkpoint，
	// 支持崩溃后恢复任务。为 nil 时跳过 checkpointing。
	//
	// 于 Phase 5 引入。
	CheckpointManager *CheckpointManager

	// SessionMessageWriter 在对话新增一条消息时被调用。设置后，每条消息
	// （system/user/assistant/tool）都会被持久化到 session_messages 表，
	// 支持多轮对话历史。为 nil 时跳过 session message 持久化。
	//
	// 这是一个 best-effort 持久化层——writer 返回的错误只会被记录，不会
	// 中断 engine 执行。EngineConfig 中的 TurnIndex 字段控制消息归属的
	// turn。
	SessionMessageWriter func(msg SessionMessageRecord) error

	// TurnIndex 是 session 内的当前 turn 序号（0-based）。
	// 用来给 session_messages 打上所属 turn 的标签。调用方应在 user turn
	// 之间递增该值（例如每次 Engine.Run() 返回后）。
	TurnIndex int

	// Router 是可选的 LLM 模型路由器。设置后，Engine 在每个 think step
	// 根据用户意图和任务上下文选择最佳模型。Router 会分类请求、映射到
	// 模型层级、并选出主模型加 fallback。为 nil 时，Engine 直接使用
	// cfg.Model（旧行为）。
	// 于 Phase 6 引入。
	Router *llm.Router

	// Registry 是可选的模型注册表。Router 设置时必填——Router 通过
	// registry 按层级和能力选择模型。Router 为 nil 时此字段被忽略。
	// 于 Phase 6 引入。
	Registry *llm.ModelRegistry

	// Providers 是 provider 名 → Provider 实例的 map，Router 用它查找
	// 所选模型 profile 对应的 provider。Router 设置时，必须包含 Router
	// 可能选中的所有模型的条目。Router 为 nil 时此字段被忽略。
	// 于 Phase 6 引入。
	Providers map[string]llm.Provider

	// Timeout 是可选的单任务执行截止时间。非零时，调用方（cmd/server）
	// 据此创建 context.WithTimeout；为零时不设截止时间。Engine 通过传入
	// 的 context 间接消费 timeout，不自行强制截止。若 context 到期，
	// Engine 返回 context.DeadlineExceeded，调用方发出 task_timeout 失败
	// 事件。
	Timeout time.Duration

	// Role 表示当前 Engine 运行的是 leader 还是 worker。
	// 由 cmd/server 在创建 root agent 或 orchestrator 在创建子 agent 时设置。
	// 工具层可通过该字段判断当前 agent 是否有权调用 leader 专用工具（如
	// dispatch_sub_agent）。
	// Phase 7-H 引入。
	Role AgentRole

	// CanDispatchSubAgents 标记当前 agent 是否可以调用 dispatch_sub_agent。
	// 仅在 Role 为 leader 时允许置 true。
	// Phase 7-H 引入。
	CanDispatchSubAgents bool

	// CanDefineWorkflow 标记当前 agent 是否允许自定义工作流。
	// leader 可以定义；worker 由父级 orchestrator 决定。
	// Phase 7-H 引入。
	CanDefineWorkflow bool

	// SupervisorSubTaskID 是当前 agent 的父级/监督 agent 的子任务 ID。
	// worker 由 orchestrator 指向 root task；leader 留空。
	// Phase 7-H 引入。
	SupervisorSubTaskID string

	// ApproverMode 决定高风险审批由谁处理："user" 或 "leader"。
	// Phase 7-H 占位用，Phase I 详细实现。
	ApproverMode string

	// SupervisorDecisionHandler 是 worker 在 ApproverMode="leader" 时把高风险
	// 审批请求委托给 supervisor leader 的回调。实现者由 cmd/server 注入，
	// 负责查找 supervisor Engine 并等待其通过 approve/reject_sub_agent_action
	// 工具做出决定。
	// Phase 7-I 引入。
	SupervisorDecisionHandler ApprovalDelegationHandler

	// Tracer 为每个 think/tool/llm step 生成无依赖的 trace span。
	// 为 nil 时跳过 tracing。
	Tracer interface {
		StartRoot(taskID, operation string) *observability.TraceContext
		StartChild(parent *observability.TraceContext, agentID, operation string) *observability.TraceContext
		Finish(ctx *observability.TraceContext, err error)
		FinishWithAttributes(ctx *observability.TraceContext, err error, attrs map[string]any)
	}

	// RootTraceCtx 是该 task 的 root span context。Tracer 用它作为本 engine
	// 发出的所有 child span 的父级。
	RootTraceCtx *observability.TraceContext

	// LLMLatencyRecorder 在每次 LLM 调用后以观测到的延迟被调用。
	LLMLatencyRecorder func(latency time.Duration)

	// ToolLatencyRecorder 在每次 tool 执行后以观测到的延迟被调用。
	ToolLatencyRecorder func(latency time.Duration)

	// OnLLMUsage 是可选回调，在 ReAct loop 中每次成功 LLM 调用后被调用。
	// 它接收实际选中的模型（Router 启用时可能与 cfg.Model 不同）、解析得到
	// 的 ModelProfile 和 API 返回的 Usage。该回调在 Phase 6-D 中由 cost
	// tracker 和 metrics collector 使用，且不把 Engine 与这些子系统耦合。
	//
	// 该回调是 best-effort 的：panic 会被 recover 并记录；回调返回的错误
	// 不会中断 ReAct loop。
	OnLLMUsage OnLLMUsage

	// EvaluationRepository 是可选的 cases.Repository，用于在任务完成时
	// 持久化验收评估结果。为 nil 时，评估结果仍会通过 task_evaluated 事件
	// 广播，但不会被持久存储。
	EvaluationRepository EvaluationRepository

	// SkillRegistry 是可选的 skill 注册表。与 ActiveSkills 一起设置时，
	// Engine 会把渲染后的 skill prompt 注入到 system prompt。
	SkillRegistry *skill.Registry

	// ActiveSkills 是待注入到 system prompt 的 skill ID 列表。
	ActiveSkills []string

	// SkillVariables 提供用于渲染 skill 模板的变量值。
	SkillVariables map[string]any

	// ActiveTodos 是可选的当前 session 待办列表文本（已格式化）。
	// 当非空时，NewEngine 会把它追加到 system prompt 中，让 LLM 了解
	// 当前 session 的未完成 TODO。该字段由 cmd/server 在构造 EngineConfig
	// 前调用 todo.FormatActiveTodos 生成；runtime 包不直接依赖 internal/todo，
	// 从而避免 import cycle。
	// Phase 7 TODO 子系统引入。
	ActiveTodos string
}

// OnLLMUsage 是每次成功 LLM 调用后被调用的回调类型。
type OnLLMUsage func(model string, profile *llm.ModelProfile, usage llm.Usage)

// EvaluationRepository 是可选的 cases.Repository，用于在任务完成时持久化
// 验收评估结果。为 nil 时，评估结果仍会通过 task_evaluated 事件广播，但
// 不会被持久存储。
type EvaluationRepository interface {
	SaveEvaluation(eval cases.CaseEvaluation) error
}

// CaseEvaluator 根据某个 case 的验收标准评估任务结果。
// 以 interface 形式定义，以避免 runtime 直接耦合 cases 包的实现细节。
type CaseEvaluator interface {
	Evaluate(taskID string, input string, result string) (cases.CaseEvaluation, error)
}

// Engine 为单个 agent 执行 ReAct (Reasoning + Acting) loop。
//
// # 生命周期
//
// Engine 通过 NewEngine 创建，随后调用一次 Run() 传入 user input。engine
// 通过 ReAct loop 处理输入并返回最终答案（或错误）。Run() 返回后，
// Engine 不应被复用——每个任务都应新建 Engine。
//
// # 状态
//
// Engine 以 []llm.Message 维护完整对话历史。包括：
//   - system：agent 的 system prompt（创建时一次性设置）
//   - user：初始 user input + 任何中间 user message
//   - assistant：LLM 响应（文本内容 + tool call）
//   - tool：tool 执行结果（JSON 序列化）
//
// stepIdx 计数器追踪当前 ReAct loop 迭代。从 0 开始，每次 tool 执行后
// 递增。当 stepIdx 达到 MaxSteps 或 LLM 产出最终答案时，engine 终止。
//
// # 事件流
//
// 每个重要状态变化都以事件形式通过 EventBus 发出：
//
//	agent_ready           → step_started → llm_thinking → llm_delta* →
//	llm_message_complete  → step_complete → [tool_call_started → tool_call_output →
//	tool_call_complete → observation → step_complete]* → task_completed
//
// * 后缀表示可能多次重复的事件（流式 token、多个 tool call、多轮 loop
// 迭代）。
type Engine struct {
	cfg               EngineConfig                     // 创建时设置的不可变配置
	llm               llm.Provider                     // LLM Provider interface（抽象 API 协议）
	tools             *tool.Registry                   // 跨 agent 共享的 tool 注册表
	bus               EventBus                         // 用于实时前端更新的事件传输层
	persist           Persistence                      // 可选持久化后端（nil = 不持久化）
	gate              *harness.PolicyGate              // 可选 policy 强制（nil = 全部允许）
	progress          *harness.ProgressManager         // 可选进度跟踪（nil = 跳过）
	taskProgress      *harness.TaskProgress            // 当前进度状态（progress 为 nil 时为 nil）
	taskID            string                           // 用于关联的唯一 task 标识
	messages          []llm.Message                    // 完整对话历史（system + user + assistant + tool）
	stepIdx           int                              // 当前 ReAct loop 迭代号（0-based）
	totalTokens       int                              // 所有 LLM 调用累计的 token 数
	tokenUsage        llm.Usage                        // 累计的详细 token 用量（input/cache/output）
	startTime         time.Time                        // 任务起始时间，用于耗时跟踪
	durationMs        int64                            // 任务总耗时（毫秒）
	approvalHandler   harness.ApprovalHandler          // 可选的 ErrApprovalRequired 审批 handler
	agentBus          AgentBus                         // 可选的 agent 间通信通道（nil = 禁用）
	checkpoint        *CheckpointManager               // 可选的崩溃恢复 checkpoint 管理器（nil = 禁用）
	sessionMsgWriter  func(SessionMessageRecord) error // 可选的 session message writer（nil = 跳过）
	turnIndex         int                              // session 内的当前 turn 序号（0-based）
	caseID            string                           // 可选的 case ID 提示，供 MockProvider 脚本匹配
	providers         map[string]llm.Provider          // Router 决策用的 provider 查找 map（空 = 未启用 Router）
	selectedModel     string                           // Router 为当前 think step 选中的模型（空 = e.cfg.Model）
	lastError         string                           // 最近一次回喂给 LLM 的可恢复错误的归一化指纹
	consecutiveErrors int                              // 同一个可恢复错误连续出现的次数
	rootTraceCtx      *observability.TraceContext      // 该 task 的 root span context

	// Pause/Resume 控件（Phase 7-A）：让前端可以在不取消 context 的情况下暂停 agent。
	// paused 是一个 atomic.Bool，Run loop 每轮检查一次；resumeCh 用来唤醒阻塞中的 loop。
	// resumeMu 保护 resumeCh 的 close/reopen 动作，避免并发触发 Resume 时出现
	// "close of closed channel" panic。
	paused   atomic.Bool
	resumeCh chan struct{}
	resumeMu sync.Mutex
}

// NewEngine 根据给定配置、tool 注册表、event bus 和 task ID 创建一个
// 新的 Engine。
//
// 应用的默认值：
//   - MaxSteps 默认 10（防止死循环）
//   - Temperature 默认 0.7（平衡创造性与确定性）
//   - MaxTokens 默认 4096（多数模型的合理安全上限）
//
// engine 初始化时把 system prompt 作为第一条消息加入对话。user input 会在
// Run() 调用时追加。
//
// LLM provider 按 engine 单独创建（不共享），使每个 agent 可以使用不同的
// endpoint、API key 或模型——这对不同 agent 可能对接不同 LLM provider 的
// 多 agent 场景至关重要。
//
// 若 cfg.Provider 已设置，则直接使用（启用自定义 provider）。否则从
// cfg.Endpoint、cfg.APIKey、cfg.Model 创建一个 OpenAIProvider。
func NewEngine(cfg EngineConfig, tools *tool.Registry, bus EventBus, taskID string) *Engine {
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 30
	}
	if cfg.MaxSteps > 200 {
		cfg.MaxSteps = 200
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}

	// 解析 LLM Provider：若显式设置了 Provider 则直接用，否则从旧的
	// Endpoint/APIKey/Model 字段创建一个默认 OpenAIProvider。
	provider := cfg.Provider
	if provider == nil {
		provider = llm.NewOpenAIProvider("openai", cfg.Endpoint, cfg.APIKey, cfg.Model)
	}

	// 解析 system prompt。如果提供了 WorkingMemory（由 MemoryRecall 在
	// engine 创建前构建），就前置到 system prompt 中，使 agent 能访问过往
	// 经验和稳定的语义规则。
	systemPrompt := cfg.SystemPrompt
	if cfg.WorkingMemory != "" {
		systemPrompt = cfg.WorkingMemory + "\n\n" + cfg.SystemPrompt
	}

	// 当 session 绑定了 workspace 时，向 system prompt 注入工作目录指引。
	// 这告诉 LLM 所有文件操作使用相对路径——文件天然基于该目录解析，所以
	// LLM 可以直接写 `snake_game.html` 而无需知道绝对路径。这不改变任何
	// tool 机制，仅是 prompt 层面的提示。WorkspaceDir 为空（旧行为）时
	// 不追加任何内容。
	if cfg.WorkspaceDir != "" {
		systemPrompt += "\n\n## Working Directory\n"
		systemPrompt += "Your working directory for all file operations is: " + cfg.WorkspaceDir + "\n"
		systemPrompt += "IMPORTANT: When using `write_file` or `read_file`, always use relative paths only "
		systemPrompt += "(e.g. \"snake_game.html\", \"src/main.go\"). Do NOT prepend directory segments or use "
		systemPrompt += "absolute paths — the system resolves all relative paths against this working directory.\n"
	}

	// 当配置了 skill 注册表和激活的 skill 时，把渲染后的 skill prompt
	// 注入 system prompt。仅追加名为 "system_prompt" 或 "task_prompt" 的
	// 模板，使用配置的 SkillVariables 渲染。这让 agent 无需修改 base
	// system prompt 即可动态扩展其指令。
	if cfg.SkillRegistry != nil && len(cfg.ActiveSkills) > 0 {
		renderer := skill.NewRenderer()
		var rendered []string
		for _, id := range cfg.ActiveSkills {
			s, ok := cfg.SkillRegistry.Get(id)
			if !ok {
				continue
			}
			for _, tmpl := range s.Templates {
				if tmpl.Name == "system_prompt" || tmpl.Name == "task_prompt" {
					rendered = append(rendered, renderer.Render(tmpl, cfg.SkillVariables))
				}
			}
		}
		if len(rendered) > 0 {
			systemPrompt += "\n\n## Skill Instructions\n\n" + strings.Join(rendered, "\n\n")
		}
	}

	// Phase 7 TODO: 把当前 session 的 active TODO 列表注入 system prompt。
	// 空字符串表示无 active todo，不追加任何内容，避免污染 prompt。
	if cfg.ActiveTodos != "" {
		systemPrompt += "\n\n" + cfg.ActiveTodos + "\n"
		systemPrompt += "You can use the todo/* tools to manage these todos (create, update status, list, delete, etc.).\n"
	}

	return &Engine{
		cfg:              cfg,
		llm:              provider,
		tools:            tools,
		bus:              bus,
		persist:          cfg.Persistence,
		gate:             cfg.PolicyGate,           // nil = 不强制 policy
		progress:         cfg.Progress,             // nil = 不跟踪进度
		agentBus:         cfg.AgentBus,             // nil = 无 agent 间通信
		checkpoint:       cfg.CheckpointManager,    // nil = 无 checkpoint/recovery
		sessionMsgWriter: cfg.SessionMessageWriter, // nil = 跳过 session message 持久化
		turnIndex:        cfg.TurnIndex,            // session 内的 turn 序号
		caseID:           cfg.CaseID,               // mock 脚本匹配用的 case ID 提示
		taskID:           taskID,
		rootTraceCtx:     cfg.RootTraceCtx, // 该 task 的 root span context
		messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
		},
		stepIdx:           0,
		totalTokens:       0,
		tokenUsage:        llm.Usage{},
		startTime:         time.Now(),
		durationMs:        0,
		approvalHandler:   cfg.ApprovalHandler, // nil = 不支持审批
		providers:         cfg.Providers,       // Router 决策用的 provider 查找 map
		lastError:         "",
		consecutiveErrors: 0,
		resumeCh:          make(chan struct{}),
	}
}

// Run 对给定 user input 执行 ReAct loop，返回最终答案、总 token 消耗和
// 错误。
//
// # ReAct Loop（分步说明）
//
// 该 loop 直到满足以下三种终止条件之一才退出：
//  1. LLM 返回不含 tool_calls 的响应 → 最终答案（成功）
//  2. stepIdx 达到 MaxSteps → 强制终止（失败）
//  3. context 被取消 → 优雅关闭（失败）
//  4. recover 到一个 panic → 紧急关闭（失败）
//
// 每次迭代之间都会检查 context 是否被取消。这让调用方可以取消长时间运行
// 的 agent（例如用户在 UI 中点击 "stop"）。
//
// # Panic Recovery
//
// engine 在 Run() 顶部通过 defer recover() 捕获来自任意层（LLM client、
// tool 执行、event bus、持久化）的 panic。捕获到 panic 时，engine 发出
// 带 panic 详情的 task_failed 事件，让前端能展示错误，然后重新 panic 以
// 保留堆栈供调试。这确保了单个有 bug 的 tool 或 nil 指针不会静默杀掉
// agent——前端总是知道发生了什么。
//
// # 返回值
//
//   - content：LLM 给出的最终答案文本（失败时为空）
//   - totalTokens：所有 LLM 调用累计的 token 数（失败时为 0）
//   - error：成功时为 nil，失败时为描述性错误
func (e *Engine) Run(ctx context.Context, userInput string) (content string, totalTokens int, err error) {
	// Panic recovery：捕获来自 LLM client、tool 执行、event bus 或持久化层
	// 的任何 panic。发出 task_failed 事件让前端知道 agent 崩溃，然后重新
	// panic 以保留堆栈。
	defer func() {
		if r := recover(); r != nil {
			// 发出带 panic 详情的 task_failed 事件，让 UI 能展示错误。
			// 该事件以 best-effort 方式发送——若 event bus 自身 panic 了，
			// 这次发送也可能失败，但我们仍然尝试。
			e.bus.SendEvent(event.NewEventWithSubTask("task_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"reason": "panic",
				"error":  fmt.Sprintf("%v", r),
			}))
			// 持久化失败状态，使任务历史显示为 failed。
			e.updateTask("failed", "", e.totalTokens)
			// 任务失败后清理内存中的上下文窗口快照，避免内存无限累积。
			DeleteTaskContextSnapshot(e.cfg.SubTaskID)
			// 重新 panic 以保留原始堆栈供服务端日志记录。
			// 该 panic 会被 HTTP server 的 recovery 中间件或调用方的
			// deferred recovery 捕获。
			panic(r)
		}
	}()

	// 把用户输入追加到对话历史。这是 ReAct loop 的起点——LLM 会看到
	// system prompt 后跟这条 user message。
	e.messages = append(e.messages, llm.Message{Role: "user", Content: userInput})

	// 持久化 user message 以便审计链路和对话回放。
	e.saveConversation("user", userInput)

	// 把 system prompt 和 user message 写入 session_messages 以支持多轮
	// 对话历史。system prompt 总是 e.messages 中的第一条消息。
	// 这些写入是 best-effort 的——失败只会被记录，不会中断 engine。
	e.writeSessionMessage("system", e.messages[0].Content, "", "", 0)
	e.writeSessionMessage("user", userInput, "", "", 0)

	// 若配置了 Harness progress 跟踪则初始化
	if e.progress != nil {
		tp, err := e.progress.Init(e.taskID, e.cfg.Contract)
		if err != nil {
			log.Printf("[Engine] Progress init failed: %v (continuing)", err)
		} else {
			e.taskProgress = tp
		}
	}

	// 通知前端 agent 已初始化完成、可处理输入。UI 用此事件展示 agent
	// 名称、模型和限制。
	e.bus.SendEvent(event.NewEventWithSubTask("agent_ready", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, 0, map[string]any{
		"agent_name": e.cfg.AgentID,
		"model":      e.cfg.Model,
		"max_steps":  e.cfg.MaxSteps,
		"session_id": e.cfg.SessionID,
		"is_root":    e.cfg.IsRoot,
	}))

	// 若配置了 AgentBus，启动 AgentBus listener goroutine。
	// 该 goroutine 监听来自其他 agent 的到达消息，并把它作为 user message
	// 追加到对话中。它与 ReAct loop 并发运行，在 context 取消时停止。
	agentMsgCh := make(chan AgentMessage, 10)
	agentBusDone := make(chan struct{})
	if e.agentBus != nil {
		agentBusHandler := func(msg AgentMessage) {
			select {
			case agentMsgCh <- msg:
			default:
				// channel 已满——丢弃消息以免阻塞发送方。
				// 这是一个安全措施；实际上 channel 应该足够大以应对突发。
			}
		}
		// Phase 7-H2 阶段 6 (MA7)：只要 SubTaskID 非空，就按 (agentID, subTaskID)
		// 精确注册——无论 leader 还是 worker。此前 worker 走 agentID-only 注册，
		// 导致两个并发 session 跑同名 worker（例如两个 "agent_writer"）时，后注册
		// 的 handler 会覆盖前者，跨 session 的 AgentBus 消息被投递到错误的 engine。
		// 按 SubTaskID 注册后，SendMessage 的精确匹配优先级保证每个 worker 实例
		// 只收到发往自己 subTaskID 的消息。SubTaskID 为空（legacy/测试路径）时
		// 保留 agentID-only 行为。
		if e.cfg.SubTaskID != "" {
			e.agentBus.RegisterHandlerBySubTask(e.cfg.AgentID, e.cfg.SubTaskID, agentBusHandler)
		} else {
			e.agentBus.RegisterHandler(e.cfg.AgentID, agentBusHandler)
		}
		go func() {
			defer close(agentBusDone)
			for {
				select {
				case <-ctx.Done():
					// context 取消——停止监听。
					if e.cfg.SubTaskID != "" {
						e.agentBus.UnregisterHandlerBySubTask(e.cfg.AgentID, e.cfg.SubTaskID)
					} else {
						e.agentBus.UnregisterHandler(e.cfg.AgentID)
					}
					return
				case msg, ok := <-agentMsgCh:
					if !ok {
						return
					}
					// Phase 7-J：把 AgentBus 输入当作一个独立 step，让前端时间线
					// 能展示跨 agent 通信。先发射 step_started，随后按用户消息处理，
					// 最后发射 step_complete 并持久化该 step。
					e.bus.SendEvent(event.NewEventWithSubTask("step_started", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
						"type":             "agent_message_input",
						"from_agent":       msg.FromAgentID,
						"from_sub_task_id": msg.SubTaskID, // 对旧消息可能为空
						"to_agent":         e.cfg.AgentID,
						"to_sub_task_id":   e.cfg.SubTaskID,
						"msg_type":         msg.Type,
						"content":          msg.Content,
					}))

					// 把到达消息作为 user message 追加到对话。LLM 会把它视为
					// 来自其他 agent 的新输入。
					formatted := fmt.Sprintf("[Agent %s]: %s", msg.FromAgentID, msg.Content)
					e.messages = append(e.messages, llm.Message{Role: "user", Content: formatted})
					e.saveConversation("user", formatted)
					e.writeSessionMessage("user", formatted, "", "", 0)

					// 发出 system_info 事件，让前端可以在 UI 中展示 agent 间通信。
					// Phase 7-J 注：保留此事件以兼容旧前端监听器，但 step 事件才是
					// 推荐的白盒展示方式（system_info 已标记为 deprecated）。
					e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
						"type":       "agent_message_received",
						"from_agent": msg.FromAgentID,
						"to_agent":   e.cfg.AgentID,
						"msg_type":   msg.Type,
						"content":    msg.Content,
					}))

					e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
						"type": "agent_message_input",
					}))
					e.saveStep(StepRecord{
						TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
						Type: "agent_message_input", Status: "completed", Content: formatted,
					})
				}
			}
		}()
	}

	// =========================================================================
	// REACT LOOP: THINK → TOOL_CALL → OBSERVE → (repeat)
	// =========================================================================
	// 该 loop 的每次迭代是 agent 推理链中的一个 "step"。loop 在 LLM 产出
	// 最终答案（无 tool call）、达到 MaxSteps 或 context 取消时终止。
	//
	// stepIdx 计数器在 think 阶段不递增——仅在 tool 执行后递增。这意味着
	// stepIdx 反映的是 tool 执行轮数，而非 LLM 调用次数。最终答案
	// （无 tool call 的 think 阶段）使用当前 stepIdx，不会递增。
	for e.stepIdx < e.cfg.MaxSteps {
		// Phase 7-A：暂停检查。
		// 如果用户从前端触发了 Pause，paused 会被原子地置为 true，
		// 这里在每轮循环开头阻塞等待 resumeCh 或者 ctx 取消。
		// 注意：pause 不取消 context，所以 LLM 流式连接不会被中途打断，
		// 这与 cancel 行为（走 ctx.Done 分支）有明显区分。
		if e.paused.Load() {
			select {
			case <-e.resumeCh:
				// Resume 已触发，继续下一轮。
			case <-ctx.Done():
				// 在暂停期间用户取消了任务，触发正常的取消逻辑。
				e.bus.SendEvent(event.NewEventWithSubTask("task_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
					"reason": "cancelled",
				}))
				e.durationMs = time.Since(e.startTime).Milliseconds()
				e.updateTask("failed", "", 0)
				e.updateTaskDuration()
				return "", e.totalTokens, ctx.Err()
			}
		}

		// 每次迭代前检查 context 是否取消。这让 HTTP handler 可以在 client
		// 断开或用户点击 "stop" 时取消 agent。若不检查，engine 会在前端
		// 已放弃后仍继续处理。
		select {
		case <-ctx.Done():
			// context 被取消——发出失败事件并立即返回。
			// 前端可凭事件数据中的 reason 字段区分 "cancelled"、
			// "llm_error" 和 "max_steps_exceeded"。
			e.bus.SendEvent(event.NewEventWithSubTask("task_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"reason": "cancelled",
			}))
			e.durationMs = time.Since(e.startTime).Milliseconds()
			e.updateTask("failed", "", 0)
			e.updateTaskDuration()
			// 任务取消后清理内存中的上下文窗口快照，避免快照无限累积。
			DeleteTaskContextSnapshot(e.cfg.SubTaskID)
			return "", e.totalTokens, ctx.Err()
		default:
			// context 仍然有效——继续进入 think 阶段。
		}

		// =====================================================================
		// PHASE 1: THINK — 把对话发给 LLM 并获取响应。
		// =====================================================================
		// LLM 接收完整对话历史（system + user + assistant + tool message），
		// 并返回：
		//   a) 文本内容且无 tool call → 这就是最终答案
		//   b) 文本内容且有 tool call → LLM 想用某个 tool
		//
		// 在该阶段，文本 token 通过 llm_delta 事件流式发送到前端，在 UI 中
		// 形成打字机效果。
		content, usage, toolCalls, err := e.think(ctx)
		if err != nil {
			// 区分取消与真正的 LLM 错误。如果 context 已被取消（例如用户点击
			// 了 stop），loop 头部已经发出过 cancelled reason；此处直接返回
			// 不覆盖为 llm_error。
			select {
			case <-ctx.Done():
				return "", e.totalTokens, ctx.Err()
			default:
			}

			// -----------------------------------------------------------------
			// 错误处理策略：feedback first, fail on repeat。
			//
			// 任何系统级错误（LLM 调用失败、网络问题等）都会先作为 observation
			// 回喂给 LLM。这给 agent 一次自我修正并继续工作的机会。只有当
			// 同一错误指纹连续出现两次时，我们才视为不可恢复并升级到人工
			// 介入。
			//
			// 指纹经过归一化（normalizeErrorFingerprint），使 provider 错误
			// 信息中易变的 per-request id（如 403 "request id: <opaque>"）
			// 不会掩盖真正的重复——否则持续的 403 会一直空转到轮询超时，而
			// 不是快速失败。
			// -----------------------------------------------------------------
			obsContent := fmt.Sprintf("[LLM ERROR] %s", normalizeErrorFingerprint(err.Error()))
			if e.isRepeatingError(obsContent) {
				e.bus.SendEvent(event.NewEventWithSubTask("task_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
					"reason": "llm_error",
					"error":  err.Error(),
				}))
				e.durationMs = time.Since(e.startTime).Milliseconds()
				e.updateTask("failed", "", e.totalTokens)
				e.updateTaskDuration()
				// 重复 LLM 错误导致任务失败，清理上下文窗口快照以释放内存。
				DeleteTaskContextSnapshot(e.cfg.SubTaskID)
				return "", e.totalTokens, fmt.Errorf("think step %d: repeated LLM error: %w", e.stepIdx, err)
			}
			e.recordFeedbackError(obsContent)

			// 把错误回填到对话，使下一个 think step 能据此反应。作为
			// user message 持久化以便审计。
			e.messages = append(e.messages, llm.Message{Role: "user", Content: obsContent})
			e.saveConversation("user", obsContent)
			e.writeSessionMessage("user", obsContent, "", "", 0)

			// 发出 system_info 事件，让前端可以展示这次重试。
			e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"type":    "llm_error_feedback",
				"content": obsContent,
			}))
			continue
		}

		// 累计 token 用量以做预算跟踪（TokenBudgetRule — Phase 4）
		e.totalTokens += usage.TotalTokens
		e.tokenUsage.PromptTokens += usage.PromptTokens
		e.tokenUsage.PromptCacheHitTokens += usage.PromptCacheHitTokens
		e.tokenUsage.PromptCacheMissTokens += usage.PromptCacheMissTokens
		e.tokenUsage.CompletionTokens += usage.CompletionTokens
		e.tokenUsage.TotalTokens += usage.TotalTokens

		// 把最新 token 用量同步给 PolicyGate，使 TokenBudgetRule 能在下一次
		// tool 执行前强制预算。
		if e.gate != nil {
			e.gate.SetTokenUsage(e.totalTokens)
		}

		// 发出 agent_status 事件，让前端展示实时 token 消耗详情
		// （input / cache / output）和进度。
		e.bus.SendEvent(event.NewEventWithSubTask("agent_status", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"prompt_tokens":            e.tokenUsage.PromptTokens,
			"prompt_cache_hit_tokens":  e.tokenUsage.PromptCacheHitTokens,
			"prompt_cache_miss_tokens": e.tokenUsage.PromptCacheMissTokens,
			"completion_tokens":        e.tokenUsage.CompletionTokens,
			"total_tokens":             e.tokenUsage.TotalTokens,
			"max_steps":                e.cfg.MaxSteps,
			"current_step":             e.stepIdx,
		}))

		// Phase 6-D：上报本次 LLM 调用的成本与可观测性指标。
		// Engine 本身与成本无关；回调由 cmd/server 提供并接到
		// CostTracker + MetricsCollector。回调中的 panic 会被 recover，
		// 这样可观测性 bug 不会让 agent loop 崩溃。
		//
		// R4 修复：reporting 用的 model 名取 think() 顶部 Router 选中的
		// selectedModel（若 Router 未启用则为 e.cfg.Model）。此前固定传
		// e.cfg.Model，当 Router 把本次调用路由到别的模型时 cost 记录的 model
		// 名与实际调用不一致。fallback 到 e.cfg.Model 保证未启用 Router 的旧
		// 链路行为不变。
		reportModel := e.selectedModel
		if reportModel == "" {
			reportModel = e.cfg.Model
		}
		if e.cfg.OnLLMUsage != nil {
			var profile *llm.ModelProfile
			if e.cfg.Registry != nil {
				profile = e.cfg.Registry.Get(reportModel)
			}
			if profile == nil {
				profile = &llm.ModelProfile{
					Name:        reportModel,
					Provider:    "unknown",
					InputPrice:  0,
					OutputPrice: 0,
				}
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[Engine] OnLLMUsage callback panicked: %v", r)
					}
				}()
				e.cfg.OnLLMUsage(reportModel, profile, usage)
			}()
		}

		log.Printf("[Engine] Step %d: content=%d chars, toolCalls=%d, selectedModel=%s, usage=%+v",
			e.stepIdx, len(content), len(toolCalls), reportModel, usage)

		// =====================================================================
		// CHECK：LLM 是给出了最终答案还是请求 tool call？
		// =====================================================================
		// 如果没有 tool call，LLM 的文本内容就是最终答案。
		// 这是 agent 成功运行的正常终止路径。
		if len(toolCalls) == 0 {
			// 持久化最终 step 以便审计链路。
			e.saveStep(StepRecord{
				TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
				Type: "think", Status: "completed", Content: content, TokenUsed: e.totalTokens,
			})
			e.saveConversation("assistant", content)
			e.writeSessionMessage("assistant", content, "", "", e.totalTokens)

			// 发出最终 observation——完整答案文本及 token 用量统计。前端用
			// 它展示最终答案和 token 成本汇总。
			e.bus.SendEvent(event.NewEventWithSubTask("observation", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"content":                  content,
				"total_tokens":             e.totalTokens,
				"prompt_tokens":            e.tokenUsage.PromptTokens,
				"prompt_cache_hit_tokens":  e.tokenUsage.PromptCacheHitTokens,
				"prompt_cache_miss_tokens": e.tokenUsage.PromptCacheMissTokens,
				"completion_tokens":        e.tokenUsage.CompletionTokens,
			}))

			// 持久化最终 observation step 以支持历史回放。
			e.saveStep(StepRecord{
				TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
				Type: "observation", Status: "completed",
				Content: content, TokenUsed: e.totalTokens,
			})

			// 发出 task_completed——告诉前端 agent 已成功完成。包含累计
			// token 分解，使前端能展示准确的 token 指标。
			e.bus.SendEvent(event.NewEventWithSubTask("task_completed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"result":                   content,
				"total_tokens":             e.totalTokens,
				"total_steps":              e.stepIdx,
				"prompt_tokens":            e.tokenUsage.PromptTokens,
				"prompt_cache_hit_tokens":  e.tokenUsage.PromptCacheHitTokens,
				"prompt_cache_miss_tokens": e.tokenUsage.PromptCacheMissTokens,
				"completion_tokens":        e.tokenUsage.CompletionTokens,
			}))

			// 持久化 completed 状态。传入累计 total (e.totalTokens) 与已耗
			// 时长，使 DB 记录反映完整成本与时间。
			e.durationMs = time.Since(e.startTime).Milliseconds()
			e.updateTask("completed", content, e.totalTokens)
			e.updateTaskDuration()
			// 任务成功完成后清理内存中的上下文窗口快照，避免已完成任务长期占用内存。
			DeleteTaskContextSnapshot(e.cfg.SubTaskID)

			// Task 4：若本次运行关联了某个 case，则评估验收标准并广播
			// task_evaluated 事件。此处错误只记录，不改变任务成功状态。
			e.evaluateAndBroadcast(userInput, content)

			return content, e.totalTokens, nil
		}

		// =====================================================================
		// PHASE 2: EXECUTE_TOOL — 执行 LLM 请求的每个 tool call。
		// =====================================================================
		// LLM 可能在单次响应中请求多个 tool call。每个 tool call 顺序执行
		// ——tool N 的结果在下一 think 阶段处理 tool N+1 的结果时对 LLM
		// 可见。
		//
		// 所有 tool call 执行完毕后，loop 回到 PHASE 1 (THINK)，LLM 看到
		// tool 结果后决定下一步。
		for _, tc := range toolCalls {
			// 在执行 tool 前持久化 think step。这确保即使 tool 执行崩溃，
			// 审计链路也能展示 LLM 决定调用 tool 时的思考内容。
			e.saveStep(StepRecord{
				TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
				Type: "think", Status: "completed", Content: content, TokenUsed: e.totalTokens,
			})
			e.saveConversation("assistant", content)
			// 把 tool call 序列化为 JSON 以便 session_messages 持久化。
			tcJSON, _ := json.Marshal(toolCalls)
			e.writeSessionMessage("assistant", content, "", string(tcJSON), usage.TotalTokens)

			// 执行 tool。engine 把 tool call 派发给 Tool Registry，后者按
			// 名查找 tool 并调用其 Execute 方法。结果是一个可 JSON 序列化
			// 的值。
			//
			// stepIdx 在 executeTool 内部递增（不在此处），因为 executeTool
			// 管理 step 生命周期事件（started/completed）。
			result, toolErr := e.executeTool(tc)
			if toolErr != nil {
				// tool 执行失败。我们不立即终止，而是把错误作为 observation
				// 回喂给 LLM，让 agent 在下一 think 迭代中自我修正。这符合
				// 平台的错误处理原则：首次错误 → 引导 AI；连续相同错误 →
				// 升级到人工。
				obsContent := e.formatToolErrorObservation(tc.Function.Name, toolErr)
				if e.isRepeatingError(obsContent) {
					e.durationMs = time.Since(e.startTime).Milliseconds()
					e.updateTask("failed", "", e.totalTokens)
					e.updateTaskDuration()
					return "", e.totalTokens, fmt.Errorf("tool %s: repeated error: %w", tc.Function.Name, toolErr)
				}
				e.recordFeedbackError(obsContent)

				// 持久化错误 observation，使对话历史和审计链路包含 LLM 在
				// 下一轮看到的失败。
				e.saveConversation("tool", obsContent)
				e.writeSessionMessage("tool", obsContent, tc.ID, "", 0)

				// 发出 observation 事件，让前端把错误展示为对 LLM 的反馈，
				// 而非终态任务失败。
				e.bus.SendEvent(event.NewEventWithSubTask("observation", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
					"content": obsContent,
				}))
				e.saveStep(StepRecord{
					TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
					Type: "observation", Status: "completed",
					Content: obsContent,
				})

				// 把 assistant message（含失败的 tool_call）和错误 observation
				// 追加到对话历史。下一 think 迭代会同时看到两者，可以决定
				// 重试或换一个 tool。
				//
				// IMPORTANT：若 tc.Function.Arguments 是 malformed JSON，把
				// 原始字符串序列化回下一次 API 请求会触发 400
				// BadRequestError，因为 provider 会解析 arguments 内的嵌套
				// JSON。我们在追加到对话历史前对每个 tool_call 做清洗；
				// 真正的错误仍保留在 tool result observation 中。
				sanitizedTC := sanitizeToolCallArguments(tc)
				e.messages = append(e.messages, llm.Message{
					Role:      "assistant",
					Content:   content,
					ToolCalls: []llm.ToolCall{sanitizedTC},
				})
				e.messages = append(e.messages, llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    obsContent,
				})

				// 继续 ReAct loop——让 LLM 决定如何恢复。
				continue
			}

			// 持久化 tool result 以便审计链路。
			e.saveConversation("tool", result)
			e.writeSessionMessage("tool", result, tc.ID, "", 0)

			// =================================================================
			// PHASE 3: OBSERVE — 把 tool result 回填到对话。
			// =================================================================
			// assistant message（含 tool_call）和 tool result message 被追加
			// 到对话历史。下一次 loop 迭代中，LLM 会看到这些消息，并可以
			// 用 tool result 来指导其下一个响应。
			//
			// 这正是 ReAct loop 工作的关键：LLM 能看到自己行为的后果并据此
			// 调整。
			//
			// IMPORTANT：成功的 tool call 在少见情况下也可能携带 malformed
			// arguments（例如 LLM 产出了无效 JSON 但 executeTool 在本地修复
			// 了）。在追加到对话历史前务必清洗 arguments，避免下一次 think
			// step 出现 400 错误。
			e.messages = append(e.messages, llm.Message{
				Role:      "assistant",
				Content:   content,
				ToolCalls: []llm.ToolCall{sanitizeToolCallArguments(tc)},
			})
			e.messages = append(e.messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
		// 回到 PHASE 1 (THINK)——LLM 现在会看到 tool 结果，决定是继续调用
		// tool 还是给出最终答案。

		// 在每次 ReAct loop 迭代结束（tool 执行后）保存 checkpoint。这支持
		// 进程崩溃后的任务恢复——可从最近 checkpoint 续跑。
		// CheckpointManager 为 nil 时跳过 checkpointing。
		e.saveCheckpoint()
	}

	// =========================================================================
	// 超过 MaxSteps——agent 在允许的迭代次数内未产出最终答案。这是一个
	// 安全机制，防止死循环（例如 LLM 用相同参数反复调用同一 tool 而不
	// 取得进展）。
	e.bus.SendEvent(event.NewEventWithSubTask("task_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"reason":       "max_steps_exceeded",
		"max_steps":    e.cfg.MaxSteps,
		"current_step": e.stepIdx,
		"total_tokens": e.totalTokens,
	}))
	e.durationMs = time.Since(e.startTime).Milliseconds()
	e.updateTask("failed", "", e.totalTokens)
	e.updateTaskDuration()
	// 超过最大步数导致失败，清理上下文窗口快照以释放内存。
	DeleteTaskContextSnapshot(e.cfg.SubTaskID)
	return "", e.totalTokens, fmt.Errorf("max steps (%d) exceeded", e.cfg.MaxSteps)
}

// saveConversation 把一条对话消息持久化到存储后端。
//
// 持久化为 nil 时（例如测试或临时性运行）为 no-op。错误只记录不返回——
// 持久化失败对 agent 执行是非致命的。即使数据库不可用，agent 仍会继续
// 处理，因为首要目标是完成用户任务。
//
// 设计理由：持久化是横切关注点，不应中断 agent 的核心 loop。数据库不可用
// 时，我们记录错误并继续——任务仍会完成，只是没有审计链路。
func (e *Engine) saveConversation(role, content string) {
	if e.persist == nil {
		return
	}
	if err := e.persist.SaveConversation(ConversationRecord{
		TaskID: e.taskID, Role: role, Content: content,
	}); err != nil {
		log.Printf("[Engine] Failed to save conversation: %v", err)
	}
}

// writeSessionMessage 通过 SessionMessageWriter 回调把一条消息写入
// session_messages 表。这是一个 best-effort 操作——失败只会被记录，绝不
// 会中断 engine 的执行。
//
// SessionMessageWriter 在 EngineConfig 中配置，通常包装
// db.InsertSessionMessage。为 nil 时（例如测试或不需要 session 持久化时），
// 本函数为 no-op。
func (e *Engine) writeSessionMessage(role, content string, toolCallID string, toolCallsJSON string, tokenCount int) {
	if e.sessionMsgWriter == nil {
		return
	}
	if err := e.sessionMsgWriter(SessionMessageRecord{
		TaskID:     e.taskID,
		TurnIndex:  e.turnIndex,
		Role:       role,
		Content:    content,
		ToolCallID: toolCallID,
		ToolCalls:  toolCallsJSON,
		TokenCount: tokenCount,
	}); err != nil {
		log.Printf("[Engine] Failed to write session message: %v", err)
	}
}

// saveStep 把一条 step 记录持久化到存储后端。
//
// 每个 step 代表 ReAct loop 的一个阶段（think 或 tool_call）。step 连同
// 其状态（completed/failed）、内容和 token 用量被持久化，用于成本跟踪
// 和审计。
//
// Like saveConversation, this is a no-op when persistence is nil and errors
// are logged but not returned — persistence failures do not interrupt the agent.
func (e *Engine) saveStep(s StepRecord) {
	if e.persist == nil {
		return
	}
	if err := e.persist.SaveStep(s); err != nil {
		log.Printf("[Engine] Failed to save step: %v", err)
	}
}

// updateTask 把最终任务状态持久化到存储后端。
//
// 在任务达到终态（completed 或 failed）时调用。status、最终结果文本和
// 总 token 数被写入 task 记录，使任务历史 UI 可以展示任务结果和成本。
//
// 与 saveConversation 和 saveStep 一样，持久化为 nil 时为 no-op。
func (e *Engine) updateTask(status, finalResult string, totalTokens int) {
	if e.persist == nil {
		return
	}
	if err := e.persist.UpdateTask(e.taskID, status, finalResult, totalTokens); err != nil {
		log.Printf("[Engine] Failed to update task: %v", err)
	}
}

func (e *Engine) updateTaskDuration() {
	if e.persist == nil {
		return
	}
	if err := e.persist.UpdateTaskDuration(e.taskID, int(e.durationMs)); err != nil {
		log.Printf("[Engine] Failed to update task duration: %v", err)
	}
}

// evaluateAndBroadcast 在 engine 关联到某个 case 时运行
// AcceptanceEvaluator，并广播带结果的 task_evaluated 事件。
//
// 它在 task_completed 已发出、任务状态已持久化后调用。评估失败会被记录
// 并写入事件，但不会把任务翻转为 failed；评估是观测性的，不是硬性闸门。
func (e *Engine) evaluateAndBroadcast(userInput, finalAnswer string) {
	if e.caseID == "" {
		return
	}
	if len(e.cfg.Contract.AcceptanceCriteria) == 0 {
		return
	}

	// 解析验收评估的作用域根目录。case 的 contract.Scope 通常是宽松的 "."
	// (见 harness.DefaultContract / cases.go)，若直接用它评估相对路径的
	// file_exists/content_contains 标准，会把 target 解析到服务器 CWD 而非
	// 本次任务真正落盘的 session workspace 目录，导致明明 Agent 已在
	// <cwd>/workspace/session-<id>/ 下写出了文件却报 "File not found"。
	//
	// 因此当 contract.Scope 是默认的 "." 或为空时，回退到 EngineConfig.WorkspaceDir
	//（即 session 的 workspace_dir，由 runAgentLoopWithTurn 从 sessions 表读入并
	// 在 tool 调用时作为 "workdir" 注入）。这样 evaluator 与 write_file/run_shell
	// 的 CWD 保持一致，相对路径的验收标准能命中真实落盘位置。
	scope := e.cfg.Contract.Scope
	if (scope == "" || scope == ".") && e.cfg.WorkspaceDir != "" {
		scope = e.cfg.WorkspaceDir
	}
	evaluator := harness.NewAcceptanceEvaluator(scope)

	// 从对话历史中收集最近的 tool 输出。我们保留最后 10 条 "tool" 角色消息；
	// 更早的输出通常与最终评估无关，且保持上下文较小能改善 judge 延迟和成本。
	var toolOutputs []string
	for i := len(e.messages) - 1; i >= 0 && len(toolOutputs) < 10; i-- {
		if e.messages[i].Role == "tool" {
			toolOutputs = append([]string{e.messages[i].Content}, toolOutputs...)
		}
	}

	if e.llm != nil {
		judge := harness.NewLLMJudge(e.llm, e.cfg.Model)
		evaluator.SetLLMJudge(judge)
	}

	// 把运行时上下文提供给 judge，使语义化 prompt 包含真实的 goal、user
	// input、agent 答案和 tool 证据。
	evaluator.SetEvaluationContext(e.cfg.Contract.Goal, userInput, finalAnswer, toolOutputs)

	report, err := evaluator.Evaluate(e.cfg.Contract.AcceptanceCriteria)

	passed := false
	score := 0.0
	reason := ""
	if err != nil {
		reason = fmt.Sprintf("Evaluation failed: %v", err)
		log.Printf("[Engine] Case evaluation failed for task=%s case=%s: %v", e.taskID, e.caseID, err)
	} else {
		passed = report.AllPassed
		reason = report.Summary
		if !passed && len(report.Results) > 0 {
			// 把第一条失败原因呈现出来以辅助调试。
			for _, r := range report.Results {
				if !r.Passed {
					reason = r.Message
					break
				}
			}
		}

		// 计算汇总分数。LLMJudge 标准贡献其返回的分数；当没有 judge 标准
		// 而存在其他标准时，仅当所有标准都通过时分数为 1.0，否则为 0.0。
		// 这使得对纯确定性 contract 的分数有意义，同时保留语义化 contract
		// 原本的 judge 平均行为。
		if len(report.Results) > 0 {
			var judgeScoreSum float64
			var judgeCount int
			var otherExists bool
			for _, r := range report.Results {
				if r.Criterion.Type == harness.AcceptLLMJudge {
					judgeScoreSum += r.Score
					judgeCount++
				} else {
					otherExists = true
				}
			}
			if judgeCount > 0 {
				score = judgeScoreSum / float64(judgeCount)
			} else if otherExists {
				if report.AllPassed {
					score = 1.0
				} else {
					score = 0.0
				}
			}
		}
	}

	// 把评估结果持久化到 case_evaluations 表。
	if e.cfg.EvaluationRepository != nil {
		saveErr := e.cfg.EvaluationRepository.SaveEvaluation(cases.CaseEvaluation{
			TaskID: e.taskID,
			CaseID: e.caseID,
			Passed: passed,
			Score:  score,
			Reason: reason,
		})
		if saveErr != nil {
			log.Printf("[Engine] Failed to save case evaluation: %v", saveErr)
		}
	}

	e.bus.SendEvent(event.NewEventWithSubTask(event.EventTaskEvaluated, e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"case_id": e.caseID,
		"passed":  passed,
		"score":   score,
		"reason":  reason,
		"report":  report,
	}))
}

// formatToolErrorObservation 生成一个简洁、稳定的 tool 执行失败指纹。
// 该内容作为 tool result 回喂给 LLM 以便其自我修正。保持格式稳定可让
// isRepeatingError 检测到连续相同的失败。
func (e *Engine) formatToolErrorObservation(toolName string, err error) string {
	return fmt.Sprintf("[TOOL ERROR] %s failed: %s", toolName, normalizeErrorFingerprint(err.Error()))
}

// normalizeErrorFingerprint 去除错误信息中易变的子串，使两次同类失败比较
// 相等。Router-activation 事后分析显示：403 "no access to model" 错误中
// 嵌入了 per-request id（"request id: 20260714033318937918881aclHY4az"），
// 每次调用都不同；不归一化的话 isRepeatingError 永远匹配不上，engine 在
// 90s 轮询超时前空转 1347 次，陷入死循环。我们做如下折叠：
//   - "request id: <opaque>"   → "request id: <redacted>"
//   - "request_id: <opaque>"   → "request_id: <redacted>"
//
// 归一化后的字符串同时用于指纹比较和回喂给 LLM 的 observation，因此 LLM
// 也看到更干净的错误信号。
func normalizeErrorFingerprint(msg string) string {
	// 去除 OpenAI 风格的 "request id: <opaque>" / "request_id: <opaque>"。
	// 保留捕获到的 "request id" / "request_id" key，对易变 id 做脱敏。
	return regexpRequestID.ReplaceAllString(msg, "$1: <redacted>")
}

// isRepeatingError 返回 true 表示同一个可恢复错误刚刚连续发生。按照
// 平台的错误处理原则，单个错误会回喂给 LLM 自我修正；连续两次相同
// （归一化后）的错误被视为死循环并升级到人工介入。
//
// 比较使用 normalizeErrorFingerprint，使易变 token（per-request id）
// 不会掩盖真正的重复。理由详见 normalizeErrorFingerprint。
func (e *Engine) isRepeatingError(errFingerprint string) bool {
	if e.consecutiveErrors == 0 {
		return false
	}
	norm := normalizeErrorFingerprint(errFingerprint)
	return e.lastError != "" && e.lastError == norm
}

// recordFeedbackError 在一个可恢复错误已回喂给 LLM 后更新 engine 的错误
// 跟踪状态。若同一个（归一化的）错误重复出现则递增计数器，否则重置以
// 跟踪新的错误模式。存储的 lastError 是归一化形式，使后续 isRepeatingError
// 比较都基于同一个归一化基线。
func (e *Engine) recordFeedbackError(errFingerprint string) {
	norm := normalizeErrorFingerprint(errFingerprint)
	if e.lastError != "" && e.lastError == norm {
		e.consecutiveErrors++
	} else {
		e.consecutiveErrors = 1
		e.lastError = norm
	}
}

// think 把当前对话历史发给 LLM 并返回
//  2. 从 Tool Registry 构建 tool 定义——告诉 LLM 有哪些 tool 可用、
//     其描述和参数 schema。LLM 据此决定是否以及如何调用 tool。
//  3. 构造 ChatRequest，包含完整对话历史、tool 定义、model、temperature
//     和 max tokens。
//  4. 以流式回调调用 llm.Provider.ChatStream。LLM 的每个文本 delta 都
//     作为 llm_delta 事件转发到前端，在 UI 中形成打字机效果。
//  5. 流结束后发出 llm_message_complete 和 step_complete 事件，让 UI 知道
//     think 阶段已结束。
//
// # 为什么用流式？
//
// 流式对用户体验至关重要——没有流式，用户会在 LLM 生成完整响应的几秒
// 内对着空白屏幕发呆。有了流式，每个 token 生成时即时出现，给用户即时
// 反馈，让 agent 显得灵敏。流式回调也是实现"白盒"哲学的机制：LLM 生成
// 的每个 token 都对用户实时可见。
//
// # Tool call 处理
//
// LLM 可能在文本内容之外或代替文本内容返回 tool call。ChatStream 方法
// 跨 SSE chunk 累积 tool call delta，并返回完整拼装好的 tool call。engine
// 据此决定是执行 tool（继续 loop）还是把文本作为最终答案返回。
func (e *Engine) think(ctx context.Context) (string, llm.Usage, []llm.ToolCall, error) {
	// 发出 step_started：UI 把该 step 切换到 "running" 状态。
	e.bus.SendEvent(event.NewEventWithSubTask("step_started", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "think",
	}))

	// Phase 7-C：为 think step 启动一个 child trace span。
	var traceCtx *observability.TraceContext
	if e.cfg.Tracer != nil && e.rootTraceCtx != nil {
		traceCtx = e.cfg.Tracer.StartChild(e.rootTraceCtx, e.cfg.AgentID, "think")
	}

	// 发出 llm_thinking：UI 展示 "Thinking..." 指示。这在 HTTP 请求发出
	// 前就发送，让用户看到即时反馈，即使 LLM API 需要几秒才响应。
	e.bus.SendEvent(event.NewEventWithSubTask("llm_thinking", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"content": "Thinking...",
	}))

	// =========================================================================
	// 根据 AllowedTools 白名单从 registry 筛选 tool 定义。
	//
	// 每个 tool 的名字、描述和 JSON Schema 参数都发给 LLM，让它决定调用
	// 哪个 tool、传什么参数。若 registry 为空，LLM 会以纯文本模式工作
	// （无法调用 tool）。
	//
	// Phase 7-I：若 TaskContract.AllowedTools 已指定，只向 LLM 暴露这些
	// tool。这阻止 agent 推理它无权执行的 tool，并使 prompt 保持精简。
	// AllowedTools 为空时保留旧行为（所有 tool 可见）。
	//
	// 同时收集 exposed / hidden 列表，发送 system_info(type=tool_visibility)，
	// 让前端清楚知道本次 think 步骤中哪些工具对 LLM 可见、哪些被白名单隐藏。
	// =========================================================================
	toolDefs := make([]llm.ToolDef, 0)
	allowed := make(map[string]struct{}, len(e.cfg.Contract.AllowedTools))
	for _, name := range e.cfg.Contract.AllowedTools {
		allowed[name] = struct{}{}
	}
	var exposedTools []string
	var hiddenTools []string
	for _, t := range e.tools.List() {
		fn := t.FullName()
		if len(allowed) > 0 {
			if _, ok := allowed[fn]; !ok {
				hiddenTools = append(hiddenTools, fn)
				continue
			}
		}
		exposedTools = append(exposedTools, fn)
		toolDefs = append(toolDefs, llm.ToolDef{
			Type: "function",
			Function: llm.FunctionDefinition{
				Name:        fn,
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type":          "tool_visibility",
		"allowed_tools": e.cfg.Contract.AllowedTools,
		"exposed":       exposedTools,
		"hidden":        hiddenTools,
	}))

	// =========================================================================
	// Phase 6 Router：动态模型选择
	// =========================================================================
	// 若配置了 Router 和 Registry，则在每次 LLM 调用前先分类用户意图并
	// 选择最佳模型层级。这支持成本高效的路由：简单 chat 用便宜模型，
	// 复杂推理用高端模型。
	var (
		selectedModel    string
		selectedProvider llm.Provider
		routeDecision    *llm.RouteDecision
	)

	// 默认使用 cfg.Model / e.llm
	selectedModel = e.cfg.Model
	selectedProvider = e.llm
	// 把 Router 的选择缓存到 engine 上，使 OnLLMUsage 回调（Run loop 中）
	// 按实际调用的模型上报成本，而不是 e.cfg.Model。每次 think() 都重置，
	// 避免失败后重试的 step 残留上次的选择。
	e.selectedModel = selectedModel

	if e.cfg.Router != nil && e.cfg.Registry != nil {
		// 从对话历史估算上下文长度。
		contextLen := 0
		for _, msg := range e.messages {
			contextLen += len(msg.Content) / 4 // 粗略：4 字符 ~ 1 token
		}
		userInput := ""
		if len(e.messages) > 0 {
			userInput = e.messages[len(e.messages)-1].Content
		}

		routeReq := &llm.RouteRequest{
			UserInput:    userInput,
			ContextLen:   contextLen,
			RequiredCaps: []llm.ModelCapability{llm.CapToolCalling, llm.CapStreaming},
		}

		var errRoute error
		routeDecision, errRoute = e.cfg.Router.Select(ctx, routeReq)
		if errRoute != nil {
			log.Printf("[Engine] Router selection failed: %v, falling back to default model", errRoute)
		} else if routeDecision != nil && routeDecision.Primary != nil {
			selectedModel = routeDecision.Primary.Name
			e.selectedModel = selectedModel //供 OnLLMUsage 上报真实调用模型

			// 从 providers map 解析所选模型对应的 provider。键可以是
			// provider 名（如 "deepseek"）或模型名。
			selectedProvider = resolveProvider(e.providers, routeDecision.Primary.Provider, routeDecision.Primary.Name)

			if selectedProvider == nil {
				// 最后兜底：从 engine 的默认 endpoint/key 构建一个全新的
				// OpenAI-compatible provider，并以 router 选中模型名锚定。
				// 即使调用方未在 Providers map 中为该模型预注册 provider，
				// 仍能让被路由的模型生效。
				selectedProvider = llm.NewOpenAIProvider(routeDecision.Primary.Provider,
					e.cfg.Endpoint, e.cfg.APIKey, selectedModel)
			}
			// 注意：当上面找到预注册 provider 时，我们不会重新锚定其模型——
			// ChatRequest.Model 字段（下方设为 selectedModel）在
			// OpenAIProvider.ChatStream 中优先，因此无论 provider 的默认
			// 模型是什么，被路由的模型都会被尊重。

			// model_routed 事件包含 fallback 信息，让前端可以预先展示
			// fallback 目标模型（白盒透明）。
			e.bus.SendEvent(event.NewEventWithSubTask("model_routed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"model":    selectedModel,
				"intent":   routeDecision.Intent,
				"tier":     routeDecision.Tier.String(),
				"reason":   routeDecision.Reason,
				"provider": routeDecision.Primary.Provider,
				"fallback": routeDecision.Fallback,
			}))
			log.Printf("[Router] Selected model: %s (intent=%s, tier=%s, reason=%s)",
				selectedModel, routeDecision.Intent, routeDecision.Tier, routeDecision.Reason)
		}
	}

	// =====================================================================
	// Context window snapshot（白盒可观测）
	// =====================================================================
	// 在每次 LLM 调用前发出当前对话的 snapshot，让前端可视化上下文窗口
	// 的占用情况，并检查每条将发给模型的 system/user/assistant/tool
	// 消息。
	//
	// token 计数是*估算*（见 internal/llm/token_estimate.go），因为大多
	// API 不提供 per-message token 用量。该比例足够准确，可用于占比
	// 可视化和容量预警。
	maxContextTokens := llm.EstimateModelContextWindow(e.cfg.Registry, selectedModel)
	snapshot := llm.BuildContextWindowSnapshot(selectedModel, maxContextTokens, e.messages)

	// 缓存 snapshot，使 REST API 可以按需返回 live 上下文窗口而无需再
	// 触发 LLM 调用。
	RecordTaskContextSnapshot(e.cfg.SubTaskID, snapshot)

	// 为了与 REST API（encodeContextWindowSnapshot）保持字段一致，
	// 这里直接序列化 snapshot 结构体，避免手写 map 导致字段不同步。
	snapshotData, _ := json.Marshal(snapshot)
	var snapshotMap map[string]any
	_ = json.Unmarshal(snapshotData, &snapshotMap)
	e.bus.SendEvent(event.NewEventWithSubTask(event.EventContextWindowSnapshot, e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, snapshotMap))

	req := llm.ChatRequest{
		Model:       selectedModel,
		Messages:    e.messages,
		Tools:       toolDefs,
		Temperature: e.cfg.Temperature,
		MaxTokens:   e.cfg.MaxTokens,
		Context:     ctx,
		CaseID:      e.caseID,
	}

	// Phase 7-C：测量 LLM 调用延迟并通过回调记录。
	llmStart := time.Now()
	// 以流式方式调用 LLM。onChunk 回调对每个 SSE chunk 调用。每个文本
	// delta 都作为 llm_delta 事件转发到前端，让 UI 实时渲染 token
	// （打字机效果）。
	content, usage, toolCalls, err := selectedProvider.ChatStream(req, func(chunk llm.StreamChunk) error {
		// 把每个 delta 流式转发到前端
		if chunk.Delta.Content != "" {
			e.bus.SendEvent(event.NewEventWithSubTask("llm_delta", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"content": chunk.Delta.Content,
			}))
		}
		if chunk.Delta.ReasoningContent != "" {
			e.bus.SendEvent(event.NewEventWithSubTask("llm_delta", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"content":           chunk.Delta.Content,
				"reasoning_content": chunk.Delta.ReasoningContent,
			}))
		}
		// Step-3.x / vLLM 风格的 reasoning 字段（与 reasoning_content 等价，仅
		// 字段名不同）。同样转发到前端，保证推理型模型在 think 阶段的思维链
		// 对用户可见（白盒哲学）。
		if chunk.Delta.Reasoning != "" {
			e.bus.SendEvent(event.NewEventWithSubTask("llm_delta", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"content":           chunk.Delta.Content,
				"reasoning_content": chunk.Delta.Reasoning,
			}))
		}
		return nil
	})

	// Fallback 重试：若主模型失败且配置了 fallback，则重试。
	if err != nil && routeDecision != nil && routeDecision.Fallback != nil {
		log.Printf("[Engine] Primary model %s failed (%v), trying fallback %s",
			selectedModel, err, routeDecision.Fallback.Name)

		var fallbackProvider llm.Provider
		fallbackProvider = resolveProvider(e.providers, routeDecision.Fallback.Provider, routeDecision.Fallback.Name)
		if fallbackProvider == nil {
			return "", usage, nil, fmt.Errorf("fallback provider %q not found: %w", routeDecision.Fallback.Provider, err)
		}

		req.Model = routeDecision.Fallback.Name
		e.selectedModel = routeDecision.Fallback.Name // fallback 成功后 cost 按实际模型上报
		e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type":     "model_fallback",
			"primary":  selectedModel,
			"fallback": routeDecision.Fallback.Name,
			"reason":   err.Error(),
		}))

		// Fallback 应尊重取消信号；若 fallback 模型流式过程中任务被取消，
		// 必须把该取消传播到 HTTP 请求。创建一个 child context，使 goroutine
		// 在父 context 结束时被回收（防止泄漏）。
		fallbackCtx := ctx
		if ctx != nil {
			var cancel context.CancelFunc
			fallbackCtx, cancel = context.WithCancel(ctx)
			defer cancel()
		}

		req.Context = fallbackCtx
		content, usage, toolCalls, err = fallbackProvider.ChatStream(req, func(chunk llm.StreamChunk) error {
			if chunk.Delta.Content != "" {
				e.bus.SendEvent(event.NewEventWithSubTask("llm_delta", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
					"content": chunk.Delta.Content,
				}))
			}
			return nil
		})
		if err == nil {
			log.Printf("[Engine] Fallback model %s succeeded", routeDecision.Fallback.Name)
		} else {
			log.Printf("[Engine] Fallback model %s also failed: %v", routeDecision.Fallback.Name, err)
		}
	}

	if err != nil {
		// Phase 7-C: finish think span with error.
		if e.cfg.Tracer != nil && traceCtx != nil {
			e.cfg.Tracer.Finish(traceCtx, err)
		}
		if e.cfg.LLMLatencyRecorder != nil {
			e.cfg.LLMLatencyRecorder(time.Since(llmStart))
		}
		// 失败返回前清空 selectedModel，避免 Run loop 里 OnLLMUsage 误用上一次
		// think 残留的选择（虽然失败分支不会进 OnLLMUsage，但保持状态干净）。
		e.selectedModel = ""
		return "", usage, nil, err
	}

	// Phase 7-C: finish think span with attributes on success.
	if e.cfg.Tracer != nil && traceCtx != nil {
		attrs := map[string]any{"model": selectedModel, "provider": e.cfg.Provider}
		if routeDecision != nil {
			attrs["intent"] = routeDecision.Intent
			attrs["tier"] = routeDecision.Tier.String()
		}
		e.cfg.Tracer.FinishWithAttributes(traceCtx, nil, attrs)
	}
	if e.cfg.LLMLatencyRecorder != nil {
		e.cfg.LLMLatencyRecorder(time.Since(llmStart))
	}

	e.bus.SendEvent(event.NewEventWithSubTask("llm_message_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, nil))
	e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "think",
	}))

	return content, usage, toolCalls, nil
}

// executeTool 执行 LLM 请求的单个 tool call，返回 JSON 序列化的结果。
//
// # 工作原理
//
//  1. 递增 stepIdx——每次 tool 执行都是 ReAct loop 中的一个新 step。
//  2. 把 tool call arguments 从 JSON 字符串解析为 map[string]any。
//     若解析失败，回退到空 map（tool 可能仍能以默认参数工作）。
//  3. 发出 step_started 和 tool_call_started 事件，让 UI 可以展示 tool
//     名、参数和 loading 指示。
//  4. 把 tool call 派发给 Tool Registry，并测量执行时间。
//  5. 成功：发出 tool_call_output、tool_call_complete 和 observation 事件。
//     observation 事件尤其关键——它告诉 UI 什么数据被回喂给 LLM。
//  6. 失败：发出 tool_call_failed 和 step_complete 事件，然后返回错误。
//
// # 为什么要测量 duration？
//
// tool 执行时间对调试和成本优化至关重要。一个跑 30 秒的 tool 是瓶颈——
// duration_ms 指标能帮我们识别慢 tool。前端可以在 tool call 卡片中展示
// 执行时间，让用户看到时间花在哪里。
//
// # 为什么在这里递增 stepIdx？
//
// stepIdx 在 executeTool 内部递增（而非在 Run loop 中），因为 executeTool
// 管理完整的 step 生命周期（started → executing → completed）。每次 tool
// 执行都是一个有独立事件的独立 step，stepIdx 在这些事件发出时必须正确。
//
// # Phase 5：审批处理
//
// 当 PolicyGate 返回 ErrApprovalRequired（来自 ApprovalRule）时，engine
// 捕获该错误并交给 handleApprovalRequired 处理。该方法向前端发出
// system_info 事件，等待用户批准或拒绝该高风险操作。若批准，直接执行
// tool（绕过 PolicyGate）。若拒绝，任务以 approval_denied 错误失败。
func (e *Engine) executeTool(tc llm.ToolCall) (string, error) {
	// 递增 stepIdx——每次 tool 执行都是一个新 step。放在这里（而不是
	// 调用方）是为了让 step_started 和 tool_call_started 事件携带正确的
	// step 序号。
	e.stepIdx++

	// 从 JSON 解析 tool call arguments。LLM 以 JSON 字符串（而非对象）
	// 形式返回 arguments，因为它以增量方式流式产出。
	// 若解析失败（例如 LLM 产出了 malformed JSON），我们先尝试对常见的
	// "未终止 string/object" 情况做 best-effort 修复（见
	// repairToolArgumentsJSON）。若修复也失败，则返回清晰错误，让
	// ReAct loop 能把错误回喂给 LLM，而不会用 malformed JSON 污染下一次
	// API 请求。
	var args map[string]any
	if parseErr := json.Unmarshal([]byte(tc.Function.Arguments), &args); parseErr != nil {
		repaired, repairErr := repairToolArgumentsJSON(tc.Function.Arguments)
		if repairErr == nil {
			log.Printf("[Engine] Repaired malformed arguments JSON for %s (orig error: %v)", tc.Function.Name, parseErr)
			args = repaired
		} else {
			errMsg := fmt.Sprintf("invalid arguments JSON for %s: %v (raw: %q, repair failed: %v)",
				tc.Function.Name, parseErr, tc.Function.Arguments, repairErr)
			log.Printf("[Engine] %s", errMsg)
			e.bus.SendEvent(event.NewEventWithSubTask("tool_call_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"tool":        tc.Function.Name,
				"error":       errMsg,
				"duration_ms": 0,
				"args":        tc.Function.Arguments,
			}))
			e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"type": "tool_call",
			}))
			e.saveStep(StepRecord{
				TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
				Type: "tool_call", Status: "failed",
				ToolName: tc.Function.Name, ToolInput: map[string]any{"_raw_arguments": tc.Function.Arguments},
				ToolOutput: errMsg,
			})
			return "", fmt.Errorf("%s", errMsg)
		}
	}

	// 若用户（LLM）未显式提供 workdir，把 session 的 workspace_dir 注入到
	// tool 输入中。这让 run_shell 之类的 tool 能在正确 CWD 下执行，而无需
	// LLM 每次都传递。
	if e.cfg.WorkspaceDir != "" {
		if _, hasWorkdir := args["workdir"]; !hasWorkdir {
			args["workdir"] = e.cfg.WorkspaceDir
		}
	}

	// Phase 7 TODO 修复：session_id 与 task_id 对 LLM 不公开（它们是平台内部
	// 路由标识，从未出现在 system prompt 中），因此 LLM 调用 todo/* 这类需要
	// session_id 的工具时无法正确传参——它会硬编码 "test-session" 之类占位值，
	// 导致 todo 写入错误 session。这里在 tool 执行前自动用 Engine 持有的真实
	// session_id / task_id 覆盖 LLM 传入的值。覆盖是无条件的：这些标识属于
	// 平台路由层，LLM 没有也不应有权威性，始终以 Engine 的真实值为准。
	if e.cfg.SessionID != "" {
		args["session_id"] = e.cfg.SessionID
	}
	if e.taskID != "" {
		args["task_id"] = e.taskID
	}

	// 发出 step 和 tool call 生命周期事件。UI 用这些事件展示：
	//   - step_started：该 step 在 step 列表中变为 "running"
	//   - tool_call_started：在卡片中展示 tool 名和参数
	// 我们用 registry 中的权威元数据（namespace、description、tags）丰富
	// tool_call_started，使前端无需猜测即可渲染风险指示和 namespace 徽章。
	toolName := tc.Function.Name
	namespace, description, tags, _ := "", "", []string{}, false
	if e.tools != nil {
		namespace, description, tags, _ = e.tools.ToolMetadata(toolName)
	}
	e.bus.SendEvent(event.NewEventWithSubTask("step_started", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "tool_call",
	}))
	e.bus.SendEvent(event.NewEventWithSubTask("tool_call_started", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":        toolName,
		"namespace":   namespace,
		"description": description,
		"tags":        tags,
		"args":        args,
	}))

	// 执行 tool 并测量耗时。duration 用于性能监控和调试——慢 tool 是
	// 影响用户体验的瓶颈。
	// DEBUG log.Printf("[Engine] executeTool %s with parsed args: %+v", tc.Function.Name, args)
	start := time.Now()
	// 构造供 tool 执行用的 ExecuteContext，把 session 工作目录透传下去。
	// 同时保留 args["workdir"] 作为兼容性回退（已存在的测试/PolicyGate 回调依赖）。
	toolCtx := tool.ExecuteContext{}
	if e.cfg.WorkspaceDir != "" {
		toolCtx.Workdir = e.cfg.WorkspaceDir
	}
	// 若配置了 PolicyGate 则经由它；否则直接执行。PolicyGate 在允许
	// tool 执行前会按 policy chain（FileScopeRule、PathTraversalRule 等）
	// 检查 tool call。
	var result any
	var execErr error
	if e.gate != nil {
		result, execErr = e.gate.Execute(tc.Function.Name, args, func(input map[string]any) (any, error) {
			return e.tools.ExecuteWithCtx(tc.Function.Name, toolCtx, input)
		})
	} else {
		result, execErr = e.tools.ExecuteWithCtx(tc.Function.Name, toolCtx, args)
	}
	duration := time.Since(start).Milliseconds()
	if e.cfg.ToolLatencyRecorder != nil {
		e.cfg.ToolLatencyRecorder(time.Since(start))
	}

	if execErr != nil {
		// === Phase 5: 审批请求处理 ===
		// 检查 PolicyGate 是否返回了 ErrApprovalRequired。
		// 如果 ApprovalRule 检测到高风险操作，会返回此错误。
		// Engine 需要发射 system_info 事件到前端，等待用户批准/拒绝。
		var approvalErr *harness.ErrApprovalRequired
		if errors.As(execErr, &approvalErr) {
			if e.cfg.Role == AgentRoleWorker && e.cfg.ApproverMode == "leader" {
				return e.handleApprovalDelegation(tc, &ApprovalError{
					ApprovalID: approvalErr.ApprovalID,
					Tool:       approvalErr.Tool,
					Reason:     approvalErr.Reason,
					Input:      approvalErr.Input,
				}, args, duration)
			}
			return e.handleApprovalRequired(tc, approvalErr, args, duration)
		}

		// S7 修复：硬性安全拦截（ErrBlockedByPolicy）必须立即失败，
		// 不再走 30s 审批超时流程。只有 ApprovalRule 主动返回的
		// ErrApprovalRequired 才进入审批。这样 PathTraversal /
		// FileScope / TokenBudget / ToolWhitelist / DangerousCommand
		// 命中时 ReAct Loop 立即收到错误并可选择重试，同时持久化
		// 拦截原因（M1）便于历史回放。
		var policyErr *harness.ErrBlockedByPolicy
		if errors.As(execErr, &policyErr) {
			reason := fmt.Sprintf("[POLICY BLOCK] %s blocked %s: %s", policyErr.Rule, policyErr.Tool, policyErr.Reason)
			e.bus.SendEvent(event.NewEventWithSubTask("tool_call_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"tool":        tc.Function.Name,
				"error":       reason,
				"duration_ms": duration,
				"args":        args,
				"policy_rule": policyErr.Rule,
			}))
			e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
				"type": "tool_call",
			}))

			// M1 修复：持久化被拦截的 tool_call step，使 GET /api/tasks?id=...
			// 能在历史回放中还原拦截事件（之前 handleApprovalRequired 不调
			// saveStep，导致拦截步骤丢失）。
			e.saveStep(StepRecord{
				TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
				Type: "tool_call", Status: "failed",
				ToolName: tc.Function.Name, ToolInput: args,
				ToolOutput: reason, DurationMs: int(duration),
			})

			// 返回错误给 ReAct Loop。Engine.Run 会把错误作为 observation 反馈
			// 给 LLM（让其换思路），而不是终止整个任务——除非达到 max_steps。
			return reason, nil
		}

		// tool 执行失败——发出失败事件并返回错误。UI 会展示 tool 名、错误
		// 消息和 duration。step 仍被标记为 "complete"（而非 "running"），
		// 因为 tool call 阶段已结束——只是以失败告终。
		e.bus.SendEvent(event.NewEventWithSubTask("tool_call_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       execErr.Error(),
			"duration_ms": duration,
			"args":        args,
		}))
		e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))

		// 持久化失败的 tool_call step，以便历史回放可以展示它。
		e.saveStep(StepRecord{
			TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
			Type: "tool_call", Status: "failed",
			ToolName: tc.Function.Name, ToolInput: args,
			DurationMs: int(duration),
		})

		return "", execErr
	}

	// DEBUG
	log.Printf("[Engine] executeTool %s succeeded, result type=%T", tc.Function.Name, result)

	// 把 tool result 序列化为 JSON 以供 LLM 对话。LLM 期望 tool result 是
	// JSON 字符串以便解析结构化数据。若序列化失败（行为良好的 tool 极少
	// 出现），我们仍有原始 result 对象——但 LLM 会收到空字符串。
	resultJSON, _ := json.Marshal(result)
	resultStr := string(resultJSON)

	// 发出 tool 执行事件。UI 用这些事件展示：
	//   - tool_call_output：原始 tool result（UI 中可折叠的 JSON）
	//   - tool_call_complete：tool 成功完成及其 duration
	//   - observation：回喂给 LLM 的数据（"observe" 阶段）
	//   - step_complete：该 step 在 step 列表中变为 "completed"
	e.bus.SendEvent(event.NewEventWithSubTask("tool_call_output", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":   tc.Function.Name,
		"result": result,
	}))
	e.bus.SendEvent(event.NewEventWithSubTask("tool_call_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":        tc.Function.Name,
		"duration_ms": duration,
	}))

	// 持久化 tool_call step，使 GET /api/tasks?id=xxx 的历史回放能正确
	// 还原 tool_call step（不会全部被当作 "think"）。
	e.saveStep(StepRecord{
		TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
		Type: "tool_call", Status: "completed",
		ToolName: tc.Function.Name, ToolInput: args, ToolOutput: resultStr,
		DurationMs: int(duration),
		TokenUsed:  0, // tool call 本身不消耗 LLM token
	})

	// 发出 observation——这是把 tool 执行接回 ReAct loop 的关键事件。
	// 前端在 Agent Tree 可视化中把它展示为 "observation" 阶段，清晰说明
	// LLM 在下一次 think 迭代中会看到什么数据。
	e.bus.SendEvent(event.NewEventWithSubTask("observation", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"content": resultStr,
	}))

	// 持久化 observation step，使历史回放也能看到它。
	e.saveStep(StepRecord{
		TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
		Type: "observation", Status: "completed",
		Content: resultStr,
	})

	e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "tool_call",
	}))

	return resultStr, nil
}

// handleApprovalRequired 处理 PolicyGate 返回的 ErrApprovalRequired 错误。
// 如果配置了 ApprovalHandler，发射 system_info 事件到前端，等待用户批准/拒绝决定。
// 如果用户批准，绕过 PolicyGate 直接执行工具调用。
// 如果用户拒绝或超时，返回错误导致任务失败。
// 如果没有配置 ApprovalHandler，直接返回错误。
//
// # 审批流程
//
//  1. 检查是否配置了 ApprovalHandler（未配置则直接拒绝）
//  2. 发射 system_info(type="approval_required") 事件到前端
//  3. 调用 ApprovalHandler.RequestApproval 发送审批请求
//  4. 调用 ApprovalHandler.WaitForDecision 等待用户决定（默认 30 秒超时）
//  5. 批准：绕过 PolicyGate 直接执行工具，发射正常事件流
//  6. 拒绝/超时：发射失败事件，返回错误
func (e *Engine) handleApprovalRequired(tc llm.ToolCall, approvalErr *harness.ErrApprovalRequired, args map[string]any, duration int64) (string, error) {
	// 如果未配置审批处理器，直接返回错误
	if e.approvalHandler == nil {
		errMsg := fmt.Sprintf("[APPROVAL REQUIRED] %s: %s (未配置审批处理器，操作被拒绝)",
			approvalErr.Tool, approvalErr.Reason)
		e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type":        "approval_blocked",
			"approval_id": approvalErr.ApprovalID,
			"tool":        approvalErr.Tool,
			"rule":        approvalErr.RuleName,
			"namespace":   approvalErr.Namespace,
			"tags":        approvalErr.Tags,
			"reason":      approvalErr.Reason,
			"message":     "审批处理器未配置，操作被自动拒绝",
		}))
		e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", fmt.Errorf("%s", errMsg)
	}

	// 发射 system_info 事件，通知前端显示审批对话框
	e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type":        "approval_required",
		"approval_id": approvalErr.ApprovalID,
		"tool":        approvalErr.Tool,
		"rule":        approvalErr.RuleName,
		"namespace":   approvalErr.Namespace,
		"tags":        approvalErr.Tags,
		"reason":      approvalErr.Reason,
		"input":       approvalErr.Input,
		"duration_ms": duration,
	}))

	// 发射 tool_call_output 事件，让前端显示工具调用信息
	e.bus.SendEvent(event.NewEventWithSubTask("tool_call_output", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":        tc.Function.Name,
		"result":      map[string]any{"status": "pending_approval", "approval_id": approvalErr.ApprovalID},
		"duration_ms": duration,
	}))

	// 向前端发起审批请求
	if err := e.approvalHandler.RequestApproval(approvalErr.ApprovalID, approvalErr.Tool, approvalErr.Reason, approvalErr.Input, approvalErr.RuleName, approvalErr.Namespace, approvalErr.Tags); err != nil {
		log.Printf("[Engine] 审批请求发送失败: %v", err)
		e.bus.SendEvent(event.NewEventWithSubTask("tool_call_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       fmt.Sprintf("审批请求发送失败: %v", err),
			"duration_ms": duration,
			"args":        args,
		}))
		e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", fmt.Errorf("approval request failed: %w", err)
	}

	// 等待前端审批决定（默认超时 30 秒）
	approved, waitErr := e.approvalHandler.WaitForDecision(approvalErr.ApprovalID, 30*time.Second)
	if waitErr != nil {
		// 超时或等待错误 — 视为拒绝
		log.Printf("[Engine] 审批等待失败: %v", waitErr)
		e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type":        "approval_timeout",
			"approval_id": approvalErr.ApprovalID,
			"tool":        approvalErr.Tool,
			"reason":      "审批超时，操作被自动拒绝",
		}))
		e.bus.SendEvent(event.NewEventWithSubTask("tool_call_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       fmt.Sprintf("审批超时: %v", waitErr),
			"duration_ms": duration,
			"args":        args,
		}))
		e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", fmt.Errorf("approval timeout: %w", waitErr)
	}

	if !approved {
		// 用户拒绝了审批请求
		log.Printf("[Engine] 审批被拒绝: %s (%s)", approvalErr.Tool, approvalErr.ApprovalID)
		e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type":        "approval_denied",
			"approval_id": approvalErr.ApprovalID,
			"tool":        approvalErr.Tool,
			"reason":      "用户拒绝了此操作",
		}))
		e.bus.SendEvent(event.NewEventWithSubTask("tool_call_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       "用户拒绝了高风险操作",
			"duration_ms": duration,
			"args":        args,
		}))
		e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", fmt.Errorf("user denied approval for %s: %s", approvalErr.Tool, approvalErr.Reason)
	}

	// 用户批准 — 绕过 PolicyGate 直接执行工具调用
	log.Printf("[Engine] 审批通过: %s (%s), 执行工具调用", approvalErr.Tool, approvalErr.ApprovalID)
	e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type":        "approval_granted",
		"approval_id": approvalErr.ApprovalID,
		"tool":        approvalErr.Tool,
		"message":     "审批通过，正在执行工具调用",
	}))

	// 直接执行工具（不经过 PolicyGate，因为用户已批准）
	execStart := time.Now()
	toolCtx := tool.ExecuteContext{}
	if e.cfg.WorkspaceDir != "" {
		toolCtx.Workdir = e.cfg.WorkspaceDir
	}
	result, execErr := e.tools.ExecuteWithCtx(tc.Function.Name, toolCtx, args)
	execDuration := time.Since(execStart).Milliseconds()

	if execErr != nil {
		e.bus.SendEvent(event.NewEventWithSubTask("tool_call_failed", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"tool":        tc.Function.Name,
			"error":       execErr.Error(),
			"duration_ms": execDuration,
			"args":        args,
		}))
		e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
			"type": "tool_call",
		}))
		return "", execErr
	}

	// 工具执行成功 — 发射正常的事件流
	resultJSON, _ := json.Marshal(result)
	resultStr := string(resultJSON)

	e.bus.SendEvent(event.NewEventWithSubTask("tool_call_output", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":   tc.Function.Name,
		"result": result,
	}))
	e.bus.SendEvent(event.NewEventWithSubTask("tool_call_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"tool":        tc.Function.Name,
		"duration_ms": execDuration,
	}))
	e.bus.SendEvent(event.NewEventWithSubTask("observation", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"content": resultStr,
	}))
	e.bus.SendEvent(event.NewEventWithSubTask("step_complete", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type": "tool_call",
	}))

	// 持久化已批准的 tool_call step，以便历史回放正常工作。
	e.saveStep(StepRecord{
		TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
		Type: "tool_call", Status: "completed",
		ToolName: tc.Function.Name, ToolInput: args, ToolOutput: resultStr,
		DurationMs: int(execDuration),
	})

	// 同时持久化 observation step。
	e.saveStep(StepRecord{
		TaskID: e.taskID, AgentID: e.cfg.AgentID, StepIndex: e.stepIdx,
		Type: "observation", Status: "completed",
		Content: resultStr,
	})

	return resultStr, nil
}

// sendAgentMessage 通过 AgentBus 向另一个 agent 发送消息。
// 它用当前 agent 的信息构造 runtime.AgentMessage，通过 AgentBus 发出，
// 并发出 system_info 事件让前端展示 agent 间通信。
//
// TaskID 被嵌入到 Metadata["task_id"]，使下游持久化
// （orchestrator.AgentBus 的 persistFn）可以把消息路由到 agent_messages
// 表中正确的行。
//
// AgentBus 为 nil 时为 no-op——agent 间通信被禁用。
func (e *Engine) sendAgentMessage(toAgentID, msgType, content string) {
	e.sendAgentMessageWithSubTask(toAgentID, msgType, content)
}

// SendAgentMessage 是 sendAgentMessage 的公开变体，供外部调用方
// （cmd/server）把 AgentBus 消息注入到本 Engine 的 listener。
// toSubTaskID 参数可选；为空时消息路由给 agentID handler。
// 于 Phase 7-I 引入。
func (e *Engine) SendAgentMessage(msgType, toSubTaskID, content string) {
	if e.agentBus == nil {
		return
	}
	if toSubTaskID == "" {
		toSubTaskID = e.cfg.SubTaskID
	}
	msg := AgentMessage{
		FromAgentID:   e.cfg.AgentID,
		FromSubTaskID: e.cfg.SubTaskID,
		ToAgentID:     e.cfg.AgentID,
		SubTaskID:     toSubTaskID,
		Type:          msgType,
		Content:       content,
		Metadata: map[string]string{
			"task_id":          e.taskID,
			"from_agent_id":    e.cfg.AgentID,
			"from_sub_task_id": e.cfg.SubTaskID,
		},
	}
	e.agentBus.SendMessage(msg)
}

// sendAgentMessageWithSubTask 通过 AgentBus 向另一个 agent 发送消息，可选
// 指定目标 subTaskID。subTaskID 为空时，消息路由给 agent 的默认 handler。
// 于 Phase 7-I 引入。
func (e *Engine) sendAgentMessageWithSubTask(toSubTaskID, msgType, content string) {
	if e.agentBus == nil {
		return
	}

	msg := AgentMessage{
		FromAgentID:   e.cfg.AgentID,
		FromSubTaskID: e.cfg.SubTaskID,
		ToAgentID:     "",
		SubTaskID:     toSubTaskID,
		Type:          msgType,
		Content:       content,
		Metadata: map[string]string{
			"task_id":          e.taskID,
			"from_agent_id":    e.cfg.AgentID,
			"from_sub_task_id": e.cfg.SubTaskID,
		},
	}

	e.agentBus.SendMessage(msg)

	// 发出 system_info 事件，让前端可以在 UI 中展示该消息。
	e.bus.SendEvent(event.NewEventWithSubTask("system_info", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"type":           "agent_message_sent",
		"from_agent":     e.cfg.AgentID,
		"to_agent":       "",
		"to_sub_task_id": toSubTaskID,
		"msg_type":       msgType,
		"content":        content,
	}))
}

// repairToolArgumentsJSON 尝试对 LLM 产出的 malformed tool arguments 做
// best-effort 修复。生产环境中最常见的失败模式是长内容 payload 末尾的
// JSON string 或 object 未终止（例如 write_file 流式输出一个大 HTML 文件
// 时截断了闭合引号/大括号）。补上缺失的终止符通常就能得到可解析的对象，
// 避免一次额外的 LLM 往返。
//
// 此函数有意保持保守：只追加缺失的闭合 token，若结果仍非法就放弃。它不是
// 通用 JSON 修复器，但廉价地覆盖了高频场景。
func repairToolArgumentsJSON(raw string) (map[string]any, error) {
	// 按从轻到重的顺序尝试修复候选。
	candidates := []string{
		raw + "\"",     // 未终止的 string value
		raw + "\" }",   // 未终止的 string value + 闭合 object
		raw + "}",      // 未终止的 object
		raw + "\" } }", // 嵌套的未终止 string/object
	}

	for _, candidate := range candidates {
		var m map[string]any
		if err := json.Unmarshal([]byte(candidate), &m); err == nil {
			return m, nil
		}
	}
	return nil, fmt.Errorf("unable to repair arguments JSON")
}

// sanitizeToolCallArguments 返回 tc 的副本，保证 Function.Arguments 是
// 语法合法的 JSON。若原始 arguments 字符串已是合法 JSON，则保留原值；
// 否则替换为一个最小合法对象以记录原始错误，避免 malformed 嵌套 JSON
// 通过 400 BadRequestError 污染下一次 LLM API 请求。
func sanitizeToolCallArguments(tc llm.ToolCall) llm.ToolCall {
	sanitized := tc
	if !isValidToolArgumentsJSON(tc.Function.Arguments) {
		sanitized.Function.Arguments = `{"_error":"invalid arguments JSON"}`
	}
	return sanitized
}

// isValidToolArgumentsJSON 返回 s 是否为语法合法的 JSON。
// 在把 tool_calls 追加回对话历史时使用，使 malformed arguments（例如
// 流式产生的未终止字符串）不会通过 400 BadRequestError 污染下一次 LLM
// 请求。
func isValidToolArgumentsJSON(s string) bool {
	var dummy map[string]any
	return json.Unmarshal([]byte(s), &dummy) == nil
}

// saveCheckpoint 把当前 engine 状态作为 checkpoint 持久化以支持崩溃恢复。
// 在每次 ReAct loop 迭代结束（tool 执行后）调用。CheckpointManager 为 nil
// 时为 no-op。
//
// checkpoint 保存：
//   - 当前 step 序号
//   - 累计 token 数
//   - 完整对话历史（messages）
//   - 当前任务进度（若可用）
//
// 恢复时，RecoverFromCheckpoint 可把 engine 还原到此状态，并从中断处继续
// 执行。
func (e *Engine) saveCheckpoint() {
	if e.checkpoint == nil {
		return
	}

	if err := e.checkpoint.Save(e.taskID, e.cfg.AgentID, e.stepIdx, e.totalTokens, e.messages, e.taskProgress); err != nil {
		log.Printf("[Engine] Failed to save checkpoint: %v", err)
	}
}

// Pause 暂停当前 Engine 的 ReAct 循环。
//
// 这是 Phase 7-A 新增的对外控制接口，与 context cancel 行为不同：
//   - Pause 不取消 context，因此已经在执行的 LLM 流式请求不会被中断；
//   - Run loop 每轮开头会检查 paused 标志位，命中后阻塞在 resumeCh 上等待恢复；
//   - 多次 Pause 是幂等的（CompareAndSwap），不会重复发送事件。
//
// 调用方（cmd/server 的 control handler）通过 engineRegistry 查表后调用此方法。
func (e *Engine) Pause() {
	if !e.paused.CompareAndSwap(false, true) {
		// 已经处于暂停状态，幂等返回。
		return
	}
	// 通知前端此 agent 已进入 paused 状态。前端根据 agent_status 的 status 字段
	// 把对应 AgentState.status 切换为 'paused'，从而禁用 Cancel/Pause 之外的交互。
	e.bus.SendEvent(event.NewEventWithSubTask("agent_status", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"status": "paused",
	}))
}

// Resume 恢复被 Pause 阻塞的 Run 循环。
//
// 行为要点：
//   - 只在当前处于 paused 状态时才会唤醒（避免误触）；
//   - 通过关闭再重建 resumeCh 实现"一次性信号"，让下一次 Pause 仍可阻塞；
//   - 同时发送 agent_status=running 事件，让前端把状态切回 running；
//   - close + 新建 channel 的过程放在锁内，避免并发触发时出现 "close of closed channel" 竞态。
func (e *Engine) Resume() {
	if !e.paused.CompareAndSwap(true, false) {
		return
	}
	e.resumeMu.Lock()
	close(e.resumeCh)
	e.resumeCh = make(chan struct{})
	e.resumeMu.Unlock()
	e.bus.SendEvent(event.NewEventWithSubTask("agent_status", e.taskID, e.cfg.SubTaskID, e.cfg.AgentID, e.stepIdx, map[string]any{
		"status": "running",
	}))
}

// IsPaused 返回当前 Engine 是否处于暂停状态。仅用于诊断与单测，不参与主循环逻辑。
func (e *Engine) IsPaused() bool {
	return e.paused.Load()
}
