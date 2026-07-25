## Context

当前 LLM 配置分散在三处：

1. `.env` 中的 `LLM_ENDPOINT` / `LLM_API_KEY` / `LLM_MODEL` 单模型字段，以及可选的 `LLM_MODELS` 多模型 JSON 数组。
2. `internal/llm/model_profile.go` 中的 `DefaultProfiles()` 硬编码模型画像（价格、能力、tier）。
3. `cmd/server/model_price_api.go` 中 `/api/models/prices` 只读内存 registry，修改运行时生效、重启丢失。

随着多模型路由（Phase 6 Router）和成本追踪的推广，运行时需要一个持久化、可观测、可手工覆盖的模型目录。本变更通过引入 `LLM_PROVIDERS` 配置和模型发现机制，把模型画像从“内存/硬编码”迁移到“数据库持久化 + .env 种子”。

## Goals / Non-Goals

**Goals:**

- 支持在 `.env` 中声明多个 LLM Provider，启动时按 `/v1/models` 自动发现模型。
- 将发现的模型与 `LLM_MODELS` 中显式声明的模型合并，持久化到 `llm_models` 表。
- 提供 REST API 查询 Provider 列表、触发手动发现同步、查询/修改模型画像。
- 运行时 `ModelRegistry` 从数据库加载，保证 Agent 路由、成本计算使用同一套画像。
- 前端 ModelPrice 弹窗升级为 LLM Model 管理页面，可查看/编辑模型的价格、能力、上下文窗口等。

**Non-Goals:**

- 不支持运行时添加/修改/删除 Provider（endpoint、api_key 仍来自 .env，仅同步发现）。
- 不实现自动周期性刷新（本次只做启动发现 + 手动刷新 API）。
- 不从模型元数据自动推断 capabilities（名命规则、vendor schema 等），第一期由用户在 UI 维护。
- 不引入模型的删除 API；发现刷新时仅将“不再返回”的模型标记为 `missing`，不物理删除，以免丢失用户覆盖的 price 等信息。

## Decisions

### 1. Provider 配置格式：JSON 数组 `LLM_PROVIDERS`

- **选择**: 与 `MCP_SERVERS` 保持一致，使用 JSON 数组声明 Provider：`{"name":"deepseek","type":"openai","endpoint":"...","api_key":"..."}`。
- **理由**: 项目已有 JSON 数组配置先例，便于前后端解析；单 Provider 场景可配一个元素，兼容现有单模型 `.env`。
- **未选**: 索引环境变量（`LLM_PROVIDER_0_NAME`）。虽然对不擅手写 JSON 的用户更友好，但会引入额外解析逻辑，与现有 `LLM_MODELS` 风格重复；本次先保持统一。

### 2. Provider 不持久化，只作为发现入口；模型画像持久化

- **选择**: `llm_providers` 表仅在启动时写入 .env 中的 Provider 作为只读快照；运行时发现到的 model 写入 `llm_models`，api_key 不存 DB（DB 中只存 name、type、endpoint、health、last_sync_at）。
- **理由**: 避免把 secret 写入 SQLite 带来的安全风险；Provider 的变更仍通过 .env + 重启生效，符合“第一期只读管理”的约束。
- **权衡**: 删除 .env 中 Provider 不会自动从 DB 删除 model，只有当该 Provider 同步失败或同步后不再返回某 model 时，该 model 才会被标记为 `missing`，保护用户手工 price 覆盖不丢失。

### 3. 模型发现策略：启动同步 + 手动刷新 API

- **选择**: 启动时串行对每个 Provider 调用 `GET {endpoint}/models`；失败后只记录日志不影响启动。新增 `POST /api/providers/:name/sync` 供手动触发刷新。
- **理由**: 实现简单，避免后台定时器引入额外 goroutine 生命周期管理；手动刷新足够满足“模型上线/价格调整后更新”的诉求。
- **权衡**: 新模型上线后不能自动感知，需手动点击刷新或重启 server。

### 4. 能力字段完全由用户维护

- **选择**: `/v1/models` 返回的模型列表中，OpenAI 标准格式只含 `id`、`object`、`created` 等字段，不包含价格、context window、reasoning/vision 等。我们不根据 id 名命规则推断能力，而是把 capabilities 作为可编辑字段，默认在首次发现时置空或从现有 `DefaultProfiles()` 补默认值。
- **理由**: 名命规则推断容易误判；把控制权交给用户更清晰，也支持自托管模型以任意 id 接入。
- **额外**: 对于已知默认 model（deepseek-v4-flash / pro），在“种子写入”阶段仍用 `DefaultProfiles()` 填充 price/window，保证新部署开箱可用。

### 5. 升级 `/api/models/prices` 语义，而不是保留旧接口

- **选择**: 删除 `cmd/server/model_price_api.go`，在 `cmd/server/model_api.go` 中实现 `/api/models/prices`（GET 从 DB 读全部 model、PUT 更新 DB 指定 model 的 price）。
- **理由**: 旧接口字段已从 `ModelPriceItem` 扩展为完整 model 画像，且持久化语义改变，保留兼容并无必要；前端同步升级。
- **BREAKING**: 旧接口返回的 `persistent: false` 和 `note` 字段被移除；新接口返回 `persistent: true`。

### 6. 模型主键与更新策略

- **选择**: `llm_models` 表以 `(provider_name, model_id)` 为复合主键（或 `id` 单列并设置 UNIQUE(provider_name, model_id)）。同一 model id 可出现在多个 Provider 下。
- **理由**: 不同 Provider 可能命名相同（如本地 ollama 和 OpenAI 都有 `gpt-4o`），价格、endpoint、可用性都可能不同，必须按 Provider 区分。
- **API/Registry 展示**: 在内部 `ModelRegistry` 中 model 名保持 `{provider_name}/{model_id}`，以避免冲突；Agent 运行时若选择模型，工厂可解析此全名。

### 7. 旧版 `LLM_ENDPOINT` / `LLM_API_KEY` / `LLM_MODEL` 兼容策略

- **选择**: 当未配置 `LLM_PROVIDERS` 时，自动根据旧字段生成一个 name 为 `default`、type 为 `openai` 的 Provider 快照，保证启动发现/DB 加载路径统一。
- **理由**: 向后兼容且不引入两套逻辑。

## Risks / Trade-offs

- **[Risk] Provider `/v1/models` 不可用导致无法发现模型** → **Mitigation**: 若发现失败，仍把 `LLM_MODELS` 与 `cfg.LLMModel` 作为显式模型写 DB；Provider last_sync_error 记录错误，前端展示同步失败原因，不影响启动。
- **[Risk] DB 中存在同名不同 Provider 的 model，旧代码按简单 model name 查找失败** → **Mitigation**: `ModelRegistry` 中以 `{provider}/{id}` 作为唯一名；`CreateProviderFromConfig` 在按 `modelName` 查找时同时支持全名和短名匹配（短名匹配第一个 Provider）。
- **[Risk] Secret 意外写入 DB** → **Mitigation**: `llm_providers` 表只存 endpoint/type/name，不存 api_key；任何 Provider 写库逻辑 review 禁止写入 key。
- **[Risk] 自动发现误覆盖用户手工修改的字段** → **Mitigation**: 发现同步只更新 `id`、新增行；已有行的 price / capabilities / max_* / fallback 等用户可改字段保留原值；仅当模型不再返回时设置 `missing=true`，不修改其它字段。
- **[Risk]Anthropic /v1/models 接口与 OpenAI 格式差异** → **Mitigation**: `ProviderManager` 按 `type` 分发解析器；第一期为每种 type 独立实现 `ListModels()`，OpenAI-compatible / DeepSeek / self-hosted 共用同一解析器，Anthropic 单独实现。

## Migration Plan

1. 发布前：在 `.env.example` 中新增 `LLM_PROVIDERS` 示例，但不移除旧字段。
2. 数据库升级：通过 migration v29/v30 自动创建 `llm_providers` / `llm_models` 表；旧 `cost_records` 中的 `model` 字段不需要迁移（仍存短名）。
3. 代码升级：
   - 部署新版本；启动时自动把旧 `.env` 字段同步为 `default` Provider，并将 `cfg.LLMModel` / `cfg.Models` 写入 `llm_models`。
   - 对配置的 Provider 尝试 `/v1/models` 发现，成功则合并入库。
   - 原 `/api/models/prices` 客户端可能因字段缺失报错；本次为 BREAKING，需要同时部署新版前端。
4. 回滚：若发现严重问题，回滚到上一版本二进制；DB 中 `llm_providers` / `llm_models` 表不会被旧代码读取，也不会影响旧代码的内存 registry（旧代码依赖 `.env` 字段）。

## Open Questions

- 是否需要在 `llm_models` 中保存 `/v1/models` 返回的原始 JSON 以便未来做更丰富的自动推断？（本次不保存，保持字段精炼。）
- 对于 `LLM_MODELS` 中声明了但 Provider 发现未返回的模型，是标记 `missing=true` 还是直接删除？本次选择标记 `missing`，保留用户覆盖。
- 是否需要把 `ModelRegistry` 的所有运行时读都加缓存，避免每次 Agent run 查 DB？`ModelRegistry` 本身已是内存结构，启动加载后由写 API 同步更新，无需额外缓存。
