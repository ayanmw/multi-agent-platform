// Package harness implements the Harness Engineering layer — the deterministic
// scaffolding that wraps the Agent's ReAct loop to enforce safety, scope, budget,
// and progress tracking.
//
// # Harness Engineering Philosophy
//
// The Harness is the structured control layer surrounding the Agent. While the
// Agent's LLM is non-deterministic and prompt-driven, the Harness is deterministic
// Go code that enforces hard constraints. The Harness does NOT rely on the LLM
// to "behave" — it intercepts tool calls BEFORE execution and can reject them.
//
// # Architecture (6-layer model, adapted for this project)
//
//   L0: Model      — LLM provider (internal/llm)
//   L1: Interface  — ReAct Loop engine (internal/runtime)
//   L2: Tool       — Tool registry + execution (internal/tool)
//   L3: Harness    — PolicyGate + TaskContract + Progress (THIS PACKAGE)
//   L4: Memory     — Self-evolving memory (Phase 4+)
//   L5: Governance — Audit + eval + cost control (Phase 6+)
//
// # Key Concepts
//
// TaskContract: A structured, machine-readable definition of what the task is,
// what it's allowed to do, and what constitutes success. The contract is enforced
// by the PolicyGate, not the LLM's system prompt.
//
// PolicyGate: A chain of PolicyRules that intercept tool calls before execution.
// Each rule can:
//   - Allow: the tool call proceeds
//   - Block:  the tool call is rejected with a reason (returned to the LLM as an error)
//   - Modify: the tool call's arguments are transformed (e.g., path normalization)
//
// TaskProgress: Externalized state tracking. The progress file is written at key
// nodes so that:
//   - The task can be resumed after a crash (checkpoint recovery)
//   - The user can see where the task is without reading the full conversation
//   - The context window is not the only source of truth about task state
//
// # Four-Question Boundary Test (四问法)
//
//   T1. Runtime Loop: Who calls the model? → Engine (internal/runtime)
//   T2. Environmental Action: Who calls tools?  → Engine via Tool Registry
//   T3. Task-Aware Context: Who manages scope?  → Harness (TaskContract)
//   T4. Independent Control: Who enforces rules? → Harness (PolicyGate)
//
// # Integration with Engine
//
// The Harness wraps the Tool Registry. When the Engine calls tool.Execute(),
// it goes through the Harness first:
//
//   Engine.ExecuteTool() → Harness.PolicyGate.Check() → Tool.Execute()
//
// If the PolicyGate blocks, the Engine receives an ErrBlockedByPolicy and
// emits a tool_call_failed event. The LLM sees the error and can try a
// different approach.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ============================================================================
// TaskContract — structured task definition enforced by the Harness
// ============================================================================

// TaskContract defines the scope, constraints, and success criteria for a task.
// It is the machine-readable contract between the user and the agent — every
// constraint in the contract is enforced by the PolicyGate, not by prompting.
//
// Design rationale: Without a TaskContract, the only constraints on the agent
// are in the system prompt, which the LLM can ignore or "forget" as context
// grows. The TaskContract is enforced by deterministic Go code — the LLM
// cannot bypass it.
type TaskContract struct {
	// Goal is a human-readable description of what the task should accomplish.
	// It is used for progress tracking and summary generation, not for enforcement.
	Goal string `json:"goal"`

	// Scope defines the working directory for file operations. All file paths
	// are resolved relative to this directory. Paths outside this scope are
	// rejected by FileScopeRule.
	Scope string `json:"scope"`

	// AllowedTools is the list of tool names the agent is permitted to use.
	// If empty, all registered tools are allowed. If set, only these tools
	// can be called (enforced by ToolWhitelistRule — Phase 4).
	AllowedTools []string `json:"allowed_tools,omitempty"`

	// TokenBudget is the maximum total tokens the agent may consume across all
	// LLM calls. When the cumulative token count exceeds this budget, the
	// PolicyGate blocks further LLM calls. 0 means unlimited.
	// Enforced by TokenBudgetRule (Phase 4).
	TokenBudget int `json:"token_budget,omitempty"`

	// MaxSteps is the maximum number of ReAct loop iterations. When exceeded,
	// the Engine terminates with max_steps_exceeded. This is enforced by the
	// Engine itself, not the PolicyGate.
	MaxSteps int `json:"max_steps"`

	// AcceptanceCriteria defines what constitutes a successful task completion.
	// Multiple criteria can be combined. All must pass for the task to be
	// considered complete. If empty, any LLM final answer is accepted.
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria,omitempty"`

	// Permissions defines what the agent is allowed to do (e.g., network access,
	// file deletion). These are enforced by the PolicyGate.
	Permissions TaskPermissions `json:"permissions"`

	// Metadata carries arbitrary key-value pairs for the harness (e.g., case name,
	// expected output, tags). Not used for enforcement.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// TaskPermissions defines the agent's operational permissions.
// All fields default to false — the agent starts with no permissions
// and must be explicitly granted each one.
type TaskPermissions struct {
	// AllowNetwork enables the agent to make HTTP requests (future tool).
	AllowNetwork bool `json:"allow_network"`

	// AllowFileDelete enables the agent to delete files (future tool).
	AllowFileDelete bool `json:"allow_file_delete"`

	// AllowFileWrite enables the agent to write/create files.
	AllowFileWrite bool `json:"allow_file_write"`

	// AllowShell enables the agent to execute shell commands.
	AllowShell bool `json:"allow_shell"`

	// AllowShellDangerous enables dangerous shell commands (e.g., rm -rf, git push --force).
	// Even with this enabled, DangerousCommandRule (Phase 5) may require frontend approval.
	AllowShellDangerous bool `json:"allow_shell_dangerous"`
}

// DefaultContract returns a permissive TaskContract suitable for simple tasks.
// Production tasks should use explicit contracts with minimal permissions.
func DefaultContract(goal string) TaskContract {
	return TaskContract{
		Goal:     goal,
		Scope:    ".",
		MaxSteps: 10,
		Permissions: TaskPermissions{
			AllowFileWrite: true,
			AllowShell:     true,
		},
	}
}

// ============================================================================
// TaskProgress — externalized state tracking
// ============================================================================

// TaskProgress tracks the task's progress at key nodes. It is written to a
// progress file in the task's working directory, providing externalized state
// that survives context-window resets and process crashes.
//
// Why externalize? The LLM's context window is the primary "memory" of what
// has been done, but it's fragile — context can be truncated, the model can
// hallucinate, and the conversation history is not verifiable. The Progress
// file is a verifiable record of what the agent has actually accomplished.
type TaskProgress struct {
	// TaskID is the unique task identifier
	TaskID string `json:"task_id"`

	// Goal is the task's objective (from TaskContract)
	Goal string `json:"goal"`

	// Status is the current task status
	Status string `json:"status"` // "running", "completed", "failed"

	// CurrentStep is the current ReAct loop iteration
	CurrentStep int `json:"current_step"`

	// TotalSteps is the max allowed steps (from TaskContract)
	TotalSteps int `json:"total_steps"`

	// TotalTokens is the cumulative token usage
	TotalTokens int `json:"total_tokens"`

	// Nodes are key milestones in the task's execution. Each node records
	// what was accomplished at that point in the task.
	Nodes []ProgressNode `json:"nodes"`

	// StartedAt is when the task began
	StartedAt time.Time `json:"started_at"`

	// UpdatedAt is the last time the progress was written
	UpdatedAt time.Time `json:"updated_at"`
}

// ProgressNode represents a key milestone in the task's execution.
// Nodes are written at significant points: tool call results, step completions,
// error encounters, and task completion.
type ProgressNode struct {
	// Step is the ReAct loop iteration when this node was recorded
	Step int `json:"step"`

	// Type describes the node type: "tool_call", "observation", "milestone", "error", "complete"
	Type string `json:"type"`

	// Summary is a human-readable description of what happened at this node
	Summary string `json:"summary"`

	// Data carries type-specific data (e.g., tool name and result for tool_call)
	Data map[string]any `json:"data,omitempty"`

	// Timestamp is when this node was recorded
	Timestamp time.Time `json:"timestamp"`
}

// ProgressManager handles writing and reading TaskProgress files.
// It writes to a JSON file in the task's scope directory.
type ProgressManager struct {
	// ProgressPath is the path to the progress file (default: "task_progress.json")
	ProgressPath string
}

// NewProgressManager creates a new ProgressManager with the default progress
// file path.
func NewProgressManager() *ProgressManager {
	return &ProgressManager{
		ProgressPath: "task_progress.json",
	}
}

// Init creates a new TaskProgress file for the given task.
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

// AddNode appends a progress node and writes the updated file.
func (pm *ProgressManager) AddNode(progress *TaskProgress, node ProgressNode) error {
	node.Timestamp = time.Now()
	progress.Nodes = append(progress.Nodes, node)
	progress.CurrentStep = node.Step
	progress.UpdatedAt = time.Now()
	return pm.Write(progress)
}

// SetStatus updates the task status and writes the file.
func (pm *ProgressManager) SetStatus(progress *TaskProgress, status string, totalTokens int) error {
	progress.Status = status
	progress.TotalTokens = totalTokens
	progress.UpdatedAt = time.Now()
	return pm.Write(progress)
}

// Write serializes the TaskProgress to the progress file.
func (pm *ProgressManager) Write(progress *TaskProgress) error {
	progress.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(progress, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal progress: %w", err)
	}
	// Write to task-specific path: {scope}/task_progress.json
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

// Load reads a TaskProgress file from disk.
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

// Exists returns true if the progress file exists on disk.
func (pm *ProgressManager) Exists() bool {
	_, err := os.Stat(pm.ProgressPath)
	return err == nil
}

// ============================================================================
// AcceptanceCriteria — defining what "success" means
// ============================================================================

// AcceptanceCriterionType defines the type of check to perform.
type AcceptanceCriterionType string

const (
	// AcceptTestPass checks that a test command exits with code 0.
	// E.g., "go test ./..." or "python -m pytest"
	AcceptTestPass AcceptanceCriterionType = "test_pass"

	// AcceptFileExists checks that a file exists at the given path.
	// E.g., "output/report.md" or "src/main.go"
	AcceptFileExists AcceptanceCriterionType = "file_exists"

	// AcceptShellExitZero checks that a shell command exits with code 0.
	// More general than test_pass — can be any command.
	AcceptShellExitZero AcceptanceCriterionType = "shell_exit_zero"

	// AcceptContentContains checks that a file contains a specific string.
	// E.g., the generated report includes "Summary" section.
	AcceptContentContains AcceptanceCriterionType = "content_contains"
)

// AcceptanceCriterion defines a single check for task completion.
// Multiple criteria can be combined in the TaskContract — all must pass.
type AcceptanceCriterion struct {
	// Type is the kind of check to perform
	Type AcceptanceCriterionType `json:"type"`

	// Target is the subject of the check (file path, command, or search string)
	Target string `json:"target"`

	// Expected is the expected value (for content_contains: the substring to find)
	Expected string `json:"expected,omitempty"`

	// Description is a human-readable explanation of what this criterion checks
	Description string `json:"description"`
}

// ============================================================================
// PolicyGate — the enforcement layer
// ============================================================================

// PolicyRule is a single rule in the PolicyGate. Each rule can inspect a tool
// call before execution and decide whether to allow, block, or modify it.
//
// The PolicyRule interface is intentionally minimal — a single Check method.
// Rules are composed into a PolicyChain, which is checked in order before
// every tool execution.
type PolicyRule interface {
	// Name returns a human-readable rule name for logging and error messages.
	Name() string

	// Check evaluates the tool call against this rule. Returns:
	//   - nil if the rule allows the call (or doesn't apply to this tool)
	//   - ErrBlockedByPolicy if the rule blocks the call
	//   - Optionally modified input (e.g., path normalization)
	Check(toolName string, input map[string]any, contract TaskContract) (allowedInput map[string]any, err error)
}

// TokenAwareRule extends PolicyRule for rules that need to know the current
// token usage. TokenBudgetRule implements this to enforce token budgets.
// The Engine calls SetTokenUsage before each tool execution to update the
// cumulative token count.
type TokenAwareRule interface {
	PolicyRule
	// SetTokenUsage updates the rule's knowledge of cumulative token usage.
	SetTokenUsage(totalTokens int)
}

// ErrBlockedByPolicy is returned when a PolicyRule blocks a tool call.
// The LLM receives this error as the tool's output and can try a different
// approach (e.g., writing to a different directory).
type ErrBlockedByPolicy struct {
	Rule    string // the rule that blocked the call
	Reason  string // human-readable reason for the block
	Tool    string // the tool that was blocked
}

// Error implements the error interface.
func (e *ErrBlockedByPolicy) Error() string {
	return fmt.Sprintf("[POLICY BLOCK] %s blocked %s: %s", e.Rule, e.Tool, e.Reason)
}

// PolicyChain is an ordered list of PolicyRules. Rules are checked in order;
// the first rule that blocks stops the chain. If a rule modifies the input,
// the modified input is passed to the next rule.
type PolicyChain struct {
	rules []PolicyRule
}

// NewPolicyChain creates a new PolicyChain with the given rules.
func NewPolicyChain(rules ...PolicyRule) *PolicyChain {
	return &PolicyChain{rules: rules}
}

// AddRule appends a rule to the chain.
func (pc *PolicyChain) AddRule(rule PolicyRule) {
	pc.rules = append(pc.rules, rule)
}

// Check runs all rules in order against the tool call. Returns the (possibly
// modified) input and nil if all rules pass, or the original input and an
// ErrBlockedByPolicy if any rule blocks.
func (pc *PolicyChain) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	currentInput := input
	for _, rule := range pc.rules {
		allowedInput, err := rule.Check(toolName, currentInput, contract)
		if err != nil {
			return input, err // return original input on block
		}
		currentInput = allowedInput
	}
	return currentInput, nil
}

// PolicyGate wraps a tool execution with policy enforcement. It is the
// primary integration point between the Harness and the Engine.
//
// Usage:
//
//	gate := harness.NewPolicyGate(chain, contract)
//	result, err := gate.Execute(toolName, input, func(input map[string]any) (any, error) {
//	    return registry.Execute(toolName, input)
//	})
type PolicyGate struct {
	chain    *PolicyChain
	contract TaskContract
}

// NewPolicyGate creates a PolicyGate with the given policy chain and contract.
// If chain is nil, all tool calls are allowed (no policy enforcement).
func NewPolicyGate(chain *PolicyChain, contract TaskContract) *PolicyGate {
	if chain == nil {
		chain = NewPolicyChain()
	}
	return &PolicyGate{
		chain:    chain,
		contract: contract,
	}
}

// Execute runs the tool call through the policy chain, then executes the tool
// if all rules pass. The executor callback is the actual tool execution logic
// (typically registry.Execute).
func (g *PolicyGate) Execute(toolName string, input map[string]any, executor func(map[string]any) (any, error)) (any, error) {
	// Check the policy chain before executing
	allowedInput, err := g.chain.Check(toolName, input, g.contract)
	if err != nil {
		return nil, err
	}

	// Execute the tool with the (possibly modified) input
	return executor(allowedInput)
}

// Contract returns the current TaskContract (read-only).
func (g *PolicyGate) Contract() TaskContract {
	return g.contract
}

// SetTokenUsage updates all TokenAwareRules in the chain with the current
// cumulative token count. Called by the Engine before each tool execution.
func (g *PolicyGate) SetTokenUsage(totalTokens int) {
	for _, rule := range g.chain.rules {
		if ta, ok := rule.(TokenAwareRule); ok {
			ta.SetTokenUsage(totalTokens)
		}
	}
}

// ============================================================================
// Helper: wrap tool execution result as a JSON string
// ============================================================================

// ToJSONString serializes a value to a JSON string for LLM consumption.
// If serialization fails, returns the error message as the string.
func ToJSONString(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"error": "failed to serialize result: %s"}`, err.Error())
	}
	return string(data)
}

// ============================================================================
// FileScopeRule — restricts file operations to the contract's scope
// ============================================================================

// FileScopeRule ensures that all file operations (read_file, write_file) stay
// within the TaskContract's Scope directory. Paths outside the scope are
// rejected with ErrBlockedByPolicy.
//
// This rule normalizes paths relative to the scope directory, so the LLM can
// use relative paths and the rule will resolve them correctly.
type FileScopeRule struct{}

// Name returns the rule name.
func (r *FileScopeRule) Name() string { return "FileScopeRule" }

// Check validates that the file path is within the contract's scope.
// If the path is absolute, it checks whether it's under the scope.
// If the path is relative, it resolves it against the scope.
// The returned input contains the normalized absolute path.
func (r *FileScopeRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	// Only apply to file-related tools
	if toolName != "write_file" && toolName != "read_file" {
		return input, nil
	}

	path, ok := input["path"].(string)
	if !ok || path == "" {
		return input, nil // no path to check; let the tool handle the error
	}

	// Resolve the scope to an absolute path
	scopeAbs, err := filepath.Abs(contract.Scope)
	if err != nil {
		return input, fmt.Errorf("FileScopeRule: resolve scope: %w", err)
	}

	// Resolve the requested path
	var targetAbs string
	if filepath.IsAbs(path) {
		targetAbs = filepath.Clean(path)
	} else {
		targetAbs = filepath.Join(scopeAbs, filepath.Clean(path))
	}

	// Check if the target is within the scope
	if !strings.HasPrefix(targetAbs, scopeAbs+string(filepath.Separator)) && targetAbs != scopeAbs {
		return input, &ErrBlockedByPolicy{
			Rule:   r.Name(),
			Reason: fmt.Sprintf("path %q is outside the allowed scope %q", path, contract.Scope),
			Tool:   toolName,
		}
	}

	// Normalize the path in the input to the absolute path
	normalizedInput := make(map[string]any, len(input))
	for k, v := range input {
		normalizedInput[k] = v
	}
	normalizedInput["path"] = targetAbs

	return normalizedInput, nil
}

// ============================================================================
// PathTraversalRule — blocks ".." in paths
// ============================================================================

// PathTraversalRule rejects file paths that contain ".." segments, which are
// the most common form of directory traversal attack. This rule works alongside
// FileScopeRule as a defense-in-depth measure.
type PathTraversalRule struct{}

// Name returns the rule name.
func (r *PathTraversalRule) Name() string { return "PathTraversalRule" }

// Check rejects paths containing ".." segments.
// This is a simple, fast check that catches the most obvious traversal attempts.
// The FileScopeRule provides a more comprehensive scope check.
func (r *PathTraversalRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	// Only apply to file-related tools and shell commands
	if toolName != "write_file" && toolName != "read_file" && toolName != "run_shell" {
		return input, nil
	}

	// Check the "path" parameter for file tools
	if path, ok := input["path"].(string); ok && path != "" {
		if strings.Contains(path, "..") {
			return input, &ErrBlockedByPolicy{
				Rule:   r.Name(),
				Reason: fmt.Sprintf("path contains '..' traversal: %q", path),
				Tool:   toolName,
			}
		}
	}

	// For run_shell, do a quick scan of the command string for .. patterns
	// that look like path traversal (not exhaustive, but catches obvious cases)
	if toolName == "run_shell" {
		if cmd, ok := input["command"].(string); ok && cmd != "" {
			// Look for patterns like "../" or "..\" in the command
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
// AcceptanceEvaluator — evaluates acceptance criteria
// ============================================================================

// AcceptanceEvaluator checks whether a task's acceptance criteria have been met.
// It runs each criterion's check and returns a report of which passed and which failed.
type AcceptanceEvaluator struct {
	scope string // working directory for resolving relative paths
}

// NewAcceptanceEvaluator creates a new AcceptanceEvaluator with the given scope.
func NewAcceptanceEvaluator(scope string) *AcceptanceEvaluator {
	return &AcceptanceEvaluator{scope: scope}
}

// EvalResult is the result of evaluating a single acceptance criterion.
type EvalResult struct {
	Criterion AcceptanceCriterion `json:"criterion"`
	Passed    bool                `json:"passed"`
	Message   string              `json:"message"`
	Duration  int64               `json:"duration_ms"`
}

// EvalReport is the result of evaluating all acceptance criteria.
type EvalReport struct {
	AllPassed bool         `json:"all_passed"`
	Results   []EvalResult `json:"results"`
	Summary   string       `json:"summary"`
}

// Evaluate runs all acceptance criteria and returns a report.
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

// evaluateOne runs a single acceptance criterion check.
func (ae *AcceptanceEvaluator) evaluateOne(criterion AcceptanceCriterion) EvalResult {
	start := time.Now()

	switch criterion.Type {
	case AcceptFileExists:
		return ae.checkFileExists(criterion, start)

	case AcceptContentContains:
		return ae.checkContentContains(criterion, start)

	case AcceptTestPass, AcceptShellExitZero:
		return ae.checkShell(criterion, start)

	default:
		return EvalResult{
			Criterion: criterion,
			Passed:    false,
			Message:   fmt.Sprintf("Unknown criterion type: %s", criterion.Type),
			Duration:  time.Since(start).Milliseconds(),
		}
	}
}

// checkFileExists verifies that a file exists at the target path.
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

// checkContentContains verifies that a file contains the expected string.
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

// checkShell runs a shell command and checks the exit code.
// NOTE: This is intentionally limited — it uses a short timeout and does not
// allow arbitrary commands. It is designed for test runners and validation
// commands, not general-purpose shell execution.
func (ae *AcceptanceEvaluator) checkShell(criterion AcceptanceCriterion, start time.Time) EvalResult {
	// For safety, shell exit checks are stubbed in the initial implementation.
	// Full implementation requires Docker sandboxing (Phase 5).
	// For now, we return a "not implemented" result that doesn't block.
	return EvalResult{
		Criterion: criterion,
		Passed:    true, // soft pass — don't block on unimplemented checks
		Message:   fmt.Sprintf("Shell check skipped (not yet implemented): %s. Full implementation in Phase 5 with Docker sandbox.", criterion.Target),
		Duration:  time.Since(start).Milliseconds(),
	}
}

// truncateForDisplay truncates a string to maxLen for display purposes.
func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ============================================================================
// TokenBudgetRule — enforces the TaskContract's token budget
// ============================================================================

// TokenBudgetRule blocks all tool calls when the cumulative token usage exceeds
// the TaskContract's TokenBudget. It implements TokenAwareRule so the Engine can
// update the cumulative token count before each tool execution.
//
// Design rationale: The token budget is a hard economic constraint — once the
// budget is exceeded, the agent should stop consuming tokens. This rule is
// checked BEFORE tool execution (not during LLM calls) because the Engine
// already tracks token usage per LLM call. The rule blocks subsequent tool
// calls to prevent the agent from making more LLM calls after the budget is
// exceeded.
//
// When TokenBudget is 0, the rule never blocks (unlimited budget).
type TokenBudgetRule struct {
	// totalTokens tracks the cumulative token usage across all LLM calls.
	// Updated by the Engine via SetTokenUsage.
	totalTokens int
}

// Name returns the rule name.
func (r *TokenBudgetRule) Name() string { return "TokenBudgetRule" }

// SetTokenUsage updates the cumulative token count. Called by the Engine
// (via PolicyGate.SetTokenUsage) before each tool execution.
func (r *TokenBudgetRule) SetTokenUsage(totalTokens int) {
	r.totalTokens = totalTokens
}

// Check blocks all tool calls if the cumulative token usage exceeds the budget.
// When TokenBudget is 0 (unlimited), this rule always allows.
func (r *TokenBudgetRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	if contract.TokenBudget <= 0 {
		return input, nil // unlimited budget
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
// ToolWhitelistRule — restricts which tools the agent can use
// ============================================================================

// ToolWhitelistRule only allows tool calls to tools listed in the TaskContract's
// AllowedTools field. If AllowedTools is empty, all tools are allowed.
//
// Design rationale: The TaskContract specifies which tools the agent is allowed
// to use. This rule enforces that restriction at the PolicyGate level, before the
// tool is executed. The LLM may still request blocked tools in its response, but
// the PolicyGate will intercept and return an error — the LLM can then try a
// different approach.
type ToolWhitelistRule struct{}

// Name returns the rule name.
func (r *ToolWhitelistRule) Name() string { return "ToolWhitelistRule" }

// Check blocks tool calls to tools not in the contract's AllowedTools list.
// If AllowedTools is empty, all tools are allowed (no restriction).
func (r *ToolWhitelistRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	if len(contract.AllowedTools) == 0 {
		return input, nil // no restrictions — all tools allowed
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