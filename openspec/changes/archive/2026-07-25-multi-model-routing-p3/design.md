# Design: Multi-Model Layered Routing P3

## 1. Provider 注册表构建

### 1.1 数据来源

- `cfg.Models []config.ModelConfig`：用户实际配置的模型（有 endpoint / api_key / provider）。
- `llm.DefaultProfiles()`：系统内置的完整 5-tier 模型池。

### 1.2 构建流程

1. 以 `DefaultProfiles()` 为基础注册所有 profile 到 `ModelRegistry`。
2. 创建一个 `map[string]llm.Provider`。
3. 对 `cfg.Models` 中的每个 `ModelConfig`：
   - 调用 `llm.NewProvider(llm.ProviderConfig{Name: mc.Provider, Endpoint: mc.Endpoint, APIKey: mc.APIKey, Model: mc.Name})`。
   - 以 `mc.Provider` 为 key 存入 map；同时以 `mc.Name` 为 key 存入 map（后续按模型名查找更直接）。
4. 对 `DefaultProfiles()` 中没有出现在 `cfg.Models` 的 profile：
   - 根据 profile.Provider 使用全局配置构造 provider：
     - `deepseek`, `openai` → 用 `cfg.LLMEndpoint` + `cfg.LLMAPIKey`。
     - `anthropic` → 用 `cfg.AnthropicEndpoint` + `cfg.AnthropicAPIKey`。
     - `gemini` → 用 `cfg.GeminiEndpoint` + `cfg.GeminiAPIKey`。
   - 同样以 provider 名和 model 名注册。

### 1.3 key 约定

```go
routerProviders := map[string]llm.Provider{
    "deepseek":            openAICompatibleProvider,
    "anthropic":           anthropicProvider,
    "gemini":              geminiProvider,
    "openai":              openAICompatibleProvider,
    "deepseek-v4-flash":   openAICompatibleProvider,
    "claude-sonnet-4-6":   anthropicProvider,
    // ...
}
```

`resolveProvider` 先按 `providerName` 查，再按 `modelName` 查。

## 2. Agent 字段 → RouteRequest 映射

| Agent 字段 | RouteRequest 字段 | 回退/说明 |
|---|---|---|
| PreferredModel | PreferredModel | 空字符串表示不指定 |
| PreferredTier (string) | PreferredTier (ModelTier) | 空字符串表示不指定；通过 `llm.ParseTier` 解析 |
| AllowAutoRoute | AllowCheapFirst | 仅当为 true 且未指定 PreferredModel 时启用；语义上相当于 "允许先尝试更便宜模型" |
| MaxCostUSD | MaxCostUSD（EngineConfig） | 0 表示无限制 |
| Role | AgentRole | leader → "leader"；worker 默认 |

`runner.go` 在构造 `EngineConfig` 后、启动 Engine 前，按 Agent DB 记录填充这些字段。若 DB 中找不到 agent 记录（例如匿名 session / 临时 agent），使用 AgentRunSpec 中已有的字段作为回退。

## 3. Router 事件去重

### 3.1 当前问题

`Select` 在每次 think 中都被调用，每次都 emit `intent_classified`。同一 task 的多次 think 应该只发一次，因为 intent 在该 task 生命周期中通常不变。

### 3.2 方案

在 `Router` 上增加：

```go
emitted   map[string]bool // key: taskID+"/"+agentID+"/"+eventType
emitMu    sync.Mutex
```

增加辅助方法 `emitOnce(eventType, key, data)`：`intent_classified` 使用 `emitOnce`，`model_rate_limited` 允许重复。

`SetBroadcaster` 时初始化 `emitted`，兼容测试中未调用 SetBroadcaster 的场景。

## 4. RateLimiter 接入真实调用

### 4.1 当前问题

`RateLimiter` 只在 `Router.filterCandidates` / `pickCheaperModel` 中读，真实调用不更新，限流于运行中不生效。

### 4.2 方案

把 `RateLimiter` 提升到 `EngineConfig` 中，使 Engine 在每次 think 成功后调用：

```go
if e.cfg.RateLimiter != nil {
    e.cfg.RateLimiter.RecordCall(selectedModel)
}
```

`cmd/server` 在创建 `Router` 时把同一个 `RateLimiter` 同时传给 `Router` 和 `EngineConfig`。

注意：`RateLimiter` 是并发安全的，单一实例在所有 Engine / Router 间共享。

## 5. 前端 RoutingPanel 增强

### 5.1 需求

- 把 `model_fallback_used` 显示为橙色警告徽章，含 primary → fallback 的切换说明。
- 把 `cost_budget_exceeded` 显示为红色错误徽章，直接说明预算已耗尽。
- 最近 N 条事件流保留，但重复 `model_routed`（同一 task 的同一模型重复）可只显示最新一条，避免时间线刷屏。

### 5.2 实现

在 `useRouteEvents.ts` 的 `decisionsByTask` 计算属性中增加 `fallbacks`、`budgetExceeded` 标记；在 `RoutingPanel.vue` 中读取这些标记渲染状态条，而不是仅依赖事件列表。

## 6. 测试策略

- `internal/llm`: `TestRouter_EmitIntentClassified_Once` 验证去重；`TestRateLimiter_RecordCallInEngine` 通过 Engine 集成测试覆盖。
- `internal/runtime`: 新增 `engine_routing_test.go`，使用 fake provider / fake router / fake bus 验证：
  -  budgets exceeded 时发出 `cost_budget_exceeded` 并终止任务。
  - 主 provider 失败、fallback provider 成功时发出 `model_fallback_used` 且任务完成并带有 fallback 结果。

## 7. 风险与回退

- 若 `cfg.Models` 为空，仍然用 `DefaultProfiles()` 构建 provider 并回退到全局 endpoint，向后兼容。
- Anthropic/Gemini provider 当前是 stub，不保证真实调用成功；但 provider 工厂和注册链路可独立验证。
- RateLimiter 默认 `fallbackLimit = 60 RPM` 在未识别模型上生效，本地模型（RPM=0）始终不限流。
