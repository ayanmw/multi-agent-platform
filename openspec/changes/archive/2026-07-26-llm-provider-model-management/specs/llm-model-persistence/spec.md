## ADDED Requirements

### Requirement: Models are persisted in SQLite
The system SHALL create and use an `llm_models` table to store model metadata including `provider_name`, `model_id`, `display_name`, `tier`, `capabilities`, `input_price`, `output_price`, `max_context_window`, `max_output_tokens`, `fallback_model`, `rate_limit_rpm`, `avg_latency_ms`, `missing`, `created_at`, and `updated_at`.

#### Scenario: Fresh database starts with seeded models
- **WHEN** the server starts with an empty database
- **THEN** `llm_models` is created and seeded with the default model profile(s) plus any models from `LLM_MODELS` and any discovered models

#### Scenario: Composite identity for provider-scoped models
- **WHEN** two providers both expose a model with ID `gpt-4o`
- **THEN** the system stores them as two distinct rows, one per provider, without conflict

### Requirement: Discovery merge preserves user-editable fields
The system SHALL, during provider discovery merge, insert new models with default values for editable fields and SHALL NOT overwrite existing values for `display_name`, `tier`, `capabilities`, `input_price`, `output_price`, `max_context_window`, `max_output_tokens`, `fallback_model`, `rate_limit_rpm`, or `avg_latency_ms` on rows that already exist.

#### Scenario: Re-discovering a model after price edit
- **GIVEN** a persisted model has `input_price` manually set to `0.05`
- **WHEN** the provider is re-synced and still returns that model ID
- **THEN** the persisted `input_price` remains `0.05`

### Requirement: Missing models are marked, not deleted
The system SHALL set `missing=true` on any persisted model that is no longer returned by its provider during sync. The system SHALL NOT delete the row.

#### Scenario: Model removed from provider catalog
- **GIVEN** a model is persisted with `missing=false`
- **WHEN** the provider no longer returns that model ID during sync
- **THEN** the row remains, but `missing` becomes `true`

### Requirement: ModelRegistry loads from persistent storage
The system SHALL initialize `ModelRegistry` from `llm_models` at startup instead of only from `DefaultProfiles()`.

#### Scenario: Server restart after price edit
- **GIVEN** an operator updated `input_price` via `PUT /api/models/{provider}/{model}` before restart
- **WHEN** the server restarts
- **THEN** `ModelRegistry` reflects the edited price

### Requirement: Static LLM_MODELS entries are merged at startup
The system SHALL parse both `LLM_MODELS` JSON array and legacy single-model fields, writing each entry into `llm_models` under the appropriate provider (`default` provider for legacy entries, matching provider by model name for `LLM_MODELS` entries).

#### Scenario: LLM_MODELS lists two models
- **WHEN** `.env` declares `LLM_MODELS=[{"name":"a","provider":"x"},{"name":"b","provider":"y"}]`
- **THEN** the system persists models `x/a` and `y/b` with `missing=false`

### Requirement: Model tier resolved via wildcard mapping
The system SHALL apply `.env` `MODEL_TIER_*` wildcard mappings when resolving the tier of a discovered or static model.

#### Scenario: Tier mapping matches wildcard
- **WHEN** `MODEL_TIER_deepseek-*=efficient` is set and a model `deepseek-v4-flash` is discovered
- **THEN** the persisted model has `tier=efficient`
