// Package llm — Router for intent classification and model selection.
//
// # Design Rationale
//
// The Router is the decision-making component that selects the best model for
// a given task. It uses a two-phase approach:
//
//  1. Rule-based filtering (zero cost, zero latency): eliminate models that
//     don't meet hard requirements (context length, required capabilities).
//  2. Intent classification (cheap model, ~100 tokens, < $0.001): classify the
//     user's request into a category, then select the appropriate tier.
//
// This design keeps routing costs negligible while ensuring complex tasks
// get the powerful models they need and simple tasks use cheap models.
//
// # Intent Categories
//
//	simple_chat       — simple Q&A, chitchat, information lookup, format conversion
//	code_generation   — code writing, debugging, refactoring, code review
//	complex_reasoning — multi-step reasoning, math, logic, architecture design
//	multi_step        — requires multiple tool calls, multi-stage execution
//
// # Usage
//
//	router := llm.NewRouter(registry, classifierProvider)
//	decision, err := router.Select(&llm.RouteRequest{
//	    UserInput:    "Write a function to sort a list",
//	    RequiredCaps: []llm.ModelCapability{llm.CapToolCalling},
//	})
//
// See doc/chapters/10-multi-model-layered-design.html for the full design.
package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// IntentClassifierPrompt is the system prompt used by the Router's classifier
// to categorize user requests. The classifier is expected to respond with
// exactly one category name and nothing else.
const IntentClassifierPrompt = `You are a request classifier. Classify the user's request into exactly one category.
Respond with ONLY the category name, nothing else.

Categories:
- simple_chat: Simple Q&A, chitchat, information lookup, format conversion, greetings
- code_generation: Code writing, debugging, refactoring, code review, testing
- complex_reasoning: Multi-step reasoning, math problems, logic analysis, architecture design, planning
- multi_step: Requires multiple tool calls, multi-stage execution, agent orchestration

Request: %s
Category:`

// RouteRequest is the input to the Router's Select method.
// It describes the task characteristics that influence model selection.
type RouteRequest struct {
	// UserInput is the user's raw request text.
	UserInput string

	// ContextLen is the estimated input token count. Used to filter models
	// whose context window is too small.
	ContextLen int

	// RequiredCaps lists capabilities the selected model MUST have.
	// E.g., if the task requires tool calling, only models with CapToolCalling
	// will be considered.
	RequiredCaps []ModelCapability

	// BudgetUSD is an optional cost ceiling. If set, the Router will only
	// consider models whose estimated cost is within this budget.
	BudgetUSD float64

	// LatencyReq is an optional latency requirement. If set, the Router will
	// prefer models with AvgLatencyMs below this threshold.
	LatencyReq time.Duration

	// PreferredTier is an optional tier preference. If set, the Router will
	// prefer models in this tier (but may fall back to adjacent tiers).
	PreferredTier ModelTier
}

// RouteDecision is the output of the Router's Select method.
// It describes which model was selected and why.
type RouteDecision struct {
	// Primary is the selected model profile.
	Primary *ModelProfile

	// Fallback is the backup model to use if the primary fails.
	// May be nil if no fallback is configured.
	Fallback *ModelProfile

	// Intent is the classified intent category.
	Intent string

	// Reason is a human-readable explanation of the routing decision.
	// This is displayed in the frontend for "white-box" transparency.
	Reason string

	// Tier is the selected model tier.
	Tier ModelTier
}

// Router selects the best model for a given task request.
//
// The Router uses a cheap classifier model (typically Haiku or DeepSeek Flash)
// to categorize user requests, then selects the appropriate model tier.
// Rule-based filtering is applied first to eliminate models that don't meet
// hard requirements.
//
// # Thread Safety
//
// Router is safe for concurrent use — the registry is goroutine-safe and
// each Select call is independent.
type Router struct {
	registry   *ModelRegistry
	classifier Provider // cheap model for intent classification
}

// NewRouter creates a new Router with the given model registry and classifier.
//
// The classifier should be a cheap, fast model (e.g., Haiku or DeepSeek Flash)
// since it's called on every request. The classifier's cost should be < $0.001
// per classification to keep routing overhead negligible.
func NewRouter(registry *ModelRegistry, classifier Provider) *Router {
	return &Router{
		registry:   registry,
		classifier: classifier,
	}
}

// Select chooses the best model for the given request.
//
// The selection process:
//  1. Classify the user's intent using the cheap classifier model
//  2. Map the intent to a target model tier
//  3. Filter models by hard requirements (context length, capabilities)
//  4. Select the best matching model from the target tier
//  5. Resolve the fallback model
//
// If the classifier call fails, it falls back to rule-based classification
// (keyword matching) so the system remains functional even if the classifier
// is unavailable.
func (r *Router) Select(ctx context.Context, req *RouteRequest) (*RouteDecision, error) {
	// Step 1: Classify intent (with fallback to rule-based)
	intent, err := r.classifyIntent(ctx, req.UserInput)
	if err != nil {
		// Classifier failed — fall back to keyword-based classification
		intent = r.keywordClassify(req.UserInput)
	}

	// Step 2: Map intent to target tier
	targetTier := max(r.intentToTier(intent), req.PreferredTier)

	// Step 3: Filter candidates by hard requirements
	candidates := r.filterCandidates(req, targetTier)

	// Step 4: Select the best candidate
	var primary *ModelProfile
	if len(candidates) > 0 {
		primary = candidates[0]
	} else {
		// No candidates in target tier — try any tier
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

	// Step 5: Resolve fallback
	fallback := r.registry.GetFallback(primary.Name)

	return &RouteDecision{
		Primary:  primary,
		Fallback: fallback,
		Intent:   intent,
		Reason:   r.buildReason(intent, primary, targetTier),
		Tier:     primary.Tier,
	}, nil
}

// classifyIntent uses the cheap classifier model to categorize the user's request.
// Returns the intent category string, or an error if the classifier call fails.
func (r *Router) classifyIntent(_ context.Context, userInput string) (string, error) {
	prompt := fmt.Sprintf(IntentClassifierPrompt, userInput)

	req := ChatRequest{
		Model:       "", // use the classifier's default model
		Messages:    []Message{{Role: "user", Content: prompt}},
		Temperature: 0, // deterministic classification
		MaxTokens:   10, // only need one word
		Stream:      false,
	}

	resp, err := r.classifier.Chat(req)
	if err != nil {
		return "", fmt.Errorf("classifier call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("classifier returned empty response")
	}

	// Normalize the response
	intent := strings.TrimSpace(resp.Choices[0].Message.Content)
	intent = strings.ToLower(intent)

	// Validate against known categories
	switch intent {
	case "simple_chat", "code_generation", "complex_reasoning", "multi_step":
		return intent, nil
	default:
		// Unknown category — default to simple_chat
		return "simple_chat", nil
	}
}

// keywordClassify is a fallback classification method that uses keyword matching.
// It's used when the classifier model is unavailable (network error, rate limit, etc.).
func (r *Router) keywordClassify(userInput string) string {
	lower := strings.ToLower(userInput)

	// Multi-step indicators
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

	// Code generation indicators
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

	// Complex reasoning indicators
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

	// Default: simple chat
	return "simple_chat"
}

// intentToTier maps an intent category to a model tier.
//
// Mapping rationale:
//   - simple_chat → TierEfficient: trivial tasks, use cheapest model
//   - code_generation → TierStandard: needs reliable tool calling and code quality
//   - complex_reasoning → TierPremium: needs deep reasoning capabilities
//   - multi_step → TierStandard: needs reliable tool calling across multiple steps
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

// filterCandidates returns models that meet all hard requirements, sorted by
// preference (target tier first, then by cost within tier).
func (r *Router) filterCandidates(req *RouteRequest, targetTier ModelTier) []*ModelProfile {
	// Get models from the target tier first, then fall back to adjacent tiers
	tiers := []ModelTier{targetTier}

	// Add adjacent tiers as fallback
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

// meetsRequirements checks whether a model satisfies all hard requirements.
func (r *Router) meetsRequirements(m *ModelProfile, req *RouteRequest) bool {
	// Check context window
	if req.ContextLen > 0 && !m.SupportsContextLen(req.ContextLen) {
		return false
	}

	// Check required capabilities
	for _, cap := range req.RequiredCaps {
		if !m.HasCapability(cap) {
			return false
		}
	}

	// Check budget ceiling (USD per 1M tokens).
	// BudgetUSD is compared against InputPrice as a conservative proxy for
	// per-request cost — if the input price alone exceeds the budget, the model
	// is rejected. Models with no price set (zero) are always accepted.
	if req.BudgetUSD > 0 && m.InputPrice > 0 && m.InputPrice > req.BudgetUSD {
		return false
	}

	// Check latency requirement.
	// If the model's average latency exceeds the requested maximum, reject it.
	if req.LatencyReq > 0 && m.AvgLatencyMs > int(req.LatencyReq.Milliseconds()) {
		return false
	}

	return true
}

// buildReason constructs a human-readable explanation of the routing decision.
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

// SelectModel is a convenience method that returns just the selected model name.
// This is useful when the caller only needs the model name, not the full decision.
func (r *Router) SelectModel(ctx context.Context, req *RouteRequest) (string, error) {
	decision, err := r.Select(ctx, req)
	if err != nil {
		return "", err
	}
	return decision.Primary.Name, nil
}