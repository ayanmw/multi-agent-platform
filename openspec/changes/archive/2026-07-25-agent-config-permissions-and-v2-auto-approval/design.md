## Context

当前 Agent 配置 (`agents` 表) 已经有一个 `config JSON DEFAULT '{}'` 列，但 CRUD API 与前端表单都没有读写它。`TaskContract.Permissions` 只从 case 或 `DefaultContract` 构造，Agent 配置无法影响它。

v2 前端 `ApprovalDialog.vue` 已经支持 `autoApprove` prop，但 `App.vue` 里写死为 `false`。后端 `internal/runtime/approval_delegation.go` 在 supervisor 未配置时直接返回错误——这在 multi-agent worker 子 agent 的场景里会立刻终止任务。

本设计基于现有 `config` 列完成增强，不引入新的 DB schema 迁移。

## Goals / Non-Goals

**Goals:**
- 让 Agent `config.permissions` 成为 `TaskContract.Permissions` 的默认补充来源（OR 语义）。
- 当 worker 审批委托不可用时，回退到用户审批路径，避免任务硬失败。
- 在 v2 前端为 Agent 配置增加权限勾选面板。
- 在 v2 前端对 `TagPolicyRule` 触发的低风险 `network`/`mcp` 审批请求执行自动批准。

**Non-Goals:**
- 不修改 `TaskContract` 结构本身。
- 不新增 DB migration；复用现有 `agents.config` 列。
- 不改变 `ApprovalRule`/`DangerousCommandRule` 的审批语义；这些仍走人工确认。
- 不为每个 tool 单独配置审批策略；只按 tag/rule 做粗粒度自动审批。

## Decisions

### Decision 1: Agent 权限放在 `config.permissions`，而不是新增列
**Rationale:** `agents.config` 已存在，可以减少 schema 复杂度。`config` 字段作为任意 JSON，未来也方便扩展其它 Agent 级运行时覆盖。
**Alternative:** 在 `agents` 表新增独立权限列。Rejected：会造成更多 DB 字段和 migration，且 `config` 列已为此用途预留。

### Decision 2: 权限合并使用 OR 语义
**Rationale:** Agent 配置应是一种“默认放行”能力。如果case显式禁止某权限，request-level 仍可通过提交 `allowed_tools`/`permissions` 覆盖；从最小惊讶原则出发，Agent 配置只启用权限、不禁用权限。
**Alternative:** AND/覆盖语义。Rejected：会让 Agent 配置意外地关闭 case 已允许的权限，难于调试。

### Decision 3: 前端自动审批仅针对 `TagPolicyRule` + 非破坏类 tag
**Rationale:** `network`/`mcp` 在当前系统里被视为“需要授权但不危险”的能力。`ApprovalRule` 和 `DangerousCommandRule` 对应的是真正可能破坏环境或泄露敏感信息的操作，必须保留人工确认。
**Alternative:** 全局自动审批开关。Rejected：会削弱安全纵深防御。

### Decision 4: Worker 委托失败必须回退到 `handleApprovalRequired`
**Rationale:** 单 agent chat/run-case 路径使用 `Role=Leader`、`ApproverMode=user`，web_search 的审批本来就能走用户审批。multi-agent 子 agent 被配置成 `worker` + `leader` 审批，但当 leader 无法处理时，回退到用户审批比直接失败更合理。这也能让 v2 的自动审批修复一次性解决单 agent 和 multi-agent 子 agent 的问题。
**Alternative:** 修复 orchestrator 保证 supervisor handler 永远有值。Rejected：即使修复了 wiring，leader 离线或超时时也应优雅降级。

## Risks / Trade-offs

- **[Risk]** 滥用 `config.permissions` 的 Agent 可能会让管理员误以为“Agent 配置=安全边界”。  
  **Mitigation:** UI 文案明确说明“这是默认权限，叠加在 Case 权限之上”，并在权限勾选框旁标注风险等级。
- **[Risk]** 自动审批在 `network` tag 下可能让用户无感知地产生外部 HTTP 调用。  
  **Mitigation:** 自动审批仍通过 `system_info` 事件记录到时间轴；`ApprovalDialog` 的 auto-approve 分支仍 emit approve 控制消息，后端日志与 approvals 表均可审计。
- **[Risk]** `config` JSON 列没有 schema 约束，未来字段命名冲突。  
  **Mitigation:** 本次使用嵌套 `config.permissions` 命名空间；后续如需更多 Agent 级覆盖继续放在 `config` 下统一前缀。
- **[Risk]** 回退到用户审批在批处理/cron 等无前端场景仍会挂起 30s。  
  **Mitigation:** 该场景本来就应该配置 `TaskContract.AutoApprovePolicy=true` 或 `AllowNetwork=true`；本次改动不改 cron 路径，但让它不再报 supervisor 错误。

## Migration Plan

1. 更新 Go 后端：持久化/读取 `config`，合并权限，委托回退。
2. 更新 SQLite 中现有 Agent：无需变更，`config` 默认为 `{}` 即保持原行为。
3. 更新 v2 前端：AgentConfig 表单、useTaskStore 自动审批逻辑、App.vue 传递 autoApprove 决策。
4. 运行 `go test ./...` 与 `npm run build`（v2）。
5. 手动在 v2 创建一个 AllowNetwork=true 的 Agent，运行 `web-research` case 验证无弹窗且任务完成。

## Open Questions

- 是否需要把 `AutoApprovePolicy` 也暴露到 Agent config 里？本次先不加，避免一次改动过大。
- 是否需要在 v2 展示最近自动审批事件的历史？当前 `system_info` 事件已进时间轴，暂不做独立面板。
