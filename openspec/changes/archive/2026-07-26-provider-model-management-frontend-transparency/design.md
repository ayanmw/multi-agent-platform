## Context

后端已完成 provider discovery、model persistence 与 REST API（`cmd/server/model_api.go`）。当前问题集中在：

1. **Router 候选池仍是静态硬编码**：`DefaultProfiles()` 包含数十个模型，与用户实际配置的 provider 无关；当 provider 不存在或 `missing=true` 时，Router 仍可能命中这些不可用的模型。
2. **Agent 运行配置没有模式概念**：`AgentRunSpec` 只有一个 `Model string`，Router 永远尝试覆盖它，导致用户无法明确“我就要用这个模型”。
3. **前端完全看不到实际可用模型列表**：`AgentConfig.vue` 是文本框；`ModelPricesDialog` 只读取价格，不理解 provider。

设计目标是在不破坏现有 mock 回归与数据迁移的前提下，把 Router 改成“只从实际已发现模型里选”，并把路由模式与模型可见性交由前端显式控制。

## Goals / Non-Goals

**Goals:**
- Router 候选池改为从 `ModelRegistry` 的实际加载记录中动态构建，排除 `missing=true` 与未配置 provider 的模型。
- 引入 `ModelSelectionMode`：`single_model`（默认）与 `auto_route`。`single_model` 下 Router 原样返回 `spec.Model`。
- 扩展 `AgentRunSpec` 与 `AgentConfig` DB schema 以存储模式、首选 tier、预算、是否允许降级。
- 前端 AgentConfig 根据模式切换 UI，并提供可刷新的模型选择器。
- 新增 `LLMModelManager.vue` 页面，支持 provider 同步、模型元数据/价格编辑。
- 每次模型选择生成可观测事件 `model_selected`。

**Non-Goals:**
- 不替换 LLM Provider 抽象层（仍走 `Provider` interface）。
- 不实现 provider API key 在前端可见或编辑（API key 仍只存在 `.env`/环境变量）。
- 不改动 RAG、Auth、gRPC 子系统。

## Decisions

### 1. Router 候选池从 `ModelRegistry` 派生，而不是 `DefaultProfiles()`
- **Rationale**: `DefaultProfiles()` 是“我们想要支持的模型全集”，而 `ModelRegistry` 才是“当前实际可用模型全集”。路由失败根因是候选池包含未配置模型。
- **实现**: `Router.Select` 内从注入的 `ModelRegistry` 取全量 profiles，再过滤：
  - `profile.Source != ""` 且 provider 在 `cfg.ProviderConfigs` 中存在（或 provider 为 `mock`/`default`）。
  - `profile.Missing == false`。
  - 对于 `Provider` 接口能返回 healthy 的 provider，可选要求 `Healthy == true`（第一次实现允许 un-healthy 进入候选但降级尝试，避免 sync 失败后彻底不可用；用日志/事件标记）。

### 2. 默认模式为 `single_model`
- **Rationale**: 兼容现有使用习惯与 most scripts。用户明确写 `model=deepseek-v4-flash` 时，系统不应悄悄换成别的模型。
- **实现**: `AgentRunSpec.ModelMode` 默认 `"single_model"`；`Engine.think` 只在 `mode == "auto_route"` 时调用 `Router.Select`。`engine.go` 内因 budget 检查所需的 `profile` 仍按 `spec.Model` 取。

### 3. `auto_route` 模式必须显式开启，并提供降级开关
- **Rationale**: 自动多模型选择是一把双刃剑，用户必须知情同意。
- **实现**: `AgentConfig.allow_auto_route` 改名为显式 `model_mode` enum。`allow_fallback` 控制当首选 tier 无可用模型时是否降 tier。

### 4. Agent 配置 schema 新增字段
- **Rationale**: 持久化运行偏好，避免每次都要 UI 重设。
- **实现**: `agents` 表已存在的 `preferred_model`、`preferred_tier`、`allow_auto_route`、`max_cost_usd` 需要语义升级：
  - `preferred_model` 保留作为 single_model 模式下的固定模型。
  - `allow_auto_route` boolean 升级为 `model_mode` text（`single_model` / `auto_route`），迁移脚本保留原 `true -> auto_route`、`false -> single_model`。
  - `preferred_tier` 在 auto_route 模式下作为首选 tier。
  - 新增 `allow_fallback` boolean default true。

### 5. 模型管理页面向导调用 `/api/providers/{name}/sync`
- **Rationale**: 手动同步比启动时自动全量同步更可控，避免前端黑盒等待。
- **实现**: 前端 `useProviders.ts` 封装 sync；`LLMModelManager.vue` 为每个 provider 显示 sync 按钮与 last_sync_at / last_sync_error。

### 6. 事件化可观测性
- **Rationale**: 白盒 Agent 要求每次外部不可见决策都产生事件。
- **实现**: `Router.Select` 返回 `{Model string, Mode string, Tier string, Reason string, Fallback bool}`，由 `Engine` 发出 `model_selected` event (type=llm/model_selected)。

## Risks / Trade-offs

- **[Risk] auto_route 模式下缺失所有候选模型导致运行失败** → **Mitigation**: `Router.Select` 在候选池为空时回退到 `spec.Model` 或 `cfg.LLMModel`，并 emit `router_fallback_default`；任务继续而非直接失败。
- **[Risk] 默认改为 single_model 后，旧内建 case 期望的自动 tier 选择失效** → **Mitigation**: mock 回归脚本使用 mock provider 与显式 model；若 case 系统提示依赖 auto_route，则设置 `preferred_model=""` 并 `model_mode=auto_route`。PR 前跑 `go test ./...` 与 `scripts/cases-regression.sh`。
- **[Risk] 前端 searchable model selector 在高模型数量下渲染慢** → **Mitigation**: 使用 virtual list 或按 provider 分组折叠；首版可简单 `<select>` + filter，后续优化。
- **[Risk] Provider sync 失败 UI 反馈不及时** → **Mitigation**: sync endpoint 返回 202 异步 + 事件 `provider_sync_started/completed/failed`；前端订阅这些事件刷新列表。

## Migration Plan

1. 数据库迁移：v32 将 `agents.allow_auto_route` boolean 改为 `model_mode` text，转换旧数据。
2. 后端代码：`router.go` 引入 `ModelRegistry` 过滤 + `ModelSelectionMode`；`engine.go` 按模式决定是否路由；`api.go` 更新 `AgentRunSpec` 解析。
3. 前端代码：更新 `AgentConfig.vue` 与类型；新增 `LLMModelManager`。
4. 验证：mock 回归 21/21、go test、真实 LLM smoke（至少 Part A）。
5. 文档：更新 `ROADMAP.md` 与 `.env.example` 关于 `model_mode` 的说明。

## Open Questions

1. 是否需要在 `agents` 表新增 `model_mode` 字段时保留 `allow_auto_route` 作为 deprecated 列？→ 建议直接替换，v32 down migration 恢复 boolean。
2. `auto_route` 模式下 `spec.Model` 是否允许为空，从而彻底由 Router 决定？→ 建议允许为空，空时 fallback 到 `cfg.LLMModel`。
3. 前端模型管理页面是否放在 `/ui/v2/models` 独立路由还是在现有 Manage Flyout 中？→ 建议先放入 Manage Flyout/Tab，与 Cron/Skills 并列，保持 v2 一致性。
