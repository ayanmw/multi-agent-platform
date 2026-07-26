## ADDED Requirements

### Requirement: Frontend can trigger provider discovery sync
The frontend SHALL provide a control that calls `POST /api/providers/{name}/sync` and reflects the provider's `healthy`, `last_sync_at`, and `last_sync_error` states.

#### Scenario: Manual sync from model manager
- **WHEN** the user clicks the sync button next to provider `deepseek` in `LLMModelManager.vue`
- **THEN** the frontend calls `POST /api/providers/deepseek/sync`, shows a loading state, and updates `last_sync_at`/`last_sync_error` after completion

#### Scenario: Sync failure is surfaced
- **WHEN** `POST /api/providers/deepseek/sync` returns an error
- **THEN** the frontend displays the error message without requiring a page reload

### Requirement: Frontend displays actual loaded models grouped by provider
The model management UI SHALL list models from `GET /api/models/prices` grouped by provider, showing `model_id`, `display_name`, `tier`, `capabilities`, `input_price`, `output_price`, `max_context_window`, `max_output_tokens`, `fallback_model`, `missing`, and `avg_latency_ms`.

#### Scenario: Models appear grouped after sync
- **WHEN** the user opens the model manager after at least one provider has synced successfully
- **THEN** the model list is grouped by provider and editable fields can be modified in place

### Requirement: Frontend run configuration exposes mode switch and related fields
The `AgentConfig.vue` form SHALL include a `model_mode` selector (`single_model` / `auto_route`). The visible fields SHALL change based on the selected mode.

#### Scenario: Single model mode UI
- **WHEN** the user sets `model_mode=single_model`
- **THEN** the form shows a searchable model selector populated from `/api/models/prices`, and hides tier/budget/fallback controls

#### Scenario: Auto route mode UI
- **WHEN** the user sets `model_mode=auto_route`
- **THEN** the form shows `preferred_tier`, `max_cost_usd`, and `allow_fallback` controls, and disables or hides the explicit model selector

### Requirement: Frontend model selector uses full provider/model identity
The model selector SHALL use the full `provider/model_id` value for selection and display, avoiding collisions when two providers expose the same short model ID.

#### Scenario: Two providers expose identical model short name
- **WHEN** both `deepseek` and `openai` providers expose a model with ID `gpt-4o-mini`
- **THEN** the selector displays them as `deepseek/gpt-4o-mini` and `openai/gpt-4o-mini`

## MODIFIED Requirements

### Requirement: Model price management is persistent
The system SHALL expose `GET /api/models/prices` and `PUT /api/models/prices/{provider}/{model}` backed by the persistent `llm_models` table. The list SHALL only return models that are loaded into `ModelRegistry` from actual provider configuration or explicit static model declarations.

#### Scenario: List model profiles from persistent storage
- **WHEN** an authenticated user calls `GET /api/models/prices`
- **THEN** the response returns `items` from `llm_models` for configured providers and `persistent` is `true`

#### Scenario: Update price persists to database
- **WHEN** an authenticated admin calls `PUT /api/models/prices/default/deepseek-v4-flash` with `{"input_price":0.10,"output_price":0.20}`
- **THEN** the row in `llm_models` is updated, and subsequent `ModelRegistry` lookups and cost calculations use the new values

#### Scenario: Update model capabilities persists to database
- **WHEN** an authenticated admin calls `PUT /api/models/prices/default/deepseek-v4-flash` with `{"capabilities":["tool_calling","streaming","reasoning"]}`
- **THEN** the capabilities are persisted and reflected in `ModelRegistry`

#### Scenario: Update context window and max output tokens
- **WHEN** an authenticated admin calls `PUT /api/models/prices/default/deepseek-v4-flash` with `{"max_context_window":65536,"max_output_tokens":8192}`
- **THEN** the corresponding fields in `llm_models` are updated and used by router and engine

#### Scenario: Update disallowed identifier fields is rejected
- **WHEN** an authenticated admin calls `PUT /api/models/prices/default/deepseek-v4-flash` with `{"provider":"other","model_id":"other-name"}`
- **THEN** the system returns `400 Bad Request` and does not modify the row

#### Scenario: Update fallback model
- **WHEN** an authenticated admin calls `PUT /api/models/prices/default/deepseek-v4-flash` with `{"fallback_model":"deepseek-v3"}`
- **THEN** the fallback model is persisted and used when the primary model is unavailable

### Requirement: Manual provider discovery sync
The system SHALL expose `POST /api/providers/{name}/sync` which re-runs `/v1/models` discovery for the named provider and merges the result into persistent model storage. Sync events (`provider_sync_started`, `provider_sync_completed`, `provider_sync_failed`) SHALL be emitted over the WebSocket event bus.

#### Scenario: Sync a healthy provider
- **WHEN** an authenticated admin calls `POST /api/providers/deepseek/sync`
- **THEN** the system calls `GET /v1/models` on that provider, merges discovered models, updates provider `last_sync_at`, clears any prior `last_sync_error`, returns the list of discovered model IDs, and emits `provider_sync_completed`

#### Scenario: Sync an unknown provider
- **WHEN** an authenticated admin calls `POST /api/providers/unknown/sync`
- **THEN** the system returns `404 Not Found`
