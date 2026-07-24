# Tasks: web_search 国内引擎与 web_research Agent Tool

## 1. Prompt 与 ExecuteContext 基础设施

- [x] 1.1 创建 `internal/tool/prompt.go`，定义 `NamedPrompt` 结构与 `WebResearchSummarizePrompt`。命名 `web-research-summarize-system`。
- [x] 1.2 扩展 `internal/tool/executor.go` 中的 `ExecuteContext`，新增 `TaskID`、`AgentID`、`StepIdx`、`SessionID`、`EventBus`、`LLMProvider` 字段。
- [x] 1.3 调整 `Registry.Execute`/`ExecuteWithCtx` 签名兼容，确保非 `CtxTool` 仍回退到 `Execute`。

## 2. Engine 传递 ExecuteContext 与 usage 回传

- [x] 2.1 在 `internal/runtime/engine.go` 的 `executeToolCall` 中构造完整 `tool.ExecuteContext`，填入 task/agent/step/session/eventbus/llm-provider/workdir。
- [x] 2.2 在 `tool_call_output` 处理逻辑中识别 result 中的 `_llm_usage`，校验字段后累加到 `e.tokenUsage` 与 cost records。
- [x] 2.3 运行现有测试，确保 `ExecuteContext` 扩展未破坏现有 tool。

## 3. Config 与国内引擎开关

- [x] 3.1 在 `internal/config/config.go` 新增配置字段：`WebSearchEnableBaidu`、`WebSearchEnableSogou`、`WebSearchEnableBingCnHTML`，并调整 `WebSearchDisableDDG` 默认逻辑（未显式设置时视为 true）。
- [x] 3.2 更新 `.env.example` 新增上述配置项。
- [x] 3.3 更新 `cmd/server/main.go` 的 `tool.WebSearchConfig` 初始化，传入新增 enable 开关。

## 4. 国内 HTML 搜索 Provider

- [x] 4.1 在 `internal/tool/web_search.go` 中实现 `callBaiduMobile`、`parseBaiduMobileHTML`，解析 m.baidu.com 自然结果，处理 redirect URL。
- [x] 4.2 实现 `callSogou`、`parseSogouHTML`，解析 www.sogou.com/web 自然结果。
- [x] 4.3 实现 `callBingCnHTML`、`parseBingCnHTML`，解析 cn.bing.com（`ensearch=0`）自然结果。
- [x] 4.4 调整 `selectWebSearchProvider` 包含国内 provider 优先级；当 API provider 失败时按国内链回退。
- [ ] 4.5 为三个 provider 添加 httptest 单测，覆盖正常结果、零结果、redirect URL。

## 5. web_research 工具实现

- [x] 5.1 创建 `internal/tool/web_research.go`，实现 `NewWebResearchTool` 与 executor，schema 包含 `query`/`num_results`/`fetch_top`/`intent`。
- [x] 5.2 在 executor 中复用 `web_search` 搜索函数拿到 searchResult slice。
- [x] 5.3 并发或串行抓取 top-N 结果正文（优先串行降低反爬风险），使用 fetch 同款 readLimited + htmlToText，失败降级使用 SERP snippet。
- [x] 5.4 使用 `ctx.LLMProvider.Chat` 做一次性非流式总结调用，prompt 来自 `prompt.go`；解析 JSON 输出，失败回退 markdown。
- [x] 5.5 在总结开始与完成时发送 `web_research_summarize_started/completed` 事件。
- [x] 5.6 返回结果中携带 `_llm_usage` 字段与 `sources` 列表。
- [x] 5.7 在 `cmd/server/main.go` 注册 `core/web_research`。

## 6. 可观测事件

- [x] 6.1 在 `pkg/event/event.go` 新增常量 `web_research_summarize_started`、`web_research_summarize_completed`。
- [x] 6.2 在 `web/v2/src/types/events.ts` 追加对应 `EventType`。

## 7. Mock 脚本与回归

- [ ] 7.1 在 `internal/llm/mock_builtin.go` 新增 `web-research` case 对应的 mock script（或复用 `web-research`），让 `LLM_USE_MOCK=true` 下 `web_research` 返回固定 JSON 摘要。
- [ ] 7.2 更新 `internal/tool/web_search_test.go` 覆盖国内 provider 选择逻辑与显式 `WEBSEARCH_PROVIDER=baidu`。
- [ ] 7.3 新增 `internal/tool/web_research_test.go`，覆盖正常摘要、fetch 失败降级、JSON 不合法降级、usage 回传字段。
- [ ] 7.4 运行 `go test ./...` 与 mock 回归脚本 `scripts/cases-regression.sh`，确保 21/21 PASS。

## 8. 文档与收尾

- [ ] 8.1 更新 `roadmaps/ROADMAP.md` 记录新 provider 与 web_research 工具。
- [ ] 8.2 更新 `internal/cases/cases.go` 中的 `web-research` case 说明，提及 `web_research` 可作为一次调用的替代方案（可选）。
- [ ] 8.3 提交 Git 并归档 OpenSpec change。
