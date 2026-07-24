# residual-cleanup Specification

## Purpose
TBD - created by archiving change cleanup-residual-bugs-and-docs. Update Purpose after archive.
## Requirements
### Requirement: 文档状态同步
变更 MUST 将项目说明文档与路线图中的 Phase 状态标记更新为与代码实际交付一致，区分"主体已完成"与"真实遗留项"，避免新会话 LLM 误判能力边界。

#### Scenario: CLAUDE.md 状态同步
- **WHEN** 变更完成后读取 `CLAUDE.md`
- **THEN** Phase UI-v2 与 Phase 7-H2 的状态描述必须反映主体已完成，并保留 real-LLM 可靠性/端到端冒烟等真实遗留说明

#### Scenario: ROADMAP.md 状态同步
- **WHEN** 变更完成后读取 `roadmaps/ROADMAP.md`
- **THEN** Phase UI-v2 / 7-H2 的进度标记必须更新为已完成，且待办列表中不得包含已被交付的阻塞项

### Requirement: OpenSpec spec Purpose 补全
对因历史归档而遗留 `Purpose: TBD` 的 spec 文件，变更 MUST 补全设计意图与范围说明，使新 reader 无需额外上下文即可理解该能力的"为什么"。

#### Scenario: multi-agent-orchestration Purpose 补全
- **WHEN** 变更完成后读取 `openspec/specs/multi-agent-orchestration/spec.md`
- **THEN** `## Purpose` 段落必须阐述编排层覆盖的静态/动态编排模式、可观测事件要求以及 mock 与 real-LLM 的能力边界

#### Scenario: workspace-worktree-isolation Purpose 补全
- **WHEN** 变更完成后读取 `openspec/specs/workspace-worktree-isolation/spec.md`
- **THEN** `## Purpose` 段落必须阐述 worktree 是主动叠加隔离层、LLM 主控生命周期以及 `WORKTREE_ENABLED=false` 的向后兼容性

### Requirement: 代码 TODO 清理与实现
变更 MUST 清理代码中已明确的三个 TODO，对可安全实现的问题给出确定性感知实现，对不可完成项给出决策说明。

#### Scenario: SourceMarket 接入 MCP Manager
- **WHEN** 服务启动并调用 `mcp.Manager.Install(ctx, source)
- **THEN** 若 `source` 类型为 `SourceMarket`，管理器 MUST 按 SourceMarket 协议完成安装，不再返回 "not implemented" observation

#### Scenario: stream-demo 工具注册
- **WHEN** 启动 stream-demo 运行
- **THEN** `web_fetch` 与 `web_search` 工具 MUST 已注册并可被 demo 的 Agent 调用，或该 TODO 被明确移除并改为文档说明

#### Scenario: checkShell 真实执行
- **WHEN** Harness policy 含 `checkShell` 且环境无 Docker 沙箱时
- **THEN** 系统 MUST 提供本地可运行的安全版检查，避免 TODO 导致 policy 永远无法触发

### Requirement: 残留问题追踪文档
变更 MUST 创建单一事实源文档 `RESIDUAL_ISSUES.md`，汇总所有已核实、已修复、已决策的残留项及其当前状态。

#### Scenario: 文档可发现
- **WHEN** 在仓库根目录查找 `RESIDUAL_ISSUES.md`
- **THEN** 该文件存在且包含分类清单（已修复、已决策、仍遗留含 owner/计划）

#### Scenario: 状态可追溯
- **WHEN** 阅读 `RESIDUAL_ISSUES.md`
- **THEN** 每个条目的状态、依据与下一步（如有）均清晰可追

