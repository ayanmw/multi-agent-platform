# PROGRESS — 白盒多 Agent 协作平台 LOOP 执行日志（no-2 版本）

> 每轮 LOOP 在末尾「执行日志」追加一条；末尾 `[LOOP STATE]` 区块记录循环轮次、质量门、DONE 标志。
> 格式：`### YYYY-MM-DD HH:MM | 轮次 | Nx-NN | 状态 + 内容 + Commit + 验证 + 下一步`

---

## 初始化

- 2026-08-02 22:31 | 初始化 | LOOP 协议建立。初始里程碑：N0 缺陷修复（N0-01 路由 bug、N0-02 历史自复制）、N1 企业级核心（多轮历史/ AgentBus 闭环 / RBAC / Shell 沙箱 / Agent CRUD 前端 / 审计）、N2 质量加固。Phase R（评审-重规划）在任务清零时触发，目标：评审完美 + mock 21/21 + 设计符合企业级多 Agent 协作平台（见 LEARNINGS 10 维度）。预算 24h（validUntil 2026-08-03T22:31:06+08:00）。

---

## 执行日志

### 2026-08-03 08:50 | 轮次 1 | N0-01 | ✅ 修复 AgentBus 路由 bug（P0）

**根因**：`internal/runtime/engine.go` 的 `sendAgentMessageWithSubTask` 硬编码 `ToAgentID: ""`。AgentBus 的 handler 以 `(agentID, subTaskID)` / `agentID` 为键注册，空目标匹配不到任何 handler，消息只会进入 `maxQueue=100` 待投递队列滞留，并按「丢最旧」把真实待投递消息挤出队列。唯一调用方是 `approval_delegation.go` 的审批委托，实际能送达全靠 `cmd/server/runner.go` 的自投递兜底重发。

**改动**：
- `internal/runtime/engine.go`：新增常量 `DefaultSupervisorAgentID = "leader"`、配置字段 `EngineConfig.SupervisorAgentID`、辅助方法 `supervisorAgentID()`；把 `sendAgentMessageWithSubTask` 重构为 `sendAgentMessageTo(toAgentID, toSubTaskID, msgType, content) bool`，写入真实 `ToAgentID`，并对空目标**防御性拒绝 + Warn 日志**（宁可不发也不污染队列）；`system_info.to_agent` 由空串改为真实目标；`sendAgentMessage` 语义修正（此前把 toAgentID 误当 subTaskID 传）；`SendAgentMessage` 补注释明确其为「自投递」语义。
- `internal/runtime/approval_delegation.go`：`DelegatedApprovalRequest` 新增 `SupervisorAgentID` / `BusNotified` 字段；委托时按 `(supervisorAgentID, SupervisorSubTaskID)` 精确路由，并回写投递结果。
- `cmd/server/runner.go`：兜底自投递改为仅在 `!req.BusNotified` 时执行，消除路由修复后的**重复投递**（保证恰好一次）。
- `internal/orchestrator/orchestrator.go`：worker EngineConfig 显式设置 `SupervisorAgentID: runtime.DefaultSupervisorAgentID`。

**测试**：新增 `internal/runtime/agentbus_routing_test.go`（4 例：默认/自定义 supervisor ID、路由目标与事件断言、空目标拒绝、无 bus no-op）＋ `internal/orchestrator/orchestrator_test.go::TestAgentBus_WorkerToLeaderRouting`（投递侧：命中 leader handler；空目标不投递且滞留队列=1）。

**验证**：`go build ./...` ✅ / `go vet ./...` ✅ / `go test -count=1 ./...` ✅ 全绿（无失败包）；`go test ./internal/runtime/... ./internal/orchestrator/...` ✅。

**Commit**：`fabc678`（前置：网络测试确定性守卫归档）、`0c4ce7e`（N0-01 本体）

**下一步**：N0-02 修复多轮历史自复制（`engine.go:823` system prompt 回写 session_messages）。

---

## [LOOP STATE]

```
loop_round:        1
phase:             N0 (缺陷修复)
quality_gate_pass: false
done:              false
last_review:       (未执行)
next_milestone:    N0
budget_validuntil: 2026-08-03T22:31:06+08:00
```
