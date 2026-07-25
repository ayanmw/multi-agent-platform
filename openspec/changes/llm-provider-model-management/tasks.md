## 1. Provider 接口扩展（低风险优先）

- [ ] 1.1 在 `internal/llm` Provider 接口增加 `ListModels(ctx context.Context) ([]ModelInfo, error)`。
- [ ] 1.2 `OpenAIProvider.ListModels()` 用 endpoint `GET /models` 拉取并解析 OpenAI-compatible 格式。
- [ ] 1.3 `DeepSeekProvider` / `SelfHostedProvider` 复用或委托 `OpenAIProvider.ListModels()`。
- [ ] 1.4 `AnthropicProvider` / `GeminiProvider` 提供 stub 实现：返回空列表并记录 "unsupported model discovery" 警告。
- [ ] 1.5 `MockProvider.ListModels()` 返回内置脚本对应的模型列表（ID 为 caseID，Provider 为 `mock`）。

## 2. Database & Config Foundation

- [ ] 2.1 添加 `MODEL_TIER_*` 通配符解析到 `internal/config/config.go`，生成 `ModelTierMapping map[string]string`。
- [ ] 2.2 添加 `LLM_PROVIDERS` JSON 数组解析到 `internal/config/config.go`（类型 `[]ProviderConfig`），未配置时回退到旧 `LLM_ENDPOINT/API_KEY/MODEL` 合成 `default` Provider。
- [ ] 2.3 保留现有 `cfg.Models []ModelConfig` 解析，作为显式静态模型补充来源。
- [ ] 2.4 添加 migration v29 创建 `llm_providers` 表（name/type/endpoint/healthy/last_sync_at/last_sync_error，不含 api_key）。
- [ ] 2.5 添加 migration v30 创建 `llm_models` 表（provider_name/model_id/display_name/tier/capabilities/input_price/output_price/max_context_window/max_output_tokens/fallback_model/rate_limit_rpm/avg_latency_ms/missing/created_at/updated_at）。
- [ ] 2.6 实现 `pkg/db/llm.go` CRUD：`InsertOrReplaceProvider`, `ListProviders`, `UpdateProviderSyncStatus`, `InsertOrReplaceModel`, `GetModel`, `ListModels`, `ListModelsByProvider`, `UpdateModel`, `MarkModelsMissingForProvider`。

## 3. Provider & Model Management Backend

- [ ] 3.1 实现 `internal/llm/provider_manager.go`：并发 `SyncAll(ctx)` / `SyncProvider(ctx, name)`，对每个 Provider 调用 `ListModels`，处理超时/错误，写入 DB，标记 missing。
- [ ] 3.2 实现 `internal/llm/model_service.go`：启动合并 `DefaultProfiles()`、`cfg.LLMModel`、`cfg.Models`、Provider 发现结果；调用 `ProfileResolver` 优先级；保证用户可编辑字段不被覆盖。
- [ ] 3.3 修改 `internal/llm/model_profile.go` / `ModelRegistry`：支持 provider-scoped model name `{provider}/{model_id}` 作为 key；`StaticRegistryFromProfiles` 测试 helper。
- [ ] 3.4 实现 `ProfileResolver`（含 `resolveTier` 通配符匹配），合并策略：`.env override` → `Provider metadata` → `DefaultProfiles()` → `conservative fallback`。
- [ ] 3.5 调整 `internal/llm/provider_factory.go`：`CreateProviderFromConfig` 从 DB/ProviderConfig 查找 provider/type/endpoint/api_key；支持全名 `provider/model` 和短名匹配。
- [ ] 3.6 更新 `cmd/server/main.go` 启动流：构造 `ProviderManager`，先同步种子模型，再异步 Provider 发现，最后从 DB 加载 `ModelRegistry`；失败不阻塞启动。
- [ ] 3.7 实现 `cmd/server/model_api.go`：
  - `GET /api/providers`（不含 api_key）
  - `POST /api/providers/{name}/sync`
  - `GET /api/models/prices`
  - `PUT /api/models/prices/{provider}/{model}`
- [ ] 3.8 删除 `cmd/server/model_price_api.go`，更新 `cmd/server/server.go` 路由注册。

## 4. Engine Cost & AgentConfig 适配

- [ ] 4.1 修改 `internal/runtime/engine.go`：`think()` 路由决策后拿到最终 `ModelProfile`，按 `usage` 和 `InputPrice` / `OutputPrice` 计算实际成本；price 为 0 时标记 `cost_unknown`。
- [ ] 4.2 修改 `web/v2/src/components/AgentConfig.vue`：模型选择从下拉框改为 searchable；数据源来自 `/api/models/prices` 的 `{provider}/{model_id}`；支持空值表示自动路由。
- [ ] 4.3 保证 `AgentRunner` / `MockProvider` 在模型名为 `provider/caseID` 时仍能正确匹配 caseID（取最后一段）。

## 5. Frontend Upgrades

- [ ] 5.1 创建 `web/v2/src/composables/useLLMModels.ts` 替代 `useModelPrices.ts`。
- [ ] 5.2 在 `web/v2/src/types/` 新增 `llm.ts`（Provider、ModelProfile、SyncStatus）。
- [ ] 5.3 创建 `web/v2/src/components/LLMModelManager.vue` 替代 `ModelPricesDialog.vue`：Provider 列表 + Sync 按钮 + Model 表格 + 内联编辑 tier/prices/capabilities/context/output/fallback。
- [ ] 5.4 更新 `TopBar.vue` 与 `App.vue`：按钮 label 改为 "LLM Models"，弹窗改开 LLMModelManager。
- [ ] 5.5 删除 `ModelPricesDialog.vue` 与 `useModelPrices.ts`。

## 6. Testing & Verification

- [ ] 6.1 添加 `ProviderManager` 发现合并逻辑单元测试（mock Provider + fake Store）。
- [ ] 6.2 添加 `ModelService` 覆盖保护与 missing 标记单元测试。
- [ ] 6.3 添加 `OpenAIProvider.ListModels` 解析测试。
- [ ] 6.4 运行 `go build ./...` 与 `go test ./...`；全绿后提交。
- [ ] 6.5 运行 `scripts/cases-regression.sh` 确保 21/21 PASS。
- [ ] 6.6 运行 `web/v2 npm run typecheck` / `npm run build`；通过。

## 7. Documentation & Git

- [ ] 7.1 更新 `.env.example`：新增 `LLM_PROVIDERS`、`MODEL_TIER_*` 示例，保留旧字段说明。
- [ ] 7.2 更新 `roadmaps/ROADMAP.md`，标记 Phase LLM Provider Model Management 完成。
- [ ] 7.3 提交所有变更：消息 `Phase LLM Provider Model Management: 实现 Provider 发现、Model 持久化与前端 Model 管理`。
