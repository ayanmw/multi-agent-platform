# task-cases Specification

本 capability 定义平台内置 Task Case 的领域契约：复杂度阶梯（L1–L5）、验收标准类型使用规约、回归覆盖要求。Case 是预配置的 `TaskContract`（SystemPrompt + DefaultInput + Permissions + AcceptanceCriteria），通过声明式数据驱动 Agent 行为，不引入额外执行路径。

## ADDED Requirements

### Requirement: Case 复杂度阶梯
平台 SHALL 按 L1–L5 五级复杂度阶梯组织内置 case，每一级只在前一级基础上新增一个维度的复杂度，便于回归定位与用户演示：
- L1 单 Agent 基线：基础 ReAct Loop / 纯对话 / 多步 shell
- L2 单 Agent + 子系统：todo / web_search / Skill / Cron / llm_judge
- L3 单 Agent + Harness 治理：PolicyGate 拦截 / 审批 / max_steps 失败 / context 压缩 / checkpoint resume
- L4 多 Agent 静态编排：parallel / sequential / DAG
- L5 多 Agent 动态编排：leader-driven dispatch / agent 互评 / 故障容忍

每个 case MUST 在 `Tags` 中标注其所属阶梯（如 `L1`/`L4`）与覆盖的能力维度（如 `tools:web_search`、`harness:policy`、`multi-agent:dispatch`）。

#### Scenario: 阶梯覆盖完整
- **WHEN** 调用 `cases.All()`
- **THEN** 返回的 case 集合 MUST 覆盖 L1、L2、L3、L4、L5 每一级至少一个 case
- **AND** 每个 case 的 Tags MUST 包含其阶梯标识

#### Scenario: 阶梯复杂度递增
- **WHEN** 比较 L4 与 L5 的多 Agent case
- **THEN** L4 case 的编排策略 MUST 是静态声明（parallel/sequential/dag），agent 集合在任务启动前确定
- **AND** L5 case 的编排 MUST 由 leader agent 运行时通过 `dispatch_sub_agent` 动态决定派发对象

### Requirement: 验收标准类型使用规约
Case 的 `AcceptanceCriteria` MUST 按任务性质选用对应验收类型，不得对所有 case 一律使用 `file_exists`：
- 代码生成类 case MUST 至少包含一个 `test_pass` 验收
- 产出报告类 case MUST 至少包含一个 `content_contains` 验收（校验结构化段落）
- 执行副作用类 case（如 git commit）MUST 至少包含一个 `shell_exit_zero` 验收
- 开放问答类 case MUST 使用 `llm_judge` 验收
- 治理类 case（拦截/失败）MAY 不要求 `status=completed`，转而断言被拦截或 `failed`

#### Scenario: 代码生成 case 验收闭环
- **WHEN** 运行 `code-gen` case
- **THEN** 其 `AcceptanceCriteria` MUST 包含 `test_pass` 类型条目，Target 为真实测试命令
- **AND** MUST 包含 `file_exists` 条目校验源码与测试文件均存在

#### Scenario: 报告 case 验收结构
- **WHEN** 运行 `research` case
- **THEN** 其 `AcceptanceCriteria` MUST 包含 `content_contains` 条目校验报告含约定段落（如 Executive Summary、References）

#### Scenario: 治理 case 允许非 completed 终态
- **WHEN** 运行 `policy-enforcement` 或 `max-steps-exhaustion` case
- **THEN** case 契约 MUST 不强制要求 `status=completed` 才算通过
- **AND** `policy-enforcement` 的预期是被 PolicyGate 拦截而非执行越界操作

### Requirement: 内置 Case 完整性
每个内置 case MUST 满足：ID 唯一（kebab-case）、Name 非空、Category 非空、SystemPrompt 非空、Contract.Goal 非空、Contract.MaxSteps > 0。`IsBuiltin=true` 的 case 不可被 PUT/DELETE。

#### Scenario: ID 唯一性
- **WHEN** 调用 `cases.All()`
- **THEN** 所有 case 的 ID MUST 两两不同

#### Scenario: 必填字段
- **WHEN** 遍历 `cases.All()` 的每个 case
- **THEN** Name、Category、SystemPrompt、Contract.Goal MUST 非空
- **AND** Contract.MaxSteps MUST 大于 0

### Requirement: Mock 回归覆盖
`scripts/cases-regression.sh` MUST 在 `LLM_USE_MOCK=true` 下覆盖全部内置 case，断言每个 case：执行不崩溃（达到 completed/failed 终态）、`total_tokens > 0`、`cost_records >= 1`。回归 MUST 按 L1–L5 分组输出。

#### Scenario: 全 case mock 回归
- **WHEN** 运行 `bash scripts/cases-regression.sh`
- **THEN** 脚本 MUST 对 `cases.All()` 返回的每个 case 至少执行一次
- **AND** 输出 MUST 按 L1–L5 分组展示通过率

#### Scenario: 失败 case 亦可回归
- **WHEN** mock 回归运行预期 `failed` 的 case（如 `max-steps-exhaustion`）
- **THEN** 脚本 MUST 将 `status=failed` 视为该 case 的 PASS 条件而非 FAIL

### Requirement: 多 Agent Case 可观测事件断言
对 L4/L5 多 Agent case，`scripts/cases-regression.sh` MUST 额外断言任务事件流中存在编排层事件：至少 1 条 `decompose_done`、至少 N 条 `agent_dispatched`（N = worker 数）、对应数量 `agent_completed`；并校验子 agent steps 经 `child_tasks[].steps` 回填。

#### Scenario: 编排事件存在
- **WHEN** mock 回归运行 `multi-agent-parallel` case（3 worker）
- **THEN** 任务事件流 MUST 含至少 1 条 `decompose_done`、3 条 `agent_dispatched`、3 条 `agent_completed`

#### Scenario: 子 agent steps 回填
- **WHEN** 查询 `GET /api/tasks?id=<rootTaskID>` 的 `child_tasks`
- **THEN** 每个 worker 的 steps MUST 非空且回填到对应 lane
