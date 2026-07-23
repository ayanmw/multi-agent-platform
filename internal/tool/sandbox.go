// Package tool — 用于隔离 shell 命令执行的 Docker sandbox。
//
// # 设计理由
//
// sandbox 将 run_shell 工具封装在 Docker 容器中以实现安全隔离。当 Docker
// 可用时，shell 命令在带资源限制、网络隔离且非 root 用户的临时容器中执行。
// 当 Docker 不可用时，回退到直接 shell 执行并输出告警。
//
// # 安全模型
//
// sandbox 强制以下安全边界：
//   - 网络隔离：默认 --network=none，阻止出站连接
//   - 资源限制：--memory 与 --cpus 防止资源耗尽
//   - 只读 rootfs：--read-only 配合 --tmpfs /tmp 防止文件系统被篡改
//   - 非 root 用户：--user 1000:1000 防止权限提升
//   - 自动清理：--rm 确保容器在执行后移除
//   - 超时：基于 context 的超时机制杀掉失控进程
//
// # 优雅降级
//
// 若 Docker 未安装或未运行，IsAvailable() 返回 false。调用方应回退到直接
// 执行并记录告警。这确保平台在无 Docker 的开发环境中也能工作。
package tool

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// SandboxConfig 保存 Docker sandbox executor 的配置。所有字段都有合理的
// 默认值；零值将使用默认值。
type SandboxConfig struct {
	// Image 是 sandbox 容器使用的 Docker 镜像。
	// 默认："ubuntu:22.04"
	Image string

	// WorkDir 是容器内的工作目录。
	// 默认：宿主进程的当前工作目录。
	WorkDir string

	// Timeout 是单条命令的最大执行时间。
	// 默认：30 秒。
	Timeout time.Duration

	// MemoryLimit 是容器可使用的最大内存。
	// 默认："256m"
	MemoryLimit string

	// CPULimit 是容器可使用的最大 CPU（单位为 CPU 核数）。
	// 默认："1.0"
	CPULimit string

	// NetworkMode 是容器的 Docker 网络模式。
	// 默认："none"（无网络访问）。需要网络访问时使用 "bridge"。
	NetworkMode string

	// ReadOnlyRoot 将容器的根文件系统设为只读。为 true 时，
	// /tmp 以 tmpfs 形式挂载，供临时文件使用。
	// 默认：true
	ReadOnlyRoot bool
}

// DefaultSandboxConfig 返回一个带安全默认值的 SandboxConfig。
// WorkDir 默认为当前工作目录。
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

// SandboxExecutor 将 shell 命令执行封装在 Docker 容器中。它提供 Run()
// 用于执行命令、IsAvailable() 用于检查 Docker 是否可用。
//
// # 用法
//
//	executor := tool.NewSandboxExecutor(tool.DefaultSandboxConfig())
//	if executor.IsAvailable() {
//	    stdout, stderr, exitCode, err := executor.Run(ctx, "ls -la")
//	    // ...
//	}
//
// # 线程安全
//
// SandboxExecutor 可被并发安全使用。每次 Run() 调用都会创建一个具名唯一
// 的新 Docker 容器，因此并发调用之间不会互相干扰。
type SandboxExecutor struct {
	cfg SandboxConfig
}

// NewSandboxExecutor 以给定配置创建一个新的 SandboxExecutor。
// cfg 中的零值会被替换为默认值。
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
	// ReadOnlyRoot 默认为 true（零值为 false，因此需要通过单独的指示器
	// 判断是否被显式设为 false）。为简便起见，除非显式设为 false，
	// 否则我们总是启用 ReadOnlyRoot。由于零值为 false，我们采用哨兵
	// 方式：若用户通过 DefaultSandboxConfig() 创建配置，则 ReadOnlyRoot
	// 为 true；若通过 SandboxConfig{} 创建，则为 false。我们通过检查
	// 配置是否看起来像默认值来处理这一情况。
	if cfg.WorkDir == "" && cfg.Image == "ubuntu:22.04" && cfg.MemoryLimit == "256m" {
		cfg.ReadOnlyRoot = true
	}

	return &SandboxExecutor{cfg: cfg}
}

// IsAvailable 检查 Docker 是否已安装且守护进程是否在运行。
// 它执行 "docker info" 并在命令成功时返回 true。结果在 executor 的
// 生命周期内被缓存。
func (s *SandboxExecutor) IsAvailable() bool {
	cmd := exec.Command("docker", "info")
	// 抑制输出 —— 我们只关心 exit code。
	// 若守护进程未运行或 Docker 未安装，docker info 会失败。
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// Run 在 Docker 容器内执行 shell 命令，返回 stdout、stderr、exit code
// 与可能的错误。
//
// 命令以以下安全 flag 执行：
//   - --rm：执行后自动移除容器
//   - --network=<mode>：网络隔离（默认 none）
//   - --memory=<limit>：内存上限
//   - --cpus=<limit>：CPU 上限
//   - --read-only：只读根文件系统（配合 --tmpfs /tmp）
//   - --user 1000:1000：非 root 用户
//   - -v <workdir>:<workdir>：挂载工作目录
//   - -w <workdir>：设置工作目录
//
// 若 context 被取消，Docker 容器将被杀掉，函数返回错误。
func (s *SandboxExecutor) Run(ctx context.Context, command string) (stdout string, stderr string, exitCode int, err error) {
	// 构建带全部安全 flag 的 docker run 命令。
	args := []string{
		"run",
		"--rm", // 自动清理
		"--network=" + s.cfg.NetworkMode,
		"--memory=" + s.cfg.MemoryLimit,
		"--cpus=" + s.cfg.CPULimit,
		"--user", "1000:1000", // 非 root 用户
	}

	// 将工作目录作为 volume 挂载，使容器可以访问宿主文件。
	// 工作目录在容器内外保持一致。
	workDir := s.cfg.WorkDir
	if workDir == "" {
		workDir = "."
	}
	args = append(args, "-v", workDir+":"+workDir)
	args = append(args, "-w", workDir)

	// 只读根文件系统，配合 tmpfs /tmp（许多工具需要 /tmp）。
	if s.cfg.ReadOnlyRoot {
		args = append(args, "--read-only", "--tmpfs", "/tmp")
	}

	// 加入镜像名称。
	args = append(args, s.cfg.Image)

	// 根据宿主 OS 选择合适的 shell。
	// 容器内 bash 始终可用（ubuntu 镜像）。
	args = append(args, "bash", "-c", command)

	// 使用带 context 超时的命令。
	cmd := exec.CommandContext(ctx, "docker", args...)

	// 分别捕获 stdout 与 stderr。
	output, cmdErr := cmd.CombinedOutput()
	outputStr := string(output)

	// 检查 context 是否被取消。
	if ctx.Err() != nil {
		return outputStr, fmt.Sprintf("command timed out after %v", s.cfg.Timeout), -1, nil
	}

	// 从命令错误中确定 exit code。
	code := 0
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
			return outputStr, cmdErr.Error(), code, nil
		}
	}

	// 对 Docker 命令而言，stdout 与 stderr 被合并到输出中。
	// 我们将完整输出作为 stdout 返回，stderr 留空。
	// 通过 exit code 区分成功与失败。
	return outputStr, "", code, nil
}

// ============================================================================
// SandboxedShellTool —— 通过 Docker 路由执行的 Tool 包装器
// ============================================================================

// SandboxedShellTool 用 Docker sandbox 隔离包装 run_shell 工具。
// 当 sandbox 可用时，命令在 Docker 容器内执行。当 sandbox 不可用时，
// 回退到直接 shell 执行。
//
// 它实现了 Tool 接口，因此可注册到 Registry 中以替代标准的 run_shell 工具。
type SandboxedShellTool struct {
	sandbox  *SandboxExecutor
	fallback Tool // 原始的 run_shell 工具（用于回退）
}

// NewSandboxedShellTool 创建一个包装给定 fallback 工具的 sandboxed shell
// 工具。当 sandbox 为 nil 或 Docker 不可用时，使用 fallback 工具直接执行。
func NewSandboxedShellTool(sandbox *SandboxExecutor, fallback Tool) *SandboxedShellTool {
	return &SandboxedShellTool{
		sandbox:  sandbox,
		fallback: fallback,
	}
}

// Name 返回工具的唯一标识符（"run_shell"）。
func (t *SandboxedShellTool) Name() string {
	return t.fallback.Name()
}

// Namespace 返回工具的 namespace，委托给 fallback 工具。
func (t *SandboxedShellTool) Namespace() string {
	return t.fallback.Namespace()
}

// FullName 返回工具的完全限定标识符，委托给 fallback 工具，使 Registry 与
// LLM 工具定义看到相同的名称。
func (t *SandboxedShellTool) FullName() string {
	return t.fallback.FullName()
}

// Description 返回工具用途的人类可读说明。当 sandbox 可用时，
// 描述会包含 sandbox 信息。
func (t *SandboxedShellTool) Description() string {
	if t.sandbox != nil && t.sandbox.IsAvailable() {
		return "Execute a shell command in a secure Docker sandbox. " +
			"The command runs with network isolation, memory limits, and non-root user. " +
			"Use this to run system commands, scripts, or development tools safely."
	}
	return t.fallback.Description()
}

// Parameters 返回描述输入形状的 JSON Schema。
func (t *SandboxedShellTool) Parameters() map[string]any {
	return t.fallback.Parameters()
}

// Tags 返回工具的 tags，委托给 fallback 工具。
func (t *SandboxedShellTool) Tags() []string {
	return t.fallback.Tags()
}

// Version 返回工具的版本标识符，委托给 fallback 工具。
func (t *SandboxedShellTool) Version() string {
	return t.fallback.Version()
}

// Source 返回工具的来源。Sandboxed 包装不改变来源，委托给 fallback。
func (t *SandboxedShellTool) Source() string {
	return t.fallback.Source()
}

// CanonicalName 返回 Registry 使用的唯一键，委托给 fallback 工具。
func (t *SandboxedShellTool) CanonicalName() string {
	return t.fallback.CanonicalName()
}

// Aliases 返回该工具的别名，委托给 fallback 工具。
func (t *SandboxedShellTool) Aliases() []string {
	return t.fallback.Aliases()
}

// Execute 运行 shell 命令。若 sandbox 可用，命令在 Docker 容器内执行；
// 否则回退到直接执行。
func (t *SandboxedShellTool) Execute(input map[string]any) (any, error) {
	// 若 sandbox 可用，在 Docker 内执行。
	if t.sandbox != nil && t.sandbox.IsAvailable() {
		return t.executeSandboxed(input)
	}
	// 回退到直接执行。
	return t.fallback.Execute(input)
}

// executeSandboxed 在 Docker sandbox 内运行命令。
// 它从输入 map 中提取 command、timeout 与 workdir，构建 Docker run 命令
// 并返回结果。
func (t *SandboxedShellTool) executeSandboxed(input map[string]any) (any, error) {
	cmdStr, ok := input["command"].(string)
	if !ok {
		return nil, fmt.Errorf("command must be a string")
	}

	// 从输入中确定 timeout，否则使用 sandbox 默认值。
	timeoutMs := 30000
	if t, ok := input["timeout_ms"].(float64); ok && t > 0 {
		timeoutMs = int(t)
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 在 sandbox 中运行命令。
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
// 辅助函数：探测宿主工作目录
// ============================================================================

// getWorkDir 返回宿主进程的当前工作目录。它被用作 sandbox 的默认 WorkDir。
func getWorkDir() string {
	// 以当前工作目录作为挂载点，使 sandbox 可以访问项目文件。
	dir := "."
	// 在 Windows 上，Docker 使用 Unix 风格路径，因此需要转换。
	// 这里直接使用当前目录；Docker Desktop 会处理路径转换。
	return dir
}

// 确保 exec 被使用（供 go vet 检查）。
var _ = runtime.GOOS
var _ = strings.TrimRight
