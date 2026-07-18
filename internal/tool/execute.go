package tool

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// NewExecuteProgramTool creates a code execution tool named "core/execute_program".
//
// Parameters:
//   - language   (string,  required): One of python, node, bash.
//   - code       (string,  required): Code to execute.
//   - timeout_ms (integer, optional): Timeout in milliseconds (default 30000).
//
// Security: this tool is tagged exec:dangerous. The Harness TagPolicyRule will
// block it unless TaskPermissions.AllowShellDangerous is true. Even when
// allowed, the executor also performs lightweight static checks for common
// destructive patterns (rm, curl|bash, etc.) as an additional defence in depth.
func NewExecuteProgramTool() *BuiltinTool {
	return NewBuiltinTool(
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
		executeProgramExecutor,
	).WithTags("exec", "exec:dangerous")
}

// executeProgramExecutor runs code in a supported interpreter via the configured
// ProgramRunner. By default this is the local host runner; SetDefaultRunner can
// swap in the DockerRunner at startup.
func executeProgramExecutor(input map[string]any) (any, error) {
	language := strings.ToLower(getString(input, "language", ""))
	code := getString(input, "code", "")
	if language == "" || code == "" {
		return nil, fmt.Errorf("language and code required")
	}
	timeout := time.Duration(getInt(input, "timeout_ms", 30000)) * time.Millisecond

	if risk := checkDangerousCode(language, code); risk != "" {
		return nil, fmt.Errorf("risk pattern detected: %s", risk)
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

// dangerousPatterns maps risky keywords to a human-readable reason. These checks
// are intentionally shallow (a determined bypass is possible). Their purpose is
// to catch obvious accidental misuse by an LLM before spawning a process.
var dangerousPatterns = []struct {
	pattern string
	reason  string
}{
	// Shell execution pipelines that download and execute untrusted code.
	{`curl\s+[^|]*\|\s*(sh|bash)`, "pipe curl into shell"},
	{`wget\s+[^|]*\|\s*(sh|bash)`, "pipe wget into shell"},
	// Destructive filesystem operations.
	{`\brm\s+-rf\s+/`, "recursive remove from root"},
	{`\brm\s+-rf\s+[~\\/]`, "recursive remove home/system"},
	// Common exfiltration / backdoor helpers.
	{`\bnc\s+-[ecl]\s+`, "netcat remote shell"},
	{`\bmkfifo\b`, "fifo backdoor"},
	{`\bprivilege\s*escalation\b`, "explicit privilege escalation"},
}

// checkDangerousCode scans source code for obviously risky idioms. It returns
// an empty string when no known pattern is found. The matching is case-insensitive.
func checkDangerousCode(language, code string) string {
	lower := strings.ToLower(code)
	// Python-specific dangerous calls.
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
