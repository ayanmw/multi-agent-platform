# 多模型分层路由与 Agent 任务调度方案

> **生成日期**: 2026-07-25
> **状态**: 已完成 ✅（P1-P3 全部落地）
> **适用版本**: v0.14.1 Alpha
> **目标读者**: 平台维护者 / Agent 架构师

---

## 1. 背景与问题

当前平台已在 `internal/llm/router.go` 实现 Phase 6 Router，支持：

- 4 类 intent 分类：`simple_chat`、`code_generation`、`complex_reasoning`、`multi_step`。
- 5 级 model tier：`TierFree` / `TierEfficient` / `TierLightweight` / `TierStandard` / `TierPremium`。
- 基于规则过滤（上下文窗口、必备能力、预算、延迟）。
- `model_routed` 事件广播。

但当前 `DefaultProfiles()` 仅注册两个 DeepSeek 模型：

```go
deepseek-v4-flash → TierEfficient
deepseek-v4-pro   → TierStandard
```

`TierLightweight`、`TierPremium`、`TierFree` 没有对应模型。实际运行中：

- `complex_reasoning` 命中 `TierPremium`，但因无候选模型而回退到 `TierStandard`。
- `simple_chat` 本可走 `TierLightweight` 或 `TierEfficient`，但缺少轻量模型，往往直接复用 `deepseek-v4-flash`。
- 本地免费模型、API 模型、商业模型未做统一调度和 fallback 规划。
- Agent 粒度、task 粒度的模型绑定尚未落地（只能依赖全局路由）。

本方案旨在补全模型分层体系，建立可扩展的多模型路由与 Agent 任务调度策略。

---

## 2. 当前系统 vs 新方案

### 2.1 核心差异对照

| 维度 | 当前系统（v0.13.4） | 新方案 | 当前问题 |
|---|---|---|---|
| **模型池** | 只有 `deepseek-v4-flash` 和 `deepseek-v4-pro` | 引入 Claude/GPT/Gemini/Qwen/GLM/Kimi 等 20+ 模型，按 5 个 tier 分配 | `TierLightweight`、`TierPremium` 等 tier 空置 |
| **Intent 分类** | 4 类：simple_chat / code_generation / complex_reasoning / multi_step | 扩展到 8 类：新增 `code_execution`、`rag_retrieval`、`web_search`、`safety_sensitive` | 无法区分“写代码”和“运行代码”，也无法识别 RAG/搜索 |
| **分类器输出** | 单字符串 | JSON：primary_intent + secondary_intents + confidence + needs_tools + estimated_steps | 信息太少，无法做精准过滤 |
| **Agent 模型绑定** | 无，所有 Agent 走全局 Router | Agent 可配置 `PreferredModel`、`PreferredTier`、`AllowAutoRoute`、`MaxCostUSD` | 无法指定某个 Agent 固定用 Sonnet |
| **Provider 支持** | 仅 OpenAI-compatible + Mock | 新增 Anthropic、Gemini、Azure OpenAI 等 | 无法直接接入 Claude/GPT-5/Gemini |
| **Fallback** | 只有 profile.FallbackModel 静态配置 | 主模型失败后 Engine 自动重试 fallback，并广播事件 | 主模型 403/429 时会死循环 |
| **成本治理** | 仅记录成本，无限制 | Task-level 预算上限、Tier-level RPM 限流、预算超限中断 | 大任务可能无节制烧钱 |
| **可观测性** | 只有 `model_routed` | 新增 `model_fallback_used`、`model_rate_limited`、`intent_classified`、`cost_budget_exceeded` 等 | 用户看不到为什么选这个模型 |

### 2.2 旧流程（当前）

```
User Request
    │
    ▼
AgentRunner.Run
    │
    ▼
Engine.NewEngine (使用 cfg.LLMModel)
    │
    ▼
Engine.callLLM ──Router 启用?──┐
    │                          │
    │                        Router.Select
    │                        (4 类 intent)
    │                            │
    │                            ▼
    │                        模型分层: flash/pro
    │                            │
    ▼                            │
OpenAIProvider (deepseek endpoint)
    │
    ▼
ReAct Loop
    │
    ▼
CostTracker 记录
    │
    ▼
model_routed 事件
```

### 2.3 新流程

```
User Request
    │
    ▼
Agent 配置解析
(PreferredModel / PreferredTier / MaxCostUSD)
    │
    ▼
Agent 是否指定模型?
    │
    ├── 是 ──→ 命中指定模型
    │
    └── 否 ──→ Router.Select
                │
                ▼
        Intent Classifier
        (8 类 + JSON 输出
         confidence / needs_tools)
                │
                ▼
        模型分层过滤
        (5 tier + 能力 + 上下文
         + 预算 + RPM)
                │
                ▼
        选择 Primary + Fallback
                │
                ▼
        Provider 工厂
        (openai / anthropic / gemini / azure)
                │
                ▼
        Engine.callLLM
                │
                ▼
        预算 / 限流检查
                │
        ┌────────┴────────┐
        │                 │
        ▼                 ▼
     通过            超限/失败
        │                 │
        ▼                 ▼
   LLM 调用      cost_budget_exceeded
        │        / model_fallback_used
        │                 │
        ▼                 │
   调用失败?              │
        │                 │
    ┌───┴───┐             │
    │       │             │
   否      是(429/500)    │
    │       │             │
    ▼       ▼             │
 ReAct   重试 fallback   │
  Loop      │             │
    │       │             │
    └───────┴─────────────┘
                │
                ▼
        CostTracker 累计
                │
                ▼
        多事件广播
        (model_routed / intent_classified
         / model_fallback_used / model_rate_limited)
                │
                ▼
        前端 Inspector 展示
```

---

## 3. 设计目标

| 目标 | 说明 |
|---|---|
| 成本可控 | 简单任务用便宜模型，复杂任务才用强模型，避免一刀切换 Opus |
| 质量可控 | 不同任务类型（代码/推理/多步/评判）定向选择最擅长的模型 |
| 高可用 | 任一模型故障或限流时自动 fallback，不中断任务 |
| 可扩展 | 新增模型只需注册 profile + provider，不改动 engine/orchestrator |
| 可观测 | 每次路由决策都生成事件，前端可查看 "为什么选了这个模型" |
| 向后兼容 | 现有 `deepseek-v4-flash/pro` 配置继续工作 |

---

## 3. 模型分层策略

### 3.1 五级 Tier 定义

| Tier | 定位 | 成本 | 典型任务 |
|---|---|---|---|
| `TierFree` | 本地/免费模型 | $0 | 开发测试、冷备 fallback、提示工程快速迭代 |
| `TierEfficient` | 低成本高吞吐 | 极低 | 批量处理、校验、摘要、简单对话 |
| `TierLightweight` | 轻量分类/路由 | 低 | intent 分类、格式转换、关键词提取 |
| `TierStandard` | 主力 workhorse | 中 | 通用 ReAct Agent、代码生成、工具调用 |
| `TierPremium` | 顶级推理 | 高 | 复杂规划、架构设计、数学推理、leader 编排 |

### 3.2 推荐模型分层分配

基于价格、能力、延迟、tool-calling 稳定性综合判断：

| 模型 | 推荐 Tier | 角色定位 |
|---|---|---|
| `claude-opus-4-5` | `TierPremium` | 复杂推理 / 架构设计 / leader 规划 |
| `claude-opus-4-6` | `TierPremium` | 同上，后续版本 |
| `claude-sonnet-4-5` | `TierStandard` | 主力 Agent / 代码生成 / 工具调用 |
| `claude-sonnet-4-6` | `TierStandard` | 主力 Agent，能力更强 |
| `gpt-5.4` | `TierStandard` | 通用 Agent / 代码生成 |
| `gemini-3.1-pro-preview` | `TierStandard` | 长上下文 / RAG 检索增强 |
| `gpt-5.3-codex` | `TierStandard` | 专属代码 Agent（tool-calling 可配） |
| `deepseek-v4-pro` | `TierStandard` | 当前主力（已配置） |
| `claude-haiku-4-5` | `TierLightweight` | intent 分类器 / 轻量路由 |
| `gpt-5.4-mini` | `TierEfficient` | 轻量对话 / 校验 / 摘要 |
| `gemini-3-flash-preview` | `TierEfficient` | 批量处理 / 长上下文摘要 |
| `gpt-5.4-nano` | `TierEfficient` | 超低延迟简单任务 |
| `deepseek-v4-flash` | `TierEfficient` | 当前轻量主力（已配置） |
| `MiniMaxM2.5` | `TierFree` | 免费 API 测试 / 中文对话 |
| `Qwen3.5-122B` | `TierFree` | 本地/免费推理备选 |
| `Qwen3.5-397B` | `TierPremium` / `TierStandard` | 本地大模型，能力接近顶级 |
| `deepseek-v4-flash-local` | `TierFree` / `TierEfficient` | 本地部署，零成本 |
| `glm-4.7` / `glm-5.1-local` / `glm-5.2-local` | `TierFree` | 本地模型 / 测试 |
| `kimi-k2.6-local` / `kimi-k2.7-code-local` | `TierFree` | 本地模型 / 代码任务备选 |
| `step-3.7-flash` | `TierEfficient` | 免费/低成本模型，推理型，注意 max_token 控制 |

### 3.3 本地免费模型的 Fallback 设计

```
Paid API 失败/限流
       │
       ▼
FallbackModel 指向本地模型（deepseek-v4-flash-local / Qwen3.5-397B）
       │
       ▼
本地模型返回结果（可能质量略低，但任务不中断）
```

- 每个付费 profile 配置 `FallbackModel` 为本地等效模型。
- Router 在 `Select()` 阶段解析 fallback，写入 `RouteDecision.Fallback`。
- Engine 检测到 provider 返回 429/500/超时 时，用 `Fallback` 重试一次，并广播 `model_fallback_used` 事件。

---

## 4. Intent 分类增强

### 4.1 当前分类的问题

当前只有 4 类，对多 Agent 系统来说粒度不够：

- 无法区分 "需要写代码" 和 "需要运行代码"。
- 无法识别 "长上下文 RAG"、"安全敏感"、"需要外部搜索"。
- `multi_step` 和 `complex_reasoning` 经常重叠。

### 4.2 推荐扩展为 8 类

| Intent 类别 | 说明 | 目标 Tier |
|---|---|---|
| `simple_chat` | 闲聊、格式转换、问候 | `TierEfficient` |
| `code_generation` | 写代码、重构、生成配置文件 | `TierStandard` |
| `code_execution` | 运行 shell、测试、build | `TierStandard` |
| `complex_reasoning` | 数学、逻辑、架构设计、证明 | `TierPremium` |
| `multi_step` | 多 tool call、Agent 编排 | `TierStandard` |
| `rag_retrieval` | 需要检索记忆/文档再回答 | `TierStandard`（长上下文模型优先） |
| `web_search` | 需要外部搜索/实时信息 | `TierEfficient`（搜索+摘要可分离） |
| `safety_sensitive` | 涉及权限、敏感数据、审批 | `TierPremium`（保守决策） |

### 4.3 多标签分类与置信度

改为让分类器返回 JSON：

```json
{
  "primary_intent": "code_generation",
  "secondary_intents": ["multi_step"],
  "confidence": 0.92,
  "needs_tools": ["run_shell", "write_file"],
  "estimated_steps": 3
}
```

- `primary_intent` 决定目标 tier。
- `secondary_intents` 用于微调：例如 `code_generation + multi_step` 仍走 `TierStandard`。
- `needs_tools` 用于过滤模型能力（必须支持 `CapToolCalling`）。
- `estimated_steps` 用于后续步数/预算预估。

### 4.4 小型 SLM Classifier（未来）

当调用频次极高时，可用本地小模型（如 `Qwen3.5-122B`、`glm-4.7`）做 embedding-based 或 SLM classifier：

```
User Input
    │
    ▼
SLM Classifier（本地，零成本）
    │
    ▼
输出 intent + confidence
```

- 分类器准确率要求不必 100%，错分仅影响成本，不影响正确性。
- 错误或低置信度时回退关键字分类 + 默认 `TierStandard`。

---

## 5. 动态 Provider 注册

### 5.1 当前 Provider 抽象

`internal/llm/provider.go`：

```go
type Provider interface {
    Name() string
    Chat(req ChatRequest) (*ChatResponse, error)
    ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error)
}
```

当前只有 `OpenAIProvider` 和 `MockProvider`。

### 5.2 需要新增的 Provider

| Provider | 协议差异 |
|---|---|
| `AnthropicProvider` | `/v1/messages`，`x-api-key` header，tool call 格式 |
| `GeminiProvider` | `/v1beta/models/{model}:generateContent`，不同 tool schema |
| `LocalProvider` | OpenAI-compatible，但 endpoint 为本地 vLLM/Ollama |
| `AzureOpenAIProvider` | endpoint 结构不同，api-version 参数 |

### 5.3 Provider 配置隔离

推荐在 `.env` 中按 provider 隔离配置：

```env
# Default / DeepSeek
LLM_ENDPOINT=https://aicoding.dobest.com/v1
LLM_API_KEY=sk-xxx
LLM_MODEL=deepseek-v4-flash

# Anthropic
ANTHROPIC_ENDPOINT=https://api.anthropic.com/v1
ANTHROPIC_API_KEY=sk-ant-xxx

# Gemini
GEMINI_ENDPOINT=https://generativelanguage.googleapis.com/v1beta
GEMINI_API_KEY=AIza...

# Azure
AZURE_OPENAI_ENDPOINT=https://xxx.openai.azure.com
AZURE_OPENAI_API_KEY=...
AZURE_OPENAI_API_VERSION=2024-08-01-preview
```

`CreateProviderFromConfig` 增加按 provider 名构造：

```go
func CreateProvider(name, model string, cfg *config.Config) (Provider, error) {
    switch name {
    case "anthropic":
        return NewAnthropicProvider(cfg.AnthropicEndpoint, cfg.AnthropicAPIKey, model), nil
    case "gemini":
        return NewGeminiProvider(cfg.GeminiEndpoint, cfg.GeminiAPIKey, model), nil
    case "openai", "deepseek":
        return NewOpenAIProvider(name, cfg.LLMEndpoint, cfg.LLMAPIKey, model), nil
    default:
        return NewOpenAIProvider(name, cfg.LLMEndpoint, cfg.LLMAPIKey, model), nil
    }
}
```

### 5.4 Provider 与 ModelProfile 的绑定

每个 `ModelProfile` 有 `Provider` 字段，Router 选择模型后：

```go
provider := routerProviders[profile.Provider]
if provider == nil {
    provider = routerProviders[profile.Name] // fallback
}
```

因此启动时需要为每个 tier 的模型构造对应的 provider，注册到 `routerProviders` map。

---

## 6. Agent-level 模型绑定

### 6.1 Agent 配置扩展

当前 Agent 配置可能没有 `model` 字段，需要扩展：

```go
type Agent struct {
    ID              string
    Name            string
    SystemPrompt    string
    PreferredModel  string   // 指定具体模型，如 "claude-sonnet-4-6"
    PreferredTier   string   // 指定 tier，如 "standard"
    AllowAutoRoute  bool     // 是否允许 Router 重选
    MaxCostUSD      float64  // 单次 task 预算上限
    // ...
}
```

### 6.2 选择优先级

```
1. Agent.PreferredModel（非空且存在）→ 直接命中
2. Agent.PreferredTier（非空）→ 在该 tier 内路由
3. Router.Select(req) 自动选择
4. cfg.LLMModel 兜底
```

### 6.3 子 Agent 继承策略

在 multi-agent 场景下：

- **静态编排**（parallel/sequential/DAG）：子 Agent 使用自己的 `PreferredModel/Tier`；未指定时继承父 Agent 的 tier，但 Router 仍可重选。
- **动态 leader 派发**：leader 决定子任务描述，子 Agent 的模型由 Router 根据子任务描述重新分类选择。
- **特殊角色 override**：
  - `leader` / `decomposer` → 强制 `TierPremium` 或 `TierStandard`
  - `worker` → Router 选择（默认 `TierStandard`）
  - `validator` / `judge` → `TierLightweight` 或 `TierEfficient`
  - `summarizer` → `TierEfficient`

---

## 7. 多 Agent 场景下的路由策略

### 7.1 角色 → Tier 映射

| 角色 | 推荐 Tier | 说明 |
|---|---|---|
| `orchestrator` / `leader` | `TierPremium` | 负责拆分子任务、决策 |
| `worker` | `TierStandard` | 执行单步工具、代码 |
| `coder` | `TierStandard` | 可用 `gpt-5.3-codex` |
| `rag_retriever` | `TierStandard` | 优先长上下文模型（Gemini） |
| `web_researcher` | `TierEfficient` | 搜索+摘要，Flash 足够 |
| `validator` / `judge` | `TierEfficient` / `TierStandard` | 质量校验 |
| `summarizer` | `TierEfficient` | 结果汇总 |
| `fallback_backup` | `TierFree` | 付费模型失败时启用 |

### 7.2 "Cheap-First Retry" 策略

对于可接收质量略低的任务，先用便宜模型跑：

```go
func (r *Router) SelectWithRetry(ctx context.Context, req *RouteRequest) (*RouteDecision, error) {
    decision, err := r.Select(ctx, req)
    if err != nil {
        return nil, err
    }

    // 如果任务允许 cheap-first，先尝试 decision.Tier-1 的模型
    if req.AllowCheapFirst && decision.Tier > TierFree {
        candidates := r.registry.GetByTier(decision.Tier - 1)
        if len(candidates) > 0 {
            decision.Primary = candidates[0]
            decision.Tier = decision.Tier - 1
            decision.CheapFirstAttempt = true
        }
    }
    return decision, nil
}
```

- 第一次用便宜模型运行。
- LLM Judge 判定结果不合格 → 用原 tier 重试。
- 成本显著降低，适合 "摘要"/"初稿"/"分类" 等任务。

### 7.3 Validator Agent 的成本优化

```
Worker (Sonnet / GPT-5.4) → 产出结果
        │
        ▼
Validator (Haiku / Flash-mini) → 快速判定 pass/fail
        │
        ▼
   pass → 返回
   fail → 重跑或升级模型
```

Validator 不一定要用强模型，一个便宜模型做 pass/fail 分类即可。

---

## 8. 成本与预算治理

### 8.1 Task-level 预算上限

扩展 `Task` 或 `AgentRunSpec`：

```go
type AgentRunSpec struct {
    MaxCostUSD    float64 // 单次 task 最大成本
    MaxTokens     int     // 总 token 上限
    // ...
}
```

Engine 每次 LLM 调用后累加 `usage` 和 `cost`：

```go
if accumulatedCost > spec.MaxCostUSD {
    emit task_failed(cost_limit_exceeded)
    return
}
```

### 8.2 Tier-level Rate Limit

`ModelProfile.RateLimitRPM` 已在 profile 中定义。需要实现一个 `RateLimiter`：

```go
type RateLimiter struct {
    mu       sync.Mutex
    counters map[string][]time.Time // model → recent call timestamps
}
```

Router 在 filterCandidates 中排除当前已超限的模型：

```go
if limiter.IsLimitExceeded(m.Name) {
    candidates = append(candidates, fallbackModel)
}
```

### 8.3 成本追踪

当前已有 `CostTracker`，建议增强：

- 按 `task_id` 汇总总成本。
- 按 `model` 汇总调用次数、token 数、总成本。
- 按 `intent` 汇总各类别任务的平均成本。

---

## 9. 可观测性

### 9.1 新增事件

当前已有 `model_routed`。建议补充：

```go
model_routed              // Router 做出选择
model_fallback_used       // 主模型失败，使用 fallback
model_rate_limited        // 模型触发限流
model_cost_limit_warning  // 接近 task 预算阈值
cost_budget_exceeded      // task 成本超限失败
intent_classified         // 分类结果（含 confidence）
provider_request_failed   // provider 调用失败
```

### 9.2 前端展示

在 v2 控制室右侧 Inspector 增加 "Routing" 面板：

- 当前 step 使用的模型名
- Intent 分类结果与置信度
- 选择原因（`RouteDecision.Reason`）
- 累计 token/成本
- Fallback 链路

---

## 10. 架构图

```
                              User Request
                                   │
                                   ▼
                    ┌──────────────────────────────┐
                    │     Agent / Task 配置层       │
                    │  PreferredModel / PreferredTier│
                    │  MaxCostUSD / AllowAutoRoute   │
                    └──────────────┬───────────────┘
                                   │
                                   ▼
                    ┌──────────────────────────────┐
                    │       Intent Classifier       │
                    │  LLM / Keyword / SLM fallback │
                    │  → primary_intent + confidence│
                    └──────────────┬───────────────┘
                                   │
                                   ▼
                    ┌──────────────────────────────┐
                    │     Model Router              │
                    │  1. intent → tier             │
                    │  2. filter by capability      │
                    │  3. filter by context length  │
                    │  4. filter by budget/rate     │
                    │  5. select primary + fallback │
                    └──────────────┬───────────────┘
                                   │
                  ┌────────────────┼────────────────┐
                  │                │                │
                  ▼                ▼                ▼
            OpenAIProvider  AnthropicProvider  GeminiProvider
                  │                │                │
                  └────────────────┼────────────────┘
                                   │
                                   ▼
                    ┌──────────────────────────────┐
                    │      Engine ReAct Loop        │
                    │   tool call → execute → loop  │
                    └──────────────┬───────────────┘
                                   │
                                   ▼
                    ┌──────────────────────────────┐
                    │   Cost Tracker / Rate Limiter │
                    │   Task budget enforcement     │
                    └──────────────┬───────────────┘
                                   │
                                   ▼
                    ┌──────────────────────────────┐
                    │   Event Bus → Frontend v2     │
                    │   model_routed / cost / usage │
                    └──────────────────────────────┘
```

---

## 11. 代码改动建议

### 11.1 文件清单

| 文件 | 改动 |
|---|---|
| `internal/llm/model_profile.go` | 扩展 `DefaultProfiles()`，注册更多模型；增加 `TierLightweight` / `TierPremium` 默认模型 |
| `internal/llm/router.go` | intent 类别扩展到 8 类；支持多标签分类 JSON；增加 `RouteRequest.AllowCheapFirst` |
| `internal/llm/provider.go` / `provider_registry.go` | 增加 `CreateProvider` 工厂函数；支持 anthropic/gemini/azure |
| `internal/llm/anthropic_provider.go` | 新增 |
| `internal/llm/gemini_provider.go` | 新增 |
| `internal/llm/rate_limiter.go` | 新增 `RateLimiter`，按 model RPM 限流 |
| `internal/llm/cost_tracker.go` | 增强按 task/intent/model 汇总 |
| `internal/runtime/engine.go` | 接入 budget enforcement；fallback 重试 |
| `internal/agent/agent.go` | 扩展 Agent 配置：`PreferredModel/Tier/AllowAutoRoute/MaxCostUSD` |
| `pkg/db/agent.go` | migration：新增 agent 配置字段 |
| `web/v2/src/` | Inspector 增加 Routing 面板 |
| `pkg/event/event.go` | 新增 `model_fallback_used` 等事件常量 |

### 11.2 关键数据结构调整

```go
// RouteRequest 增加字段
type RouteRequest struct {
    UserInput       string
    ContextLen      int
    RequiredCaps    []ModelCapability
    BudgetUSD       float64
    LatencyReq      time.Duration
    PreferredTier   ModelTier
    PreferredModel  string
    AllowCheapFirst bool      // 是否允许先用便宜模型试跑
    AgentRole       string    // "leader"/"worker"/"validator"
}

// RouteDecision 增加字段
type RouteDecision struct {
    Primary           *ModelProfile
    Fallback          *ModelProfile
    Intent            string
    Confidence        float64
    Reason            string
    Tier              ModelTier
    CheapFirstAttempt bool
}
```

---

## 12. 实施优先级与工作量估算

| 阶段 | 内容 | 优先级 | 估算工作量 |
|---|---|---|---|
| P0 | 扩展 `DefaultProfiles()`，完成模型分层映射 | 高 | 2-3h |
| P0 | Agent 配置表迁移 + `PreferredModel/Tier` | 高 | 3-4h |
| P0 | Router 扩展 8 类 intent + JSON classifier | 高 | 4-5h |
| P1 | Provider 工厂函数 + Anthropic/Gemini provider | 中 | 6-8h |
| P1 | RateLimiter + Router 限流过滤 | 中 | 3-4h |
| P1 | Engine budget enforcement + fallback 重试 | 中 | 4-5h |
| P2 | 前端 Inspector Routing 面板 | 低 | 4-6h |
| P2 | 新增事件 + 成本汇总报表 | 低 | 3-4h |
| P3 | SLM classifier 本地嵌入方案 | 远期 | 8-12h |
| P3 | Cheap-First Retry 策略 | 远期 | 4-6h |

---

## 13. 风险与注意事项

| 风险 | 说明 | 缓解措施 |
|---|---|---|
| API key  leaked | 多 provider 配置增加泄露面 | 统一走 `.env`，不写入代码 |
| 模型名漂移 | 提供商可能更新模型名 | profile 用 `Name` 作为内部标识，可映射 |
| mock 测试回归 | 新增 provider 可能破坏 mock 路径 | 每个 provider 增加 mock case |
| 分类成本 | 每次请求都调用 classifier | 高频场景缓存分类结果 30s |
| Router 死循环 | fallback 指向无 provider 的模型 | 启动时校验 fallback 存在性 |

---

## 14. 下一步建议

1. **先完成 P0**：模型分层 + Agent 配置扩展 + Router 8 类 intent。让现有系统能从 `deepseek-v4-flash/pro` 平滑扩展到 `Haiku/Sonnet/Opus/GPT/Gemini` 等商业模型。
2. **P1 接入 Anthropic/Gemini provider**：解锁顶级模型的 tool-calling 能力。
3. **P2 完成前端可观测性**：让用户看到 "为什么选了这个模型"。
4. **P3 引入本地 SLM classifier 和 Cheap-First Retry**：进一步压低成本。

---

> 本方案为规划文档，实施前建议新建 OpenSpec change，并拆分为多个 task 逐步落地。
