# multi-agent-orchestration Specification

## Purpose
TBD - created by archiving change extend-task-cases. Update Purpose after archive.
## Requirements
### Requirement: 静态编排策略 Case 覆盖
平台 MUST 提供覆盖三种静态编排策略的内置 case：parallel（worker 并行）、sequential（worker 顺序链式，前序输出作为后序输入）、DAG（带依赖的 A→B→C 编排）。静态编排的 agent 集合 MUST 在任务启动前由 decomposer 一次性确定，运行时不变。

#### Scenario: parallel 编排
- **WHEN** 运行 `multi-agent-parallel` case
- **THEN** orchestrator MUST 并行派发所有 worker
- **AND** 事件流 MUST 含对每个 worker 的 `agent_dispatched`（mode=parallel）与 `agent_completed`

#### Scenario: sequential 编排链式转发
- **WHEN** 运行 `multi-agent-sequential` case（researcher → writer）
- **THEN** 前序 agent 完成后其结果 MUST 经 AgentBus 转发为后序 agent 的 observation 输入
- **AND** 后序 agent MUST 在前序完成之后才启动

#### Scenario: DAG 依赖编排
- **WHEN** 运行 `multi-agent-dag` case（A→B→C 依赖链）
- **THEN** orchestrator MUST 按 `RunBlockingDAG` 的依赖顺序调度
- **AND** B MUST 在 A 产物就绪后启动，C MUST 在 B 产物就绪后启动

### Requirement: 动态编排 Case 覆盖
平台 MUST 提供覆盖 leader-driven 动态派发的内置 case：leader agent 运行时通过 `dispatch_sub_agent` 工具决定派发对象与策略，而非启动前静态拆分。leader 的 SubTaskID MUST 等于 root task ID。

#### Scenario: leader 运行时派发
- **WHEN** 运行 `multi-agent-leader-dispatch` case
- **THEN** leader agent MUST 在其 ReAct Loop 中调用 `dispatch_sub_agent` 工具
- **AND** 派发的 worker 的 `parent_task_id` MUST 等于真实 root task ID（非占位符）

#### Scenario: agent 互评消息往返
- **WHEN** 运行 `multi-agent-review` case（writer + reviewer + leader 裁决）
- **THEN** writer 与 reviewer 之间 MUST 通过 AgentBus 交换至少一轮消息
- **AND** leader MUST 汇总 reviewer 反馈作出最终裁决

### Requirement: 编排层可观测事件
无论静态还是动态编排，编排层 MUST 发出可观测事件供前端白盒追踪：`decompose_done`（拆分决策完成）、`agent_dispatched`（每个 worker 派发）、`agent_completed`（每个 worker 完成，携带 status/tokens/duration/result）。root task MUST NOT 是无事件的空壳。

#### Scenario: 编排事件完整
- **WHEN** 任意 L4/L5 多 Agent case 运行完成
- **THEN** 事件流 MUST 含 `decompose_done`、≥1 条 `agent_dispatched`、≥1 条 `agent_completed`
- **AND** root task 的可观测 step 数 MUST 大于 0（非空壳）

### Requirement: 故障容忍可回归
平台 SHOULD 提供 `multi-agent-fault-tolerance` case 验证 leader 对 worker 失败的处理。若底层尚不支持"真注入 worker 崩溃"，case 可降级为"验 leader 能处理 worker 返回 error 结果并产出降级结论"，但 MUST 在 case 描述中标注当前能力边界。

#### Scenario: worker 失败处理
- **WHEN** 运行 `multi-agent-fault-tolerance` case 且某 worker 返回失败结果
- **THEN** leader MUST 不因单 worker 失败而整体崩溃
- **AND** 任务终态 MUST 反映 leader 的降级/重派决策

