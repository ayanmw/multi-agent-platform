## ADDED Requirements

### Requirement: web_search supports Baidu mobile HTML provider
The system SHALL support a `baidu_mobile` web search provider that parses m.baidu.com mobile search results without requiring an API key.

#### Scenario: Baidu provider returns search results
- **WHEN** no configured API provider exists and `WEBSEARCH_ENABLE_BAIDU` is true
- **THEN** web_search SHALL send a request to m.baidu.com, parse natural result titles/snippets/URLs, and return them as formatted search results.

#### Scenario: Baidu URL redirects are resolved
- **WHEN** a Baidu result URL is a redirect (e.g. `https://m.baidu.com/...?url=...`)
- **THEN** the provider SHALL extract the real target URL from the redirect parameter before returning it.

#### Scenario: Explicit Baidu provider selection
- **WHEN** `WEBSEARCH_PROVIDER=baidu` is set
- **THEN** web_search SHALL use the Baidu mobile provider regardless of other configured providers.

---

### Requirement: web_search supports Sogou HTML provider
The system SHALL support a `sogou` web search provider that parses www.sogou.com/web search results without requiring an API key.

#### Scenario: Sogou provider returns search results
- **WHEN** the Baidu mobile provider is disabled or fails and `WEBSEARCH_ENABLE_SOGOU` is true
- **THEN** web_search SHALL send a request to www.sogou.com, parse natural result titles/snippets/URLs, and return them as formatted search results.

---

### Requirement: web_search supports Bing China HTML provider
The system SHALL support a `bing_cn_html` web search provider that parses cn.bing.com search results without requiring an API key.

#### Scenario: Bing China provider returns search results
- **WHEN** Baidu and Sogou providers are disabled or fail and `WEBSEARCH_ENABLE_BING_CN_HTML` is true
- **THEN** web_search SHALL send a request to cn.bing.com with `ensearch=0`, parse natural result titles/snippets/URLs, and return them as formatted search results.

---

### Requirement: DuckDuckGo fallback is disabled by default
The system SHALL default `WEBSEARCH_DISABLE_DDG` to true so that environments without DuckDuckGo access do not attempt the DDG fallback by default.

#### Scenario: No provider and DDG disabled
- **WHEN** no API provider is configured, no domestic provider is enabled, and `WEBSEARCH_DISABLE_DDG=true`
- **THEN** web_search SHALL return a clear "not configured" message instead of attempting DuckDuckGo.

#### Scenario: User explicitly enables DDG
- **WHEN** `WEBSEARCH_DISABLE_DDG=false` is set
- **THEN** the existing DuckDuckGo fallback provider SHALL remain available as the last resort.

---

### Requirement: Domestic provider fallback order
The system SHALL try configured API providers first, then fall back to domestic providers in the order: `baidu_mobile`, `sogou`, `bing_cn_html`, and finally DuckDuckGo if explicitly enabled.

#### Scenario: API provider fails
- **WHEN** a configured API provider returns a non-2xx or parse error
- **THEN** web_search SHALL fall back to the domestic provider chain before giving up.

#### Scenario: Baidu fails and Sogou succeeds
- **WHEN** Baidu mobile request fails and Sogou is enabled
- **THEN** web_search SHALL return Sogou results transparently.
