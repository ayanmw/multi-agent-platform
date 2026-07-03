
# 本项目简介

本项目语言主要使用 golang 
从0开始写一个agent软件,对标 claude/codex/openclaw/manus 类似的软件, 是多Agent平台


一、整体架构

════════════════════════════════════════════════════════════════
                   系统架构全景
════════════════════════════════════════════════════════════════

┌─────────────────────────────────────────────────────────────┐
│                         Vue 3 Frontend                      │
│  ┌──────────┐ ┌──────────────┐ ┌─────────────────────────┐  │
│  │ Chat UI  │ │ Agent Tree   │ │ Case Cards              │  │
│  │ (SSE)    │ │ Visualization│ │ (预设任务一键发起)      │  │
│  │ TypeWriter││ 可折叠/高亮  │ │                         │  │
│  │ Markdown ││ 实时状态更新  │ │                         │  │
│  └────┬─────┘ └──────┬───────┘ └───────────┬─────────────┘  │
│       │              │                     │                │
└───────┼──────────────┼─────────────────────┼────────────────┘
        │   gRPC/WS    │                     │  REST
        ▼              ▼                      ▼
┌─────────────────────────────────────────────────────────────┐
│                      Go Backend Server                      │
│                                                             │
│  ┌────────────────┐  ┌──────────────────────────────────┐   │
│  │  Protocol      │  │  gRPC (primary)                  │   │
│  │  Layer         │  │  ? Bidirectional streaming       │   │
│  │                │  │  ? Type-safe proto               │   │
│  │                │  │  ? WebSocket (fallback/未来扩展) │   │
│  └────────┬───────┘  └──────────────────────────────────┘   │
│           │                                                 │
│  ┌────────┴──────────────────────────────────────────────┐  │
│  │  Agent Runtime                                        │  │
│  │  ┌──────────┐  ┌───────────┐  ┌───────────────────┐   │  │
│  │  │ LLM      │  │ Tool      │  │ Loop              │   │  │
│  │  │ Client   │  │ Registry  │  │ Engine            │   │  │
│  │  │ (OpenAI  │  │ (动态注册)│  │ (ReAct + 状态机)  │   │  │
│  │  │ 兼容API) │  │           │  │                   │   │  │
│  │  └──────────┘  └───────────┘  └───────────────────┘   │  │
│  └───────────────────────────────────────────────────────┘  │
│           │                                                 │
│  ┌────────┴──────────────────────────────────────────────┐  │
│  │  Persistence Layer (SQLite + file system)             │  │
│  │  Agents │ Tasks │ Steps │ Tools │ Messages │ Prompts  │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  RAG Layer (future: Phase 5+)                        │   │
│  │  Embedding → Vector Store → Retrieval                │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                              │
                    ┌─────────┴──────────┐
                    │  Internal LLM API  │
                    │  aicoding.dobest   │
                    │  /v1 (OpenAI 兼容) │
                    └────────────────────┘

## Why
需要构建一个可本地训练、调试和观察 AI Agent 行为的多 Agent 系统。作为学习工具时，需要能实时看到每一步的思考过程、工具调用、token 流；作为产品使用时，需要可配置、可扩展、可持久化。

已有内部 API (`aicoding.dobest.com/v1`, deepseek-v4-flash)，用 Go 做后端 (高并发/强类型/部署简单)，Vue 3 做前端 (实时渲染/状态可视化)，在单仓库内快速迭代出一套可工作的 MVP。

  ## What Changes

  - **后端 Agent Runtime**: 实现 ReAct Loop 引擎，支持多 Agent 并发执行，每个 Agent 可配置不同 API endpoint、model、tools
  - **WebSocket 实时通信**: 前后端全双工通信，所有 LLM token、tool call、step 状态实时推送到前端
  - **WebSocket fallback WebSocket**: 保留 API 转型到 gRPC 的预留接口，后端协议层抽象
  - **SQLite 持久化**: 存储 Agents 配置、Tasks 执行历史、Steps 详细日志、Tools 注册表、Conversations 会话
  - **工具注册系统**: 支持运行时注册 (AI 读写 / API 注册)，工具以 JSON Schema 描述参数
  - **Vue 前端 UI**
    - Case Cards 面板：预设任务，一键发起多 Agent 任务
    - Agent Tree 可视化：任务树可折叠，实时显示每个 step 的 think/tool_call/observation
    - TypeWriter token 流渲染
    - Agent 配置页面 (CRUD)
    - 任务历史侧边栏
  - **预设 Task Cases**: 代码生成+执行验证、研究任务、多Agent协作、交互对话、长任务+人工干预

- **新依赖**: Go 1.23+, modernc.org/sqlite (纯Go SQLite), gorilla/websocket, protobuf (预留 gRPC), Vue 3 + Vite + TypeScript
- **API**: 后端暴露 WebSocket endpoint (`/ws`)，REST API 用于 Agent 配置 CRUD、Task 历史查询
- **数据**: local SQLite 文件 (`data/app.db`)，Markdown 文件存储于 `storage/`
- **外部服务**: 内部 LLM API (`https://aicoding.dobest.com/v1`, deepseek-v4-flash)
- **架构**: 单仓库 monorepo，go/ 和 web/ 子目录


  ① 技术选型确认
     - Go 1.23+
     - modernc.org/sqlite (纯Go的SQLite)
     - gorilla/websocket (WebSocket)
     - Vue 3 + Vite + TypeScript
	 
# AI 技术

- `agent-runtime`: 核心 Agent Loop 引擎（ReAct + 状态机），支持 ReAct Loop、Tool Dispatch、Memory 管理
- `websocket-bus`: WebSocket 事件总线，定义 AgentEvent 协议，全双工实时推送 LLM token / step 状态 / tool 结果
- `tool-registry`: Tool 注册与调度系统，JSON Schema 参数验证，运行时注册接口，执行结果结构化返回
- `persistence-layer`: SQLite 持久化层，存储 Agent 配置、Task/Step 历史、Tool Schema、Conversation 日志
- `agent-config`: Agent 配置管理 CRUD，运行时 API 注册，多 API endpoint / model 隔离
- `frontend-ui`: Vue 3 + Vite 前端，Case Cards、Agent Tree 可视化、TypeWriter 渲染、配置页面

##上下文管理
会话级短期记忆
长期记忆
失败踩坑记忆
遗忘不重要的事情的记忆
全局记忆
上下文token管理: 不会携带完整的历史,而是经过记忆管理的


## AI API接口: 使用openai compatible 的API
 可以使用以下 token ,写入 .env ,环境变量优先级 系统环境变量<.env
https://aicoding.dobest.com/v1
sk-k6izovyLmoUIwu4zI1ddjDsTQ646hwT5I5KSChU5UKFHHmgM
deepseek-v4-flash

根据API 进行token 读/读缓存/写 token 统计,严格根据API返回的response来统计,如果API不支持,每次需要给与一个提示

##


#Plan
  Phase 0-4 均为新系统，无迁移。6 阶段独立递进：

  ```
  Phase 0 → Phase 1 → Phase 2 → Phase 3 → Phase 4
   (骨架)    (Agent)    (UI)     (Cases)   (并发)

  每个 Phase 完成后，手动验证，确认后再进下一 Phase。
  ```

建议的占坑计划（你提的"分层渐进"）

Phase 1 ──── 基础通信层
             目标：Go SSE → Vue 实时渲染 跑通
             验证：一个 token 流能实时出现在 Vue 上

Phase 2 ──── 单 Agent Loop
             目标：Think → Tool → Observe 完整闭环
             验证：LLM 发出的 tool call 能让 Go 执行并回传

Phase 3 ──── UI 可视化层
             目标：步骤状态树、可折叠、高亮
             验证：运行一个复杂任务，你能看到每一步

Phase 4 ──── 多Agent并发
             目标：多个并行的 Agent，实时更新
             验证：一个任务拆成2个agent并行，结果正确合并

Phase 5 ──── Agent配置 + 工具注册系统
             目标：动态注册 tool、配置 agent API endpoint
             验证：无需改代码，json/yaml 配置新 agent

Phase 6 ──── 打磨层（按需）
             ? 日志持久化（Postgres/Badger）
             ? Cost 追踪
             ? Prompt template
             ? Session 管理
             ? 权限 / 配额
			 ? 可观测性 持续优化

  ## Open Questions

  - `run_shell` 的沙箱方案（docker / firecracker / 仅 win 本地？）— Phase 5 再定
  - 前端 Vue 状态管理（Pinia vs reactive store）— Phase 2 决定
  - Agent 间通信协议（Agent A 调用 Agent B 的输入输出格式）— Phase 4 细化
  - 前端 Markdown 渲染库（markdown-it / marked + 自定义组件）— Phase 2 决定
  EOF)

  ## Phase 0: 项目骨架 & 打通 WebSocket 通信

  ### Task 0.1: Go 项目初始化
  - Initialize Go module
  - Create directory structure: cmd/server/, internal/{agent,runtime}, pkg/{event,db,tool}, web/
  - Add dependencies: modernc.org/sqlite, gorilla/websocket

  ### Task 0.2: SQLite 初始化 + Schema 创建
  - Create database file at `data/app.db`
  - Initialize all tables: agents, tasks, steps, tools, conversations, files
  - Implement DB access layer (CRUD interface)

  ### Task 0.3: Event Schema 定义
  - Define AgentEvent struct and EventType enum
  - Implement JSON serialization/deserialization

  ### Task 0.4: WebSocket Server (Go)
  - Implement WebSocket handler at `/ws`
  - Handle connection lifecycle (connect, message, disconnect)
  - Broadcast mechanism for sending events to connected clients

  ### Task 0.5: Vue 3 + Vite 初始化
  - Create Vue 3 + TypeScript project in web/
  - Install dependencies: vue-router, pinia (或 reactive store)
  - Basic layout: sidebar (cases + history) + main (agent tree)

  ### Task 0.6: WebSocket Client (Vue)
  - Implement WebSocket composable
  - Handle connect/disconnect/reconnect
  - Event router to dispatch events to stores

  ### Task 0.7: Token 流端到端验证
  - Go: hard-code a simple SSE/WS stream that emits test tokens
  - Vue: render tokens in real-time with typewriter effect
  - Verify: typed text appears smoothly on page

# 重要规则(长期记忆-铁律)
每次内容产出都要提交到git
