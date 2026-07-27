## Context

当前 `config.Load()` 先调用 `loadEnvFile`（把 `.env` 的键值 `os.Setenv` 进系统环境变量，仅当系统环境变量不存在时），然后再用 `os.Getenv` 读配置。这导致：
- 系统环境变量天然优先于 `.env`（与部分开发者直觉相反）。
- `.env` 的值被写进了系统环境变量，污染了进程全局状态。
- 非 `config.Load()` 路径（如 `cmd/server/main.go` 的 `os.Getenv("LOG_LEVEL")`）读取时机一旦在 `config.Load()` 之前，就完全读不到 `.env`。

## Goals / Non-Goals

**Goals:**
- 建立独立的 `.env` 缓存层，默认 `.env` 优先。
- 提供 `SetOSFirst()` 让调用方在启动早期切换为系统环境变量优先。
- 不污染 `os.Getenv`，保持两份来源独立。
- 所有配置读取走统一封装。

**Non-Goals:**
- 不改动外部 CLI/CI 设置环境变量的方式。
- 不实现 `.env` 插值、多行值等高级语法。
- 不强制替换所有 `os.Getenv`（文件路径类仍用 `os.Getenv`）。

## Decisions

1. **内存缓存而非实时读文件**
   - 启动时加载一次，避免每次 `Getenv` 都 IO。
   - 提供 `ReloadEnvCache()` 供热重载场景。

2. **默认 .env 优先**
   - 满足用户需求；`SetOSFirst()` 留给需要 12-factor 优先级的调用方。

3. **保留 `ApplyEnvFileToOS`**
   - 兼容旧 `loadEnvFile` 行为，避免破坏依赖 `os.Getenv` 读到 `.env` 的外部脚本/测试。

4. **不替换 `ENV_FILE` 定位本身**
   - `EnvFilePath()` 仍用 `os.Getenv("ENV_FILE")`，否则无法确定要加载哪个 `.env`。

## Risks / Trade-offs

- [Risk] 默认 `.env` 优先可能与现有部署习惯冲突（CI 环境变量通常被认为最高优先级）。
  - Mitigation: 提供 `ENV_FILE_PRIORITY=os` 在 `.env` 内声明 OS 优先，或调用 `SetOSFirst()`。
- [Risk] `t.Setenv` 在默认策略下无法覆盖 `.env` 缓存，可能让部分测试困惑。
  - Mitigation: 文档说明；测试可调用 `ResetEnvCache()` 后使用 `t.Setenv`。

## Migration Plan

1. 合并后，所有 `.env` 中的同名变量将覆盖系统环境变量。
2. 若某环境需要保持系统环境变量优先，在 `.env` 中增加 `ENV_FILE_PRIORITY=os` 或在 `main.go` 调用 `config.SetOSFirst()` 之后立即 `config.ReloadEnvCache()`。
3. 不需要数据迁移。
