## Why

当前 server 在收到 Ctrl+C（`os.Interrupt` / `SIGTERM`）后，日志打印 `shutting down WebSocket hub` 便停止响应，进程无法退出，必须强制 kill。这是因为优雅关闭只停止了 WebSocket Hub，但没有等待其 goroutine 结束、没有关闭 HTTP server、没有停止 Heartbeat 和 Cron scheduler 等后台 goroutine，导致主 goroutine 阻塞在 `http.ListenAndServe` 上。

另外，`cmd/server/main.go` 已膨胀到 1000+ 行，初始化逻辑与一次性关闭逻辑混在一起，信号处理分散在函数中部，既难测试也不利于后续扩展新的需要优雅关闭的子系统。

## What Changes

- 新增 `cmd/server/shutdown.go`：引入 `shutdownManager` 统一注册与管理所有需要优雅关闭的子系统（Hub、Heartbeat、Cron Scheduler、MCP Manager、HTTP server）。
- 改造 `cmd/server/main.go`：
  - 用 `http.Server` + `Shutdown` 替换 `http.ListenAndServe`，在收到信号后主动关闭 HTTP listener。
  - 把各子系统的关闭器注册到 `shutdownManager`，让 signal handler 只负责触发一次 `Shutdown(ctx)`。
  - 保持现有初始化顺序不变，仅把关闭逻辑外迁，不破坏已有行为。
- 改造 `internal/harness/heartbeat.go`：
  - 为 `Heartbeat.Stop()` 增加 `sync.WaitGroup`，等待后台 goroutine 真正退出，而不是只 cancel 不等待。
- 新增/补充测试：
  - `cmd/server/shutdown_test.go`：验证 `shutdownManager` 按注册顺序调用 closer，并在总超时后强制返回。
  - `internal/harness/heartbeat_test.go`：验证 `Stop()` 之后后台 goroutine 已退出，不再跑 Beat。

## Capabilities

### New Capabilities

- `server-graceful-shutdown`：定义 server 进程收到终止信号后，按顺序关闭 Hub、HTTP server、Cron scheduler、Heartbeat、MCP manager 等子系统的行为契约。

### Modified Capabilities

- 无（本次为内部行为/实现重构，不对外暴露 API 或改变协议语义）。

## Impact

- `cmd/server/main.go`：信号处理与关闭逻辑大量简化，仅保留初始化与依赖构造。
- `cmd/server/shutdown.go`：新增文件，包含 `shutdownManager` 与辅助函数。
- `internal/harness/heartbeat.go`：`Heartbeat` 增加 `sync.WaitGroup` 与生命周期管理。
- `internal/harness/heartbeat_test.go` / `cmd/server/shutdown_test.go`：新增测试覆盖。
- 部署行为变化：server 现在可以在 5 秒内响应 Ctrl+C 正常退出；若关闭器超时，会记录 warning 后继续退出，不再僵死。
