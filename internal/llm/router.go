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
//	complex_reasoning —— 多步推理、数学、逻辑、架构设计
//	multi_step        —— 需要多次 tool call、多阶段执行
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
	"fmt"
	"strings"
	"time"
)

// IntentClassifierPrompt 是 Router 分类器使用的 system prompt，
// 用于对用户请求分类。分类器应仅回复一个类别名，不返回其他内容。
const IntentClassifierPrompt = `You are a request classifier. Classify the user's request into exactly one category.
Respond with ONLY the category name, nothing else.

Categories:
- simple_chat: Simple Q&A, chitchat, information lookup, format conversion, greetings
- code_generation: Code writing, debugging, refactoring, code review, testing
- complex_reasoning: Multi-step reasoning, math problems, logic analysis, architecture design, planning
- multi_step: Requires multiple tool calls, multi-stage execution, agent orchestration

Request: %s
Category:`

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
}

// RouteDecision 是 Router Select 方法的输出。
// 它描述选中了哪个 model 以及原因。
type RouteDecision struct {
	// Primary 是选中的 model profile。
	Primary *ModelProfile

	// Fallback 是 primary 失败时的备用 model。
	// 未配置 fallback 时可能为 nil。
	Fallback *ModelProfile

	// Intent 是分类得到的 intent 类别。
	Intent string

	// Reason 是人类可读的路由决策说明。
	// 它会显示在前端，体现"白盒"透明度。
	Reason string

	// Tier 是选中的 model 层级。
	Tier ModelTier
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
	registry   *ModelRegistry
	classifier Provider // 用于 intent 分类的便宜 model
}

// NewRouter 以给定的 model registry 与分类器创建新的 Router。
//
// 分类器应是便宜快速的 model（例如 Haiku 或 DeepSeek Flash），
// 因为每个请求都会调用它。单次分类成本应 < $0.001，
// 以保持路由开销可忽略。
func NewRouter(registry *ModelRegistry, classifier Provider) *Router {
	return &Router{
		registry:   registry,
		classifier: classifier,
	}
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
	intent, err := r.classifyIntent(ctx, req.UserInput)
	if err != nil {
		// 分类器失败 —— 回退到基于关键字的分类
		intent = r.keywordClassify(req.UserInput)
	}

	// Step 2：把 intent 映射到目标层级
	targetTier := max(r.intentToTier(intent), req.PreferredTier)

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

	return &RouteDecision{
		Primary:  primary,
		Fallback: fallback,
		Intent:   intent,
		Reason:   r.buildReason(intent, primary, targetTier),
		Tier:     primary.Tier,
	}, nil
}

// classifyIntent 用便宜分类 model 对用户请求分类。
// 返回 intent 类别字符串；分类器调用失败则返回 error。
func (r *Router) classifyIntent(_ context.Context, userInput string) (string, error) {
	prompt := fmt.Sprintf(IntentClassifierPrompt, userInput)

	req := ChatRequest{
		Model:       "", // 使用分类器的默认 model
		Messages:    []Message{{Role: "user", Content: prompt}},
		Temperature: 0, // 确定性分类
		// NOTE：推理型 model（DeepSeek R1/V4、Step-3.x）会在思维链上烧光
		// 整个 token 预算，然后才给出最终答案。预算很小（例如 10）时
		// Content 一直为空，分类器总是回退。512 足以让推理收敛并让 model
		// 在 Content 中吐出单个类别 token；在 Flash 层级 model 上单次
		// 仍 < $0.001，符合"路由成本可忽略"的设计目标。
		MaxTokens: 512,
		Stream:    false,
	}

	resp, err := r.classifier.Chat(req)
	if err != nil {
		return "", fmt.Errorf("classifier call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("classifier returned empty response")
	}

	// 归一化响应。部分推理型 model（DeepSeek R1/V4、Step-3.x）在
	// max_tokens 很小时把输出放在 Message.Reasoning 中，Content 为空
	//（推理耗尽预算，没产出 final answer）。回退到 Reasoning 让
	// classifyIntent 仍能提取出类别 token；若两字段都没有已知类别，
	// 下面的 default 分支会映射到 simple_chat。
	intent := strings.TrimSpace(resp.Choices[0].Message.Content)
	if intent == "" {
		intent = strings.TrimSpace(resp.Choices[0].Message.Reasoning)
	}
	intent = strings.ToLower(intent)

	// 对已知类别做校验。分类器可能给类别加上标点或尾巴
	//（"simple_chat."、"category: code_generation"），
	// 因此扫描首个已知类别子串而非要求精确匹配。这样既保持路由稳健，
	// 又不削弱契约（无法识别的文本仍会走下面的 simple_chat 默认）。
	for _, cat := range []string{"simple_chat", "code_generation", "complex_reasoning", "multi_step"} {
		if strings.Contains(intent, cat) {
			return cat, nil
		}
	}

	// 未知类别 —— 默认 simple_chat，让路由优雅降级
	//（最便宜层级），而不是让整个任务失败。
	return "simple_chat", nil
}

// keywordClassify 是基于关键字匹配的回退分类方法。
// 在分类 model 不可用时（网络错误、限流等）使用。
func (r *Router) keywordClassify(userInput string) string {
	lower := strings.ToLower(userInput)

	// multi_step 指示词
	multiStepKeywords := []string{
		"multi-step", "multi step", "pipeline", "orchestrate",
		"first", "then", "after that", "finally",
		"multiple agents", "subtask", "decompose",
	}
	for _, kw := range multiStepKeywords {
		if strings.Contains(lower, kw) {
			return "multi_step"
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
			return "code_generation"
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
			return "complex_reasoning"
		}
	}

	// 默认：simple chat
	return "simple_chat"
}

// intentToTier 将 intent 类别映射到 model 层级。
//
// 映射理由：
//   - simple_chat → TierEfficient：琐碎任务，用最便宜 model
//   - code_generation → TierStandard：需要可靠的 tool calling 与代码质量
//   - complex_reasoning → TierPremium：需要深度推理能力
//   - multi_step → TierStandard：跨多步需要可靠 tool calling
func (r *Router) intentToTier(intent string) ModelTier {
	switch intent {
	case "simple_chat":
		return TierEfficient
	case "code_generation":
		return TierStandard
	case "complex_reasoning":
		return TierPremium
	case "multi_step":
		return TierStandard
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

			if r.meetsRequirements(m, req) {
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

// buildReason 构造人类可读的路由决策说明。
func (r *Router) buildReason(intent string, primary *ModelProfile, targetTier ModelTier) string {
	return fmt.Sprintf(
		"Intent: %s → Tier: %s → Model: %s (%s, $%.2f/$%.2f per 1M tokens)",
		intent,
		targetTier.String(),
		primary.Name,
		primary.Provider,
		primary.InputPrice,
		primary.OutputPrice,
	)
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