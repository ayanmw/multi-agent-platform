# Implementation Tasks

<!-- 
  TASK TRACKING
  Run: openspec tasks done <task-id>  → to mark complete
  Run: openspec tasks list           → to see all tasks & status
-->

## Phase 0: 项目骨架 & 打通 WebSocket 通信 ✅ COMPLETE

- [x] 0.1: Initialize Go module + directory structure (cmd/, internal/, pkg/, web/)
- [x] 0.2: Add Go dependencies (gorilla/websocket, modernc.org/sqlite)
- [x] 0.3: SQLite schema initialization (all 6 tables: agents, tasks, steps, tools, conversations, files)
- [x] 0.4: Define AgentEvent struct + EventType enum (18 event types)
- [x] 0.5: WebSocket server at /ws (connect/disconnect/broadcast via Hub)
- [x] 0.6: Vue 3 frontend initialized (web/index.html with CDN Vue 3, dark theme)
- [x] 0.7: Vue WebSocket composable + event router (reactive state tree)
- [x] 0.8: Hard-coded token stream end-to-end test (/api/tasks stream-demo)

**Commit**: "feat: initial project scaffold - Phase 0 WS demo working"

## Phase 1: Agent Loop 核心引擎

- [ ] 1.1: OpenAI-compatible LLM client (internal API, streaming SSE)
- [ ] 1.2: Tool interface + ToolRegistry implementation
- [ ] 1.3: 3 built-in tools: run_shell, write_file, read_file
- [ ] 1.4: ReAct Loop engine (think → tool_call → observe → loop)
- [ ] 1.5: Step state machine + event emission
- [ ] 1.6: Agent config loading from SQLite + Task start flow

## Phase 2: 前端可视化

- [ ] 2.1: AgentTree component (recursive, real-time updates)
- [ ] 2.2: Typewriter component (LLMDelta streaming + markdown render)
- [ ] 2.3: Step collapse/expand, status indicators, syntax highlight
- [ ] 2.4: Pause/Resume/Cancel controls + metrics panel

## Phase 3: 预设 Cases & 配置页面

- [ ] 3.1: 5 preset Task Cases definitions (code gen, research, multi-agent, chat, long task)
- [ ] 3.2: CaseCard UI component + Run button handler
- [ ] 3.3: Agent config CRUD page (name, prompt, model, endpoint, tools)
- [ ] 3.4: Task history sidebar (list past tasks, click to restore)

## Phase 4: 多 Agent 并发

- [ ] 4.1: Multi-Agent Task dispatch (parallel Agent execution)
- [ ] 4.2: Frontend multi-tree rendering (side-by-side, color-coded)
- [ ] 4.3: Agent-Agent messaging protocol

## Phase 5: 运行时 Agent 注册

- [ ] 5.1: Runtime Tool Registration REST API
- [ ] 5.2: AI-assisted tool registration (LLM generates schema → auto-register)
