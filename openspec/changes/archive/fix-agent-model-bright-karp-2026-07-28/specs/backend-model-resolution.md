# spec: agent.Model 生效（后端）

## 功能

`single_model` 模式下，`agents.model` 字段作为固定模型；为空时回退到 `cfg.LLMModel`。

## 接口与数据流

### AgentRunSpec 更新

```go
// AgentRunSpec 增加 Model 字段
type AgentRunSpec struct {
    // ... existing fields ...
    Model            string
    PreferredModel   string
    PreferredTier    string
    ModelMode        string
    AllowFallback    bool
    MaxCostUSD       float64
}
```

### 模型解析函数

```go
func resolveEffectiveModel(spec AgentRunSpec, agent *db.Agent, cfg *config.Config) string {
    if spec.Model != "" {
        return spec.Model
    }
    if agent != nil && agent.Model != "" {
        return agent.Model
    }
    return cfg.LLMModel
}
```

### runner.go 改动

- 位置：读取 agent 信息后的 provider 创建位置。
- 行为：
  1. `effectiveModel := resolveEffectiveModel(spec, agent, cfg)`
  2. `provider, err := llm.CreateProviderFromConfig(cfg, effectiveModel, caseID)`
  3. `EngineConfig.Model = effectiveModel`
- `ModelMode` 保持从 agent 读取，默认 `single_model`。

### orchestrator.go 改动

- 在 `runAgent` 中新增：
  ```go
  effectiveModel := spec.Model
  if effectiveModel == "" && db.DB != nil && spec.AgentID != "" {
      agent, _ := db.QueryAgentByID(spec.AgentID)
      if agent != nil && agent.Model != "" {
          effectiveModel = agent.Model
      }
  }
  if effectiveModel == "" {
      effectiveModel = cfg.LLMModel
  }
  ```
- provider 与 `EngineConfig.Model` 使用 `effectiveModel`。
- 将 agent 的 `ModelMode`、`PreferredModel`、`PreferredTier`、`AllowFallback`、`MaxCostUSD` 注入 `EngineConfig`（统一读取，空值走默认值）。

## 边界条件

- `agent.Model` 为 `""` → 回退 `cfg.LLMModel`。
- `spec.Model` 非空 → 最高优先级（允许 cron/leader override）。
- DB 查询失败 → 回退 `cfg.LLMModel`。
- `auto_route` 模式 → 不使用 `agent.Model`，仍由 Router 根据 `PreferredModel/Tier` 选择。
