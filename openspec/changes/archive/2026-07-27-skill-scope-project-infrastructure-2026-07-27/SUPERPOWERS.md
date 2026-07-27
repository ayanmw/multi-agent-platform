# Execution Discipline — superpowers

本 Spec 实施时必须遵循 superpowers 方法论核心步骤：

## 1. Brainstorming（按需）

如遇到 scope/workdir 匹配规则、project 与 session 优先级等设计疑义，先调用 `/brainstorming` skill 或简短自问自答，记录决策到 `design.md`。

## 2. Writing-Plans / TDD

- **先写测试与类型**：
  - 先实现 `internal/skill/registry_test.go` 中的 `ResolveActiveSkills` 测试（红线）。
  - 再实现 `ResolveActiveSkills` 本身（绿线）。
  - 对 DB migration，先写 schema 变更与 CRUD 测试（最简单读写）。
  - 对 REST API，先写 `cmd/server/api_skill_project_test.go` 中的 HTTP 请求断言，再实现 handler。
  - 对 Runner 注入，先写最小 `runAgentLoopWithTurn` 的 EngineConfig 断言或 mock run。

- **重构循环**：每次绿线后检查是否可提取 helper、减少重复。

## 3. Systematic Debugging

若出现：
- `ActiveSkills` 为空导致 skill 不注入
- project skill 被错误注入到 global session
- `SkillVariables` nil 导致 Renderer panic

使用以下步骤：
1. 在 `ResolveActiveSkills` 入口打印 registry 全部 skill 与过滤条件。
2. 在 `engine.go` 渲染点打印 `cfg.ActiveSkills`、`cfg.SkillVariables`。
3. 确认 `cmd/server/runner.go` 是否从 session 正确解析 projectID/workdir。
4. 用 `dlv` 非交互模式（参考 memory `dlv-debug-go-method.md`）Attach 到 server 复现问题。

## 4. Code Review

完成编码后，对照以下清单自我 review：

- [ ] 新增字段都已落 DB migration，旧数据有默认值。
- [ ] `ResolveActiveSkills` 对空 projectID/workdir 不误过滤。
- [ ] `SkillVariables` 在 worktree 场景中是否使用 holder.Get() 后的实际目录？（Spec 5 进一步细化，本次使用 runner 已解析 workdir 即可。）
- [ ] REST API 新增 query/body 字段不会破坏现有 test。
- [ ] 不存在 map 并发写问题（registry 已有 RWMutex）。

## 5. Verification Before Completion

必须完成 `verify.md` 中全部检查并记录结果：

- 运行全部相关 go test，贴出结果摘要。
- 运行 mock cases-regression，贴出 PASS/SKIP/FAIL 统计。
- 至少完成一次手动验证：用 HTTP client 或前端发送请求，确认 project/global skill 隔离。

## 6. OpenSpec 生命周期收尾

- 在 `tasks.md` 中勾选所有任务。
- `openspec-verify-change`（或手工等价流程）：确认 spec、design、verify 一致。
- `openspec-archive-change` 到 `openspec/changes/archive/`。
- Git commit：`Phase skill-scope-project-infrastructure: 为 Skill 引入 Scope/ProjectID/WorkspaceDir`。
