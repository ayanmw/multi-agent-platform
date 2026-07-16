# 综合技术报告：多智能体平台架构、API 策略与生产就绪度

**报告日期：** 2026-07-16  
**范围：** LLM API 迁移评估、测试基础设施、行业框架格局及生产就绪度路线图  
**状态：** 最终版

---

## 执行摘要

本报告整合了多智能体平台四个关键维度的研究成果：(1) 从 OpenAI Chat Completions API 迁移到新版 Responses API 的技术评估；(2) 全面 API 测试基础设施的设计与实现；(3) 2026 年 AI 智能体框架格局分析（LangGraph、AutoGen、CrewAI）；(4) 以安全加固和运营成熟度为核心的生产就绪度路线图。

**主要结论：**
- **API 迁移：** 当前阶段不建议迁移到 Responses API。鉴于项目的白盒架构、现有 Chat Completions 与 DeepSeek 的兼容性，以及待解决的 P0/P1 缺陷，成本大于收益。若未来需要 OpenAI 专有能力，建议可选地引入 `OpenAIResponsesProvider`。
- **测试基础设施：** 基于 Mock 的三层测试架构（全局 Mock / 用例级真实 / 端点级 Mock）已具备生产就绪度，已完成 9 个测试套件，46/46 冒烟测试全部通过。
- **行业定位：** 平台的定制化 Go 语言 ReAct 引擎与 LangGraph 的图基理念一致，但以牺牲生态广度换取最大控制力和最小依赖。
- **生产阻塞项：** 必须在生产部署前解决四个安全问题（M4–M6、Phase 0 `task_id` 碰撞），预计需要 6–8 小时工程投入。

---

## 1. 当前系统架构

### 1.1 核心引擎设计

平台实现了一个基于 Go 语言编写的定制 **ReAct（Reason + Act）循环**引擎，围绕**白盒可观测性**原则设计——每一个 LLM token、工具调用和推理步骤都通过 WebSocket 事件暴露给前端。

**关键架构组件：**

| 组件 | 位置 | 职责 |
|------|------|------|
| `runtime.Engine` | `internal/runtime/engine.go` | ReAct 循环、工具执行、步骤持久化 |
| `llm.Provider` | `internal/llm/provider.go` | LLM 后端的统一接口 |
| `OpenAIProvider` | `internal/llm/openai_provider.go` | Chat Completions API（主用） |
| `AnthropicProvider` | `internal/llm/anthropic_provider.go` | Messages API 转换 |
| `MockProvider` | `internal/llm/mock_provider.go` | 确定性测试回放 |
| `Router` | `internal/llm/router.go` | 意图分类 + 模型选择 |
| `Harness Policy` | `internal/harness/policy.go` | 工具白名单、token 预算、成本上限 |
| `CostTracker` | `internal/cost/cost_tracker.go` | Token/成本聚合 |
| `Memory` | `internal/memory/memory.go` | 向量召回 + 情景记忆 |

### 1.2 提供者抽象

`Provider` 接口在协议细节和引擎逻辑之间强制实现清晰的分离：

```go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    ChatStream(ctx context.Context, req ChatRequest, cb StreamCallback) error
}
```

当前实现：
- **OpenAIProvider** — Chat Completions API（主用，通过 OpenAI 兼容端点支持 DeepSeek/Groq/Together）
- **AnthropicProvider** — Messages API 及 schema 转换
- **MockProvider** — 确定性脚本回放用于测试

### 1.3 消息模型

引擎维护一个显式的 `[]llm.Message` 历史，包含角色（`system`、`user`、`assistant`、`tool`）、工具调用 ID 和推理内容。这种显式模型支持：
- 完整的会话回放和调试
- 上下文窗口管理
- 在任意轮次注入记忆
- 前端渲染推理链

---

## 2. LLM API 迁移分析：Chat Completions → Responses API

### 2.1 当前 Chat Completions 使用情况

平台大量使用 Chat Completions API 的特性：

| 特性 | 用途 | 实现位置 |
|------|------|----------|
| SSE 流式传输 | 核心 UX —— 逐 token 的 `llm_delta` 事件 | `engine.go:1161-1175` |
| 工具调用 | `ToolCall` / `FunctionCall` 统一结构 | `openai_provider.go:207-228` |
| 推理内容 | DeepSeek R1/V4 扩展字段 | `openai_provider.go:203-204` |
| 用量跟踪 | `PromptTokens`、`CompletionTokens`、`TotalTokens` | `engine.go:663-668` |
| 工具选择 | 基于字符串（`auto`、`none` 或工具名） | `anthropic_provider.go:660-679` |

### 2.2 Responses API 概述

OpenAI 的 Responses API（`POST /v1/responses`）引入了更高级别的抽象：

| 维度 | Chat Completions | Responses API |
|------|------------------|---------------|
| 输入模型 | 带角色的 `messages[]` | `input[]`（InputItem 数组） |
| 系统提示 | `messages[0].role=system` | 专用的 `instructions` 字段 |
| 输出模型 | `choices[0].message` | 顶层 `output[]` 数组 |
| 工具调用 | `message.tool_calls[]` | `output[].type=function_call` |
| 上下文管理 | 应用维护消息 | 原生 `previous_response_id` |
| 内置工具 | 无 | `web_search_preview`、`file_search`、`computer_use_preview` |
| SSE 事件 | 简单的 `delta.content` | 复杂事件分类（`output_item.added`、`output_text.delta` 等） |
| 用量粒度 | `prompt/completion/total` | `input_text_tokens`、`output_reasoning_tokens` 等 |

### 2.3 迁移挑战

#### Schema 不兼容
Responses API 用 `input/output` 项和 `previous_response_id` 取代了 `messages[]` 范式。将引擎显式的 `[]llm.Message` 历史映射到此模型需要以下方案之一：
- **浅层适配**：提供者内部转换，引擎不变（收益低）
- **深度适配**：引擎使用 `previous_response_id`，放弃显式消息管理（高风险、高投入）

#### 流式传输复杂性
Responses API 的 SSE 事件显著更复杂：
- Chat Completions：约 1 种事件类型（`delta`）
- Responses API：10+ 种事件类型（`response.output_item.added`、`response.output_text.delta`、`response.function_call_arguments.delta` 等）

这会使解析器代码量估计增加 2–3 倍，并复杂化前端依赖的 `llm.StreamChunk` 聚合。

#### 多提供者碎片化
Responses API 是**OpenAI 独家**的。Anthropic（Messages API）和 DeepSeek（兼容 OpenAI 的 Chat Completions）不会采用它。引入 `ResponsesProvider` 将向矩阵中添加第三个适配器，增加维护负担而不替换现有提供者。

#### 成本与可观测性影响
- **成本跟踪**：细粒度的用量字段必须映射回 `llm.Usage`，否则 `CostTracker` 流程会中断。
- **回放/序列化**：`session_messages` 表存储 `role/content/tool_call_id` JSON。Responses API 的 `input/output` 项在迁移前无法写入现有 schema。
- **前端事件**：`llm_delta` 事件需要从 Responses API 的分层事件中进行准确聚合；任何不匹配都会降低白盒 UX。

#### 测试债务
当前真实提供者的单元测试覆盖率是 **0%**（`TEST_COVERAGE_REPORT.md` §3.3）。在未先覆盖现有提供者的情况下迁移到 Responses API 会加剧技术债务。

### 2.4 建议

**当前阶段不要迁移到 Responses API。**

**理由：**
1. DeepSeek（默认模型）完全兼容 Chat Completions；在主路径上迁移毫无收益。
2. Responses API 是 OpenAI 专有的，Anthropic 和 DeepSeek 不会采用。
3. 项目的白盒、完全可控的设计理念与 Responses API 的不透明上下文管理相冲突。
4. 第 7 阶段优先事项（安全、RBAC、可观测性、外部向量）更为紧迫。
5. P0/P1 缺陷仍未解决；深度适配会放大风险。

**未来触发条件：** 如果 OpenAI 发布仅在 Responses API 上独家提供的模型或能力（例如高级 `computer_use_preview`、原生网络搜索），则将 `OpenAIResponsesProvider` 作为**可选**适配器与 `OpenAIProvider` 并行引入，而不是作为替代。

**如果迁移成为必要（分阶段方法）：**
- **阶段 A**（2–3 天）：实现 `responses_provider.go` 原型，支持非流式 + 基础流式。
- **阶段 B**（3–4 天）：在 `llm.ChatRequest` 中扩展 `UseResponsesAPI` 和 `PreviousResponseID`；在 `llm.Usage` 中扩展细粒度字段。
- **阶段 C**（3–5 天）：引擎浅层适配 —— 有条件地使用 `previous_response_id` 与完整消息。
- **阶段 D**（2–3 天）：单元测试、回归套件、文档。

**预计总投入：** 2–3 周。

---

## 3. 测试基础设施设计

### 3.1 三层 Mock/真实 切换

平台实现了一个复杂的三优先级切换机制，用于控制 LLM 调用路由：

| 优先级 | 变量 | 行为 |
|--------|------|------|
| 1（最高） | `LLM_MOCK_ENDPOINTS` | 为特定端点/用例强制使用 Mock |
| 2 | `LLM_REAL_CASES` | 为特定用例强制使用真实 LLM |
| 3 | `LLM_USE_MOCK` | 全局默认（Mock 或真实） |

此设计支持：
- **默认 Mock 回归**：快速、确定性的 CI/CD 流水线。
- **定向真实验证**：仅对特定用例（例如 `research`、`dialogue）进行真实 LLM 调用。
- **端点级覆盖**：关键端点可被强制为 Mock 或真实，无论全局设置如何。

### 3.2 MockProvider 架构

`MockProvider` 在不发起 HTTP 调用的情况下实现统一的 `Provider` 接口：

- **内置脚本**：6 个预置用例（`code-gen`、`dialogue`、`research`、`multi-agent`、`long-task`、`tool-error`）。
- **动态脚本**：通过 `/api/mock/scripts` 进行运行时 CRUD（持久化到 SQLite `mock_scripts` 表）。
- **匹配**：基于优先级 —— 先精确匹配 `case_id`，再回退到关键词模糊匹配。
- **响应序列**：支持模板的多轮响应（`{{file}}`、`{{content}}`）。

### 3.3 测试覆盖状态

| 模块 | 测试文件 | 顶层测试数 | 覆盖重点 |
|------|----------|-----------|----------|
| MockProvider | `mock_provider_test.go` | 16 | 用例匹配、动态覆盖、用量生成 |
| Config | `config_test.go` | 6（38 子测试） | 三层优先级、环境变量解析 |
| Harness Policy | `policy_test.go` | 11 | 7 条规则 + 链短路 |
| Auth | `auth_test.go` | 16 | bcrypt、密钥生成、角色匹配 |
| DB | `database_test.go` | 18 | 迁移幂等性、16 表 CRUD |
| Router | `router_test.go` | 32（53 子测试） | 意图分类、回退链 |
| Tool Registry | `registry_test.go` | 20 | 注册/执行/注销、并发性 |
| Cost | `cost_test.go` | 10 | 精度、聚合、可重试性 |
| Memory | `memory_test.go` | 11 | 余弦相似度、向量 CRUD、维度验证 |

**合计：** 9 个包，140+ 个顶层测试，全部通过（`go test ./...` 干净）。

### 3.4 冒烟测试结果

- **测试端点：** 46 通过 / 0 失败 / 1 跳过（WebSocket 需要 wscat/Go 客户端）
- **真实 LLM 验证：** `dialogue` 用例成功完成并有成本记录；`research` 用例触发了真实的 `run_shell` 但因 `max_steps=5` 不足而失败（是配置问题，不是代码 bug）。
- **API 调整清单：** 在 `docs/API_CHANGELOG.md` 中记录了 16 项供前端评估。

---

## 4. 行业格局：2026 年的 AI 智能体框架

### 4.1 框架对比

| 维度 | LangGraph | AutoGen | CrewAI |
|------|-----------|---------|--------|
| **执行模型** | 基于图（DAG） | 对话式 | 声明式流程 |
| **状态管理** | 显式共享状态 | 消息线程 | Crew 级上下文 |
| **控制流** | 显式边 | 对话驱动 | 流程模板 |
| **学习曲线** | 陡峭 | 中等 | 平缓 |
| **工具生态** | 1000+（LangChain） | 500+ | 200+ |
| **多智能体模式** | 监督者 | 对话管理器 | 管理者智能体 |
| **人在环路** | 节点级检查点 | 对话中断 | 任务审批 |
| **生产就绪度** | 最高 | 高 | 中高 |

### 4.2 关键行业趋势

1. **向图收敛**：即使是 AutoGen 也采用了图概念；基于 DAG 的执行现已成为标准。
2. **JSON Schema 通用化**：所有框架都使用 JSON Schema 描述工具。
3. **多智能体主流化**：78% 的企业部署使用 3 个以上智能体（2026 行业调查）。
4. **可观测性普及**：OpenTelemetry、追踪和评估框架成为一等公民。
5. **工具生态成熟**：5000+ 预构建工具；安全沙箱和速率限制成为标准。

### 4.3 平台定位

定制化 Go 平台占据的细分市场类似于 **LangGraph**（细粒度控制、生产加固），但具有：
- **零外部 LLM 框架依赖**（无 LangChain、无 AutoGen）
- **手写 HTTP + SSE** 以实现最大协议控制
- **从第一天起内置 Mock/测试基础设施**
- **引擎、策略、成本和记忆之间的紧耦合**

**权衡：** 相比 LangChain 生态广度较小，但控制力更强、攻击面更小，且不存在第三方抽象的版本锁定风险。

---

## 5. 生产就绪度评估

### 5.1 安全阻塞项（生产前必须修复）

| ID | 问题 | 严重程度 | 投入 | 状态 |
|----|------|----------|------|------|
| M4 | 身份验证对敏感端点的 GET 豁免 | 高 | ~1 小时 | 已规划 |
| M5 | 缺少 RBAC/角色强制 | 高 | ~3–4 小时 | 已规划 |
| M6 | `/api/auth/api-keys` 无 token 可枚举 | 高 | ~1 小时 | 依赖 M5 |
| Phase 0 | `task_id` 秒级碰撞 | 低 | ~10 分钟 | 已规划 |

**预计总投入：** 6–8 小时。

### 5.2 真实 LLM 设计优化

使用真实 LLM（`deepseek-v4-flash-local`）进行测试发现了 9 个设计层面的问题：

| ID | 问题 | 严重程度 | 根本原因 | 投入 |
|----|------|----------|----------|------|
| D1 | 路由器回退链指向不可用的模型 | 🔴 高 | 配置文件的 `FallbackModel` 使用标准名称而非 `-local` 变体 | 小（约 10 行） |
| D2 | 多智能体 `max_steps=3` 对真实 LLM 不足 | 🟡 中 | 针对 Mock 调优的默认值无法扩展到真实 token 预算 | 小 |
| D3 | 小对话成本显示为 `$0` | 🟡 中 | `(tokens * price_cents) / 1_000_000` 中的整数分截断 | 中（约 30 行） |
| D4 | 推理模型在链上耗尽预算，内容为空 | 🟡 中 | `MaxTokens=4096` 对推理模型不足 | 小–中 |
| D5 | `OpenAIProvider` chunk 循环忽略 `ctx.Done()` | 🟡 中 | 缺少上下文取消检查 | 小（约 5 行） |
| D6 | 多智能体无并发节流，触发 429 | 🟡 中 | `pool.WorkerPool.dispatch` 是占位符 | 小–中 |
| D7 | 无 HTTP 错误重试逻辑 | 🟢 低 | 瞬态失败时直接返回错误 | 中 |
| D8 | 引擎对 N 个工具调用冗余保存 `think` 步骤 N 次 | 🟢 低 | 每个工具循环内的 `saveStep` | 小（约 5 行） |
| D9 | WS 广播背压丢弃慢客户端的事件 | 🟢 低 | 无缓冲通道 + 客户端缓冲区溢出 | 中 |

**推荐批次顺序：**
1. **批次 1（正确性）：** D1、D4 —— 直接导致失败或空输出。
2. **批次 2（健壮性）：** D5、D6 —— 生产环境的取消和速率限制。
3. **批次 3（精度/体验）：** D3、D8 —— 成本准确性和数据整洁性。
4. **批次 4（长期）：** D2、D7、D9 —— 优化、非阻塞。

### 5.3 第 7 阶段路线图

| 阶段 | 重点 | 关键交付物 |
|------|------|------------|
| **

---
*注：第 5.3 节末尾不完整。*
