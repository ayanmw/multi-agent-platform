# Spec: Rate Limiter Runtime Update

## 概述

定义 `RateLimiter` 如何随 Engine 的真实 LLM 调用而被更新，使限流状态反映运行时而非仅在 Router 候选过滤时静态检查。

## 调用点

在 `Engine.think` 主模型调用成功后，立即调用：

```go
if e.cfg.RateLimiter != nil {
    e.cfg.RateLimiter.RecordCall(selectedModel)
}
```

调用位置在主模型**成功后**，fallback **之前**。理由：

- 若主模型失败并进入 fallback，主模型调用不应影响主模型的限流计数（实际未完成有效请求）。
- fallback 成功后也应记录 fallback 模型的调用。

因此更精确的做法：

```go
// 在最终成功的 provider 调用后
if e.cfg.RateLimiter != nil {
    e.cfg.RateLimiter.RecordCall(selectedModel)
}
```

`selectedModel` 在 fallback 成功后已被更新为 fallback 模型名，因此同一位置记录即可。

## EngineConfig 字段

```go
type EngineConfig struct {
    // ...
    RateLimiter *llm.RateLimiter
}
```

为 nil 时完全禁用运行时记录（不影响 Router 侧静态限流过滤）。

## cmd/server 注入

在 `main.go` 创建全局 `rateLimiter := llm.NewRateLimiter()`，并同时传给：

- `modelRouter := llm.NewRouter(modelRegistry, classifierProvider, rateLimiter)`
- 每个 `AgentDeps` 中的 `EngineConfig.RateLimiter` 字段（延迟到运行时 runner 注入）。

由于 `AgentDeps` 在创建时还没有 `RateLimiter` 字段，需要在 `AgentDeps` 中新增 `RateLimiter *llm.RateLimiter`。

## 测试要求

- Engine 配置 `RateLimiter`，主 provider 调用一次成功后，检查 `IsLimitExceeded(selectedModel)` 的行为变化（取决于 RPM 设置）。
- 不配置 `RateLimiter` 时 Engine 运行不受影响。
