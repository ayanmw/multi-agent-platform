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

- [x] 1.1: OpenAI-compatible LLM client (internal API, streaming SSE)
- [x] 1.2: Tool interface + ToolRegistry implementation
- [x] 1.3: 3 built-in tools: run_shell, write_file, read_file
- [x] 1.4: ReAct Loop engine (think → tool_call → observe → loop)
- [x] 1.5: Step state machine + event emission
- [x] 1.6: Agent config loading from SQLite + Task start flow

## Phase 2: 前端可视化

- [x] 2.1: AgentTree component (recursive, real-time updates)
- [x] 2.2: Typewriter component (LLMDelta streaming + markdown render)
- [x] 2.3: Step collapse/expand, status indicators, syntax highlight
- [x] 2.4: Pause/Resume/Cancel controls + metrics panel

## Phase 3: 预设 Cases & 配置页面

- [x] 3.1: 5 preset Task Cases definitions (code gen, research, multi-agent, chat, long task)
- [x] 3.2: CaseCard UI component + Run button handler
- [x] 3.3: Agent config CRUD page (name, prompt, model, endpoint, tools)
- [x] 3.4: Task history sidebar (list past tasks, click to restore)

## Phase 4: 多 Agent 并发

- [x] 4.1: Multi-Agent Task dispatch (parallel Agent execution)
- [x] 4.2: Frontend multi-tree rendering (side-by-side, color-coded)
- [x] 4.3: Agent-Agent messaging protocol

## Phase 5: 运行时 Agent 注册

- [x] 5.1: Runtime Tool Registration REST API
- [x] 5.2: AI-assisted tool registration (LLM generates schema → auto-register)

---

## Lifecycle Note

此 change 为初始 MVP spec（Phase 0-5）。经核查，proposal 列出的 6 个 capability（agent-runtime / websocket-bus / tool-registry / persistence-layer / agent-config / frontend-ui）与 tasks.md 全部任务均已在代码中完整实现，实际实现并已超越此范围（auth / memory / observability / cron / skill / todo / mcp / cost / pool 等属后续 change，见 `phase-6-tech-debt-completion` 等）。按"已有实现完全覆盖则标记完成并 archive"规则，全部勾选并归档为历史基线。
