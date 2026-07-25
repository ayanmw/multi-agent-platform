## Why

当前 v2 前端缺少 v1 曾支持的自动审批能力：当 Agent 调用 `web_search`/`web_research` 等带 `"network"` tag 的工具时，`TagPolicyRule` 要求 `TaskContract.Permissions.AllowNetwork=true`，但普通 chat/run-case 路径默认未授予该权限，导致用户要么看到审批弹窗、要么在 multi-agent worker 路径直接报错 "worker 未配置 supervisor，无法委托审批"。本变更让 Agent 配置可携带默认权限，并在 v2 前端支持按规则自动审批，恢复并改进 v1 的自动审批体验。

## What Changes

- **Agent 配置新增 `config.permissions` 字段**：在后端 `AgentRecord.Config` 中增加标准化的 `permissions` 子结构，对应 `TaskPermissions` 的 5 个权限位。
- **Agent CRUD API 透传 `config`**：`agentRequest` 增加 `config` 字段并在 Insert/Update 时持久化到 `agents.config` JSON 列；Query 时回传完整 `config`。
- **启动任务时合并 Agent 默认权限**：`startChatTask` 与 `handleRunCase` 在构建 `TaskContract` 后，将当前 Agent `config.permissions` 中显式启用的权限按 OR 语义合并到 `contract.Permissions`。
- **审批委托失败时回退用户审批**：`internal/runtime/approval_delegation.go` 在 worker 未配置 supervisor 或委托超时时，不再直接返回错误，而是回退到 `handleApprovalRequired`（用户审批 UI）。
- **v2 前端 AgentConfig 增加权限配置面板**：在 Agent 编辑表单中新增 5 个权限复选框（Network / File Write / File Delete / Shell / Dangerous Shell）并持久化到 `config.permissions`。
- **v2 前端恢复自动审批**：当 `approval_required` 事件的 rule 为 `TagPolicyRule` 且 tool tag 全部落在用户/Agent 允许的类别内时，自动发送 approve 控制消息；其余高风险审批仍显示弹窗等待用户确认。
- **保持向后兼容**：`config` 列为 JSON 并已有默认值 `{}`，未配置权限的 Agent 行为保持不变；`web-research` 等内置 case 仍按自身 `Contract.Permissions` 生效。

## Capabilities

### New Capabilities

- `agent-config-permissions`: Agent 配置可声明默认任务权限，运行时合并到 TaskContract。
- `v2-auto-approval`: v2 前端根据规则自动批准低风险审批请求，高风险仍走人工确认。
- `approval-delegation-fallback`: worker 审批委托失败时回退到用户审批，避免硬错误。

### Modified Capabilities

- `task-cases`: 无 spec 级需求变更；仅说明内置 case 的 `Contract.Permissions` 与 Agent 默认权限的叠加语义。

## Impact

- **后端**：`pkg/db/persistence.go`（无需 schema 迁移，config 已存在）、`cmd/server/api.go`、`cmd/server/tasks_api.go`、`internal/runtime/approval_delegation.go`。
- **前端**：`web/v2/src/composables/useAgentStore.ts`、`web/v2/src/components/AgentConfig.vue`、`web/v2/src/composables/useTaskStore.ts`、`web/v2/src/App.vue`。
- **API**：`/api/agents` 的请求/响应体增加 `config` 字段；运行时行为变化对现有未使用 `config` 的调用方透明。
- **测试**：需要补充 `cmd/server` 的 runner 集成测试与 `internal/runtime/approval_delegation.go` 的单元测试。
