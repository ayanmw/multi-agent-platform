## Why

平台内置 case 体系停留在 Phase 3 单 Agent 演示水平（5 个 case，验收清一色 `file_exists`），而平台已实现的真·多 Agent 编排（RunBlocking / RunBlockingDAG / dispatch_sub_agent leader-driven）、Harness 治理（PolicyGate 拦截、审批链、context 压缩、checkpoint resume）、以及 Skill / Cron / web_search / todo 等子系统**全部没有任何 case 覆盖**。后果是：项目"白盒 + 多 Agent 协作 + 可观测"的核心卖点链路既无法向用户演示，也没有回归保护——`multi-agent-dual-entry-placeholder-bug` 记录的"leader-driven 链路长期死代码"就是同一症状的延伸：代码修通了，但没有 case 持续验证它通着。本次变更通过建立"复杂度阶梯式 case 矩阵"补齐这块，并首次把 `openspec/specs/` capability 契约建立起来。

## What Changes

- **L1 验收加固（改造现有 4 个 case）**：
  - `code-gen` 追加 `test_pass` 验收（真跑 `go test`），闭环 self-fix loop
  - `research` 追加 `content_contains` 验收报告结构（Executive Summary / References），并将 input 改为触发联网调研
  - `long-task` 追加 `shell_exit_zero` 验收 git commit 真发生
  - `dialogue` 保持 baseline 不变
- **L2 单 Agent + 子系统（新增 ~5 个 case）**：`todo-driven`（todo 工具）、`web-research`（web_search+fetch+jsonparse）、`skill-code-helper`（Skill 注入）、`cron-notify`（Cron Agent Tool + 事件回流）、`llm-judge-qa`（LLM Judge 验收）
- **L3 单 Agent + Harness 治理（新增 ~5 个 case，目前完全空白）**：`policy-enforcement`（PolicyGate 拦截越界）、`approval-flow`（审批通过/拒绝两路径）、`max-steps-exhaustion`（失败路径）、`context-compression`（compressor 触发仍完成）、`checkpoint-resume`（Pause/Resume 续跑）
- **L4 多 Agent 静态编排（新增 ~3 个 case，`multi-agent` case 升级）**：`multi-agent-parallel`（3 worker 并行）、`multi-agent-sequential`（researcher→writer 链式 AgentBus 转发）、`multi-agent-dag`（A→B→C 依赖 RunBlockingDAG）；旧 `multi-agent` "伪扮演" case 标记废弃或替换
- **L5 多 Agent 动态编排（新增 ~3 个 case，平台最复杂链路）**：`multi-agent-leader-dispatch`（leader 运行时 dispatch_sub_agent 决定派谁）、`multi-agent-review`（writer+reviewer 互评+leader 裁决，验消息往返）、`multi-agent-fault-tolerance`（worker 失败，验 leader 重派/降级）
- **回归基建**：扩展 `scripts/cases-regression.sh`，按 L1–L5 分组回归；为多 Agent case 增加断言（子 agent step 回填、`decompose_done`/`agent_dispatched`/`agent_completed` 事件存在性）
- **前端类型同步**：`web/v2/src/types/case.ts` 无需结构变更（模型已支持），仅在 UI 侧补 case 的 category/tag 分组展示（如有必要）
- **文档**：更新 `CLAUDE.md` Case 相关章节、`roadmaps/ROADMAP.md`

## Capabilities

### New Capabilities
- `task-cases`: 内置 case 领域模型、复杂度阶梯（L1–L5）清单、验收标准类型使用规约、回归覆盖要求
- `multi-agent-orchestration`: 多 Agent 编排能力（parallel / sequential / pipeline / DAG / leader-driven dispatch）的 case 覆盖契约与可观测事件要求

### Modified Capabilities
（无——`openspec/specs/` 此前为空，本次首次建立 capability 契约，不存在既有 spec 的 requirement 变更）

## Impact

- **代码**：`internal/cases/cases.go`（新增 ~16 个 case 构造函数 + All() 注册）、`internal/cases/cases_test.go`（新增 case 完整性单测：ID 唯一、Category/Tags 非空、验收类型合法）、`scripts/cases-regression.sh`（分组回归 + 多 Agent 事件断言）
- **依赖链路**：L3/L5 case 依赖 Harness 治理与 orchestrator 已实现能力；若 `multi-agent-leader-dispatch` 跑通时撞到 memory 记录的"阶段 4（编排层 step 事件）/阶段 5（DAG）/阶段 6（worker 跨 session 隔离）"未完成项，case 会逼出这些阶段的完成（属预期连带工作，不在本变更核心范围但可能触发后续 change）
- **API/DB**：无 schema 变更（case 模型已持久化，新增内置 case 在空库种子化时自动插入）
- **前端**：`web/v2/src/types/case.ts` 结构不变；Category/Tag 分组如有 UI 调整属次要
- **文档**：`CLAUDE.md`、`roadmaps/ROADMAP.md`、本变更归档到 `openspec/changes/archive/`
- **回归**：现有冒烟流程（`scripts/smoke-test.sh`、`real-llm-smoke.sh`、`cases-regression.sh`）扩展
