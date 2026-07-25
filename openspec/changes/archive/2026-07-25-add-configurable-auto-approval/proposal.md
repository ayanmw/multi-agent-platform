## Why

当前 web/v2 的自动审批逻辑是硬编码的：只有 `TagPolicyRule` 且工具 tags 全部为 `network`/`mcp` 时才会自动通过。用户无法在前端控制哪些风险标签可以被自动审批，也无法一键开关；同时后端在 30s 审批超时后只能自动拒绝，无法在无前端连接或前端未及时响应时根据配置自动批准低风险操作。为了一个更安全、更灵活的白盒 Agent 体验，需要把自动审批做成可配置、前后端一致的能力。

## What Changes

- 在 web/v2 `CommandBar` 左侧 `Options` 按钮浮窗中新增「自动审批」配置区。
- 提供可选的风险标签复选列表（如 `network`、`mcp`），支持一键全选 / 取消全选。
- 至少选中一个标签时「自动审批」才视为开启；未选中任何标签时等于关闭。
- 前端继续按当前策略自动 approve 匹配标签的 `approval_required` 事件。
- 后端 `WebSocketApprovalHandler.WaitForDecision` 在第一个 5s 等待后，若前端未响应且配置了自动审批，则自动批准匹配的低风险审批请求；否则继续等待后续 25s（总共默认 30s）再按原逻辑超时拒绝。
- 修改 `agent-config-permissions` 相关 spec/实现，把自动审批标签集合纳入配置范畴。

## Capabilities

### New Capabilities

- `configurable-auto-approval`: v2 前端 Options 浮窗中的自动审批 UI 配置，包含标签选择、一键全选、空选关闭逻辑。
- `backend-auto-approval`: 后端审批等待流程中读取自动审批策略，在 5s 短超时窗口内自动批准匹配标签的请求。

### Modified Capabilities

- `v2-auto-approval`: 当前硬编码的 low-risk tags 需改为从本地配置读取（LocalStorage / app 级状态），spec 要求从「前端自动审批固定白名单」扩展为「用户可配置标签集合」。
- `agent-config-permissions`: 审批策略配置成为 Agent / Session 配置的一部分（可选持久化到 SQLite；本次至少通用地走 API / LocalStorage）。

## Impact

- 前端：`web/v2/src/components/CommandBar.vue`、`OptionsFlyout.vue`（或相关浮窗组件）、`useTaskStore.ts`、`useAgentStore.ts`。
- 后端：`internal/harness/approval.go`（`WebSocketApprovalHandler`）、`internal/ws/hub.go`（控制消息处理）、`pkg/db`（新增/复用配置表保存自动审批标签）。
- REST / WebSocket：可能新增 `/api/settings/auto-approval` 或作为 session config 的一部分下发；event 结构不变，但 `system_info` 审批流程行为变化。
- 测试：新增/修改 `useTaskStore.autoapprove.spec.ts`、`approval_test.go`、`ws-smoke.go` 或 policy smoke 测试。
