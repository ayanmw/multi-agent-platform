# 多 Agent 平台 — 项目说明

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
  skill/                     # Skill 可复用 prompt 包
    models.go                # Skill 领域模型（来源、状态、模板、变量）
    registry.go              # 进程内 Skill 注册表
    store.go                 # SQLite 持久化
    loader.go                # built_in / local_db 加载
    renderer.go              # {{ variable }} 模板渲染
    builtin.go               # 内置 Skill 种子
  tool/registry.go           # Tool 注册表
  tool/builtin.go            # 3 个内置工具 (run_shell, write_file, read_file)
  ws/hub.go                  # WebSocket Hub (connect/broadcast/disconnect)
pkg/
  event/event.go             # 统一事件结构 + 序列化
  db/database.go             # SQLite 初始化 + Schema (6 表)
web/                         # 前端 Vite + Vue 3 + TypeScript
  src/components/SkillPicker.vue   # `/` 触发的 Skill 搜索面板
  src/components/TaskInput.vue     # chat 输入 + 集成 SkillPicker
  src/App.vue                      # 处理 `/skill-id ` 前缀并启用 skill
data/                        # SQLite 数据库文件
storage/                     # 文件存储
.env                         # API Key + 配置 (gitignore)
```

## Skill 系统

Skill 是可复用的 prompt + 任务知识包，不是 Agent、Tool 或 Plugin。它让同一个 Agent 在不切换配置的前提下，根据启用的 Skill 动态切换专长。

### 核心概念

| 概念 | 说明 |
|------|------|
| `SkillSource` | `built_in` / `local_file` / `local_db` / `market` / `mcp` |
| `SkillState` | `discovered → validated → loaded → enabled / disabled / invalid` |
| `SkillTemplate` | 模板，名称 `system_prompt` / `task_prompt` 会被注入到 Engine system prompt |
| `SkillParameter` | 模板变量定义，含类型、必填、默认值、描述 |
| `Renderer` | 使用 `{{ variable }}` 风格占位符渲染模板并自动提取变量 |

### 关键文件

```
internal/skill/
  skill.go       # 核心模型（SkillSource / SkillState / Skill / Template / Parameter）
  registry.go    # 内存注册表，支持 List / Get / Set / Exists
  store.go       # SQLite 持久化（skills 表 migration）
  loader.go      # built_in + local_db 加载
  renderer.go    # {{ variable }} 模板渲染与变量提取
  builtin.go     # 内置 Skill 种子（builtin-code-helper / builtin-error-diagnosis）
  tools.go       # skill/create_local, skill/delete_local, skill/list Agent Tools
  events.go      # Skill 相关 event constants（skill_enabled / skill_disabled / skill_created / skill_deleted）
```

### 注入机制

Engine 在 `NewEngine` 构建 system prompt 后，如果有 `SkillRegistry` 和 `ActiveSkills`：

```go
if cfg.SkillRegistry != nil && len(cfg.ActiveSkills) > 0 {
    renderer := skill.NewRenderer()
    for _, id := range cfg.ActiveSkills {
        s, ok := cfg.SkillRegistry.Get(id)
        if !ok { continue }
        for _, tmpl := range s.Templates {
            if tmpl.Name == "system_prompt" || tmpl.Name == "task_prompt" {
                rendered = append(rendered, renderer.Render(tmpl, cfg.SkillVariables))
            }
        }
    }
}
```

启用多个 Skill 时，多个模板按顺序追加到 `## Skill Instructions` 章节，便于叠加。变量缺失时，Renderer 优先使用 `SkillParameter.Default`；无默认值则保留占位符。

### REST API

```
GET    /api/skills?source=built_in|local_db   # 列出（可过滤来源）
GET    /api/skills/search?q=code              # 搜索 id / display_name / description / tags
POST   /api/skills                            # 创建 local_db Skill
GET    /api/skills/:id                        # 详情
PUT    /api/skills/:id                        # 更新 local editable Skill
DELETE /api/skills/:id                        # 删除 local editable Skill
POST   /api/skills/:id/enable                 # 启用（同步 registry 与 store）
POST   /api/skills/:id/disable                # 禁用
```

内置 Skill（`is_local_editable=false`）不可 PUT / DELETE，返回 `403 Forbidden`。单元测试见 `cmd/server/api_skill_test.go`。

### Agent Tools

| Tool | 说明 |
|------|------|
| `skill/create_local` (alias `skill_create_local`) | Agent 在运行中创建本地 Skill |
| `skill/delete_local` | 删除本地 Skill |
| `skill/list` | 列出已加载 Skill |

### 前端触发方式

- 在 `TaskInput` 输入框中键入 `/` 触发 `SkillPicker` 悬浮面板。
- `SkillPicker` 调用 `GET /api/skills/search?q=`，支持 ↑/↓ 选择、Enter 确认、Esc 取消。
- 选中后，父组件在输入框填入 `/skill-id ` 前缀。
- `App.vue` 的 `handleSend` 解析该前缀，先调用 `POST /api/skills/{id}/enable`，然后将剩余文本作为真实 input 发送。
- 输入框不再含 `/` 触发字符时，SkillPicker 自动关闭。

---


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
Phase 0 ✅ → Phase 1 ✅ → Phase 2 ✅ → Phase 3 ✅ → Phase 4 ✅ → Phase 5 ✅ → Phase 6 ✅ (Skeleton)
  (骨架)      (Agent)     (UI)       (Cases)    (并发)      (注册)      (高级)
```

| Phase | 状态 | 核心交付 |
|-------|------|---------|
| 0 骨架 | ✅ | WS Hub + Event Schema + SQLite + Vue CDN |
| 1 Agent | ✅ | LLM Client + 3 Tools + ReAct Engine + .env |
| 2 UI | ✅ | Vite + TypeScript + AgentTree + TypeWriter + Markdown |
| 3 Cases | ✅ | 5 预设 Cases + Card UI + 历史回放 |
| 4 并发 | ✅ | 多 Agent 并行 + 前端多树渲染 |
| 5 注册 | ✅ | 运行时 Tool 注册 + Docker 沙箱 |
| 6 高级 | ✅ | RAG + Auth + gRPC + 可观测性 |

---

## 扩展 Phase

```
Phase skill ✅ → Phase 7 🔜
  (Skill 系统)      (生产化与深度集成)
```

| Phase | 状态 | 核心交付 |
|-------|------|---------|
| skill | ✅ | 可复用 prompt 包 + Renderer + Registry + REST API + Agent Tools + 前端 `/` 触发 SkillPicker + E2E 测试 |
| 7 生产化 | ⬜ | tokenizer、context 压缩、RBAC、MCP 增强、K8s 部署等（Roadmap 统一规划）|

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

## 待定问题

- `run_shell` 沙箱方案 (Docker / Firecracker) → Phase 5
- 前端状态管理 (Pinia vs reactive) → Phase 2
- Agent 间通信协议 → Phase 4
- Markdown 渲染库 (marked / markdown-it) → Phase 2