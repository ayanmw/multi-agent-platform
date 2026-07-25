## 1. Database & Config Foundation

- [ ] 1.1 Add `LLM_PROVIDERS` parsing to `internal/config/config.go` with JSON array structure and legacy fallback.
- [ ] 1.2 Add migration v29 for `llm_providers` table.
- [ ] 1.3 Add migration v30 for `llm_models` table.
- [ ] 1.4 Implement `pkg/db/llm.go` CRUD: `InsertProvider`, `ListProviders`, `UpdateProviderSyncStatus`, `InsertOrReplaceModel`, `ListModels`, `GetModel`, `UpdateModel`, `MarkModelsMissing`.

## 2. Provider & Model Management Backend

- [ ] 2.1 Implement `internal/llm/provider_manager.go`: provider config, HTTP `/v1/models` discovery per type, sync orchestration.
- [ ] 2.2 Implement `internal/llm/model_service.go`: startup merge of `DefaultProfiles()`, `cfg.LLMModel`, `cfg.Models`, discovery results; persistence; field overwrite protection.
- [ ] 2.3 Modify `internal/llm/model_profile.go`/`ModelRegistry` to support provider-scoped model names.
- [ ] 2.4 Adjust `internal/llm/provider_factory.go` `CreateProviderFromConfig` to look up provider/type/endpoint/api_key from DB-backed structures.
- [ ] 2.5 Update `cmd/server/main.go` startup flow: construct `ProviderManager`, run initial discovery/merge, load `ModelRegistry` from persistence.
- [ ] 2.6 Implement `cmd/server/model_api.go` with endpoints: `GET /api/providers`, `POST /api/providers/{name}/sync`, `GET /api/models/prices`, `PUT /api/models/prices/{provider}/{model}`.
- [ ] 2.7 Delete `cmd/server/model_price_api.go` and update `cmd/server/server.go` route registration.

## 3. Frontend Upgrades

- [ ] 3.1 Create `web/v2/src/composables/useLLMModels.ts` replacing `useModelPrices.ts` with provider/model API calls.
- [ ] 3.2 Extend TypeScript types in `web/v2/src/types/` for provider and full model metadata.
- [ ] 3.3 Create `web/v2/src/components/LLMModelManager.vue` replacing `ModelPricesDialog.vue` with provider list, sync button, model table, inline editing for tier/prices/capabilities/context/output/fallback.
- [ ] 3.4 Update `App.vue`, `TopBar.vue` to open the new manager and rename button label.
- [ ] 3.5 Delete `ModelPricesDialog.vue` and old `useModelPrices.ts`.

## 4. Testing & Verification

- [ ] 4.1 Add unit tests for `ProviderManager` discovery merge logic with mocked HTTP client.
- [ ] 4.2 Add unit tests for `ModelService` overwrite-protection and missing-model marking.
- [ ] 4.3 Run `go build ./...` and `go test ./...`; fix failures.
- [ ] 4.4 Run mock regression `scripts/cases-regression.sh` and ensure 21/21 PASS.
- [ ] 4.5 Run frontend `npm run typecheck` / `npm run build` in `web/v2`; fix type errors.

## 5. Documentation & Git

- [ ] 5.1 Update `.env.example` with `LLM_PROVIDERS` sample and notes.
- [ ] 5.2 Update `roadmaps/ROADMAP.md` to mark Phase LLM Provider Model Management as completed.
- [ ] 5.3 Commit all changes with message `Phase LLM Provider Model Management: 实现 Provider 发现、Model 持久化与前端 Model 管理`.
