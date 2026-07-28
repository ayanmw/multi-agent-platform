# design: agent.Model 生效与前端 agent 选择下发

## 总体架构

```
┌─────────────────────────────────────────────────────────┐
│  Frontend (web/v2)                                       │
│  OptionsFlyout.vue ──emit──▶ App.vue ──▶ useTaskStore   │
│  AgentConfig.vue ──▶ /api/agents/:id (model dropdown)   │
└─────────────────────────────────────────────────────────┘
                            │
                            ▼ HTTP/WS
┌─────────────────────────────────────────────────────────┐
│  Backend                                                 │
│  /api/tasks ──▶ AgentRunner.Run(ctx, spec)              │
│     resolveAgent(spec.AgentID): model → EngineConfig     │
│     single_model: cfg.Model = agent.Model || cfg.LLMModel│
│     auto_route: Router 使用 PreferredModel/Tier          │
│  orchestrator.runAgent() 子 agent 同样查 DB model        │
└─────────────────────────────────────────────────────────┘
```

## 模型解析优先级

```go
func resolveEffectiveModel(spec AgentRunSpec, agent *db.Agent, cfg *config.Config) string {
    if spec.Model != "" { return spec.Model }
    if agent != nil && agent.Model != "" { return agent.Model }
    return cfg.LLMModel
}
```

## 后端改动点

### cmd/server/runner.go

- `AgentRunSpec` 增加 `Model string` 字段（显式 override）。
- 在读取 agent 信息同一块中，增加读取 `agent.Model`。
- 使用 `effectiveModel` 变量：`spec.Model → agent.Model → cfg.LLMModel`。
- `CreateProviderFromConfig(cfg, effectiveModel, caseID)`。
- `EngineConfig.Model = effectiveModel`。
- `handleRunCase` / `handleSessionChat` 透传 `AgentID`（已有）。

### internal/orchestrator/orchestrator.go

- `runAgent` 中，若 `spec.Model == ""` 且 `spec.AgentID != ""`，查询 DB agent。
- 用 `agent.Model || cfg.LLMModel` 创建 provider 和 `EngineConfig.Model`。
- 从 agent 读取 `ModelMode`、`PreferredModel`、`PreferredTier`、`AllowFallback`、`MaxCostUSD` 注入 `EngineConfig`，使子 agent 也能走 `auto_route`。

### internal/runtime/engine.go

- 无需修改：`single_model` 直接使用 `e.cfg.Model`，`auto_route` 使用 Router。
- 防御性兜底已存在：若 `e.cfg.Model` 为空，engine 内部会回退到 `cfg.LLMModel`（`engine.go` 中 `CreateProviderFromConfig`）。

## 前端改动点

### OptionsFlyout.vue

- 增加 `modelValue` + `agentId` 双 prop，或直接使用 `v-model:agentId`（Vue 3.3+）。
- `selectedAgentId` 初始化为 `props.agents.find(a => a.is_default)?.id ?? props.agents[0]?.id ?? ''`。
- `watch(selectedAgentId, v => emit('update:agentId', v))`。
- 保留计算属性 `selectedAgent`。

### App.vue

- 新增 `currentAgentId` ref，默认 `''`。
- `OptionsFlyout` 绑定 `v-model:agentId="currentAgentId"`。
- `handleSend` 中调用 `startTask` / `startTurn` / `startMultiAgentTask` 时传入 `agentId: currentAgentId.value || undefined`。
- 若 `currentAgentId` 为空，store 回退到 `agent_default`。

### AgentConfig.vue

- model filter input 增加 `@keydown.enter`：若 `filteredModels` 只剩一项，自动选中。
- `single_model` 模式下保存前校验 `form.model` 非空。
- `preferred_model` 从手填改为 dropdown + filter，与 model 共用组件/逻辑。

## 数据模型

Agent 表字段已存在：`model`、`preferred_model`、`preferred_tier`、`model_mode`、`allow_fallback`、`max_cost_usd`。

## 测试策略

- 后端：`cmd/server/runner_model_test.go` 新建，覆盖 resolve 优先级。
- 后端：`internal/orchestrator/orchestrator_test.go` 覆盖子 agent DB model 读取。
- 前端：`web/v2/src/components/OptionsFlyout.test.ts` 覆盖 emit。
- 手动 E2E：改 default agent model → 运行 → 查看实际请求模型。

## 风险与回滚

- 风险：orchestrator 子 agent 从 DB 读取模型后会改变原行为；但父 agent 指定 `spec.Model` 仍可 override。
- 回滚：恢复 `runner.go` 与 `orchestrator.go` 的 model 解析为原 `cfg.LLMModel`。
