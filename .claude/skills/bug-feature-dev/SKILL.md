---
name: bug-feature-dev
description: |
  Multi-Agent Platform 项目的 Bug 修复与功能开发工作流。当用户报告 bug、提出功能需求、
  或说"修复xxx"、"新增xxx功能"、"优化xxx"、"加一个xxx"时触发此 Skill。
  也适用于 "帮我实现"、"开发"、"改进" 等开发类请求。
  覆盖从需求分析→方法论选择→ROADMAP更新→API设计→子系统分工实现→验证的完整流程。
---

# Bug / Feature 工作流

本 Skill 定义了 Multi-Agent Platform 项目的标准开发流程。每个 bug 或功能点都遵循以下步骤。

---

## 技术栈速查

| 层 | 技术 | 关键目录 |
|----|------|---------|
| 后端 | Go 1.25 | `cmd/server/*_api.go`, `internal/`, `pkg/` |
| 路由入口 | `cmd/server/server.go:registerRoutes` | 方法值注册，不是 `main.go` 内联 `http.HandleFunc` |
| 启动入口 | `cmd/server/runner.go` | `AgentRunner.Run(ctx, AgentRunSpec{...})` 是唯一统一入口 |
| 数据库 | SQLite (modernc.org/sqlite) | `pkg/db/database.go`, `pkg/db/migrate.go`, 子系统 `pkg/db/{tool,workspace,cron}.go` |
| 迁移 | `pkg/db/migrate.go` — 自动增量迁移 | 新增字段追加 Migration 条目，不修改已有 Migration |
| 前端 v1 | Vue 3 + Vite + TypeScript | `web/src/` |
| 前端 v2（控制室，默认根路径） | Vue 3 + Vite + TypeScript | `web/v2/src/` |
| 通信 | WebSocket (gorilla/websocket) | `internal/ws/hub.go` |
| 事件 | `pkg/event/event.go` | 统一 `AgentEvent` JSON；新事件优先只 WS 广播，不写 task steps |
| LLM | OpenAI-compatible API | `internal/llm/client.go` |
| Skill | 可复用 prompt 包 | `internal/skill/*`, `cmd/server/api_skill*.go` |
| Tool | 插件化工具 | `internal/tool/*` |
| Worktree | git worktree 隔离工作区 | `internal/workspace/*`, `internal/tool/worktree.go`, `cmd/server/workspace_api.go` |
| Cron | 定时器系统 | `internal/cron/*`, `cmd/server/cron_api.go` |
| 配置 | `.env` + 环境变量 | `internal/config/config.go` |

---

## 工作流步骤

### 步骤 1: 需求分析

当用户提出 bug 或功能需求时，**不要直接开始写代码**。先分析：

1. **复述需求** — 用自己的话确认理解是否正确
2. **判断影响范围** — 涉及哪些模块？（前端/后端/数据库/API/WebSocket 事件/子系统）
3. **列出潜在风险** — 可能破坏现有功能吗？需要数据迁移吗？会影响 worktree / cron / skill 等已交付子系统吗？
4. **与用户讨论** — 确认需求是否成立，方案是否合理

**Bug 额外步骤**：先定位根因，读取相关代码，确认问题可复现后再修复。

### 步骤 1.5: 选择方法论

本项目同时存在 **OpenSpec** 与 **superpowers** 两套方法论，LLM 自主选择，但一旦选定必须走完整流程：

- **跨模块 / 新 capability / 需要 spec 约束 / 可能引入新数据库表或 WebSocket 事件契约** → 先调用 `openspec-new-change`，本 skill 负责后续 `apply` 与 `verify` 阶段，禁止长期挂起不归档。
- **纯 bugfix / 小重构 / 单文件改动 / 可在一次会话内闭环** → 直接继续本 skill。

如果不确定，优先走 OpenSpec，避免变更失控。

### 步骤 2: 更新 ROADMAP

需求确认后，**立即更新 `roadmaps/ROADMAP.md`**：

- 在对应 Phase 下新增条目，格式：`- [ ] 功能描述`
- 如果是 bug 修复，在对应 Phase 下新增：`- [x] Bug修复: 描述`
- 更新版本历史表（`版本历史` 表格）
- 更新 `> **Last updated**` 日期

### 步骤 3: 判断 API 变更

在开始实现前，先判断是否需要 API 变更：

| 需要 API 变更 | 不需要 API 变更 |
|--------------|----------------|
| 新增 REST 端点 | 纯前端 UI 调整（注意区分 web/v1 还是 web/v2） |
| 修改请求/响应结构 | 纯后端内部逻辑 |
| 新增 WebSocket 事件类型 | 前端样式修改 |
| 数据库表结构变更 | 配置/文档更新 |

**如果需要 API 变更**：
1. 先设计 API 契约（端点路径、请求/响应 JSON 结构）
2. 如果是新的 WebSocket 事件类型：
   - 后端在 `pkg/event/event.go` 中定义事件常量
   - 前端在 `web/src/types/events.ts` 或 `web/v2/src/types/events.ts` 的 `EventType` union 中添加
3. 先实现后端 API 层，再并行实现前后端

**如果数据库表结构变更**：
1. 在 `pkg/db/migrate.go` 的 `migrations` 列表末尾追加新的 Migration 条目
2. 版本号递增，描述清晰，SQL 使用 `ALTER TABLE ADD COLUMN`
3. 同步更新 `pkg/db/database.go` 主 schema 与对应子系统 CRUD 文件（`pkg/db/{tool,workspace,cron,...}.go`）
4. 不要修改已有 Migration 条目

### 步骤 4: 按边界拆分子 Agent

不要固定只分两个 Agent。根据变更边界选择 1~3 个独立子 Agent：

**模式 A：纯后端子系统改动（如 cron / worktree / skill 内部逻辑）**
```
子 Agent A: 后端全栈实现
  范围: pkg/db/{子系统}.go, internal/{子系统}/*, cmd/server/{子系统}_api.go
  职责: 数据库迁移、internal 领域逻辑、REST API、WebSocket 事件、Agent Tools
```

**模式 B：前端无关的通用后端改动（如 engine / runner / event）**
```
子 Agent A: 后端核心实现
  范围: internal/*, cmd/server/runner.go/server.go
  职责: 业务逻辑、启动链路、事件路由
```

**模式 C：需要前后端的 feature（默认模式）**
```
子 Agent A: 后端 Go 实现
  范围: pkg/db/{相关子系统}.go, internal/{相关子系统}/*, cmd/server/{相关}_api.go
  职责: 数据库操作、API handler、业务逻辑、WebSocket 事件

子 Agent B: 前端 Vue 实现
  范围: web/v2/src/*（默认 targeting v2 控制室），必要时再改 web/src/*
  职责: 组件、composable、类型定义、样式、API 调用
```

**子 Agent A — 后端实现要点**：
- 数据库操作按子系统放到 `pkg/db/{子系统}.go`，不是全部堆到 `pkg/db/persistence.go`
- API handler 按领域放到 `cmd/server/*_api.go`（如 `api_skill.go`、`cron_api.go`、`workspace_api.go`）
- 路由注册统一在 `cmd/server/server.go:registerRoutes` 方法值注册，不要回到 `main.go` 写 `http.HandleFunc`
- WebSocket 事件通过 `hub.SendEvent(event.NewEvent(...))` 发送；orchestrator 编排事件不写 task steps，只做 WS 广播
- 新增事件类型需在 `pkg/event/event.go` 中定义
- 工具执行走 `internal/tool/registry.go:ExecuteWithCtx`，注意 `ExecuteContext.Workdir` 注入与 `WorkdirHolder` 协同
- 遵循现有代码风格：导出函数用大写；注释用中文，专业术语保留英文；每个导出类型/函数/接口必须有注释

**子 Agent B — 前端实现要点**：
- 默认 targeting `web/v2/src/`；如影响 v1 旧版，再处理 `web/src/`
- 状态管理用 composable（`useTaskStore`、`useSessionStore`、`useAgentStore`、`useCrons`、`useCronEvents` 等）
- 组件放在 `web/v2/src/components/` 下
- 类型定义在 `web/v2/src/types/` 下
- 新事件类型在对应 composable（如 `useCronEvents.ts`、`useTaskStore.ts`）的 `handleEvent` 中处理
- API 调用使用 `fetch`，不引入 axios
- 遵循现有代码风格：`<script setup lang="ts">` + scoped CSS

### 步骤 5: 验证

子 Agent 完成后，**必须全量验证**（根据改动范围选择子集，但核心几项不能跳过）：

```bash
# 1. 后端编译
cd D:/Claude-Code-MultiAgent && go build ./...

# 2. 后端测试
go test ./...

# 3. 相关子系统集成测试（如相关）
go test ./internal/workspace/... ./internal/tool/... ./internal/cron/... ./internal/skill/... ./cmd/server/...

# 4. mock 回归（21 case），Windows 下请先 export PYTHONUTF8=1
cd D:/Claude-Code-MultiAgent && bash scripts/cases-regression.sh

# 5. 前端 v1（如改动 web/）
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit && npx vite build

# 6. 前端 v2（如改动 web/v2/）
cd D:/Claude-Code-MultiAgent/web/v2 && npx vue-tsc --noEmit && npx vite build

# 7. 最终二进制
cd D:/Claude-Code-MultiAgent && go build -o server.exe ./cmd/server
```

**编译、测试、mock 回归全部通过才算完成状态可关闭**。如果有编译错误或测试失败，修复后重新验证。

### 步骤 6: 更新 ROADMAP 标记完成

- 将对应条目的 `- [ ]` 改为 `- [x]`
- 更新 `> **Last updated**` 日期
- 如果有版本号变更，更新 `> **Current version**`
- 如果本次变更走了 OpenSpec，还必须调用 `openspec-verify-change` 与 `openspec-archive-change` 完成归档

---

## 编码约定

- **Go**: 标准库优先，interface 抽象，goroutine 安全
- **事件驱动**: 所有状态变更通过 EventBus 广播，不直接操作前端状态
- **注释**: 用中文；每个导出类型/函数/接口必须有注释；专业术语保留英文，必要时 English(中文) 注译
- **白盒 Agent**: ReAct Loop、SSE 解析、Event 路由、Tool 执行等关键流程必须有行内注释
- **数据库迁移**: 新增字段用 `pkg/db/migrate.go` 追加 Migration，不要手动改 SQLite；同步更新对应子系统 CRUD 文件
- **前端类型**: 与后端事件结构保持同步，`web/v2/src/types/events.ts` 与 `web/src/types/events.ts` 的 `EventType` union 涵盖所有后端事件类型
- **错误处理**: 后端返回有意义的错误信息，前端用 Toast 组件展示
- **Tool 接口**: Phase 8-A 后扩展 `Version() / Source() / CanonicalName()`，动态工具走 `ToolDescriptor → DynamicTool → DynamicExecutor`

---

## 子系统速查：改哪里

按子系统归类常见修改入口，避免子 Agent 写到废弃位置：

### Skill 系统
- 模型：`internal/skill/skill.go`
- 内存注册表：`internal/skill/registry.go`
- SQLite 持久化：`internal/skill/store.go`
- 加载：`internal/skill/loader.go`
- 内置种子：`internal/skill/builtin.go`
- 模板渲染：`internal/skill/renderer.go`
- Agent Tools：`internal/skill/tools.go`
- REST + 测试：`cmd/server/api_skill.go`、`cmd/server/api_skill_test.go`、`cmd/server/api_skill_scan_test.go`

### Worktree 隔离
- 原语：`internal/workspace/manager.go`
- 单测：`internal/workspace/manager_test.go`
- Agent Tools：`internal/tool/worktree.go`
- REST + 孤儿扫描：`cmd/server/workspace_api.go`
- 集成测试：`cmd/server/workspace_api_test.go`、`cmd/server/workspace_orphan_test.go`

### Cron 定时器
- 领域模型/调度/执行：`internal/cron/*.go`
- 表迁移与 CRUD：`pkg/db/cron.go`
- REST：`cmd/server/cron_api.go`
- 测试：`internal/cron/*_test.go`、`cmd/server/cron_api_test.go`

### Tool 插件化
- 注册表/执行入口：`internal/tool/registry.go`
- 内置工具：`internal/tool/builtin.go`
- 可序列化描述：`internal/tool/descriptor.go`
- 动态工具：`internal/tool/dynamic.go`
- 执行体：`internal/tool/executor.go`
- 加载器：`internal/tool/loader.go`

### 多 Agent 编排
- 启动收口：`cmd/server/runner.go`
- orchestrator 逻辑：`internal/runtime/engine.go` 或相关 orchestrator 实现
- 编排事件：WS 广播，不写 task steps

---

## 并行子 Agent 调用模板

按步骤 4 选择模式后，使用以下模板。注意明确依赖边界：后端 Agent 先完成 API 契约，前端 Agent 再开始对接。

**模式 A / B（纯后端）**
```
子 Agent A（后端）:
  prompt: "在 D:/Claude-Code-MultiAgent 项目中实现 [功能] 的后端部分。
  涉及: pkg/db/{子系统}.go（数据库迁移与 CRUD）、internal/{子系统}/*（领域逻辑）、
  cmd/server/{子系统}_api.go（REST handler）。路由注册在 cmd/server/server.go:registerRoutes。
  [具体需求]。
  完成后运行 go build ./... 与 go test ./{相关包}/... 验证。"
```

**模式 C（前后端）**
```
子 Agent A（后端）:
  prompt: "在 D:/Claude-Code-MultiAgent 项目中实现 [功能] 的后端部分。
  涉及: pkg/db/{相关}.go（数据库迁移与 CRUD）、internal/{相关}/*（领域逻辑）、
  cmd/server/{相关}_api.go（REST handler）。路由注册在 cmd/server/server.go:registerRoutes，
  事件走 hub.SendEvent(event.NewEvent(...))。请优先完成并稳定 API 契约。
  [具体需求]。
  完成后运行 go build ./... 与 go test ./{相关包}/... 验证。"

子 Agent B（前端 v2）:
  prompt: "在 D:/Claude-Code-MultiAgent/web/v2 项目中实现 [功能] 的前端部分。
  涉及: web/v2/src/components/（组件）、web/v2/src/composables/（状态管理）、
  web/v2/src/types/events.ts（类型定义）。API 契约由后端 Agent 提供。
  [具体需求]。
  完成后运行 npx vue-tsc --noEmit && npx vite build 验证。"
```

多个 Agent 同时启动，等待全部完成后进行步骤 5 的全量验证。

---

## 常见场景速查

### 新增 REST API 端点

1. `cmd/server/{子系统}_api.go` — 添加 `*appServer` 方法 handler
2. `cmd/server/server.go:registerRoutes` — 方法值注册路由
3. 前端 v2 composable 或组件中调用 `fetch('/api/xxx')`（默认），必要时同步 v1

### 新增 WebSocket 事件类型

1. `pkg/event/event.go` — 定义事件常量
2. 后端 `hub.SendEvent(event.NewEvent("new_type", ...))` 发送
3. `web/v2/src/types/events.ts` — `EventType` union 添加新类型（v1 同步需要再加 `web/src/types/events.ts`）
4. 对应 composable 的 `handleEvent` 添加 case

### 新增数据库字段 / 表

1. `pkg/db/migrate.go` — 追加 Migration 条目
2. `pkg/db/database.go` — 同步主 schema（如新增表）
3. `pkg/db/{子系统}.go` — 更新 struct 和 SQL 查询（不要只改 persistence.go）
4. 如果是 API 返回字段，更新 JSON 序列化

### 新增前端组件

1. 默认在 `web/v2/src/components/NewComponent.vue` 创建组件
2. 在父组件中 import 并使用
3. 如需跨组件共享状态，在 `web/v2/src/composables/` 中添加
4. 如影响 v1 旧版，同步改 `web/src/components/`

### 修复 Bug

1. 先定位根因（读代码 + 复现）
2. 确认修复方案不影响现有功能，特别是 worktree / cron / skill 等子系统
3. 实现修复（小改动直接本 skill；跨模块先 openspec）
4. 全量验证（`go build`、`go test`、必要时 mock 回归）
5. ROADMAP 标记为 `[x] Bug修复: 描述`
