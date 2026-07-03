# Multi-Agent Platform — 项目说明

> Go + Vue 3 多 Agent 实时协作平台。从零构建，完全可观测。

---

## 设计哲学

**白盒 Agent**：每一个 LLM token、每一次 tool call、每一个 step 状态转换都生成事件并实时广播到前端。不做"黑盒 Agent"。

参考但不依赖以下框架的设计思想：
- **eino** (ByteDance) — 组件化编排、Stream 接口
- **langchaingo** — Chain / Memory 抽象层次
- **trpc-agent-go** — Agent 间通信协议、并发模型

所有实现从零手写，保证最大控制权和可观测性。

---

## 技术栈

| 层 | 技术 | 说明 |
|----|------|------|
| 语言 | Go 1.25 | 后端全部 |
| 数据库 | modernc.org/sqlite | 纯 Go SQLite，单文件部署 |
| 通信 | gorilla/websocket | Phase 0-4，Phase 6 迁 gRPC |
| 前端 | Vue 3 + Vite + TypeScript | 当前 CDN 单文件，Phase 2 迁移 |
| LLM | OpenAI-compatible API | `aicoding.dobest.com/v1`，deepseek-v4-flash |
| 配置 | .env + 环境变量 | 优先级：系统环境变量 > .env > 默认值 |

---

## 项目结构

```
cmd/server/main.go          # 入口：HTTP Server + WS Hub + API 路由
internal/
  agent/agent.go             # Agent 类型定义
  config/config.go           # .env 加载 + 配置管理
  llm/client.go              # OpenAI-compatible SSE streaming 客户端
  runtime/engine.go          # ReAct Loop 引擎 + Step 状态机
  tool/registry.go           # Tool 注册表
  tool/builtin.go            # 3 个内置工具 (run_shell, write_file, read_file)
  ws/hub.go                  # WebSocket Hub (connect/broadcast/disconnect)
pkg/
  event/event.go             # 统一事件结构 + 序列化
  db/database.go             # SQLite 初始化 + Schema (6 表)
web/                         # 前端 (CDN 单文件，待迁移到 Vite)
data/                        # SQLite 数据库文件
storage/                     # 文件存储
.env                         # API Key + 配置 (gitignore)
```

---

## 事件系统

所有前后端通信统一为 `AgentEvent` JSON，关键事件类型：

```
task_started → step_started → llm_thinking → llm_delta* → llm_message_complete
  → tool_call_started → tool_call_output → tool_call_complete
  → observation → step_complete → ... → task_completed / task_failed
```

每个事件携带 `{task_id, agent_id, step_index, timestamp, data}`，前端可按 Agent 独立追踪并行状态。

---

## ReAct Loop 引擎

```
User Input → System Prompt + Messages → LLM (streaming)
  → 有 tool_calls? → Tool Registry.Execute() → 追加 tool result 到 Messages
  → 无 tool_calls? → Final Answer → task_completed
  → 超过 max_steps? → task_failed (max_steps_exceeded)
```

---

## Phase 计划

```
Phase 0 ✅ → Phase 1 ✅ → Phase 2 🔜 → Phase 3 → Phase 4 → Phase 5 → Phase 6
  (骨架)      (Agent)     (UI)       (Cases)    (并发)    (注册)    (高级)
```

| Phase | 状态 | 核心交付 |
|-------|------|---------|
| 0 骨架 | ✅ | WS Hub + Event Schema + SQLite + Vue CDN |
| 1 Agent | ✅ | LLM Client + 3 Tools + ReAct Engine + .env |
| 2 UI | 🔜 | Vite + TypeScript + AgentTree + TypeWriter + Markdown |
| 3 Cases | ⬜ | 5 预设 Cases + Card UI + 历史回放 |
| 4 并发 | ⬜ | 多 Agent 并行 + 前端多树渲染 |
| 5 注册 | ⬜ | 运行时 Tool 注册 + Docker 沙箱 |
| 6 高级 | ⬜ | RAG + Auth + gRPC + 可观测性 |

---

## 编码约定

- **Go**: 标准库优先，interface 抽象，goroutine 安全
- **事件驱动**: 所有状态变更通过 EventBus 接口广播，不直接操作前端状态
- **Tool 接口**: `Name() / Description() / Parameters() / Execute(input) -> output`
- **错误处理**: 每个 Step 失败都生成 `task_failed` 事件，含具体 reason
- **Token 统计**: 严格使用 API 返回的 `usage` 字段，不做本地估算

---

## Git 铁律

- 每个 Phase 完成后必须提交 Git
- 提交信息格式：`Phase X: 简要描述`
- 同步更新 `roadmaps/ROADMAP.md`

---

## 注释铁律 — 白盒 Agent 设计哲学

**代码即文档**。本项目的核心设计哲学是"白盒 Agent"——每一行代码都应该是可读、可理解、可追溯的。这意味着：

- **每个导出的类型/函数/接口必须有注释**，说明其职责、使用场景、与其它模块的关系
- **关键流程必须有行内注释**（ReAct Loop、SSE 解析、Event 路由、Tool 执行等）
- **TODO 注释标注未实现功能**，格式：`// TODO: Phase X — 描述`
- **接口设计需注释"为什么"**，而非"是什么"（代码已经说了"是什么"）
- **前端组件同样要求**：每个组件的 props/emits/生命周期 需注释其数据流和事件流

Why: 多 Agent 系统的复杂度在于不可见的状态流转和决策链路。注释是"白盒"的第一层——让阅读者不需要运行代码就能理解 Agent 的内部逻辑。从文档 → 注释 → 前端可视化，三层递进体现设计哲学。

---

## 内存规则

- 会话期间短期记忆
- 踩坑记录写入 memory
- 重大设计决策写入 memory
- 全局记忆写入 memory
- Token 管理：不携带完整历史，精简上下文

---

## API 配置

```
Endpoint:  https://aicoding.dobest.com/v1
Model:     deepseek-v4-flash
API Key:   写在 .env (gitignore)
```

.env 文件格式：
```
LLM_ENDPOINT=https://aicoding.dobest.com/v1
LLM_API_KEY=sk-xxx
LLM_MODEL=deepseek-v4-flash
```

---

## Open Questions

- `run_shell` 沙箱方案 (Docker / Firecracker) → Phase 5
- 前端状态管理 (Pinia vs reactive) → Phase 2
- Agent 间通信协议 → Phase 4
- Markdown 渲染库 (marked / markdown-it) → Phase 2