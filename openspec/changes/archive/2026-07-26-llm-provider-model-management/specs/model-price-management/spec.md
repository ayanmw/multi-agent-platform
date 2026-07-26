## MODIFIED Requirements

### Requirement: Model price management is persistent
The system SHALL expose `GET /api/models/prices` and `PUT /api/models/prices/{provider}/{model}` backed by the persistent `llm_models` table.

#### Scenario: List model profiles from persistent storage
- **WHEN** an authenticated user calls `GET /api/models/prices`
- **THEN** the response returns `items` from `llm_models` and `persistent` is `true`

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

## REMOVED Requirements

### Requirement: Model prices were runtime-only
**Reason**: Superseded by persistent model management; prices now survive restart.
**Migration**: Consumers of `GET /api/models/prices` should stop relying on `persistent` being `false` and on the `note` field; both are removed.
