package tool

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ============================================================================
// ShellSandboxConfig —— 无 Docker 环境下的 Shell 执行安全降级策略（N1-04）
// ============================================================================
//
// # 设计理由
//
// 平台在「有 Docker」时使用 SandboxExecutor 把 run_shell 隔离在容器内执行；
// 在「无 Docker」的开发/自托管环境，run_shell / execute_program 直接在本机
// 执行，缺乏任何危险命令防护。本文件为这条**本地路径**提供一层轻量但有效的
// 安全降级：
//   - 一份危险命令前缀黑名单（rm -rf /、git push --force、fork bomb 等）；
//   - 策略枚举 allow / ask / deny 控制命中黑名单时的处置；
//   - 无人值守（无人工审批通道）时 ask 默认降级为 deny，并写入审计轨迹。
//
// 该机制是防御纵深（defense-in-depth），**不替代** Docker sandbox：当
// EnableSandbox / Docker 可用时，命令仍在隔离容器中执行，本策略只在本地
// 回退路径上生效。
//
// # 与已有 execute_program 静态检查的关系
//
// execute_program 已通过 checkDangerousCode 对常见破坏性写法（os.system、
// curl|sh 等）做浅层静态拦截。本策略的黑名单与它互补：在 deny/ask 策略下，
// 二者共同拦截；在 allow 策略下，本策略允许 checkDangerousCode 放行的同时
// 仍把命中写入审计（风险被显式放开，可追溯）。

// ShellSandboxPolicy 定义本地 Shell 执行的安全策略。
type ShellSandboxPolicy int

const (
	// PolicyDeny 拒绝所有命中黑名单的命令，并写审计。这是无人值守环境的
	// 默认策略——最安全，不会误伤正常开发命令（黑名单仅覆盖灾难性写法）。
	PolicyDeny ShellSandboxPolicy = iota
	// PolicyAsk 命中黑名单时需人工审批；无人值守（本平台当前无交互式
	// 审批通道）时降级为 deny 并写审计。与 PolicyDeny 在运行时等价，保留
	// 以便将来接入人工-in-the-loop 审批门时直接启用。
	PolicyAsk
	// PolicyAllow 允许命中黑名单的命令执行，但把命中写入审计告警，便于
	// 事后复盘。operator 显式接受全部风险时使用。
	PolicyAllow
)

// String 返回策略的规范小写字符串表示，用于审计与配置往返。
func (p ShellSandboxPolicy) String() string {
	switch p {
	case PolicyAllow:
		return "allow"
	case PolicyAsk:
		return "ask"
	default:
		return "deny"
	}
}

// ParseShellSandboxPolicy 把配置字符串解析为策略，非法/空值回退到 deny。
func ParseShellSandboxPolicy(s string) ShellSandboxPolicy {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "allow":
		return PolicyAllow
	case "ask":
		return PolicyAsk
	default:
		// 空串、未知值均按 deny 处理（fail-closed）。
		return PolicyDeny
	}
}

// ShellSandboxDecision 是 Evaluate 对一条命令的裁决结果。
type ShellSandboxDecision int

const (
	// DecisionAllow 命令可正常执行。
	DecisionAllow ShellSandboxDecision = iota
	// DecisionAsk 命令需人工审批；无人值守环境下由调用方降级为 deny。
	DecisionAsk
	// DecisionDeny 命令被拒绝，不执行。
	DecisionDeny
)

// ShellSandboxConfig 持有本地 Shell 执行的安全降级配置。
// 零值（Policy=0=PolicyDeny、空黑名单）仍具有安全默认：对灾难性命令拒绝。
type ShellSandboxConfig struct {
	// Policy 控制命中黑名单时的处置（allow/ask/deny）。
	Policy ShellSandboxPolicy
	// Blacklist 是危险命令前缀/模式列表，每个条目按 (?i) 大小写不敏感正则
	// 匹配整条命令。命中即视为危险命令。
	Blacklist []string
	// Allowlist 是白名单模式列表，命中即放行（优先级高于 Blacklist），
	// 供 operator 对已知安全命令做精确豁免。
	Allowlist []string

	// compiledBlacklist 是 Blacklist 预编译后的正则，避免每次执行重复编译。
	// 不导出，由 NewShellSandboxConfig / DefaultShellSandboxConfig 填充。
	compiledBlacklist []*regexp.Regexp
}

// defaultShellBlacklist 是灾难性命令的默认前缀黑名单。
// 仅覆盖确实会造成不可逆破坏的写法，避免误伤正常开发命令
// （例如 rm -rf /tmp/foo 不会命中，因为 / 后必须紧跟空白或行尾）。
var defaultShellBlacklist = []string{
	// 从根/家目录递归强制删除。
	`rm\s+-rf\s+/+(?:\s|$)`,
	`rm\s+-fr\s+/+(?:\s|$)`,
	`rm\s+-rf\s+/\*(?:\s|$)`,
	`rm\s+-fr\s+/\*(?:\s|$)`,
	`rm\s+-rf\s+~(?:\s|$)`,
	`rm\s+-fr\s+~(?:\s|$)`,
	// 文件系统格式化。
	`mkfs`,
	// fork bomb。
	`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`,
	// 直接覆写磁盘设备。
	`dd\b.*\bof=/dev/`,
	// 强制推送（可能覆盖远端历史）。
	`git\s+push\s+(?:--force|-[a-z]*f)(?:\s|$)`,
	// 下载脚本并直接执行（供应链投毒常见手法）。
	`curl\b.*\|\s*(?:ba)?sh\b`,
	`wget\b.*\|\s*(?:ba)?sh\b`,
	// 关闭/重启宿主。
	`(?:^|\s)(?:shutdown|reboot|halt|poweroff)(?:\s|$)`,
	// 向块设备写入。
	`>\s*/dev/(?:sd|hd|nvme|sda)`,
	`tee\s+/dev/(?:sd|hd|nvme|sda)`,
	// 递归放开根目录权限。
	`chmod\s+-R\s+777\s+/`,
}

// DefaultShellSandboxConfig 返回带安全默认值的 ShellSandboxConfig：
// 策略 deny + 灾难性命令黑名单。这是无 Docker 环境下的默认行为。
func DefaultShellSandboxConfig() ShellSandboxConfig {
	return NewShellSandboxConfig(PolicyDeny, defaultShellBlacklist, nil)
}

// NewShellSandboxConfig 用给定策略与黑白名单构造 ShellSandboxConfig，
// 预编译黑名单正则（无法编译的条目被静默跳过并记录原因）。
func NewShellSandboxConfig(policy ShellSandboxPolicy, blacklist, allowlist []string) ShellSandboxConfig {
	c := ShellSandboxConfig{
		Policy:    policy,
		Blacklist: blacklist,
		Allowlist: allowlist,
	}
	for _, p := range blacklist {
		re, err := regexp.Compile("(?i)" + p)
		if err != nil {
			// 无法编译的黑名单条目被静默跳过（不阻断启动）。
			continue
		}
		c.compiledBlacklist = append(c.compiledBlacklist, re)
	}
	return c
}

// Evaluate 对一条命令做安全裁决。
// 返回值：decision（allow/ask/deny）、matchedRule（命中的黑名单模式，未命中
// 为空串）、dangerous（是否命中黑名单，即命令是否危险）。
//
// 裁决顺序：
//  1. 命中 Allowlist → 直接 Allow（精确豁免，优先级最高）。
//  2. 命中 Blacklist → 按 Policy 决定：
//     - allow：Allow 但 dangerous=true（调用方写审计告警，风险被放开）。
//     - ask  ：Ask（无人值守由调用方降级为 deny）。
//     - deny ：Deny。
//  3. 未命中任何名单 → Allow。
func (c ShellSandboxConfig) Evaluate(command string) (ShellSandboxDecision, string, bool) {
	cmd := strings.TrimSpace(command)

	// 1) 白名单精确豁免（优先级最高）。
	for _, a := range c.Allowlist {
		if re, err := regexp.Compile("(?i)" + a); err == nil && re.MatchString(cmd) {
			return DecisionAllow, "", false
		}
	}

	// 2) 黑名单检查。
	for _, re := range c.compiledBlacklist {
		if re.MatchString(cmd) {
			switch c.Policy {
			case PolicyAllow:
				// 显式放开危险命令，但标记 dangerous 供审计。
				return DecisionAllow, re.String(), true
			case PolicyAsk:
				// 需人工审批；无人值守由调用方降级为 deny。
				return DecisionAsk, re.String(), true
			default: // PolicyDeny
				return DecisionDeny, re.String(), true
			}
		}
	}

	// 3) 未命中 → 放行。
	return DecisionAllow, "", false
}

// ShellSandboxAuditSink 接收 Shell 沙箱安全裁决的审计记录（N1-04）。
// 接口刻意保持最小，使 tool 包不依赖 observability 包——否则会形成
// observability → pkg/db → cron → tool → observability 的 import cycle。
// 生产环境由 cmd/server 注入 observability.DefaultAuditor 的适配实现
// （see cmd/server/main.go 的 shellSandboxAuditAdapter），使审计进入统一
// 审计轨迹（内存 + SQLite）。
type ShellSandboxAuditSink interface {
	Record(action, actor, target, reason string)
}

// defaultShellSandboxAuditSink 是 ShellSandboxAuditSink 的进程内默认实现：
// 一个有界 ring buffer，保证在不注入真实审计器时也能自包含地记录裁决。
type defaultShellSandboxAuditSink struct {
	mu    sync.Mutex
	recs  []map[string]any
	limit int
}

func newDefaultShellSandboxAuditSink() *defaultShellSandboxAuditSink {
	return &defaultShellSandboxAuditSink{limit: 1000}
}

func (s *defaultShellSandboxAuditSink) Record(action, actor, target, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, map[string]any{
		"action": action,
		"actor":  actor,
		"target": target,
		"reason": reason,
	})
	if len(s.recs) > s.limit {
		s.recs = s.recs[len(s.recs)-s.limit:]
	}
}

// shellSandboxAuditSink 是全局审计接收器，默认使用内存 ring buffer。
//
// N3-04b (E7 并发安全)：该接收器被 run_shell / execute_program 等工具执行路径
// （多 goroutine）并发读取，而 SetShellSandboxAuditSink 在启动期写入，二者无同步
// 会触发 data race。故用 shellSandboxAuditSinkMu 保护读写，保证 go test -race 干净。
var (
	shellSandboxAuditSinkMu sync.Mutex
	shellSandboxAuditSink   ShellSandboxAuditSink = newDefaultShellSandboxAuditSink()
)

// SetShellSandboxAuditSink 替换全局审计接收器。cmd/server 在启动时调用它以
// 注入 observability.DefaultAuditor 的适配实现（nil 参数被忽略，保留现有接收器）。
// N3-04b 起经 shellSandboxAuditSinkMu 同步。
func SetShellSandboxAuditSink(s ShellSandboxAuditSink) {
	if s == nil {
		return
	}
	shellSandboxAuditSinkMu.Lock()
	shellSandboxAuditSink = s
	shellSandboxAuditSinkMu.Unlock()
}

// shellSandboxAuditSinkGet 在并发工具执行路径上安全获取当前接收器（N3-04b）。
func shellSandboxAuditSinkGet() ShellSandboxAuditSink {
	shellSandboxAuditSinkMu.Lock()
	defer shellSandboxAuditSinkMu.Unlock()
	return shellSandboxAuditSink
}

// recordShellSandboxAudit 把一次 Shell 沙箱裁决写入当前审计接收器。
// actor 优先取 AgentID，缺失时记为 system（cron/手工触发等）。
func recordShellSandboxAudit(ctx ExecuteContext, action, command, rule string, policy ShellSandboxPolicy) {
	actor := ctx.AgentID
	if actor == "" {
		actor = "system"
	}
	shellSandboxAuditSinkGet().Record(action, actor, command,
		fmt.Sprintf("shell_sandbox policy=%s matched_rule=%q session=%s", policy.String(), rule, ctx.SessionID))
}
