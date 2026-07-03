# Multi-Agent Platform — Product Roadmap

> **Last updated**: 2026-07-03
> **Current version**: v0.3 Alpha (Phase 2 complete)
> **Update rule**: 每个 Phase 任务完成后，必须更新本文件并提交 Git。

---

## 路线图总览

```
Phase 0 ✅ → Phase 1 ✅ → Phase 2 ✅ → Phase 3 🔜 → Phase 4 → Phase 5 → Phase 6
  (骨架)      (Agent)     (UI)       (Cases)    (并发)    (注册)    (高级)
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
- [ ] Agent CRUD 前端页面 → Phase 3（配置页面与 Agent CRUD 合并实现）
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

## Phase 3: 预设 Cases + 配置页面 + Harness 基础

**目标**: 提供一键式任务和 Agent 配置管理，引入 Harness 基础组件

### 交付物
- [ ] 5 个预设 Task Cases（代码生成、研究、多Agent、对话、长任务）
- [ ] CaseCard UI 组件 + Run 按钮
- [ ] Agent 配置 CRUD 页面（REST API + 前端表单）
- [ ] 任务历史侧边栏（SQLite 读取 + 回放）
- [ ] **Harness: TaskContract 定义**（目标、范围、验收标准、预算、权限）
- [ ] **Harness: Progress 文件管理**（TaskProgress 类型 + 关键节点自动写入）
- [ ] **Harness: FileScopeRule + PathTraversalRule**（路径安全，在 write_file 之前拦截）
- [ ] **Harness: AcceptanceCriteria 基础实现**（test_pass / file_exists / shell_exit_zero）
- [ ] **Memory: Task 完成时自动生成摘要**（Engine 调用轻量模型做单 task 总结）

### 验证标准
- 点击 Case 卡片 → 任务自动执行 → 历史可回放
- TaskContract 的预算/权限在运行时被强制执行

---

## Phase 4: 多 Agent 并发 + Harness 控制层 + 记忆基础

**目标**: 支持多个 Agent 并行执行，引入 Policy Gate 和记忆系统

### 交付物
- [ ] 多 Agent Task 分派（goroutine 并行）
- [ ] 前端多树渲染（并排或选项卡，颜色区分）
- [ ] Agent 间通信协议（Agent A 调用 Agent B 的接口）
- [ ] **多模型分层基础**: `ModelProfile` 类型 + `ModelRegistry` 注册表
- [ ] **Agent 模型绑定**: 创建 Agent 时可选指定模型（从 Registry 中选择）
- [ ] **多模型配置加载**: 从 `.env` / DB 加载多个模型配置
- [ ] **Harness: PolicyChain 完整实现**（PolicyGate + PolicyChain + 内置规则链）
- [ ] **Harness: TokenBudgetRule**（累计 token 超过 TaskContract 预算时硬拒绝）
- [ ] **Harness: ToolWhitelistRule**（只允许 TaskContract 中声明的工具）
- [ ] **Harness: Checkpoint / Recovery**（CheckpointManager + 崩溃恢复流程）
- [ ] **Memory: `memories` 表 + `memory_links` 表** Schema（pkg/db/database.go）
- [ ] **Memory: Heartbeat 后台整理器**（定时扫描新 conversation → 触发抽取管线）
- [ ] **Memory: Candidate → Semantic 晋升管线**（三条晋升通道的代码实现）

### 验证标准
- 一个任务拆成 2 个 Agent 并行，前端同时看到两棵树更新
- 不同 Agent 使用不同模型（如一个用 deepseek-flash，一个用 deepseek-pro）
- 工具调用超过 TokenBudget 时被 PolicyGate 拦截，Engine 收到 ErrBlockedByPolicy
- 进程崩溃后可从 checkpoint 恢复，不从头开始
- 心跳定时触发记忆抽取，Semantic 规则有明确的 promotion_reason

---

## Phase 5: 运行时注册 + Provider + Router + 记忆召回

**目标**: 支持动态注册工具和 Agent，引入 Provider 抽象、Router 路由和记忆召回

### 交付物
- [ ] 运行时 Tool 注册 REST API
- [ ] AI 自描述工具注册（LLM 生成 JSON Schema → 自动注册）
- [ ] Docker 沙箱（run_shell 安全隔离）
- [ ] **LLM Provider 接口抽象**: `Provider` 接口 + `OpenAIProvider` 基线实现
- [ ] **Router 路由决策**: 意图分类 + 模型选择（轻量模型做路由，成本 < $0.001/次）
- [ ] **模型能力矩阵**: 标注各模型的 tool_calling / streaming / vision / reasoning 能力
- [ ] **Harness: ApprovalRule**（高风险操作通过 WebSocket 发送确认请求到前端）
- [ ] **Harness: DangerousCommandRule**（Shell 命令危险模式检测）
- [ ] **Memory: MemoryRecall 召回**（新任务启动时构建 Working Memory）
- [ ] **Memory: 记忆冲突检测 + 合并**（同义规则合并，冲突规则标记）

### 验证标准
- 无需重启服务，通过 API 注册新工具并立即使用
- 同一任务请求根据意图自动路由到不同模型（简单→Flash，复杂→Pro）
- 高风险操作（如 git push）触发前端审批弹窗
- 新任务启动时，Semantic 规则和相关 Episode 写入 Working Memory 注入 System Prompt

---

## Phase 6: 高级特性（远期）

**目标**: 生产级特性 — 多厂商 LLM、成本控制、安全合规、记忆治理

### 交付物
- [ ] RAG 向量检索（Embedding + 向量数据库）
- [ ] 用户认证 / 多租户（OAuth2 / JWT）
- [ ] gRPC 协议迁移（EventBus 接口切换）
- [ ] 可观测性（OpenTelemetry + Prometheus）
- [ ] Prompt Template 引擎
- [ ] API Key 加密存储
- [ ] **多厂商 LLM Provider**: Anthropic Provider + DeepSeek Provider（reasoning_content 支持）
- [ ] **Worker Pool 并发调度**: 多 Agent 并发管理 + 模型分配 + 限流控制
- [ ] **CostTracker 成本追踪**: 按模型/分层/任务维度的 API 成本报告
- [ ] **降级策略**: 主模型不可用时自动 fallback（Opus→Sonnet→DeepSeek→本地）
- [ ] **模型能力矩阵完整实现**: 根据任务所需能力自动筛选可用模型
- [ ] **Harness: Eval 回归闭环**（失败 Trace → 回归测试集 → 自动化回归）
- [ ] **Harness: 完整治理与审计**（身份 + 审批 + 成本 + 合规日志）
- [ ] **Memory: 向量检索增强**（LanceDB / ChromaDB 语义召回）
- [ ] **Memory: 遗忘曲线 + 冷存储**（超过 30 天未 access → status=cold）
- [ ] **Memory: 记忆审查 Agent**（定期扫描 Semantic 层，标记过期/冲突规则）

### 参考文档
- `doc/chapters/09-llm-api-comparison.html` — LLM 厂商 API 差异分析
- `doc/chapters/10-multi-model-layered-design.html` — 多模型分层设计
- `doc/chapters/11-harness-memory-design.html` — Harness 与自进化记忆设计

---

## 版本历史

| 版本 | 日期 | 变更 |
|------|------|------|
| v0.1 | 2026-07-03 | Phase 0 完成，初始骨架搭建 |
| v0.2 | 2026-07-03 | Phase 1 完成，Agent Loop 核心引擎 + e2e 测试工具 |
| v0.3 | 2026-07-03 | Phase 2 完成，Vite + TS 前端迁移 + Embed 集成 |