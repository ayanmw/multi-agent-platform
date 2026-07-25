# Proposal: Multi-Model Layered Routing P3

## Why

P1-P2 已经落地了 Provider 工厂、RateLimiter、Engine 成本预算治理、Fallback 重试、路由事件可观测性与前端 Routing 面板。但当前代码还存在若干集成缺口，导致 Router 在真实运行时并不完全生效：

1. `cmd/server` 启动时没有为每个 tier 模型预注册对应 provider，Router 选择 Anthropic/Gemini 模型后 `resolveProvider` 找不到 provider，会回退到 OpenAIProvider 并使用不兼容的 endpoint。
2. Agent 中已有的 `PreferredModel`、`PreferredTier`、`AllowAutoRoute`、`MaxCostUSD` 字段没有被 runner 读取并传入 `RouteRequest`，全局路由仍然只依赖用户输入文本。
3. `intent_classified` 事件在每个 think step 都会广播，同一 task 的多次 think 产生重复事件，前端事件流显得嘈杂。
4. `RateLimiter` 只在 Router 候选过滤时被检查，但真实 LLM 调用成功后没有 `RecordCall`，限流状态不会随运行过程更新。
5. 前端 RoutingPanel 尚未显式展示 `model_fallback_used` 和 `cost_budget_exceeded` 这类关键链路状态。

## What Changes

- **预注册 RouterProviders**: 在 `cmd/server/main.go` 启动流程中，遍历 `cfg.Models` + `DefaultProfiles()`，为每个 profile 使用 `llm.NewProvider` 工厂创建 provider，并以 `providerName` 和 `modelName` 两个 key 注册到 `routerProviders` map，然后注入到 `AgentDeps.RouterProviders`。
- **Agent 配置传入 RouteRequest**: 在 `AgentRunSpec` 中增加 `PreferredModel`、`PreferredTier`、`AllowAutoRoute`、`MaxCostUSD`、`AgentRole` 字段；`runner.go` 根据 Agent DB 记录或 spec 字段填充 `RouteRequest`；`Engine.think` 在调用 `Router.Select` 时传入这些字段。
- **Router 事件去重**: 在 `Router` 中维护一个按 `(taskID,agentID)` 去重的 `emitted` 集合，使 `intent_classified` 在每个 task 生命周期内只广播一次；`model_rate_limited` 仍然每次限流都广播。
- **RateLimiter 接入真实调用**: 在 `Engine.think` 调用成功后调用 `rateLimiter.RecordCall(selectedModel)`。需要把 RateLimiter 从 Router 中透传到 Engine，或添加 `EngineConfig.RateLimiter` 字段。
- **新增路由事件测试**: 在 `internal/runtime` 增加 Engine 级测试，验证 `model_routed`、`model_fallback_used`、`cost_budget_exceeded` 事件的触发条件。
- **前端 RoutingPanel 增强**: 在 `RoutingPanel.vue` 中把 fallback / budget 事件渲染为显式状态徽章，并减少重复事件对时间线的干扰。

## Capabilities

### New Capabilities

- `multi-model-router-provider-registration`: 定义启动期 provider 注册表构建逻辑、key 约定和缺失 provider 的处理。
- `agent-route-request-binding`: 定义 Agent 配置字段到 `RouteRequest` 的映射规则，以及字段缺失时的回退策略。
- `router-event-deduplication`: 定义每个 task 生命周期内 `intent_classified` 只广播一次的语义和实现。
- `rate-limiter-runtime-update`: 定义 LLM 调用成功后对 RateLimiter 的更新契约。

### Modified Capabilities

- `model-routing-engine-integration`: 在 P2 基础上扩展，要求 RouteRequest 包含 Agent 级绑定字段。
- `routing-panel-component`: 在 P2 基础上扩展 fallback / budget 状态展示。

## Impact

- `cmd/server/main.go`: 新增 `initRouterProviders` 或内联逻辑。
- `cmd/server/runner.go`: `AgentRunSpec` 字段扩展；`runAgentLoopWithTurn` 中读取 Agent 记录并填充 `EngineConfig` / `RouteRequest`。
- `internal/llm/router.go`: 新增去重集合与 `SetTaskScope` 方法；`Select` 签名不变。
- `internal/runtime/engine.go`: 新增 `EngineConfig.RateLimiter` 字段；`think` 中传入 `RouteRequest` 字段并在成功后 `RecordCall`。
- `web/v2/src/components/RoutingPanel.vue`: 增强事件展示。
- 相关测试文件新增/更新。
