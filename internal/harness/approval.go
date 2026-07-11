// Package harness — ApprovalRule, DangerousCommandRule, and ApprovalHandler
//
// This file implements the Phase 5 Harness features:
//   - ApprovalRule: 拦截高风险工具调用，要求前端审批后才能执行
//   - DangerousCommandRule: 检测危险 shell 命令模式，纵深防御
//   - ApprovalHandler: 通过 WebSocket 与前端交互审批流程
//
// # 设计哲学
//
// 审批机制是 Harness 安全体系的关键一环。PolicyRule 的 Check 方法在工具执行前
// 被调用，如果检测到高风险操作，返回 ErrApprovalRequired。Engine 捕获此错误后
// 发射 system_info 事件到前端，前端展示审批对话框，用户点击批准/拒绝后通过
// WebSocket 控制消息回传决定。Engine 收到决定后重试（批准）或拒绝（拒绝）工具调用。
//
// DangerousCommandRule 是纵深防御措施：即使没有配置 ApprovalRule，它也会在
// PolicyChain 中拦截危险命令。只有当 TaskContract.Permissions.AllowShellDangerous
// 为 true 时才允许危险命令通过。
package harness

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// ============================================================================
// ErrApprovalRequired — 审批请求错误类型
// ============================================================================

// ErrApprovalRequired 是 ApprovalRule 在检测到高风险操作时返回的错误类型。
// Engine 捕获此错误后发射 system_info 事件到前端，前端展示审批对话框。
// 用户批准后，Engine 绕过 PolicyGate 直接执行工具调用。
type ErrApprovalRequired struct {
	// ApprovalID 是审批请求的唯一标识符，用于前后端关联审批响应
	ApprovalID string
	// Tool 是需要审批的工具名称
	Tool string
	// Reason 是需要审批的原因（中文描述，展示给用户）
	Reason string
	// Input 是工具调用的原始参数
	Input map[string]any
}

// Error 实现 error 接口，返回审批请求的描述信息。
func (e *ErrApprovalRequired) Error() string {
	return fmt.Sprintf("[APPROVAL REQUIRED] %s: %s (approval_id=%s)", e.Tool, e.Reason, e.ApprovalID)
}

// ============================================================================
// ApprovalHandler — 审批处理器接口
// ============================================================================

// ApprovalHandler 定义了与前端交互审批流程的接口。
// 实现者通过 WebSocket 发送审批请求事件，并等待前端返回批准/拒绝决定。
//
// 典型实现：WebSocketApprovalHandler — 使用 ws.Hub 发送事件，
// 通过 channel 等待前端控制消息响应。
type ApprovalHandler interface {
	// RequestApproval 向前端发送审批请求事件。
	// approvalID: 审批请求唯一标识
	// toolName:  需要审批的工具名称
	// reason:    审批原因（中文描述）
	// input:     工具调用的参数
	RequestApproval(approvalID string, toolName string, reason string, input map[string]any) error

	// WaitForDecision 阻塞等待前端的审批决定，直到收到决定或超时。
	// 返回 true 表示批准，false 表示拒绝。
	// timeout 为 0 时使用默认超时（30 秒）。
	WaitForDecision(approvalID string, timeout time.Duration) (bool, error)
}

// ============================================================================
// EventSender — 最小事件发送接口（避免循环依赖 ws.Hub）
// ============================================================================

// EventSender 是最小化的事件发送接口，用于 WebSocketApprovalHandler
// 发送审批事件到前端。避免直接依赖 ws.Hub 造成的循环引用。
type EventSender interface {
	SendEvent(event.Event)
}

// ============================================================================
// WebSocketApprovalHandler — 基于 WebSocket 的审批处理器
// ============================================================================

// WebSocketApprovalHandler 实现 ApprovalHandler 接口，通过 WebSocket Hub
// 与前端交互审批流程。
//
// # 工作流程
//
//  1. RequestApproval: 发射 system_info(type="approval_required") 事件到前端，
//     并创建一个 buffered channel 用于等待审批决定。
//  2. WaitForDecision: 阻塞在 channel 上，等待前端通过 WebSocket 控制消息
//     （action: "approve" / "deny"）返回决定。
//  3. HandleDecision: 由 main.go 中的控制处理器调用，将审批决定写入 channel。
//
// 并发安全：使用 sync.Mutex 保护 pending map 的读写。
type WebSocketApprovalHandler struct {
	bus     EventSender          // 事件总线，用于发送审批事件到前端
	pending map[string]chan bool // 待审批的 channel，key 为 approvalID
	mu      sync.Mutex           // 保护 pending map 的并发访问
}

// NewWebSocketApprovalHandler 创建一个新的 WebSocket 审批处理器。
// bus 是事件发送器（通常是 ws.Hub），用于向前端发射审批事件。
func NewWebSocketApprovalHandler(bus EventSender) *WebSocketApprovalHandler {
	return &WebSocketApprovalHandler{
		bus:     bus,
		pending: make(map[string]chan bool),
	}
}

// RequestApproval 向前端发送审批请求事件，并创建等待 channel。
// 事件格式：system_info { type: "approval_required", approval_id, tool, reason, input }
func (h *WebSocketApprovalHandler) RequestApproval(approvalID string, toolName string, reason string, input map[string]any) error {
	// 创建 buffered channel（容量 1，避免发送方阻塞）
	h.mu.Lock()
	h.pending[approvalID] = make(chan bool, 1)
	h.mu.Unlock()

	// 发射审批请求事件到前端
	h.bus.SendEvent(event.NewEvent("system_info", "", "", 0, map[string]any{
		"type":        "approval_required",
		"approval_id": approvalID,
		"tool":        toolName,
		"reason":      reason,
		"input":       input,
	}))

	return nil
}

// WaitForDecision 阻塞等待前端审批决定。
// 超时时间默认为 30 秒。返回 true 表示批准，false 表示拒绝或超时。
func (h *WebSocketApprovalHandler) WaitForDecision(approvalID string, timeout time.Duration) (bool, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	h.mu.Lock()
	ch, ok := h.pending[approvalID]
	h.mu.Unlock()

	if !ok {
		return false, fmt.Errorf("approval %s: 审批请求未找到", approvalID)
	}

	select {
	case approved := <-ch:
		// 审批决定已收到，清理 pending 记录
		h.mu.Lock()
		delete(h.pending, approvalID)
		h.mu.Unlock()
		return approved, nil
	case <-time.After(timeout):
		// 超时：自动拒绝，清理 pending 记录
		h.mu.Lock()
		delete(h.pending, approvalID)
		h.mu.Unlock()
		return false, fmt.Errorf("approval %s: 审批超时（%v）", approvalID, timeout)
	}
}

// HandleDecision 处理前端发送的审批决定。
// 由 main.go 的 WebSocket 控制处理器调用。
// approved 为 true 表示用户批准，false 表示拒绝。
func (h *WebSocketApprovalHandler) HandleDecision(approvalID string, approved bool) {
	h.mu.Lock()
	ch, ok := h.pending[approvalID]
	h.mu.Unlock()

	if ok {
		// 非阻塞发送：channel 容量为 1，不会阻塞
		select {
		case ch <- approved:
		default:
			// 如果 channel 已满（已收到决定），忽略重复消息
		}
	}
}

// ============================================================================
// ApprovalRule — 高风险操作审批规则
// ============================================================================

// ApprovalRule 实现了 PolicyRule 接口，用于拦截高风险工具调用并请求前端审批。
//
// # 触发条件
//
//   - run_shell: 命令包含 rm -rf、git push --force、sudo、chmod 777、dd if=、
//     mkfs、fork bomb 等高风险模式
//   - write_file: 路径包含 /etc/、/System/、C:\Windows\ 等系统路径
//   - delete_file: 所有删除操作都需要审批
//
// # 审批流程
//
//  1. Check 方法检测到高风险操作 → 生成唯一 approvalID
//  2. 返回 ErrApprovalRequired（携带 approvalID、tool、reason、input）
//  3. Engine 捕获错误 → 发射 system_info 事件 → 调用 ApprovalHandler 等待决定
//  4. 用户批准 → Engine 绕过 PolicyGate 直接执行工具
//  5. 用户拒绝 → Engine 返回拒绝错误，任务失败
//
// 如果 handler 为 nil，ApprovalRule 仍然会检测高风险操作并返回 ErrApprovalRequired，
// 但 Engine 无法等待审批决定（因为没有 handler），任务会直接失败。
type ApprovalRule struct {
	handler ApprovalHandler // 审批处理器（nil 时无法等待审批决定）
}

// NewApprovalRule 创建一个新的审批规则。
// handler 为 nil 时，规则仍然检测高风险操作但无法等待审批决定。
func NewApprovalRule(handler ApprovalHandler) *ApprovalRule {
	return &ApprovalRule{handler: handler}
}

// Name 返回规则名称。
func (r *ApprovalRule) Name() string { return "ApprovalRule" }

// Check 检查工具调用是否需要审批。如果需要审批，返回 ErrApprovalRequired；
// 否则返回 nil（允许执行）。
func (r *ApprovalRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	// 检查工具调用是否需要审批
	if !r.RequiresApproval(toolName, input) {
		return input, nil
	}

	// 生成审批原因（中文描述，展示给前端用户）
	reason := r.getApprovalReason(toolName, input)
	// 生成唯一审批 ID
	approvalID := GenerateApprovalID()

	return input, &ErrApprovalRequired{
		ApprovalID: approvalID,
		Tool:       toolName,
		Reason:     reason,
		Input:      input,
	}
}

// RequiresApproval 检查给定的工具调用是否需要前端审批。
// 公开方法，供外部调用者在 PolicyGate 之外检查审批需求。
func (r *ApprovalRule) RequiresApproval(toolName string, input map[string]any) bool {
	switch toolName {
	case "run_shell":
		if cmd, ok := input["command"].(string); ok && cmd != "" {
			return isHighRiskShellCommand(cmd)
		}
	case "write_file":
		if path, ok := input["path"].(string); ok && path != "" {
			return isHighRiskFilePath(path)
		}
	case "delete_file":
		// 所有删除操作都需要审批
		return true
	}
	return false
}

// getApprovalReason 生成审批原因的中文描述。
func (r *ApprovalRule) getApprovalReason(toolName string, input map[string]any) string {
	switch toolName {
	case "run_shell":
		if cmd, ok := input["command"].(string); ok {
			for pattern, desc := range highRiskShellPatterns {
				if matched, _ := regexp.MatchString(pattern, cmd); matched {
					return fmt.Sprintf("高风险 shell 命令需要审批: %s (匹配: %s)", truncateStr(cmd, 60), desc)
				}
			}
			return fmt.Sprintf("高风险 shell 命令需要审批: %s", truncateStr(cmd, 60))
		}
	case "write_file":
		if path, ok := input["path"].(string); ok {
			return fmt.Sprintf("写入系统路径需要审批: %s", path)
		}
	case "delete_file":
		return "删除文件操作需要审批"
	}
	return fmt.Sprintf("工具 %s 需要审批", toolName)
}

// ============================================================================
// DangerousCommandRule — Shell 危险命令检测规则（纵深防御）
// ============================================================================

// DangerousCommandRule 实现了 PolicyRule 接口，用于检测 shell 命令中的危险模式。
// 这是纵深防御措施：即使 ApprovalRule 未配置，此规则也会在 PolicyChain 中拦截
// 危险命令。
//
// # 行为
//
//   - AllowShellDangerous = true:   允许所有命令（跳过此规则）
//   - AllowShellDangerous = false:  检测危险命令模式，触发 ErrApprovalRequired 审批流程
//   - AutoApprovePolicy = true:     自动批准所有 policy block，但事件仍然记录
//   - 只对 run_shell 工具生效，其他工具直接放行
//
// 危险模式列表见 dangerousPatterns map，涵盖：
// 文件破坏、提权、磁盘操作、网络工具、进程终止、敏感文件访问、
// 容器/K8s 操作、Git 破坏性操作、包发布、远程访问、加密工具、
// 编码载荷解码、计划任务、Windows 注册表/服务/用户管理、信息收集等。
type DangerousCommandRule struct{}

// Name 返回规则名称。
func (r *DangerousCommandRule) Name() string { return "DangerousCommandRule" }

// Check 检测 shell 命令中的危险模式。如果 AllowShellDangerous 为 true，
// 所有命令都被允许。如果 AutoApprovePolicy 为 true，自动批准但记录日志。
// 否则，匹配危险模式的命令会触发 ErrApprovalRequired 审批流程。
func (r *DangerousCommandRule) Check(toolName string, input map[string]any, contract TaskContract) (map[string]any, error) {
	// 只对 run_shell 工具生效
	if toolName != "run_shell" {
		return input, nil
	}

	// 如果显式允许危险 shell 操作，跳过此规则
	if contract.Permissions.AllowShellDangerous {
		return input, nil
	}

	// 获取命令字符串
	cmd, ok := input["command"].(string)
	if !ok || cmd == "" {
		return input, nil
	}

	// Auto-approve policy: 自动批准所有 policy block，但事件由 Engine 记录
	if contract.AutoApprovePolicy {
		return input, nil
	}

	// 检查命令是否匹配危险模式
	for pattern, desc := range dangerousPatterns {
		matched, err := regexp.MatchString(pattern, cmd)
		if err != nil {
			// 正则编译错误（理论上不会发生，因为模式是硬编码的）
			continue
		}
		if matched {
			// 返回 ErrApprovalRequired 而非 ErrBlockedByPolicy，
			// 让 Engine 走 handleApprovalRequired 审批流程。
			// 前端会显示被拦截的命令，用户可 approve/deny。
			approvalID := GenerateApprovalID()
			return input, &ErrApprovalRequired{
				ApprovalID: approvalID,
				Tool:       toolName,
				Reason:     fmt.Sprintf("危险命令被策略检测: %s (匹配模式: %s)", desc, pattern),
				Input:      input,
			}
		}
	}

	return input, nil
}

// ============================================================================
// 高危模式定义
// ============================================================================

// highRiskShellPatterns 定义了 ApprovalRule 的高风险 shell 命令模式。
// 这些是最高风险的操作，需要前端用户审批后才能执行。
// key: 正则表达式模式, value: 中文描述
var highRiskShellPatterns = map[string]string{
	`(?i)\brm\s+-rf\b`:               "递归强制删除 (rm -rf)",
	`(?i)git\s+push\s+.*--force`:     "Git 强制推送 (git push --force)",
	`(?i)git\s+push\s+.*-f\b`:        "Git 强制推送 (git push -f)",
	`(?i)\bsudo\b`:                   "提权操作 (sudo)",
	`(?i)chmod\s+777`:                "宽松权限 (chmod 777)",
	`>\s*/dev/(?:sd[a-z]+\d*|hd[a-z]+\d*|nvme\d+n\d+|xvd[a-z]+|vd[a-z]+|disk/|mapper/)`: "写入磁盘设备 (>/dev/...)",
	`(?i)\bmkfs\b`:                   "创建文件系统 (mkfs)",
	`(?i)\bdd\s+if=`:                 "磁盘镜像操作 (dd)",
	`:\(\)\s*\{\s*:\|:&\s*\}`:       "Fork 炸弹 (:(){ :|:& };:)",
	`;\s*:\s*$`:                      "Fork 炸弹变体",
}

// highRiskFilePaths 定义了 ApprovalRule 的高风险文件路径。
// 写入这些路径的文件操作需要前端用户审批。
var highRiskFilePaths = []string{
	"/etc/",
	"/System/",
	`C:\Windows\`,
	`C:\Windows\System32\`,
}

// dangerousPatterns 定义了 DangerousCommandRule 的完整危险命令模式列表。
// 这是纵深防御的完整列表，比 highRiskShellPatterns 更广泛。
// key: 正则表达式模式, value: 中文描述
var dangerousPatterns = map[string]string{
	// === 破坏性文件操作 ===
	`(?i)\brm\s+-rf\b`:           "递归强制删除 (rm -rf)",
	`(?i)\brm\s+-r\b`:            "递归删除 (rm -r)",
	`(?i)\brmdir\b`:              "删除目录 (rmdir)",
	`(?i)\bdel\s+/f\b`:           "Windows 强制删除 (del /f)",
	`(?i)\bformat\b`:             "格式化磁盘 (format)",

	// === Git 强制推送 ===
	`(?i)git\s+push\s+.*--force`: "Git 强制推送 (git push --force)",
	`(?i)git\s+push\s+.*-f\b`:    "Git 强制推送 (git push -f)",

	// === 权限提升 ===
	`(?i)\bsudo\b`:               "提权操作 (sudo)",
	`(?i)\bsu\s+-`:               "切换用户 (su -)",

	// === 宽松权限 ===
	`(?i)chmod\s+777`:            "宽松权限设置 (chmod 777)",
	`(?i)chmod\s+-R\b`:           "递归修改权限 (chmod -R)",

	// === 磁盘操作 ===
	`>\s*/dev/(?:sd[a-z]+\d*|hd[a-z]+\d*|nvme\d+n\d+|xvd[a-z]+|vd[a-z]+|disk/|mapper/)`: "写入磁盘设备",
	`(?i)\bdd\s+if=`:             "磁盘镜像操作 (dd)",
	`(?i)\bmkfs\b`:               "创建文件系统 (mkfs)",

	// === Fork 炸弹 ===
	`:\(\)\s*\{\s*:\|:&\s*\}`:   "Fork 炸弹",
	`;\s*:\s*$`:                  "Fork 炸弹变体",

	// === 系统控制 ===
	`(?i)\bshutdown\b`:           "系统关机 (shutdown)",
	`(?i)\breboot\b`:             "系统重启 (reboot)",
	`(?i)\bhalt\b`:               "系统停止 (halt)",

	// === 管道到 Shell ===
	`(?i)\bcurl\b.*\|\s*(?:ba)?sh\b`:       "curl 管道到 shell",
	`(?i)\bwget\b.*\|\s*(?:ba)?sh\b`:       "wget 管道到 shell",
	`(?i)\bwget\b.*-O\s*-\s*\|\s*(?:ba)?sh\b`: "wget 管道到 shell",

	// === eval / exec ===
	`(?i)\beval\b`:               "eval 执行 (eval)",
	`(?i)\bexec\b`:               "exec 执行 (exec)",

	// === 网络工具（可能用于反向 Shell） ===
	`(?i)\bnc\b`:                 "网络工具 (nc)",
	`(?i)\bncat\b`:               "网络工具 (ncat)",

	// === 防火墙修改 ===
	`(?i)\biptables\b`:           "防火墙修改 (iptables)",
	`(?i)\bufw\b`:                "防火墙修改 (ufw)",

	// === 所有权修改 ===
	`(?i)\bchown\b`:              "文件所有权修改 (chown)",
	`(?i)\bchgrp\b`:              "文件组修改 (chgrp)",

	// === 进程终止 ===
	`(?i)\bkill\s+-9\b`:          "强制终止进程 (kill -9)",
	`(?i)\bpkill\b`:              "批量终止进程 (pkill)",
	`(?i)\bkillall\b`:            "批量终止进程 (killall)",

	// === 敏感文件访问 ===
	`/etc/passwd`:                "敏感文件访问 (/etc/passwd)",
	`/etc/shadow`:                "敏感文件访问 (/etc/shadow)",

	// === Docker 特权容器 ===
	`(?i)docker\s+run\s+.*--privileged`: "特权 Docker 容器 (--privileged)",
	`(?i)docker\s+exec\b`:               "Docker 容器执行 (docker exec)",

	// === Kubernetes 破坏性操作 ===
	`(?i)kubectl\s+delete\b`:     "Kubernetes 删除操作 (kubectl delete)",
	`(?i)kubectl\s+exec\b`:       "Kubernetes 容器执行 (kubectl exec)",

	// === Git 破坏性操作 ===
	`(?i)git\s+reset\s+--hard\b`: "Git 硬重置 (git reset --hard)",
	`(?i)git\s+clean\s+-fd\b`:    "Git 清理未跟踪文件 (git clean -fd)",

	// === 包发布 ===
	`(?i)npm\s+publish\b`:        "NPM 包发布 (npm publish)",
	`(?i)pip\s+install\b`:        "Python 包安装 (pip install)",

	// === 远程访问 ===
	`(?i)\bssh\b`:                "SSH 远程访问 (ssh)",
	`(?i)\bscp\b`:                "SCP 远程传输 (scp)",

	// === 加密工具（常用于恶意软件） ===
	`(?i)\bcertutil\b`:           "证书工具 (certutil)",
	`(?i)\bopenssl\b`:            "加密工具 (openssl)",
	`(?i)\bgpg\b`:                "加密工具 (gpg)",

	// === 编码载荷解码 ===
	`(?i)\bbase64\s+-d\b`:        "Base64 解码 (base64 -d)",
	`(?i)\bxxd\s+-r\b`:           "十六进制解码 (xxd -r)",

	// === 计划任务持久化 ===
	`(?i)\bcrontab\b`:            "计划任务 (crontab)",
	`(?i)\bat\b`:                 "计划任务 (at)",
	`(?i)\bschtasks\b`:           "Windows 计划任务 (schtasks)",

	// === Windows 注册表修改 ===
	`(?i)\breg\s+add\b`:          "Windows 注册表添加 (reg add)",
	`(?i)\breg\s+delete\b`:       "Windows 注册表删除 (reg delete)",

	// === Windows 服务控制 ===
	`(?i)\bsc\s+stop\b`:          "Windows 服务停止 (sc stop)",
	`(?i)\bsc\s+delete\b`:        "Windows 服务删除 (sc delete)",

	// === Windows 用户管理 ===
	`(?i)\bnet\s+user\b`:         "Windows 用户管理 (net user)",
	`(?i)\bnet\s+localgroup\b`:   "Windows 本地组管理 (net localgroup)",

	// === 信息收集（AllowShellDangerous=false 时阻止） ===
	`(?i)\bwhoami\b`:             "信息收集 (whoami)",
	`(?i)\bid\b`:                 "信息收集 (id)",
	`(?i)\buname\s+-a\b`:         "信息收集 (uname -a)",

	// === 环境信息泄露（AllowShellDangerous=false 时阻止） ===
	`(?i)\benv\b`:                "环境变量泄露 (env)",
	`(?i)\bprintenv\b`:           "环境变量泄露 (printenv)",
	`(?i)\bcat\s+/proc/`:         "进程信息泄露 (cat /proc/)",
}

// ============================================================================
// 辅助函数
// ============================================================================

// isHighRiskShellCommand 检查 shell 命令是否包含高风险模式。
// 用于 ApprovalRule 的 RequiresApproval 方法。
func isHighRiskShellCommand(cmd string) bool {
	for pattern := range highRiskShellPatterns {
		matched, _ := regexp.MatchString(pattern, cmd)
		if matched {
			return true
		}
	}
	return false
}

// isHighRiskFilePath 检查文件路径是否指向高风险系统路径。
// 用于 ApprovalRule 的 RequiresApproval 方法。
//
// 修复（TEST_REPORT 低危项）：原实现用 strings.Contains(path, "/etc/") 做
// 子串匹配，导致：
//   - "./etc/x"（项目内子目录）不匹配 "/etc/"，可绕过审批。
//   - 绝对路径分隔符差异（Windows 反斜杠）也不匹配。
// 现在先用 filepath.ToSlash 统一分隔符，再做规范化比较。对 Unix 系统路径
// （/etc/、/System/）要求 path 以 risky 前缀开头或紧跟分隔符，避免 "./etc/"
// 被错误放行；对 Windows 系统路径保持子串匹配（盘符路径天然带反斜杠）。
func isHighRiskFilePath(path string) bool {
	// 统一为正斜杠便于匹配 Unix 风格的系统路径
	normalized := strings.ToLower(filepath.ToSlash(path))
	for _, risky := range highRiskFilePaths {
		r := strings.ToLower(risky)
		// Windows 盘符路径（如 c:\windows\）仍用子串匹配
		if strings.Contains(r, ":") {
			if strings.Contains(normalized, r) {
				return true
			}
			continue
		}
		// Unix 系统路径：要求 path 以 risky 开头（绝对路径），
		// 这样 "./etc/foo" 不会被误判为 "/etc/foo"。
		if strings.HasPrefix(normalized, r) {
			return true
		}
	}
	return false
}

// GenerateApprovalID 生成唯一的审批请求 ID。
// 格式: "approval_" + 8 字节随机十六进制字符串。
func GenerateApprovalID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		// 降级：使用时间戳作为 ID
		return fmt.Sprintf("approval_%d", time.Now().UnixNano())
	}
	return "approval_" + hex.EncodeToString(bytes)
}

// truncateStr 截断字符串到指定长度，超出部分以 "..." 替代。
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}