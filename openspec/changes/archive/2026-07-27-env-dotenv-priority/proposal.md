## Why

当前代码中既有 `config.Load()` 通过 `loadEnvFile` 将 `.env` 写入系统环境变量，也有许多测试和运行时代码直接调用 `os.Getenv`。这导致优先级不一致：部分地方 `.env` 能覆盖系统环境变量（因为先执行了 loadEnvFile），部分地方则不能。需要把 `.env` 读取封装成独立层，默认`.env` 优先于系统环境变量，同时不污染 `os.Getenv`，并允许按需切换为系统环境变量优先。

## What Changes

- 新增 `internal/config/env.go`：提供 `Getenv` / `LookupEnv`，先查 `.env` 缓存，再查系统环境变量；默认 `.env` 优先。
- 新增 `SetDotEnvFirst` / `SetOSFirst` 控制优先级，供启动早期切换。
- `Load()` 改为先 `ReloadEnvCache()` 预热 `.env` 缓存，再全部使用 `Getenv` 读取配置。
- 保留 `ApplyEnvFileToOS` 作为独立兼容方法（原 `loadEnvFile` 语义），需要 `os.Getenv` 也能读到 `.env` 的场景调用。
- `cmd/server/main.go` 中的 `LOG_LEVEL`、`LOG_FILE`、`REQUIRE_AUTH`、`isTruthyEnv` 改为 `config.Getenv`。
- `internal/tool/web_search_gemini_test.go` 的真实网络测试改为先 `config.Getenv` 再回退 `os.Getenv`（避免 tool 包 import config 产生循环依赖）。
- 新增 `internal/config/env_test.go` 覆盖 `.env` 优先、OS 优先、来源报告与缓存重置。

## Capabilities

### New Capabilities
- `env-dotenv-priority`: 提供 `.env` 优先的环境变量读取封装，与 `os.Getenv` 保持独立。

### Modified Capabilities
- 无现有 spec 的需求变更，纯属实现层重构。

## Impact

- 后端：`internal/config/env.go`、 `internal/config/config.go`、 `cmd/server/main.go`、 `internal/tool/web_search_gemini_test.go`。
- 行为变更：server 启动时 `.env` 中的变量现在会覆盖同名的系统环境变量（默认策略）。
- 无数据库/API 变更。
