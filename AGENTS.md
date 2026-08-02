# AGENTS.md

本文件为 CodeBuddy Code 在本仓库中工作时提供指引。

## 项目简介

一个从零构建的**白盒多 Agent 协作平台** —— 后端 Go + 前端 Vue 3 / Vite / TypeScript。每一个 LLM token、每一次工具调用、每一次 step 状态流转都会产生事件并实时推送到前端。设计哲学是"不做黑盒 Agent"：代码即文档，一切皆可观测。

- 模块路径：`github.com/anmingwei/multi-agent-platform`（Go 1.25）
- 数据库：`modernc.org/sqlite`（纯 Go，单文件，28 张表）
- LLM：OpenAI 兼容的 SSE 流式接口（`.env` 中的 `LLM_ENDPOINT`，默认 `deepseek-v4-flash`）
- 前端有两个版本，均内嵌进 Go 二进制：`web/v2`（可观测控制室，默认在 `/` 提供）和 `web`（v1，在 `/ui/v1/` 提供）

> 权威、详尽的设计文档是 `CLAUDE.md`（设计哲学、各子系统、事件清单）。`README.md` 包含快速开始与状态矩阵。本文件是高层次的导览，深度内容请以这两个文件为准。

## 常用命令

### 构建与运行

```bash
# 构建前端（两者都会内嵌进 Go 二进制）
cd web && npm install && npm run build && cd ..     # v1
cd web/v2 && npm run build && cd ..                 # v2（默认提供）

# 构建服务端二进制
go build -o server.exe ./cmd/server/
./server.exe --port 8080
# / 提供 v2；/ui/v1/ 提供旧版 v1。无需环境变量 —— 按路径分发。

# 不产出二进制直接运行
go run ./cmd/server/
```

配置从 `.env` 加载（参见 `.env.example`）。优先级：**系统环境变量 > `.env` > 默认值**。`cp .env.example .env` 用于本地测试（通常默认值即可）。

### 测试

```bash
# Go 单元/集成测试（整个模块）
go test ./...
# 单个包
go test ./internal/workspace/...
# 单个测试函数
go test ./internal/workspace/ -run TestManager -v

# 静态检查（仓库内无 lint 配置；若已安装则使用 golangci-lint 默认规则）
go vet ./...
golangci-lint run ./...

# 端到端 / 回归（确定性，无需真实 LLM）
bash scripts/smoke-test.sh                 # 后端冒烟测试
bash scripts/cases-regression.sh           # 21 个内置 case 的 mock 回归（目标 21/21）
bash scripts/multi-agent-smoke.sh          # 多 Agent 编排（mock）

# 真实 LLM 冒烟（需配置 .env 中的 API key）
bash scripts/real-llm-smoke.sh
#   SKIP_PARTB=1  → 只跑 Part A（6 个场景），更快更省
#   SMOKE_FRESH=1 → 启动前清空隔离产物目录
```

**Windows 回归注意点：** mock case 回归会通过 Python 从 stdin 读取 JSON。必须 `export PYTHONUTF8=1`，否则 `/api/tasks` 响应中的中文字段（如 `skill/list` 返回的 Skill DisplayName）会被按 GBK 解码，`/api/tasks` 的 JSON 解析失败，导致轮询 status 恒为空、误判超时。

Mock 测试通过 `LLM_USE_MOCK=true` 配合 `internal/llm/mock_builtin.go` 中的 22 个内置脚本来驱动引擎（每个 case 一个 + 一个 `tool-error` 兜底）。mock 脚本通过精确 `CaseID` 匹配被选中（见 `internal/llm/mock_provider.go` 的 `selectScript`）。

### 前端开发

```bash
cd web && npm install && npm run dev        # v1 热重载
cd web/v2 && npm run dev                     # v2 热重载
```

### 手动 HTTP 验证

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/metrics
curl -X POST http://localhost:8080/api/tasks -H "Content-Type: application/json" \
  -d '{"action":"chat","input":"1+1=?"}'
# 实时事件：wscat -c 'ws://localhost:8080/ws?session_id=<session_id>'
```

## 高层架构

### 请求流程

```
用户输入
  → POST /api/tasks  (action: chat|multi_agent|...)  或  POST /api/sessions/:id/chat
  → cmd/server 路由（cmd/server/*.go）  → AgentRunner.Run(ctx, AgentRunSpec{...})
  → ReAct 引擎（internal/runtime/engine.go）
       Step 0: think  → LLM ChatStream → SSE → llm_delta 事件
       Step 1: tool_call → PolicyGate（审批 / 预算 / 白名单，internal/harness）
       Step 2: observe → 循环
  → 超过 max_steps → task_failed（max_steps_exceeded）
  → 最终答案 → task_completed
  → WebSocket Hub（internal/ws）向所有订阅客户端广播事件
```

### Agent 启动入口（修改 agent 调用逻辑前必读）

Phase 8-A 把分散的 `runAgentLoop*` 入口收敛为单一漏斗：

- `AgentRunner.Run(ctx, AgentRunSpec{...})` 是启动一次 agent run 的**唯一**方式。依赖通过 `AgentDeps`（或 `appServer.deps()`）注入；所有运行期可变状态都放进 `AgentRunSpec`。**不要**重新引入长参数列表。
- `AgentRunner.Recover(...)` 负责 checkpoint / 崩溃恢复。
- 每次 run 会创建一个 `workspace.WorkdirHolder`（初值 = session 的 `WorkspaceDir`）。如果处于 worktree 中，holder 的值会切换到 worktree 路径 —— 该 holder 是工具 CWD 的**唯一事实来源**。

`cmd/server` 拆分成多个文件：`main.go`（子系统初始化 + worktree 孤儿扫描）、`server.go`（`appServer` 聚合体 + `registerRoutes`）、`runner.go`（`AgentRunSpec`/`AgentDeps`/`AgentRunner` 漏斗）、`api.go`（handler，全部是 `*appServer` 的方法），以及功能文件 `tasks_api.go`、`checkpoint_api.go`、`tool_api.go`、`cron_api.go`、`workspace_api.go`、`mcp_api.go`、`api_todo.go`、`model_api.go`、`persistence.go`、`shutdown.go`。

### 工具系统（新增/修改工具前必读）

- 工具接口（自 Phase 8-A 起）：`Name() / Description() / Parameters() / Execute(input)` **外加** `Version() / Source() / CanonicalName()`。
- 注册表键为 `namespace/name@version`；`IsBuiltin` 由 `Source` 决定。通过 `internal/tool/registry.go` 获取注册表。
- `Registry.ExecuteWithCtx(ctx, input)` 注入 workdir：当存在非空 `WorkdirHolder` 时，其值**覆盖** LLM 传入的 `input["workdir"]`（经由 `ExecuteContext.Workdir`）。LLM 无法伪造 workdir 逃逸出 worktree。
- 内置工具：`run_shell`、`write_file`、`read_file`（`internal/tool/builtin.go`）。`worktree/create|exit|status` 是 worktree 的入口（`internal/tool/worktree.go`）。
- 动态工具持久化到 DB（`tools` 表，migration v27）并在启动时加载；`DynamicTool` 委托给 `DynamicExecutor` 执行。
- 工具元数据在 `ToolDescriptor` 中，`ToolExecutor` / `ToolLoader`（`DBToolLoader`、`RecordLoader`）为抽象层。

### 各子系统（每个都与 skill/tool/cron/memory 平级）

- `internal/runtime` —— ReAct Loop 引擎 + Step 状态机 + 持久化。
- `internal/llm` —— Provider 抽象（OpenAI/Anthropic/DeepSeek + `MockProvider`）。流式客户端在 `client.go`。
- `internal/orchestrator` —— 多 Agent 编排：静态（`parallel`/`sequential`/`DAG`）与动态 leader 驱动 `dispatch_sub_agent`。发出 `decompose_done` / `agent_dispatched` / `agent_completed` 作为**仅 WS** 事件（不写入 task steps —— 需经 WS 或 `/api/replay/events` 捕获）。
- `internal/harness` —— PolicyChain / TaskContract / ApprovalRule / `FileScopeRule`（作用域跟随 worktree holder）。
- `internal/skill` —— 可复用 prompt 包（Registry / Store / Renderer / `{{ var }}` 模板）。注入到引擎 system prompt。REST `/api/skills*`，工具 `skill/*`。
- `internal/todo` —— session 级 TODO，6 个 Agent 工具，`/api/todos`。
- `internal/cron` —— 调度器（`robfig/cron`，秒级），4 种 `action_type`（`start_task`/`script`/`webhook`/`notify_session`），REST `/api/crons*`，工具 `cron/*`。`CRON_ENABLED=false` 关闭自动触发但保留 CRUD。
- `internal/memory` —— 作用域召回（session/project/global）、向量存储、上下文压缩。
- `internal/workspace` —— session 级 git **worktree** 隔离（`Manager` git 原语 + `WorkdirHolder`）。`WORKTREE_ENABLED=false` → Manager 为 nil，能力关闭，行为与旧路径一致。REST **只**暴露 create/get（exit 因脏状态风险，仅由 LLM 经 Agent Tool 驱动）。
- `internal/auth` —— API key + bcrypt，可配置 `REQUIRE_AUTH`，RBAC。
- `internal/cost` —— `CostTracker` + `CostBudgetRule`。`internal/observability` —— 结构化日志、Prometheus `/metrics`、`/healthz`、tracer。
- `internal/pool` —— Worker Pool 并发。`internal/agent` —— Agent 类型定义。`internal/cases` —— 21 个内置 L1–L5 case（`cases.All()`）。`internal/config` —— `.env` + 配置（含 `MCP_SERVERS`、`WORKTREE_ENABLED`、`CRON_ENABLED`）。
- `internal/tool/mcp` —— MCP client/manager/repository。外部 MCP server 暴露的工具命名为 `mcp__<server>__<tool>`；它们作为普通工具注册。支持静态（`MCP_SERVERS`）、动态 API、内置市场、SSE 远程、远程 marketplace 五种方式。

### 数据与事件

- `pkg/db` —— SQLite schema + 迁移（当前 v28）+ CRUD。`modernc.org/sqlite` **默认不开启外键**，因此删除需手动级联（见 `cron_api.go` 删除 executions）。
- `pkg/event` —— 统一的 `AgentEvent` 结构 + 序列化。**所有**后端→前端的消息都是携带 `{task_id, agent_id, step_index, timestamp, data}` 的 `AgentEvent` JSON。所有状态变更都经由 EventBus 广播，绝不直接修改前端状态。

### 前端

- `web/v2`（默认，控制室：三栏布局）—— `web/v2/src`，composables（`useTaskStore`、`useWebSocket`、`useCrons`、`useCronEvents`），`types/events.ts` 枚举所有 EventType。
- `web`（v1）—— `useTaskStore`、`useWebSocket`、`AgentTree`。
- 两者各自独立构建，并通过 `go:embed`（`web/embed.go` 的 `UIVersionsRegistry`）内嵌。

## 此处重要的约定

- **注释是硬性规则（"白盒"哲学）。** 每个导出的类型/函数/接口都必须有文档注释，说明职责与关系；关键流程（ReAct Loop、SSE 解析、事件路由、工具执行）需有行内注释；未完成的工作用 `// TODO: Phase X — 描述`。接口注释优先写"为什么"而非"是什么"。
- **Token 统计**只使用 API 返回的 `usage` 字段 —— 绝不做本地估算。
- **Git：** 每个 Phase 完成后必须提交；提交信息格式 `Phase X: 简要描述`；同步更新 `roadmaps/ROADMAP.md`。
- **方法论：** OpenSpec（变更制品生命周期：proposal→design→specs→tasks→apply→verify→archive）和/或 superpowers（TDD/debug/review 纪律）—— 按任务选择并走完所选流程；不要遗留未归档的 OpenSpec change。
- **确定性测试：** 在回归/CI 类运行中优先使用 `LLM_USE_MOCK=true` + 内置 mock 脚本，而非真实 LLM 调用。
- 工具 workdir 安全：永远不要信任 LLM 传入的 `input["workdir"]` —— 一律经由 `ExecuteContext.Workdir` / `WorkdirHolder`。

## 进一步查阅

- `CLAUDE.md` —— 完整设计哲学、子系统内部机制、事件清单、API 配置。
- `README.md` —— 状态矩阵、MCP 接入（5 种集成模式 + REST API 表）、curl 示例。
- `docs/` —— `API_CHANGELOG.md`、`History.md`、`PHASE7_PLAN.md`、测试报告。
- `roadmaps/ROADMAP.md` —— 路线图 + 版本历史。
- `openspec/changes/` —— 当前/已归档的变更制品。
- `internal/*` 与 `cmd/server/*` —— 代码本身；以文档注释为准，而非本摘要。
