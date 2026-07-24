# web-research-tool Specification

## Purpose
TBD - created by archiving change web-search-china-providers. Update Purpose after archive.
## Requirements
### Requirement: web_research tool performs search, fetch, and summarize
The system SHALL provide a `core/web_research` tool that accepts a query, performs a web search, fetches the top-N result pages, and returns a concise LLM-generated summary with accessible source links.

#### Scenario: web_research returns summary and sources
- **WHEN** the tool is called with `query="Go 1.25 release"`
- **THEN** it SHALL perform a search, fetch the top result pages, call an LLM to produce a summary, and return JSON containing `summary` and `sources` where each source has `title` and `url`.

#### Scenario: web_research accepts an optional intent parameter
- **WHEN** the tool is called with `query="..."` and `intent="extract technical details"`
- **THEN** the summary prompt SHALL include the intent so the LLM biases the summary toward the requested angle.

---

### Requirement: web_research output includes accessible source URLs
The system SHALL ensure every source URL returned by `web_research` is the real, user-accessible URL of the result page, not a redirect or tracker URL.

#### Scenario: Baidu search result source URL
- **WHEN** a search result comes from the Baidu mobile provider
- **THEN** `web_research` SHALL resolve any Baidu redirect and store the final target URL in `sources`.

---

### Requirement: web_research LLM usage is reported to engine
The system SHALL report token usage from the internal LLM summarization call back to the engine so that task token usage and cost records remain accurate.

#### Scenario: web_research returns _llm_usage
- **WHEN** `web_research` completes successfully
- **THEN** its result map SHALL include `_llm_usage` with `prompt_tokens`, `completion_tokens`, and `total_tokens`.

#### Scenario: engine accumulates tool LLM usage
- **WHEN** the engine processes the `web_research` result and detects `_llm_usage`
- **THEN** it SHALL add those token counts to the current task's `tokenUsage` and cost records.

---

### Requirement: web_research emits observable events
The system SHALL emit `web_research_summarize_started` and `web_research_summarize_completed` events so the UI can display the internal summarization step.

#### Scenario: Summarize started event
- **WHEN** `web_research` begins the LLM summarization step
- **THEN** it SHALL emit `web_research_summarize_started` with `task_id`, `agent_id`, `step_idx`, `model`, and `input_chars`.

#### Scenario: Summarize completed event
- **WHEN** the LLM summarization step completes
- **THEN** it SHALL emit `web_research_summarize_completed` with `task_id`, `agent_id`, `step_idx`, `model`, `total_tokens`, and `source_count`.

---

### Requirement: web_research handles fetch and JSON failures gracefully
The system SHALL degrade gracefully when page fetches fail or the LLM returns invalid JSON.

#### Scenario: Fetch failure fallback
- **WHEN** one or more top result pages cannot be fetched within timeout
- **THEN** `web_research` SHALL summarize using the available SERP snippets and still return source links.

#### Scenario: Invalid JSON fallback
- **WHEN** the LLM summary is not valid JSON
- **THEN** `web_research` SHALL return the raw text as markdown with a trailing sources list instead of failing.

