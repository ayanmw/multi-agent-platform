## Why

后端虽已落地 provider/model 持久化与 REST API，但当前系统存在两层不透明：
1. **数据不透明**：`internal/llm/model_profile.go` 中硬编码了 `DefaultProfiles()` 数十个模型，Router 在选择模型时优先依赖这些静态预设，而不是当前已配置/已发现的实际模型列表；当用户未配置某些 provider 时，这些模型条目依然存在并可能进入候选池，导致路由失败。
2. **前端不透明**：`AgentConfig.vue` 仍是固定 model 文本输入，没有“单模型固定 / 多模型自动路由”两种模式的显式切换，也看不到实际可用模型、provider 健康状态、同步刷新入口，Router 的选择结果对用户完全黑盒。

因此需要一个变更，让 Router **只从实际可用模型池** 中做选择，并在前端提供清晰的模式切换与模型可见性。

## What Changes

- **Router 实际数据驱动**：`Router.Select` 不再读取 `DefaultProfiles()` 做候选模型，而是从 `ModelRegistry` 的 **实际已加载模型** 中派生候选池。
- **缺失/未配置模型自动排除**：候选模型必须存在对应的 `llm_models` 记录且 `missing=false`，且属于 healthy/已配置的 provider；未配置 provider 的硬编码模型不进入候选池。
- **可回退兜底**：当实际可用池为空或全部失败时，明确回退到 legacy/default 模型并发出 `router_fallback_default` 事件，而不是静默失败。
- **Agent 运行配置新增两种模式**：
  - **single_model**（默认）：固定使用 `model` 字段；Router 完全跳过，不对 input 做 tier 分类，也不替换模型。
  - **auto_route**：启用多模型自动路由；按实际可用池、intent、tier、budget、RPM 选择模型。
- **前端运行配置 UI**：`AgentConfig.vue` 增加模式选择器（single_model / auto_route），模式不同时分别展示：
  - single_model：searchable 模型下拉框（基于 `/api/models/prices` 的实际模型）。
  - auto_route：首选 tier 下拉、预算上限、是否允许自动降级开关、实际可用池摘要。
- **模型管理页面**：`LLMModelManager.vue` 替换旧 `ModelPricesDialog`，展示按 provider 分组的实际模型列表、provider 健康与同步按钮、价格/能力/tier 编辑；新增 `/api/providers/{name}/sync` 触发入口。
- **运行时可观测**：每次 Router 选择发出 `model_selected` 事件，包含 `mode`、`requested_tier`、`actual_model`、`reason`（budget/fallback/tier）；`task_started` 等事件携带 `selected_mode`。

## Capabilities

### New Capabilities
- `model-auto-routing`: 让 Router 基于实际可用模型池做动态模型选择，并支持事件化可观测。
- `frontend-model-visibility`: 前端模型管理、provider 同步、运行配置模式切换。
- `router-mode-selection`: Agent 运行时显式选择 single_model（默认固定模型）或 auto_route（多模型自动路由）。

### Modified Capabilities
- `llm-provider-management`（已归档 `openspec/changes/archive/2026-07-26-llm-provider-model-management/specs/llm-provider-management/spec.md`）: 需求扩展——provider sync 必须提供 REST endpoint 供前端触发，且 sync 结果要写 model 可用性。
- `model-price-management`（已归档 `openspec/changes/archive/2026-07-26-llm-provider-model-management/specs/model-price-management/spec.md`）: 需求扩展——model 价格与元数据编辑的对象从单段 model 名升级为 `provider/model` 全名，且 price list 只返回实际加载的模型。

## Impact

- 后端：`internal/llm/router.go`、`internal/llm/model_profile.go`、`internal/llm/model_service.go`、`internal/runtime/engine.go`、`cmd/server/model_api.go`、`pkg/event/event.go`（新增事件类型）。
- 前端：`web/v2/src/components/AgentConfig.vue`、`web/v2/src/components/TopBar.vue`、`web/v2/src/App.vue`；新增 `LLMModelManager.vue`；新增/更新 `composables/useLLMModels.ts`、`composables/useProviders.ts`、`types/llm.ts`。
- 配置：`.env` 的 `LLM_MODEL` 继续作为 **single_model 模式默认值**；`MODEL_TIER_*` 只在 auto_route 模式下生效。
- 行为变化：**BREAKING** — 默认行为从“Router 永远参与并可能替换模型”改为“默认 single_model 固定模型，不路由”；已有外部脚本依赖 Router 自动替换的会受影响。
