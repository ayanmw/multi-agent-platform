# Execution Discipline — superpowers

本 Spec 实施时遵循 superpowers 核心步骤。

## TDD 顺序

1. 先写 `file_loader_test.go` 中"创建临时目录 + `LoadGlobal` 能发现 skill"的测试（红）。
2. 实现 `FileLoader` 与 `parseSkillFile`（绿）。
3. 测试 `LoadForWorkdir` 与 `RefreshAll` 的卸载行为（红）。
4. 实现卸载与重扫逻辑（绿）。
5. 测试 scan-config REST API（红）。
6. 实现 settings 与 scan API（绿）。

## 调试 checklist

如发现 skill 不加载：
1. 打印 `getEnabledScanDirs()` 返回值，确认配置正确读取。
2. 打印扫描目录绝对路径，确认 workdir 已解析。
3. 打印解析出的 `Skill.SourceURL` 与 `Scope`。
4. 检查 `registry.List` 是否包含，以及 `ResolveActiveSkills` 是否过滤掉（Spec 1）。

如发现 refresh 后旧 skill 残留：
1. 在 `RefreshAll` 中先遍历 registry，删除所有 `source=local_file` 且匹配 workdir 的 skill。
2. 确认 `Registry.Unregister` 正确清理内存 map。

## Code Review 检查项

- [ ] 不直接写死 4 个目录字符串，从 `DefaultSkillScanDirs` 读取。
- [ ] frontmatter 解析失败能优雅降级（不要整个扫描失败），可记 `InvalidReason`。
- [ ] 文件读取使用 `os.ReadFile`，避免大文件占用；必要时限制文件大小（MVP 可不做）。
- [ ] settings 表未初始化时使用默认值，不报错。
- [ ] 不加 race；registry 操作沿用已有锁。

## 完成前验证

必须完成 `verify.md` 全部检查，并贴出：
- unit test 结果
- go build 结果
- cases-regression 结果
- 至少一次手动扫描测试结果

## OpenSpec / Git 收尾

- tasks 全部勾选
- openspec-verify-change
- openspec-archive-change
- commit: `Phase skill-filesystem-scanner: 支持 .claude/skills 等目录自动发现`
