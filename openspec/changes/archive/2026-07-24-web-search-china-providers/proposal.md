# Proposal: web_search 国内引擎与 web_research Agent Tool

## Why

当前 `core/web_search` 默认依赖 DuckDuckGo 作为零 API key 的回退搜索。在无法访问 DDG 的网络环境下，该工具实际上不可用。同时，用户希望有一个更深度的工具，能够自动抓取搜索结果中的若干页面正文，并通过一次 LLM 调用生成带可访问来源链接的摘要，减少返回给主 Agent 的上下文冗余。

## What Changes

- **新增三个零 API key 的国内搜索引擎 provider**：
  - `baidu_mobile`：m.baidu.com 移动端 HTML。
  - `sogou`：www.sogou.com/web HTML。
  - `bing_cn_html`：cn.bing.com 国内版 HTML。
- **默认关闭 DuckDuckGo 回退**，并将上述国内引擎放在已配置 API provider 之后作为新的兜底链。
- **新增 `core/web_research` 工具**：功能上等于 `web_search` + 抓取 top-N 结果正文 + LLM 摘要。输出 JSON（含 `summary` 和 `sources`），JSON 不合法时降级为 markdown 文本，始终附带可访问的原文链接。
- **扩展 `tool.ExecuteContext`**：增加 `TaskID`、`AgentID`、`Step`、`SessionID`、`EventBus`、`LLMProvider`，让 tool 能发送事件并调用 LLM。
- **Engine 识别 tool 内 LLM 调用的 usage**：tool 通过 `_llm_usage` 字段回传，engine 累加到本 task 的 `tokenUsage` 与 cost records，保持白盒可观测与成本统计准确。
- **Prompt 集中管理**：所有新增 prompt 放在 `internal/tool/prompt.go` 中，每个 prompt 具有唯一 name，未来可持久化到 DB。
- **新增 mock 脚本与回归测试**：确保 `LLM_USE_MOCK=true` 模式下 `web_research` 路径可用，mock 回归 21/21 不受影响。
- **新增环境变量控制**：`WEBSEARCH_ENABLE_BAIDU`、`WEBSEARCH_ENABLE_SOGOU`、`WEBSEARCH_ENABLE_BING_CN_HTML`、`WEBSEARCH_DISABLE_DDG` 等。

## Capabilities

### New Capabilities

- `web-search-china-providers`：定义百度/搜狗/Bing 国内 HTML 搜索 provider 的解析、优先级、错误回退与配置。
- `web-research-tool`：定义 `core/web_research` 工具的 schema、行为、LLM 摘要流程、可观测事件与 usage 回传契约。
- `tool-execute-context-extension`：定义 `ExecuteContext` 新增字段及其注入方式，确保 engine 传递 task/agent/step/eventbus/llm-provider 给 tool executor。

### Modified Capabilities

- (无现有 spec 变更，仅新增实现)。

## Impact

- `internal/tool/web_search.go`（百度/搜狗/Bing国内解析与优先级）。
- `internal/tool/web_research.go`（新工具实现）。
- `internal/tool/prompt.go`（新增 prompt）。
- `internal/tool/executor.go`（ExecuteContext 扩展）。
- `internal/runtime/engine.go`（传递 ExecuteContext、识别 `_llm_usage` 累加）。
- `internal/config/config.go` 与 `.env.example`（新增配置项）。
- `cmd/server/main.go`（注入 LLM provider 与 EventBus 到 WebSearchConfig）。
- `internal/llm/mock_builtin.go`（新增 web_research summarizer mock 脚本）。
- `pkg/event/event.go` 与 `web/v2/src/types/events.ts`（新增 `web_research_summarize_started/completed` 事件）。
- 相关测试文件需新增/更新。
