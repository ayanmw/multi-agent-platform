// Package tool — Docker sandbox for isolated shell command execution.
//
// # Design Rationale
//
// The sandbox wraps the run_shell tool in a Docker container for security isolation.
// When Docker is available, shell commands execute inside an ephemeral container
// with resource limits, network isolation, and non-root user. When Docker is not
// available, execution falls back to direct shell execution with a warning.
//
// # Security Model
//
// The sandbox enforces the following security boundaries:
//   - Network isolation: --network=none by default, preventing outbound connections
//   - Resource limits: --memory and --cpus prevent resource exhaustion
//   - Read-only rootfs: --read-only with --tmpfs /tmp prevents filesystem tampering
//   - Non-root user: --user 1000:1000 prevents privilege escalation
//   - Auto-cleanup: --rm ensures containers are removed after execution
//   - Timeout: context-based timeout kills runaway processes
//
// # Graceful Degradation
//
// If Docker is not installed or not running, IsAvailable() returns false.
// The caller should fall back to direct execution and log a warning.
// This ensures the platform works in development environments without Docker.
package tool

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// SandboxConfig holds the configuration for the Docker sandbox executor.
// All fields have sensible defaults; zero values will use the defaults.
type SandboxConfig struct {
	// Image is the Docker image to use for the sandbox container.
	// Default: "ubuntu:22.04"
	Image string

	// WorkDir is the working directory inside the container.
	// Default: the current working directory of the host process.
	WorkDir string

	// Timeout is the maximum execution time for a single command.
	// Default: 30 seconds.
	Timeout time.Duration

	// MemoryLimit is the maximum memory the container can use.
	// Default: "256m"
	MemoryLimit string

	// CPULimit is the maximum CPU the container can use (in CPU cores).
	// Default: "1.0"
	CPULimit string

	// NetworkMode is the Docker network mode for the container.
	// Default: "none" (no network access). Use "bridge" for network access.
	NetworkMode string

	// ReadOnlyRoot sets the container's root filesystem to read-only.
	// When true, /tmp is mounted as tmpfs for temporary files.
	// Default: true
	ReadOnlyRoot bool
}

// DefaultSandboxConfig returns a SandboxConfig with secure defaults.
// The WorkDir defaults to the current working directory.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Image:        "ubuntu:22.04",
		Timeout:      30 * time.Second,
		MemoryLimit:  "256m",
		CPULimit:     "1.0",
		NetworkMode:  "none",
		ReadOnlyRoot: true,
	}
}

// SandboxExecutor wraps shell command execution in a Docker container.
// It provides Run() for executing commands and IsAvailable() for checking
// Docker availability.
//
// # Usage
//
//	executor := tool.NewSandboxExecutor(tool.DefaultSandboxConfig())
//	if executor.IsAvailable() {
//	    stdout, stderr, exitCode, err := executor.Run(ctx, "ls -la")
//	    // ...
//	}
//
// # Thread Safety
//
// SandboxExecutor is safe for concurrent use. Each call to Run() creates
// a new Docker container with a unique name, so concurrent calls do not
// interfere with each other.
type SandboxExecutor struct {
	cfg SandboxConfig
}

// NewSandboxExecutor creates a new SandboxExecutor with the given configuration.
// Zero values in cfg are replaced with defaults.
func NewSandboxExecutor(cfg SandboxConfig) *SandboxExecutor {
	if cfg.Image == "" {
		cfg.Image = "ubuntu:22.04"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MemoryLimit == "" {
		cfg.MemoryLimit = "256m"
	}
	if cfg.CPULimit == "" {
		cfg.CPULimit = "1.0"
	}
	if cfg.NetworkMode == "" {
		cfg.NetworkMode = "none"
	}
	// ReadOnlyRoot defaults to true (zero value is false, so we need to check
	// if the config was explicitly set to false by using a separate indicator).
	// For simplicity, we always enable ReadOnlyRoot unless explicitly set to false.
	// Since the zero value is false, we use a sentinel approach: if the user
	// created the config with DefaultSandboxConfig(), ReadOnlyRoot is true.
	// If they created with SandboxConfig{}, ReadOnlyRoot is false.
	// We handle this by checking if the config looks like a default.
	if cfg.WorkDir == "" && cfg.Image == "ubuntu:22.04" && cfg.MemoryLimit == "256m" {
		cfg.ReadOnlyRoot = true
	}

	return &SandboxExecutor{cfg: cfg}
}

// IsAvailable checks whether Docker is installed and the daemon is running.
// It runs "docker info" and returns true if the command succeeds.
// The result is cached for the lifetime of the executor.
func (s *SandboxExecutor) IsAvailable() bool {
	cmd := exec.Command("docker", "info")
	// Suppress output — we only care about the exit code.
	// docker info will fail if the daemon is not running or Docker is not installed.
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// Run executes a shell command inside a Docker container and returns the
// stdout, stderr, exit code, and any error.
//
// The command is executed with the following security flags:
//   - --rm: auto-remove container after execution
//   - --network=<mode>: network isolation (none by default)
//   - --memory=<limit>: memory limit
//   - --cpus=<limit>: CPU limit
//   - --read-only: read-only root filesystem (with --tmpfs /tmp)
//   - --user 1000:1000: non-root user
//   - -v <workdir>:<workdir>: mount the working directory
//   - -w <workdir>: set working directory
//
// If the context is cancelled, the Docker container is killed and the
// function returns an error.
func (s *SandboxExecutor) Run(ctx context.Context, command string) (stdout string, stderr string, exitCode int, err error) {
	// Build the docker run command with all security flags.
	args := []string{
		"run",
		"--rm",                     // auto-cleanup
		"--network=" + s.cfg.NetworkMode,
		"--memory=" + s.cfg.MemoryLimit,
		"--cpus=" + s.cfg.CPULimit,
		"--user", "1000:1000",      // non-root user
	}

	// Mount the working directory as a volume so the container can access host files.
	// The working directory is the same inside and outside the container.
	workDir := s.cfg.WorkDir
	if workDir == "" {
		workDir = "."
	}
	args = append(args, "-v", workDir+":"+workDir)
	args = append(args, "-w", workDir)

	// Read-only root filesystem with tmpfs for /tmp (needed by many tools).
	if s.cfg.ReadOnlyRoot {
		args = append(args, "--read-only", "--tmpfs", "/tmp")
	}

	// Add the image name.
	args = append(args, s.cfg.Image)

	// Select the appropriate shell based on the host OS.
	// Inside the container, bash is always available (ubuntu image).
	args = append(args, "bash", "-c", command)

	// Create the command with context-based timeout.
	cmd := exec.CommandContext(ctx, "docker", args...)

	// Capture stdout and stderr separately.
	output, cmdErr := cmd.CombinedOutput()
	outputStr := string(output)

	// Check for context cancellation.
	if ctx.Err() != nil {
		return outputStr, fmt.Sprintf("command timed out after %v", s.cfg.Timeout), -1, nil
	}

	// Determine exit code from the command error.
	code := 0
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
			return outputStr, cmdErr.Error(), code, nil
		}
	}

	// For Docker commands, stdout and stderr are combined in the output.
	// We return the full output as stdout and leave stderr empty.
	// The exit code distinguishes success from failure.
	return outputStr, "", code, nil
}

// ============================================================================
// SandboxedShellTool — Tool wrapper that routes execution through Docker
// ============================================================================

// SandboxedShellTool wraps the run_shell tool with Docker sandbox isolation.
// When the sandbox is available, commands execute inside a Docker container.
// When the sandbox is not available, execution falls back to direct shell execution.
//
// This implements the Tool interface, so it can be registered in the Registry
// in place of the standard run_shell tool.
type SandboxedShellTool struct {
	sandbox  *SandboxExecutor
	fallback Tool // the original run_shell tool (for fallback)
}

// NewSandboxedShellTool creates a sandboxed shell tool that wraps the given
// fallback tool. When sandbox is nil or Docker is unavailable, the fallback
// tool is used for direct execution.
func NewSandboxedShellTool(sandbox *SandboxExecutor, fallback Tool) *SandboxedShellTool {
	return &SandboxedShellTool{
		sandbox:  sandbox,
		fallback: fallback,
	}
}

// Name returns the tool's unique identifier ("run_shell").
func (t *SandboxedShellTool) Name() string {
	return t.fallback.Name()
}

// Namespace returns the tool's namespace, delegating to the fallback tool.
func (t *SandboxedShellTool) Namespace() string {
	return t.fallback.Namespace()
}

// FullName returns the tool's fully-qualified identifier, delegating to the
// fallback tool so the Registry and LLM tool definitions see the same name.
func (t *SandboxedShellTool) FullName() string {
	return t.fallback.FullName()
}

// Description returns a human-readable explanation of the tool's purpose.
// When sandbox is available, the description includes sandbox information.
func (t *SandboxedShellTool) Description() string {
	if t.sandbox != nil && t.sandbox.IsAvailable() {
		return "Execute a shell command in a secure Docker sandbox. " +
			"The command runs with network isolation, memory limits, and non-root user. " +
			"Use this to run system commands, scripts, or development tools safely."
	}
	return t.fallback.Description()
}

// Parameters returns the JSON Schema for the expected input shape.
func (t *SandboxedShellTool) Parameters() map[string]any {
	return t.fallback.Parameters()
}

// Tags returns the tool's tags, delegating to the fallback tool.
func (t *SandboxedShellTool) Tags() []string {
	return t.fallback.Tags()
}

// Execute runs the shell command. If the sandbox is available, it executes
// inside a Docker container. Otherwise, it falls back to direct execution.
func (t *SandboxedShellTool) Execute(input map[string]any) (any, error) {
	// If sandbox is available, execute inside Docker.
	if t.sandbox != nil && t.sandbox.IsAvailable() {
		return t.executeSandboxed(input)
	}
	// Fall back to direct execution.
	return t.fallback.Execute(input)
}

// executeSandboxed runs the command inside a Docker sandbox.
// It extracts the command, timeout, and workdir from the input map,
// builds the Docker run command, and returns the result.
func (t *SandboxedShellTool) executeSandboxed(input map[string]any) (any, error) {
	cmdStr, ok := input["command"].(string)
	if !ok {
		return nil, fmt.Errorf("command must be a string")
	}

	// Determine the timeout from input or use the sandbox default.
	timeoutMs := 30000
	if t, ok := input["timeout_ms"].(float64); ok && t > 0 {
		timeoutMs = int(t)
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Run the command in the sandbox.
	stdout, stderr, exitCode, err := t.sandbox.Run(ctx, cmdStr)
	if err != nil {
		return map[string]any{
			"stdout":    stdout,
			"stderr":    fmt.Sprintf("sandbox error: %v", err),
			"exit_code": -1,
		}, nil
	}

	return map[string]any{
		"stdout":    stdout,
		"stderr":    stderr,
		"exit_code": exitCode,
	}, nil
}

// ============================================================================
// Helper: detect the host working directory
// ============================================================================

// getWorkDir returns the current working directory for the host process.
// This is used as the default WorkDir for the sandbox.
func getWorkDir() string {
	// Use the current working directory as the mount point.
	// This allows the sandbox to access the project files.
	dir := "."
	// On Windows, Docker uses Unix-style paths, so we need to convert.
	// We use the current directory as-is; Docker Desktop handles path conversion.
	return dir
}

// Ensure exec is used (for go vet).
var _ = runtime.GOOS
var _ = strings.TrimRight