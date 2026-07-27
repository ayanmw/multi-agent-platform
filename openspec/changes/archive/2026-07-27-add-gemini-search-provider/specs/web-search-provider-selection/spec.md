## ADDED Requirements

### Requirement: Configured API providers are selected in priority order with Gemini preferred
The system SHALL select the first configured API provider in the following priority order: `gemini`, `brave`, `bing`, `google`, `tavily`, `parallel`, `exa`. If none are configured, it SHALL fall back to the domestic provider chain and DuckDuckGo as defined by the existing `web-search-china-providers` spec.

#### Scenario: Gemini is configured
- **WHEN** `GEMINI_API_KEY` is set or `WEBSEARCH_ENABLE_GEMINI=true`
- **THEN** `core/web_search` SHALL use the `gemini` provider before any other configured provider

#### Scenario: No Gemini and Brave is configured
- **WHEN** Gemini is not configured and `WEBSEARCH_ENABLE_BRAVE=true` or `WEBSEARCH_BRAVE_API_KEY` is set
- **THEN** `core/web_search` SHALL use the `brave` provider

#### Scenario: Explicit provider override
- **WHEN** `WEBSEARCH_PROVIDER=gemini` is set
- **THEN** `core/web_search` SHALL use the `gemini` provider regardless of other configured providers
