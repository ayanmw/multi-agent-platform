## ADDED Requirements

### Requirement: Provider configuration is loaded from environment
The system SHALL parse `LLM_PROVIDERS` (JSON array) from `.env` / environment variables into a list of provider records containing `name`, `type`, `endpoint`, and `api_key`.

#### Scenario: Single provider declared via JSON
- **WHEN** `.env` contains `LLM_PROVIDERS=[{"name":"deepseek","type":"openai","endpoint":"https://api.deepseek.com/v1","api_key":"sk-xxx"}]`
- **THEN** the system loads exactly one provider named `deepseek` with type `openai`

#### Scenario: Backward compatibility when LLM_PROVIDERS is absent
- **WHEN** `.env` only contains legacy `LLM_ENDPOINT`, `LLM_API_KEY`, and `LLM_MODEL`
- **THEN** the system synthesizes a provider named `default` with type `openai` using those legacy values

#### Scenario: Unsupported provider type falls back to openai-compatible
- **WHEN** a provider entry declares a `type` that is not one of `openai`, `deepseek`, `anthropic`, `gemini`, or `self-hosted`
- **THEN** the system treats it as `openai-compatible` for discovery and chat, and logs a warning

#### Scenario: Model tier wildcard mapping
- **WHEN** `.env` contains `MODEL_TIER_claude-opus-*=premium` and a provider returns `claude-opus-4`
- **THEN** the persisted model has `tier=premium`

### Requirement: Provider list can be queried via REST API
The system SHALL expose `GET /api/providers` returning all configured providers with their `name`, `type`, `endpoint`, `healthy` flag (discovery success status), `last_sync_at` timestamp, and `last_sync_error` if any. API keys SHALL NOT be returned.

#### Scenario: Query providers after startup
- **WHEN** an authenticated admin calls `GET /api/providers`
- **THEN** the response contains an array of providers with no `api_key` field

### Requirement: Manual provider discovery sync
The system SHALL expose `POST /api/providers/{name}/sync` which re-runs `/v1/models` discovery for the named provider and merges the result into persistent model storage.

#### Scenario: Sync a healthy provider
- **WHEN** an authenticated admin calls `POST /api/providers/deepseek/sync`
- **THEN** the system calls `GET /v1/models` on that provider, merges discovered models, updates provider `last_sync_at`, clears any prior `last_sync_error`, and returns the list of discovered model IDs

#### Scenario: Sync an unknown provider
- **WHEN** an authenticated admin calls `POST /api/providers/unknown/sync`
- **THEN** the system returns `404 Not Found`

### Requirement: Discovery failures do not block startup
The system SHALL attempt discovery for each provider during startup but SHALL continue startup if discovery fails or times out.

#### Scenario: Provider discovery times out
- **WHEN** a provider endpoint is unreachable during startup
- **THEN** the server starts successfully, logs the failure, and sets `healthy=false` and `last_sync_error` for that provider
