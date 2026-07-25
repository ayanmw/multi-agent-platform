## Why

当前项目仅支持从 .env 静态读取 LLM 的 `endpoint` / `api_key` / `model`，并通过内存中的 `ModelRegistry` 维护价格与能力。随着多模型路由、成本追踪、Agent 模型选择等能力扩展，模型画像（价格、上下文窗口、能力、fallback）成为核心配置；但这些画像分散在代码的 `DefaultProfiles()` 中，运行时仅通过 `/api/models/prices` 修改内存，重启即丢失，无法支持多 Provider 自动发现和持久化管理。因此需要一个统一、持久化的 LLM Provider 与 Model 管理子系统。

## What Changes

- 新增 `.env` 配置 `LLM_PROVIDERS`：以 JSON 数组声明 Provider（name、type、endpoint、api_key），支持 OpenAI-compatible / Anthropic / DeepSeek / 自托管四类 type。
- 服务启动时，按 Provider 调用 OpenAI-compatible `/v1/models` 自动发现可用模型，并与 `LLM_MODELS` 中显式声明的模型合并后持久化到数据库。
- 新增数据库表 `llm_providers`（运行时只读快照，.env 种子）与 `llm_models`（持久化模型画像），migration v29/v30。
- 新增 `internal/llm/provider_manager.go`：管理 Provider 配置与发现同步；`model_service.go`：管理模型画像的 CRUD、覆盖字段保护、刷新删除不存在模型。
- 替换原有只读内存 `ModelRegistry` 价格能力源：启动时从 DB 加载到 `ModelRegistry`，运行期 price/capability 修改直接写 DB，重启后恢复。
- 升级 REST API：新增 `GET /api/providers`、`POST /api/providers/:name/sync`、`GET/PUT /api/models`、直接升级 `/api/models/prices` 语义为 DB 持久化读取。
  - **BREAKING**: `/api/models/prices` 返回结构从 `{items, count, persistent, note}` 扩展为 `{items, count, persistent: true}`，且修改后持久化；旧 `runtime-only note` 不再适用。
- 升级前端：将顶部栏的 “模型价格管理” 弹窗升级为 “LLM Model 管理” 页面，展示 Provider 列表、发现状态、模型列表，支持价格 / tier / capabilities / max_context_window / max_output_tokens / fallback_model 编辑。
- 删除 `cmd/server/model_price_api.go` 内存价格接口，合并到新的 `cmd/server/model_api.go`。

## Capabilities

### New Capabilities

- `llm-provider-management`: 从 .env 加载多 Provider、提供列表/同步/发现状态 API。
- `llm-model-persistence`: 将模型画像持久化到 SQLite，启动合并 Provider 发现与静态配置，支持运行时编辑并持久化。

### Modified Capabilities

- `model-price-management`: 现有 `/api/models/prices` 从运行时内存管理升级为数据库持久化管理，前端从价格弹窗升级为模型管理页面。

## Impact

- **后端**: `internal/config/config.go`（新增 `LLM_PROVIDERS` 解析）、`pkg/db/migrate.go`（新增 v29/v30 migration）、`pkg/db/llm.go`（新增 DB 层）、`internal/llm/`（新增 provider_manager/model_service，修改 ModelRegistry 初始化）、`cmd/server/`（新增 model_api.go，删除 model_price_api.go，调整 main.go 启动初始化与 registerRoutes）。
- **前端**: `web/v2/src/composables/useModelPrices.ts` 升级为 `useLLMModels.ts`、`web/v2/src/components/ModelPricesDialog.vue` 升级为 `LLMModelManager.vue`、相关类型文件。`App.vue` / `TopBar.vue` 调用入口同步调整。
- **测试**: 新增 provider manager / model service 单元测试；mock 回归需确认 `ModelRegistry` 初始化路径不受影响。
- **兼容性**: 旧的单模型 `.env` 字段 `LLM_ENDPOINT`/`LLM_API_KEY`/`LLM_MODEL` 与 `LLM_MODELS` 继续兼容；`LLM_PROVIDERS` 为新增可选配置，未配置时行为等价旧路径。
