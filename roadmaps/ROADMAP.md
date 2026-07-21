# 多 Agent 平台 — 产品路线图

> **最近更新**: 2026-07-21
> **当前版本**: v0.8.0 Alpha（Skill 系统 + MCP 按 agent 可见性 + contract limits 闭环）
> **更新规则**: 每个 Phase 任务完成后，必须更新本文件并提交 Git。

---

## 路线图总览

```
Phase 0 ✅ → Phase 1 ✅ → Phase 2 ✅ → Phase 3 ✅ → Phase 4 ✅ → Phase 5 ✅ → Phase 6 ✅ → Phase skill ✅ → Phase UI-v2 🚧 (Skeleton)
  (骨架)      (Agent)     (UI)       (Cases)    (并发)      (注册)      (高级)       (Skill 系统)
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
- [ ] Agent CRUD 前端页面 → Phase 4（配置页面与 Agent CRUD 合并实现）
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
- [ ] **多模型配置加载**: 从 `.env` / DB 加载多个模型配置
- [x] **Harness: PolicyChain 完整实现**（PolicyGate + PolicyChain + 内置规则链）
- [x] **Harness: TokenBudgetRule**（累计 token 超过 TaskContract 预算时硬拒绝）
- [x] **Harness: ToolWhitelistRule**（只允许 TaskContract 中声明的工具）
- [ ] **Harness: Checkpoint / Recovery**（CheckpointManager + 崩溃恢复流程）
- [ ] **Memory: `memories` 表 + `memory_links` 表** Schema（pkg/db/database.go）
- [ ] **Memory: Heartbeat 后台整理器**（定时扫描新 conversation → 触发抽取管线）
- [ ] **Memory: Candidate → Semantic 晋升管线**（三条晋升通道的代码实现）

### Phase 3 遗留推进
- [x] 全局版本文件 `version.txt` → `go:embed` → `/api/version` → Vue 响应式绑定
- [x] 发送消息非阻塞 loading 动画（isTaskPending + 15s 安全超时）
- [x] 全局 Toast 错误提示
- [x] AgentTree 空状态骨架屏
- [x] AgentTree 智能滚动（用户上滚暂停自动滚动，显示 "↓ Bottom" 按钮）
- [x] CaseCard 点击卡片显示详情弹窗，Run 按钮独立触发
- [x] 全局快捷键系统（Ctrl+Shift+C 取消、Ctrl+Shift+P 暂停/恢复、? 提示面板）
- [x] TypeWriter 代码块复制按钮 + Tool output JSON 格式化/还原切换
- [x] MetricsPanel 运行时长 + Agent 选择器占坑

### 验证标准
- [x] 一个任务拆成 2 个 Agent 并行，前端同时看到两棵树更新
- [ ] 不同 Agent 使用不同模型（如一个用 deepseek-flash，一个用 deepseek-pro）→ 延迟到 Phase 5（ModelRegistry）
- [x] 工具调用超过 TokenBudget 时被 PolicyGate 拦截，Engine 收到 ErrBlockedByPolicy
- [ ] 进程崩溃后可从 checkpoint 恢复，不从头开始 → 延迟到 Phase 5
- [ ] 心跳定时触发记忆抽取，Semantic 规则有明确的 promotion_reason → 延迟到 Phase 5

---

## Phase 5: 运行时注册 + Provider + Router + 记忆召回 + 会话历史 + 多轮对话

**目标**: 支持动态注册工具和 Agent，引入 Provider 抽象、Router 路由、会话/历史管理、记忆召回、Project 管理、多轮对话

**完成日期**: 2026-07-07
**Git commit**: `c224906`

### 交付物
- [x] **Session 管理 + Task 金字塔结构**（后端持久化 + 前端会话列表）
- [x] **Session CRUD API**（`/api/sessions` + `/api/sessions/:id`）
- [x] **前端 Session 侧边栏**（useSessionStore + localStorage 缓存）
- [x] **useTaskStore 重构**（单任务 → taskCache + activeTaskId）
- [x] **resolveSession + deriveSessionStatus**（自动创建/绑定 Session）
- [x] 运行时 Tool 注册 REST API
- [ ] AI 自描述工具注册（LLM 生成 JSON Schema → 自动注册）→ Phase 6
- [x] Docker 沙箱（run_shell 安全隔离）
- [x] **LLM Provider 接口抽象**: `Provider` 接口 + `OpenAIProvider` 基线实现
- [x] **Router 路由决策**: 意图分类 + 模型选择（轻量模型做路由，成本 < $0.001/次）
- [x] **模型能力矩阵**: 标注各模型的 tool_calling / streaming / vision / reasoning 能力
- [x] **Harness: ApprovalRule**（高风险操作通过 WebSocket 发送确认请求到前端）
- [x] **Harness: DangerousCommandRule**（Shell 命令危险模式检测）
- [x] **Memory: MemoryRecall 召回**（新任务启动时构建 Working Memory）
- [x] **Memory: 记忆冲突检测 + 合并**（同义规则合并，冲突规则标记）
- [x] **Memory: memories 表 + memory_links 表 + Heartbeat 后台整理器**
- [x] **AgentBus 接入 Engine ReAct Loop**（Agent 间通信）
- [x] **Checkpoint / Recovery**（任务检查点 + 崩溃恢复）
- [x] **Agent 配置 CRUD 前端页面**（创建/编辑/删除 Agent）

### Phase 5-A: Project 管理 + 多轮对话 ✅ 已完成 (2026-07-07)
- [x] **Project CRUD API**（`/api/projects` + `/api/projects/:id`）
- [x] **Project 管理前端**（useProjectStore + 侧边栏 Project 分组 + ProjectConfig 组件）
- [x] **session_messages 表**（Session 级消息持久化，Engine 通过 SessionMessageWriter 同步写入）
- [x] **多轮对话 API**（`POST /api/sessions/:id/chat` + `GET /api/sessions/:id/messages`）
- [x] **多轮对话上下文注入**（新 Task 启动时自动注入历史 messages + Working Memory）
- [x] **前端多轮时间线**（TurnList + TurnItem 组件，展开/折叠，时间线展示）
- [x] **Session 内继续聊天**（COMPLETED/FAILED 的 Session 也可继续发送消息）
- [x] **任务层级架构**（root → turn_2, turn_3 (siblings) → child_of_turn_2 (children)）
- [x] **DB 迁移 v5-v8**（projects 表 + session_messages 表 + sessions/tasks/memories 新增字段）

### Phase 5-B: 上下文压缩 + 记忆作用域（优化）✅ 已完成 (2026-07-07)
- [x] Memory 作用域扩展（scope 字段 + session/project/global 召回优先级）
- [x] 上下文压缩引擎（阈值检测 turn_count>=20 或 total_tokens>=100KB + 摘要生成）
- [x] 前端 Memory 浏览页（按 scope/project 查看记忆）

### 新增核心设计：会话与任务历史管理（Session & Task History）

为了让用户在不刷新页面的情况下启动多个任务、切换查看历史任务、继续执行已失败的任务，Phase 5 引入 **Session（会话）** 概念。

#### 核心概念

| 概念 | 说明 |
|------|------|
| **Session** | 前端的一次"对话上下文"，在后端持久化。每个 Session 有一个根 Task，根 Task 可派生多个子 Task。 |
| **Root Task** | 用户发起的主 Agent 任务（`tasks.is_root = 1`）。Session 的 `root_task_id` 指向它。 |
| **Child Task** | 由根 Task 或 Agent 派生的子任务（`tasks.parent_task_id`），形成 Task 金字塔。 |
| **Active Session** | 当前用户正在查看的 Session。切换 Session 不影响正在运行的其他任务。 |
| **Task History** | 所有已完成 / 失败 / 进行中的 Task 列表，按 Session 组织。 |

#### 前端状态设计

```ts
interface Session {
  id: string           // 后端生成的 session id
  name: string         // 默认取 user_input 前 30 字符或 "New Session"
  rootTaskId: string | null
  status: 'empty' | 'running' | 'completed' | 'failed'
  totalTokens: number  // 聚合该 Session 下所有 Task 的 token
  createdAt: number
  updatedAt: number
}
```

`useTaskStore` 从"单任务"改为"任务缓存"：
```ts
const taskCache = ref<Record<string, TaskState>>({})  // taskId -> TaskState
const activeTaskId = ref<string | null>(null)
```

#### 用户交互流程

1. **新建会话**：侧边栏 `+ New Session` → 后端创建空 Session → 回到主界面（预设 Cases + 输入框）
2. **发送消息**：在 Session 内启动根 Task → WebSocket 事件更新 `taskCache[taskId]`
3. **子任务生成**：多 Agent 执行时，子 Agent 的子 Task 作为 child task 绑定同一 Session
4. **会话列表**：左侧边栏展示所有 Session，显示名称、状态、总 token、总耗时
5. **切换会话**：点击历史 Session → 显示根 Task 及子 Task 的 AgentTree
6. **删除会话**：删除 Session 及其下所有 Task/Steps/Files（级联）→ 自动切换到其他 Session
7. **继续执行**：在历史失败 Session 上 Continue → 在当前 Session 内开启新一轮，保留完整会话上下文
8. **服务端恢复**：刷新后前端调用 `GET /api/sessions` 拉取历史 Session 列表

#### 后端数据模型

-新增 `sessions` 表：`id`、`name`、`root_task_id`、`status`、`user_input`、`created_at`、`updated_at`
- `tasks` 表新增：`session_id`、`parent_task_id`、`is_root`
- 新增索引：`idx_tasks_session_id`, `idx_tasks_parent_task_id`

#### 与现有功能的关系

- `lastUserInput`（Phase 4+） → Session 级别保存，用于 Continue
- Continue with max steps ×2（Phase 4+） → 在 Session 上下文内重新启动
- Task 历史侧边栏（Phase 3 遗留） → 升级为 Session 列表
- 后端新增 API：`/api/sessions` CRUD，`/api/tasks` 与 `/api/multi-agent` 增加 `session_id` 参数

### Phase 4 延迟项
- ~~Agent 配置 CRUD 前端页面~~ → Phase 5 已完成
- ~~Task 历史侧边栏（升级为 Session 列表）~~ → Phase 5 已完成
- ~~Memory: Task 完成时自动生成摘要（用于 Session 名称和预览）~~ → Phase 5 已完成
- ~~AgentBus 接入 Engine ReAct Loop~~ → Phase 5 已完成
- ~~ModelProfile + ModelRegistry~~ → Phase 5 已完成
- ~~Checkpoint / Recovery~~ → Phase 5 已完成
- ~~Memory: memories/memories_links 表 + Heartbeat + Candidate→Semantic 晋升管线~~ → Phase 5 已完成

### 验证标准
- [x] 无需重启服务，通过 API 注册新工具并立即使用
- [x] 同一任务请求根据意图自动路由到不同模型（简单→Flash，复杂→Pro）
- [x] 高风险操作（如 git push）触发前端审批弹窗
- [x] 新任务启动时，Semantic 规则和相关 Episode 写入 Working Memory 注入 System Prompt
- [x] 完成一个任务后，点击「新建会话」即可回到主界面继续发起新任务
- [x] 刷新页面后，历史任务列表可恢复，点击历史任务可回看执行过程和结果

---

## Phase 6: 高级特性 ✅ 已完成

**目标**: 生产级特性 — 多厂商 LLM、成本控制、安全合规、记忆治理、可观测性

**完成日期**: 2026-07-15
**Git commit**: `Phase 6-F: memory type system + CRUD + LLM summarizer + vector persistence + frontend observability`
**版本**: v0.6.5 Alpha

### Phase 6-C 交付物（技术债务修复 + 骨架）
- [x] 多厂商 LLM Provider: AnthropicProvider + DeepSeek reasoning_content + Provider 工厂
- [x] Router 接入 Engine: 动态模型选择 + Fallback 降级重试 + model_routed 白盒事件
- [x] Worker Pool 并发调度: 优先级队列 + 信号量限流 + 任务取消
- [x] CostTracker 成本追踪: cost_records 表 + 多维度聚合
- [x] CostBudgetRule: 集成到 PolicyChain + TaskContract.CostBudgetUSD
- [x] 降级策略: ResolveFallbackChain + IsRetryableError + 自动 fallback
- [x] Provider Context 传递: ChatRequest.Context 透传，fallback 使用父 ctx
- [x] CostTracker 整数精度: CostCents int64 存储，避免浮点漂移
- [x] ProviderRegistry 排序: List() 返回稳定字母序
- [x] 迁移版本对齐: 补齐 v8 no-op 占位迁移
- [x] RAG 基础骨架: EmbeddingProvider + VectorStore + InMemoryVectorStore
- [x] Auth 基础骨架: User/Role/APIKey + bcrypt 哈希 + CRUD 端点骨架
- [x] 可观测性骨架: StructuredLogger JSON 结构化日志

### Phase 6-D 交付物（可观测性 + 成本持久化落地，非空壳）
- [x] 结构化日志接入业务流: `LOG_LEVEL` 配置 + server/DB/任务生命周期 JSON 日志
- [x] `/healthz` 端点: DB ping + WS hub 状态 JSON 检查
- [x] `/metrics` 端点: Prometheus 文本格式暴露 `agent_tasks_total`, `llm_calls_total`, `llm_tokens_total`, `cost_cents_total`
- [x] 任务状态计数器: started / completed / failed 计数接入 MetricsCollector
- [x] migration v11: `cost_records` 表新增 `cost_cents` 列并回填旧数据
- [x] `CostRepository` 接口: 内存 store + SQLite store，任务运行时写入真实记录
- [x] `OnLLMUsage` callback: Engine 与成本/指标子系统解耦的集成点
- [x] `/api/costs` 查询端点: 按 task_id / session_id / project_id 聚合，从 repository 读取
- [x] `modelRegistry` 注入 CostTracker: tier / provider / pricing 字段正确填充
- [x] 验证: `go build ./...`, `go vet ./...` 通过；curl `/healthz`, `/metrics`, `/api/costs` 均返回正确数据；任务运行后 `cost_records` 产生真实记录

### Phase 6-F 交付物（Memory 类型体系 + CRUD + LLM 摘要 + 向量持久化 + 前端可观测性）
- [x] CosineSimilarity 复核并清理过时 BUG 注释，补充非单位向量回归测试
- [x] 向量库 SQLite 持久化: migration v16 `memory_embeddings` 表 + `SqliteVectorStore` 启动加载 + 写时同步
- [x] 真实 LLM 摘要: `LLMSummarizerImpl` 接管 `ContextCompressor` / `Heartbeat`，失败回退 keyword 路径
- [x] Memory 类型体系: `preference/rule/fact/lesson/reflection/session_summary` 校验 + API filter/pagination/stats
- [x] Memory CRUD API: `GET/POST/PUT/DELETE /api/memories` + `/api/memories/:id/embed` + `/api/memories/stats`
- [x] 前端可观测性: `MemoryBrowser` + `RAGPreviewPanel` + `MemoryEventsTimeline` + `MemoryCreateDialog` + tabbed overlay
- [x] 验证: `go build ./...`, `go vet ./...`, `go test ./...`, `vue-tsc --noEmit`, `vite build` 全通过

### Phase 6-E 交付物（Auth 实际生效 + RAG 向量召回落地，非空壳）
- [x] migration v12: 创建 `users` 表与 `api_keys` 表（bcrypt 哈希存储，prefix 索引加速验证）
- [x] DB-backed `auth.APIKeyStore`: SqliteAPIKeyStore 实现 Create/List/Revoke/Verify（prefix 预筛 + bcrypt）
- [x] 默认 admin 用户 + API key 自动种子: 首次启动打印到日志一次，之后不再显示
- [x] `/api/auth/api-keys` 端点: GET 列表 / POST 创建 / DELETE 吊销，归属校验
- [x] 可配置 Auth 中间件: `REQUIRE_AUTH=true` 时校验 `Authorization: Bearer <key>`，默认关闭注入种子用户
- [x] 受保护操作: 删除 session/project、创建/删除 agent、工具注册、删除/更新 memory、run_shell 等写操作
- [x] 本地 EmbeddingProvider: `LocalEmbeddingProvider` 基于 FNV-1a 哈希的 TF-IDF/one-hot 向量（vocabSize=2048，零外部依赖）
- [x] MemoryRecall 向量索引: `BuildVectorIndex` 启动时加载 consolidated/semantic 记忆并嵌入 InMemoryVectorStore
- [x] 召回向量精排: `blendVectorScores` 混合关键词与余弦相似度（0.3 keyword + 0.7 vector）
- [x] `/api/memories/recall?query=` 端点: 纯向量检索返回按相似度排序的记忆列表
- [x] 验证: `go build ./...`, `go vet ./...` 通过；启动后 auth 端点 CRUD 正常（auth on/off 双模式）；向量召回端点返回正确结构

----

## Phase 6-G: 上下文窗口可观测性 ✅ (2026-07-16)

> **版本**: v0.6.6 Alpha  
> **Commit**: `Phase 6-G: context window observability — snapshot events + UI panel + smoke test`

### 交付物
- [x] 后端 Token 估算：`internal/llm/token_estimate.go` + 单元测试
- [x] Engine 每次 `think()` 前发射 `context_window_snapshot` 事件，含 model / max_context_tokens / estimated_total_tokens / estimated_usage_ratio / messages
- [x] 前端 `ContextWindowPanel.vue`：总量进度条 + role 分组条形图 + 可展开 message 列表
- [x] 前端 `useContextWindow.ts`：task-scoped 快照 + 自动/手动 Refresh
- [x] 后端 API `GET /api/tasks/:id/context_window`：内存优先 + DB 重建
- [x] 新增 `scripts/context-window-smoke.sh` + `scripts/context-window-smoke.go`，real-LLM 冒烟测试验证事件字段

### 后续上下文窗口增强（本次提交）
- [x] 默认 max context tokens 从 64K 提升到 200K，与现代主流大上下文模型对齐
  - 修改：`internal/llm/token_estimate.go` 引入 `defaultContextWindow = 200_000`
  - 同步更新 `internal/llm/token_estimate_registry_test.go` 断言
- [x] 拆分 `/api/tasks` 与 `/api/tasks/` handler，修复 `/api/tasks/:id` 和 `/api/tasks/:id/context_window` 404
- [x] `newTaskID()` 毫秒后缀：同一秒内多个 task 不再 ID 冲突
- [x] 历史 session Context Window 从 `session_messages` 重建，不再显示 "Waiting for the next agent think step..."
- [x] 前端移除 `ContextWindowPanel` 中 `watch(immediate)` 与 `onMounted` 双重刷新，避免重复请求 `fetchContextWindowSnapshot()`

### 验证
- `go test ./internal/llm ./internal/runtime` ✅
- `go build ./cmd/server` ✅
- `cd web && npm run build` ✅
- `bash scripts/context-window-smoke.sh` (LLM_USE_MOCK=false) ✅ PASS 9 / FAIL 0

### 已知限制
- token 数为本地启发式估算（~4 字符/token），非 API 精确值；字段命名已作 `estimated_*` 区分。
- 超长 tool 输出 message 会完整进入事件 payload，后续若出现性能问题可再引入 truncation + 按需拉取。
- refresh API 对非活跃任务只能基于 DB 对话记录做 best-effort 重建，tool_call 结构可能不完整。

---

## Phase 6 收尾：安全与质量修复批次 ✅ (2026-07-10 ~ 2026-07-11)

> **版本**: v0.6.2 Alpha
> **Commit**: `7f24a24` / `750e98f` / `94b9bba` / `692cad8` / `bd0cc4b`

### 修复摘要（源自 docs/TEST_REPORT.md 5 维度端到端评测）

| 编号 | 问题 | 修复文件 | 状态 |
|------|------|---------|------|
| S1 | FileScopeRule Windows 放行 Unix 绝对路径 | `internal/harness/harness.go` | ✅ |
| S2 | child_tasks 永远空（未设 parent_task_id） | `internal/orchestrator/orchestrator.go` | ✅ (随 S2 父提交) |
| S3 | SQLite 并发写丢失（缺 busy_timeout/WAL） | `pkg/db/database.go` | ✅ (随 S3 父提交) |
| S4 | step ID 碰撞 | `cmd/server/persistence.go` | ✅ (随 S4 父提交) |
| S5 | root task agent_ids 永远空 | `pkg/db/persistence.go` | ✅ (随 S5 父提交) |
| S6 | cancel 控制消息未实现 | `cmd/server/main.go` | ✅ cancel 已实现 |
| S7 | 硬性安全拦截转 30s 审批超时 | `internal/runtime/engine.go` | ✅ |
| M1 | Policy 拦截原因不持久化 | `internal/runtime/engine.go` | ✅ (随 S7) |
| M2 | CostBudgetRule 未接入 orchestrator | `internal/orchestrator/orchestrator.go` | ✅ |
| M3 | /api/tasks body 不支持 TaskContract 透传 | `cmd/server/main.go` + `api.go` | ✅ |
| F1 | ApprovalDialog 关闭静默丢消息 | `web/src/App.vue` | ✅ |
| F2 | sendControl 断线丢消息 | `web/src/composables/useWebSocket.ts` | ✅ |
| F3 | loadTask agentModelMap 死代码 | `web/src/composables/useTaskStore.ts` | ✅ |
| F7 | loadTask startedAt 时间戳为 0 | `web/src/composables/useTaskStore.ts` | ✅ |

### 验证
- `go build ./...` / `go test ./...` / `vue-tsc` 全通过
- 端到端评测：34 PASS / 8 FAIL / 3 SKIP（报告见 `docs/TEST_REPORT.md`）

## Phase 2.5：UI 修复批次 ✅ (2026-07-11)

> **版本**: v0.6.3 Alpha
> **范围**: 前端体验修复，覆盖 F5 / F6 / F10 / F11 四项；F8 / F9 标记 TODO 推入 Phase 7。本次批次还包含任务超时配置、智能自动滚动、多轮继续上下文保持等 UI/UX 改进，详见 `docs/History.md`。

| 编号 | 问题 | 修复文件 | 状态 |
|------|------|---------|------|
| F5 | idle 状态样式缺失 | `web/src/types/events.ts` + `App.vue` + `StatusIndicator.vue` | ✅ |
| F6 | ApprovalDialog 无错误状态 / 超时无通知 | `web/src/composables/useTaskStore.ts` + `ApprovalDialog.vue` + `App.vue` | ✅ |
| F10 | Step key 多 agent 冲突 | `web/src/components/AgentTree.vue` | ✅ |
| F11 | TypeWriter 频繁 DOM 操作防抖 | `web/src/components/TypeWriter.vue` | ✅ |
| — | 可配置任务超时（0 = 无限制） | `internal/harness/harness.go` + `cmd/server/main.go` + `cmd/server/api.go` + `web/src/components/TaskInput.vue` | ✅ |
| — | 一键展开/折叠全部 + 最新 step 自动展开 | `web/src/App.vue` + `web/src/components/AgentTree.vue` + `TurnItem.vue` + `TurnList.vue` | ✅ |
| — | 智能自动滚动（底部阈值 + 暂停提示 + Ctrl+End 恢复） | `web/src/App.vue` | ✅ |
| — | max_steps 失败后 Continue 保留 session 上下文 | `web/src/App.vue` + `web/src/composables/useTaskStore.ts` | ✅ |
| — | Step 索引 #{{ index }} 显示 | `web/src/components/AgentTree.vue` | ✅ |
| — | 默认最大步数 MaxSteps 10 → 30 | `internal/harness/harness.go` + `internal/runtime/engine.go` + `web/src/components/TaskInput.vue` | ✅ |
| — | 首次错误反馈给 AI / 连续两次相同错误才失败 | `internal/runtime/engine.go` | ✅ |
| — | 任务/会话耗时统计 (`duration_ms`) | `pkg/db/*` + `internal/runtime/*` + `cmd/server/*` + `web/src/components/MetricsPanel.vue` + `TurnItem.vue` / `AgentTree.vue` | ✅ |
| F8 | WS 重连不补事件 | `internal/ws/hub.go` + `cmd/server/api.go` + `web/src/composables/useWebSocket.ts` | ✅ |
| F9 | maxSteps 滑块与后端脱节 | `internal/config/config.go` + `cmd/server/main.go` + `web/src/components/TaskInput.vue` | ✅ |

### 验证
- `npx vue-tsc -b --noEmit` 通过
- `npx vite build` 通过

### 已知遗留（已全部解决或迁移）

| 编号 | 问题 | 说明 | 状态 |
|------|------|------|------|
| M4 | Auth GET 请求豁免敏感端点 | 已收紧：敏感 GET 端点纳入 auth | ✅ |
| M5 | 无 RBAC enforcement | 已接入 `RequireRole` / `auth_http.go` | ✅ |
| M6 | GET /api/auth/api-keys 无 token 可枚举 | 列表响应已脱敏 key_hash | ✅ |
| F8 | WS 重连不补事件 | 已交付 ring buffer + `/api/replay/events` + 前端重连拉取 | ✅ |
| F9 | maxSteps 滑块与后端脱节 | 已从前端拉取 `/api/contract-limits` 并限制滑块 | ✅ |

---

### 参考文档
- `doc/chapters/09-llm-api-comparison.html` — LLM 厂商 API 差异分析
- `doc/chapters/10-multi-model-layered-design.html` — 多模型分层设计
- `doc/chapters/11-harness-memory-design.html` — Harness 与自进化记忆设计
- `openspec/changes/phase-6-tech-debt-completion/` — Phase 6 变更产物

---

## Phase skill: 可复用 Skill 系统 ✅ 已完成 (2026-07-18)

**目标**: 为 Agent 提供可复用的 prompt + 任务知识包，让同一 Agent 在不切换配置时按启用 Skill 切换专长。

### 交付物
- [x] `internal/skill/` 核心模型：`SkillSource` / `SkillState` / `Skill` 及其 `Template` / `Parameter` / `Triggers`
- [x] 内存注册表 `Registry`：`List / Get / Set / Exists / Delete / Filter`
- [x] SQLite 持久化 `Store` 与 `Loader`：`built_in` 种子 + `local_db` 加载
- [x] `Renderer`：`{{ variable }}` 模板渲染与变量自动提取
- [x] 内置 Skill：`builtin-code-helper`、`builtin-error-diagnosis`，默认启用
- [x] Agent Skill 管理 Tools：`skill/create_local`、`skill/delete_local`、`skill/list`
- [x] Engine system prompt 注入：`system_prompt` / `task_prompt` 模板追加到 `## Skill Instructions`
- [x] Skill 相关事件常量：`skill_enabled`、`skill_disabled`、`skill_created`、`skill_deleted`
- [x] REST API：`GET /api/skills`、`GET /api/skills/search`、`POST /api/skills`、`PUT /api/skills/:id`、`DELETE /api/skills/:id`、`enable` / `disable`
- [x] 前端 SkillPicker：`TaskInput` 中输入 `/` 触发搜索，↑/↓ 选择、Enter 确认、Esc 取消
- [x] 前端启用流程：`App.vue` 解析 `/skill-id ` 前缀，调用 `POST /api/skills/{id}/enable` 后再发送真实输入
- [x] 单元测试：`internal/skill/*_test.go`、`internal/runtime/engine_skill_test.go`
- [x] API 端到端测试：`cmd/server/api_skill_test.go`
- [x] E2E 测试：`cmd/server/skill_e2e_test.go` 验证启用 skill 后 system prompt 正确注入

### 验证标准
- `go test ./internal/skill ./internal/runtime ./cmd/server -count=1` 通过
- `vue-tsc --noEmit` + `vite build` 通过
- Skill 启用/禁用前后，system prompt 长度与内容变化符合预期（E2E 覆盖）

### 已知限制 / 后续规划
- [ ] Skill 变量目前由调用方通过 `SkillVariables` 传入；未来可从 Project / Session 上下文自动推导
- [ ] 自动触发器（Triggers）已建模但未接入 Router / 调度器
- [ ] `local_file` 与 `mcp` 来源当前未实现完整加载器，留作 Phase 7 扩展

---

## Phase UI-v2: Observable Control Room 前端重设计 🚧 进行中 (2026-07-19)

**目标**: 在不破坏 `web/`（v1）的前提下，于 `web/v2/` 实现全新"可观测控制室"风格 UI，桌面三栏 Dock + 移动 3-tab，新老版本通过 `UI_VERSION` 环境变量运行时切换。

### 交付物
- [x] Go embed 双版本：`web/embed.go` 同时 embed `dist`（v1）与 `v2/dist`（v2）；`cmd/server/main.go` 按 `UI_VERSION=v2` 选择静态资源。
- [x] 设计系统：`global.css` deep-space dark + industrial 主题，CSS tokens；`responsive.css` 桌面/平板/移动适配。
- [x] 布局组件：`TopBar` / `DockPanel` / `SessionDock` / `CommandBar` / `MobileNav` / `TimelineTrack` / `AgentLane` / `StepCard` / `InspectorTabs` / `InspectorContent`。
- [x] Store composables 全量迁移适配（Task / Session / Agent / Project / Case / Memory / ContextWindow / Trace / Toast / RecentMods / ModelPrices / MCP / Keyboard / Layout / Skills）。
- [x] 后端 API 连线：WebSocket 事件、Skill 真实 API + `/skill-id ` 前缀解析、multi-agent、`startTaskWithCase`、会话/任务历史、审批对话框。
- [x] Inspector 全 tab 接入真实组件；Cases tab 接 `CaseDetailModal` + `CaseForm`（新建/编辑）。
- [x] 颜色 token 统一：Case 系列组件 + 布局组件 v1 硬编码颜色/hex fallback 全部迁移到 v2 CSS variables（新增 `--text-on-accent`）。
- [x] 构建验证：`web/` 与 `web/v2/` 均构建通过，`go build ./cmd/server` 通过。

### 待验证 / 待办
- [ ] 端到端冒烟：`UI_VERSION=v2` 启动后跑通 chat / case / skill / multi-agent，桌面与移动端均正常。
- [ ] Traces tab 升级为可折叠树。
- [ ] 用户验收满意后合并 `ui-v2-control-room` → `main`。

### 已知注意
- 工作分支 `ui-v2-control-room` 位于隔离 worktree，未合并 main 前不影响生产部署。
- 子 agent 并行失控教训已记录至 `memory/lead-subagent-parallel-control.md`，后续收尾改用单 agent 串行。

---

## Phase 7: 生产化与深度集成 🔜 规划中（暂不实施）

### 候选特性
- [ ] 在线 tokenizer（tiktoken / cl100k_base）替换当前字符启发式估算
- [ ] 后端 context window 压缩策略：按 token 预算自动截断/摘要历史 messages
- [x] `/api/tasks/:id/context_window` 历史快照查询端点（WS 实时事件 + REST 查询双通道）
- [x] F8 / F9 遗留修复（WS 重连补事件、maxSteps 滑块同步）
- [x] RBAC enforcement + Auth 敏感端点保护
- [x] **MCP 增强：SSE transport、远程市场安装、工具变更事件 `{mcp_tools_changed}`、按 agent 的 MCP 可见性**
  - 已交付（本批次）: stdio transport + Manager 生命周期 + DB 持久化 + `/api/mcp/servers` REST API + **MCP Marketplace Provider（static default market）+ 前端市场安装入口 + agent tools 白名单自动注入 `AllowedTools`**
- [x] **web_search fallback**: 未配置 Exa/Parallel 时自动降级到 DuckDuckGo HTML/lite 搜索，无需 API key
- [x] **Contract limits**: 后端 `CONTRACT_LIMIT_*` 配置 + `/api/contract-limits` + 请求校验 / clamping + 前端 TaskInput 消费 + `Scopes` 下拉校验

**目标**: 在 Phase 6 落地的 Auth（API key + RBAC 骨架）与 RAG（本地 TF-IDF + 内存向量库）之上，推进生产化、多用户、深度可观测与外部集成。延续 6-D/6-E 的"非空壳、真实运行"原则。MCP 已作为 Phase 6 扩展完成核心能力（stdio transport + Manager + DB + REST API），剩余增强项入 Phase 7。

**状态**: 仅规划，暂不实施。各子阶段可独立交付，实施前需为每个 7-X 子阶段新建 OpenSpec change。

### 7-A 身份与多用户体系
- [ ] JWT access/refresh token，与现有 API key 并存（API key 保留为程序化访问通道）
- [ ] OAuth2 第三方登录（GitHub / Google）
- [ ] Web UI 登录页 + 用户管理界面（Vue 路由守卫）
- [ ] 数据隔离: session / project / memory 按 `user_id` 隔离（DB 列 + 查询过滤）
- [ ] 配额管理: 每用户 token / 成本 / 并发任务上限，接入 PolicyGate
- [ ] RBAC 细化: 角色权限下沉到 Tool / 端点级

### 7-B 外部向量与 Embedding 集成
- [x] `EmbeddingProvider` 远程实现: OpenAI text-embedding-3 / Cohere（复用现有接口，无侵入）
- [ ] `VectorStore` 持久化后端: pgvector（保留 SQLite 兜底，Phase 7-E 再做迁移）
- [x] 混合检索: 向量召回 + BM25 关键词 + 重排（`HybridRanker` 替换 `blendVectorScores`）
- [x] 增量索引: `MemoryIndexer` + `PostInsertMemoryHook` 实时 upsert，替代启动全量 `BuildVectorIndex`
- [x] 语义去重: 新 memory 与已有记忆相似度阈值合并，控制记忆膨胀

### 7-C 深度可观测
- [x] OpenTelemetry trace: 跨 Agent / Tool / LLM 调用链路 span (dependency-free Tracer)
- [x] Prometheus 延迟直方图: `llm_latency_ms`, `tool_latency_ms` 添加至 `/metrics`
- [x] 审计日志: 写操作记录 actor / target / before / after，SQLite 持久化
- [x] 多 Agent trace 树可视化: 前端 `TraceTreePanel.vue`
- [x] 事件回放: `/api/replay/tasks/{task_id}` 从 steps + conversations 重建

### 7-D Harness 治理与合规
- [ ] 成本预算硬限制: 触发阈值自动暂停 + 告警（强化 CostBudgetRule）
- [ ] 审批工作流增强: 多级审批 / 超时升级 / 代理审批
- [ ] 合规快照: tool call 输入输出录制、文件变更 diff
- [ ] 数据保留策略: episodic memory TTL + 自动归档
- [ ] PII 脱敏: memory / 日志敏感信息自动打码

### 7-E 生产部署
- [ ] Docker Compose / K8s 部署清单
- [ ] Postgres 替换 SQLite（迁移工具，保留 SQLite 作单机兜底）
- [ ] CI/CD: GitHub Actions build / test / vet / lint
- [ ] 备份恢复: DB 定时备份 + 向量库快照
- [ ] HA: 多实例 + 共享存储（可延后至 Phase 8）

### 依赖与优先级
- **7-A → 7-D**: 合规治理依赖多用户身份（actor 归属）
- **7-B 独立**: 可先行，纯增强 RAG，无破坏性
- **7-C 独立**: 可并行，与业务逻辑解耦
- **7-E 最后**: 依赖前四项稳定

**建议实施顺序**: 7-B / 7-C（并行）→ 7-A → 7-D → 7-E

---

## Phase 7-H2: Multi-Agent 编排遗留闭环 🚧 进行中（2026-07-21 起）

> **背景**: `scripts/multi-agent-smoke.sh` 与 `scripts/real-llm-smoke.sh` 长期记录的"已知后端 bug，无人修复"清单。本次集中闭环 multi-agent 编排层的结构性缺陷，使其从"一次性 fan-out 原型"升级为可观测、可跑通的 leader-driven 编排。
> **范围**: 仅 multi-agent 编排链路 + Tracer 事件流 + 子任务可观测回填。不涉及 7-A/7-D 的多用户与合规。
> **原则**: 延续 6-D/6-E 的"非空壳、真实运行"——每个子阶段必须 smoke 脚本验证通过才算交付。

### 根因（详见 memory `multi-agent-dual-entry-placeholder-bug`）

| 编号 | 问题 | 定位 | 影响 |
|------|------|------|------|
| MA1 | **dispatch_sub_agent 占位符硬编码** `"<leaderSubTaskID>"` | `internal/tool/builtin.go:752` | leader-driven 链路从未跑通；子任务 parent_task_id 挂在假 ID，前端 `QueryChildTasks` 永远空 |
| MA2 | **双入口语义分裂**：前端只接 `/api/multi-agent`（静态 decomposer + RunBlocking），leader-driven 入口（`/api/tasks` action=multi-agent）是死代码 | `cmd/server/main.go:1474` vs `:956` | 用户永远用到的是空壳静态编排，看不到 Leader 思考/派发 |
| MA3 | **Tracer 不广播事件**：`Tracer.push` 只入内存缓冲，无 `SendEvent(trace_span)` | `internal/observability/trace.go:147` | 前端 Traces 面板永远空；multi-agent 路径根本没接 Tracer |
| MA4 | **orchestrator 未接入 Tracer**：`runAgent` 创建 Engine 时 `Tracer`/`RootTraceCtx` 为 nil | `internal/orchestrator/orchestrator.go:387-422` | multi-agent 下零 span |
| MA5 | **child_tasks 不返回 steps**：`/api/tasks?id=root` 的 child_tasks 无 steps 字段 + 前端只建空占位 | `cmd/server/api.go` + `web/v2/src/composables/useTaskStore.ts:1027` | 子 agent lane 永远 "No steps yet" |
| MA6 | **root task 是空壳**：静态编排 root 无 engine loop，`final_result="all agents completed"` | `internal/orchestrator/orchestrator.go:265` | 编排层无可观测 step，违背白盒哲学 |
| MA7 | **AgentBus worker 跨 session 串台**：worker 用 `RegisterHandler(agentID)` 不带 SubTaskID | `internal/runtime/engine.go:703` | 两 session 同名 worker 覆盖 handler（边缘场景） |
| MA8 | **Router 死代码**：`runAgentLoopWithTurn` 未设 `Router/Registry/Providers`，`e.cfg.Router != nil` 永远 false | `cmd/server/main.go` chat 路径 EngineConfig | Phase 6 动态模型选择在 chat 路径未生效（`real-llm-smoke.sh:489` 标注） |
| MA9 | **root task 状态汇聚靠轮询**：多 agent 并行终态由最后一个 agent 决定 | `internal/orchestrator/orchestrator.go:253-266` | `multi-agent-smoke.sh` 第 8 项"仍存" |

### 实施阶段

#### 阶段 1 — 修通 leader-driven 主链路（MA1 + MA2）
- [x] `internal/tool/registry.go`：新增 `Clone()` 方法，支持基于 base registry 创建带独立 tools map / order slice 的浅拷贝
- [x] `internal/tool/builtin.go`：
  - 新增 `NewLeaderTools(dispatcher, leaderSubTaskID, resolveApproval)`，把 dispatch_sub_agent / approve_sub_agent_action / reject_sub_agent_action 三个 leader 专用工具按真实 taskID 构造
  - `NewDispatchSubAgentTool` 直接持有 `leaderSubTaskID string`，调用 `dispatcher.Dispatch` 时传入真实 root task ID，替代原 `"<leaderSubTaskID>"` 占位符
  - 移除旧的 `RegisterBuiltinsWithDispatcher*` 与 `canDispatchFn` 全局权限模式
- [x] `cmd/server/main.go`：
  - base `toolRegistry` 仅调用 `tool.RegisterBuiltins`（不含 leader 工具）
  - `runAgentLoopWithTurn` 中 root leader 从 base registry `Clone()` 并注册 `NewLeaderTools`，taskID 直接注入
  - 删除 `leaderDispatchEnabled atomic.Bool` 及其 Store(true/false)，消灭全局单 leader 竞态；dispatch 权限由"leader registry 是否含 dispatch_sub_agent"天然控制
  - chat 与 multi-agent 统一走 `/api/tasks` leader-driven 入口
- [x] `web/v2/src/composables/useTaskStore.ts`：`startMultiAgentTask` 改 POST `/api/tasks` with `action:"multi-agent"`；前端为返回的 `agent_ids` 预创建 leader lane
- [x] `web/v2/src/App.vue`：multi-agent 在已有 session 上触发时仍调用 `startMultiAgentTask`（不以 turn 方式追加），保证 leader 每次都从 root 开始
- [x] `internal/orchestrator/decomposer.go`：新增 `StringSlice` 类型兼容 LLM 把 `output_to` 输出为单个字符串或字符串数组的两种情况，修复真实 LLM 下 LLMDecomposer parse failed 退回到规则分解器的问题
- [x] 保留 `/api/multi-agent` 入口作"显式 agents 静态编排"兼容路径（已保留并通过 `scripts/multi-agent-smoke.sh` / `scripts/real-llm-smoke.sh` 回归）
- [x] smoke 验证：`scripts/multi-agent-smoke.sh` 全 PASS (12/0/0)；`scripts/real-llm-smoke.sh` 场景 3 在 18s 内完成，status=completed，agents=2

#### 阶段 2 — Tracer 接入事件流（MA3 + MA4）
- [x] `internal/observability/trace.go`：`Tracer` 增加 `OnSpan func(SpanRecord)` 字段与 `SetOnSpan` 方法；`push()` 末尾异步触发回调；`StartChild(parent, agentID, operation)` 改为显式传入 agentID，避免 worker span 错误继承父级 agentID
- [x] `internal/runtime/engine.go`: `EngineConfig.Tracer` 接口同步更新 `StartChild` 签名；`think()` 调用时传入 `e.cfg.AgentID`
- [x] `cmd/server/main.go`：`init()` 注册 `tracer.SetOnSpan` 回调 → `hub.SendEvent(event.NewEventWithSubTask(EventTraceSpan, ...))`；新增 `hubInstance` 包级引用；新增 `spanRecordToMap` 序列化函数
- [x] `internal/orchestrator/orchestrator.go`：`Orchestrator` 加 tracer 字段 + `SetTracer`；`RunBlocking` 开头 `StartRoot(rootTaskID,"orchestrate")` 并透传给子 agent `EngineConfig.Tracer/RootTraceCtx`
- [ ] engine.go 补 tool / llm 子 span 埋点（可选，当前 think span 已覆盖主要调用）
- [ ] smoke 验证：chat + multi-agent 都能在 Traces tab 看到 span 树，`agent_id` 列非空

#### 阶段 3 — child steps 回填（MA5）
- [x] `cmd/server/api.go` `handleGetTask`：新增 `ChildTaskDetail` 包装类型，`child_tasks` 每个 child 附带其 `steps`（按 child id 查询）
- [x] `web/v2/src/composables/useTaskStore.ts` `loadTask`：后端 child_task 类型声明增加 `steps` 字段；新增 `persistedStepToStep` 转换函数；处理 child_tasks 时如果对应 agent lane 不存在则新建并把 child steps 填进去
- [x] smoke 验证：`scripts/multi-agent-smoke.sh` 12/0/0；`scripts/real-llm-smoke.sh` 14/0/3，场景 3 在 71s 完成（真实 LLM researcher 耗时较长），status=completed

#### 阶段 4 — 编排层可观测（MA6 + MA9）
- [ ] `internal/orchestrator/orchestrator.go`：发编排层 step 事件（`decompose_done` / `agent_dispatched` / `agent_completed`），挂在 root task、`agent_id="orchestrator"`
- [ ] root `final_result` 从 `"all agents completed"` 改为各 worker 结果聚合摘要
- [ ] root task 终态由 `RunBlocking` 汇聚显式 `UpdateTask`，不再依赖轮询（MA9）
- [ ] smoke 验证：前端出现 orchestrator 编排 lane，能看到拆分决策与派发过程

#### 阶段 5（长期）— workflow DAG 表达力
- [ ] decomposer 输出从扁平 `[]AgentSpec` 升级为带依赖/条件的 DAG
- [ ] `RunBlocking` 按 DAG 调度（A 完成且满足条件才跑 C）
- [ ] Leader 多轮 dispatch 的 observation 格式标准化

#### 阶段 6（低优先级）— AgentBus 隔离 + Router 死代码（MA7 + MA8）
- [ ] `internal/runtime/engine.go:703`：worker 改 `RegisterHandlerBySubTask`
- [ ] `cmd/server/main.go`：chat 路径 EngineConfig 补 `Router/Registry/Providers`（阶段 1 已为 leader registry clone；MA8 需单独验证 chat path model_routed 事件）
- [ ] 验证 `/api/multi-agent` 静态编排兼容路径在 leader-driven 默认入口切换后仍可回归
- [ ] smoke 验证：两 session 同名 worker 不串台；chat 路径触发 `model_routed` 事件

### 依赖与执行

```
阶段1 (leader 主链路) ──→ 阶段3 (child steps) ──→ 阶段4 (编排可观测)
        │
阶段2 (Tracer) ──────────────────────────────→
                                                 阶段5 (DAG) → 阶段6 (隔离+Router)
```

- 阶段 1、2 独立可并行；阶段 3 依赖 1；阶段 4 依赖 1+3；阶段 5 大改单独排；阶段 6 随时可做
- 子 agent 串行执行（见 memory `subagent-serial-execution`），阶段 1/2 可派发但禁止并行改同一批文件

### 验证基准

- [x] `bash scripts/multi-agent-smoke.sh` 全 PASS，FINDINGS 清单第 5/8/9 项闭环
- [x] `bash scripts/real-llm-smoke.sh`（LLM_USE_MOCK=false）场景 3 不再 timeout，`status=completed` 与 `agent_count=2` 一致（18s 完成）
- [ ] 前端 `UI_VERSION=v2` 跑通 multi-agent：leader lane + worker lanes + Traces 面板均有数据

---

## 版本历史

| 版本 | 日期 | 变更 |
|------|------|------|
| v0.1 | 2026-07-03 | Phase 0 完成，初始骨架搭建 |
| v0.2 | 2026-07-03 | Phase 1 完成，Agent Loop 核心引擎 + e2e 测试工具 |
| v0.3 | 2026-07-03 | Phase 2 完成，Vite + TS 前端迁移 + Embed 集成 |
| v0.4 | 2026-07-03 | Phase 3 完成，Harness 基础 + 预设 Cases + CaseCard UI |
| v0.4 Alpha | 2026-07-05 | Phase 4 完成，多 Agent 并发 + Harness 控制层 + 前端体验优化 |
| v0.5 | 2026-07-06 | Phase 5 完成: Session 管理 + Provider + Router + 工具注册 + Harness 审批 + Memory 四层 + Docker 沙箱 + AgentBus + Checkpoint |
| v0.5 Alpha | 2026-07-07 | Phase 5-A 完成: Project 管理 + 多轮对话 + session_messages 持久化 + TurnList 时间线组件 |
| v0.6 Alpha | 2026-07-08 | Phase 6 完成: 6-C 技术债务修复 + 6-D 可观测性/成本持久化真实落地 |
| v0.6.1 Alpha | 2026-07-08 | Phase 6-E 完成: Auth 中间件实际生效 + RAG 本地向量召回接入 MemoryRecall |
| v0.6.4 Alpha | 2026-07-11 | 可配置任务超时、Memory overlay、展开/折叠/智能滚动、Continue 上下文保留、step 索引、错误反馈优先策略 |
| v0.6.5 Alpha | 2026-07-15 | Phase 6-F 完成: memory 类型体系 + CRUD API + LLM 摘要 + 向量持久化 + 前端可观测性 |
| v0.7.0 Alpha | 2026-07-15 | Case Management 增强: 自定义 Case CRUD + Tag/Category 筛选 + 内置 Case 自动种子 + LLM Judge 评估 + `task_evaluated` 事件 + 前端任务库 |
| v0.7.1 Alpha | 2026-07-18 | 扩展工具注册表: namespace/tag 身份体系 + 新增 core/list_dir、core/apply_diff、core/delete_file、core/fetch_url、core/parse_json、core/execute_program + mcp/web_search 占位 |
| v0.7.2 Alpha | 2026-07-18 | MCP 支持落地: `internal/tool/mcp` JSON-RPC client + stdio transport + Manager 生命周期 + `mcp_servers` DB 持久化 + `/api/mcp/servers` REST API + time/calc 示例 + MCP 市场 Provider（default static market）+ 前端市场安装入口 |
| v0.7.3 Alpha | 2026-07-18 | MCP SSE transport: `internal/tool/mcp/sse_transport.go` + endpoint handshake + JSON-RPC over SSE + Manager/REST/前端 create dialog 已支持 `sse` transport |
| v0.7.4 Alpha | 2026-07-18 | MCP 远程 marketplace: 新增 `URLProvider` 从 HTTP URL 拉取 JSON catalog + `MCP_MARKETS` 环境变量注册 + Manager 自动加载远程市场 + 示例与测试 |
| v0.7.5 Alpha | 2026-07-18 | DuckDuckGo fallback: core/web_search 无 API key 时自动降级到 DuckDuckGo HTML/lite 搜索 |
| v0.7.6 Alpha | 2026-07-19 | Phase 7 遗留闭环: WS 重连补事件、RBAC enforcement、API keys 脱敏、maxSteps 滑块同步、MCP 按 agent 可见性、contract limits 校验与前端消费 |
| v0.8.0 Alpha | 2026-07-19 | Phase skill 完成: 可复用 Skill 系统（模型/注册表/持久化/加载器/Renderer/内置 Skill/Agent Tools/Engine 注入/REST API/前端 SkillPicker/E2E 测试）落地 |
| v0.9.0 Alpha | 2026-07-19 | Phase UI-v2 进行中: Observable Control Room 新前端（`web/v2/`）骨架 + 核心连线 + 颜色 token 统一 + Go embed 双版本运行时切换（`UI_VERSION=v2`）；待端到端冒烟验证后合并 main |
| v0.9.1 Alpha | 2026-07-21 | Phase 7-H2 启动: multi-agent 编排遗留闭环规划（MA1-MA9，dispatch_sub_agent 占位符 bug + Tracer 事件流 + child steps 回填），见 ROADMAP "Phase 7-H2" 章节 |
| v0.9.2 Alpha | 2026-07-21 | Phase 7-H2 阶段 1: leader-driven 主链路重构落地 — Registry.Clone + per-leader registry + 删除 leaderDispatchEnabled 全局竞态，前端 multi-agent 入口切到 /api/tasks action=multi-agent |
| v0.9.3 Alpha | 2026-07-21 | Phase 7-H2 阶段 2: Tracer 接入事件流 + decomposer output_to 字符串/数组兼容修复；`scripts/multi-agent-smoke.sh` (12/0/0) 与 `scripts/real-llm-smoke.sh` (14/0/3) 验证通过 |
| v0.9.4 Alpha | 2026-07-21 | Phase 7-H2 阶段 3: `handleGetTask` 返回 child_tasks.steps + 前端 `loadTask` 回填 worker lane；新增 `TestHandleGetTaskChildSteps` 单测；smoke 验证同 v0.9.3 |