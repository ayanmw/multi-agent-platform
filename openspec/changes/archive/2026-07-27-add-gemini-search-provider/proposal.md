## Why

现有 `core/web_search` 工具已支持 Brave/Bing/Google/Tavily/Exa/Parallel 以及国内 HTML 抓取等 provider，但部分免费/低门槛 provider 需要信用卡或稳定性不佳。Gemini API 提供官方的 `google_search` 工具（带 grounding），无需信用卡即可注册且有免费额度，适合作为高优先级 provider 接入到现有搜索链路中。

## What Changes

- 在 `internal/tool/web_search.go` 中新增 `gemini` provider，通过 Gemini `v1beta/models/{model}:generateContent` REST 端点调用 `google_search` 工具。
- 在 `WebSearchConfig` 中新增 `GeminiAPIKey`、`GeminiEndpoint`、`GeminiModel`、`EnableGeminiSearch` 字段。
- 在 `internal/config/config.go` 中新增 `GEMINI_API_KEY`、`GEMINI_ENDPOINT`、`GEMINI_SEARCH_MODEL`、`WEBSEARCH_ENABLE_GEMINI` 环境变量映射。
- 在 `cmd/server/main.go` 的 `webSearchCfg` 初始化中透传 Gemini 配置。
- 将 Gemini provider 在 provider 优先级中设为最高（当 `WEBSEARCH_ENABLE_GEMINI=true` 或配置了 `GEMINI_API_KEY` 时）。
- 提供单元测试：用 `httptest` 模拟 Gemini 响应，验证请求体、`X-goog-api-key` 头、响应解析与格式化。
- 新增一个真实网络冒烟单元测试（默认 Skip），使用用户提供的 `GEMINI_API_KEY` 调用 `gemini-flash-latest` 并断言返回 grounding 结果。

## Capabilities

### New Capabilities
- `gemini-web-search-provider`: 通过 Gemini API 调用官方 Google Search 工具并解析 grounding 结果到统一 `searchResult` 格式。

### Modified Capabilities
- `web-search-provider-selection`: 修改 provider 选择逻辑，将 Gemini 作为已配置时的最高优先级选项（无源码级破坏性变更，纯优先级调整）。

## Impact

- 后端：`internal/tool/web_search.go`、`internal/tool/web_search_test.go`、新增 `web_search_gemini_test.go`（或其他命名）。
- 配置：`internal/config/config.go`、`cmd/server/main.go`、`.env` 示例。
- REST API 行为：`core/web_search` 在配置了 Gemini key 时优先走 Gemini。
- 前端：无需变更；事件类型与 schema 不变。
