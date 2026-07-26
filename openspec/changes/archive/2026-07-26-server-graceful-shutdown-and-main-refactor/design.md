## Context

`cmd/server/main.go` 目前负责所有子系统的初始化，并且在第 249–257 行用一个内联 goroutine 处理 `os.Interrupt` / `SIGTERM`：该 goroutine 只调用 `hub.Shutdown(context.Background())`，然后主 goroutine 继续阻塞在 `http.ListenAndServe` 上。

实际现象：Ctrl+C 时信号 goroutine 关闭 Hub，但 `http.Server` 仍在 `Accept` 上阻塞，Heartbeat/Cron scheduler 等后台 goroutine 仍在运行，于是进程无法退出。同时，main.go 已膨胀到超过 1000 行，信号处理、路由注册、静态文件服务、工具初始化全挤在一起，新增子系统时很难判断在哪里加关闭逻辑。

## Goals / Non-Goals

**Goals:**
- 收到 `SIGINT` / `SIGTERM` 后，server 应在 5 秒内完成优雅退出。
- 优雅关闭顺序应可预测：先停止接受新 HTTP 连接，再关闭对外广播的 Hub，然后停止后台调度器、Heartbeat、MCP 等。
- 把 main.go 中的关闭逻辑提取到独立文件，使其回归"初始化 + 构造 appServer + 启动"三段式。
- 为关闭逻辑提供单元测试，避免再次回归。

**Non-Goals:**
- 不引入 systemd / docker 等外部生命周期管理。
- 不改变任何 REST / WebSocket 协议语义。
- 不改动 agent runtime 内部取消逻辑（`cancelRegistry` / `engineRegistry` 仍走现有 WS control handler）。
- 不拆解 main.go 中除关闭逻辑外的其他部分。

## Decisions

### 1. 引入 `shutdownManager` 集中管理关闭器
**选择**：在 `cmd/server/shutdown.go` 中实现一个轻量 registry，允许任意子系统注册 `func(ctx context.Context) error` 关闭函数；收到信号后按注册顺序串行调用，并赋予总超时。

**理由**：
- main.go 不再为每个子系统写内联 goroutine 或 `defer`。
- 顺序可控；关闭 HTTP listener 应当早于关闭 Hub（停止新连接 → 停止广播）。
- 测试简单：可以用内存计数器验证调用顺序与超时行为。

**替代方案**：用 `github.com/oklog/run` 或类似库。否决：引入外部依赖过重；本需求只需 100 行左右自研代码。

### 2. 使用 `http.Server` + `Shutdown` 替换 `http.ListenAndServe`
**选择**：创建 `http.Server`，在 `shutdownManager` 中注册其 `Shutdown`；主 goroutine 启动 `srv.ListenAndServe()`，收到信号后关闭。

**理由**：
- 这是 Go 标准优雅关闭 HTTP 的标准做法。
- 只要 handler 不持有无限 goroutine，`Shutdown` 返回后主 goroutine 即可从 `ListenAndServe` 返回并执行 `log.Fatal` 之外的正常退出。

### 3. Heartbeat 等待后台 goroutine 退出
**选择**：在 `Heartbeat` 中增加 `sync.WaitGroup`，`Start` 时 `wg.Add(1)`，goroutine 退出时 `wg.Done()`；`Stop` 先 cancel 再 `wg.Wait()`。

**理由**：
- 当前 `Stop()` 只 cancel，如果 Beat 正在执行长效 DB 查询，`main` 退出时可能强行中断 goroutine 并触发 data race 或资源泄漏。
- 测试需要可观察的生命周期终点。

### 4. Cron scheduler 已提供 `Stop()`，直接注册到 `shutdownManager`
**选择**：复用现有 `cron.Scheduler.Stop()`，无需改动。

**理由**：
- 已有实现会停止 robfig cron 与 once timer，满足需求。

### 5. MCP manager 关闭保留现有 `defer mcpManager.Close()`，同时注册到 `shutdownManager`
**选择**：`defer` 保证正常启动失败路径也能释放；`shutdownManager` 保证信号路径触发。

**理由**：
- `Close` 本身幂等，双保险无风险。

## Risks / Trade-offs

- [Risk] `shutdownManager` 串行调用关闭器，若前一个 closer 卡住，后续 closer 无法执行。  
  → Mitigation：每个 closer 内部应使用传入的 ctx 超时， manager 本身也设总超时；超过总超时后 Log warning 并返回，避免无限等待。
- [Risk] 现有运行中的任务在 server 退出时不会等待完成。  
  → Mitigation：本变更范围是"进程能退出"，不是"零停机"。若有运行中任务，退出会断开 WS / HTTP，任务因 ctx 取消自然结束；未来可扩展为等待 active tasks 结束。
- [Risk] `http.Server.Shutdown` 在 Windows 上 Listener 关闭行为与 Unix 一致，但需验证。  
  → Mitigation：通过本地启动 + Ctrl+C 实测验证。
- [Risk] Heartbeat 的 `wg.Wait()` 可能阻塞整体退出，如果 Beat 内部査询卡住。  
  → Mitigation：`Beat` 已接受 `ctx` 并在查询前检查 `ctx.Done()`；`shutdownManager` 总超时作为第二道防线。
