// Package orchestrator 实现多 agent 的编排层(orchestration layer)。
//
// # Architecture(架构)
//
// orchestrator 位于 Engine 之上，负责协调多个并发运行的 agent。它的职责包括：
//  1. Task decomposition(任务分解) — 把用户请求拆分为不同 agent 的子任务
//  2. Agent lifecycle(agent 生命周期) — 启动、监控、停止 agent goroutine
//  3. Event routing(事件路由) — 确保每个 agent 的事件都正确打上 agent_id 标签
//  4. Progress aggregation(进度聚合) — 把多个 agent 的进度合并为统一视图
//  5. Agent communication(agent 间通信) — agent 之间通过 AgentBus 互相调用
//
// # Design Philosophy(设计哲学)
//
// orchestrator 不是"黑盒"调度器。它在每一次生命周期状态切换
// （agent_started、agent_completed、agent_failed）时都发出事件，让前端能够
// 实时渲染多 agent 的执行过程。每个 agent 作为独立的 goroutine 运行，拥有
// 自己的 Engine，并通过共享的 WebSocket Hub 广播事件。
//
// # Agent Communication(agent 间通信)
//
// agent 之间通过 AgentBus —— 一个轻量的消息传递层 —— 进行通信。Agent A 向
// Agent B 发送消息，orchestrator 负责路由，Agent B 的 Engine 把它作为
// ReAct loop 中的一条"user"消息处理。这样可以支持如下模式：
//   - Code Review(代码评审)：Agent A 写代码 → Agent B 评审 → Agent A 修复问题
//   - Research Dispatcher(研究派发)：orchestrator 把子问题 fan-out 给多个研究 agent
//   - Supervisor(监督者)：orchestrator 监控 agent 输出并在需要时介入
//
// # Concurrency Model(并发模型)
//
// 每个 agent 在自己的 goroutine 中运行。orchestrator 用 WaitGroup 追踪所有
// agent 的完成情况。agent 共享同一个 WebSocket Hub（goroutine-safe）来广播
// 事件。orchestrator 不在 agent 之间共享状态 —— 每个 agent 有自己的 Engine、
// 自己的对话历史和自己的 tool registry。通信完全通过 AgentBus 显式进行。
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/observability"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// AgentSpec 定义由 orchestrator 启动的单个 agent。
// 每个 agent 有自己的配置、system prompt 和任务。
type AgentSpec struct {
	// AgentID 是该 agent 的唯一标识（例如 "code_writer"、"reviewer"）。
	AgentID string `json:"agent_id"`

	// Name 是人类可读的显示名（例如 "Code Writer"、"Code Reviewer"）。
	Name string `json:"name"`

	// SystemPrompt 定义该 agent 的人格、能力和约束。
	// 每个 agent 类型（writer、reviewer、researcher）都有不同的 system prompt。
	SystemPrompt string `json:"system_prompt"`

	// Input 是该 agent 自己的任务描述。
	Input string `json:"input"`

	// Model 是该 agent 使用的 LLM model。为空时使用 orchestrator 的默认 model。
	Model string `json:"model,omitempty"`

	// Contract 是该 agent 的 TaskContract，定义范围、预算、可用 tool 等。
	// 为 nil 时使用 DefaultContract(Input)。
	Contract *harness.TaskContract `json:"contract,omitempty"`

	// AllowedTools 是该 agent 允许使用的 tool 名称列表。
	// 为空时表示所有已注册的 tool 都可用。
	AllowedTools []string `json:"allowed_tools,omitempty"`

	// ParentAgentID 是派生该 agent 的父 agent（用于 agent 间通信）。
	// 顶层 agent 此字段为空。
	ParentAgentID string `json:"parent_agent_id,omitempty"`

	// OutputTo 是该 agent 完成时通过 AgentBus 接收其最终结果的 agent ID 列表。
	// 这把数据流与执行策略（parallel vs sequential）解耦：一个 agent 可以
	// 把输出转发给其它 agent，无论它们是否同时运行。如果目标 agent 在
	// parallel 下尚未运行，消息会被入队，等目标 agent 注册 handler 后再投递。
	OutputTo []string `json:"output_to,omitempty"`

	// WorkingMemory 是来自先前任务的可选上下文，在 agent 启动前注入到
	// system prompt 中。由 MemoryRecall 在 orchestration 之前构建。设置后
	// 会被前置到 system prompt 中。
	WorkingMemory string `json:"working_memory,omitempty"`
}

// AgentResult 保存单个 agent 执行的结果。
type AgentResult struct {
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Status      string `json:"status"` // "completed"、"failed"、"cancelled"
	Result      string `json:"result"`
	TotalTokens int    `json:"total_tokens"`
	Error       string `json:"error,omitempty"`
	Duration    int64  `json:"duration_ms"`
}

// WorkflowEdge 描述 DAG 中两个 agent 节点之间的依赖与可选执行条件。
// From 是上游 agent 的 AgentID，To 是下游 agent 的 AgentID。
// Condition 为空时表示“上游必须 completed”；非空时使用轻量 DSL 在运行时求值。
type WorkflowEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
}

// WorkflowNode 是 DAG 中的单个 agent 节点。
// 除了 agent 规格外，还声明直接依赖（Dependencies）与作用于本节点的
// 执行条件（Condition）。当 AgentWorkflow.Edges 为空时，Dependencies
// 用于隐式构造边。
type WorkflowNode struct {
	Agent        AgentSpec `json:"agent"`
	Dependencies []string  `json:"dependencies,omitempty"`
	Condition    string    `json:"condition,omitempty"`
}

// AgentWorkflow 是 Phase 7-H2 阶段 5 引入的 DAG 编排结构。
// 它与扁平的 Agents/Strategy 向后兼容：Workflow 为 nil 时，编排器回退
// 到原有 strategy 调度逻辑。
type AgentWorkflow struct {
	Nodes []WorkflowNode `json:"nodes"`
	Edges []WorkflowEdge `json:"edges,omitempty"`
}

// Orchestrator 负责管理多个并发运行的 agent。
//
// # Lifecycle(生命周期)
//
//  1. 用 New() 创建 orchestrator
//  2. 用一组 AgentSpec 调用 Run()
//  3. orchestrator 在各自的 goroutine 中启动每个 agent
//  4. 每个 agent 通过共享的 Hub 发出事件
//  5. orchestrator 等待所有 agent 完成
//  6. 返回聚合后的结果
//
// # Usage(用法)
//
//	orch := orchestrator.New(hub, cfg, tools, persist, agentBus, checkpointMgr)
//	results := orch.RunBlocking(ctx, specs)
//	for _, r := range results {
//	    fmt.Printf("%s: %s (%d tokens)\n", r.Name, r.Status, r.TotalTokens)
//	}
type Orchestrator struct {
	// hub 相关引用
	hub           *ws.Hub
	cfg           *config.Config
	tools         *tool.Registry
	persist       runtime.Persistence
	agentBus      *AgentBusAdapter           // Phase 5: agent 间通信
	checkpointMgr *runtime.CheckpointManager // Phase 5: 崩溃恢复

	// Phase 6 Router：可选的 model router 及 provider lookup，由所有子 agent
	// 共享。非 nil 时，每个 agent 的 Engine 在每次 LLM 调用前都会做意图分类
	// 并选择 model tier（见 engine.go:1115）。为 nil 时，agent 退回到
	// cfg.LLMModel 的直接路径（legacy 行为）。
	modelRouter     *llm.Router
	routerRegistry  *llm.ModelRegistry
	routerProviders map[string]llm.Provider

	// Phase 7-H2: 可选 tracer，用于为 orchestrator 调度的子 agent 生成
	// span。tracer 为 nil 时保留旧行为。
	tracer interface {
		StartRoot(taskID, operation string) *observability.TraceContext
		StartChild(parent *observability.TraceContext, agentID, operation string) *observability.TraceContext
		Finish(ctx *observability.TraceContext, err error)
		FinishWithAttributes(ctx *observability.TraceContext, err error, attrs map[string]any)
	}
	rootTraceCtx *observability.TraceContext
}

// New 创建一个新的 Orchestrator。
// agentBus 和 checkpointMgr 可以为 nil —— 为 nil 时禁用多 agent 通信和
// checkpoint 功能。
// modelRouter/routerRegistry/routerProviders 是可选的 Phase 6 Router 依赖；
// 三个都传 nil 时，子 agent 保留 legacy 的单 model 行为。
func New(hub *ws.Hub, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, agentBus *AgentBusAdapter, checkpointMgr *runtime.CheckpointManager, modelRouter *llm.Router, routerRegistry *llm.ModelRegistry, routerProviders map[string]llm.Provider) *Orchestrator {
	return &Orchestrator{
		hub:             hub,
		cfg:             cfg,
		tools:           tools,
		persist:         persist,
		agentBus:        agentBus,
		checkpointMgr:   checkpointMgr,
		modelRouter:     modelRouter,
		routerRegistry:  routerRegistry,
		routerProviders: routerProviders,
	}
}

// SetTools 允许在工具注册表创建后再设置，解决 Phase 7-H 中 dispatcher
// 依赖 orchestrator、而 orchestrator 又依赖 tool registry 的初始化顺序问题。
func (o *Orchestrator) SetTools(tools *tool.Registry) {
	o.tools = tools
}

// SetAgentBus 允许在 AgentBus 创建后再设置。
func (o *Orchestrator) SetAgentBus(agentBus *AgentBusAdapter) {
	o.agentBus = agentBus
}

// SetPersistence 允许在 persistence 创建后再设置。
func (o *Orchestrator) SetPersistence(persist runtime.Persistence) {
	o.persist = persist
}

// SetTracer 设置 tracer 与 root trace context。
// 设置后，RunBlocking 会为本次 orchestration 生成 root span，并在每个
// 子 agent 的 EngineConfig 中透传 Tracer/RootTraceCtx。
func (o *Orchestrator) SetTracer(tracer interface {
	StartRoot(taskID, operation string) *observability.TraceContext
	StartChild(parent *observability.TraceContext, agentID, operation string) *observability.TraceContext
	Finish(ctx *observability.TraceContext, err error)
	FinishWithAttributes(ctx *observability.TraceContext, err error, attrs map[string]any)
}, rootTraceCtx *observability.TraceContext) {
	o.tracer = tracer
	o.rootTraceCtx = rootTraceCtx
}

// RunBlocking 启动所有 agent 并阻塞直到全部完成。
// 返回每个 agent 一个结果的 slice，顺序与输入 specs 一致。
//
// 当 strategy 为 "sequential" 时，agent 依次执行：agent i 的结果会通过
// AgentBus 在 agent i+1 启动前发送给它。这样可以支持 researcher → writer
// 这种 writer 需要研究输出的流水线。
// 其它任何 strategy（包括空字符串）都按原来的并发方式运行。
//
// rootTaskID 会被透传给每个 agent，使子任务可以把自己的 parent_task_id
// 设为根任务。这是持久化层用来构建 child_tasks 树的 hook。
func (o *Orchestrator) RunBlocking(ctx context.Context, rootTaskID string, strategy string, specs []AgentSpec) []AgentResult {
	results := make([]AgentResult, len(specs))

	// Phase 7-H2: 若 tracer 已设置，为本次 orchestration 启动 root span。
	var orchTraceCtx *observability.TraceContext
	if o.tracer != nil {
		orchTraceCtx = o.tracer.StartRoot(rootTaskID, "orchestrate")
	}

	// Phase 7-H2 MA6: 编排层 step 事件计数器，用于在 root task 下生成连续的
	// orchestrator step_index。从 1 开始，避免与 task_started/tracer 等事件混淆。
	orchStepIdx := 1
	nextOrchStep := func() int {
		v := orchStepIdx
		orchStepIdx++
		return v
	}

	// 发送 decompose_done 事件，让前端知道 orchestrator 已拿到拆分决策。
	agentIDs := make([]string, len(specs))
	for i, s := range specs {
		agentIDs[i] = s.AgentID
	}
	o.hub.SendEvent(event.NewEvent("decompose_done", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
		"strategy":    strategy,
		"agent_count": len(specs),
		"agent_ids":   agentIDs,
	}))

	// Phase 7-C: "pipeline" 通过 OutputTo 链式转发实现，底层复用 parallel 调度。
	if strategy == "pipeline" {
		for i := 0; i < len(specs)-1; i++ {
			specs[i].OutputTo = append(specs[i].OutputTo, specs[i+1].AgentID)
		}
		strategy = "parallel"
	}
	if strategy == "sequential" {
		// Sequential pipeline(顺序流水线)：agent 依次执行。每个 agent 的输出
		// 在下一个 agent 启动前通过 AgentBus 转发，形成 researcher -> writer
		// 这样的链路，writer 会把 researcher 的输出作为 user message 看到并
		// 进入自己的对话历史。
		for i, spec := range specs {
			o.hub.SendEvent(event.NewEvent("agent_dispatched", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
				"agent_id":   spec.AgentID,
				"agent_name": spec.Name,
				"mode":       "sequential",
				"sequence":   i,
			}))
			results[i] = o.runAgent(ctx, rootTaskID, spec, nextOrchStep)
			// agent_completed 事件携带 worker summary，供前端编排 lane 展示。
			o.hub.SendEvent(event.NewEvent("agent_completed", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
				"agent_id":     spec.AgentID,
				"agent_name":   spec.Name,
				"status":       results[i].Status,
				"total_tokens": results[i].TotalTokens,
				"duration_ms":  results[i].Duration,
				"result":       results[i].Result,
				"error":        results[i].Error,
			}))
			if i+1 < len(specs) && results[i].Status == "completed" && o.agentBus != nil {
				next := specs[i+1]
				o.agentBus.SendMessage(runtime.AgentMessage{
					FromAgentID:   spec.AgentID,
					FromSubTaskID: rootTaskID + "_" + spec.AgentID,
					ToAgentID:     next.AgentID,
					SubTaskID:     rootTaskID + "_" + next.AgentID,
					Type:          "observation",
					Content:       results[i].Result,
				})
			}
		}
	} else {
		var wg sync.WaitGroup

		for i, spec := range specs {
			wg.Add(1)
			go func(idx int, s AgentSpec) {
				defer wg.Done()
				o.hub.SendEvent(event.NewEvent("agent_dispatched", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
					"agent_id":   s.AgentID,
					"agent_name": s.Name,
					"mode":       "parallel",
					"sequence":   idx,
				}))

				results[idx] = o.runAgent(ctx, rootTaskID, s, nextOrchStep)

				// agent_completed 事件携带 worker summary，供前端编排 lane 展示。
				o.hub.SendEvent(event.NewEvent("agent_completed", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
					"agent_id":     s.AgentID,
					"agent_name":   s.Name,
					"status":       results[idx].Status,
					"total_tokens": results[idx].TotalTokens,
					"duration_ms":  results[idx].Duration,
					"result":       results[idx].Result,
					"error":        results[idx].Error,
				}))
				// 把结果转发给 OutputTo 中声明的目标 agent，即使在
				// parallel 下也一样。AgentBus 会在目标尚未注册 handler 时把消息入队。
				if results[idx].Status == "completed" && o.agentBus != nil {
					for _, targetID := range s.OutputTo {
						toSubTaskID := rootTaskID + "_" + targetID
						// Phase 7-J: 如果目标是 leader，使用 rootTaskID 作为其 SubTaskID，
						// 因为 leader Engine 按 (leader, rootTaskID) 注册 handler。
						if targetID == "leader" {
							toSubTaskID = rootTaskID
						}
						o.agentBus.SendMessage(runtime.AgentMessage{
							FromAgentID:   s.AgentID,
							FromSubTaskID: rootTaskID + "_" + s.AgentID,
							ToAgentID:     targetID,
							SubTaskID:     toSubTaskID,
							Type:          "observation",
							Content:       results[idx].Result,
						})
					}
				}
			}(i, spec)
		}

		wg.Wait()
	}

	return o.runBlockingCommon(rootTaskID, strategy, results, nextOrchStep, orchTraceCtx)
}

// runBlockingCommon 完成编排层事件、root 终态聚合与持久化。
// 现有 RunBlocking 与新的 RunBlockingDAG 最终都调用它，避免重复代码。
func (o *Orchestrator) runBlockingCommon(rootTaskID, strategy string, results []AgentResult, nextOrchStep func() int, orchTraceCtx *observability.TraceContext) []AgentResult {
	rootStatus := "completed"
	rootTokens := 0
	resultItems := make([]map[string]any, 0, len(results))
	for _, r := range results {
		rootTokens += r.TotalTokens
		if r.Status != "completed" {
			rootStatus = "failed"
		}
		resultItems = append(resultItems, map[string]any{
			"agent_id":     r.AgentID,
			"agent_name":   r.Name,
			"status":       r.Status,
			"total_tokens": r.TotalTokens,
			"duration_ms":  r.Duration,
			"result":       r.Result,
			"error":        r.Error,
		})
	}

	// Phase 7-H2 MA6: root final_result 由各 worker 结果聚合为可读摘要，
	// 不再是空壳 "all agents completed"。失败情况下保留可读短语以便 UI 展示。
	var rootResult string
	if rootStatus == "failed" {
		rootResult = "one or more child agents failed"
		var failed []string
		for _, r := range results {
			if r.Status != "completed" {
				failed = append(failed, fmt.Sprintf("%s: %s", r.AgentID, r.Error))
			}
		}
		if len(failed) > 0 {
			rootResult += "\n" + strings.Join(failed, "\n")
		}
	} else {
		summary, _ := json.Marshal(resultItems)
		rootResult = "All agents completed. Worker summaries:\n" + string(summary)
	}
	if o.persist != nil {
		if err := o.persist.UpdateTask(rootTaskID, rootStatus, rootResult, rootTokens); err != nil {
			log.Printf("[Orchestrator] Failed to update root task %s status: %v", rootTaskID, err)
		}
	}
	if status := rootStatus; status == "completed" {
		o.hub.SendEvent(event.NewEvent("task_completed", rootTaskID, "orchestrator", 0, map[string]any{
			"result":       rootResult,
			"total_tokens": rootTokens,
		}))
	} else {
		o.hub.SendEvent(event.NewEvent("task_failed", rootTaskID, "orchestrator", 0, map[string]any{
			"reason":       rootResult,
			"total_tokens": rootTokens,
		}))
	}
	if o.tracer != nil && orchTraceCtx != nil {
		o.tracer.Finish(orchTraceCtx, nil)
	}

	return results
}

// evaluateWorkflowCondition 以当前已知结果评估轻量条件表达式。
// 支持的语法：
//   - 空字符串：true（表示无条件依赖，仅要求前置 agent 完成）
//   - "<agent_id>.completed" / "<agent_id>.failed" / "<agent_id>.succeeded"：按前置节点状态判断
//   - 用 "&&" / "||" 连接的多条子表达式
//   - 可用圆括号分组
// 返回 bool 与错误；解析失败时返回 false，并让上层回退为 false 以免阻塞。
func evaluateWorkflowCondition(expr string, resultsByID map[string]AgentResult) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true
	}

	// 把 agent.status token 替换为布尔字面量，然后交给 govaluate 风格最直接的
	// 方式：递归解析括号/&&/||，或如本项目避免新依赖，手写 token-based evaluator。
	// 这里采用极简 tokenizer + shunting-yard + 常量求值。
	tokens := tokenizeCondition(expr, resultsByID)
	if len(tokens) == 0 {
		return true
	}

	postfix, err := shuntingYard(tokens)
	if err != nil {
		log.Printf("[Orchestrator] condition parse error '%s': %v", expr, err)
		return false
	}
	return evalPostfix(postfix)
}

// conditionToken 描述条件表达式中的最小单元。
type conditionToken struct {
	typ   string // "bool", "and", "or", "lparen", "rparen"
	value bool   // 仅 typ == "bool" 时有效
}

// tokenizeCondition 把表达式拆分为 token。现在仅支持 <agent_id>.<status> 形式。
func tokenizeCondition(expr string, resultsByID map[string]AgentResult) []conditionToken {
	var tokens []conditionToken
	expr = strings.TrimSpace(expr)
	for expr != "" {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			break
		}
		switch {
		case strings.HasPrefix(expr, "&&"):
			tokens = append(tokens, conditionToken{typ: "and"})
			expr = expr[2:]
		case strings.HasPrefix(expr, "||"):
			tokens = append(tokens, conditionToken{typ: "or"})
			expr = expr[2:]
		case strings.HasPrefix(expr, "("):
			tokens = append(tokens, conditionToken{typ: "lparen"})
			expr = expr[1:]
		case strings.HasPrefix(expr, ")"):
			tokens = append(tokens, conditionToken{typ: "rparen"})
			expr = expr[1:]
		default:
			// 读取到下一个操作符或括号。
			end := len(expr)
			for i := 0; i < len(expr); i++ {
				if expr[i] == '&' || expr[i] == '|' || expr[i] == '(' || expr[i] == ')' {
					end = i
					break
				}
			}
			atom := strings.TrimSpace(expr[:end])
			expr = expr[end:]
			if atom == "" {
				continue
			}
			parts := strings.SplitN(atom, ".", 2)
			if len(parts) != 2 {
				tokens = append(tokens, conditionToken{typ: "bool", value: false})
				continue
			}
			res, ok := resultsByID[parts[0]]
			value := false
			if ok {
				switch strings.ToLower(parts[1]) {
				case "completed", "succeeded", "success":
					value = res.Status == "completed"
				case "failed":
					value = res.Status == "failed"
				default:
					value = res.Status == "completed"
				}
			}
			tokens = append(tokens, conditionToken{typ: "bool", value: value})
		}
	}
	return tokens
}

// shuntingYard 把中缀 token 数组转换为后缀表达式，支持 && || 和括号。
func shuntingYard(tokens []conditionToken) ([]conditionToken, error) {
	precedence := map[string]int{"and": 1, "or": 0}
	var out []conditionToken
	var stack []conditionToken
	for _, t := range tokens {
		switch t.typ {
		case "bool":
			out = append(out, t)
		case "lparen":
			stack = append(stack, t)
		case "rparen":
			for len(stack) > 0 && stack[len(stack)-1].typ != "lparen" {
				out = append(out, stack[len(stack)-1])
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 {
				return nil, fmt.Errorf("mismatched parenthesis")
			}
			stack = stack[:len(stack)-1]
		case "and", "or":
			for len(stack) > 0 {
				top := stack[len(stack)-1]
				if top.typ == "lparen" || precedence[top.typ] < precedence[t.typ] {
					break
				}
				out = append(out, top)
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, t)
		default:
			return nil, fmt.Errorf("unknown token type %s", t.typ)
		}
	}
	for len(stack) > 0 {
		if stack[len(stack)-1].typ == "lparen" {
			return nil, fmt.Errorf("mismatched parenthesis")
		}
		out = append(out, stack[len(stack)-1])
		stack = stack[:len(stack)-1]
	}
	return out, nil
}

// evalPostfix 求值后缀表达式。
func evalPostfix(tokens []conditionToken) bool {
	var stack []bool
	for _, t := range tokens {
		switch t.typ {
		case "bool":
			stack = append(stack, t.value)
		case "and", "or":
			if len(stack) < 2 {
				return false
			}
			b := stack[len(stack)-1]
			a := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			if t.typ == "and" {
				stack = append(stack, a && b)
			} else {
				stack = append(stack, a || b)
			}
		default:
			return false
		}
	}
	if len(stack) != 1 {
		return false
	}
	return stack[0]
}

// RunBlockingDAG 按 AgentWorkflow 的 DAG 拓扑调度执行 agent。
// 只有所有依赖都完成（且满足 condition）时才启动下游 agent；如果某个依赖失败
// 导致整个 workflow 无法继续，orchestrator 会把剩余节点标记为 skipped 并完成。
func (o *Orchestrator) RunBlockingDAG(ctx context.Context, rootTaskID string, workflow *AgentWorkflow) []AgentResult {
	if workflow == nil || len(workflow.Nodes) == 0 {
		return nil
	}

	// Phase 7-H2: 为本次 orchestration 启动 root span。
	var orchTraceCtx *observability.TraceContext
	if o.tracer != nil {
		orchTraceCtx = o.tracer.StartRoot(rootTaskID, "orchestrate-dag")
	}

	orchStepIdx := 1
	nextOrchStep := func() int {
		v := orchStepIdx
		orchStepIdx++
		return v
	}

	nodeMap := make(map[string]WorkflowNode, len(workflow.Nodes))
	agentIDs := make([]string, 0, len(workflow.Nodes))
	for _, n := range workflow.Nodes {
		nodeMap[n.Agent.AgentID] = n
		agentIDs = append(agentIDs, n.Agent.AgentID)
	}

	o.hub.SendEvent(event.NewEvent("decompose_done", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
		"strategy":    "dag",
		"agent_count": len(workflow.Nodes),
		"agent_ids":   agentIDs,
		"workflow": map[string]any{
			"edges": workflow.Edges,
		},
	}))

	// 构造邻接表与入度计数。Edges 为空时从 node.Dependencies 隐式建边。
	inDegree := make(map[string]int, len(workflow.Nodes))
	outEdges := make(map[string][]WorkflowEdge, len(workflow.Nodes))
	for _, n := range workflow.Nodes {
		inDegree[n.Agent.AgentID] = 0
	}
	for _, n := range workflow.Nodes {
		for _, dep := range n.Dependencies {
			if _, ok := nodeMap[dep]; ok {
				outEdges[dep] = append(outEdges[dep], WorkflowEdge{From: dep, To: n.Agent.AgentID, Condition: n.Condition})
				inDegree[n.Agent.AgentID]++
			}
		}
	}
	for _, e := range workflow.Edges {
		if _, ok := nodeMap[e.From]; ok {
			if _, ok2 := nodeMap[e.To]; ok2 {
				outEdges[e.From] = append(outEdges[e.From], e)
				inDegree[e.To]++
			}
		}
	}

	results := make([]AgentResult, 0, len(workflow.Nodes))
	resultsByID := make(map[string]AgentResult)
	mu := sync.Mutex{}

	// Kahn 算法调度：先执行入度为 0 的节点。依赖完成后再把满足条件的下游入度减一。
	pending := make([]string, 0, len(workflow.Nodes))
	for id, deg := range inDegree {
		if deg == 0 {
			pending = append(pending, id)
		}
	}

	var runWg sync.WaitGroup
	completedCh := make(chan string, len(workflow.Nodes))
	skippedCh := make(chan string, len(workflow.Nodes))

	// runOne 执行单个节点。
	runOne := func(id string) {
		defer runWg.Done()
		n := nodeMap[id]
		o.hub.SendEvent(event.NewEvent("agent_dispatched", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
			"agent_id":   n.Agent.AgentID,
			"agent_name": n.Agent.Name,
			"mode":       "dag",
			"condition":  n.Condition,
		}))
		r := o.runAgent(ctx, rootTaskID, n.Agent, nextOrchStep)
		mu.Lock()
		results = append(results, r)
		resultsByID[r.AgentID] = r
		mu.Unlock()
		o.hub.SendEvent(event.NewEvent("agent_completed", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
			"agent_id":     r.AgentID,
			"agent_name":   r.Name,
			"status":       r.Status,
			"total_tokens": r.TotalTokens,
			"duration_ms":  r.Duration,
			"result":       r.Result,
			"error":        r.Error,
		}))
		completedCh <- id
	}

	active := 0
	for _, id := range pending {
		runWg.Add(1)
		go runOne(id)
		active++
	}

	// doneCount 跟踪已完成/跳过的节点总数，防止 goroutine 泄漏。
	doneCount := 0
	totalNodes := len(workflow.Nodes)
	for doneCount < totalNodes {
		select {
		case id := <-completedCh:
			doneCount++
			active--
			mu.Lock()
			res := resultsByID[id]
			mu.Unlock()
			for _, edge := range outEdges[id] {
				// 依赖完成且满足边条件时才启动下游。
				if res.Status != "completed" {
					continue
				}
				// 节点自身 condition 与边 condition 同时满足。
				nodeCondOK := evaluateWorkflowCondition(nodeMap[edge.To].Condition, resultsByID)
				edgeCondOK := evaluateWorkflowCondition(edge.Condition, resultsByID)
				if !nodeCondOK || !edgeCondOK {
					// 条件不满足：计数器递减，若归零则置为 skipped。
					mu.Lock()
					inDegree[edge.To]--
					deg := inDegree[edge.To]
					mu.Unlock()
					if deg == 0 {
						doneCount++
						o.hub.SendEvent(event.NewEvent("agent_completed", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
							"agent_id":    edge.To,
							"agent_name":  nodeMap[edge.To].Agent.Name,
							"status":      "skipped",
							"skip_reason": "condition not satisfied",
						}))
						mu.Lock()
						results = append(results, AgentResult{
							AgentID:  edge.To,
							Name:     nodeMap[edge.To].Agent.Name,
							Status:   "skipped",
							Result:   "condition not satisfied",
						})
						resultsByID[edge.To] = results[len(results)-1]
						mu.Unlock()
						skippedCh <- edge.To
					}
					continue
				}
				mu.Lock()
				inDegree[edge.To]--
				deg := inDegree[edge.To]
				mu.Unlock()
				if deg == 0 {
					runWg.Add(1)
					go runOne(edge.To)
					active++
				}
			}
		case id := <-skippedCh:
			// skipped 节点本身不触发新的边，但要通知它的下游继续减入度。
			doneCount++
			for _, edge := range outEdges[id] {
				mu.Lock()
				inDegree[edge.To]--
				deg := inDegree[edge.To]
				mu.Unlock()
				if deg == 0 {
					doneCount++
					o.hub.SendEvent(event.NewEvent("agent_completed", rootTaskID, "orchestrator", nextOrchStep(), map[string]any{
						"agent_id":    edge.To,
						"agent_name":  nodeMap[edge.To].Agent.Name,
						"status":      "skipped",
						"skip_reason": "upstream skipped",
					}))
					mu.Lock()
					results = append(results, AgentResult{
						AgentID:  edge.To,
						Name:     nodeMap[edge.To].Agent.Name,
						Status:   "skipped",
						Result:   "upstream skipped",
					})
					resultsByID[edge.To] = results[len(results)-1]
					mu.Unlock()
					skippedCh <- edge.To
				}
			}
		case <-ctx.Done():
			// 超时或被取消：停止产生新的 agent，已有的返回当前结果。
			doneCount = totalNodes
		}
	}

	close(completedCh)
	close(skippedCh)
	runWg.Wait()

	// 确保 resultsByID 包含所有节点；缺失的视为 skipped（只在 ctx 取消时可能）。
	for _, n := range workflow.Nodes {
		if _, ok := resultsByID[n.Agent.AgentID]; !ok {
			resultsByID[n.Agent.AgentID] = AgentResult{
				AgentID: n.Agent.AgentID,
				Name:    n.Agent.Name,
				Status:  "skipped",
				Result:  "workflow cancelled or timed out",
			}
			results = append(results, resultsByID[n.Agent.AgentID])
		}
	}

	return o.runBlockingCommon(rootTaskID, "dag", results, nextOrchStep, orchTraceCtx)
}

// RunWithCallback 并发启动 agent，并在每个 agent 完成时调用 onResult。
// 这样调用方可以在结果到达时立即处理，而不必等待所有 agent 全部完成。
//
// rootTaskID 会被透传给每个 agent，使子任务可以把自己的 parent_task_id
// 设为根任务。
func (o *Orchestrator) RunWithCallback(ctx context.Context, rootTaskID string, specs []AgentSpec, onResult func(AgentResult)) {
	var wg sync.WaitGroup

	// RunWithCallback 无需编排层 step 事件；透传一个 no-op 计数器保持
	// runAgent 签名一致，避免调用方无感知破坏。
	noopOrchStep := func() int { return 0 }

	for _, spec := range specs {
		wg.Add(1)
		go func(s AgentSpec) {
			defer wg.Done()
			result := o.runAgent(ctx, rootTaskID, s, noopOrchStep)
			onResult(result)
		}(spec)
	}

	wg.Wait()
}

// runAgent 启动单个 agent 并返回其结果。
// 这是为单个 agent spec 创建并运行 Engine 的核心方法。
//
// rootTaskID 是该 agent 所属的父/根任务。agent 自己的 sub-task ID 由
// rootTaskID + "_" + spec.AgentID 推导得到。我们会同时持久化任务行及其
// meta 行，使 parent_task_id 指回根任务，从而能通过
// QueryChildTasks(rootTaskID) 查到。
func (o *Orchestrator) runAgent(ctx context.Context, rootTaskID string, spec AgentSpec, _ func() int) AgentResult {
	start := time.Now()

	// 构建该 agent 的 contract
	contract := harness.DefaultContract(spec.Input)
	if spec.Contract != nil {
		contract = *spec.Contract
	}
	contract.Goal = spec.Input
	if len(spec.AllowedTools) > 0 {
		contract.AllowedTools = spec.AllowedTools
	}

	// 构建完整的规则链 PolicyGate（与 main.go:886-896 保持一致）
	tokenBudgetRule := &harness.TokenBudgetRule{}
	costBudgetRule := harness.NewCostBudgetRule()
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		&harness.DangerousCommandRule{},
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
		costBudgetRule,
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	// 进度跟踪
	progressManager := harness.NewProgressManager()

	// 若 agent 指定了 model 则使用之，否则使用默认 model
	model := spec.Model
	if model == "" {
		model = o.cfg.LLMModel
	}

	// 根据 mock/全局配置解析 LLM Provider。provider 每个 agent 创建一次，
	// 然后传给 Engine，使 mock 开关（LLM_USE_MOCK / LLMRealCases /
	// LLMMockEndpoints）都能被正确遵守。
	// 出错时记日志并退回 nil；Engine 会用 Endpoint/APIKey/Model 创建一个
	// 默认的 OpenAIProvider。
	provider, err := llm.CreateProviderFromConfig(o.cfg, model, "")
	if err != nil {
		log.Printf("[Orchestrator] Failed to create provider for agent=%s (falling back to default): %v", spec.AgentID, err)
		provider = nil
	}

	// 推导该 agent 的 sub-task ID，并查询 session ID，以便持久化父子关系。
	// SaveTaskMeta 需要 session ID；我们通过 QueryTaskByID 从根任务记录里读取。
	subTaskID := rootTaskID + "_" + spec.AgentID
	var sessionID string
	if o.persist != nil {
		sessionID = o.persist.QueryTaskSessionID(rootTaskID)
	}

	// 解析 session 的 workspace_dir，这样 run_shell 之类的 tool 就能在
	// 正确的 CWD 下执行，而无需 LLM 每次都显式传入。
	workspaceDir := ""
	if sessionID != "" {
		if sess, err := db.QuerySessionByID(sessionID); err == nil && sess.WorkspaceDir != "" {
			workspaceDir = sess.WorkspaceDir
		}
	}

	// OnLLMUsage 把累计 cost 喂给 costBudgetRule，这样当 USD 预算超限时
	// PolicyChain 可以阻止后续 tool 调用。与 main.go:888-895
	// （handleMultiAgent）和 main.go:1173-1181（handleRecoverCheckpoint）对齐。
	// 价格估算：deepseek-v4-flash 约 $0.05 / 1M input tokens、$0.10 / 1M output。
	onUsage := func(model string, _ *llm.ModelProfile, usage llm.Usage) {
		// 简单的成本估算；如需精确计费，请传入 *ModelRegistry。
		cost := (float64(usage.PromptTokens)*0.05 + float64(usage.CompletionTokens)*0.10) / 1_000_000
		costBudgetRule.SetCost(cost)
	}

	engine := runtime.NewEngine(runtime.EngineConfig{
		AgentID:           spec.AgentID,
		SystemPrompt:      spec.SystemPrompt,
		Model:             model,
		Endpoint:          o.cfg.LLMEndpoint,
		APIKey:            o.cfg.LLMAPIKey,
		Provider:          provider, // 上面解析出的 mock 或真实 provider
		CaseID:            "",       // orchestrator 的 specs 暂不携带 case ID
		Temperature:       0.7,
		MaxTokens:         4096,
		MaxSteps:          contract.MaxSteps,
		Persistence:       o.persist,
		PolicyGate:        policyGate,
		Progress:          progressManager,
		Contract:          contract,
		WorkingMemory:     spec.WorkingMemory, // Phase 6: 工作记忆注入
		AgentBus:          o.agentBus,         // Phase 5: 多Agent通信
		CheckpointManager: o.checkpointMgr,    // Phase 5: 崩溃恢复
		WorkspaceDir:      workspaceDir,       // Session-level workspace directory
		OnLLMUsage:        onUsage,
		// Phase 6 Router: 透传共享的 Router/Registry/Providers，使子 agent
		// 参与动态 model 选择。modelRouter 为 nil 时 Engine 退回到单 model
		// 路径（legacy 行为）。
		Router:    o.modelRouter,
		Registry:  o.routerRegistry,
		Providers: o.routerProviders,
		// 7-G: 子 agent 拥有各自的 SubTaskID，这样事件和 snapshot 会按
		// 每个 agent 执行实例相互隔离。
		SubTaskID: subTaskID,
		// Phase 7-H2: 把 tracer/rootTraceCtx 透传给子 agent engine，使其 span
		// 能挂到 orchestration root 下。
		Tracer:       o.tracer,
		RootTraceCtx: o.rootTraceCtx,
		// Phase 7-H: 子 agent 是 worker，禁止再派发和自定义工作流。
		Role:                 runtime.AgentRoleWorker,
		CanDispatchSubAgents: false,
		CanDefineWorkflow:    false,
		SupervisorSubTaskID:  rootTaskID,
		ApproverMode:         "leader",
	}, o.tools, &hubAdapter{hub: o.hub}, subTaskID)

	// Phase 7-A: 注意 —— 每个 agent 的 Engine/cancel 注册被刻意保留在
	// cmd/server 层（由调用方创建 context 并持有 registry 表）。Orchestrator
	// 与那些全局 sync.Map registry 保持解耦，这样在无需 main.go 包级状态的
	// 情况下也能做单元测试。

	// 发出 agent_started 事件，便于 orchestrator 追踪
	o.hub.SendEvent(event.NewEvent("agent_ready", subTaskID, spec.AgentID, 0, map[string]any{
		"agent_name":    spec.Name,
		"model":         model,
		"max_steps":     contract.MaxSteps,
		"parent_agent":  spec.ParentAgentID,
		"allowed_tools": spec.AllowedTools,
	}))

	// 为该 agent 持久化任务创建：先创建子任务行，再写 meta，使
	// parent_task_id 指回根任务。这是让 QueryChildTasks(rootTaskID) 能
	// 返回该子任务的关键。
	if o.persist != nil {
		if err := o.persist.SaveTask(subTaskID, spec.Input, []string{spec.AgentID}); err != nil {
			log.Printf("[Orchestrator] Failed to save child task %s: %v", subTaskID, err)
		} else if err := o.persist.SaveTaskMeta(subTaskID, sessionID, rootTaskID, false); err != nil {
			log.Printf("[Orchestrator] Failed to bind child task %s to root %s: %v", subTaskID, rootTaskID, err)
		}
	}

	// 运行 engine
	result, totalTokens, err := engine.Run(ctx, spec.Input)

	duration := time.Since(start).Milliseconds()

	if err != nil {
		log.Printf("[Orchestrator] Agent %s (%s) failed: %v", spec.AgentID, spec.Name, err)
		o.hub.SendEvent(event.NewEvent("task_failed", subTaskID, spec.AgentID, 0, map[string]any{
			"reason":       err.Error(),
			"agent_name":   spec.Name,
			"total_tokens": totalTokens,
			"duration_ms":  duration,
		}))
		if o.persist != nil {
			if uerr := o.persist.UpdateTask(subTaskID, "failed", result, totalTokens); uerr != nil {
				log.Printf("[Orchestrator] Failed to update child task %s status: %v", subTaskID, uerr)
			}
		}
		return AgentResult{
			AgentID:     spec.AgentID,
			Name:        spec.Name,
			Status:      "failed",
			Result:      result,
			TotalTokens: totalTokens,
			Error:       err.Error(),
			Duration:    duration,
		}
	}

	log.Printf("[Orchestrator] Agent %s (%s) completed: %d tokens, %dms",
		spec.AgentID, spec.Name, totalTokens, duration)

	// 持久化子任务的终态，这样对 subTaskID 调 QueryTaskByID 时能正确反映
	// completed/failed，便于回放和调试。
	if o.persist != nil {
		if err := o.persist.UpdateTask(subTaskID, "completed", result, totalTokens); err != nil {
			log.Printf("[Orchestrator] Failed to update child task %s status: %v", subTaskID, err)
		}
	}

	o.hub.SendEvent(event.NewEvent("task_completed", subTaskID, spec.AgentID, 0, map[string]any{
		"result":       result,
		"agent_name":   spec.Name,
		"total_tokens": totalTokens,
		"duration_ms":  duration,
	}))

	return AgentResult{
		AgentID:     spec.AgentID,
		Name:        spec.Name,
		Status:      "completed",
		Result:      result,
		TotalTokens: totalTokens,
		Duration:    duration,
	}
}

// hubAdapter 把 ws.Hub 适配为 runtime.EventBus interface。
// 所有 agent 共享同一个 adapter —— Hub 本身是 goroutine-safe。
type hubAdapter struct {
	hub *ws.Hub
}

func (a *hubAdapter) SendEvent(evt event.Event) {
	a.hub.SendEvent(evt)
}

// ============================================================================
// AgentBus — agent 间通信
// ============================================================================

// AgentMessage 是从一个 agent 发往另一个 agent 的消息。
// 它携带发送方身份、接收方身份、可选的 sub-task 目标（用于精确路由）、
// 消息内容以及可选的 metadata。
type AgentMessage struct {
	// FromAgentID 是发送该消息的 agent。
	FromAgentID string `json:"from_agent_id"`

	// ToAgentID 是目标 agent。为空时表示广播给所有 agent。
	ToAgentID string `json:"to_agent_id"`

	// SubTaskID 是目标 sub-task ID。Phase 7-I：设置后，消息会被路由到
	// 为精确 (ToAgentID, SubTaskID) 配对注册的 handler。
	// Phase 7-J：leader 注册时使用 rootTaskID，因此子 agent 给 leader 发
	// 消息时要把 ToAgentID 设为 "leader"、SubTaskID 设为 rootTaskID。
	SubTaskID string `json:"sub_task_id,omitempty"`

	// FromSubTaskID 是发送方的 sub-task ID。Phase 7-J：持久化用它记录
	// 是哪个 sub-task 发出的消息，这样前端时间线能画出准确的 agent 间箭头。
	FromSubTaskID string `json:"from_sub_task_id,omitempty"`

	// Type 描述消息类型："request"、"response"、"observation"、"error"
	Type string `json:"type"`

	// Content 是消息体。
	Content string `json:"content"`

	// Metadata 携带任意 key-value 上下文。
	Metadata map[string]string `json:"metadata,omitempty"`

	// Timestamp 是消息发送时间。
	Timestamp time.Time `json:"timestamp"`
}

// AgentBus 是 agent 间通信通道。
// agent 在执行过程中可以通过它互相发送消息。
// 该 bus 是 goroutine-safe 的，可在所有 agent 间共享。
type AgentBus struct {
	mu       sync.RWMutex
	handlers map[string]func(AgentMessage) // agentID → message handler
	// subTaskHandlers 把 "agentID\x1fsubTaskID" 映射到 handler。
	// Phase 7-I：支持按 (agentID, subTaskID) 精确路由；空 subTaskID 等价于 RegisterHandler。
	subTaskHandlers map[string]func(AgentMessage)
	queue           []AgentMessage // 尚未注册 handler 的 agent 的待处理消息
	maxQueue        int            // 最大待处理消息数

	// persistFn 是一个可选 hook，对每条流经 bus 的消息（无论已投递还是
	// 入队）都会异步调用。它让 orchestrator 可以把 AgentBus 流量持久化到
	// SQLite，而又不会让 runtime AgentBus 包耦合到 db 包。
	persistFn func(AgentMessage) error
}

// NewAgentBus 创建一个新的 AgentBus，默认队列大小为 100。
func NewAgentBus() *AgentBus {
	return &AgentBus{
		handlers:        make(map[string]func(AgentMessage)),
		subTaskHandlers: make(map[string]func(AgentMessage)),
		maxQueue:        100,
	}
}

// SetPersistFn 安装一个回调，在 SendMessage 给消息赋上 Timestamp 之后
// 调用。回调在独立的 goroutine 中运行，这样慢的持久化写入不会阻塞
// 消息投递。
//
// 传 nil 可禁用持久化（默认行为）。main.go 用它把 db.InsertAgentMessage
// 接入 bus。
func (b *AgentBus) SetPersistFn(fn func(AgentMessage) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.persistFn = fn
}

// RegisterHandler 为指定 agent 注册一个 message handler。
// 当消息地址指向该 agent 时，会调用此 handler。
func (b *AgentBus) RegisterHandler(agentID string, handler func(AgentMessage)) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.handlers[agentID] = handler

	// 投递该 agent 之前积压的待处理消息
	b.deliverPendingLocked(agentID, "", handler)
}

// RegisterHandlerBySubTask 注册一个 (agentID, subTaskID) 精确处理器。
// 当 subTaskID 为空时行为与 RegisterHandler 一致。Phase 7-I。
func (b *AgentBus) RegisterHandlerBySubTask(agentID, subTaskID string, handler func(AgentMessage)) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key := subTaskHandlerKey(agentID, subTaskID)
	if subTaskID == "" {
		b.handlers[agentID] = handler
	} else {
		b.subTaskHandlers[key] = handler
	}

	// 优先投递精确匹配的待处理消息，再处理仅 agentID 匹配的待处理消息。
	b.deliverPendingLocked(agentID, subTaskID, handler)
	if subTaskID != "" {
		b.deliverPendingLocked(agentID, "", handler)
	}
}

// UnregisterHandler 移除指定 agent 的 message handler。
func (b *AgentBus) UnregisterHandler(agentID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.handlers, agentID)
}

// UnregisterHandlerBySubTask 移除 (agentID, subTaskID) 处理器。
// subTaskID 为空时行为与 UnregisterHandler 一致。Phase 7-I。
func (b *AgentBus) UnregisterHandlerBySubTask(agentID, subTaskID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if subTaskID == "" {
		delete(b.handlers, agentID)
		return
	}
	delete(b.subTaskHandlers, subTaskHandlerKey(agentID, subTaskID))
}

// subTaskHandlerKey 构造 (agentID, subTaskID) handler 的 map key。
// 使用不可打印的 unit separator 避免与合法 agentID 冲突。
func subTaskHandlerKey(agentID, subTaskID string) string {
	return agentID + "\x1f" + subTaskID
}

// deliverPendingLocked 尝试把队列中匹配 target 的待处理消息投递给 handler。
// 必须在 b.mu 已加锁的情况下调用。
func (b *AgentBus) deliverPendingLocked(agentID, subTaskID string, handler func(AgentMessage)) {
	// 先精确匹配 (agentID, subTaskID)
	for i := 0; i < len(b.queue); {
		msg := b.queue[i]
		if matchesTarget(msg, agentID, subTaskID) {
			handler(msg)
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			continue
		}
		i++
	}
}

// matchesTarget 判断 msg 是否匹配给定的 (agentID, subTaskID)。
// 当 subTaskID 非空时要求完全一致；当 subTaskID 为空时只匹配未指定
// SubTaskID 的消息，避免精确消息被 agentID-only handler 误收。Phase 7-J。
func matchesTarget(msg AgentMessage, agentID, subTaskID string) bool {
	if msg.ToAgentID != agentID {
		return false
	}
	if subTaskID == "" {
		// agentID-only fallback：仅匹配没有指定 subTaskID 的消息。
		return msg.SubTaskID == ""
	}
	return msg.SubTaskID == subTaskID
}

// SendMessage 从一个 agent 向另一个 agent 发送消息。
// 如果目标 agent 已注册 handler，则立即调用该 handler。
// 否则消息会被入队，留待后续投递。
//
// Phase 7-I：SendMessage 先查找精确的 (ToAgentID, SubTaskID) handler，
// 再退回到 ToAgentID-only handler，因此 sub-task 专属路由优先级更高。
// SubTaskID 为空时只匹配 ToAgentID-only handler。
//
// Phase 7-J：队列中的消息也按同样的匹配规则重新投递，确保
// message.SubTaskID 改变后不会错误地进入旧的 agentID-only handler。
//
// 当通过 SetPersistFn 安装了持久化回调时，每条消息 —— 无论已投递还是
// 入队 —— 都会在单独的 goroutine 中交给该回调，使消息路由不会被
// 存储 I/O 阻塞。
func (b *AgentBus) SendMessage(msg AgentMessage) {
	msg.Timestamp = time.Now()

	// 异步触发持久化 hook。在锁下 snapshot 函数引用，避免与
	// SetPersistFn 竞争；hook 为 nil 时是 no-op。
	b.mu.RLock()
	persist := b.persistFn
	b.mu.RUnlock()
	if persist != nil {
		go func(m AgentMessage) {
			if err := persist(m); err != nil {
				log.Printf("[AgentBus] persist message failed: %v", err)
			}
		}(msg)
	}

	b.mu.RLock()
	var handler func(AgentMessage)
	if msg.SubTaskID != "" {
		handler = b.subTaskHandlers[subTaskHandlerKey(msg.ToAgentID, msg.SubTaskID)]
	}
	if handler == nil {
		handler = b.handlers[msg.ToAgentID]
	}
	b.mu.RUnlock()

	if handler != nil {
		// 立即投递
		handler(msg)
		return
	}

	// 入队等待后续投递
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.queue) >= b.maxQueue {
		// 丢弃最旧的消息，防止队列无限增长
		b.queue = b.queue[1:]
	}
	b.queue = append(b.queue, msg)
}

// ============================================================================
// TaskDecomposer — 把用户请求拆分为子任务
// ============================================================================

// TaskDecomposer 把复杂的用户请求拆分为多个 agent 的子任务。
// 这是一个简单的基于规则的实现 —— 未来版本可能使用 LLM 动态分解任务。
type TaskDecomposer struct{}

// DecomposeResult 保存分解后的任务规范。
type DecomposeResult struct {
	// Agents 是要运行的 agent spec 列表。保留用于与旧版 API 和 rule-based
	// 分解器向后兼容。
	Agents []AgentSpec

	// Strategy 描述 agent 之间的协调方式：
	//   "parallel" —— 所有 agent 独立运行
	//   "sequential" —— agent 依次运行，每个能看到上一个 agent 的输出
	//   "pipeline" —— agent 通过链式传递数据（A → B → C）
	//   "dag" —— 使用 Workflow 定义的 DAG 拓扑调度（Phase 7-H2 阶段 5）
	Strategy string

	// Workflow 是可选的 DAG 编排结构。当 Strategy == "dag" 或 Workflow 非
	// nil 时使用 DAG 调度，否则回退到 Strategy 的 legacy 行为。
	Workflow *AgentWorkflow `json:"workflow,omitempty"`
}

// Decompose 按 case 类型把用户请求拆分为 agent specs。
// 目前分解基于预设 case 定义。
// Phase 5+ 将改用基于 LLM 的分解。
func (td *TaskDecomposer) Decompose(input string, caseType string) (*DecomposeResult, error) {
	switch caseType {
	case "multi_agent":
		// 多 agent case：拆分为 researcher + writer + reviewer
		return &DecomposeResult{
			Strategy: "sequential",
			Agents: []AgentSpec{
				{
					AgentID: "agent_researcher",
					Name:    "Researcher",
					SystemPrompt: "You are a research agent. Your job is to gather information, " +
						"analyze facts, and provide a structured research summary. " +
						"Use the available tools (read_file and run_shell) to gather data. " +
						"If you need external information not available from local files or shell commands, " +
						"state the limitation and answer based on your training knowledge. " +
						"Output your findings as a clear, structured report.",
					Input:    "Research the following topic: " + input + ". Provide a structured summary of findings.",
					OutputTo: []string{"agent_writer"},
				},
				{
					AgentID: "agent_writer",
					Name:    "Writer",
					SystemPrompt: "You are a technical writer. Your job is to take research findings " +
						"and produce a well-structured, clear, and engaging document. " +
						"Use write_file to save your output.",
					Input:         "Based on the provided research, write a comprehensive report.",
					ParentAgentID: "agent_researcher",
				},
			},
		}, nil

	case "code_gen":
		// 代码生成：单 agent + code-gen tool
		return &DecomposeResult{
			Strategy: "parallel",
			Agents: []AgentSpec{
				{
					AgentID: "agent_coder",
					Name:    "Code Generator",
					SystemPrompt: "You are a code generation agent. Write clean, well-documented code. " +
						"Always include tests and explanations.",
					Input:        input,
					AllowedTools: []string{"write_file", "read_file", "run_shell"},
				},
			},
		}, nil

	default:
		// 默认：单 agent
		return &DecomposeResult{
			Strategy: "parallel",
			Agents: []AgentSpec{
				{
					AgentID: "agent_default",
					Name:    "Default Agent",
					SystemPrompt: "You are a helpful AI assistant with access to tools. " +
						"When you need to run commands, read files, or write files, use the available tools.",
					Input: input,
				},
			},
		}, nil
	}
}

// ============================================================================
// 多 agent 预设 case
// ============================================================================

// MultiAgentSpecs 返回预定义的多 agent 任务规范。
// /api/cases endpoint 和前端的 case 卡片会用到它们。
func MultiAgentSpecs() []AgentSpec {
	return []AgentSpec{
		{
			AgentID: "agent_researcher",
			Name:    "Research Agent",
			SystemPrompt: "You are a research agent. Gather information, analyze facts, and " +
				"provide structured summaries. Be thorough and cite sources.",
			AllowedTools: []string{"read_file", "write_file", "run_shell"},
		},
		{
			AgentID: "agent_coder",
			Name:    "Code Agent",
			SystemPrompt: "You are a code generation agent. Write clean, well-tested code. " +
				"Always include error handling and documentation.",
			AllowedTools: []string{"write_file", "read_file", "run_shell"},
		},
		{
			AgentID: "agent_reviewer",
			Name:    "Review Agent",
			SystemPrompt: "You are a code review agent. Review code for correctness, " +
				"security, performance, and style. Provide constructive feedback.",
			AllowedTools: []string{"read_file", "write_file", "run_shell"},
		},
	}
}

// AgentColors 把 agent 角色映射到前端展示颜色。
// 这些颜色用于在多树视图中区分不同 agent。
var AgentColors = map[string]string{
	"agent_default":    "#4a9eff", // blue
	"agent_researcher": "#51cf66", // green
	"agent_coder":      "#f0a030", // orange
	"agent_writer":     "#9b59b6", // purple
	"agent_reviewer":   "#e74c3c", // red
}

// AgentColor 返回某个 agent 的展示颜色，找不到时返回默认灰色。
func AgentColor(agentID string) string {
	if c, ok := AgentColors[agentID]; ok {
		return c
	}
	return "#888888"
}
