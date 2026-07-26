## ADDED Requirements

### Requirement: Router candidate pool is derived from loaded models
The Router SHALL build its candidate model list from the `ModelRegistry` instances that correspond to actual persisted `llm_models` rows with `missing=false` and whose provider is configured in the running system.

#### Scenario: Static default profile for unconfigured provider is excluded
- **WHEN** `LLM_PROVIDERS` only contains provider `deepseek`
- **THEN** any profiles in `DefaultProfiles()` belonging to provider `openai` or `anthropic` SHALL NOT be considered by `Router.Select`

#### Scenario: Missing discovered model is excluded from routing
- **WHEN** a model previously discovered by `deepseek` is marked `missing=true` in `llm_models`
- **THEN** `Router.Select` SHALL NOT select that model even if it matches the target tier

### Requirement: Router falls back to the requested single model when auto-route pool is empty
When `model_mode=auto_route` but no candidate model satisfies the filters, the system SHALL fall back to the model specified in `AgentRunSpec.Model` (or `cfg.LLMModel` if empty), emit a `router_fallback_default` event, and continue the call.

#### Scenario: All available models are filtered out by context length
- **WHEN** `auto_route` is enabled and all available models have `max_context_window` smaller than the request context
- **THEN** the Router returns the spec/default model, emits `router_fallback_default`, and the Engine continues

### Requirement: Router emits model selection event
Every `Router.Select` invocation SHALL generate an `llm/model_selected` event containing `mode`, `requested_tier`, `actual_model`, `reason`, and `fallback`.

#### Scenario: Successful auto-route emits event
- **WHEN** `Router.Select` picks model `deepseek/deepseek-v4-flash` for a `standard` tier code-generation request
- **THEN** an `llm/model_selected` event is emitted with `mode=auto_route`, `requested_tier=standard`, `actual_model=deepseek/deepseek-v4-flash`, `reason=tier_match`, `fallback=false`
