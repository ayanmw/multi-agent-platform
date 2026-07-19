# 后端 LLM 迁移评估：Chat Completions API → Responses API

> 评估日期：2026-07-12  
> 评估范围：`internal/llm/*`、`internal/runtime/engine.go`、Provider 工厂与 Mock 体系  
> 目标：判断后端是否应从 OpenAI Chat Completions API 迁移到更新的 Responses API

---

## 1. 当前 Chat Completions API 使用情况

### 1.1 核心调用位置

后端所有真实 LLM 调用最终都通过 `llm.Provider` 接口落地，当前唯一真实实现是 `OpenAIProvider`：

| 文件 | 功能 | 关键调用/URL |
|------|------|-------------|
| `internal/llm/openai_provider.go:81,127` | 流式 / 非流式 | `POST /v1/chat/completions` |
| `internal/llm/client.go:182,230` | 旧 `Client.Chat` / `Client.ChatStream`（仍被 `OpenAIProvider` 包装） | `POST /v1/chat/completions` |
| `internal/runtime/engine.go:1161,1225` | ReAct Loop 的 think()/fallback | `selectedProvider.ChatStream(req, cb)` |
| `internal/llm/router.go:203` | 路由意图分类（classifier） | `classifier.Chat(req)` |
| `internal/orchestrator/orchestrator.go:270` | 子 Agent Provider 创建 | `CreateProviderFromConfig(...)` |
| `cmd/server/main.go:905,1226` | 任务入口 / checkpoint recover | `CreateProviderFromConfig(...)` |

### 1.2 已使用的 Chat Completions 特性

- **SSE streaming**：Engine 依赖 `onChunk` 回调逐 token 广播 `llm_delta`（`engine.go:1161-1175`），是白盒 Agent 的核心。
- **Tool / function calling**：统一 `ToolCall` / `FunctionCall` 结构；`OpenAIProvider` 在 SSE 中按 `index` 累积 `tool_calls[].function.arguments` 片段（`openai_provider.go:207-228`）。
- **Message roles**：`system | user | assistant | tool`，`tool_call_id` 用于关联 tool result。
- **Usage**：`Usage{PromptTokens, CompletionTokens, TotalTokens, PromptCacheHitTokens, PromptCacheMissTokens}`；Engine 累积到 `tokenUsage` 用于成本、预算、指标（`engine.go:663-668`）。
- **ToolChoice**：统一为 string（`"auto"`、`"none"` 或具体工具名），`AnthropicProvider` 内部转成 object（`anthropic_provider.go:660-679`）。
- **Reasoning content**（扩展）：`Delta.ReasoningContent` 字段已存在，用于 DeepSeek R1/V4；当前把它拼进 `contentBuilder`（`openai_provider.go:203-204`），并作为独立字段透传给前端（`engine.go:1168-1172`）。

### 1.3 使用方式总结

Engine 只关心统一类型 `llm.ChatRequest` / `llm.StreamChunk` / `llm.ToolCall` / `llm.Usage`，所有 HTTP/协议细节被封在 Provider 实现里。当前真实 LLM 默认使用 `OpenAIProvider`，DeepSeek/Groq/Together 等通过 OpenAI-compatible 路径工作；Anthropic 通过独立的 `AnthropicProvider` 转换。Mock 测试通过 `MockProvider` 完全不走 HTTP。

---

## 2. Responses API 概述

Responses API（OpenAI 2025 年起主推，端点 `POST /v1/responses`）是一个比 Chat Completions 更高层的抽象，核心差异如下：

| 维度 | Chat Completions | Responses API |
|------|------------------|---------------|
| 端点 | `/v1/chat/completions` | `/v1/responses` |
| 输入 | `messages: [{role, content}]` | `input: string | array[InputItem]`；旧 `messages` 被 `input` 替代 |
| system prompt | `messages[0].role == "system"` | `instructions` 字段 |
| 输出对象 | `choices[0].message` | 顶层 `output` 数组（text / tool_call / reasoning 等 `OutputItem`） |
| tool calls | `message.tool_calls[].function` | `output[].type == "function_call"` / `"tool_call"`；调用结果作为 `input` 项再次发送 |
| reasoning | 部分厂商扩展（`reasoning_content`） | 内置 `output[].type == "reasoning"`，含 `summary` / `thinking` 字段 |
| 内置工具 | 无 | `tools` 可包含 `web_search_preview`、`file_search`、`computer_use_preview` 等 |
| annotations | 无 | web_search / file_search 结果可带 `annotations` |
| 多轮会话 | 应用层自己维护 messages | API **原生保留对话上下文**（通过 `previous_response_id`） |
| SSE chunk | `choices[0].delta` | `output[].type` + `delta` 事件；payload 结构更复杂 |
| 非流式结构 | `ChatCompletion` | `Response` 对象，含 `output[]`、usage、模型信息、创建时间 |
| usage | 固定 `prompt/completion/total` | 更细粒度，含 reasoning、input_text_tokens、output_text_tokens、refusal 等 |

关键影响：Responses API 把"消息数组 + tool result 回环"模型改成了"input/output items + previous_response_id"模型。这意味着应用层不再需要在每次请求时回传完整的 assistant/tool message 历史——API 端会替你维护。这对当前 Engine 的消息管理是结构性变化。
---

## 3. 迁移困难与挑战

### 3.1 Schema 变化（输入/输出格式）

- 当前 `llm.Message` 模型（`role`, `content`, `tool_calls`, `tool_call_id`）和 Responses API 的 `InputItem` / `OutputItem` 模型不一致。要做映射：
  - `system` → `instructions`
  - `user` → `input[type=message]`
  - `assistant` + `tool_calls` → 上一轮的 `output[]`
  - `tool` → `input[type=function_call_output]`
- Responses API 的 `input` 支持复合 content（text、image、file 等）和 `previous_response_id`；Engine 目前按文本角色维护的 `[]llm.Message` 需要重写或新增一层 adapter。

### 3.2 Streaming 差异

- Chat Completions 的 SSE：`data: {"choices":[{"delta":{"content":"..."}}]}`，自解析实现简单（项目已有稳定 `OpenAIProvider` 代码 254 行 + 旧 client 394 行）。
- Responses API 的 SSE：`data: {"type":"response.output_item.added" | "response.output_text.delta" | "response.function_call_arguments.delta" | ...}`，事件种类繁多，解析逻辑大约需要多 2-3 倍代码量，且错误处理路径更复杂。
- 当前前端依赖的 `llm_delta` 事件只含 `content` / `reasoning_content`；Responses API 的事件分层更细，需要在 adapter 里重新聚合成 `llm.StreamChunk`。

### 3.3 Tool / Function Calling 差异

- 当前统一抽象是 `ToolCall{ID, Type, Function{Name, Arguments}}`。
- Responses API 使用 `output[].type == "function_call"` / `"tool_call"`，并支持内部内置工具；function result 不是 `role=tool` 消息，而是 `input[type=function_call_output]`。
- 名称差异：`function_call` vs `tool_call`、参数字段可能叫 `arguments` 但也可能是 `call_id` 关联。`Engine.executeTool` 和消息持久化（`saveConversation`、`writeSessionMessage`）都假设了现有 `llm.Message` 结构，需要改。

### 3.4 Token / Usage 差异

- `Usage` 结构需要扩展：Responses API 返回 `input_tokens`、`output_tokens`、`total_tokens`，并细分为 `input_text_tokens`、`input_image_tokens`、`input_audio_tokens`、`output_text_tokens`、`output_reasoning_tokens` 等。
- 当前 `cost_tracker.go` 按 `PromptTokens * InputPrice + CompletionTokens * OutputPrice` 估算成本（`docs/TEST_REPORT.md` §3.3 已指出 mock usage 不准）。迁移后需要把细粒度 usage 映射回 `llm.Usage`，否则成本和预算链路会断。
- Anthropic/DeepSeek 是否也迁移？Responses API 是 OpenAI 独家协议，Anthropic 仍用 Messages API、DeepSeek 仍兼容 OpenAI Chat Completions；引入 Responses API 意味着再多一个 Provider（可用 adapter），不会完全替代现有体系。

### 3.5 多 Provider 支持影响

- 项目当前已抽象出 `Provider` 接口，并支持 OpenAI-compatible / Anthropic / Mock。若引入 Responses API，最佳做法是新增一个 `ResponsesProvider`（或 `OpenAIResponsesProvider`）实现同一接口，而不是替换 `OpenAIProvider`。
- 这会增加一层维护成本：
  - Chat Completions Provider 保留给 DeepSeek 等兼容端点
  - Responses Provider 给 OpenAI 新模型
  - AnthropicProvider 继续存在
- 配置层（`config.ModelConfig.Provider`, `provider_factory.go`）需要新增 `"openai-responses"` 之类的 provider 名，并确保 Router / Registry 正确路由。

### 3.6 Mock Provider / 测试迁移成本

- `MockProvider` 不依赖 HTTP，仅依赖统一 `ChatRequest` / `StreamChunk` / `ToolCall` 类型。只要保留统一类型不变，新增 Responses Provider 不会强制改动 mock。
- 但如果想测试 Responses Provider，需要新增基于 `httptest.Server` 的 mock responder（当前 `openai_provider.go` 和 `anthropic_provider.go` 单测覆盖率为 0，`docs/TEST_COVERAGE_REPORT.md` §3.3 已标红）。这部分工作量约为 1-2 天。
- `mock_store.go`、`mock_builtin.go` 中内置的 6 个 case 使用 `llm.ToolCall` 和 `llm.Message`，迁移到 Responses API 时如果统一类型发生破坏性变更，这些脚本需要同步更新。

### 3.7 ReAct Engine 影响

- Engine 的核心是维护 `[]llm.Message` 并在每轮 think 时把完整历史发给 LLM。Responses API 的 `previous_response_id` 让"历史管理"从应用层部分上移到 API 层。
- 这带来两个选择：
  1. **浅适配**：在 Provider 内部把 `[]llm.Message` 转换成 `input` + `previous_response_id`（可选地截断历史）。这样 Engine 几乎不用改，但无法利用 Responses API 的 native 多轮/内置工具/annotation 优势。
  2. **深适配**：Engine 改为使用 `previous_response_id` 而非自己拼 messages。这需要重写消息持久化、session 多轮对话、上下文压缩、记忆召回注入等逻辑，影响面极大。
- 当前项目尚有多个 P0/P1 缺陷未收敛（`docs/TEST_REPORT.md` 34 PASS / 8 FAIL / 3 SKIP；`docs/PHASE7_PLAN.md` 中 M4/M5/M6/F8/F9 等遗留未实施）。深适配会放大风险。

### 3.8 成本 / 回放 / 可观测性影响

- 成本链路：`Usage` 映射错误会直接影响 `CostBudgetRule`、`CostTracker`、`/api/costs` 聚合。
- 回放链路：`session_messages` 表按 role/content/tool_call_id/tool_calls JSON 存储（`cmd/server/main.go:1009-1019`）， Responses API 的 input/output items 不能直接写入现有 schema。
- 可观测性：`agent_status` / `llm_delta` 等前端事件基于 `llm.StreamChunk`，若 Responses Provider 的事件聚合不准确，白盒 Agent 的可观测性会退化。

### 3.9 SDK / 依赖影响

- 当前项目**未引入** `github.com/openai/openai-go` 或任何官方 SDK，LLM 调用全是手写 HTTP + SSE（`go.mod` 只依赖 uuid、websocket、crypto、sqlite）。
- 若迁移 Responses API，手动维护 SSE 事件解析的成本显著高于 Chat Completions。引入官方 SDK 可以简化实现，但会增加一个外部依赖、提高审计/安全/版本锁定负担，并与项目"从零实现、最大控制权"的设计哲学相冲突。

---

## 4. 推荐结论

### 4.1 是否迁移？

**不建议在当前阶段全面迁移**。判定为：**中等偏高工作量、中等收益、当前非必要**。

理由：

1. **当前 LLM 端点 DeepSeek 完全兼容 Chat Completions**，也是项目默认模型。迁移 Responses API 并不会改善主力路径。
2. **Responses API 是 OpenAI 专有协议**，Anthropic / DeepSeek / 其他国产模型不会跟进；引入它只会让 Provider 矩阵更复杂（再多一个 adapter）。
3. **项目设计哲学是白盒 + 完全控制**；Responses API 的 native context、内置工具、annotations 是便利抽象，但与 Engine 当前显式消息管理冲突，要么浅适配（收益小），要么深适配（风险大）。
4. **Phase 7 优先级是安全、RBAC、可观测、外部向量等生产化能力**，而非 LLM 协议升级。迁移 Responses API 会分散 P0/P1 修复精力。
5. **若未来 OpenAI 模型（如 o3/gpt-5 系列）只在 Responses API 提供新能力**，可以届时新增 `OpenAIResponsesProvider` 作为可选 Provider，而不是替换 `OpenAIProvider`。

### 4.2 若一定要迁移，建议分阶段

若产品/业务上确定必须使用 Responses API（例如依赖 `web_search_preview`、`computer_use_preview` 等内置工具），建议按以下顺序：

#### Phase A：能力验证（约 2-3 天，不落地主分支）
- 用独立分支实现 `responses_provider.go` 原型，支持非流式 + 流式基本 text completion。
- 端到端验证 `dialogue` case 能跑通（不改动 Engine，仅新增 Provider）。
- 输出事件映射表和 usage 映射表。

#### Phase B：统一类型兼容（约 3-4 天）
- 扩展 `llm.ChatRequest`：新增 `UseResponsesAPI bool`、`PreviousResponseID string`（不影响 Chat Completions）。
- 扩展 `llm.Usage`：新增细粒度字段，保留原有 `PromptTokens/CompletionTokens/TotalTokens`。
- 在 `provider_factory.go` 增加 `"openai-responses"` 分支。
- 实现 Responses Provider 的 SSE 解析并接入 `Provider` 接口。

#### Phase C：Engine 浅适配（约 3-5 天）
- 在 Engine `think()` 中根据 `provider.Name()` 决定是否回传完整 messages 或 `previous_response_id`。
- 处理 function/tool call result：把 `llm.Message{role: "tool"}` 转换为 Responses API 的 `input[type=function_call_output]`。
- 保持 MockProvider 和其他 Provider 不变。

#### Phase D：完整回归与文档（约 2-3 天）
- 补 `OpenAIResponsesProvider` 的单元测试（当前真实 provider 单测覆盖率为 0，是历史债务）。
- 跑 `scripts/cases-regression.sh` / `scripts/smoke-test.sh` / `ws-smoke.go` 全量回归。
- 更新 `doc/chapters/09-llm-api-comparison.html` 与 `docs/API_CHANGELOG.md`。

**总估算**：完整落地约 2-3 周，且需要在当前 P0/P1 bug 收敛后再启动。

### 4.3 更推荐的替代方案

- **保持 Chat Completions 为主路径**，把人力集中在 `AnthropicProvider` 单测、`OpenAIProvider` 单测、以及 `runtime` 单测上（`docs/TEST_COVERAGE_REPORT.md` §8 P0/P1 已明确列出）。
- **当且仅当**未来必须调用仅支持 Responses API 的模型或原生 web-search/computer-use 能力时，再新增 `OpenAIResponsesProvider`，作为 `Provider` 矩阵的可选项而非默认项。
- 若只是想要"思维链展示/annotations/web search"，当前 DeepSeek `reasoning_content` 已覆盖思维链；外部 web search 可作为独立 Tool 在现有 function calling 体系内实现，无需切换到 Responses API。

---

## 5. 参考文件

- `internal/llm/provider.go`
- `internal/llm/openai_provider.go`
- `internal/llm/anthropic_provider.go`
- `internal/llm/mock_provider.go`
- `internal/llm/client.go`
- `internal/llm/provider_factory.go`
- `internal/runtime/engine.go`
- `docs/TEST_COVERAGE_REPORT.md`
- `docs/TEST_REPORT.md`
- `docs/PHASE7_PLAN.md`
- `docs/API_CHANGELOG.md`
- `doc/chapters/09-llm-api-comparison.html`
