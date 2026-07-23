# extend-task-cases 实施任务清单

本任务清单遵循 OpenSpec spec-driven 流程，将 `proposal.md` / `design.md` / `specs/` 中定义的 L1–L5 case 矩阵与回归基建拆分为可追踪的具体任务。

## 1. 环境准备与基线确认

- [x] 1.1 确认当前工作目录为 `D:\Claude-Code-MultiAgent\.worktrees\extend-task-cases`，且 Go 环境可用
- [x] 1.2 运行 `go test ./internal/cases/...` 建立当前基线（预期失败或仅覆盖旧 5 个 case）
- [x] 1.3 阅读 `internal/cases/cases.go` 完整内容，确认 Case 结构体与现有 5 个 case 实现
- [x] 1.4 阅读 `internal/harness/harness.go`，确认 AcceptanceCriterionType 枚举与 TaskContract 字段
- [x] 1.5 阅读 `scripts/cases-regression.sh`，确认现有回归逻辑与 `tool-error` 残留行

## 2. L1 单 Agent 基线 case 验收加固

- [x] 2.1 改造 `code-gen` case：追加 `test_pass` 验收条目，确保生成代码可通过 `go test`
- [x] 2.2 改造 `code-gen` case：保留 `file_exists` 验收条目校验源码与测试文件均存在
- [x] 2.3 改造 `research` case：追加 `content_contains` 验收条目校验报告结构（含 Executive Summary / References 段落）
- [x] 2.4 改造 `research` case：更新 `DefaultInput` 使其触发调研型输出
- [x] 2.5 改造 `long-task` case：追加 `shell_exit_zero` 验收条目校验 git commit 等副作用真实发生
- [x] 2.6 确认 `dialogue` case 保持 L1 baseline 不变，验收维持现状
- [x] 2.7 运行 `go test ./internal/cases/...` 确认 L1 case 结构通过单测

## 3. L2 单 Agent + 子系统 case 新增

- [x] 3.1 新增 `todo-driven` case：覆盖 `todo/create`、`todo/list`、`todo/toggle` 工具链，使用 `content_contains` 或 `shell_exit_zero` 验收
- [x] 3.2 新增 `web-research` case：覆盖 `web_search` + fetch/parse 工具，产出结构化报告并用 `content_contains` 验收
- [x] 3.3 新增 `skill-code-helper` case：启用内置 `builtin-code-helper` Skill，验收代码生成文件存在且通过 `test_pass`
- [x] 3.4 新增 `cron-notify` case：通过 Agent Tool 创建并触发 Cron，验收 `cron_execution_completed` 事件或 session 消息
- [x] 3.5 新增 `llm-judge-qa` case：开放问答型 case，使用 `llm_judge` 验收判断回答质量
- [x] 3.6 为每个 L2 case 配置 Tags（`L2`、`tools:xxx`、`skill:xxx` 等）
- [x] 3.7 运行 `go test ./internal/cases/...` 确认 L2 case 结构通过单测

## 4. L3 Harness 治理 case 新增

- [x] 4.1 新增 `policy-enforcement` case：配置 PolicyGate 拦截越界操作，验收要求为 `status` 非 completed 或被拦截事件存在，而非执行成功
- [x] 4.2 新增 `approval-flow` case：构造需审批场景，验收审批通过/拒绝两条路径的行为
- [x] 4.3 新增 `max-steps-exhaustion` case：触发 `max_steps_exceeded` 失败路径，验收 `status=failed`
- [x] 4.4 新增 `context-compression` case：配置长上下文触发 compressor，要求任务仍到达 completed 终态
- [x] 4.5 新增 `checkpoint-resume` case：配合脚本侧 pause/resume API 验证续跑能力，验收最终完成
- [x] 4.6 为每个 L3 case 配置 Tags（`L3`、`harness:policy`、`harness:approval`、`harness:max_steps`、`harness:compressor`、`harness:checkpoint`）
- [x] 4.7 运行 `go test ./internal/cases/...` 确认 L3 case 结构通过单测

## 5. L4 多 Agent 静态编排 case 新增

- [x] 5.1 新增 `multi-agent-parallel` case：3 worker 并行执行，用 `orchestrator.RunBlocking` parallel 模式
- [x] 5.2 新增 `multi-agent-sequential` case：researcher → writer 顺序链式编排，前序结果经 AgentBus 转发为后序 observation
- [x] 5.3 新增 `multi-agent-dag` case：A→B→C 依赖链，使用 `orchestrator.RunBlockingDAG`
- [x] 5.4 保留旧 `multi-agent` case，将其 Description 标记为 legacy 伪扮演对照，确保回归脚本兼容
- [x] 5.5 为每个 L4 case 配置 Tags（`L4`、`multi-agent:parallel`、`multi-agent:sequential`、`multi-agent:dag`）
- [x] 5.6 运行 mock 回归并确认 L4 case 事件流包含 `decompose_done` + N 条 `agent_dispatched` + N 条 `agent_completed`

## 6. L5 多 Agent 动态编排 case 新增

- [x] 6.1 新增 `multi-agent-leader-dispatch` case：leader 运行时调用 `dispatch_sub_agent` 决定派发对象
- [x] 6.2 新增 `multi-agent-review` case：writer + reviewer 互评 + leader 裁决，验证 AgentBus 消息往返
- [x] 6.3 新增 `multi-agent-fault-tolerance` case：worker 返回失败结果，验证 leader 能降级处理且整体不崩溃
- [x] 6.4 在 case 描述中标注 `multi-agent-fault-tolerance` 的能力边界（若底层不支持真注入崩溃）
- [x] 6.5 为每个 L5 case 配置 Tags（`L5`、`multi-agent:dispatch`、`multi-agent:review`、`multi-agent:fault-tolerance`）
- [x] 6.6 若 L5 case 撞到 orchestrator 底层阶段 4/5/6 未完成项，记录为潜在后续 change，不阻塞本任务清单
- [x] 6.7 运行 mock 回归确认 L5 case 事件流满足 spec 要求（至少 1 decompose_done、≥1 agent_dispatched、≥1 agent_completed）

## 7. Case 单测与完整性校验

- [x] 7.1 在 `internal/cases/cases_test.go` 新增测试：所有 case ID 唯一
- [x] 7.2 新增测试：所有 case Name / Category / SystemPrompt / Contract.Goal 非空
- [x] 7.3 新增测试：所有 case Contract.MaxSteps > 0
- [x] 7.4 新增测试：所有 case Tags 包含阶梯标识（L1/L2/L3/L4/L5）
- [x] 7.5 新增测试：L1/L2/L3/L4/L5 每级至少存在一个 case
- [x] 7.6 新增测试：验收类型值属于 harness 预定义枚举
- [x] 7.7 运行 `go test ./internal/cases/...` 全绿

## 8. 回归脚本扩展

- [x] 8.1 清理 `scripts/cases-regression.sh` 中已不存在的 `tool-error` 残留行
- [x] 8.2 将脚本输出按 L1–L5 分组展示
- [x] 8.3 扩展脚本：对全部 `cases.All()` 返回的 case 执行一次 mock 回归
- [x] 8.4 扩展脚本：支持将预期 `failed` 的 case（如 `max-steps-exhaustion`）的 `status=failed` 视为 PASS
- [x] 8.5 扩展脚本：对 L4/L5 case 增加编排事件断言（decompose_done / agent_dispatched / agent_completed）
- [x] 8.6 扩展脚本：对 L4/L5 case 增加 `child_tasks[].steps` 回填断言
- [x] 8.7 运行 `bash scripts/cases-regression.sh` 全绿

## 9. Real-LLM 冒烟与边界标记

- [ ] 9.1 阅读 `scripts/real-llm-smoke.sh`，确认现有抽样逻辑
- [ ] 9.2 为 `multi-agent-leader-dispatch` / `multi-agent-fault-tolerance` 在脚本中标注 known-limitation（若底层未就绪则允许跳过或 fail）
- [ ] 9.3 在 real-llm-smoke 中增加 `code-gen`、`multi-agent-parallel`、`policy-enforcement` 代表性场景抽样
- [ ] 9.4 手动运行 real-llm-smoke 代表性场景一次，记录结果

## 10. 文档与归档

- [x] 10.1 更新 `CLAUDE.md` 中 Case 相关章节，补充 L1–L5 阶梯说明与新增 case 列表
- [x] 10.2 更新 `roadmaps/ROADMAP.md`，将 extend-task-cases 标记为已完成或进行中
- [x] 10.3 在 `openspec/changes/archive/` 下创建 `extend-task-cases.md` 归档本次变更
- [x] 10.4 运行 `openspec validate extend-task-cases` 确认 4/4 产物完成且有效
- [x] 10.5 提交 Git：`git add -A` → `git commit -m "Phase skill: extend-task-cases L1–L5 case 矩阵与回归基建"`
