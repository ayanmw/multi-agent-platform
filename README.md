# Multi-Agent Platform

A Go + Vue 3 real-time multi-agent platform for learning and internal use.

## Structure

- `/cmd/server` - Main entry point
- `/internal/` - Internal business logic
  - `/agent` - Agent runtime and ReAct loop
  - `/runtime` - Task and step state management
  - `/tool` - Tool registry and built-in tools
  - `/ws` - WebSocket hub
  - `/llm` - LLM client (OpenAI-compatible)
  - `/config` - Configuration management
- `/pkg/` - Public packages
  - `/event` - Event types and serialization
  - `/db` - Database layer
- `/web/` - Vue 3 frontend (Vite + TypeScript)
- `/data/` - SQLite database
- `/storage/` - File storage

## Quick Start

### Backend
```bash
cd /cmd/server
go run main.go
```

### Frontend
```bash
cd web
npm install
npm run dev
```

## Design Documents

See `/openspec/changes/multi-agent-platform/` for:
- `proposal.md` - Project goals and scope
- `design.md` - Technical design decisions
- `specs/` - Detailed specifications
- `tasks.md` - Implementation tasks
