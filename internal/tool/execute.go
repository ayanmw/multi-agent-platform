package tool

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// NewExecuteProgramTool 创建名为 "core/execute_program" 的代码执行工具。
//
// 参数：
//   - language   (string,  required)：python、node、bash 之一。
//   - code       (string,  required)：要执行的代码。
//   - timeout_ms (integer, optional)：超时时间，单位毫秒（默认 30000）。
//
// 安全：该工具带有 exec:dangerous tag。Harness 的 TagPolicyRule 会拦截它，
// 除非 TaskPermissions.AllowShellDangerous 为 true。即便被允许，executor
// 也会对常见破坏性模式（rm、curl|bash 等）做轻量静态检查，作为额外的
// 纵深防御。
func NewExecuteProgramTool() *BuiltinTool {
	// bt 先声明再赋值，使闭包能在调用时读取 bt.shellSandbox。
	// 默认 deny 策略由 DefaultShellSandboxConfig 提供（与 run_shell 一致）。
	var bt *BuiltinTool
	bt = NewBuiltinTool(
		"execute_program",
		"core",
		"Execute a short program in a supported interpreter (python, node, bash). Runs with a timeout and returns stdout/exit_code.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"language": map[string]any{
					"type":        "string",
					"description": "One of python, node, bash",
				},
				"code": map[string]any{
					"type":        "string",
					"description": "Code to execute",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Timeout in milliseconds (default 30000)",
				},
			},
			"required": []string{"language", "code"},
		},
		func(_ ExecuteContext, input map[string]any) (any, error) { return executeProgramExecutor(input, bt.shellSandbox) },
	).WithTags("exec", "exec:dangerous")
	bt.shellSandbox = DefaultShellSandboxConfig()
	return bt
}

// executeProgramExecutor 通过所配置的 ProgramRunner 在受支持的解释器中
// 运行代码。默认是本地 host runner；启动时可通过 SetDefaultRunner 替换为
// DockerRunner。
//
// 本地（无 Docker）安全降级（N1-04）：在既有 checkDangerousCode 静态拦截之上，
// 结合 ShellSandboxConfig 策略枚举裁决——deny/ask（无人值守）仍拒绝危险代码，
// allow 策略则放行但写审计告警。
func executeProgramExecutor(input map[string]any, cfg ShellSandboxConfig) (any, error) {
	language := strings.ToLower(getString(input, "language", ""))
	code := getString(input, "code", "")
	if language == "" || code == "" {
		return nil, fmt.Errorf("language and code required")
	}
	timeout := time.Duration(getInt(input, "timeout_ms", 30000)) * time.Millisecond

	if risk := checkDangerousCode(language, code); risk != "" {
		// 既有静态危险检查命中。默认（deny/ask）仍拒绝；仅 allow 策略放开。
		if cfg.Policy == PolicyAllow {
			recordShellSandboxAudit(ExecuteContext{}, "shell_sandbox_allowed_risk", code, risk, cfg.Policy)
		} else {
			recordShellSandboxAudit(ExecuteContext{}, "shell_sandbox_denied", code, risk, cfg.Policy)
			return nil, fmt.Errorf("risk pattern detected: %s", risk)
		}
	}

	// 额外：对整段代码跑 Shell 沙箱黑名单，覆盖 git push --force 等
	// checkDangerousCode 未覆盖的危险写法。deny/ask 拒绝；allow 放行（已在上
	// 一步处理危险静态检查，这里命中黑名单同样按策略裁决）。
	if decision, rule, _ := cfg.Evaluate(code); decision != DecisionAllow {
		recordShellSandboxAudit(ExecuteContext{}, "shell_sandbox_denied", code, rule, cfg.Policy)
		return nil, fmt.Errorf("blocked by shell sandbox policy: %s", rule)
	}

	if language == "go" {
		return nil, fmt.Errorf("go execution not yet supported in sandbox")
	}

	out, exitCode, err := GetDefaultRunner().Run(context.Background(), language, code, timeout)
	timedOut := false
	if err != nil {
		if exitCode == -1 && (err == context.DeadlineExceeded || strings.Contains(err.Error(), "context deadline exceeded")) {
			timedOut = true
			err = nil
		}
	}

	return map[string]any{
		"stdout":    out,
		"stderr":    "",
		"exit_code": exitCode,
		"timed_out": timedOut,
	}, err
}

// dangerousPatterns 将风险关键字映射为人类可读的原因。这些检查刻意保持
// 浅层（有决心的话仍可绕过）。其目的是在启动进程之前捕捉 LLM 明显的
// 误用。
var dangerousPatterns = []struct {
	pattern string
	reason  string
}{
	// 下载并执行不可信代码的 shell 执行管道。
	{`curl\s+[^|]*\|\s*(sh|bash)`, "pipe curl into shell"},
	{`wget\s+[^|]*\|\s*(sh|bash)`, "pipe wget into shell"},
	// 破坏性文件系统操作。
	{`\brm\s+-rf\s+/`, "recursive remove from root"},
	{`\brm\s+-rf\s+[~\\/]`, "recursive remove home/system"},
	// 常见的数据外泄 / 后门辅助工具。
	{`\bnc\s+-[ecl]\s+`, "netcat remote shell"},
	{`\bmkfifo\b`, "fifo backdoor"},
	{`\bprivilege\s*escalation\b`, "explicit privilege escalation"},
}

// checkDangerousCode 扫描源代码中明显有风险的习惯用法。若未匹配到任何
// 已知模式，则返回空字符串。匹配大小写不敏感。
func checkDangerousCode(language, code string) string {
	lower := strings.ToLower(code)
	// Python 特有的危险调用。
	if language == "python" {
		pyPatterns := []struct {
			pattern string
			reason  string
		}{
			{`\beval\s*\(`, "eval()"},
			{`\bexec\s*\(`, "exec()"},
			{`\bos\.system\s*\(`, "os.system()"},
			{`\bsubprocess\.call\s*\(.*shell\s*=\s*true`, "subprocess shell=True"},
			{`\bsubprocess\.run\s*\(.*shell\s*=\s*true`, "subprocess shell=True"},
			{`__import__\s*\(\s*['"]os['"]`, "dynamic os import"},
			{`__import__\s*\(\s*['"]subprocess['"]`, "dynamic subprocess import"},
		}
		for _, p := range pyPatterns {
			re := regexp.MustCompile(p.pattern)
			if re.MatchString(lower) {
				return p.reason
			}
		}
	}
	for _, p := range dangerousPatterns {
		re := regexp.MustCompile(p.pattern)
		if re.MatchString(lower) {
			return p.reason
		}
	}
	return ""
}
