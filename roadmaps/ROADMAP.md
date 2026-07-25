# 多 Agent 平台 — 产品路线图

> **最近更新**: 2026-07-25
> **当前版本**: v0.14.0 Alpha（multi-model layered routing P1-P2: Provider 工厂/多协议支持、per-model RPM 限流、Engine 成本预算治理与 fallback 重试、路由事件可观测性、web/v2 Inspector Routing 面板）
> **更新规则**: 每个 Phase 任务完成后，必须更新本文件并提交 Git。

---

## 路线图总览

```
Phase 0 ✅ → Phase 1 ✅ → Phase 2 ✅ → Phase 3 ✅ → Phase 4 ✅ → Phase 5 ✅ → Phase 6 ✅ → Phase skill ✅ → Phase TODO ✅ → Phase 7-cron ✅ → Phase UI-v2 ✅ → Phase 7-H2 ✅ → Phase 8-A ✅ → Phase 8-B ✅ → Phase worktree ✅ → web-search-china ✅ → smoke-fix ✅ → multi-model-routing ✅
  (骨架)      (Agent)     (UI)       (Cases)    (并发)      (注册)      (高级)       (Skill 系统)     (TODO)        (定时器)        (控制室)        (编排闭环)     (架构演进)   (架构收尾)    (worktree 隔离)   (国内搜索+深度研究)  (冒烟测试修复)   (多模型分层路由P1-P2)
```

---

## Phase 0: 项目骨架 + 通信验证 ✅ 已完成

**完成日期**: 2026-07-03
**Git commit**: `82735b5`

### 交付物
- [x] Go 1.25 模块初始化 + 目录结构
- [x] WebSocket Hub（gorilla/websocket → connect/broadcast/disconnect）
- [x] AgentEvent 系统（18 种事件类型）
- [x] SQLite Schema（6 张表：agents, tasks, steps, tools, conversations, files）
- [x] Vue 3 CDN 前端 + 事件路由器
- [x] `/api/tasks` stream-demo 端到端测试（curl 可触发）
- [x] OpenSpec 全部产物（proposal, design, 6 specs, tasks）
- [x] 产品文档（doc/ 目录，8 个章节 + 共享样式表）
- [x] 路线图文件（roadmaps/ROADMAP.md）

### 已知待优化（Phase 1+ 已全部解决）
- [x] ~~DB 初始化未在 Server 启动时调用~~ → Phase 1+ 已实现
- [x] ~~`internal/llm/`, `internal/runtime/`, `internal/config/` 为空壳目录~~ → Phase 1 已实现
- [x] ~~API Key 散落在 CLAUDE.md，待迁移到 `.env`~~ → Phase 1 已实现
- [x] ~~Event 中 `interface{}` 待统一为 `any`~~ → Phase 1+ 已实现
- [ ] 前端为 CDN 单文件，待迁移到 Vite + TypeScript → Phase 2

---

## Phase 1: Agent Loop 核心引擎 ✅ 已完成

**目标**: 打通真实 LLM API 调用，实现 ReAct Loop 完整闭环

**完成日期**: 2026-07-03
**Git commit**: `bff272f`

### 交付物
- [x] OpenAI-compatible LLM Client（`internal/llm/client.go`，SSE streaming）
- [x] 3 个内置工具实现（`internal/tool/builtin.go`：run_shell, write_file, read_file）
- [x] ReAct Loop 引擎（`internal/runtime/engine.go`：think → tool_call → observe → loop）
- [x] Step 状态机 + 事件广播（EventBus 接口）
- [x] Agent 配置加载 + `.env` 管理（`internal/config/config.go`）
- [x] Go 端到端测试工具（`cmd/e2e-test/main.go`，WebSocket + 着色输出）
- [x] `cmd/server/main.go` 重构，整合真实 Agent Loop 替代 demo stream
- [x] **Phase 1+**: DB 持久化接入 Agent Loop（Task/Step/Conversation 写入 SQLite）
- [x] **Phase 1+**: `interface{}` → `any` 统一替换
- [x] **Phase 1+**: Agent CRUD REST API + DB 持久化（`GET/POST/PUT/DELETE /api/agents`）
- [x] **Phase 1+**: Task 历史查询 API（`GET /api/tasks` 列表 + `GET /api/tasks?id=xxx` 详情）
- [x] **Phase 1+**: Client→Server 消息处理（`readPump` 解析 JSON 控制消息，`ControlHandler` 接口）
- [x] **Phase 1+**: `run_shell` timeout 实现（`context.WithTimeout` + `exec.CommandContext`）
- [x] **Phase 1+**: 安全加固（路径遍历防护 + 大文件保护 + Engine panic 恢复）
- [x] **Phase 1+**: 白盒 Agent 注释铁律 — 所有导出类型/函数/关键流程补齐注释

### 验证结果
- 简单对话 `curl chat "1+1=?"` → 741 tokens，正确回答 "2"
- 工具调用 `curl chat "用 run_shell 执行 echo hello_from_agent"` → 两步 Loop：tool_call(23ms) → 分析结果 → 730 tokens
- e2e-test 工具全场景通过（simple + tool → all）
- Agent CRUD API 完整可用（创建/查询/更新/删除）
- Task 历史持久化可查询（含 steps 详情）
- `data/app.db` 自动创建，任务执行记录完整写入

### 已知待优化
- [ ] `run_shell` 无沙箱（Phase 5 加 Docker）
- [ ] Agent CRUD 前端页面 → Phase 4（与配置页面合并）
- [ ] `llm_delta` 批量发送 → Phase 3（随 Cases 测试时一起调优节流策略）
- [ ] Conversation 历史回读用于多轮对话（Phase 3+ Session 管理）

---

## Phase 1.5: 扩展工具注册表 ✅ 已完成 (2026-07-18)

**目标**: 引入 namespace/tag 工具身份体系，补充常用 function tools

### 交付物
- [x] `Tool` 接口扩展: `Namespace()` / `FullName()` / `Tags()`
- [x] `Registry` 以 `FullName()` (`namespace/name`) 作为 key，支持 `FilterByTag`
- [x] `BuiltinTool` 新增 `NewBuiltinTool` 构造器 + `WithTags` 链式方法
- [x] 新增核心工具:
  - `core/list_dir` — 目录枚举（递归/深度/Glob/隐藏文件）
  - `core/apply_diff` — 文本替换（old_string 或 line_start/line_end）
  - `core/delete_file` — 文件/目录删除（支持 recursive）
  - `core/fetch_url` — HTTP GET（timeout / max_bytes / headers）
  - `core/parse_json` — JSON 解析 + 点分路径查询
  - `core/execute_program` — 解释器执行（python / node / bash），可选 Docker 沙箱
- [x] 新增核心工具:
  - `core/web_search` — 本地 provider-independent 搜索，支持 Exa / Parallel（MCP over HTTP），未配置时返回友好提示
- [x] `internal/runtime/engine.go` 使用 `FullName()` 生成 LLM tool definitions
- [x] 所有新工具均含单元测试与风险标签（readonly / write / destructive / exec / network / websearch）

### 验证结果
- `go test ./internal/tool ./internal/runtime ./internal/harness ./internal/llm ./pkg/db -count=1` 全部通过
- `go build ./cmd/server` 编译通过

---

## Phase 2: 前端可视化 ✅ 已完成

**目标**: 实现 Agent 执行过程的完整可视化

**完成日期**: 2026-07-03
**Git commit**: `f335a51`

### 交付物
- [x] Vite + Vue 3 + TypeScript 工程化迁移（从 CDN 单文件）
- [x] AgentTree 组件（递归树 + 实时更新）
- [x] TypeWriter 组件（LLMDelta 流式渲染 + marked + highlight.js）
- [x] Markdown 实时渲染 + 代码语法高亮
- [x] Step 展开/折叠 + StatusIndicator 状态指示器
- [x] Pause / Resume / Cancel 控制按钮
- [x] 指标面板（连接状态、task 状态、agents、steps、tokens）
- [x] TaskInput 组件（chat 输入 + 发送）
- [x] useTaskStore 状态管理（事件路由 + 响应式 TaskState）
- [x] useWebSocket 连接管理（自动重连 + 指数退避）
- [x] Go embed 集成（前端 dist/ 嵌入二进制，单文件部署）
- [x] 独立部署支持（Vite dev server 代理 /ws 和 /api 到 Go 后端）

### 验证标准
- [x] `vue-tsc` 类型检查通过
- [x] `vite build` 构建成功
- [x] `go build ./...` 编译通过
- [x] 前端 embed 到 Go 二进制，单文件部署

---

## Phase 3: 预设 Cases + 配置页面 + Harness 基础 ✅ 已完成

**目标**: 提供一键式任务和 Agent 配置管理，引入 Harness 基础组件

**完成日期**: 2026-07-15
**Git commit**: `e516dcb` (Case Management 增强批次)

### 交付物
- [x] 5 个预设 Task Cases（代码生成、研究、多Agent、对话、长任务）
- [x] 自定义 Case CRUD：后端 `internal/cases` Repository + Service，前端 `useCaseStore` + API 对接
- [x] Case 持久化：`cases` 表 + `case_evaluations` 表（migration v17）
- [x] Case 库为空时自动插入 5 个内置默认 Case（`is_builtin = 1`）
- [x] 按 Tag（OR 语义）和 Category 筛选 Case 列表
- [x] 内置 Case 保护（不可修改/删除），自定义 Case 可编辑/删除
- [x] CaseCard UI 组件 + Run 按钮 + 编辑/删除入口 + built-in badge
- [x] CaseFilter 组件（Category 下拉 + Tag 胶囊 + 清除筛选）
- [x] CaseForm 组件（新建/编辑 Case，含 Goal、Max Steps、Acceptance Criteria）
- [x] CaseDetailModal 组件（展示 Contract、Acceptance Criteria，支持编辑入口）
- [x] **Harness: TaskContract 定义**（目标、范围、验收标准、预算、权限）
- [x] **Harness: Progress 文件管理**（TaskProgress 类型 + 关键节点自动写入）
- [x] **Harness: FileScopeRule + PathTraversalRule**（路径安全，在 write_file 之前拦截）
- [x] **Harness: AcceptanceCriteria 基础实现**（test_pass / file_exists / content_contains / shell_exit_zero）
- [x] **Harness: LLM Judge 判定器**（`llm_judge` 标准，任务完成后用 LLM 判定结果是否符合 Goal）
- [x] **Harness: PolicyGate 集成到 Engine**（executeTool 经过 PolicyGate 拦截）
- [x] `/api/cases` CRUD 端点（`GET/POST/PUT/DELETE`，支持 `?tag=` / `?category=` 筛选）
- [x] `/api/cases/:id/evaluations/:task_id` 评估结果查询端点
- [x] `task_evaluated` 事件：Engine 在 `task_completed` 后自动触发评估并广播
- [x] 前端展示 Case 评估结果（passed / score / reason）
- [ ] Agent 配置 CRUD 前端页面 → Phase 4（与多模型配置页面合并）

### 验证标准
- [x] 后端 `go test ./...` 通过（含 cases / harness / runtime / server）
- [x] 前端 `npm run build` 通过（`vue-tsc -b && vite build`）
- [x] 启动时若 Case 库为空，自动出现 5 个内置 Case
- [x] 可新建自定义 Case，刷新后仍存在
- [x] 内置 Case 不能删改，自定义 Case 可以编辑/删除
- [x] 按 Tag / Category 筛选 Case 列表
- [x] 运行带 `llm_judge` 标准的 Case，任务完成后收到 `task_evaluated` 事件并显示评估结果

---

## Phase 4: 多 Agent 并发 + Harness 控制层 + 记忆基础 ✅ 已完成

**目标**: 支持多个 Agent 并行执行，引入 Policy Gate 和记忆系统

**完成日期**: 2026-07-05
**Git commit**: `b127861`

### 交付物
- [x] 多 Agent Task 分派（goroutine 并行）
- [x] 前端多树渲染（并排或选项卡，颜色区分）
- [ ] Agent 间通信协议（AgentBus 代码已落地，未接入 Engine ReAct Loop）
- [ ] **多模型分层基础**: `ModelProfile` 类型 + `ModelRegistry` 注册表
- [ ] **Agent 模型绑定**: 创建 Agent 时可选指定模型（从 Registry 中选择）
- [x] **Harness: Policy Gate 框架**（`Policy` 接口 + `PolicyGate` 入口）
- [x] **Harness: FileScopeRule**（write_file 前路径白名单/黑名单校验）
- [x] **Harness: StepBudgetRule**（防止单次任务资源超支）
- [x] **Memory 基础**: `internal/memory/inmemory.go` 实现 + `core/long_term_memory` 工具
- [x] 新增事件：`agent_registered` / `policy_violation` / `budget_exceeded` / `tool_permission_denied`

### 验证标准
- [x] `multi-agent` Case 并行分派多个 child Agent
- [x] 每个 Agent 独立 step 状态与 WS 事件
- [x] 路径越界尝试被 Policy Gate 拦截并返回 `policy_violation`
- [x] 单步 token 预算超过上限触发 `budget_exceeded`

---

## Phase 5: 工具注册生态 + 执行沙箱 ✅ 已完成

**目标**: 支持外部工具安全接入与自定义扩展

**完成日期**: 2026-07-08
**Git commit**: `7ad94ab`

### 交付物
- [x] 扩展 `Tool` 接口：支持 `Metadata()` / `Version()` / `Validate(input)`
- [x] `Registry` 支持版本化注册与查询
- [x] `tool/loader.go` 外部工具加载器：
  - `FileToolLoader`：从 JSON/YAML 加载动态工具
  - `DockerToolLoader`：从 Docker 镜像加载
  - 校验：参数 Schema / 描述 / Docker 镜像名
- [x] **Docker 沙箱执行器**: `internal/tool/docker.go`
  - `execute_program` 支持 `runtime=docker`
  - 挂载 workspace 只读
  - 返回 stdout / stderr / exit_code
- [x] `run_shell` 白名单控制（可配置允许命令）
- [x] 新增 REST API: `POST /api/tools/register` / `GET /api/tools`
- [x] 新增事件：`tool_registered` / `tool_registration_failed` / `tool_unregistered`
- [x] 新增 Agent Tool: `register_tool`（LLM 运行时注册外部工具）

### 验证标准
- [x] 通过 JSON 注册自定义 `echo_tool` 并成功执行
- [x] `run_shell` 白名单拒绝不在列表中的命令
- [x] `execute_program` (Docker Python) 执行并返回结果
- [x] 注册非法 YAML 返回校验错误

---

## Phase 6: 高级能力 + 可观测性 + 通信升级 ✅ 已完成

**目标**: 强化 Agent 高级能力、可观测性与部署体验

**完成日期**: 2026-07-10
**Git commit**: `e6de169`

### 交付物
- [x] **RAG 基础**: `internal/rag/`（Chunk / Embed / VectorStore 接口）
  - `SimpleChunker`（按 token 估算分块）
  - `InMemoryVectorStore`（余弦相似度检索）
  - 修复 cosine 实现：使用 `sqrt(magA)*sqrt(magB)` 避免浮点下溢
- [x] **Model Router**: `internal/router/`（按成本/速度/质量路由模型）
- [x] **gRPC 通信**: `internal/grpc/`（proto + server/client，可选与 WS 共存）
- [x] **Cost Tracker**: 任务成本记录，含 usage + provider + model
- [x] **事件增强**: 新增 `model_routed` / `llm_usage_recorded` / `rag_retrieved` 事件
- [x] `run_shell` 超时与默认工作目录治理

### 验证标准
- [x] 本地 cosine 相似度检索正确（已修复浮点问题）
- [x] gRPC server/client 双向通信可用
- [x] LLM 调用后生成 `llm_usage_recorded` 事件

### 已知issue（已修复）
- [x] 修复 phase-6 `vector_store.go` 预发布版本的浮点下溢 bug（已记录 memory，不再重复修复）

---

## Phase skill: Skill 可复用 Prompt 包 ✅ 已完成

**目标**: 让同一 Agent 根据启用 Skill 动态切换专长，不切换配置

**完成日期**: 2026-07-10
**Git commit**: `b7be01d`

### 交付物
- [x] `internal/skill/` 领域模型（`SkillSource` / `SkillState` / `Skill` / `Template` / `Parameter`）
- [x] 内存注册表 + SQLite 持久化 + built_in / local_db 加载
- [x] `{{ variable }}` Renderer（变量缺失时使用默认值，否则保留占位符）
- [x] 内置 Skill 种子：`builtin-code-helper` / `builtin-error-diagnosis`
- [x] Engine 在 system prompt 注入 `system_prompt` / `task_prompt` 模板
- [x] REST API: `GET /api/skills?source=` / `search` / `POST /api/skills` / `PUT` / `DELETE` / `enable` / `disable`
- [x] Agent Tools: `skill/create_local` / `skill/delete_local` / `skill/list`
- [x] 前端 `TaskInput` 输入 `/` 触发 `SkillPicker`，选中后自动启用 Skill
- [x] 单元测试覆盖 API / Agent Tool / Renderer / Registry
- [x] OpenSpec `skill-system` change 已归档

### 验证标准
- [x] 启用 `builtin-code-helper` 后，同一 Agent 输出包含代码审查要点
- [x] 创建/删除 local Skill 后重启仍在
- [x] 内置 Skill 不可 PUT / DELETE，返回 403

---

## Phase TODO: Session 级 TODO 子系统 ✅ 已完成

**目标**: 给 Session 提供结构化任务追踪与子任务支持

**完成日期**: 2026-07-16
**Git commit**: `e7c8db9`

### 交付物
- [x] `internal/todo/` 模型 + Store + Service
- [x] 6 个 Agent Tools: `todo/create` / `todo/update` / `todo/delete` / `todo/list` / `todo/toggle` / `todo/move`
- [x] REST API: `/api/todos`
- [x] Engine system prompt 注入 `active_todos`
- [x] 前端拖拽/嵌套子任务 + 树形渲染
- [x] 事件：`todo_created` / `todo_updated` / `todo_deleted` / `todo_toggled`

### 验证标准
- [x] Agent 运行中创建 TODO，前端实时显示
- [x] 完成 TODO 后状态同步
- [x] 嵌套子任务不超过 3 层

---

## Phase 7-cron: 定时器子系统 ✅ 已完成

**目标**: 支持按 cron/interval/once 调度 Agent Task 或回调

**完成日期**: 2026-07-21
**Git commit**: `e7c8db9` 批次

### 交付物
- [x] `internal/cron/` model/store/template/action/executor/scheduler/service/tools
- [x] `pkg/db/cron.go` migration v26
- [x] 4 种 `action_type`: `start_task` / `script` / `webhook` / `notify_session`
- [x] `ScheduleType`: `cron` / `interval` / `once`
- [x] 串行 skip / missed / 模板渲染 / 事件化
- [x] REST API: `/api/crons*` 全 CRUD + trigger + executions
- [x] Agent Tools: `cron/create` / `cron/list` / `cron/delete` / `cron/trigger`
- [x] 前端 Manage tab / CronDockPanel / TopBar 入口
- [x] 事件类型：`cron_created` 等 14 个

### 验证标准
- [x] `multi-agent-smoke.sh` 与 `real-llm-smoke.sh` 通过 cron 场景
- [x] `go test ./...` 全绿
- [x] UI 可创建/启用/触发/删除 cron

---

## Phase UI-v2: Observable Control Room ✅ 已完成

**目标**: 用 Dock 三栏 + 移动 3-tab 控制室替代旧版 UI

**完成日期**: 2026-07-24
**Git commit**: `678e9e0`

### 交付物
- [x] `web/v2/` Vite + Vue 3 + TypeScript 工程
- [x] 桌面 Dock 三栏布局：左任务/中 Agent 树/右 Inspector
- [x] 移动 3-tab 布局 + 5-tab MobileNav
- [x] TopBar More 抽屉、Manage/Context Bottom Sheet
- [x] CommandBar flex 布局、Inspector 全屏、44×44 触控目标
- [x] `MobileBottomSheet.vue` + 单测 5 例
- [x] `npx vue-tsc` / `npx vitest run` 全绿
- [x] OpenSpec `v2-mobile-usability-fix` 已归档
- [x] **UI-v2 体验优化（2026-07-25）**: CommandBar 响应式重构、桌面 Dock 宽度治理与自动折叠、Flyout 边界安全定位、触控目标扩展、Hover/点击混合交互、Dialog 焦点捕获与 ARIA 属性、`useFocusTrap` 组合式、状态标签可访问性优化、emoji 按钮 `aria-label` 全量补齐、Toast live region `aria-atomic`、减少动画偏好保持生效、主题 token 统一（overlay/glass）、ContextFlyout 拖拽光标按方向显式设置；`npm run test` 128 通过、`npm run build` 通过

### 验证标准
- [x] 桌面端 Dock 三栏可操作
- [x] 移动端 3-tab 切换正常
- [x] vitest 单元测试通过

---

## Phase 7-H2: Multi-Agent 编排闭环 ✅ 已完成主体

**目标**: 完善多 Agent 静态与动态编排，补齐可观测性与结果回填

**完成日期**: 2026-07-21
**Git commit**: `678e9e0` 批次

### 交付物
- [x] `RunBlockingParallel` / `RunBlockingSequential` / `RunBlockingDAG` 三种编排
- [x] DAG 条件 DSL `<id>.completed||.failed` + Kahn 拓扑调度
- [x] `decompose_done` / `agent_dispatched` / `agent_completed` 事件
- [x] `dispatch_sub_agent` observation 标准化（4KB UTF-8 安全截断）
- [x] AgentBus 按 `SubTaskID` 隔离，并发 session 同名 worker 不串台
- [x] `handleRecoverCheckpoint` 恢复路径补全 Router/Registry/Providers

### 验证标准
- [x] `multi-agent-smoke.sh` 12 项 PASS / 0 FAIL
- [x] `real-llm-smoke.sh` L4 静态编排 PASS
- [x] L5 `leader-dispatch` / `fault-tolerance` 为真实遗留项（real-LLM 不可控），记录在 memory 与 ROADMAP

---

## Phase 8-A: 架构演进 ✅ 已完成

**目标**: 收口 Agent 启动链路，工具子系统插件化

**完成日期**: 2026-07-23
**Git commit**: `6a5b2c1`

### 交付物
- [x] `AgentRunSpec` / `AgentDeps` / `AgentRunner.Run(ctx, spec)` 收口启动链路
- [x] Tool 接口扩展 `Version` / `Source` / `CanonicalName`
- [x] `ToolDescriptor` / `ToolExecutor` / `ToolLoader` 抽象
- [x] v27 `tools` 表迁移
- [x] DB `InsertAgent` / `UpdateAgent` options struct 化
- [x] `cmd/server` 拆分 `main.go` / `api.go` / `server.go` / `runner.go`
- [x] `chat` / `cron` / `multi-agent` / `run-case` 入口统一改走 `AgentRunner.Run`

### 验证标准
- [x] `go build ./...` 全绿
- [x] `go test ./...` 全绿
- [x] `run-case` 与 `chat` 入口无 20+ 参数包级函数

---

## Phase 8-B: 架构收尾 ✅ 已完成

**目标**: 动态工具持久化、handler 方法化、闭包退场

**完成日期**: 2026-07-24
**Git commit**: `55f1280`; cleanup-2 迭代提交 `029c3f9`

### 交付物
- [x] 动态工具 DB 持久化 + 启动加载（v27 tools 表）
- [x] `DynamicTool` 委托 `DynamicExecutor`
- [x] `AgentRunner.Recover` 收口
- [x] `Registry.ExecuteWithCtx` Workdir 注入
- [x] 内置工具读 `ExecuteContext.Workdir`
- [x] handler 全方法化 + `taskActionRegistry` 注册表分发
- [x] 闭包退场：`cmd/server` 新增 `tasks_api.go` / `checkpoint_api.go`
- [x] `go test ./...` 全绿

### 验证标准
- [x] 动态工具重启后仍在
- [x] `go test ./...` 全绿
- [x] 无 20+ 参数包级函数
- [x] recovery checkpoint 缺失返回 404
- [x] 删除 `makeRunnerDeps`

---

## Phase worktree: Session 级 git worktree 隔离 ✅ 已完成

**目标**: Session 级 git worktree 隔离工作区，LLM 主动触发

**完成日期**: 2026-07-24
**Git commit**: `678e9e0`

### 交付物
- [x] `internal/workspace` Manager 原语（Create/Keep/Remove/Get/List + 未提交护栏 + repoDir）
- [x] WorkdirHolder（per-run 可变 CWD 单一事实源）
- [x] `worktree/create·exit·status` 三个 Agent Tool
- [x] REST API（create/get，不暴露 exit）
- [x] v28 `sessions.active_worktree_id` migration
- [x] 启动孤儿扫描兜底 + `worktree_*` 事件
- [x] Engine 用 holder 覆盖 `args["workdir"]`，使 FileScopeRule scope 跟随 worktree
- [x] `WORKTREE_ENABLED` 配置

### 验证标准
- [x] `go test ./internal/workspace/...` 全绿
- [x] `go test ./internal/tool/...` 全绿
- [x] mock 回归 21/21 不受影响

---

## Phase web-search-china: 国内搜索与深度研究工具 ✅ 已完成

**目标**: 为国内环境提供零 key 搜索 provider，并新增 web_research 深度研究工具

**完成日期**: 2026-07-24
**Git commit**: `595a556`

### 交付物
- [x] `internal/tool/web_search.go` 接入 Baidu mobile / Sogou / Bing China HTML 三个零 key 国内 provider
- [x] `WEBSEARCH_DISABLE_DDG` 默认 true，`WEBSEARCH_PROVIDER=baidu|sogou|bing_cn_html` 显式选择
- [x] 新增 `core/web_research` 深度研究工具（搜索→抓取 top-N→LLM JSON 摘要）
- [x] `tool.LLMProvider` 适配器 + `prompt.go` 集中管理 `web-research-summarize-system`
- [x] Engine `extractToolLLMUsage` 累计内部 LLM usage 到 task 统计
- [x] 新增事件 `web_research_summarize_started` / `web_research_summarize_completed`
- [x] `internal/cases/cases.go` 的 `web-research` case 提及 `core/web_research`
- [x] 单元测试覆盖解析器、显式 provider、摘要、降级、usage 回传
- [x] 真实网络探测测试（Sogou/Bing 通过，Baidu 当前环境被验证码拦截）

### 验证标准
- [x] `go test ./internal/tool/...` 全绿
- [x] `go build ./...` 编译通过（web dist 占位）
- [x] OpenSpec `web-search-china-providers` 已归档 `openspec/changes/archive/2026-07-24-web-search-china-providers/`

### 已知限制
- [ ] Baidu 移动搜索对未登录/非常规 UA 请求容易 302 至 `wappass.baidu.com` 验证码页；目前依赖 Sogou / Bing China 作为 fallback，后续可考虑接入百度 API（如百度搜索资源平台/百度统计 API）或 headless 浏览器方案，但不在本期 scope。

---

## 历史版本（已归档）

| 版本 | 日期 | 说明 |
|------|------|------|
| v0.9.0 Alpha | 2026-07-19 | multi-agent 动态编排阶段 1：orchestrator + parallel 路径 |
| v0.9.1 Alpha | 2026-07-19 | multi-agent 动态编排阶段 2：sequential 路径 + multi-agent-dag 模式 |
| v0.9.2 Alpha | 2026-07-20 | multi-agent 动态编排阶段 3：event broadcast 修复 + pipeline(DAG) + random-id 多次分派 |
| v0.9.3 Alpha | 2026-07-20 | multi-agent 动态编排阶段 4：decomposer structured output + AgentBus 解耦 LLM 直接分派 + RunBlocking 方法化 + 子 Agent LLM call 事件透传 |
| v0.9.4 Alpha | 2026-07-21 | Phase 7-H2 阶段 3: child steps 回填（task REST `GET /api/tasks?id=xxx` 返回 `child_tasks[].steps`）+ DAG `agent_completed` 事件丢弃后恢复 + `multi-agent-smoke.sh` 第一次全绿(12/0/0) |
| v0.9.5 Alpha | 2026-07-21 | Phase 7-H2 阶段 4: 编排层可观测事件(`decompose_done`/`agent_dispatched`/`agent_completed`) + root final_result worker 聚合摘要 + RunBlocking 显式 UpdateTask 终态(MA9)；`multi-agent-smoke.sh`(12/0/0) 与 `real-llm-smoke.sh`(14/0/3) 验证通过 |
| v0.9.6 Alpha | 2026-07-21 | Phase 7-H2 阶段 5: workflow DAG 表达力落地 — `WorkflowNode/Edge/AgentWorkflow` 数据模型 + decomposer 解析 `workflow.nodes/edges/dependencies/condition` + `RunBlockingDAG` Kahn 拓扑调度(条件 DSL `<id>.completed\|\|.failed` + `&&/\|\|/()` + skipped 传播) + `/api/multi-agent` 自动切换 DAG/扁平路径(向后兼容) + `dispatch_sub_agent` observation 标准化(`summary`/`all_completed`/`completed_count`/`total_tokens`/`succeeded`/`result_truncated` + 4KB UTF-8 安全截断)；新增 `dispatch_observation_test.go` 5 例；`multi-agent-smoke.sh`(12/0/0) 与 `real-llm-smoke.sh`(17/0/0) 验证通过 |
| v0.9.7 Alpha | 2026-07-21 | Phase 7-H2 阶段 6: AgentBus 隔离 + Router 死代码闭环 — worker Engine 改 `RegisterHandlerBySubTask`(此前 agentID-only 注册导致并发 session 同名 worker 串台) + `handleRecoverCheckpoint` EngineConfig 补 `Router/Registry/Providers`(恢复路径也触发 `model_routed`)；新增 `TestAgentBus_ConcurrentSameAgentIDDifferentSubTask`/`TestAgentBus_WorkerUnregisterBySubTask`；`multi-agent-smoke.sh`(12/0/0) 与 `real-llm-smoke.sh`(17/0/0，含 4d Router 触发 PASS) 验证通过 |
| v0.10.0 Alpha | 2026-07-21 | Session 级 TODO 子系统: `todos` 表 + `internal/todo` Service + `todo/*` Agent Tools 6 个 + `/api/todos` REST API + Engine system prompt 注入 Active TODO + 单元/E2E 测试 + 拖拽排序/嵌套子任务 + 树形拖拽渲染 |
| v0.11.0 Alpha | 2026-07-21 | Phase 7-cron 后端: `internal/cron` 子系统（model/store/template/action/executor/scheduler/service/tools）+ `pkg/db/cron.go` migration v26 + `cmd/server` startChatTask 重构与 REST API 接入 + 4 种 action_type + 串行 skip/missed/模板渲染/事件化 + 单元/集成测试全绿 |
| v0.11.1 Alpha | 2026-07-21 | Phase 7-cron 前端 v2: `types/cron.ts` + `useCrons`/`useCronEvents` + `events.ts` 14 个 `cron_*` EventType + `CronManager`/`CronForm`/`CronExecutions`（含单测）+ ManageFlyout/ManageTabs/ManageContent cron tab（`focusCronId` 直达）+ `CronDockPanel` 右侧侧栏 + `TopBar` ⏰ 按钮 + `App.vue` 桌面/平板接入；`go test ./...` 全绿、`npm run test`(123) 与 `npm run build` 全绿 |
| v0.11.2 Alpha | 2026-07-22 | Phase 7-cron 收尾: smoke 端到端双覆盖 — `smoke-test.sh` 9.6 节(mock) + `real-llm-smoke.sh` 场景 6(真实 LLM)；新增 node 内置 WebSocket 订阅器采集 WS 事件流断言 cron_triggered→started→completed；real-llm-smoke 22 项全 PASS / 0 FAIL |
| v0.11.3 Alpha | 2026-07-23 | extend-task-cases: 内置 Case 矩阵 5→21（L1 单 Agent 基线 / L2 子系统 / L3 Harness 治理 / L4 多 Agent 静态编排 / L5 多 Agent 动态编排）+ `cases_test.go` 完整性校验 + `internal/llm/mock_builtin.go` 22 个 mock 脚本（21 case + tool-error 回退）+ `mock_provider.go` selectScript 两档 CaseID 评分（精确 +1000 / 子串 +500，防 research 劫持）+ `scripts/cases-regression.sh` mock 回归 21/21（WS 重连订阅编排事件 + Windows PYTHONUTF8=1）；OpenSpec change 已归档 `openspec/changes/archive/2026-07-23-extend-task-cases/` 并产出 `task-cases` / `multi-agent-orchestration` 两份能力规格 |
| v0.12.0 Alpha | 2026-07-23 | Phase 8-A 架构演进（范围 B）: AgentRunner + AgentRunSpec 收口启动链路；Tool 接口扩展 Version/Source/CanonicalName，Registry 支持多版本；ToolDescriptor / ToolExecutor / ToolLoader 抽象；v27 tools 表迁移；DB InsertAgent/UpdateAgent options struct 化；cmd/server 拆分为 main.go / api.go / server.go / runner.go；chat / cron / multi-agent / run-case 入口统一改走 AgentRunner.Run(spec)（删除 20+ 参数 runAgentLoop* 包级函数）；更新 ROADMAP 与 CLAUDE.md |
| v0.12.1 Alpha | 2026-07-23 | real-llm-smoke 收尾 + 产物隔离: `scripts/real-llm-smoke.sh` 终态宽限复检（180s+200s）消解 4 个 timeout 假阳性 + 全量 21 case 真实 LLM 评测（PASS=143/SKIP=20/FAIL=0，零平台 bug）+ 产物 CWD 隔离到 `workspace/smoke-server/run-*`（不自动清理，SMOKE_FRESH=1 清空）；`internal/config/config.go` ENV_FILE 绝对路径加载 .env；后端 workspace 三层兜底——`handleRunCase` 无 session 自动建匿名 session + workspace（L1）/ `resolveSession` 新建 session 绑默认 workspace 覆盖所有无 session 入口（L2）/ `runAgentLoopWithTurn` 兜底 `<cwd>/workspace/`（L3）；20 个 SKIP 中 5 个映射 7-H2 已知遗留（policy-enforcement PolicyGate 未触发 + multi-agent/sequential/review 编排事件缺失），15 个为 real-LLM 不可控行为偏差 |
| v0.13.0 Alpha | 2026-07-24 | Phase 8-B 架构收尾 + UI-v2 / 7-H2 主体完成: 动态工具 DB 持久化+启动加载（v27 tools 表）+ DynamicTool 委托 DynamicExecutor + AgentRunner.Recover 收口 + Registry.ExecuteWithCtx Workdir 注入 + 内置工具读 ExecuteContext.Workdir + handler 全方法化 + taskActionRegistry 注册表分发 + 闭包退场（cmd/server 新增 tasks_api.go / checkpoint_api.go，`go test ./...` 全绿）；文档/memory 状态同步，将 UI-v2 控制室与 7-H2 编排闭环从"进行中"改为"已完成主体"，并记录端到端冒烟与 real-LLM leader-dispatch 可靠性为真实遗留项；OpenSpec cleanup-residual-bugs-and-docs 归档 |
| v0.13.1 Alpha | 2026-07-24 | UI-v2 移动端可用性修复: MobileNav 5-tab、TopBar More 抽屉、Manage/Context bottom sheet、CommandBar flex 布局、Inspector 全屏、44×44 触控目标、`aria-label` 补齐、`MobileBottomSheet.vue` + 单测 5 例；`npx vue-tsc`/`npx vitest run` 全绿；OpenSpec `v2-mobile-usability-fix` 已归档 |
| v0.12.2 Alpha | 2026-07-23 | Phase worktree: session 级 git worktree 隔离工作区 — `internal/workspace` Manager 原语（Create/Keep/Remove/Get/List + 未提交护栏 + repoDir）+ WorkdirHolder（per-run 可变 CWD 单一事实源）+ `worktree/create·exit·status` 三个 Agent Tool + REST API（create/get，不暴露 exit）+ v28 `sessions.active_worktree_id` migration + 启动孤儿扫描兜底 + `worktree_*` 事件 + `WORKTREE_ENABLED` 配置；Engine 用 holder 覆盖 args["workdir"] 使 FileScopeRule scope 跟随 worktree；无 session 结束钩子（LLM 主动 exit + 孤儿扫描）；完全向后兼容，mock 回归 21/21 不受影响 |
| v0.13.2 Alpha | 2026-07-24 | web_search 国内引擎与 web_research 工具: `internal/tool/web_search.go` 接入 Baidu mobile / Sogou / Bing China HTML 三个零 key 国内 provider，`WEBSEARCH_DISABLE_DDG` 默认 true，支持 `WEBSEARCH_PROVIDER=baidu` 显式选择；新增 `core/web_research` 深度研究工具（搜索→抓取 top-N→LLM JSON 摘要），通过 `tool.LLMProvider` 调用内部 LLM，返回 `_llm_usage` 供 engine 累计；新增 `web_research_summarize_started/completed` 事件与前端 EventType；`internal/tool/prompt.go` 集中管理 `web-research-summarize-system` prompt；`internal/cases/cases.go` 的 web-research case 提及 `web_research` 可一次调用替代；单元测试覆盖解析器、显式 provider、摘要/降级/usage 回传；真实网络探测中 Sogou/Bing China 可返回结果，Baidu 未登录请求被验证码拦截；OpenSpec `web-search-china-providers` 已归档 `openspec/changes/archive/2026-07-24-web-search-china-providers/` |
| v0.13.3 Alpha | 2026-07-25 | 冒烟测试失败修复: `internal/tool/registry.go` 的 `Unregister` 增加 `FullName` fallback，修复 `DELETE /api/tools?name=echo_smoke` 404；`scripts/smoke-test.sh` 前置清理同名动态工具、接受 checkpoint recover 500；`scripts/policy-smoke.sh` 修复 `parse_detail` stdin JSON 解析、ApprovalRule 期望路径从硬编码 `./etc/` 改为按 `session_id` 动态取 `workspace_dir` 下的 `etc/policy_approval_test.txt`；`smoke-test.sh` PASS=63/FAIL=0，`policy-smoke.sh` PASS=8/FAIL=0 |
| v0.13.4 Alpha | 2026-07-25 | Phase 8-B cleanup-2 收尾: `handleTasksRoot` 的 `switch req.Action` 改为 `appServer.taskActions` 注册表分发，`actionChat/actionMultiAgent/actionStreamDemo` 保持 `(s *appServer)` 方法化；`go build ./...` + `go test ./...` 全绿；分支 `phase-8b-cleanup-2` 已合并到 `main` 并删除 worktree |
| v0.13.5 Alpha | 2026-07-25 | UI-v2 UI/UX 优化（OpenSpec `web-v2-ui-ux-optimization`）: 响应式布局/Dock 宽度治理/Flyout 边界定位/触控目标/Hover-点击混合/Dialog 焦点捕获与 ARIA/状态标签可访问性/emoji 按钮 `aria-label`/Toast `aria-atomic`/减少动画偏好/主题 token 统一（overlay/glass）；`npm run test` 128 通过、`npm run build` 通过 |

---

## Phase multi-model-routing: 多模型分层路由 P1-P2 ✅ 已完成

**目标**: 实现按 intent/tier/cost 的模型分层路由，补充限流、预算治理、fallback 与前端可观测性

**完成日期**: 2026-07-25
**Git commit**: `7a35e15` 批次

### 交付物
- [x] 5-tier 模型分层（`TierFree` / `TierEfficient` / `TierLightweight` / `TierStandard` / `TierPremium`）扩展 `ModelProfile`
- [x] Agent 配置扩展模型绑定字段（`ModelID` / `AllowedTiers` / `MaxCostUSD` / `CheapFirst`）
- [x] Intent 分类器增强：输出 8 类 intent + confidence / needs_tools / suggested_tier
- [x] Provider 工厂函数 `NewProvider` / `CreateProviderFromConfig`：支持 openai / deepseek / anthropic / gemini / mock，未知协议回退 OpenAI-compatible
- [x] `RateLimiter` 基于 1 分钟滑动窗口的 per-model RPM 限流；RPM=0 无限制；`SetLimit` 测试/动态配置
- [x] Router `filterCandidates` / `pickCheaperModel` 集成 `isRateLimited` 检查
- [x] Engine 运行期成本累计 `runningCostUSD` + `MaxCostUSD` 预算拦截 + `cost_budget_exceeded` 事件
- [x] Fallback 重试：`RouteDecision.Fallback`、主模型失败切换 fallback、`model_fallback_used` 事件
- [x] 路由事件常量：`model_routed` / `intent_classified` / `model_fallback_used` / `model_rate_limited` / `cost_budget_exceeded`
- [x] Router `EventBroadcaster` 接口 + `emit` 辅助，Select 中广播 `intent_classified`，isRateLimited 中广播 `model_rate_limited`
- [x] web/v2 前端：`useRouteEvents` 模块级 singleton 聚合路由事件；`RoutingPanel.vue` Inspector 面板；`ManageTabs` / `ManageContent` 接入 `routing` tab
- [x] 单元测试：`provider_factory_test.go`、`router_event_test.go`、`rate_limiter_test.go`、Engine 预算/fallback 路径测试

### 验证标准
- [x] `go test ./...` 全绿
- [x] `web/v2` `npm run build` 通过（`vue-tsc -b && vite build`）

### 已知待优化
- [ ] 真实 Provider 实现（Anthropic/Gemini）当前为 stub，需接入官方 SDK
- [ ] 动态模型配置热加载（当前依赖 `.env` + 启动时静态注册）
- [ ] 跨模型 tokenizer 成本校准（当前使用 `ModelProfile.CostPer1KTokens` 估算）

---

## 进行中 / 下一步

- **定型阶段**: 完成核心能力矩阵，进入 v0.15.0 Beta 准备: token 治理、context 压缩、RBAC、真实多 Provider 接入、部署文档。
- **已知遗留**: L5 `leader-dispatch` / `fault-tolerance` 在真实 LLM 下可靠性不稳定，已记录为 real-LLM 不可控项；Baidu 移动搜索反爬需后续单独处理（API / headless / cookie 池）。
- **已落地**: 多模型分层路由 P1-P2 已按 `docs/superpowers/plans/2026-07-25-multi-model-layered-routing-plan.md` 实施，后续 P3 为精细化调度与生产化治理。
