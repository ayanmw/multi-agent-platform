# backend-security-and-concurrency-hardening 变更提案

## Why

本次后端 review 发现若干高/中风险点集中在三个领域：

1. **可观测性下的健壮性**：单个 tool 或 Engine 内部 panic 会被重新抛出，可能导致整个 server 崩溃；AgentBus listener 与主 loop 对 `messages` 切片的并发访问缺乏显式同步契约。
2. **工具层安全**：`run_shell` 的 `cmd.Dir` 未执行 workdir 作用域校验；DynamicTool 的 shell 类型通过 `{param}` 模板将 LLM 输入直接拼入 `sh -c`，存在命令注入风险；cron 的 webhook action 未限制 URL scheme/内网地址，存在 SSRF 风险。
3. **资源与状态一致性**：worktree 分支删除失败被静默忽略、cron execution / schedule meta 更新错误被吞掉、Hub 没有优雅关闭路径、scheduler 不等待运行中 execution 完成即停止。

这些问题尚未阻塞当前 mock 回归（21/21 PASS）与单测，但随着真实 LLM、多用户并发、长期运行 cron 的上线，会被放大。本变更把 review 结论转化为可分批执行的改进计划，优先处理高安全风险与panic/并发边界，再处理资源清理和全局状态治理。

## What Changes

按批次推进：

### 第一批：高优先级安全与健壮性

1. **Engine panic 优雅降级**
   - `Engine.Run` 捕获 panic 后记录堆栈并返回 error，不再重新 `panic`。
   - 保持 `task_failed` 事件与 `updateTask("failed")` 不变。

2. **Engine AgentBus listener 并发契约**
   - 明确 `messages` 切片的读写边界；主 loop 与 listener 之间使用显式同步（`sync.Mutex` 或消息 channel 定序）。
   - 确保 `Run` 返回前 listener 已完全退出。

3. **`run_shell` workdir 作用域校验**
   - 在设置 `cmd.Dir` 前校验 `ExecuteContext.Workdir` 必须落在 session workspace 或当前 active worktree 路径下。
   - Empty workdir 仍回退到旧路径（向后兼容）。

4. **DynamicTool shell 注入防护**
   - 弃用 `sh -c "{commandTemplate}"` 模式。
   - 对 command template 做受限解析：支持 `{param}` 占位符替换后作为单一命令字符串的 `sh -c` 参数，并对替换值进行 shell 元字符转义；或要求模板被解析为 `program + args`，使用 `exec.Command(name, args...)`，禁止解释 shell。

5. **cron webhook SSRF 防护**
   - 仅允许 `http`/`https` scheme。
   - 默认阻止指向私有地址、localhost、链路本地地址的 URL；通过配置 `CRON_WEBHOOK_ALLOW_PRIVATE=true` 可显式放行（测试/内网场景）。

### 第二批：中优先级资源与一致性

6. **worktree 分支删除失败不再静默忽略**
   - `Remove`/`RemoveOrphan` 在 `git branch -D` 失败时返回 warning 并记录日志。
   - `RemoveReport` 增加 `BranchRemoved bool` 与 `Warnings []string`。

7. **cron execution 与 schedule meta 错误显式暴露**
   - `UpdateExecution` / `UpdateCronScheduleMeta` 失败时记录 error 日志；事件 data 中可选增加 `persisted:false` 标记。

8. **cron script action 超时控制**
   - 每个 tool 调用使用 `ctx` 限制执行时间；整条 script 增加整体 timeout。

9. **worktree `Create` 目录已存在防护**
   - 生成 shortID 后检查 `wtPath` 是否存在，碰撞时带最多 3 次重试。

### 第三批：架构与全局状态治理

10. **Hub 优雅关闭**
    - 为 `Hub` 增加 `Shutdown(ctx)`：停止接收新 client，flush 待广播事件，等待 writer goroutine 退出或超时。

11. **全局 registry 注入治理（可选、低侵入）**
    - 保持现有包级全局变量作为兼容代理；新增 `appServer` 字段承载 registry，handler 优先使用 `s.xxx`。
    - 先在新代码/重构路径落地，不一次性重写旧调用点。

## Capabilities

### New Capabilities

- `engine-panic-graceful-degradation`: Engine panic 后优雅返回 error，不再重新抛出 panic。
- `engine-agentbus-concurrency-contract`: AgentBus listener 与主 loop 的并发消息写入契约。
- `tool-shell-workdir-scope-validation`: `run_shell` 在执行前校验 `cmd.Dir` 位于允许作用域内。
- `dynamic-tool-shell-injection-prevention`: DynamicTool shell 模板执行增加命令注入防护。
- `cron-webhook-ssrf-guard`: cron webhook action 的 scheme 与地址白名单校验。
- `cron-execution-persistence-error-visibility`: cron execution 与 schedule meta 更新失败的日志/事件暴露。
- `cron-script-timeout`: cron script action 的 context 超时控制。
- `worktree-branch-removal-visibility`: worktree 分支删除失败不再静默忽略。
- `worktree-create-path-collision-retry`: worktree Create 目录碰撞重试。
- `hub-graceful-shutdown`: WebSocket Hub 优雅关闭路径。

### Modified Capabilities

- `workspace-worktree-isolation`: `RemoveReport` 增加 `BranchRemoved` 与 `Warnings`；Create 增加路径碰撞重试语义。仅返回结构扩展，不改变现有 requirement 行为，属于 backward-compatible delta。
- `tool-execute-context-extension`: 增加 `Workdir` 作用域校验场景，明确 `run_shell` 与 `write_file`/`read_file` 共享同一 scope 规则。

## Impact

- **代码模块**：`internal/runtime/engine.go`、`internal/tool/builtin.go`、`internal/tool/executor.go`、`internal/tool/dynamic.go`、`internal/cron/action.go`、`internal/cron/executor.go`、`internal/workspace/manager.go`、`internal/ws/hub.go`。
- **API/行为变更**：无破坏性 API 变更；新增字段均为返回结构扩展。DynamicTool shell 模板若依赖 shell 元字符解释可能行为变化（BREAKING for 恶意/不可控输入，但符合安全预期）。
- **配置变更**：新增可选环境变量 `CRON_WEBHOOK_ALLOW_PRIVATE`（默认 false）。
- **测试**：每个 batch 配套单测/集成测试更新，保持 mock 回归 21/21 PASS。
