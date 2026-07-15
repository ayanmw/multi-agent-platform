# Multi-Agent Platform — Product Roadmap

> **Last updated**: 2026-07-11
> **Current version**: v0.6.4 Alpha (Phase 6 收尾/UI 体验修复批次)
> **Update rule**: 每个 Phase 任务完成后，必须更新本文件并提交 Git。

---

## 路线图总览

```
Phase 0 ✅ → Phase 1 ✅ → Phase 2 ✅ → Phase 3 ✅ → Phase 4 ✅ → Phase 5 ✅ → Phase 6 ✅ (Skeleton)
  (骨架)      (Agent)     (UI)       (Cases)    (并发)      (注册)      (高级)
```

---

## Phase 0: 项目骨架 + 通信验证 ✅ COMPLETED

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

## Phase 1: Agent Loop 核心引擎 ✅ COMPLETED

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

## Phase 2: 前端可视化 ✅ COMPLETED

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

## Phase 3: 预设 Cases + 配置页面 + Harness 基础 ✅ COMPLETED

**目标**: 提供一键式任务和 Agent 配置管理，引入 Harness 基础组件

**完成日期**: 2026-07-03
**Git commit**: `455b047`

### 交付物
- [x] 5 个预设 Task Cases（代码生成、研究、多Agent、对话、长任务）
- [x] CaseCard UI 组件 + Run 按钮
- [x] **Harness: TaskContract 定义**（目标、范围、验收标准、预算、权限）
- [x] **Harness: Progress 文件管理**（TaskProgress 类型 + 关键节点自动写入）
- [x] **Harness: FileScopeRule + PathTraversalRule**（路径安全，在 write_file 之前拦截）
- [x] **Harness: AcceptanceCriteria 基础实现**（test_pass / file_exists / shell_exit_zero）
- [x] **Harness: PolicyGate 集成到 Engine**（executeTool 经过 PolicyGate 拦截）
- [x] `/api/cases` 端点（列出所有预设 Cases）
- [ ] Agent 配置 CRUD 前端页面 → Phase 4（与多模型配置页面合并）
- [ ] 任务历史侧边栏 → Phase 4（与多 Agent 并发时一起实现回放）
- [ ] Memory: Task 完成时自动生成摘要 → Phase 4（与记忆系统合并）

### 验证标准
- 点击 Case 卡片 → 任务自动执行 → 历史可回放
- TaskContract 的预算/权限在运行时被强制执行

---

## Phase 4: 多 Agent 并发 + Harness 控制层 + 记忆基础 ✅ COMPLETED

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

### Phase 5-A: Project 管理 + 多轮对话 ✅ COMPLETED (2026-07-07)
- [x] **Project CRUD API**（`/api/projects` + `/api/projects/:id`）
- [x] **Project 管理前端**（useProjectStore + 侧边栏 Project 分组 + ProjectConfig 组件）
- [x] **session_messages 表**（Session 级消息持久化，Engine 通过 SessionMessageWriter 同步写入）
- [x] **多轮对话 API**（`POST /api/sessions/:id/chat` + `GET /api/sessions/:id/messages`）
- [x] **多轮对话上下文注入**（新 Task 启动时自动注入历史 messages + Working Memory）
- [x] **前端多轮时间线**（TurnList + TurnItem 组件，展开/折叠，时间线展示）
- [x] **Session 内继续聊天**（COMPLETED/FAILED 的 Session 也可继续发送消息）
- [x] **任务层级架构**（root → turn_2, turn_3 (siblings) → child_of_turn_2 (children)）
- [x] **DB 迁移 v5-v8**（projects 表 + session_messages 表 + sessions/tasks/memories 新增字段）

### Phase 5-B: 上下文压缩 + 记忆作用域（优化）✅ COMPLETED (2026-07-07)
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

-新增 `sessions` 表：`id`, `name`, `root_task_id`, `status`, `user_input`, `created_at`, `updated_at`
- `tasks` 表新增：`session_id`, `parent_task_id`, `is_root`
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

## Phase 6: 高级特性 ✅ COMPLETED

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
| F8 | WS 重连不补事件 | `web/src/composables/useWebSocket.ts`（标记 TODO） | ⏳ Phase 7 |
| F9 | maxSteps 滑块与后端脱节 | `web/src/components/TaskInput.vue`（标记 TODO） | ⏳ Phase 7 |

### 验证
- `npx vue-tsc -b --noEmit` 通过
- `npx vite build` 通过（82 modules transformed）

### 已知遗留（推入 Phase 7 安全加固）

| 编号 | 问题 | 说明 |
|------|------|------|
| M4 | Auth GET 请求豁免敏感端点 | 需团队讨论安全策略 |
| M5 | 无 RBAC enforcement | Role 结构已搭好，enforcement 需设计路由规则映射 |
| M6 | GET /api/auth/api-keys 无 token 可枚举 | 依赖 M5 RBAC |
| F8 | WS 重连不补事件 | 需要后端 replay 端点或前端 onopen reload task |
| F9 | maxSteps 滑块与后端脱节 | 需要从后端读取 max_steps 合理范围 |

---

### 参考文档
- `doc/chapters/09-llm-api-comparison.html` — LLM 厂商 API 差异分析
- `doc/chapters/10-multi-model-layered-design.html` — 多模型分层设计
- `doc/chapters/11-harness-memory-design.html` — Harness 与自进化记忆设计
- `openspec/changes/phase-6-tech-debt-completion/` — Phase 6 变更产物

---

## Phase 7: 生产化与深度集成 🔜 PLANNING (暂不实施)

**目标**: 在 Phase 6 落地的 Auth（API key + RBAC 骨架）与 RAG（本地 TF-IDF + 内存向量库）之上，推进生产化、多用户、深度可观测与外部集成。延续 6-D/6-E 的"非空壳、真实运行"原则。

**状态**: 仅规划，暂不实施。各子阶段可独立交付，实施前需为每个 7-X 子阶段新建 OpenSpec change。

### 7-A 身份与多用户体系
- [ ] JWT access/refresh token，与现有 API key 并存（API key 保留为程序化访问通道）
- [ ] OAuth2 第三方登录（GitHub / Google）
- [ ] Web UI 登录页 + 用户管理界面（Vue 路由守卫）
- [ ] 数据隔离: session / project / memory 按 `user_id` 隔离（DB 列 + 查询过滤）
- [ ] 配额管理: 每用户 token / 成本 / 并发任务上限，接入 PolicyGate
- [ ] RBAC 细化: 角色权限下沉到 Tool / 端点级

### 7-B 外部向量与 Embedding 集成
- [ ] `EmbeddingProvider` 远程实现: OpenAI text-embedding-3 / Cohere（复用现有接口，无侵入）
- [ ] `VectorStore` 持久化后端: pgvector（推荐，配合 SQLite→Postgres 迁移）或 ChromaDB
- [ ] 混合检索: 向量召回 + BM25 关键词 + 重排（替换当前 `blendVectorScores` 的线性混合）
- [ ] 增量索引: memory 写入时实时 upsert，替代启动全量 `BuildVectorIndex`
- [ ] 语义去重: 新 memory 与已有记忆相似度阈值合并，控制记忆膨胀

### 7-C 深度可观测
- [ ] OpenTelemetry trace: 跨 Agent / Tool / LLM 调用链路 span
- [ ] Prometheus SDK 替换手写 metrics，增加延迟直方图
- [ ] 审计日志: 所有写操作记录 actor / target / before / after
- [ ] 多 Agent 协作 trace 树可视化（前端复用 AgentTree）
- [ ] 事件回放: 基于 SQLite 事件流重建任务执行过程

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
