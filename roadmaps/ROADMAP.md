# Multi-Agent Platform — Product Roadmap

> **Last updated**: 2026-07-03
> **Current version**: v0.2 Alpha (Phase 1 complete)
> **Update rule**: 每个 Phase 任务完成后，必须更新本文件并提交 Git。

---

## 路线图总览

```
Phase 0 ✅ → Phase 1 ✅ → Phase 2 🔜 → Phase 3 → Phase 4 → Phase 5 → Phase 6
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
- [ ] Agent CRUD 前端页面（Phase 2 随 Vue 迁移实现）
- [ ] `llm_delta` 批量发送（当前每 token 一条 WS 消息，Phase 2 加节流）
- [ ] Conversation 历史回读用于多轮对话（Phase 3+ Session 管理）

---

## Phase 2: 前端可视化 🔜

**目标**: 实现 Agent 执行过程的完整可视化

### 交付物
- [ ] AgentTree 组件（递归树 + 实时更新）
- [ ] TypeWriter 组件（LLMDelta 流式渲染 + 打字机效果）
- [ ] Markdown 实时渲染 + 代码语法高亮（marked + highlight.js）
- [ ] Step 展开/折叠 + 状态指示器（running/completed/failed 图标）
- [ ] Pause / Resume / Cancel 控制按钮
- [ ] 指标面板（token 消耗、耗时统计）

### 验证标准
- 运行一个复杂任务，能在前端看到每一步的完整可视化

---

## Phase 3: 预设 Cases + 配置页面

**目标**: 提供一键式任务和 Agent 配置管理

### 交付物
- [ ] 5 个预设 Task Cases（代码生成、研究、多Agent、对话、长任务）
- [ ] CaseCard UI 组件 + Run 按钮
- [ ] Agent 配置 CRUD 页面（REST API + 前端表单）
- [ ] 任务历史侧边栏（SQLite 读取 + 回放）

### 验证标准
- 点击 Case 卡片 → 任务自动执行 → 历史可回放

---

## Phase 4: 多 Agent 并发

**目标**: 支持多个 Agent 并行执行

### 交付物
- [ ] 多 Agent Task 分派（goroutine 并行）
- [ ] 前端多树渲染（并排或选项卡，颜色区分）
- [ ] Agent 间通信协议（Agent A 调用 Agent B 的接口）

### 验证标准
- 一个任务拆成 2 个 Agent 并行，前端同时看到两棵树更新

---

## Phase 5: 运行时注册 + 扩展

**目标**: 支持动态注册工具和 Agent

### 交付物
- [ ] 运行时 Tool 注册 REST API
- [ ] AI 自描述工具注册（LLM 生成 JSON Schema → 自动注册）
- [ ] Docker 沙箱（run_shell 安全隔离）

### 验证标准
- 无需重启服务，通过 API 注册新工具并立即使用

---

## Phase 6: 高级特性（远期）

**目标**: 生产级特性

### 交付物
- [ ] RAG 向量检索（Embedding + 向量数据库）
- [ ] 用户认证 / 多租户（OAuth2 / JWT）
- [ ] gRPC 协议迁移（EventBus 接口切换）
- [ ] 可观测性（OpenTelemetry + Prometheus）
- [ ] Prompt Template 引擎
- [ ] API Key 加密存储

---

## 版本历史

| 版本 | 日期 | 变更 |
|------|------|------|
| v0.1 | 2026-07-03 | Phase 0 完成，初始骨架搭建 |
| v0.2 | 2026-07-03 | Phase 1 完成，Agent Loop 核心引擎 + e2e 测试工具 |