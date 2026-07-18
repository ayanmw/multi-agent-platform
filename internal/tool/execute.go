package tool

import (
	"context"
	"fmt"
	"os/exec"
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

// executeProgramExecutor runs code in a supported interpreter.
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

	var cmdArgs []string
	switch language {
	case "python":
		for _, candidate := range []string{"python", "python3", "py"} {
			if _, err := exec.LookPath(candidate); err == nil {
				cmdArgs = []string{candidate, "-c", code}
				break
			}
		}
		if len(cmdArgs) == 0 {
			return nil, fmt.Errorf("python interpreter not found")
		}
	case "node":
		cmdArgs = []string{"node", "-e", code}
	case "bash":
		cmdArgs = []string{"bash", "-c", code}
	case "go":
		return nil, fmt.Errorf("go execution not yet supported in sandbox")
	default:
		return nil, fmt.Errorf("unsupported language: %s", language)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return map[string]any{
			"stdout":    string(out),
			"stderr":    "",
			"exit_code": -1,
			"timed_out": true,
		}, nil
	}

	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return map[string]any{
		"stdout":    string(out),
		"stderr":    "",
		"exit_code": exitCode,
		"timed_out": false,
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
