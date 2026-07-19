package tool

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// ProgramRunner 抽象了 execute_program 运行代码的方式。实现可以在本地执行
// （为向后兼容的默认行为）或在 sandbox（如 Docker）中执行。该抽象使
// Phase 5 可以在不重写 executor 的情况下引入 sandbox。
type ProgramRunner interface {
	// Run 以给定超时执行源代码，返回 stdout 与 stderr 合并的输出、
	// exit code 以及可能的执行错误。
	Run(ctx context.Context, language, code string, timeout time.Duration) (output string, exitCode int, err error)
}

// LocalRunner 在宿主机器上以受支持的解释器执行代码。它是默认 runner，
// 与引入 sandbox 之前的行为保持一致。
type LocalRunner struct{}

// NewLocalRunner 创建一个 LocalRunner。
func NewLocalRunner() *LocalRunner { return &LocalRunner{} }

// Run 在本地使用 python、node 或 bash 执行代码。
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

// DockerRunner 在短生命周期的 Docker 容器内执行代码。默认禁用网络访问，
// 并以只读方式挂载文件系统。仅支持 python、node 与 bash，分别使用其官方镜像。
type DockerRunner struct {
	Image string
}

// NewDockerRunner 以给定镜像创建一个 DockerRunner。
func NewDockerRunner(image string) *DockerRunner {
	return &DockerRunner{Image: image}
}

// Run 在 Docker 容器内执行代码，容器使用按 language 选择的解释器镜像。
// 容器以 --rm -i --network none 与只读根文件系统运行。
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

// defaultRunner 是 execute_program 使用的包级 ProgramRunner。默认为本地
// 执行，可在启动时启用 sandbox 时被替换。
var defaultRunner ProgramRunner = NewLocalRunner()

// SetDefaultRunner 替换 execute_program 使用的 runner。
func SetDefaultRunner(runner ProgramRunner) {
	defaultRunner = runner
}

// GetDefaultRunner 返回当前配置的 ProgramRunner。
func GetDefaultRunner() ProgramRunner {
	return defaultRunner
}
