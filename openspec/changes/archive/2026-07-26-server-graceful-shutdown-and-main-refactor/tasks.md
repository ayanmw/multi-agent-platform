## 1. Shutdown Manager

- [x] 1.1 创建 `cmd/server/shutdown.go`，实现 `shutdownManager`（Register / Shutdown / 总超时）以及导出函数 `NewShutdownManager()`。
- [x] 1.2 为 `shutdownManager` 编写 `cmd/server/shutdown_test.go`，验证顺序调用与超时行为。

## 2. Heartbeat Lifecycle

- [x] 2.1 修改 `internal/harness/heartbeat.go`，为 `Heartbeat` 增加 `sync.WaitGroup`，确保 `Stop()` 等待后台 goroutine 退出。
- [x] 2.2 新增/修改 `internal/harness/heartbeat_test.go`，验证 `Stop()` 后 goroutine 退出、不再执行 Beat。

## 3. Main.go 改造

- [x] 3.1 将 `http.ListenAndServe` 替换为 `http.Server`，并在 `shutdownManager` 中注册 `Shutdown`。
- [x] 3.2 在 `cmd/server/main.go` 中用 `shutdownManager` 注册 hub、heartbeat、cron scheduler、mcp manager 等关闭器。
- [x] 3.3 简化 signal handler，统一调用 `shutdownManager.Shutdown(ctx)`；移除仅关闭 Hub 的内联 goroutine。

## 4. 验证

- [x] 4.1 运行 `go test ./cmd/server/... ./internal/harness/...` 通过。
- [x] 4.2 运行完整 `go test ./...` 通过。
- [x] 4.3 本地启动 server，发送 Ctrl+C，验证进程在 5 秒内退出。
- [x] 4.4 提交代码并记录 OpenSpec 进度。
