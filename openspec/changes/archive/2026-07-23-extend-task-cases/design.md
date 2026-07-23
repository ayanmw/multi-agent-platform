## Context

平台 `internal/cases/cases.go` 现有 5 个内置 case（code-gen / research / multi-agent / dialogue / long-task），验收几乎清一色 `AcceptFileExists`，且 `multi-agent` 仍是 Phase 3 的"单 LLM 扮演三角色"占位。与此同时平台已实现：真·多 Agent 编排（`orchestrator.RunBlocking` parallel/sequential/pipeline + `RunBlockingDAG` + leader-driven `dispatch_sub_agent`）、Harness 治理（`PolicyGate` + `ApprovalRule` 审批链 + `compressor` + `checkpoint` + `Pause/Resume`）、以及 Skill / Cron / web_search / todo / diff / delete 等子系统。这些能力**零 case 覆盖**，既无演示也无回归。

回归基建现状：`scripts/cases-regression.sh` 已用 `LLM_USE_MOCK=true` 跑 6 个 mock case（含一个已不存在的 `tool-error`），断言 status / has_tool / tokens / cost_records，但**不断言验收标准是否被实际评估**，也**无多 Agent 事件断言**。`scripts/real-llm-smoke.sh` 跑真实 LLM 场景。

约束：
- 无 DB schema 变更（case 模型已持久化，内置 case 空库种子化）
- `web/v2/src/types/case.ts` 结构已完备，无需变更
- 白盒哲学：每个 case 必须能通过事件流被前端完整观测
- 子 agent 串行执行约束（memory `subagent-serial-execution`）：实现期派发 worker 用串行，不并行改文件

## Goals / Non-Goals

**Goals:**
- 建立 L1–L5 复杂度阶梯式 case 矩阵，覆盖平台全部已实现核心能力
- L1 改造：4 个现有 case 的验收从"文件存在"升级到"真验证行为"（test_pass / shell_exit_zero / content_contains）
- L2–L5 新增约 16 个 case，每个 case 明确标注覆盖的能力维度（tools / harness / multi-agent）
- `multi-agent` case 从"伪扮演"升级为真派发，并按 parallel/sequential/dag/leader 拆成 4 个独立 case
- 扩展 `scripts/cases-regression.sh`：按 L1–L5 分组、增加验收评估断言、增加多 Agent 事件断言（decompose_done / agent_dispatched / agent_completed）
- 首次建立 `openspec/specs/` capability 契约（`task-cases` + `multi-agent-orchestration`）

**Non-Goals:**
- 不改 orchestrator / engine / harness 的实现代码（若 L5 case 撞到 memory 记录的阶段 4/5/6 未完成项，另起 change 修底层，本变更只负责"加 case + 回归"）
- 不做 case 导入/导出、case 权限共享（沿用 2026-07-15 case-management-system plan 的 Non-Goals）
- 不改前端 case 模型结构；Category/Tag 分组 UI 调整属次要，可选
- 不引入新 DB 表、不改 API 路由
- 不为每个 case 写真实 LLM 冒烟（mock 回归为主，real-llm-smoke 仅覆盖代表性 case）

## Decisions

### D1: case 作为纯数据扩展，零代码逻辑
所有新 case 都是 `internal/cases/cases.go` 里的构造函数 + `All()` 注册，不引入新的执行路径。case 的行为完全由 `SystemPrompt` + `DefaultInput` + `Contract`（Permissions / AcceptanceCriteria / MaxSteps）声明式定义。
**Why**: 符合 case 系统设计哲学（case = 预配置 TaskContract），避免 case 逻辑散落到 engine/orchestrator。
**Alt**: 为多 Agent case 写专用 runner → 拒绝，那样会绕过 orchestrator 的标准链路，失去"真演示"价值。

### D2: 验收类型按 case 能力精确选用
| case 类型 | 验收类型 | 理由 |
|----|----|----|
| 代码生成 | `test_pass` + `file_exists` | 闭环 self-fix |
| 研究报告 | `content_contains` + `file_exists` | 验结构非仅存在 |
| git 任务 | `shell_exit_zero` | 验 commit 真发生 |
| 开放问答 | `llm_judge` | 演示 LLM 评估 |
| 治理/失败 case | 不要求 completed | `policy-enforcement` 验被拦截、`max-steps-exhaustion` 验 `failed` |
**Why**: 现有 case 全 `file_exists` 等于没验收，验收类型存在但从未被 case 使用。
**Alt**: 全部用 `llm_judge` → 拒绝，mock 回归下 LLM judge 不可用，且成本高。

### D3: `multi-agent` case 拆分为 4 个，旧 case 废弃
- `multi-agent`（伪扮演）→ 标记 `IsBuiltin` 保留但 Description 注明"legacy 模拟"，避免破坏现有 `cases-regression.sh` 的 `multi-agent` 行
- 新增 `multi-agent-parallel` / `multi-agent-sequential` / `multi-agent-dag` / `multi-agent-leader-dispatch`
**Why**: 旧 case 仍被回归脚本引用，直接删会破坏脚本；拆分让每种编排策略有独立可回归单元。
**Alt**: 直接改 `multi-agent` 为真派发 → 拒绝，会丢失"伪扮演"对照基线，且语义跳跃。

### D4: mock 回归与 real-llm 分层
- `cases-regression.sh`（mock）：覆盖全部 case 的**结构完整性 + 执行不崩**（status / tokens / cost / 事件存在性），不断言业务产物质量
- `real-llm-smoke.sh`（真实 LLM）：只覆盖代表性 case（code-gen 真生成、multi-agent-parallel 真派发、policy-enforcement 真拦截），断言产物
**Why**: mock 无法验证 LLM 真的写了代码，但能验证 case 配置不崩、事件流完整；真实 LLM 贵且慢，只抽样。
**Alt**: 全部真实 LLM 回归 → 拒绝，16 个 case 跑一遍成本和时间不可接受。

### D5: 多 Agent case 的事件断言
回归脚本对 L4/L5 case 额外断言：task 的 steps/events 中存在 `decompose_done`、至少 N 条 `agent_dispatched`、对应数量 `agent_completed`；子 agent steps 经 `child_tasks[].steps` 回填到 worker lane（memory 记录阶段 3 已落地）。
**Why**: 白盒哲学要求编排层可观测；这正是 `multi-agent-dual-entry-placeholder-bug` 里"root task 空壳"问题的回归防线。
**Alt**: 只断言 final status → 拒绝，无法发现"子 agent 挂在假 taskID 下"这类回归。

### D6: capability 契约新建两个 spec
`task-cases`（case 领域：阶梯、验收规约、回归要求）+ `multi-agent-orchestration`（编排策略 + 可观测事件契约）。`openspec/specs/` 此前为空，本次首次建立。
**Why**: proposal 已声明这两个 capability；spec 是 validate 通过的硬性要求，也是未来 case/orchestrator 变更的契约锚点。
**Alt**: 只建 `task-cases` → 拒绝，多 Agent 编排契约独立且更易被底层变更引用。

## Risks / Trade-offs

- **[L5 case 撞到 orchestrator 阶段 4/5/6 未完成]** → Mitigation: `multi-agent-leader-dispatch` / `multi-agent-fault-tolerance` 在 tasks.md 标注为"依赖底层就绪，可能拆出后续 change"；mock 回归只验事件存在性不验派发正确性；real-llm-smoke 里这两个 case 标 `known-limitation` 跳过或允许 fail。
- **[mock 回归无法验证产物质量]** → Mitigation: D4 分层，真实质量留给 real-llm-smoke 抽样；mock 层只保证"不崩 + 事件完整"。
- **[case 数量从 5 涨到 ~21，前端卡片膨胀]** → Mitigation: 用 Category（generation/research/interaction/collaboration/governance/automation）+ Tags 分组；前端如有压力后续单独优化 UI（Non-Goal 本轮不做）。
- **[旧 `multi-agent` 保留导致语义重复]** → Trade-off: 为回归脚本兼容性容忍，Description 明确标注 legacy。
- **[`tool-error` case 已不存在于 cases.go 但脚本仍引用]** → Mitigation: 本变更顺带清理脚本该行，或补一个 `tool-error` case（倾向清理，因 Non-Goal 不扩 mock-only case）。
- **[内置 case 种子化时机]** → Risk: 已有 DB 不会重新种子，老用户拿不到新 case。Mitigation: 文档说明"删除 data/*.db 或手动 INSERT 触发种子化"；不在本变更引入迁移（Non-Goal）。

## Migration Plan

1. 在 `feat/extend-task-cases` worktree 内完成全部代码 + 回归脚本
2. `go test ./internal/cases/...` + `bash scripts/cases-regression.sh`（mock）全绿
3. 手动跑 `scripts/real-llm-smoke.sh` 代表性场景
4. PR 合入 main（项目 Git 铁律：Phase 完成提交）
5. 已有 DB 的用户需删除 `data/*.db` 重新种子化以获取新内置 case（文档说明，无自动迁移）
6. OpenSpec 变更归档到 `openspec/changes/archive/`

**Rollback**: case 是纯数据，回滚只需 revert `internal/cases/cases.go` + 脚本；无 schema/API 影响。

## Open Questions

- Q1: 旧 `multi-agent` case 是否最终删除，还是永久保留为 legacy 对照？→ 倾向保留至下一周期再删，本变更只标注。
- Q2: `multi-agent-fault-tolerance` 的"worker 失败"如何在不改 engine 的前提下构造？→ 需在 design 实现期确认是否有 case 级 hook 注入失败，若无则降级为"验 leader 能处理 worker 返回 error 结果"而非"真注入崩溃"。可能触发 specs 调整。
- Q3: L3 `checkpoint-resume` 在 mock 下如何验证 Pause/Resume？→ 可能需要在回归脚本里主动调 pause/resume API，而非靠 case 自身。tasks.md 标注为脚本侧工作。
