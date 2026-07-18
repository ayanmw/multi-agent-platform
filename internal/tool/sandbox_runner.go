package tool

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// ProgramRunner abstracts how execute_program runs code. Implementations can
// execute locally (the default for backward compatibility) or inside a sandbox
// such as Docker. The abstraction lets Phase 5 introduce sandboxing without
// rewriting the executor.
type ProgramRunner interface {
	// Run executes the given program source code with a timeout. It returns
	// stdout combined with stderr, the exit code, and any execution error.
	Run(ctx context.Context, language, code string, timeout time.Duration) (output string, exitCode int, err error)
}

// LocalRunner executes code in a supported interpreter on the host machine.
// It is the default runner and matches the pre-sandbox behavior.
type LocalRunner struct{}

// NewLocalRunner creates a LocalRunner.
func NewLocalRunner() *LocalRunner { return &LocalRunner{} }

// Run executes the code locally using python, node, or bash.
func (r *LocalRunner) Run(ctx context.Context, language, code string, timeout time.Duration) (string, int, error) {
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
			return "", -1, fmt.Errorf("python interpreter not found")
		}
	case "node":
		cmdArgs = []string{"node", "-e", code}
	case "bash":
		cmdArgs = []string{"bash", "-c", code}
	default:
		return "", -1, fmt.Errorf("unsupported language: %s", language)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return string(out), -1, context.DeadlineExceeded
	}

	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return string(out), exitCode, err
}

// DockerRunner executes code inside a short-lived Docker container.
// It disables network access and mounts the filesystem read-only by default.
// Only python, node, and bash are supported via their respective official images.
type DockerRunner struct {
	Image string
}

// NewDockerRunner creates a DockerRunner with the given image.
func NewDockerRunner(image string) *DockerRunner {
	return &DockerRunner{Image: image}
}

// Run executes the code inside a Docker container using the interpreter image
// selected by language. The container is run with --rm -i --network none
// and a read-only root filesystem.
func (r *DockerRunner) Run(ctx context.Context, language, code string, timeout time.Duration) (string, int, error) {
	image := r.imageFor(language)
	if image == "" {
		return "", -1, fmt.Errorf("unsupported language for docker sandbox: %s", language)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i",
		"--network", "none",
		"--read-only",
		image,
		r.interpreterFor(language),
		"-c", code,
	)
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return string(out), -1, nil
	}

	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return string(out), exitCode, err
}

func (r *DockerRunner) imageFor(language string) string {
	switch language {
	case "python":
		if r.Image != "" {
			return r.Image
		}
		return "python:3.11-slim"
	case "node":
		return "node:20-slim"
	case "bash":
		return "bash:5.2"
	default:
		return ""
	}
}

func (r *DockerRunner) interpreterFor(language string) string {
	switch language {
	case "python":
		return "python"
	case "node":
		return "node"
	case "bash":
		return "bash"
	default:
		return ""
	}
}

// defaultRunner is the package-level ProgramRunner used by execute_program.
// It defaults to local execution and may be replaced at startup when sandbox is
// enabled.
var defaultRunner ProgramRunner = NewLocalRunner()

// SetDefaultRunner replaces the runner used by execute_program.
func SetDefaultRunner(runner ProgramRunner) {
	defaultRunner = runner
}

// GetDefaultRunner returns the currently configured ProgramRunner.
func GetDefaultRunner() ProgramRunner {
	return defaultRunner
}
