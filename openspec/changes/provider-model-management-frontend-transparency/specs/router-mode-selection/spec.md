## ADDED Requirements

### Requirement: Agent run supports two model selection modes
The system SHALL support `model_mode` values `single_model` and `auto_route`. `single_model` SHALL be the default. When `single_model` is active, the Engine SHALL use `AgentRunSpec.Model` without invoking the Router. When `auto_route` is active, the Engine SHALL invoke `Router.Select` and use its result.

#### Scenario: Single model mode prevents router override
- **WHEN** an Agent run is started with `model_mode=single_model` and `model=deepseek-v4-flash`
- **THEN** the LLM call uses `deepseek-v4-flash` regardless of input intent or preferred tier

#### Scenario: Auto route mode selects based on intent
- **WHEN** an Agent run is started with `model_mode=auto_route`, `preferred_tier=standard`, and the input is a multi-step coding task
- **THEN** `Router.Select` is called and its returned model is used for the LLM call

### Requirement: Agent configuration schema supports routing preferences
The `agents` table SHALL store `model_mode` (text, default `single_model`), `preferred_model` (text), `preferred_tier` (text), `max_cost_usd` (real), and `allow_fallback` (boolean default true).

#### Scenario: Migrate existing boolean allow_auto_route flag
- **WHEN** the database contains rows with the legacy `allow_auto_route` boolean column
- **THEN** migration v32 converts `true` to `model_mode=auto_route` and `false` to `model_mode=single_model`

#### Scenario: Auto route with fallback disabled
- **WHEN** `model_mode=auto_route`, `preferred_tier=premium`, and `allow_fallback=false`
- **THEN** `Router.Select` SHALL NOT return a lower-tier model when no premium candidate is available; it SHALL fall back to the spec/default model instead

### Requirement: Agent run spec exposes routing fields
`AgentRunSpec` SHALL carry `ModelMode`, `PreferredTier`, `MaxCostUSD`, and `AllowFallback`, populated from the agent configuration or explicit override.

#### Scenario: Default spec inherits from agent config
- **WHEN** an agent has `model_mode=auto_route`, `preferred_tier=efficient`, `max_cost_usd=0.05`, and `allow_fallback=false`
- **THEN** any run started for that agent uses those values unless explicitly overridden
