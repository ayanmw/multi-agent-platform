# dotenv-package 变更提案

## Why

当前 `.env` 与环境变量相关逻辑集中在 `internal/config/env.go`，与 `internal/config` 包强耦合。随着越来越多模块（如 `internal/tool`）希望以统一方式读取 `.env` 配置，这种耦合会引发 import cycle 风险，并阻碍其他包复用 dotenv 缓存层。同时，现有手写 `.env` 解析器过于简单，不支持引号、换行、插值等标准 dotenv 特性。将环境变量封装迁移到独立的 `internal/config/dotenv` 子包，并引入业界的 `godotenv`，可以提升复用性、解析健壮性与长期可维护性。

## What Changes

- 新建 `internal/config/dotenv` 子包，承载 `.env` 加载、缓存与优先级读取逻辑。
- **BREAKING**: 将 `internal/config` 中的 `Getenv` / `LookupEnv` / `LoadEnvFile` / `ReloadEnvCache` / `SetDotEnvFirst` / `SetOSFirst` / `ResetEnvCache` / `ApplyEnvFileToOS` 迁移到 `internal/config/dotenv`，`internal/config` 重新导出薄封装以保持现有调用方兼容。
- 使用 `github.com/joho/godotenv` 替换手写 `.env` 解析器，支持带引号字符串、注释、多行值等标准特性。
- 新增子包 API 规范（如 `Must`、`MustBool`、`MustInt`、`GetenvWithDefault` 等）方便业务代码简化读取。
- 更新 `internal/tool/web_search_gemini_test.go` 的真实网络测试，使其通过 `internal/config/dotenv` 读取 `GEMINI_API_KEY`，不再依赖 `internal/config`，彻底消除 import cycle 隐患。
- 补充单元测试，覆盖各种引号、转义、空值、优先级切换与来源报告场景。

## Capabilities

### New Capabilities

- `config-dotenv-package`: 独立子包形式的 `.env` 读取与优先级管理，提供缓存 API、来源报告与优先级切换。

### Modified Capabilities

- `env-dotenv-priority`: 实现层面移动到独立子包，`internal/config` 仅保留薄封装，要求与行为不变；新增 godotenv 解析能力意味着 `.env` 文件接受更宽的语法，属于兼容性增强而非破坏。

## Impact

- 受影响的包：`internal/config`、`internal/tool`（测试）、未来所有希望独立读取 `.env` 的模块。
- 新增依赖：`github.com/joho/godotenv`。
- 不修改 `.env` 优先级语义、不改动 `os.Getenv` 默认行为；`internal/config.Getenv` 行为保持向后兼容。
