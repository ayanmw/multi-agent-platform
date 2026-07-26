# backend-security-and-concurrency-hardening Design

## Context

本次变更源于一次全面后端 review，发现当前代码在三个方向存在可放大的风险：

1. **Engine 边界健壮性**：`Engine.Run` 使用 `defer recover()` 后重新 `panic(r)`，导致单个 LLM/tool 异常可能顶掉调用方 recovery；AgentBus listener 与主 loop 对 `messages` 切片存在隐式并发写入。
2. **工具层安全**：`run_shell` 对 `ExecuteContext.Workdir` 未做作用域校验；`DynamicTool` 的 shell 实现把 LLM 输入直接拼入 `sh -c` 命令字符串；cron webhook 对 URL 无 scheme/地址限制。
3. **资源与状态一致性**：worktree 分支删除失败被吞掉、cron 的 DB 更新错误被吞掉、Hub 没有优雅关闭、scheduler stop 不等待运行中 execution。

当前项目已完成 worktree 隔离、cron 子系统、Phase 8-A/B 架构重构，mock 回归 21/21 PASS，单测全绿。架构主线清晰，但边界健壮性尚未被「真实 LLM + 多用户 + 长期运行」场景充分验证。本设计在尽量不破坏现有 API 的前提下，按高/中/低优先级分批补齐这些边界。

## Goals / Non-Goals

**Goals：**

- 让 `Engine.Run` 在内部 panic 时返回 error 并写日志，不再重新抛出。
- 明确并加固 AgentBus listener 与主 loop 的并发契约。
- `run_shell` 在设置 `cmd.Dir` 前校验 workdir 作用域。
- DynamicTool shell 类型具备命令注入防护。
- cron webhook action 限制 scheme 与目标地址。
- cron execution / schedule meta 更新失败被显式记录。
- worktree 分支删除失败有可见反馈。
- cron script action 与 Hub 具备关闭/超时控制能力。

**Non-Goals：**

- 不改动前端 UI 行为（事件字段扩展除外）。
- 不引入新的外部依赖（除标准库外）。
- 不一次性彻底消除所有全局状态（第三批为渐进式治理）。
- 不修改 LLM provider 协议或 tool schema 结构。

## Decisions

### Decision 1: Engine panic 后返回 error 而非重新 panic

**选择**：在 `Engine.Run` 的 `defer recover()` 中完成 `task_failed` 事件发送、任务状态更新、context snapshot 清理后，用 `err = fmt.Errorf(...)` 让函数正常返回，不再 `panic(r)`。

**理由**：
- 当前「重新 panic」的假设是「调用方一定有 recovery」。但 `AgentRunner.RunSync`、测试路径、cron 触发的 recovery 路径并不都有显式 recovery。
- 一个 agent 任务内部的 bug（如 tool 返回非法格式导致 map 操作 panic）不应影响整个 server 进程。
- 保留堆栈仍可通过 `debug.Stack()` 写入日志/error 字符串。

**替代方案**：要求所有调用方加 recovery。拒绝——调用链分散在 cmd/server、cmd/server/cron、测试、orchestrator 中，难以保证。

**风险**：失去「重新 panic 让进程退出/重启」的强失败信号。
**缓解**：日志 + observability span + `task_failed` 事件足够暴露问题。

### Decision 2: AgentBus listener 使用显式 mutex 保护 messages 写入

**选择**：在 `Engine` 中增加 `msgMu sync.Mutex`，任何追加到 `e.messages` 的操作（主 loop 的 tool result、listener 的 agent message）都先上锁。

**理由**：
- `messages` 是 Engine 的核心可变状态，当前并发写入依赖「主 loop 与 listener 不会同时写」的隐式假设。
- 使用 mutex 是最小侵入方案，不改变消息顺序语义。

**替代方案**：把 listener 收到的消息通过 channel 发给主 loop，由主 loop 统一写。拒绝——需要大改 ReAct loop 结构，且 listener 的异步特性会被削弱。

**风险**：频繁加锁是否影响性能。
**缓解**：锁粒度极小（仅 slice append），无 IO 操作。

### Decision 3: `run_shell` workdir 作用域校验放在工具层而非 Engine

**选择**：`run_shell` executor 在设置 `cmd.Dir` 前，调用 harness 的 `FileScopeRule`（或等效校验）确认 `ctx.Workdir` 位于 session `WorkspaceDir` 或当前 active worktree 下。

**理由**：
- `write_file`/`read_file` 已通过 `resolvePathWithCtx` + `FileScopeRule` 做 scope 校验；`run_shell` 作为同等危险工具应遵循同一规则。
- 在工具层校验更贴近危险操作，且错误信息可直接作为 observation 返回给 LLM。

**替代方案**：在 Engine.executeToolCall 中统一校验所有工具的 `ctx.Workdir`。拒绝——不同工具对 workdir 语义不同（如某些未来 tool 可能合法读取全局配置），统一拦截可能过度限制。

**风险**：校验失败可能导致某些现有测试中的相对路径脚本失败。
**缓解**：仅校验 `ctx.Workdir` 是否落入允许 scope，不校验命令内容；空 workdir 仍回退到旧行为。

### Decision 4: DynamicTool shell 模板解析为 `program + args`

**选择**：把 `command` 模板按空格切分为 `program` 与 `args`；`{param}` 占位符替换后，使用 `exec.CommandContext(ctx, program, args...)` 执行。

**理由**：
- `exec.Command(name, args...)` 不经过 shell 解释，自然避免 `;`、`|`、`$(...)` 等注入。
- 保持现有模板语法 `{param}`，向后兼容大部分正常用例。

**替代方案 A**：保留 `sh -c`，但对替换值做 shell 转义。拒绝——转义仍依赖黑名单，容易遗漏；且无法阻止 LLM 在模板本身写入 `sh -c` payload。
**替代方案 B**：完全禁用 shell 类型 dynamic tool。拒绝——现有功能需要保留。

**风险**：用户原本期望 `command: "bash -c 'echo {x}'"` 的写法会失败。
**缓解**：分阶段推进：第一阶段限制为「简单 program + args」并通过文档/错误信息提示；第二阶段可提供显式 `shell: true` 选项（需额外审批）。

### Decision 5: cron webhook 限制 scheme 与地址

**选择**：默认仅允许 `http`/`https`；禁止 `file://`、`ftp://`、`unix://` 等；禁止指向回环地址、链路本地地址、私有地址；可通过 `CRON_WEBHOOK_ALLOW_PRIVATE=true` 放行私有地址。

**理由**：
- SSRF 是 webhook 类功能的标准风险。
- 提供显式开关，便于内网/测试场景。

**替代方案**：维护一个 URL 白名单列表。拒绝——初期维护成本高，黑名单 + scheme 方案已覆盖主要风险。

### Decision 6: 错误暴露统一使用日志 + 事件标记

**选择**：`UpdateExecution` / `UpdateCronScheduleMeta` 失败时记录结构化 warn 日志；在已发出的 completed/failed 事件 data 中追加可选字段 `persisted: false`。

**理由**：
- 不强改 events schema，前端可忽略该字段。
- 日志让运维能发现 DB 不一致。

### Decision 7: 全局状态治理采用渐进式封装

**选择**：第三批不改变所有旧调用点，而是新增 `appServer` 字段并在新代码/重构路径使用；包级全局变量作为向后兼容代理保留。

**理由**：
- 完全消除全局状态会涉及大量文件改动，容易引入回归。
- 渐进式封装让后续新子系统自然脱离全局变量。

## Risks / Trade-offs

| Risk | Mitigation |
|---|---|
| DynamicTool shell 不再支持复杂 shell 语法，影响 LLM 动态创建的某些工具 | 在错误信息中明确说明；后续如需要可提供显式 `shell: true` 高权限开关 |
| cron webhook 私有地址默认被阻，影响内网部署用户 | 提供 `CRON_WEBHOOK_ALLOW_PRIVATE` 环境变量显式放行 |
| Engine panic 不再重新抛出，可能导致某些严重 bug 被静默吞掉 | 日志/span/task_failed 事件三重暴露；保留 `debug.Stack()` |
| 增加 mutex 后代码路径变复杂，可能引入死锁 | 锁范围严格限制在 append 操作；review 时重点检查 |
| 多批次变更导致中间状态需要反复回归测试 | 每批次独立 PR/提交，并跑全量单测 + mock 回归 |

## Migration Plan

1. **本地验证**：每批次完成后 `go test ./...`、mock 回归。
2. **配置新增**：部署时可选设置 `CRON_WEBHOOK_ALLOW_PRIVATE=true`（如需要）。
3. **向后兼容**：无 API breaking change；新增字段可忽略。
4. **回滚**：若某批次引入回归，可单独 revert 该批次 commit。

## Open Questions

1. `run_shell` 的 workdir scope 规则是否应与 `FileScopeRule` 完全一致，还是允许一个更宽松的「session workspace 或其直系子目录」列表？
2. DynamicTool shell 模板是否需要支持 `shell: true` 显式高风险模式，还是本轮只支持 `program + args`？
3. cron webhook 私有地址黑名单是否复用 Go `net` 包的 `IsPrivate` / `IsLoopback` / `IsLinkLocalUnicast`，还是自维护列表？
