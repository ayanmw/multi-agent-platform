## Why

项目经过 Phase 8-B 架构收尾与 worktree 隔离后，文档、memory 与代码中仍残留大量"进行中"标记与未实现 TODO。这些不确定信息会导致新会话 LLM 重复追问旧状态、误判能力边界、甚至重复修复已修复的问题。本变更旨在系统梳理并同步所有残留状态：先更新文档与状态标记，再修复确定可落地的小问题，对未实现项给出方案并实现，彻底终结不确定信息。

## What Changes

1. **文档状态同步（最容易）**
   - 更新 `CLAUDE.md` 中 Phase UI-v2 与 Phase 7-H2 的状态描述，区分"已完成"与"真实遗留"。
   - 更新 `roadmaps/ROADMAP.md` 中 Phase UI-v2 / 7-H2 的进度与待办，补齐已完成项。
   - 更新 `memory/multi-agent-dual-entry-placeholder-bug.md`，明确 7-H2 已完成部分与 real-LLM 可靠性遗留的现状。

2. **OpenSpec 规格补全（容易）**
   - 为 `openspec/specs/multi-agent-orchestration/spec.md` 与 `workspace-worktree-isolation/spec.md` 补全 `Purpose` 段落。

3. **代码 TODO 清理与实现（中等）**
   - `internal/tool/mcp/server.go:77`：将 `SourceMarket` 接入 `Manager.Install`。
   - `cmd/server/main.go:1022`：在 stream-demo 中注册 `web_fetch` / `web_search`（或删除该 demo 的 TODO 并改为说明）。
   - `internal/harness/harness.go:814`：实现 `checkShell` 真实执行（在 Docker sandbox 不可用场景下提供本地可运行的安全版本）。

4. **新增追踪文档（容易）**
   - 在变更内创建 `RESIDUAL_ISSUES.md`，列出所有已核实、已修复、已决策的项，作为单一事实源。

## Capabilities

### New Capabilities
- `residual-cleanup`: 项目级残留问题清理与状态同步流程。

### Modified Capabilities
- `multi-agent-orchestration`: 更新 spec Purpose，反映 7-H2 已完成范围与已知 real-LLM 限制。
- `workspace-worktree-isolation`: 更新 spec Purpose，反映 worktree 隔离已交付。

## Impact

- `CLAUDE.md`、`roadmaps/ROADMAP.md`：状态描述同步。
- `openspec/specs/`：spec Purpose 补全。
- `internal/tool/mcp/server.go`、`cmd/server/main.go`、`internal/harness/harness.go`：TODO 清理与实现。
- `memory/*.md`：memory 状态更新。
- 无破坏性 API 变更。
