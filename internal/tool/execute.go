package tool

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// NewExecuteProgramTool creates a code execution tool named "core/execute_program".
//
// Parameters:
//   - language   (string,  required): One of python, node, bash.
//   - code       (string,  required): Code to execute.
//   - timeout_ms (integer, optional): Timeout in milliseconds (default 30000).
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
	).WithTags("exec", "dangerous")
}

// executeProgramExecutor runs code in a supported interpreter.
func executeProgramExecutor(input map[string]any) (any, error) {
	language := strings.ToLower(getString(input, "language", ""))
	code := getString(input, "code", "")
	if language == "" || code == "" {
		return nil, fmt.Errorf("language and code required")
	}
	timeout := time.Duration(getInt(input, "timeout_ms", 30000)) * time.Millisecond

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
