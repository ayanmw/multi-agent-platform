# Execution Discipline — superpowers

## TDD 顺序

1. 先扩展 `token_estimate_test.go`：期望 `BuildContextWindowSnapshot` 返回 `SkillBlocks`（红）。
2. 修改 `BuildContextWindowSnapshot` 签名与实现（绿）。
3. 写 `engine_skill_test.go`：启用 skill 启动 engine，验证 `injectedSkillBlocks` 长度与内容（红）。
4. 在 `NewEngine` 实现 skill block 记录（绿）。
5. 测试 `skill_rendered` 事件广播（红）。
6. 实现事件广播（绿）。
7. 前端先写类型变更，再写 `ContextWindowPanel` 渲染测试与实现。

## 调试 checklist

skill_blocks 为空：
1. 检查 `NewEngine` 中 `cfg.ActiveSkills` 是否非空。
2. 检查 `cfg.SkillRegistry.Get(id)` 是否成功。
3. 检查模板名是否为 `system_prompt` 或 `task_prompt`。
4. 检查 renderer.Render 结果是否非空。

skill_blocks token 数为 0：
1. 确认 `len(block.Content) > 0`。
2. 确认复用 `EstimateTokenCount` 计算。

前端未显示：
1. 检查 WS event data 中是否有 `skill_blocks`。
2. 检查 `ContextWindowSnapshotData` 类型定义。
3. 检查 `useContextWindow` 是否正确把 data 传给 panel。

## Code Review 检查项

- [ ] `BuildContextWindowSnapshot` 新签名所有调用点已更新。
- [ ] 不破坏 `session_messages` 重建 snapshot 的路径。
- [ ] skill_rendered 事件的 `task_id` / `agent_id` 字段正确。
- [ ] 前端空态处理完善。
- [ ] Skill badge 匹配逻辑不会误判普通 system prompt。

## 完成前验证

- 跑完全部相关测试。
- 至少一次手动启用 skill 并查看 ContextWindowPanel skill 区。
- mock regression 通过。

## OpenSpec / Git 收尾

- tasks 全部勾选
- openspec-verify-change
- openspec-archive-change
- commit: `Phase skill-context-window-stats: 上下文窗口独立统计 skill 注入`
