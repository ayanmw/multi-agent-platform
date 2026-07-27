## Context

`core/web_search` 当前通过 `WebSearchConfig` 管理多个搜索 provider，由 `selectWebSearchProvider` 按固定优先级选择已配置项。provider 统一返回 `(providerName, text)`，再包装为统一 JSON 给 LLM。本次要在不破坏现有接口的前提下，把 Gemini 的 `google_search` 工具作为高优先级 provider 接入。

Gemini REST API 的关键特征：
- 端点：`POST https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent`
- 认证：`X-goog-api-key` 请求头。
- 工具声明：请求体 `tools` 数组含 `{ "google_search": {} }`。
- Dynamic retrieval（可选）：`toolConfig.google_search.dynamic_retrieval` 可设置 `dynamic_threshold`（0.0~1.0，默认未启用）。
- 响应：位于 `candidates[0].content.parts[0].text`；grounding 元数据位于 `candidates[0].groundingMetadata`，包含 `webSearchQueries`、`groundingChunks`（含 `web` 标题/URL）、`groundingSupports` 等。

## Goals / Non-Goals

**Goals:**
- 新增 `gemini` provider，复用现有 `WebSearchConfig`/`webSearchExecutor`/searchResult 格式化链路。
- 当 `GEMINI_API_KEY` 存在或 `WEBSEARCH_ENABLE_GEMINI=true` 时，`selectWebSearchProvider` 优先选择 Gemini。
- 解析 Gemini grounding 元数据，生成与现有 provider 风格一致的摘要文本。
- 单元测试：模拟端点验证请求与响应；真实网络冒烟测试使用 `.env` 中的 key。

**Non-Goals:**
- 不新增 WebSocket 事件类型。
- 不改动前端。
- 不实现 dynamic retrieval 的 runtime 自适应阈值（仅保留配置字段，默认不启用）。
- 不将 Gemini 作为通用 LLM provider（仅用于 search grounding）。

## Decisions

1. **provider 优先级：Gemini 最高**
   - 理由：用户明确“可用时优先级最高”。
   - 替代方案：维持 brave -> bing -> google 顺序，将 Gemini 放在 brave 之后。拒绝，因为不满足用户需求。

2. **认证方式：请求头 `X-goog-api-key`**
   - 理由：与官方文档示例一致，不污染 URL query，避免 key 进入日志。
   - 替代方案：query `key=...`。拒绝，header 更安全且官方 curl 示例使用 header。

3. **模型默认 `gemini-flash-latest`**
   - 理由：flash 模型支持 grounding 且快/便宜，适合搜索场景。
   - 可由 `GEMINI_SEARCH_MODEL` 覆盖。

4. **返回格式：复用 `formatSearchResults(searchResult[])`**
   - 理由：保持 LLM 侧一致性。
   - 解析策略：优先从 `groundingChunks` 提取标题、URL、片段；若缺失，回退到纯文本摘要。

5. **错误处理：HTTP 非 2xx 时报错，不自动 fallback 到 DuckDuckGo**
   - 理由：若用户显式启用 Gemini，失败应让上层看到真实错误；现有 `fallbackChinaProvider` 仅在其他 API provider 失败后触发。
   - 例外：若 Gemini 未返回结果，走已有 fallback 逻辑（保持统一）。

## Risks / Trade-offs

- [Risk] 用户提供的 API key 可能带 free tier RPD/TPM 限制，高并发时被打满。
  - Mitigation：仅作为搜索 provider，单次调用；未来若需可在 config 增加 `timeout`/`max_results` 等限流参数。
- [Risk] Gemini 响应结构不稳定（实验性 beta 端点）。
  - Mitigation：解析时尽量宽容，缺失字段不 fatal，记录 warning 后回退纯文本。
- [Risk] 天竺网络导致 generativelanguage.googleapis.com 不可达。
  - Mitigation：这不属于代码层问题；若需代理可由 `HTTPClient.Transport` 透传（已通过 `WebSearchConfig.HTTPClient` 支持）。

## Migration Plan

1. 代码变更合并后，开发者在 `.env` 中新增 `GEMINI_API_KEY=...`。
2. 不需要数据迁移或 schema 变更。
3. Rollback：删除 `.env` 中的 `GEMINI_API_KEY` 或设置 `WEBSEARCH_ENABLE_GEMINI=false` 即可回到旧优先级。
