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
- Provider 接口新增 `ListModels()`，各 Provider 类型各自实现，便于未来接入 Anthropic / Gemini 等异构接口。
- Engine 成本计算按实际路由命中的 `ModelProfile` 单价和 API 返回 usage 精确计算，unknown cost 显式标记。

**Non-Goals:**

- 不支持运行时添加/修改/删除 Provider（endpoint、api_key 仍来自 .env，仅同步发现）。
- 不实现自动周期性刷新（本次只做启动发现 + 手动刷新 API）。
- 不从模型元数据自动推断 capabilities（名命规则、vendor schema 等），第一期由用户在 UI 维护。
- 不引入模型的删除 API；发现刷新时仅将“不再返回”的模型标记为 `missing`，不物理删除，以免丢失用户覆盖的 price 等信息。

## Decisions

### 1. Provider 配置格式：JSON 数组 `LLM_PROVIDERS`

- **选择**: 与 `MCP_SERVERS` 保持一致，使用 JSON 数组声明 Provider：`{"name":"deepseek","type":"openai","endpoint":"https://aicoding.dobest.com/v1","api_key":"..."}`。
- **理由**: 项目已有 JSON 数组配置先例，便于前后端解析；单 Provider 场景可配一个元素，兼容现有单模型 `.env`。
- **未选**: 索引环境变量（`LLM_PROVIDER_0_NAME`）。虽然对不擅手写 JSON 的用户更友好，但会引入额外解析逻辑，与现有 `LLM_MODELS` 风格重复；本次先保持统一。

### 2. Provider 不持久化，只作为发现入口；模型画像持久化

- **选择**: `llm_providers` 表仅在启动时写入 .env 中的 Provider 作为只读快照；运行时发现到的 model 写入 `llm_models`，api_key 不存 DB（DB 中只存 name、type、endpoint、health、last_sync_at）。
- **理由**: 避免把 secret 写入 SQLite 带来的安全风险；Provider 的变更仍通过 .env + 重启生效，符合“第一期只读管理”的约束。
- **权衡**: 删除 .env 中 Provider 不会自动从 DB 删除 model，只有当该 Provider 同步失败或同步后不再返回某 model 时，该 model 才会被标记为 `missing`，保护用户手工 price 覆盖不丢失。

### 3. 模型发现策略：启动异步同步 + 手动刷新 API + 超时/失败缓存

- **选择**: 启动时对每个 Provider 在独立 goroutine 中调用 `GET {endpoint}/models`，配置 10 秒超时；失败后只记录日志不影响启动，并继续使用 `llm_models` 表中已有模型（即上次缓存）。新增 `POST /api/providers/:name/sync` 供手动触发刷新。
- **理由**: 避免启动阶段串行网络请求阻塞 server；DB 已有模型可直接作为缓存 fallback。
- **权衡**: 新模型上线后不能自动感知，需手动点击刷新或重启 server。

### 4. Provider 接口扩展 `ListModels()`

- **选择**: `internal/llm` 下 Provider 接口增加 `ListModels(ctx context.Context) ([]ModelInfo, error)`。`OpenAIProvider` / `DeepSeekProvider` / `SelfHostedProvider`（统一按 OpenAI-compatible 解析）各自实现；`AnthropicProvider` / `GeminiProvider` 留 stub 并在unsupported log 警告，本次只保证 OpenAI-compatible 路径可用；`MockProvider` 返回内置脚本对应的模型列表。
- **ModelInfo 字段**: `ID`, `DisplayName`, `Provider`, `Capabilities`, `ContextLen`, `InputPrice`, `OutputPrice`, `RateLimitRPM`。
- **理由**: 把发现逻辑下放到 Provider，便于不同 vendor 的模型列表 API 格式差异处理；ProviderManager 只做并发编排与合并。

### 5. 能力字段完全由用户维护

- **选择**: `/v1/models` 返回的模型列表中，OpenAI 标准格式只含 `id`、`object`、`created` 等字段，不包含价格、context window、reasoning/vision 等。我们不根据 id 名命规则推断能力，而是把 capabilities 作为可编辑字段，默认在首次发现时置空或从现有 `DefaultProfiles()` 补默认值。
- **理由**: 名命规则推断容易误判；把控制权交给用户更清晰，也支持自托管模型以任意 id 接入。
- **额外**: 对于已知默认 model（deepseek-v4-flash / pro），在“种子写入”阶段仍用 `DefaultProfiles()` 填充 price/window，保证新部署开箱可用。

### 6. ProfileResolver 优先级（价格/能力/上下文窗口）

- **选择**: 发现并写入 `llm_models` 时，字段解析优先级如下：
  1. `.env` 中显式配置的价格/上下文窗口（如 `LLM_MODELS` 或未来 `MODEL_OVERRIDE_*`）。
  2. Provider `ListModels` 返回的元数据。
  3. 内置 `DefaultProfiles()` 中的默认值。
  4. 保守 fallback（capabilities 留空，context/output 给 0，price 给 0）。
- **原有行保护**: 重新发现已存在的模型时，不覆盖用户可编辑字段（price、capabilities、max_*、fallback、rate_limit_rpm、avg_latency_ms）；仅更新 `display_name`（如 Provider 返回）、`missing=false`、同步时间。

### 7. Model Tier 通配符映射 `MODEL_TIER_*`

- **选择**: 新增 `cfg.ModelTierMapping map[string]string`，键支持 glob 通配符（如 `claude-opus-*`）。发现时调用 `resolveTier(modelID, mapping)` 匹配，未命中则根据上下文窗口/价格推断（`small`/`standard`/`premium`/`efficient`），仍无把握则默认 `standard`。
- **配置示例**:
  ```
  MODEL_TIER_claude-opus-*=premium
  MODEL_TIER_claude-sonnet-*=standard
  MODEL_TIER_deepseek-v4-flash=efficient
  ```

### 8. 升级 `/api/models/prices` 语义，而不是保留旧接口

- **选择**: 删除 `cmd/server/model_price_api.go`，在 `cmd/server/model_api.go` 中实现 `/api/models/prices`（GET 从 DB 读全部 model、PUT 更新 DB 指定 model 的 price）。
- **理由**: 旧接口字段已从 `ModelPriceItem` 扩展为完整 model 画像，且持久化语义改变，保留兼容并无必要；前端同步升级。
- **BREAKING**: 旧接口返回的 `persistent: false` 和 `note` 字段被移除；新接口返回 `persistent: true`。

### 9. 模型主键与更新策略

- **选择**: `llm_models` 表以 `(provider_name, model_id)` 为复合主键（或 `id` 单列并设置 UNIQUE(provider_name, model_id)）。同一 model id 可出现在多个 Provider 下。
- **理由**: 不同 Provider 可能命名相同（如本地 ollama 和 OpenAI 都有 `gpt-4o`），价格、endpoint、可用性都可能不同，必须按 Provider 区分。
- **API/Registry 展示**: 在内部 `ModelRegistry` 中 model 名保持 `{provider_name}/{model_id}`，以避免冲突；Agent 运行时若选择模型，工厂可解析此全名。

### 10. Engine 成本计算按实际 ModelProfile

- **选择**: `engine.go` 的路由决策后拿到最终选中的 `ModelProfile`（已含最新 price/capabilities）。调用成功后按 `usage` 和 `profile.InputPrice` / `profile.OutputPrice` 计算成本；若 price = 0 且非 mock，则标记 `cost_unknown` 而非给出 0 成本。
- **理由**: 模型动态化后，同一 model 的价格可能被用户编辑，成本估算必须实时反映 DB 值。

### 11. 旧版 `LLM_ENDPOINT` / `LLM_API_KEY` / `LLM_MODEL` 兼容策略

- **选择**: 当未配置 `LLM_PROVIDERS` 时，自动根据旧字段生成一个 name 为 `default`、type 为 `openai` 的 Provider 快照，保证启动发现/DB 加载路径统一。
- **理由**: 向后兼容且不引入两套逻辑。

### 12. 测试 Helper

- **选择**: 新增 `llm.StaticRegistryFromProfiles(profiles ...*ModelProfile) *ModelRegistry` 供测试直接构造 registry；`MockProvider.ListModels()` 返回内置脚本对应的模型列表。
- **理由**: 避免测试依赖真实 `/v1/models` 网络调用，保证回归脚本稳定。

## Risks / Trade-offs

- **[Risk] Provider `/v1/models` 不可用导致无法发现模型** → **Mitigation**: 若发现失败，仍把 `LLM_MODELS` 与 `cfg.LLMModel` 作为显式模型写 DB；Provider last_sync_error 记录错误，前端展示同步失败原因，不影响启动；DB 已有模型可直接作为缓存。
- **[Risk] DB 中存在同名不同 Provider 的 model，旧代码按简单 model name 查找失败** → **Mitigation**: `ModelRegistry` 中以 `{provider}/{id}` 作为唯一名；`CreateProviderFromConfig` 在按 `modelName` 查找时同时支持全名和短名匹配（短名匹配第一个 Provider）。
- **[Risk] Secret 意外写入 DB** → **Mitigation**: `llm_providers` 表只存 endpoint/type/name，不存 api_key；任何 Provider 写库逻辑 review 禁止写入 key。
- **[Risk] 自动发现误覆盖用户手工修改的字段** → **Mitigation**: 发现同步只更新 `id`、新增行；已有行的 price / capabilities / max_* / fallback 等用户可改字段保留原值；仅当模型不再返回时设置 `missing=true`，不修改其它字段。
- **[Risk] Anthropic /v1/models 接口与 OpenAI 格式差异** → **Mitigation**: `Provider.ListModels()` 按 `type` 独立实现；第一期 OpenAI-compatible / DeepSeek / self-hosted 共用同一解析器，Anthropic 单独 stub 并 log 警告。
- **[Risk] 模型名前缀影响 MockProvider 的 caseID 匹配** → **Mitigation**: 回归脚本与 `MockProvider.selectScript` 同时支持 `provider/caseID` 和裸 `caseID` 匹配；`AgentRunner` 调用时若用户选的是全名，截取最后一段作为 caseID。

## Migration Plan

1. 发布前：在 `.env.example` 中新增 `LLM_PROVIDERS` 示例，但不移除旧字段。
2. 数据库升级：通过 migration v29/v30 自动创建 `llm_providers` / `llm_models` 表；旧 `cost_records` 中的 `model` 字段不需要迁移（仍存短名）。
3. 代码升级：
   - 部署新版本；启动时自动把旧 `.env` 字段同步为 `default` Provider，并将 `cfg.LLMModel` / `cfg.Models` 写入 `llm_models`。
   - 对配置的 Provider 异步 `/v1/models` 发现，成功则合并入库。
   - 原 `/api/models/prices` 客户端可能因字段缺失报错；本次为 BREAKING，需要同时部署新版前端。
4. 回滚：若发现严重问题，回滚到上一版本二进制；DB 中 `llm_providers` / `llm_models` 表不会被旧代码读取，也不会影响旧代码的内存 registry（旧代码依赖 `.env` 字段）。

## Open Questions

- 是否需要在 `llm_models` 中保存 `/v1/models` 返回的原始 JSON 以便未来做更丰富的自动推断？（本次不保存，保持字段精炼。）
- 对于 `LLM_MODELS` 中声明了但 Provider 发现未返回的模型，是标记 `missing=true` 还是直接删除？本次选择标记 `missing`，保留用户覆盖。
- 是否需要把 `ModelRegistry` 的所有运行时读都加缓存，避免每次 Agent run 查 DB？`ModelRegistry` 本身已是内存结构，启动加载后由写 API 同步更新，无需额外缓存。
