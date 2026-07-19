// Package harness 实现 Harness Engineering 层 —— 包裹 Agent 的 ReAct loop 的确定性
// 脚手架，用于强制执行安全、范围、预算与进度跟踪。
//
// # Harness Engineering 设计哲学
//
// Harness 是围绕 Agent 的结构化控制层。Agent 的 LLM 是非确定性的、由 prompt 驱动的，
// 而 Harness 是确定性的 Go 代码，强制执行硬性约束。Harness 不依赖 LLM "自觉" —— 它在
// tool 执行前拦截 tool call，并可以拒绝调用。
//
// # 架构（6 层模型，已针对本项目调整）
//
//   L0: Model      — LLM provider（internal/llm）
//   L1: Interface  — ReAct Loop 引擎（internal/runtime）
//   L2: Tool       — Tool 注册表与执行（internal/tool）
//   L3: Harness    — PolicyGate + TaskContract + Progress（本 package）
//   L4: Memory     — 自演化记忆（Phase 4+）
//   L5: Governance — 审计 + eval + 成本控制（Phase 6+）
//
// # 关键概念
//
// TaskContract：一个结构化、机器可读的任务定义，描述任务是什么、允许做什么、什么算成功。
// 该 contract 由 PolicyGate 强制执行，而非依赖 LLM 的 system prompt。
//
// PolicyGate：一条由多个 PolicyRule 组成的链，在 tool 执行前拦截 tool call。
// 每条 rule 可以：
//   - Allow：允许 tool call 继续执行
//   - Block：拒绝 tool call 并附上原因（作为 error 返回给 LLM）
//   - Modify：改写 tool call 的参数（例如路径规范化）
//
// TaskProgress：外部化状态跟踪。progress 文件在关键节点写入，以便：
//   - 任务在崩溃后可恢复（checkpoint recovery）
//   - 用户无需阅读完整对话即可看到任务进度
//   - context window 不是任务状态的唯一真相来源
//
// # 四问法边界判定（Four-Question Boundary Test）
//
//   T1. Runtime Loop：谁调用模型？        → Engine（internal/runtime）
//   T2. Environmental Action：谁调用 tool？→ Engine 通过 Tool Registry
//   T3. Task-Aware Context：谁管理范围？   → Harness（TaskContract）
//   T4. Independent Control：谁强制执行规则？→ Harness（PolicyGate）
//
// # 与 Engine 的集成
//
// Harness 包裹 Tool Registry。当 Engine 调用 tool.Execute() 时，会先经过 Harness：
//
//   Engine.ExecuteTool() → Harness.PolicyGate.Check() → Tool.Execute()
//
// 如果 PolicyGate 拦截，Engine 会收到一个 ErrBlockedByPolicy 并发射 tool_call_failed
// 事件。LLM 看到该 error 后可以尝试其他方案。
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ============================================================================
// TaskContract —— 由 Harness 强制执行的结构化任务定义
// ============================================================================

// TaskContract 定义任务的范围、约束与成功标准。它是用户与 agent 之间机器可读的
// contract —— contract 中的每一项约束都由 PolicyGate 强制执行，而非依赖 prompt。
//
// 设计理由：没有 TaskContract 时，对 agent 的唯一约束只存在于 system prompt 中，
// 而 LLM 可能忽略或"忘记"这些约束（尤其当 context 增长时）。TaskContract 由确定性
// 的 Go 代码强制执行 —— LLM 无法绕过它。
type TaskContract struct {
	// Goal 是任务应完成什么的人类可读描述。用于进度跟踪与 summary 生成，不用于强制执行。
	Goal string `json:"goal"`

	// Scope 定义文件操作的工作目录。所有文件路径都相对此目录解析。Scope 之外的路径
	// 会被 FileScopeRule 拒绝。
	Scope string `json:"scope"`

	// AllowedTools 是允许 agent 使用的 tool 名称列表。为空时允许所有已注册的 tool；
	// 非空时只允许调用这些 tool（由 ToolWhitelistRule —— Phase 4 —— 强制执行）。
	AllowedTools []string `json:"allowed_tools,omitempty"`

	// TokenBudget 是 agent 在所有 LLM 调用中可消耗的最大 token 总数。当累计 token
	// 数超过此预算时，PolicyGate 拦截后续 LLM 调用。0 表示无限制。
	// 由 TokenBudgetRule（Phase 4）强制执行。
	TokenBudget int `json:"token_budget,omitempty"`

	// MaxSteps 是 ReAct loop 的最大迭代次数。超过时 Engine 以 max_steps_exceeded 终止。
	// 此约束由 Engine 自身强制执行，而非 PolicyGate。
	MaxSteps int `json:"max_steps"`

	// AcceptanceCriteria 定义什么算任务成功完成。可组合多个标准，全部通过才视为完成。
	// 为空时，任何 LLM final answer 都被接受。
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria,omitempty"`

	// Permissions 定义 agent 被允许做什么（例如网络访问、文件删除）。由 PolicyGate
	// 强制执行。
	Permissions TaskPermissions `json:"permissions"`

	// AutoApprovePolicy 为 true 时，自动批准所有策略检测到的危险命令，无需用户交互。
	// 审批事件仍会发射并记录，以便审计追踪。
	// 为 false（默认）时，策略拦截进入 WAITING 状态等待用户审批。
	AutoApprovePolicy bool `json:"auto_approve_policy,omitempty"`

	// Metadata 携带 harness 的任意键值对（例如 case 名、期望输出、tags）。不用于强制执行。
	Metadata map[string]string `json:"metadata,omitempty"`

	// CostBudgetUSD 是本任务允许的最大 USD 成本。当累计成本超过此预算时，
	// CostBudgetRule 拦截后续 tool call。0 表示无限制（无成本约束）。
	CostBudgetUSD float64 `json:"cost_budget_usd,omitempty"`

	// TimeoutSeconds 是本任务的最大运行时间。达到超时后 Engine 以 reason "task_timeout"
	// 中止。0 表示无限制（无超时）。调用者在创建执行 context 前将此值转换为
	// time.Duration。
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// TaskPermissions 定义 agent 的操作权限。所有字段默认为 false —— agent 初始无任何
// 权限，每一项都必须显式授予。
type TaskPermissions struct {
	// AllowNetwork 允许 agent 发起 HTTP 请求（未来 tool）。
	AllowNetwork bool `json:"allow_network"`

	// AllowFileDelete 允许 agent 删除文件（未来 tool）。
	AllowFileDelete bool `json:"allow_file_delete"`

	// AllowFileWrite 允许 agent 写入/创建文件。
	AllowFileWrite bool `json:"allow_file_write"`

	// AllowShell 允许 agent 执行 shell 命令。
	AllowShell bool `json:"allow_shell"`

	// AllowShellDangerous 允许危险 shell 命令（例如 rm -rf、git push --force）。
	// 即使开启此项，DangerousCommandRule（Phase 5）仍可能要求前端审批。
	AllowShellDangerous bool `json:"allow_shell_dangerous"`
}

// DefaultContract 返回一个适合简单任务的宽松 TaskContract。它允许常见的读写操作，
// 但默认不允许破坏性操作、网络访问或 shell 执行。调用者在创建 contract 时必须显式
// 授予那些权限。
func DefaultContract(goal string) TaskContract {
	return TaskContract{
		Goal:           goal,
		Scope:          ".",
		MaxSteps:       30,
		TimeoutSeconds: 0,
		Permissions: TaskPermissions{
			AllowFileWrite: true,
		},
	}
}

// ============================================================================
// TaskProgress —— 外部化状态跟踪
// ============================================================================

// TaskProgress 在关键节点跟踪任务进度。它被写入任务工作目录下的 progress 文件，
// 提供外部化状态，可穿越 context-window 重置与进程崩溃。
//
// 为什么要外部化？LLM 的 context window 是"已完成什么"的主要"记忆"，但它很脆弱 ——
// context 可能被截断、模型可能幻觉、对话历史不可校验。Progress 文件是 agent 实际
// 完成了什么的可校验记录。
type TaskProgress struct {
	// TaskID 是任务的唯一标识符
	TaskID string `json:"task_id"`

	// Goal 是任务的目标（来自 TaskContract）
	Goal string `json:"goal"`

	// Status 是任务当前状态
	Status string `json:"status"` // "running", "completed", "failed"

	// CurrentStep 是当前 ReAct loop 迭代
	CurrentStep int `json:"current_step"`

	// TotalSteps 是允许的最大步数（来自 TaskContract）
	TotalSteps int `json:"total_steps"`

	// TotalTokens 是累计 token 使用量
	TotalTokens int `json:"total_tokens"`

	// Nodes 是任务执行中的关键里程碑。每个 node 记录该节点完成了什么。
	Nodes []ProgressNode `json:"nodes"`

	// StartedAt 是任务开始时间
	StartedAt time.Time `json:"started_at"`

	// UpdatedAt 是 progress 最后写入时间
	UpdatedAt time.Time `json:"updated_at"`
}

// ProgressNode 表示任务执行中的一个关键里程碑。Nodes 写入于有意义的节点：
// tool 调用结果、step 完成、遇到错误、任务完成。
type ProgressNode struct {
	// Step 是记录此 node 时的 ReAct loop 迭代
	Step int `json:"step"`

	// Type 描述 node 类型："tool_call"、"observation"、"milestone"、"error"、"complete"
	Type string `json:"type"`

	// Summary 是此节点发生了什么的人类可读描述
	Summary string `json:"summary"`

	// Data 携带类型相关的数据（例如 tool_call 的 tool 名称与结果）
	Data map[string]any `json:"data,omitempty"`

	// Timestamp 是此 node 记录时间
	Timestamp time.Time `json:"timestamp"`
}

// ProgressManager 负责读写 TaskProgress 文件。它将 JSON 文件写入任务的 scope 目录。
type ProgressManager struct {
	// ProgressPath 是 progress 文件路径（默认："task_progress.json"）
	ProgressPath string
}

// NewProgressManager 创建使用默认 progress 文件路径的 ProgressManager。
func NewProgressManager() *ProgressManager {
	return &ProgressManager{
		ProgressPath: "task_progress.json",
	}
}

// Init 为给定任务创建一个新的 TaskProgress 文件。
func (pm *ProgressManager) Init(taskID string, contract TaskContract) (*TaskProgress, error) {
	progress := &TaskProgress{
		TaskID:     taskID,
		Goal:       contract.Goal,
		Status:     "running",
		TotalSteps: contract.MaxSteps,
		Nodes:      make([]ProgressNode, 0),
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	return progress, pm.Write(progress)
}

// AddNode 追加一个 progress node 并写入更新后的文件。
func (pm *ProgressManager) AddNode(progress *TaskProgress, node ProgressNode) error {
	node.Timestamp = time.Now()
	progress.Nodes = append(progress.Nodes, node)
	progress.CurrentStep = node.Step
	progress.UpdatedAt = time.Now()
	return pm.Write(progress)
}

// SetStatus 更新任务状态并写入文件。
func (pm *ProgressManager) SetStatus(progress *TaskProgress, status string, totalTokens int) error {
	progress.Status = status
	progress.TotalTokens = totalTokens
	progress.UpdatedAt = time.Now()
	return pm.Write(progress)
}

// Write 将 TaskProgress 序列化到 progress 文件。
func (pm *ProgressManager) Write(progress *TaskProgress) error {
	progress.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(progress, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal progress: %w", err)
	}
	// 写入任务专用路径：{scope}/task_progress.json
	dir := filepath.Dir(pm.ProgressPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create progress dir: %w", err)
		}
	}
	if err := os.WriteFile(pm.ProgressPath, data, 0644); err != nil {
		return fmt.Errorf("write progress: %w", err)
	}
	return nil
}

// Load 从磁盘读取 TaskProgress 文件。
func (pm *ProgressManager) Load() (*TaskProgress, error) {
	data, err := os.ReadFile(pm.ProgressPath)
	if err != nil {
		return nil, fmt.Errorf("read progress: %w", err)
	}
	var progress TaskProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, fmt.Errorf("unmarshal progress: %w", err)
	}
	return &progress, nil
}

// Exists 当 progress 文件在磁盘上存在时返回 true。
func (pm *ProgressManager) Exists() bool {
	_, err := os.Stat(pm.ProgressPath)
	return err == nil
}

// ============================================================================
// AcceptanceCriteria —— 定义什么算"成功"
// ============================================================================

// AcceptanceCriterionType 定义要执行的检查类型。
type AcceptanceCriterionType string

const (
	// AcceptTestPass 检查某条测试命令以退出码 0 退出。
	// 例如 "go test ./..." 或 "python -m pytest"
	AcceptTestPass AcceptanceCriterionType = "test_pass"

	// AcceptFileExists 检查给定路径的文件存在。
	// 例如 "output/report.md" 或 "src/main.go"
	AcceptFileExists AcceptanceCriterionType = "file_exists"

	// AcceptShellExitZero 检查 shell 命令以退出码 0 退出。
	// 比 test_pass 更通用 —— 可以是任意命令。
	AcceptShellExitZero AcceptanceCriterionType = "shell_exit_zero"

	// AcceptContentContains 检查文件包含特定字符串。
	// 例如生成的报告包含 "Summary" 段落。
	AcceptContentContains AcceptanceCriterionType = "content_contains"

	// AcceptLLMJudge 让 LLM 按 rubric 评估 agent 的 final answer。criterion 的
	// Target 字段包含 rubric / 问题。
	AcceptLLMJudge AcceptanceCriterionType = "llm_judge"
)

// AcceptanceCriterion 定义单个任务完成检查。TaskContract 中可组合多个标准 —— 必须全部通过。
type AcceptanceCriterion struct {
	// Type 是要执行的检查类型
	Type AcceptanceCriterionType `json:"type"`

	// Target 是检查的目标（文件路径、命令或搜索字符串）
	Target string `json:"target"`

	// Expected 是期望值（对 content_contains：要查找的子串）
	Expected string `json:"expected,omitempty"`

	// Description 是此 criterion 检查什么的人类可读说明
	Description string `json:"description"`
}

// ============================================================================
// PolicyGate —— 强制执行层
// ============================================================================

// PolicyRule 是 PolicyGate 中的一条 rule。每条 rule 可在 tool 执行前检查 tool call
// 并决定允许、拦截或改写。
//
// PolicyRule 接口刻意保持最小 —— 只有一个 Check 方法。Rules 组合成 PolicyChain，
// 在每次 tool 执行前按顺序检查。
type PolicyRule interface {
	// Name 返回人类可读的 rule 名称，用于日志和错误信息。
	Name() string

	// Check 根据此 rule 评估 tool call。返回：
	//   - nil 表示 rule 允许此次调用（或此 rule 不适用于此 tool）
	//   - ErrBlockedByPolicy 表示 rule 拦截此次调用
	//   - 可选地返回改写后的 input（例如路径规范化）
	Check(toolName string, input map[string]any, contract TaskContract) (allowedInput map[string]any, err error)
}

// TokenAwareRule 扩展 PolicyRule，适用于需要感知当前 token 使用量的 rule。
// TokenBudgetRule 实现此接口以强制执行 token 预算。Engine 在每次 tool 执行前调用
// SetTokenUsage 更新累计 token 数。
type TokenAwareRule interface {
	PolicyRule
	// SetTokenUsage 更新 rule 对累计 token 使用量的感知。
	SetTokenUsage(totalTokens int)
}

// ErrBlockedByPolicy 在 PolicyRule 拦截 tool call 时返回。LLM 收到此 error 作为
// tool 的输出，并可以尝试其他方案（例如写入其他目录）。
type ErrBlockedByPolicy struct {
	Rule    string // 拦截此次调用的 rule
	Reason  string // 拦截原因的人类可读说明
	Tool    string // 被拦截的 tool
}

// Error 实现 error 接口。
func (e *ErrBlockedByPolicy) Error() string {
	return fmt.Sprintf("[POLICY BLOCK] %s blocked %s: %s", e.Rule, e.Tool, e.Reason)
}

// PolicyChain 是 PolicyRule 的有序列表。Rules 按顺序检查；第一条拦截的 rule 停止链。
// 如果某条 rule 改写了 input，改写后的 input 会传递给下一条 rule。
type PolicyChain struct {
	rules []PolicyRule
}

// NewPolicyChain 用给定的 rules 创建 PolicyChain。
func NewPolicyChain(rules ...PolicyRule) *PolicyChain {
	return &PolicyChain{rules: rules}
}

// AddRule 向链追加一条 rule。
func (pc *PolicyChain) AddRule(rule PolicyRule) {
	pc.rules = append(pc.rules, rule)
}

// Check 对 tool call 按顺序运行所有 rule。若所有 rule 通过，返回（可能被改写的）
// input 和 nil；若任一 rule 拦截，返回原始 input 和 ErrBlockedByPolicy。
func (pc *PolicyChain) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	currentInput := input
	for _, rule := range pc.rules {
		allowedInput, err := rule.Check(toolName, currentInput, contract)
		if err != nil {
			return input, err // 拦截时返回原始 input
		}
		currentInput = allowedInput
	}
	return currentInput, nil
}

// PolicyGate 用策略强制执行包裹一次 tool 执行。它是 Harness 与 Engine 之间的
// 主要集成点。
//
// 用法：
//
//	gate := harness.NewPolicyGate(chain, contract)
//	result, err := gate.Execute(toolName, input, func(input map[string]any) (any, error) {
//	    return registry.Execute(toolName, input)
//	})
type PolicyGate struct {
	chain    *PolicyChain
	contract TaskContract
}

// NewPolicyGate 用给定的策略链与 contract 创建 PolicyGate。若 chain 为 nil，则
// 所有 tool call 都被允许（无策略强制执行）。
func NewPolicyGate(chain *PolicyChain, contract TaskContract) *PolicyGate {
	if chain == nil {
		chain = NewPolicyChain()
	}
	return &PolicyGate{
		chain:    chain,
		contract: contract,
	}
}

// Execute 在策略链上运行 tool call，若所有 rule 通过则执行 tool。executor 回调是
// 实际的 tool 执行逻辑（通常是 registry.Execute）。
func (g *PolicyGate) Execute(toolName string, input map[string]any, executor func(map[string]any) (any, error)) (any, error) {
	// 在执行前检查策略链
	allowedInput, err := g.chain.Check(toolName, input, g.contract)
	if err != nil {
		return nil, err
	}

	// 用（可能被改写的）input 执行 tool
	return executor(allowedInput)
}

// Contract 返回当前的 TaskContract（只读）。
func (g *PolicyGate) Contract() TaskContract {
	return g.contract
}

// SetTokenUsage 用当前累计 token 数更新链中所有 TokenAwareRule。由 Engine 在每次
// tool 执行前调用。
func (g *PolicyGate) SetTokenUsage(totalTokens int) {
	for _, rule := range g.chain.rules {
		if ta, ok := rule.(TokenAwareRule); ok {
			ta.SetTokenUsage(totalTokens)
		}
	}
}

// ============================================================================
// 辅助：将 tool 执行结果包装为 JSON 字符串
// ============================================================================

// ToJSONString 将一个值序列化为 JSON 字符串供 LLM 使用。若序列化失败，则将 error
// 信息作为字符串返回。
func ToJSONString(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error": "failed to serialize result: %s"}`, err.Error())
	}
	return string(data)
}

// ============================================================================
// FileScopeRule —— 将文件操作限制在 contract 的 scope 内
// ============================================================================

// FileScopeRule 确保所有文件操作（read_file、write_file）都在 TaskContract 的 Scope
// 目录内。Scope 之外的路径会被 ErrBlockedByPolicy 拒绝。
//
// 此 rule 会将路径相对 scope 目录进行规范化，因此 LLM 可以使用相对路径，rule 会正确解析。
type FileScopeRule struct{}

// Name 返回 rule 名称。
func (r *FileScopeRule) Name() string { return "FileScopeRule" }

// isUnixAbsolutePath 报告 p 是否为 Unix 风格绝对路径（以 '/' 开头）。
// 在 Windows 上，filepath.IsAbs 只识别盘符根路径（C:\、\server\share），对 "/etc/passwd"
// 返回 false，从而被当作相对路径并入 scope 目录 —— 这是一个跨平台安全漏洞。通过显式
// 检测 Unix 绝对路径，我们确保在所有平台上都将其视为绝对路径，使下面的 scope 前缀检查
// 在 scope 为 Windows 路径时拒绝它（仅当 scope 为匹配的 Unix 路径时才接受）。
func isUnixAbsolutePath(p string) bool {
	return strings.HasPrefix(p, "/")
}

// Check 校验文件路径是否在 contract 的 scope 内。
// 若路径是绝对路径（Windows 或 Unix 风格），检查它是否在 scope 之下。
// 若路径是相对路径，相对 scope 解析。
// 返回的 input 包含规范化后的绝对路径。
//
// Scope 解析优先级（从高到低）：
//  1. contract.Scope —— 调用方显式提供的 scope（期望是绝对路径）
//  2. input["workdir"] —— Engine 在 LLM 未显式传入 workdir 时注入的 session
//     workspace_dir（engine.go:1330）
//  3. os.Getwd() —— 兜底路径，确保 rule 在 scope 与 workdir 都未设置时仍有可用根
//
// 若没有 workdir 兜底，默认 contract（Scope="."）会解析到服务器 CWD，于是用相对路径
// "foo.txt" 的 write_file 会落到服务器 CWD 而非 session workspace —— 这正是我们要修复
// 的 bug。当 Engine 将 session workspace 作为 "workdir" 注入时，我们把 scope 锚定到那里。
func (r *FileScopeRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	// 只对文件相关 tool 生效
	if toolName != "write_file" && toolName != "read_file" {
		return input, nil
	}

	path, ok := input["path"].(string)
	if !ok || path == "" {
		return input, nil // 无 path 可检查；让 tool 自行处理 error
	}

	// 确定有效的 scope 根。contract.Scope 优先；当它是宽松默认值 "."（未显式设置 scope）
	// 时，回退到 tool input 中传入的 session workspace_dir，使相对路径解析到 session
	// workspace 内而非服务器 CWD。
	scope := contract.Scope
	if scope == "" || scope == "." {
		if wd, hasWd := input["workdir"].(string); hasWd && wd != "" {
			scope = wd
		}
	}

	// 将 scope 解析为绝对路径
	scopeAbs, err := filepath.Abs(scope)
	if err != nil {
		return input, &ErrBlockedByPolicy{
			Rule:   "FileScopeRule",
			Reason: fmt.Sprintf("resolve scope: %v", err),
			Tool:   toolName,
		}
	}

	// 解析请求路径。我们将 Windows 绝对路径（filepath.IsAbs，如 "C:\..."）与 Unix 绝对
	// 路径（以 '/' 开头，如 "/etc/passwd"）都视为绝对路径。在 Windows 上，Unix 绝对路径
	// 不可能位于 Windows scope 之内，因此下面的前缀检查会拒绝它，而不是默默地把它并入
	// scope 目录。
	var targetAbs string
	if filepath.IsAbs(path) || isUnixAbsolutePath(path) {
		targetAbs = filepath.Clean(path)
	} else {
		targetAbs = filepath.Join(scopeAbs, filepath.Clean(path))
	}

	// 检查目标是否在 scope 内。我们用 scopeAbs + 分隔符进行比较，以避免 scope "/foo"
	// 误匹配 "/foobar"。
	if !strings.HasPrefix(targetAbs, scopeAbs+string(filepath.Separator)) && targetAbs != scopeAbs {
		return input, &ErrBlockedByPolicy{
			Rule:   r.Name(),
			Reason: fmt.Sprintf("path %q is outside the allowed scope %q", path, contract.Scope),
			Tool:   toolName,
		}
	}

	// 将 input 中的路径规范化为绝对路径
	normalizedInput := make(map[string]any, len(input))
	for k, v := range input {
		normalizedInput[k] = v
	}
	normalizedInput["path"] = targetAbs

	return normalizedInput, nil
}

// ============================================================================
// PathTraversalRule —— 拦截路径中的 ".."
// ============================================================================

// PathTraversalRule 拒绝包含 ".." 段的文件路径 —— 这是最常见的目录穿越（directory
// traversal）攻击形式。此 rule 与 FileScopeRule 配合，作为纵深防御措施。
type PathTraversalRule struct{}

// Name 返回 rule 名称。
func (r *PathTraversalRule) Name() string { return "PathTraversalRule" }

// Check 拒绝包含 ".." 段的路径。这是一个简单、快速的检查，能捕获最显眼的穿越尝试。
// FileScopeRule 提供更全面的 scope 检查。
func (r *PathTraversalRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	// 只对文件相关 tool 与 shell 命令生效
	if toolName != "write_file" && toolName != "read_file" && toolName != "run_shell" {
		return input, nil
	}

	// 检查文件 tool 的 "path" 参数
	if path, ok := input["path"].(string); ok && path != "" {
		if strings.Contains(path, "..") {
			return input, &ErrBlockedByPolicy{
				Rule:   r.Name(),
				Reason: fmt.Sprintf("path contains '..' traversal: %q", path),
				Tool:   toolName,
			}
		}
	}

	// 对 run_shell，快速扫描命令字符串中形如路径穿越的 .. 模式
	// （不全面，但能捕获显眼情况）
	if toolName == "run_shell" {
		if cmd, ok := input["command"].(string); ok && cmd != "" {
			// 查找命令中 "../" 或 "..\" 模式
			if strings.Contains(cmd, "../") || strings.Contains(cmd, `..\`) {
				return input, &ErrBlockedByPolicy{
					Rule:   r.Name(),
					Reason: fmt.Sprintf("shell command contains path traversal pattern: %q", cmd),
					Tool:   toolName,
				}
			}
		}
	}

	return input, nil
}

// ============================================================================
// AcceptanceEvaluator —— 评估 acceptance criteria
// ============================================================================

// AcceptanceEvaluator 检查任务的 acceptance criteria 是否满足。它运行每条 criterion
// 的检查并返回一份报告，说明哪些通过、哪些失败。
type AcceptanceEvaluator struct {
	scope  string     // 解析相对路径的工作目录
	judge  *LLMJudge  // 可选的 LLM judge，用于语义标准（nil = soft pass）

	// 由 SetEvaluationContext 填入的评估 context。它将原始任务 goal、用户输入、
	// agent final answer 与近期 tool 输出传递给 LLM judge，使 prompt 包含真实证据
	// 而非空占位符。
	goal        string
	userInput   string
	finalAnswer string
	toolOutputs []string
}

// NewAcceptanceEvaluator 创建用给定 scope 的 AcceptanceEvaluator。
func NewAcceptanceEvaluator(scope string) *AcceptanceEvaluator {
	return &AcceptanceEvaluator{scope: scope}
}

// SetLLMJudge 附加可选的 LLM judge，用于评估 AcceptLLMJudge 标准。传 nil 会清除之前
// 附加的 judge。
// 当未配置 LLM judge 时，AcceptLLMJudge 标准的检查会 soft pass，避免阻塞未配置评估器的
// 旧任务或配置错误的引擎，同时仍会记录该标准因 judge 不可用而被跳过。
func (ae *AcceptanceEvaluator) SetLLMJudge(judge *LLMJudge) {
	ae.judge = judge
}

// SetEvaluationContext 向 AcceptanceEvaluator 提供 LLMJudge 标准所需的运行时信息。
// judge prompt 在评估语义 rubric 时会包含 Goal、UserInput、FinalAnswer 与近期的
// ToolOutputs。
func (ae *AcceptanceEvaluator) SetEvaluationContext(goal, userInput, finalAnswer string, toolOutputs []string) {
	ae.goal = goal
	ae.userInput = userInput
	ae.finalAnswer = finalAnswer
	ae.toolOutputs = toolOutputs
}

// EvalResult 是评估单个 acceptance criterion 的结果。
type EvalResult struct {
	Criterion AcceptanceCriterion `json:"criterion"`
	Passed    bool                `json:"passed"`
	Message   string              `json:"message"`
	Score     float64             `json:"score,omitempty"`
	Duration  int64               `json:"duration_ms"`
}

// EvalReport 是评估所有 acceptance criteria 的结果。
type EvalReport struct {
	AllPassed bool         `json:"all_passed"`
	Results   []EvalResult `json:"results"`
	Summary   string       `json:"summary"`
}

// Evaluate 运行所有 acceptance criteria 并返回一份报告。
func (ae *AcceptanceEvaluator) Evaluate(criteria []AcceptanceCriterion) (*EvalReport, error) {
	if len(criteria) == 0 {
		return &EvalReport{
			AllPassed: true,
			Results:   []EvalResult{},
			Summary:   "No acceptance criteria defined — any LLM output is accepted.",
		}, nil
	}

	results := make([]EvalResult, 0, len(criteria))
	allPassed := true

	for _, criterion := range criteria {
		result := ae.evaluateOne(criterion)
		results = append(results, result)
		if !result.Passed {
			allPassed = false
		}
	}

	summary := "All acceptance criteria passed."
	if !allPassed {
		failed := 0
		for _, r := range results {
			if !r.Passed {
				failed++
			}
		}
		summary = fmt.Sprintf("%d/%d criteria passed, %d failed.", len(results)-failed, len(results), failed)
	}

	return &EvalReport{
		AllPassed: allPassed,
		Results:   results,
		Summary:   summary,
	}, nil
}

// evaluateOne 运行单个 acceptance criterion 检查。
func (ae *AcceptanceEvaluator) evaluateOne(criterion AcceptanceCriterion) EvalResult {
	start := time.Now()

	switch criterion.Type {
	case AcceptFileExists:
		return ae.checkFileExists(criterion, start)

	case AcceptContentContains:
		return ae.checkContentContains(criterion, start)

	case AcceptTestPass, AcceptShellExitZero:
		return ae.checkShell(criterion, start)

	case AcceptLLMJudge:
		return ae.checkLLMJudge(criterion, start)

	default:
		return EvalResult{
			Criterion: criterion,
			Passed:    false,
			Message: fmt.Sprintf("Unknown criterion type: %q. Valid types: file_exists, content_contains, test_pass, shell_exit_zero, llm_judge",
				criterion.Type),
			Duration: time.Since(start).Milliseconds(),
		}
	}
}

// checkFileExists 验证目标路径存在文件。
func (ae *AcceptanceEvaluator) checkFileExists(criterion AcceptanceCriterion, start time.Time) EvalResult {
	target := criterion.Target
	if !filepath.IsAbs(target) {
		target = filepath.Join(ae.scope, target)
	}

	_, err := os.Stat(target)
	if err == nil {
		return EvalResult{
			Criterion: criterion,
			Passed:    true,
			Message:   fmt.Sprintf("File exists: %s", criterion.Target),
			Duration:  time.Since(start).Milliseconds(),
		}
	}

	return EvalResult{
		Criterion: criterion,
		Passed:    false,
		Message:   fmt.Sprintf("File not found: %s (%v)", criterion.Target, err),
		Duration:  time.Since(start).Milliseconds(),
	}
}

// checkContentContains 验证文件包含期望的字符串。
func (ae *AcceptanceEvaluator) checkContentContains(criterion AcceptanceCriterion, start time.Time) EvalResult {
	target := criterion.Target
	if !filepath.IsAbs(target) {
		target = filepath.Join(ae.scope, target)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		return EvalResult{
			Criterion: criterion,
			Passed:    false,
			Message:   fmt.Sprintf("Cannot read file: %s (%v)", criterion.Target, err),
			Duration:  time.Since(start).Milliseconds(),
		}
	}

	if strings.Contains(string(data), criterion.Expected) {
		return EvalResult{
			Criterion: criterion,
			Passed:    true,
			Message:   fmt.Sprintf("Content found in %s: %q", criterion.Target, truncateForDisplay(criterion.Expected, 60)),
			Duration:  time.Since(start).Milliseconds(),
		}
	}

	return EvalResult{
		Criterion: criterion,
		Passed:    false,
		Message:   fmt.Sprintf("Content not found in %s: %q", criterion.Target, truncateForDisplay(criterion.Expected, 60)),
		Duration:  time.Since(start).Milliseconds(),
	}
}

// checkShell 运行 shell 命令并检查退出码。
// 注意：此处刻意限制 —— 使用较短超时且不允许任意命令。它面向测试 runner 与校验命令，
// 不用于通用 shell 执行。
func (ae *AcceptanceEvaluator) checkShell(criterion AcceptanceCriterion, start time.Time) EvalResult {
	// 出于安全考虑，初始实现中 shell 退出检查是 stub。
	// 完整实现需要 Docker sandbox（Phase 5）。
	// 目前返回"未实现"结果，但不会阻塞。
	return EvalResult{
		Criterion: criterion,
		Passed:    true, // soft pass —— 不因未实现的检查而阻塞
		Message:   fmt.Sprintf("Shell check skipped (not yet implemented): %s. Full implementation in Phase 5 with Docker sandbox.", criterion.Target),
		Duration:  time.Since(start).Milliseconds(),
	}
}

// checkLLMJudge 使用可选的 LLM judge 评估语义 rubric。若未配置 judge，该 criterion 收到
// soft pass，使未配置评估器的任务不被阻塞（同时仍记录 judge 不可用）。
func (ae *AcceptanceEvaluator) checkLLMJudge(criterion AcceptanceCriterion, start time.Time) EvalResult {
	if ae.judge == nil {
		return EvalResult{
			Criterion: criterion,
			Passed:    true,
			Message:   "LLM judge criterion skipped: no LLM judge configured",
			Duration:  time.Since(start).Milliseconds(),
		}
	}

	result, err := ae.judge.Evaluate(context.Background(), JudgeRequest{
		Goal:        ae.goal,
		Rubric:      criterion.Target,
		UserInput:   ae.userInput,
		FinalAnswer: ae.finalAnswer,
		ToolOutputs: ae.toolOutputs,
	})
	if err != nil {
		return EvalResult{
			Criterion: criterion,
			Passed:    false,
			Message:   fmt.Sprintf("LLM judge evaluation failed: %v", err),
			Duration:  time.Since(start).Milliseconds(),
		}
	}

	msg := fmt.Sprintf("LLM judge passed=%v score=%.2f reason=%s", result.Passed, result.Score, result.Reason)
	return EvalResult{
		Criterion: criterion,
		Passed:    result.Passed,
		Message:   msg,
		Score:     result.Score,
		Duration:  time.Since(start).Milliseconds(),
	}
}

// truncateForDisplay 将字符串截断到 maxLen 长度以便展示。
func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ============================================================================
// TokenBudgetRule —— 强制执行 TaskContract 的 token 预算
// ============================================================================

// TokenBudgetRule 在累计 token 使用量超过 TaskContract 的 TokenBudget 时拦截所有
// tool call。它实现 TokenAwareRule，以便 Engine 在每次 tool 执行前更新累计 token 数。
//
// 设计理由：token 预算是硬性经济约束 —— 一旦预算超限，agent 应停止消耗 token。
// 此 rule 在 tool 执行前（而非 LLM 调用期间）检查，因为 Engine 已按 LLM 调用跟踪
// token 使用量。rule 拦截后续 tool call 以防 agent 在预算超限后继续发起 LLM 调用。
//
// 当 TokenBudget 为 0 时，rule 永不拦截（无限制预算）。
type TokenBudgetRule struct {
	// totalTokens 跟踪所有 LLM 调用的累计 token 使用量。由 Engine 通过 SetTokenUsage 更新。
	totalTokens int
}

// Name 返回 rule 名称。
func (r *TokenBudgetRule) Name() string { return "TokenBudgetRule" }

// SetTokenUsage 更新累计 token 数。由 Engine（通过 PolicyGate.SetTokenUsage）在每次
// tool 执行前调用。
func (r *TokenBudgetRule) SetTokenUsage(totalTokens int) {
	r.totalTokens = totalTokens
}

// Check 当累计 token 使用量超过预算时拦截所有 tool call。当 TokenBudget 为 0
// （无限制）时，此 rule 总是允许。
func (r *TokenBudgetRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	if contract.TokenBudget <= 0 {
		return input, nil // 无限制预算
	}

	if r.totalTokens >= contract.TokenBudget {
		return input, &ErrBlockedByPolicy{
			Rule:   r.Name(),
			Reason: fmt.Sprintf("token budget exceeded: %d/%d tokens used", r.totalTokens, contract.TokenBudget),
			Tool:   toolName,
		}
	}

	return input, nil
}

// ============================================================================
// ToolWhitelistRule —— 限制 agent 可使用的 tool
// ============================================================================

// ToolWhitelistRule 只允许调用 TaskContract AllowedTools 字段中列出的 tool。若
// AllowedTools 为空，则允许所有 tool。
//
// 设计理由：TaskContract 指定 agent 被允许使用哪些 tool。此 rule 在 PolicyGate 层、
// tool 执行前强制执行该限制。LLM 仍可在其响应中请求被拦截的 tool，但 PolicyGate 会
// 拦截并返回 error —— LLM 随后可尝试其他方案。
type ToolWhitelistRule struct{}

// Name 返回 rule 名称。
func (r *ToolWhitelistRule) Name() string { return "ToolWhitelistRule" }

// Check 拦截调用 contract AllowedTools 列表外 tool 的 tool call。若 AllowedTools 为空，
// 允许所有 tool（无限制）。
func (r *ToolWhitelistRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	if len(contract.AllowedTools) == 0 {
		return input, nil // 无限制 —— 允许所有 tool
	}

	for _, allowed := range contract.AllowedTools {
		if toolName == allowed {
			return input, nil
		}
	}

	return input, &ErrBlockedByPolicy{
		Rule:   r.Name(),
		Reason: fmt.Sprintf("tool %q is not in the allowed tools list: %v", toolName, contract.AllowedTools),
		Tool:   toolName,
	}
}