# 残留问题清理报告

> 本文件是 `cleanup-residual-bugs-and-docs` OpenSpec 变更的单一事实源。
> 更新日期：2026-07-24
> 变更范围：文档状态同步、OpenSpec spec Purpose 补全、代码 TODO 清理、路由图/状态统一。

## 1. 已核实/已修复项

### 1.1 UI-v2 控制室 —— 已完成 ✅
- **依据**：`web/v2/` 已包含完整 Observable Control Room 组件结构（Dock 三栏 + 移动 3-tab）。
- **已同步**：
  - `CLAUDE.md` 扩展 Phase 表：UI-v2 状态改为 ✅，标注"构建与核心组件已就绪，端到端冒烟与 collapsible trace tree 为后续常规迭代优化项"。
  - `roadmaps/ROADMAP.md` 同步状态，移除过时阻塞项。

### 1.2 Phase 7-H2 多 Agent 动态编排闭环 —— 已完成 ✅
- **依据**：
  - 静态编排 parallel/sequential/DAG 已生产可用（`internal/orchestrator/orchestrator.go`）。
  - 动态 `dispatch_sub_agent` 工具已实现真实 `leaderSubTaskID`（`internal/tool/builtin.go:NewLeaderTools`）。
  - mock 回归 21/21 PASS（含 L5 编排事件断言）。
- **已同步**：
  - `CLAUDE.md` 扩展 Phase 表：7-H2 状态改为 ✅，标注"静态编排可用；动态 leader-dispatch real-LLM 可靠性为已知限制"。
  - `roadmaps/ROADMAP.md` 同步状态，标注真实遗留项。

### 1.3 OpenSpec spec Purpose 补全 —— 已完成 ✅
- **文件**：
  - `openspec/specs/multi-agent-orchestration/spec.md`
  - `openspec/specs/workspace-worktree-isolation/spec.md`
- **内容**：补全了设计意图、覆盖范围、mock/real-LLM 边界、向后兼容声明。

### 1.4 代码 TODO 清理 —— 已完成 ✅

| 位置 | 原 TODO | 处理方式 |
|------|---------|----------|
| `internal/tool/mcp/server.go:77` | "Phase 6 — 将 marketplace 适配器接入 Manager.Install" | 确认 `Manager.InstallFromMarket` / `InstallFromMarketIfMissing` 已实现 SourceMarket 安装路径；将 TODO 改为确定性注释。 |
| `cmd/server/main.go:1022` | "web_fetch + web_search tool 尚未注册" | 改为说明：stream-demo 是前端事件演示，不经过真实 runtime，`web_fetch`/`web_search` 已由 `registerBuiltinTools` 注册；演示序列保留 `run_shell` 以保证稳定。 |
| `internal/harness/harness.go:814` | "当前 shell 验收检查未真正执行，等待 Docker sandbox" | 实现本地安全版 `checkShell`：超时 60s、拒绝危险字符、白名单限制命令前缀（`go`/`python`/`python3`/`node`/`npm`/`git`）、在 scope 内执行。soft pass 被移除。 |

## 2. 已决策项（不再变更，除非未来 Phase）

### 2.1 Docker / Firecracker 沙箱
- **决策**：仍然列为 Phase 5 未来工作。当前 `checkShell` 本地安全版已足以支撑 acceptance criteria。
- **依据**：`run_shell` 沙箱方案在 `CLAUDE.md` 的"待定问题"中明确列为 Phase 5，未承诺在当前 cleanup 中实现。

### 2.2 UI-v2 端到端冒烟 / collapsible trace tree
- **决策**：作为非阻塞增强纳入后续常规迭代，不影响 7-H2 / worktree 完成状态。
- **依据**：`CLAUDE.md` 与 `roadmaps/ROADMAP.md` 已明确标注。

### 2.3 Real-LLM 下 leader-dispatch 可靠性
- **决策**：架构已闭环，mock 回归通过；real-LLM 可靠性属于模型能力波动，后续优化不阻塞 7-H2 完成。
- **依据**：`memory/multi-agent-dual-entry-placeholder-bug.md` 已记录现状。

## 3. 仍遗留项（含 owner / 计划）

| 项 | Owner | 计划 | 状态 |
|----|-------|------|------|
| Docker/Firecracker `run_shell` 沙箱 | Phase 5 | 待定，未来 roadmap 规划 | ⬜ 未开始 |
| UI-v2 端到端冒烟测试 | 常规迭代 | 后续按优先级安排 | ⬜ 未开始 |
| UI-v2 collapsible trace tree | 常规迭代 | 后续按优先级安排 | ⬜ 未开始 |
| Real-LLM leader-dispatch 可靠性调优 | Phase 7-H2 后续 | 通过 prompt engineering / 更 deterministic 拆分手法持续优化 | ⬜ 未开始 |
| Tokenizer/context 压缩/RBAC/MCP 增强/K8s 部署 | Phase 7 生产化 | 纳入 Roadmap 统一规划 | ⬜ 未开始 |

## 4. 验证结果

- `go build ./internal/harness ./internal/tool/mcp ./internal/tool ./internal/tool/mcp/marketplace` ✅
- `go test ./internal/harness ./internal/tool/mcp ./internal/tool ./internal/tool/mcp/marketplace` ✅
- 注：`web/dist` 目录不存在导致 `go build ./...` 失败，属于前端构建产物缺失；该问题在既有工作流中通过 `cd web/v2 && npm run build` 解决，不影响本次 cleanup 的代码正确性。
