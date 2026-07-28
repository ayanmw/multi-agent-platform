# tasks: fix-agent-model-bright-karp

- [ ] Task 1: 后端 runner 解析 `agent.Model` 并生效
  - 文件：`cmd/server/runner.go`
  - 要点：`AgentRunSpec` 增加 `Model`，使用 `resolveEffectiveModel`，`EngineConfig.Model` 与 provider 创建使用实际模型。

- [ ] Task 2: 后端 orchestrator 子 agent 查 DB model
  - 文件：`internal/orchestrator/orchestrator.go`
  - 要点：`runAgent` 中 `spec.Model == ""` 时查 `db.QueryAgentByID`，读取 model 与路由偏好。

- [ ] Task 3: 后端测试
  - 文件：`cmd/server/runner_model_test.go`（新建）、`internal/orchestrator/orchestrator_test.go`
  - 要点：验证优先级 `spec.Model > agent.Model > cfg.LLMModel`，`auto_route` 路径不受 `agent.Model` 影响。

- [ ] Task 4: 前端 OptionsFlyout 选择下发
  - 文件：`web/v2/src/components/OptionsFlyout.vue`、`web/v2/src/App.vue`
  - 要点：`v-model` 绑定，默认选中 default agent，`handleSend` 传 `agentId`。

- [ ] Task 5: 前端 AgentConfig dropdown 体验增强
  - 文件：`web/v2/src/components/AgentConfig.vue`
  - 要点：Enter 快速选中、保存校验、`preferred_model` 下拉。

- [ ] Task 6: 前端测试
  - 文件：`web/v2/src/components/OptionsFlyout.test.ts`
  - 要点：选择 agent 后 emit `update:modelValue`。

- [ ] Task 7: 集成验证
  - 后端：`go test ./cmd/server/... ./internal/runtime/... ./internal/orchestrator/...`
  - 前端：`cd web/v2 && npm run test:unit`（或 `npm run test`）
  - 手动：修改 default agent model，运行任务，确认实际请求模型变化。

- [ ] Task 8: OpenSpec verify + archive
