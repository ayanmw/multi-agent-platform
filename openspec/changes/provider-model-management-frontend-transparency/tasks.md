## 1. Schema & Backend Foundation

- [x] 1.1 Add migration v32: rename `agents.allow_auto_route` boolean to `model_mode` text, add `allow_fallback` boolean default true; convert existing rows (`true -> auto_route`, `false -> single_model`).
- [x] 1.2 Update `internal/agent/agent.go` (`AgentConfig`) and `internal/runtime/engine.go` (`EngineConfig` / `AgentRunSpec`) with `ModelMode`, `PreferredTier`, `MaxCostUSD`, `AllowFallback` fields.
- [x] 1.3 Update `pkg/db/agent.go` CRUD to read/write new fields.
- [x] 1.4 Add new event constants in `pkg/event/event.go`: `llm/model_selected`, `llm/router_fallback_default`, `provider_sync_started`, `provider_sync_completed`, `provider_sync_failed`.

## 2. Model Registry Availability

- [x] 2.1 Modify `internal/llm/model_service.go` to track whether a profile comes from an actually configured provider or from static `DefaultProfiles()`/`LLM_MODELS`.
- [x] 2.2 Update `ModelRegistry` lookups / list methods to expose `AvailableProfiles()` that filters by `missing=false` and provider configured.
- [x] 2.3 Ensure `ModelRegistry` returns full `provider/model_id` as primary identity and continues to expose short-name aliases for backward compatibility.
- [ ] 2.4 Add unit tests for `AvailableProfiles()` filtering behavior.

## 3. Router Actual-Data Rewrite

- [x] 3.1 Refactor `internal/llm/router.go` `Select` to build candidates from `ModelRegistry.AvailableProfiles()` instead of `DefaultProfiles()`.
- [x] 3.2 Implement empty-pool fallback: return spec/default model, emit `llm/router_fallback_default` event.
- [x] 3.3 Implement `allow_fallback=false` behavior: when no candidate matches the requested tier, do not descend tier.
- [x] 3.4 Emit `llm/model_selected` event after every selection decision.
- [x] 3.5 Add router unit tests around configured-provider filtering, missing exclusion, fallback, and `allow_fallback=false`.

## 4. Engine Mode Integration

- [x] 4.1 Update `internal/runtime/engine.go` to read `AgentRunSpec.ModelMode`; only call `Router.Select` when `model_mode=auto_route`.
- [x] 4.2 Ensure cost/budget checks use the actually selected model's profile; in `single_model` mode this is the fixed `spec.Model` profile.
- [x] 4.3 Emit `model_selected` / `router_fallback_default` events from `Engine` when appropriate.
- [x] 4.4 Update mock provider tests and any engine tests that assume router always runs.

## 5. Backend API Enhancements

- [x] 5.1 Update `GET /api/models/prices` to return only models from configured providers (or explicit static declarations) and include `provider` / `model_id` identity.
- [x] 5.2 Verify `PUT /api/models/prices/{provider}/{model}` correctly handles full identity and disallows changing identifier fields.
- [x] 5.3 Ensure `POST /api/providers/{name}/sync` emits `provider_sync_started/completed/failed` events.
- [x] 5.4 Update agent REST handlers in `cmd/server/agent_api.go` (or equivalent) to accept and persist `model_mode`, `preferred_tier`, `max_cost_usd`, `allow_fallback`.

## 6. Frontend Types & Composables

- [x] 6.1 Update `web/v2/src/types/llm.ts` to include `ModelSelectionMode`, `LLMProvider`, `LLMModel`, and updated `AgentConfig` shape.
- [x] 6.2 Create `web/v2/src/composables/useProviders.ts` for `GET /api/providers` and `POST /api/providers/{name}/sync`.
- [x] 6.3 Update `web/v2/src/composables/useModelPrices.ts` to group by provider and use full `provider/model_id` identity.

## 7. Frontend UI

- [x] 7.1 Replace `ModelPricesDialog.vue` usage with new `LLMModelManager.vue` (or extend it) accessible from `TopBar.vue` / `ManageTabs`.
- [x] 7.2 Implement provider list with sync button, health badge, last sync timestamp, and error display in `LLMModelManager.vue`.
- [x] 7.3 Implement grouped model table in `LLMModelManager.vue` with inline editing for display_name, tier, capabilities, prices, context/output limits, fallback_model.
- [x] 7.4 Update `AgentConfig.vue` to add `model_mode` selector and conditional fields (`single_model` searchable model selector vs `auto_route` tier/budget/fallback controls).
- [x] 7.5 Wire `App.vue` to open `LLMModelManager` and update menu labels.

## 8. Verification & Documentation

- [ ] 8.1 Run `go test ./...` and fix failures.
- [ ] 8.2 Run `scripts/cases-regression.sh` and ensure 21/21 PASS (single_model default must not break mock scripts).
- [ ] 8.3 Run `scripts/real-llm-smoke.sh` Part A (white-box scenarios) to verify auto_route path.
- [ ] 8.4 Update `roadmaps/ROADMAP.md` to mark this change.
- [ ] 8.5 Update `.env.example` with comments for `MODEL_TIER_*` and model mode defaults.
