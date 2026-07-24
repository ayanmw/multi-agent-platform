# Design: web_search 国内引擎与 web_research Agent Tool

## Context

当前 `core/web_search` 已支持 Exa、Parallel、Bing/Google/Tavily/Brave API 和 DuckDuckGo HTML 回退。在国内网络环境下 DuckDuckGo 通常不可达，因此需要引入百度、搜狗、Bing 国内等无需 API key 的 HTML 搜索 provider。同时，为了降低主 Agent 上下文负担，新增 `core/web_research` 工具，在 tool 内部完成“搜索→抓取正文→LLM 摘要”链路。

## Goals / Non-Goals

**Goals:**

1. `core/web_search` 在国内无 API key 时也能返回可用搜索结果。
2. 默认关闭 DuckDuckGo，国内引擎按 百度移动端 → 搜狗 → Bing 国内 顺序兜底。
3. 允许显式 `WEBSEARCH_PROVIDER=baidu`/`sogou`/`bing_cn_html` 强制选择引擎。
4. 新增 `core/web_research`，支持 `query` + 可选 `intent`/`num_results`/`fetch_top`，返回 LLM 摘要 + sources。
5. `web_research` 内部调用 LLM 的 usage 必须回传 engine 并计入 task 统计。
6. `web_research` 执行过程中发送 WS 可观测事件。
7. 所有新增 prompt 集中放在 `prompt.go`，带唯一 name；未来可 DB 化。
8. mock 模式下新增对应 mock 脚本，保证 21/21 回归不受影响。

**Non-Goals:**

1. 不改动 `core/web_search` 的 API provider（Bing/Google/Tavily/Brave/Exa/Parallel）行为。
2. 不直接支持付费/官方 API 的百度/搜狗（仅 HTML 解析）。
3. 不处理目标网页的 JavaScript 渲染（仅静态 HTML 抓正文）。
4. 本次不实现 prompt DB 持久化，仅预留 name 字段。

## Decisions

### 1. 拆为两条独立 tool

- `core/web_search`：轻量，只返回 SERP。
- `core/web_research`：深度，SERP + fetch + LLM 摘要。

**理由**：让主 Agent 按需选择，不强制所有搜索都走 LLM；同时 `web_search` 可保持原有返回值结构，避免破坏现有 Case。

### 2. 国内引擎优先级链

```
[已配置 API provider] -> baidu_mobile -> sogou -> bing_cn_html -> duckduckgo(默认关闭)
```

API provider 失败后同样会回退到国内引擎链。理由：API key 可能失效，国内引擎兜底保证可用性。

### 3. tool 内调用 LLM

`web_research` 内部使用注入的 `llm.Provider` 做一次性非流式 `Chat(req)` 调用。

**理由**：与 cron 子系统复用 chat 链路 precedent 一致；能在 tool 边界内完成“多页→摘要”的闭环，避免主 Agent 上下文暴增。

### 4. usage 通过 `_llm_usage` 回传

`web_research` executor 返回的 map 中携带 `_llm_usage` 字段，engine 在 `tool_call_output` 处识别并累加到本 task 的 usage。

**理由**：不改动 `Tool` 接口，侵入性最小；engine 仍掌握最终统计权，防止 tool 伪造。

### 5. ExecuteContext 扩展

新增字段：

```go
type ExecuteContext struct {
    Workdir     string
    TaskID      string
    AgentID     string
    StepIdx     int
    SessionID   string
    EventBus    event.Bus
    LLMProvider llm.Provider
}
```

**理由**：`EventBus` 和 `LLMProvider` 是 `web_research` 发出事件和调用 LLM 的最小依赖；`TaskID`/`AgentID`/`StepIdx`/`SessionID` 用于事件与 usage 的上下文绑定。

### 6. prompt 集中放在 `internal/tool/prompt.go`

每个 prompt 是一个带 name 的常量/变量，例如：

```go
type NamedPrompt struct {
    Name    string
    Content string
}

var WebResearchSummarizePrompt = NamedPrompt{
    Name: "web-research-summarize-system",
    Content: `...`,
}
```

**理由**：CRAUDE.md 语言偏好铁律要求注释中文，prompt 内容中文或英文按场景决定；统一管理便于后续 DB 化。

### 7. 输出格式：JSON 优先，降级 markdown

正常输出：

```json
{
  "summary": "markdown 摘要文本",
  "sources": [
    {"title": "...", "url": "..."},
    ...
  ]
}
```

LLM 不输出合法 JSON 时，回退为 markdown 文本，并在末尾追加 sources 列表。

### 8. 可观测事件

新增：

- `web_research_summarize_started`
- `web_research_summarize_completed`

事件中携带 `task_id`、`agent_id`、`step_idx`、模型名、输入字符数、输出 tokens 等。

### 9. mock 脚本

新增 `builtin:web-research-summarizer` mock 脚本，匹配 caseID `web-research`，让 `LLM_USE_MOCK=true` 下 `web_research` 能返回固定 JSON。

## Risks / Trade-offs

- **[Risk] HTML 解析脆弱**：百度/搜狗/Bing 国内页面结构可能变化 → 每个 provider 提供独立解析函数 + 正则 + httptest 测试；变更后容易定位修复。
- **[Risk] 目标网页反爬导致 fetch 正文失败 → 失败时只使用 SERP 摘要继续 LLM 总结，不终止整个 tool 调用。**
- **[Risk] LLM 摘要幻觉或 JSON 不合法 → 对输出做 JSON 解析，失败时降级为 markdown；prompt 中强烈约束保留 sources。**
- **[Risk] tool 内 LLM 调用超时导致 step 时间变长 → 单独设置摘要 timeout（如 20s），整体 timeout 25s 不够则扩展到 45s。**
- **[Risk] 主 Agent 无法引用原始链接 → 输出字段中 `sources` 必填，摘要中要求 LLM 保留 `[标题](url)` 格式。**
- **[Risk] usage 回传伪造 → engine 仅识别白名单字段（prompt/completion/total）且要求非负；未来可加 step 签名。**

## Migration Plan

1. 合并后，现有未启用任何 API provider 的环境将自动用上百度/搜狗/Bing 国内，无需用户修改配置。
2. `WEBSEARCH_DISABLE_DDG` 默认改为 `true`，但仍可通过设置 `WEBSEARCH_DISABLE_DDG=false` 重新启用 DuckDuckGo。
3. `core/web_search` 返回值结构不改变；新增 `core/web_research` 不会破坏旧 Case。
4. 无需数据迁移；prompt 暂时在代码中，后续 DB 化设计再讨论。

## Open Questions

1. `web_research` 的默认 `fetch_top` 取 3 是否合适？（可后续按真实 LLM 反馈调整）
2. 摘要模型固定用 `cfg.LLMModel` 是否在某些 tier 路由场景下会选择过贵模型？（先固定，后续可加 `WEBSEARCH_SUMMARIZE_MODEL` 覆盖）
3. 是否需要把 `web_research` 加入某个内置 Case 的回归路径？（在任务阶段决定是否扩展 `web-research` case）
