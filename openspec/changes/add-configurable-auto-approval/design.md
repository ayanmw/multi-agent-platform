## Context

v2 前端当前在 `useTaskStore.ts` 中硬编码了一个低风险的 `TagPolicyRule` 自动审批白名单（`network`、`mcp`）。`ApprovalDialog.vue` 虽然接收 `autoApprove` prop，但 `App.vue` 写死传入 `false`。后端 `WebSocketApprovalHandler.WaitForDecision` 使用 30 秒单阶段超时，超时后只能自动拒绝。用户希望在不降低安全性的前提下，能够显式控制哪些风险标签可以被自动通过，并且在前端离线或响应慢时后端也能基于同样策略兜底。

## Goals / Non-Goals

**Goals:**
- 在 `CommandBar` 左侧 `Options` 浮窗中新增「自动审批」配置区，列出可选风险标签。
- 支持一键全选 / 取消全选；至少选中一个标签时才算开启，空选等价于关闭。
- 配置持久化在浏览器 LocalStorage（用户名级），页面刷新后保留。
- 前端收到 `approval_required` 时，按用户选中的标签集合判断，匹配则自动 approve，不匹配则弹窗。
- 后端 `WaitForDecision` 在首次 5 秒内未收到前端决定时，若审批请求匹配自动审批策略，则自动批准并返回；否则继续等待剩下的 25 秒（默认总共 30 秒），再超时拒绝。
- 前后端判定规则保持一致：仅处理 `TagPolicyRule`，且工具 tags 全部被包含在用户选中的自动审批标签集合内；未选中任何标签 = 不自动审批。

**Non-Goals:**
- 本次不引入每个 Agent / 每个 Session 独立的自动审批配置；配置先走前端 LocalStorage，未来可下沉到 Agent config。
- 不修改 policy rule 的判定逻辑，只改 `shouldAutoApprove` 的输入源。
- 不改动审批对话框的样式和超时 toast 逻辑（仍保留 28s 前端倒计时）。

## Decisions

1. **配置只存在前端 LocalStorage，但判定逻辑同时在前端和后端复用一份。**
   - 理由：后端当前没有按 user 维度的轻量设置 API，新增数据库表/REST 会扩大变更范围。LocalStorage 足够满足个人工作台场景；后端只负责在前端未响应时按前端已经下发的策略兜底。
2. **后端采用「两段式等待」：先等 5s，再等剩余时间。**
   - 理由：用户想要「5s 没反应就自动审批」，而不是把总超时改成 5s。保留 30s 总窗口可以兼容慢网络或用户正在看对话框的情况；5s 后只是额外做一次自动审批检查。
3. **自动审批只认 `TagPolicyRule`，且要求全部 tags 都被用户勾选。**
   - 理由：这是现有 `shouldAutoApprove` 的保守语义，避免部分匹配的狡猾组合绕过审批。如果用户希望能部分匹配，应升级语义；本次先延续当前安全水位。
4. **标签候选项以常量硬编码在前端，而不是从后端工具 tag API 动态拉取。**
   - 理由：当前工具 `tags` 已经稳定为 `network`、`mcp`、`exec`、`*dangerous`、`*shell` 等类别，构建一个用户可感知的候选集（低风险 + 高风险去重展示）比展示所有工具 tag 更可读。高风险 tag 仍可展示但禁用或置灰？不，本次允许选择任何 tag，但默认只勾选低风险，用户自行负责。
5. **后端审批处理器需要一个可注入的 `AutoApprovalPolicy`。**
   - 理由：`WebSocketApprovalHandler` 当前不感知配置。新增 `AutoApprover` 接口/函数可以把 "tags 集合 + rule 名" 的判定与 handler 解耦，便于测试和后续接入持久化。

## Risks / Trade-offs

- [Risk] 用户把所有 tag（含 `exec`、`shell`）都勾上后等于完全关闭了审批，误操作可能导致高危命令自动执行。
  → Mitigation: UI 中高风险 tag 用红色/警告样式提示；默认只勾选 `network`、`mcp`；选中高风险 tag 时顶部显示确认提示。
- [Risk] 后端在 5s 后自动批准时，前端已经快弹窗了，可能导致前端随后收到 late approve/已结束事件。
  → Mitigation: 5s 自动批准只针对匹配低风险 tags 的请求（用户已明确授权）；handler 清理 pending 后前端再发决定会被默认忽略（channel 已满或已删除），行为正确。
- [Risk] LocalStorage 配置不同浏览器间不共享。
  → Mitigation: 这是个人工作台可接受；未来迁移到 Agent config 持久化时，LocalStorage 可作为缓存层。
- [Risk] 5s 兜底窗口对国产模型 reasoning 慢启动场景可能太短。
  → Mitigation: 5s 等待的是「前端控制消息」，不是 LLM 推理；如果前端已经离线，5s 后兜底自动批准是预期行为。只在「前端在线但没来得及点」时才可能过早，低风险标签已授权，可接受。

