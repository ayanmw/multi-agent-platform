# proposal: agent.Model 未生效与前端选择未下发

## 问题陈述

1. 后端在 `single_model` 模式下未读取 DB 中的 `agents.model`，实际请求一直使用 `.env` 的 `LLM_MODEL`。
2. 前端 `OptionsFlyout.vue` 选择了 agent 后没有把选择结果传回 `App.vue`，导致运行请求始终走 `agent_default`。
3. AgentConfig 中模型选择体验需要增强：filter 回车快速选中、空值校验、`preferred_model` 也改为下拉。

## 预期结果

- `single_model` 模式下，若 `agent.Model` 非空，实际 LLM 请求使用该模型；否则回退 `cfg.LLMModel`。
- 多 agent orchestrator 的子 agent 同样从 DB 读取 `agent.Model`。
- 前端 Options 面板选择的 agent 会随任务请求下发，并在 AgentConfig 提供更好的下拉选择体验。

## 范围

- 后端：`cmd/server/runner.go`、`internal/orchestrator/orchestrator.go`、`internal/runtime/engine.go`（确认无需改动）。
- 前端：`web/v2/src/components/OptionsFlyout.vue`、`web/v2/src/App.vue`、`web/v2/src/components/AgentConfig.vue`。
- 测试：后端新增/更新 runner 与 orchestrator 测试；前端 OptionsFlyout 测试。

## 验收标准

- 修改 default agent 的 model 后，运行任务时 context window / 日志显示实际模型已切换。
- `agent.Model` 为空时回退到 `.env` 的 `LLM_MODEL`。
- `go test ./...` 全绿（除已知的 Gemini 配额测试外）。
- 前端单元测试通过。
- 遵循 CLAUDE.md 的 OpenSpec + superpowers 流程。
