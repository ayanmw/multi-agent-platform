## Why

需要构建一个可本地训练、调试和观察 AI Agent 行为的多 Agent 系统。作为学习工具时，需要能实时看到每一步的思考过程、工具调用、token 流；作为产品使用时，需要可配置、可扩展、可持久化。

基于用户已有内部 API (`aicoding.dobest.com/v1`, deepseek-v4-flash)，用 Go 做后端 (高并发/强类型/部署简单)，Vue 3 做前端 (实时渲染/状态可视化)，在单仓库内快速迭代出一套可工作的 MVP。

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

## Capabilities

### New Capabilities

- `agent-runtime`: 核心 Agent Loop 引擎（ReAct + 状态机），支持 ReAct Loop、Tool Dispatch、Memory 管理
- `websocket-bus`: WebSocket 事件总线，定义 AgentEvent 协议，全双工实时推送 LLM token / step 状态 / tool 结果
- `tool-registry`: Tool 注册与调度系统，JSON Schema 参数验证，运行时注册接口，执行结果结构化返回
- `persistence-layer`: SQLite 持久化层，存储 Agent 配置、Task/Step 历史、Tool Schema、Conversation 日志
- `agent-config`: Agent 配置管理 CRUD，运行时 API 注册，多 API endpoint / model 隔离
- `frontend-ui`: Vue 3 + Vite 前端，Case Cards、Agent Tree 可视化、TypeWriter 渲染、配置页面

### Modified Capabilities

_(无，此为全新系统)_

## Impact

- **新依赖**: Go 1.23+, modernc.org/sqlite (纯Go SQLite), gorilla/websocket, protobuf (预留 gRPC), Vue 3 + Vite + TypeScript
- **API**: 后端暴露 WebSocket endpoint (`/ws`)，REST API 用于 Agent 配置 CRUD、Task 历史查询
- **数据**: local SQLite 文件 (`data/app.db`)，Markdown 文件存储于 `storage/`
- **外部服务**: 内部 LLM API (`https://aicoding.dobest.com/v1`, deepseek-v4-flash)
- **架构**: 单仓库 monorepo，go/ 和 web/ 子目录
