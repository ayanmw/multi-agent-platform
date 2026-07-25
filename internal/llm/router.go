// Package llm —— Router，用于 intent 分类与 model 选择。
//
// # 设计理由
//
// Router 是决策组件，为给定任务选择最合适的 model。它采用两阶段策略：
//
//  1. 基于规则过滤（零成本、零延迟）：剔除不满足硬性要求
//     （上下文长度、必要能力）的 model。
//  2. Intent 分类（便宜 model，约 100 token，< $0.001）：将用户请求
//     归入某个类别，再选择合适的层级。
//
// 该设计让路由成本几乎可忽略，同时保证复杂任务拿到所需强 model，
// 简单任务用便宜 model。
//
// # Intent 类别
//
//	simple_chat       —— 简单问答、闲聊、信息查询、格式转换
//	code_generation   —— 代码编写、调试、重构、代码评审
//	code_execution    —— 运行 shell、测试、build、部署脚本
//	complex_reasoning —— 多步推理、数学、逻辑、架构设计
//	multi_step        —— 需要多次 tool call、多阶段执行
//	rag_retrieval     —— 需要检索记忆/文档再回答
//	web_search        —— 需要外部搜索/实时信息
//	safety_sensitive  —— 涉及权限、敏感数据、审批、安全决策
//
// # 用法
//
//	router := llm.NewRouter(registry, classifierProvider)
//	decision, err := router.Select(&llm.RouteRequest{
//	    UserInput:    "Write a function to sort a list",
//	    RequiredCaps: []llm.ModelCapability{llm.CapToolCalling},
//	})
//
// 完整设计参见 doc/chapters/10-multi-model-layered-design.html。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// IntentClassifierPrompt 是 Router 分类器使用的 system prompt。
// 分类器输出 JSON，包含主要意图、次要意图、置信度、可能需要的工具及估计步数。
const IntentClassifierPrompt = `You are a request classifier. Classify the user's request into exactly one primary category and any secondary categories.
Respond with ONLY a JSON object, no markdown, no explanation.

Categories:
- simple_chat: Simple Q&A, chitchat, information lookup, format conversion, greetings
- code_generation: Code writing, debugging, refactoring, code review, testing
- code_execution: Running shell commands, tests, build, deployment scripts
- complex_reasoning: Multi-step reasoning, math problems, logic analysis, architecture design, planning
- multi_step: Requires multiple tool calls, multi-stage execution, agent orchestration
- rag_retrieval: Needs to search/retrieve memories or documents before answering
- web_search: Needs external search or real-time information
- safety_sensitive: Involves permissions, sensitive data, approvals, security decisions

JSON schema:
{
  "primary_intent": "<category>",
  "secondary_intents": ["<category>"],
  "confidence": 0.92,
  "needs_tools": ["tool_name"],
  "estimated_steps": 3
}

Request: %s
JSON:`

// IntentClassification 是 Router 分类器返回的结构化结果。
type IntentClassification struct {
	// PrimaryIntent 是主要意图类别，决定目标 tier。
	PrimaryIntent string `json:"primary_intent"`

	// SecondaryIntents 是次要意图类别，用于微调路由决策。
	SecondaryIntents []string `json:"secondary_intents"`

	// Confidence 是分类置信度，范围 [0,1]。
	Confidence float64 `json:"confidence"`

	// NeedsTools 预测任务可能需要的 tool 名称列表。
	NeedsTools []string `json:"needs_tools"`

	// EstimatedSteps 估计完成任务需要的 ReAct 步数。
	EstimatedSteps int `json:"estimated_steps"`
}

// RouteRequest 是 Router Select 方法的输入。
// 它描述影响 model 选择的任务特征。
type RouteRequest struct {
	// UserInput 是用户原始请求文本。
	UserInput string

	// ContextLen 是估算的输入 token 数。用于过滤上下文窗口过小的 model。
	ContextLen int

	// RequiredCaps 列出所选 model 必须具备的能力。
	// 例如任务需要 tool calling 时，只考虑带 CapToolCalling 的 model。
	RequiredCaps []ModelCapability

	// BudgetUSD 是可选的成本上限。若设置，Router 只考虑
	// 估算成本在预算内的 model。
	BudgetUSD float64

	// LatencyReq 是可选的延迟要求。若设置，Router 倾向
	// AvgLatencyMs 低于该阈值的 model。
	LatencyReq time.Duration

	// PreferredTier 是可选的层级偏好。若设置，Router 倾向
	// 该层级的 model（但可能回退到相邻层级）。
	PreferredTier ModelTier

	// PreferredModel 是可选的具体模型名偏好。若设置且存在于 Registry，
	// 则直接命中该模型，跳过自动选择。
	PreferredModel string

	// AllowCheapFirst 表示是否允许先尝试更便宜层级的模型。
	// 当任务可接受质量略低时，先用低成本模型试跑可降低整体成本。
	AllowCheapFirst bool

	// AgentRole 表示发起请求的 agent 角色，例如 "leader" / "worker" / "validator"。
	// Router 可据此做角色到 tier 的 override（如 leader 强制 TierPremium）。
	AgentRole string
}

// RouteDecision 是 Router Select 方法的输出。
// 它描述选中了哪个 model 以及原因。
type RouteDecision struct {
	// Primary 是选中的 model profile。
	Primary *ModelProfile

	// Fallback 是 primary 失败时的备用 model。
	// 未配置 fallback 时可能为 nil。
	Fallback *ModelProfile

	// Intent 是分类得到的主要 intent 类别（向后兼容）。
	Intent string

	// IntentClass 是完整的分类结果，包含置信度、次要意图、工具预测与步数估计。
	IntentClass IntentClassification

	// Reason 是人类可读的路由决策说明。
	// 它会显示在前端，体现"白盒"透明度。
	Reason string

	// Tier 是选中的 model 层级。
	Tier ModelTier

	// Confidence 是分类置信度，复制自 IntentClass.Confidence。
	Confidence float64

	// NeedsTools 预测任务可能需要的 tool 列表，复制自 IntentClass.NeedsTools。
	NeedsTools []string

	// EstimatedSteps 估计完成任务的 ReAct 步数，复制自 IntentClass.EstimatedSteps。
	EstimatedSteps int

	// CheapFirstAttempt 表示当前是否是先用便宜模型试跑的决策。
	CheapFirstAttempt bool
}

// Router 为给定任务请求选择最合适的 model。
//
// Router 用一个便宜的分类 model（通常是 Haiku 或 DeepSeek Flash）
// 对用户请求分类，再选择合适的 model 层级。
// 先做基于规则的过滤，剔除不满足硬性要求的 model。
//
// # 线程安全
//
// Router 可安全并发使用 —— registry 是 goroutine 安全的，
// 每次 Select 调用相互独立。
type Router struct {
	registry    *ModelRegistry
	classifier  Provider       // 用于 intent 分类的便宜 model
	rateLimiter *RateLimiter   // 可选；非 nil 时按模型 RPM 过滤候选
	broadcaster EventBroadcaster // 可选；路由事件广播器
	taskID      string           // 事件 task_id
	agentID     string           // 事件 agent_id
}

// EventBroadcaster 是 Router 用来广播路由相关事件的最小接口。
type EventBroadcaster interface {
	SendEvent(eventType string, data map[string]any)
}

// NewRouter 以给定的 model registry 与分类器创建新的 Router。
//
// 分类器应是便宜快速的 model（例如 Haiku 或 DeepSeek Flash），
// 因为每个请求都会调用它。单次分类成本应 < $0.001，
// 以保持路由开销可忽略。
//
// rateLimiter 为可选；nil 时禁用按模型 RPM 限流过滤。
func NewRouter(registry *ModelRegistry, classifier Provider, rateLimiter *RateLimiter) *Router {
	return &Router{
		registry:    registry,
		classifier:  classifier,
		rateLimiter: rateLimiter,
	}
}

// SetBroadcaster 配置 Router 的事件广播器与身份标识，供外部注入。
func (r *Router) SetBroadcaster(b EventBroadcaster, taskID, agentID string) {
	r.broadcaster = b
	r.taskID = taskID
	r.agentID = agentID
}

// emit 在 broadcaster 存在时发送路由事件。
func (r *Router) emit(eventType string, data map[string]any) {
	if r.broadcaster == nil {
		return
	}
	r.broadcaster.SendEvent(eventType, data)
}

// Select 为给定请求选择最合适的 model。
//
// 选择流程：
//  1. 用便宜分类 model 对用户 intent 分类
//  2. 把 intent 映射到目标 model 层级
//  3. 按硬性要求过滤 model（上下文长度、能力）
//  4. 从目标层级中选择最佳匹配 model
//  5. 解析 fallback model
//
// 若分类器调用失败，则回退到基于规则的关键字分类，
// 即使分类器不可用系统仍可用。
func (r *Router) Select(ctx context.Context, req *RouteRequest) (*RouteDecision, error) {
	// Step 1：分类 intent（失败回退到基于规则）
	intentClass, err := r.classifyIntent(ctx, req.UserInput)
	if err != nil {
		// 分类器失败 —— 回退到基于关键字的分类
		intentClass = r.keywordClassify(req.UserInput)
	}

	// 广播 intent 分类完成事件，让前端 Inspector 看到分类结果（白盒）。
	r.emit("intent_classified", map[string]any{
		"primary_intent":    intentClass.PrimaryIntent,
		"secondary_intents": intentClass.SecondaryIntents,
		"confidence":        intentClass.Confidence,
		"needs_tools":       intentClass.NeedsTools,
		"estimated_steps":   intentClass.EstimatedSteps,
		"source":            "classifier",
	})

	// Step 2：把 intent 映射到目标层级，并考虑角色 override 与 PreferredTier
	targetTier := r.resolveTargetTier(req, intentClass)

	// Step 3：按硬性要求过滤候选
	candidates := r.filterCandidates(req, targetTier)

	// Step 4：选择最佳候选
	var primary *ModelProfile
	if len(candidates) > 0 {
		primary = candidates[0]
	} else {
		// 目标层级无候选 —— 任意层级都试一遍
		allModels := r.registry.List()
		for _, m := range allModels {
			if r.meetsRequirements(m, req) {
				primary = m
				break
			}
		}
	}

	if primary == nil {
		return nil, fmt.Errorf("no suitable model found for request")
	}

	// Step 5：解析 fallback
	fallback := r.registry.GetFallback(primary.Name)

	// Step 6：根据 AllowCheapFirst 决定是否降级试跑
	cheapFirst := false
	if req.AllowCheapFirst && targetTier > TierFree {
		if cheaper := r.pickCheaperModel(primary, req, targetTier-1); cheaper != nil {
			fallback = primary
			primary = cheaper
			targetTier = cheaper.Tier
			cheapFirst = true
		}
	}

	return &RouteDecision{
		Primary:           primary,
		Fallback:          fallback,
		Intent:            intentClass.PrimaryIntent,
		IntentClass:       intentClass,
		Reason:            r.buildReason(intentClass, primary, targetTier, cheapFirst),
		Tier:              targetTier,
		Confidence:        intentClass.Confidence,
		NeedsTools:        intentClass.NeedsTools,
		EstimatedSteps:    intentClass.EstimatedSteps,
		CheapFirstAttempt: cheapFirst,
	}, nil
}

// classifyIntent 用便宜分类 model 对用户请求分类。
// 返回结构化的 IntentClassification；分类器调用失败则返回 error。
func (r *Router) classifyIntent(_ context.Context, userInput string) (IntentClassification, error) {
	prompt := fmt.Sprintf(IntentClassifierPrompt, userInput)

	req := ChatRequest{
		Model:       "", // 使用分类器的默认 model
		Messages:    []Message{{Role: "user", Content: prompt}},
		Temperature: 0, // 确定性分类
		// 512 足以让 Flash 层级 model 输出完整 JSON 并收敛；
		// 在 Flash 层级 model 上单次仍 < $0.001，符合"路由成本可忽略"目标。
		MaxTokens: 512,
		Stream:    false,
	}

	resp, err := r.classifier.Chat(req)
	if err != nil {
		return IntentClassification{}, fmt.Errorf("classifier call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return IntentClassification{}, fmt.Errorf("classifier returned empty response")
	}

	// 归一化响应：Content 为空时回退到 Reasoning（部分推理 model 的 budget 耗尽行为）。
	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	if raw == "" {
		raw = strings.TrimSpace(resp.Choices[0].Message.Reasoning)
	}

	var cls IntentClassification
	if err := json.Unmarshal([]byte(raw), &cls); err != nil {
		return IntentClassification{}, fmt.Errorf("classifier returned invalid JSON: %w", err)
	}

	// 校验主意图类别；未知则视为无效，触发上层 fallback。
	if !r.isKnownIntent(cls.PrimaryIntent) {
		return IntentClassification{}, fmt.Errorf("classifier returned unknown primary_intent: %s", cls.PrimaryIntent)
	}

	cls.PrimaryIntent = strings.ToLower(strings.TrimSpace(cls.PrimaryIntent))
	for i := range cls.SecondaryIntents {
		cls.SecondaryIntents[i] = strings.ToLower(strings.TrimSpace(cls.SecondaryIntents[i]))
	}

	return cls, nil
}

// keywordClassify 是基于关键字匹配的回退分类方法。
// 在分类 model 不可用时（网络错误、限流等）使用。
// 返回结构化的 IntentClassification，主意图由关键字决定；次要意图与置信度
// 保持保守默认值，让 downstream 仍有统一的数据结构可用。
func (r *Router) keywordClassify(userInput string) IntentClassification {
	lower := strings.ToLower(userInput)

	// multi_step 指示词（最先检查，优先级最高）
	multiStepKeywords := []string{
		"multi-step", "multi step", "pipeline", "orchestrate",
		"first", "then", "after that", "finally",
		"multiple agents", "subtask", "decompose",
	}
	for _, kw := range multiStepKeywords {
		if strings.Contains(lower, kw) {
			return IntentClassification{PrimaryIntent: "multi_step", Confidence: 0.7, EstimatedSteps: 3}
		}
	}

	// safety_sensitive 指示词
	safetyKeywords := []string{
		"permission", "approve", "secret", "password", "credential", "token",
		"access control", "security", "confidential", "private key",
	}
	for _, kw := range safetyKeywords {
		if strings.Contains(lower, kw) {
			return IntentClassification{PrimaryIntent: "safety_sensitive", Confidence: 0.7, EstimatedSteps: 2}
		}
	}

	// web_search 指示词
	webKeywords := []string{
		"search", "look up", "latest", "current", "news", "weather",
		"real-time", "realtime", "what is the price", "who won",
	}
	for _, kw := range webKeywords {
		if strings.Contains(lower, kw) {
			return IntentClassification{PrimaryIntent: "web_search", Confidence: 0.7, EstimatedSteps: 2}
		}
	}

	// code_execution 指示词
	execKeywords := []string{
		"run shell", "execute", "run the test", "run tests", "run command",
		"build project", "deploy", "start the server", "npm test", "go test",
		"python ", "bash ", "sh ", "terminal",
	}
	for _, kw := range execKeywords {
		if strings.Contains(lower, kw) {
			return IntentClassification{PrimaryIntent: "code_execution", Confidence: 0.75, EstimatedSteps: 2}
		}
	}

	// rag_retrieval 指示词
	ragKeywords := []string{
		"from my documents", "from the knowledge base", "retrieve", "recall",
		"memory", "remember", "what did we discuss", "previous conversation",
	}
	for _, kw := range ragKeywords {
		if strings.Contains(lower, kw) {
			return IntentClassification{PrimaryIntent: "rag_retrieval", Confidence: 0.7, EstimatedSteps: 2}
		}
	}

	// code_generation 指示词
	codeKeywords := []string{
		"write code", "implement", "function", "class", "debug",
		"refactor", "test case", "unit test", "api endpoint",
		"algorithm", "data structure", "fix bug", "compile",
	}
	for _, kw := range codeKeywords {
		if strings.Contains(lower, kw) {
			return IntentClassification{PrimaryIntent: "code_generation", Confidence: 0.75, EstimatedSteps: 2}
		}
	}

	// complex_reasoning 指示词
	reasoningKeywords := []string{
		"analyze", "architecture", "design pattern", "explain why",
		"compare", "evaluate", "optimize", "trade-off", "tradeoff",
		"prove", "proof", "mathematical", "logic",
	}
	for _, kw := range reasoningKeywords {
		if strings.Contains(lower, kw) {
			return IntentClassification{PrimaryIntent: "complex_reasoning", Confidence: 0.75, EstimatedSteps: 3}
		}
	}

	// 默认：simple chat
	return IntentClassification{PrimaryIntent: "simple_chat", Confidence: 0.6, EstimatedSteps: 1}
}

// intentToTier 将 intent 类别映射到 model 层级。
//
// 映射理由：
//   - simple_chat / web_search → TierEfficient：琐碎任务与搜索+摘要，用低成本 model
//   - code_generation / code_execution / multi_step / rag_retrieval → TierStandard：需要可靠 tool calling 与代码质量
//   - complex_reasoning / safety_sensitive → TierPremium：需要深度推理与保守决策
func (r *Router) intentToTier(intent string) ModelTier {
	switch intent {
	case "simple_chat", "web_search":
		return TierEfficient
	case "code_generation", "code_execution", "multi_step", "rag_retrieval":
		return TierStandard
	case "complex_reasoning", "safety_sensitive":
		return TierPremium
	default:
		return TierEfficient
	}
}

// filterCandidates 返回满足所有硬性要求的 model，按偏好排序
//（目标层级在前，层级内按成本）。
func (r *Router) filterCandidates(req *RouteRequest, targetTier ModelTier) []*ModelProfile {
	// 先取目标层级的 model，再回退到相邻层级
	tiers := []ModelTier{targetTier}

	// 加入相邻层级作为 fallback
	for t := ModelTier(0); t <= TierPremium; t++ {
		if t != targetTier {
			tiers = append(tiers, t)
		}
	}

	var candidates []*ModelProfile
	seen := make(map[string]bool)

	for _, tier := range tiers {
		for _, m := range r.registry.GetByTier(tier) {
			if seen[m.Name] {
				continue
			}
			seen[m.Name] = true

			if r.meetsRequirements(m, req) && !r.isRateLimited(m) {
				candidates = append(candidates, m)
			}
		}
	}

	return candidates
}

// meetsRequirements 检查 model 是否满足所有硬性要求。
func (r *Router) meetsRequirements(m *ModelProfile, req *RouteRequest) bool {
	// 检查上下文窗口
	if req.ContextLen > 0 && !m.SupportsContextLen(req.ContextLen) {
		return false
	}

	// 检查必要能力
	for _, cap := range req.RequiredCaps {
		if !m.HasCapability(cap) {
			return false
		}
	}

	// 检查预算上限（USD per 1M tokens）。
	// BudgetUSD 与 InputPrice 比较作为单次请求成本的保守代理 ——
	// 若仅输入价就超过预算，则拒绝该 model。
	// 未设价格（0）的 model 一律接受。
	if req.BudgetUSD > 0 && m.InputPrice > 0 && m.InputPrice > req.BudgetUSD {
		return false
	}

	// 检查延迟要求。
	// 若 model 平均延迟超过所请求的最大值，则拒绝。
	if req.LatencyReq > 0 && m.AvgLatencyMs > int(req.LatencyReq.Milliseconds()) {
		return false
	}

	return true
}

// resolveTargetTier 综合 intent、PreferredTier、AgentRole 决定目标 tier。
func (r *Router) resolveTargetTier(req *RouteRequest, intent IntentClassification) ModelTier {
	// 角色 override：leader/decomposer 不低于 TierStandard，validator/summarizer 不高于 TierLightweight
	switch req.AgentRole {
	case "leader", "decomposer":
		return max(max(r.intentToTier(intent.PrimaryIntent), TierStandard), req.PreferredTier)
	case "validator", "summarizer":
		// 这些角色通常不需要强模型；但仍尊重 PreferredTier 的升级请求。
		if req.PreferredTier > 0 {
			return req.PreferredTier
		}
		return min(r.intentToTier(intent.PrimaryIntent), TierLightweight)
	}

	return max(r.intentToTier(intent.PrimaryIntent), req.PreferredTier)
}

// pickCheaperModel 在指定层级（及以下）选择一个满足硬性要求且比 primary 便宜的 model。
func (r *Router) pickCheaperModel(primary *ModelProfile, req *RouteRequest, maxTier ModelTier) *ModelProfile {
	for t := maxTier; t >= TierFree; t-- {
		for _, m := range r.registry.GetByTier(t) {
			if m.Name == primary.Name {
				continue
			}
			if r.meetsRequirements(m, req) && !r.isRateLimited(m) {
				return m
			}
		}
	}
	return nil
}

// isRateLimited 检查模型是否触发 RPM 限流。未配置限流器时返回 false。
// 当模型被限流时，广播 model_rate_limited 事件，让前端 Inspector 看到被
// 过滤原因（白盒透明）。
func (r *Router) isRateLimited(m *ModelProfile) bool {
	if r.rateLimiter == nil {
		return false
	}
	exceeded := r.rateLimiter.IsLimitExceeded(m.Name)
	if exceeded {
		r.emit("model_rate_limited", map[string]any{
			"model": m.Name,
			"tier":  m.Tier.String(),
			"reason": "RPM limit exceeded in sliding window",
		})
	}
	return exceeded
}

// buildReason 构造人类可读的路由决策说明。
func (r *Router) buildReason(intent IntentClassification, primary *ModelProfile, targetTier ModelTier, cheapFirst bool) string {
	suffix := ""
	if cheapFirst {
		suffix = " (cheap-first attempt)"
	}
	return fmt.Sprintf(
		"Intent: %s (%.2f)%s → Tier: %s → Model: %s (%s, $%.2f/$%.2f per 1M tokens)",
		intent.PrimaryIntent,
		intent.Confidence,
		suffix,
		targetTier.String(),
		primary.Name,
		primary.Provider,
		primary.InputPrice,
		primary.OutputPrice,
	)
}

// isKnownIntent 检查给定的 intent 字符串是否在 8 类合法集合中。
func (r *Router) isKnownIntent(intent string) bool {
	switch strings.ToLower(strings.TrimSpace(intent)) {
	case "simple_chat", "code_generation", "code_execution", "complex_reasoning",
		"multi_step", "rag_retrieval", "web_search", "safety_sensitive":
		return true
	}
	return false
}

// SelectModel 是便捷方法，仅返回选中的 model 名。
// 调用方只需要 model 名而不需要完整决策时很有用。
func (r *Router) SelectModel(ctx context.Context, req *RouteRequest) (string, error) {
	decision, err := r.Select(ctx, req)
	if err != nil {
		return "", err
	}
	return decision.Primary.Name, nil
}
