# Proposal: Context Window Skill 统计与事件同步

## 问题

当前 `context_window_snapshot` 只统计 messages 的 role/token 分布，skill 注入的 system_prompt 内容被埋没在第一条 system message 中，前端无法独立看出"有多少 token 被 skill 占用"。同时，skill 启用/注入等状态变更没有通过 SSE/WS 事件同步到前端，导致 ContextWindowPanel 与用户管理面板不能及时刷新。

## 目标

1. Engine 注入 skill 时记录每个 skill 的渲染后内容、预估 token 数。
2. `context_window_snapshot` 数据结构新增 `skill_blocks` 数组，展示每个 skill 对上下文的贡献。
3. 广播 `skill_rendered` 事件，在每次 run 启动 skill 注入完成时发送，包含 blocks 数据。
4. 前端 `types/events.ts` 补齐 skill 生命周期事件与 snapshot skill_blocks。
5. ContextWindowPanel 独立渲染 skill 注入区与 badge。
6. 新增 `useSkillEvents.ts` 订阅 skill 事件并维护 reactive 状态。

## 成功标准

- 启用 skill 后运行 task，`GET /api/tasks/:id/context_window` 的 `skill_blocks` 非空。
- ContextWindowPanel 正确展示 skill 名称、模板名、token 数、占比。
- `skill_enabled` / `skill_disabled` / `skill_loaded` / `skill_rendered` 等事件在 WS 中可被订阅。
- `go test ./internal/runtime ./internal/llm ./cmd/server` 通过；mock 回归 21/21 PASS。

## 关联变更

- 依赖 Spec 1 的 `EngineConfig.SkillVariables` 与 `ResolveActiveSkills`。
- Spec 6（前端 UI）依赖本 Spec 的 snapshot 字段扩展和事件类型。
