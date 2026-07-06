---
name: bug-feature-dev
description: |
  Multi-Agent Platform 项目的 Bug 修复与功能开发工作流。当用户报告 bug、提出功能需求、
  或说"修复xxx"、"新增xxx功能"、"优化xxx"、"加一个xxx"时触发此 Skill。
  也适用于 "帮我实现"、"开发"、"改进" 等开发类请求。
  覆盖从需求分析→ROADMAP更新→API设计→前后端并行实现→验证的完整流程。
---

# Bug / Feature 工作流

本 Skill 定义了 Multi-Agent Platform 项目的标准开发流程。每个 bug 或功能点都遵循以下步骤。

---

## 技术栈速查

| 层 | 技术 | 关键目录 |
|----|------|---------|
| 后端 | Go 1.25 | `cmd/server/`, `internal/`, `pkg/` |
| 数据库 | SQLite (modernc.org/sqlite) | `pkg/db/database.go`, `pkg/db/persistence.go` |
| 迁移 | `pkg/db/migrate.go` — 自动增量迁移 | 新增字段追加 Migration 条目 |
| 前端 | Vue 3 + Vite + TypeScript | `web/src/` |
| 通信 | WebSocket (gorilla/websocket) | `internal/ws/hub.go` |
| 事件 | `pkg/event/event.go` — 统一事件结构 | 前后端通过 `AgentEvent` JSON 通信 |
| LLM | OpenAI-compatible API | `internal/llm/client.go` |
| 配置 | `.env` + 环境变量 | `internal/config/config.go` |

---

## 工作流步骤

### 步骤 1: 需求分析

当用户提出 bug 或功能需求时，**不要直接开始写代码**。先分析：

1. **复述需求** — 用自己的话确认理解是否正确
2. **判断影响范围** — 涉及哪些模块？（前端/后端/数据库/API/WebSocket 事件）
3. **列出潜在风险** — 可能破坏现有功能吗？需要数据迁移吗？
4. **与用户讨论** — 确认需求是否成立，方案是否合理

**Bug 额外步骤**：先定位根因，读取相关代码，确认问题可复现后再修复。

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
| 新增 REST 端点 | 纯前端 UI 调整 |
| 修改请求/响应结构 | 纯后端内部逻辑 |
| 新增 WebSocket 事件类型 | 前端样式修改 |
| 数据库表结构变更 | 配置/文档更新 |

**如果需要 API 变更**：
1. 先设计 API 契约（端点路径、请求/响应 JSON 结构）
2. 如果是新的 WebSocket 事件类型，在 `web/src/types/events.ts` 的 `EventType` union 中添加
3. 先实现后端 API 层，再并行实现前后端

**如果数据库表结构变更**：
1. 在 `pkg/db/migrate.go` 的 `migrations` 列表末尾追加新的 Migration 条目
2. 版本号递增，描述清晰，SQL 使用 `ALTER TABLE ADD COLUMN`
3. 不要修改已有 Migration 条目

### 步骤 4: 前后端并行实现

将任务拆分为两个独立的子 Agent：

```
子 Agent A: 后端 Go 实现
  范围: pkg/db/*, internal/*, cmd/server/*
  职责: 数据库操作、API handler、业务逻辑、WebSocket 事件

子 Agent B: 前端 Vue 实现
  范围: web/src/*
  职责: 组件、composable、类型定义、样式、API 调用
```

**子 Agent A — 后端实现要点**：
- 数据库操作放 `pkg/db/persistence.go`
- API handler 放 `cmd/server/api.go`
- 路由注册在 `cmd/server/main.go` 的 `http.HandleFunc` 中
- WebSocket 事件通过 `hub.SendEvent(event.NewEvent(...))` 发送
- 新增事件类型需在 `pkg/event/event.go` 中定义
- 遵循现有代码风格：导出函数用大写，注释说明职责

**子 Agent B — 前端实现要点**：
- 状态管理用 composable（`useTaskStore`, `useSessionStore`, `useAgentStore`）
- 组件放在 `web/src/components/` 下
- 类型定义在 `web/src/types/events.ts`
- 新事件类型在 `useTaskStore.ts` 的 `handleEvent` 中处理
- API 调用使用 `fetch`，不引入 axios
- 遵循现有代码风格：`<script setup lang="ts">` + scoped CSS

### 步骤 5: 验证

两个子 Agent 完成后，**必须全量验证**：

```bash
# 后端
cd D:/Claude-Code-MultiAgent && go build ./...

# 前端
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit && npx vite build

# 最终二进制
cd D:/Claude-Code-MultiAgent && go build -o server.exe ./cmd/server
```

**三项全部通过才算完成**。如果有编译错误，修复后重新验证。

### 步骤 6: 更新 ROADMAP 标记完成

- 将对应条目的 `- [ ]` 改为 `- [x]`
- 更新 `> **Last updated**` 日期
- 如果有版本号变更，更新 `> **Current version**`

---

## 编码约定

- **Go**: 标准库优先，interface 抽象，goroutine 安全
- **事件驱动**: 所有状态变更通过 EventBus 广播，不直接操作前端状态
- **注释**: 每个导出类型/函数/接口必须有注释（白盒 Agent 设计哲学）
- **数据库迁移**: 新增字段用 `pkg/db/migrate.go` 追加 Migration，不要手动改 SQLite
- **前端类型**: 与后端事件结构保持同步，EventType union 涵盖所有后端事件类型
- **错误处理**: 后端返回有意义的错误信息，前端用 Toast 组件展示

---

## 并行子 Agent 调用模板

当需要前后端并行实现时，使用以下模式：

```
子 Agent A（后端）:
  prompt: "在 D:/Claude-Code-MultiAgent 项目中实现 [功能] 的后端部分。
  涉及: pkg/db/persistence.go（数据库操作）、cmd/server/api.go（API handler）、
  cmd/server/main.go（路由注册）。[具体需求]。
  完成后运行 go build ./... 验证。"

子 Agent B（前端）:
  prompt: "在 D:/Claude-Code-MultiAgent/web 项目中实现 [功能] 的前端部分。
  涉及: web/src/components/（组件）、web/src/composables/（状态管理）、
  web/src/types/events.ts（类型定义）。[具体需求]。
  完成后运行 vue-tsc --noEmit && vite build 验证。"
```

两个 Agent 同时启动，等待全部完成后进行步骤 5 的全量验证。

---

## 常见场景速查

### 新增 REST API 端点

1. `cmd/server/api.go` — 添加 handler 函数
2. `cmd/server/main.go` — 注册 `http.HandleFunc`
3. 前端 composable 或组件中调用 `fetch('/api/xxx')`

### 新增 WebSocket 事件类型

1. `pkg/event/event.go` — 确认事件类型已定义
2. 后端 `hub.SendEvent(event.NewEvent("new_type", ...))` 发送
3. `web/src/types/events.ts` — `EventType` union 添加新类型
4. `web/src/composables/useTaskStore.ts` — `handleEvent` 添加 case

### 新增数据库字段

1. `pkg/db/migrate.go` — 追加 Migration 条目
2. `pkg/db/persistence.go` — 更新 struct 和 SQL 查询
3. 如果是 API 返回字段，更新 JSON 序列化

### 新增前端组件

1. `web/src/components/NewComponent.vue` — 创建组件
2. 在父组件中 import 并使用
3. 如需跨组件共享状态，在 composable 中添加

### 修复 Bug

1. 先定位根因（读代码 + 复现）
2. 确认修复方案不影响现有功能
3. 实现修复
4. 全量验证
5. ROADMAP 标记为 `[x] Bug修复: 描述`