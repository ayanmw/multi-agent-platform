# Spec: Router Provider Registration

## 概述

定义 `cmd/server` 启动时如何为所有模型 tier 构建 `map[string]llm.Provider`（`RouterProviders`），供 Engine 在 Router 决策后解析所选模型对应的 provider。

## 注册表 key

`RouterProviders` 是一个 `map[string]llm.Provider`，key 包含两类：

1. **Provider 名**：如 `"openai"`、`"deepseek"`、`"anthropic"`、`"gemini"`。
2. **模型名**：如 `"deepseek-v4-flash"`、`"claude-sonnet-4-6"`。

`Engine.resolveProvider` 的查找顺序为先 provider 名、后模型名：

```go
func resolveProvider(providers map[string]llm.Provider, providerName, modelName string) llm.Provider {
    if providers == nil {
        return nil
    }
    if p, ok := providers[providerName]; ok {
        return p
    }
    if p, ok := providers[modelName]; ok {
        return p
    }
    return nil
}
```

## 构建流程

1. 初始化 `routerProviders := make(map[string]llm.Provider)`。
2. 遍历 `cfg.Models`：
   - 调用 `llm.NewProvider(llm.ProviderConfig{Name: mc.Provider, Endpoint: mc.Endpoint, APIKey: mc.APIKey, Model: mc.Name})`。
   - 以 `mc.Provider` 和 `mc.Name` 为 key 注册（后写入覆盖前者，以模型名为 key 的项指向同一实例）。
3. 遍历 `llm.DefaultProfiles()`：
   - 若模型名已存在于 `cfg.Models` 中，跳过。
   - 否则根据 `profile.Provider` 选择全局配置：
     - `deepseek` / `openai`：`cfg.LLMEndpoint` + `cfg.LLMAPIKey`
     - `anthropic`：`cfg.AnthropicEndpoint` + `cfg.AnthropicAPIKey`
     - `gemini`：`cfg.GeminiEndpoint` + `cfg.GeminiAPIKey`
   - 调用 `llm.NewProvider` 创建 provider 并注册。

## 异常处理

- `llm.NewProvider` 失败（例如缺少 API key）时记录 warn 日志，但不阻塞 server 启动；该 provider 不会被注册到 map，相关模型在 Router 选择后会回退到默认 OpenAIProvider（可能真实调用失败，但链路可用）。
- 未知 provider 类型（非 openai/deepseek/anthropic/gemini）时，`NewProvider` 会回退到 OpenAIProvider；Endpoint 使用全局 `cfg.LLMEndpoint`。
