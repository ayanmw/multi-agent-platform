# Execution Discipline — superpowers

## TDD 顺序

1. 先写 `command_loader_test.go`：临时目录 + `.claude/commands/ops/new.md` → 期望 ID 为 `ops:new`（红）。
2. 实现 `CommandLoader` 扫描与 ID 生成（绿）。
3. 再写 frontmatter 解析测试（`skill`, `command` 覆盖）。
4. 实现 frontmatter 解析。
5. 写 `CommandRegistry` 基本 CRUD 测试，然后实现。
6. 写 `api_skill_command_test.go` REST 测试，然后实现 handler。
7. 前端先写 `SkillPicker.spec.ts`（props/emit），再实现组件。
8. 集成 `CommandBar` 的 `/` 触发测试与实现。

## 调试 checklist

命令 ID 不正确：
1. 打印 `relativePath` 与 `strings.TrimSuffix` 结果。
2. 检查 frontmatter `command` / `id` 优先级。

命令未被加载：
1. 确认 `LoadForWorkdir` 被 session hook 调用。
2. 打印扫描目录与发现文件列表。
3. 检查 `CommandRegistry.ListForWorkdir` 过滤条件。

invoke 后 skill 未启用：
1. 在 `POST /api/skill-commands/:id/invoke` 中打印 command.SkillID。
2. 调用 skill registry `UpdateState(enabled)` 并写 store。
3. 检查 runner 是否使用 `ResolveActiveSkills` 正确包含该 skill。

## Code Review 检查项

- [ ] 命令 ID 只使用 `[a-zA-Z0-9_:-]` 字符，避免路径遍历或特殊字符。
- [ ] `GET /api/skill-commands/:id/details` 返回 prompt 全文，受权限控制。
- [ ] 临时 skill 不写入 DB，source 使用 `command_temporary` 或类似标记。
- [ ] 前端 picker 按 Esc 可关闭，失焦可关闭。
- [ ] CommandBar 输入普通 `//` 文本不触发 picker。

## 完成前验证

- unit test 通过后贴出摘要。
- 至少一次手动 `/` 触发与 invoke 操作。
- 运行 mock regression 并贴出结果。

## OpenSpec / Git 收尾

- tasks 全部勾选
- openspec-verify-change
- openspec-archive-change
- commit: `Phase skill-command-system: 支持 .claude/commands 命令触发器`
