# Multi-Agent Platform

> Go + Vue 3 多 Agent 实时协作平台。从零构建，完全可观测的白盒 Agent。

## 快速开始

### 1. 配置

```bash
# 编辑 .env 文件（API Key 等）
# 已有默认值，通常无需修改
```

### 2. 编译运行

```bash
go build -o server.exe ./cmd/server/
./server.exe --port 8080
```

### 3. 端到端测试（推荐）

```bash
# 编译
go build -o e2e-test.exe ./cmd/e2e-test/

# 启动 server 后运行测试
./e2e-test.exe --scenario all

# 仅测试简单对话
./e2e-test.exe --scenario simple

# 仅测试工具调用
./e2e-test.exe --scenario tool
```

### 4. curl 手动验证

```bash
# 健康检查
curl http://localhost:8080/health

# 对话测试
curl -X POST http://localhost:8080/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"action":"chat","input":"1+1=?"}'

# 工具调用测试
curl -X POST http://localhost:8080/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"action":"chat","input":"用 run_shell 执行 echo hello"}'

# WebSocket 实时事件
wscat -c ws://localhost:8080/ws
```

## 项目结构

```
cmd/
  server/main.go      服务入口：HTTP + WS + API 路由
  e2e-test/main.go     端到端测试工具（WebSocket 事件着色打印）
internal/
  agent/agent.go        Agent 类型定义（Agent / Step / Status）
  config/config.go      .env 加载 + 配置优先级
  llm/client.go         OpenAI-compatible SSE streaming 客户端
  runtime/engine.go     ReAct Loop 引擎 + Step 状态机 + EventBus 接口
  tool/
    registry.go          Tool 注册表（Register / Execute / List）
    builtin.go           3 个内置工具（run_shell, write_file, read_file）
  ws/hub.go             WebSocket Hub（connect/broadcast/disconnect）
pkg/
  event/event.go        统一事件结构（Event + EventType）
  db/database.go        SQLite Schema（6 表）
web/                    Vue 3 前端（CDN 单文件，Phase 2 迁移到 Vite）
```

## 架构概览

```
POST /api/tasks {action:"chat", input:"..."}
  → ReAct Engine
    → Step 0: think (LLM ChatStream → SSE → llm_delta events)
    → Step 1: tool_call (Tool Registry.Execute → tool_call_output events)
    → Step 2: observe (LLM analyzes tool result)
    → task_completed / task_failed
  → WebSocket Hub → 所有事件实时广播到所有客户端
```

## 当前状态

**v0.2 Alpha** — Phase 1 完成

| 功能 | 状态 |
|------|------|
| WebSocket 实时通信 | ✅ |
| LLM API 客户端 (SSE streaming) | ✅ |
| 3 个内置工具 (run_shell, write_file, read_file) | ✅ |
| ReAct Loop 引擎 (think → tool_call → observe) | ✅ |
| .env 配置管理 | ✅ |
| Go 端到端测试工具 (着色输出) | ✅ |
| DB 持久化接入 Agent Loop | ⬜ |
| Vue 3 + Vite 前端迁移 | 🔜 Phase 2 |

## 设计文档

- `CLAUDE.md` — 项目设计哲学 + 编码约定 + 事件系统 + API 配置
- `roadmaps/ROADMAP.md` — 路线图 + 版本历史
- `openspec/changes/multi-agent-platform/` — 详细技术设计 + 规格说明