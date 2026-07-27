# gemini-web-search-provider Specification

## Purpose
TBD - created by archiving change add-gemini-search-provider. Update Purpose after archive.
## Requirements
### Requirement: Gemini search provider is supported
The system SHALL support a new `gemini` provider for `core/web_search` that calls the Gemini `generateContent` REST endpoint with the `google_search` tool.

#### Scenario: Successful Gemini search
- **WHEN** a user or agent calls `core/web_search` with a query
- **AND** `GEMINI_API_KEY` is configured or `WEBSEARCH_ENABLE_GEMINI=true`
- **THEN** the system SHALL send a `POST` request to `{GeminiEndpoint}/models/{GeminiModel}:generateContent` with the `X-goog-api-key` header
- **AND** the request body SHALL contain `tools: [{ "google_search": {} }]`
- **AND** the system SHALL parse the response into a search result summary containing title, URL, and snippet
- **AND** return the result with `"provider": "gemini"`

### Requirement: Gemini grounding metadata is parsed
The system SHALL extract search results from `groundingMetadata.groundingChunks` and fallback to the plain text response if the metadata is absent or empty.

#### Scenario: Grounding chunks present
- **WHEN** the Gemini response includes `groundingMetadata.groundingChunks`
- **THEN** each chunk with `web.title` and `web.uri` SHALL become one search result
- **AND** the corresponding `groundingSupports` segment text SHALL be used as the snippet when available

#### Scenario: Grounding chunks absent
- **WHEN** the Gemini response does not include usable grounding chunks
- **THEN** the system SHALL fallback to returning `candidates[0].content.parts[0].text` as a single summary

