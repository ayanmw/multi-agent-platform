# dotenv-package 设计

## Context

当前 `internal/config/env.go` 实现了 `.env` 文件的内存缓存、优先级读取与 `.env → os` 写入功能。它作为 `internal/config` 包的一部分，与配置模型 `Config` 强耦合。任何希望复用 `.env` 读取能力的包，如果尝试 `import "github.com/ayanmw/multi-agent-platform/internal/config"`，都必须连带引入大量无关类型（`Config`、MCP 配置等），并可能触发循环依赖。

同时，手写 `.env` 解析器只做了最简单的 `KEY=VALUE` 分 trim，不支持：
- 单/双引号值（含转义）
- 行内注释 `#`
- 多行值
- `export` 前缀
- 变量插值（可选）

这导致用户如果将 `FOO="bar"` 放入 `.env`，缓存中会保留引号，可能破坏下游业务逻辑。

## Goals / Non-Goals

**Goals:**

- 将 `.env` 加载/缓存/优先级读取逻辑提取到独立的 `internal/config/dotenv` 子包，与 `internal/config` 解耦。
- 使用 `github.com/joho/godotenv` 替换手写解析器，获得标准 dotenv 解析能力。
- 在 `internal/config` 包中保留薄封装（type alias / 函数转发），使现有调用方 `config.Getenv(...)` 零改动。
- 通过 `internal/config/dotenv` 向 `internal/tool/web_search_gemini_test.go` 提供读取能力，避免 import `internal/config`。
- 扩展更便捷的 API：`GetenvWithDefault`、`MustBool`、`MustInt`，减少重复解析样板。

**Non-Goals:**

- 不引入 `.env` 文件变更热重载；`ReloadEnvCache` 仍只在启动时显式调用或测试手动管理。
- 不做全量环境变量追踪（如 `Setenv` 监听同步到缓存）；缓存与 `os.Setenv` 保持独立。
- 不替换项目所有 `os.Getenv`；仅封装业务配置读取点，文件定位环境变量（如 `ENV_FILE`）仍保持 `os.Getenv`。

## Decisions

### Decision 1: 使用 `internal/config/dotenv` 子包路径

- **选择**: 在 `internal/config` 下新建 `dotenv` 子包，而非完全独立的 `pkg/dotenv`。
- **理由**: `config/dotenv` 语义清晰，说明它是配置子系统的环境变量层；仍属于 internal，不对外承诺稳定 API。`internal/config` 可以继续 re-export 关键函数，保持当前调用代码不变。
- **替代方案**: `pkg/dotenv` 会使其成为公共 API，增加长期维护负担，目前没有必要。

### Decision 2: 使用 `github.com/joho/godotenv` 作为解析引擎

- **选择**: 新增 `github.com/joho/godotenv` 依赖。
- **理由**: 它是 Go 社区最广泛使用的 dotenv 解析库，兼容 Ruby dotenv，支持引号、转义、注释、`export` 前缀。零外部依赖，体积很小。
- **替代方案**: 继续手写解析器。拒绝原因是需要持续维护标准语法的兼容细节（如 `\n`、多行值），边际成本高。

### Decision 3: `internal/config` 保留薄封装而非完全移除

- **选择**: `internal/config` 中保留同名的 `Getenv` / `LookupEnv` / `LoadEnvFile` / `ReloadEnvCache` / `SetDotEnvFirst` / `SetOSFirst` / `ResetEnvCache` / `ApplyEnvFileToOS` 函数，内部调用 `dotenv.X`。类型 `LookupEnvResult` 也保留为 `dotenv.LookupEnvResult` 的别名。
- **理由**: 避免一次性修改几十处调用方，降低回归风险。未来可渐进迁移。
- **权衡**: 存在两套 API 入口可能让新开发者困惑；通过注释明确“新代码优先使用 `dotenv` 包”来规避。

### Decision 4: 优先级与缓存保持在子包内部

- **选择**: `dotenv` 包内部维持 `envCache`、`envPriorityMode` 和互斥锁。
- **理由**: 缓存是 dotenv 层的状态，不应该由 `config.Config` 持有；独立后逻辑更内聚。

### Decision 5: `web_search_gemini_test.go` 改 import `internal/config/dotenv`

- **选择**: 真实网络测试通过 `dotenv.Getenv("GEMINI_API_KEY")` 读取。
- **理由**: `internal/tool` import `internal/config` 会触发循环依赖（`config` import `tool/mcp`，`tool/mcp` import `tool`）。子包 `dotenv` 不依赖 `tool`，可安全 import。

## Risks / Trade-offs

- [Risk] 引入 `godotenv` 后，`.env` 语法更宽松，可能导致旧值行为变化（例如 `"true"` 之前保留引号，现在去掉引号）。
  → Mitigation: 这是更正确的行为；同时检查 `isTruthyEnv` 等解析点，确保其本就会 trim 空格。update 文档说明。
- [Risk] `internal/config` 薄封装隐藏了 `dotenv` 包的来源报告与优先级 API，新开发者可能继续用错入口。
  → Mitigation: 在 `config/env.go`（或保留文件）顶部注释中明确推荐新代码直接 `import ".../internal/config/dotenv"`。
- [Risk] 循环依赖虽然解除，但 `config` 仍 `import tool/mcp`；未来若 `tool/mcp` 需要读 `.env` 可直接用 `dotenv` 包。
  → Mitigation: 本次只迁移 dotenv；不扩大 `tool/mcp` 职责。

## Migration Plan

1. 新增 `internal/config/dotenv/dotenv.go`，暴露缓存/加载/读取 API。
2. 新增 `internal/config/dotenv/dotenv_test.go`。
3. 修改 `go.mod` 添加 `github.com/joho/godotenv`。
4. 将 `internal/config/env.go` 改为转发层。
5. 更新 `internal/tool/web_search_gemini_test.go` 使用 `dotenv.Getenv`。
6. 运行 `go build ./...` 与 `go test ./internal/config/... ./internal/tool/... ./cmd/server/...`。
7. 提交 Git，更新 ROADMAP，归档 OpenSpec。
