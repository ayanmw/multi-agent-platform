# Spec: Agent Route Request Binding

## 概述

定义 `internal/agent.Agent` 中的模型绑定字段如何流转到 `internal/llm.RouteRequest`，以及缺失字段时的回退策略。

## 字段映射

| Agent 字段 | 类型 | RouteRequest 字段 | 说明 |
|------------|------|-------------------|------|
| PreferredModel | string | PreferredModel | 空字符串表示未指定 |
| PreferredTier | string | PreferredTier | 通过 `llm.ParseTier` 解析为 `ModelTier`；空字符串表示未指定 |
| AllowAutoRoute | bool | AllowCheapFirst | 仅当 PreferredModel 为空时生效；语义为允许用更低 tier 试跑 |
| MaxCostUSD | float64 | EngineConfig.MaxCostUSD | 在 Engine 级别生效，作为 Agent 预算上限 |
| Role | AgentRole | AgentRole | `"leader"` / `""` (worker) |

## 回退策略

1. 若 DB 中存在 Agent 记录，以 DB 字段为准。
2. 若 DB 中不存在 Agent 记录（匿名 session / 临时 agent），以 `AgentRunSpec` 中的字段为准。
3. 若两者都未设置，则走 Router 自动选择路径。

## 边界条件

- `PreferredModel` 存在但不在 Registry 中：Router 仍尝试按 PreferredModel 命中；`Select` 中应检查命中失败时回退到自动选择（当前未实现，需在 P3 补齐或后续处理）。
- `PreferredTier` 非法字符串：视为未指定并记录 warn 日志。
- `MaxCostUSD <= 0`：视为无预算限制。
- `AllowAutoRoute=true` 且 `PreferredModel` 非空：不启用 cheap-first，因为用户已显式指定模型；仅当未指定模型时 cheap-first 生效。
