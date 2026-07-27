# Proposal: 文件系统 Skill 扫描器

## 问题

当前 skill 只有两个来源：`built_in` 和 `local_db`。Claude Code 用户已经习惯把 skill 放在项目目录的 `.claude/skills/<name>/SKILL.md` 中随仓库共享，也期望 `.agents/skills`、`.agent/skills`、`.opencode/skills` 等常见目录被自动识别。本次改造需要让 backend 把文件系统 skill 作为 `local_file` source 加载到 registry，供前端只读查看与 Engine 注入；同时提供可配置扫描目录与手动刷新机制。

## 目标

1. 新增 `internal/skill/file_loader.go`，支持从以下目录扫描 skill：
   - `<base>/.claude/skills/<skill-id>/SKILL.md`
   - `<base>/.agents/skills/<skill-id>/SKILL.md`
   - `<base>/.agent/skills/<skill-id>/SKILL.md`
   - `<base>/.opencode/skills/<skill-id>/SKILL.md`
2. 解析 `SKILL.md` 的 YAML frontmatter 与 Markdown 正文，生成 `Skill{Source: local_file}`。
3. 支持"全局"扫描（server CWD）与"项目级"扫描（session workdir）。
4. 新增全局配置 `skill_scan_dirs`（JSON 数组）控制启用哪些目录模板，默认全部启用。
5. 提供 `POST /api/skills/scan` 强制刷新全部已知 workdir 的文件系统 skill。
6. 加载/卸载时广播 `skill_loaded` / `skill_unloaded` / `skill_changed` 事件。

## 成功标准

- 在 `<workdir>/.claude/skills/foo/SKILL.md` 创建 skill 后，前端 Manage Skills 与 `GET /api/skills?source=local_file` 能立即读到。
- 关闭某个扫描目录后，该目录下的 skill 不再被发现。
- `POST /api/skills/scan` 后内存 registry 与磁盘同步（增/删/改）。
- 文件系统 skill 默认 `scope=project`，全局 `.claude/skills`（server CWD 下）`scope=global`。
- `go test ./internal/skill ./cmd/server` 通过；mock 回归 21/21 PASS。

## 关联变更

- 依赖 Spec 1（Skill Scope / Project / Workdir 基础设施）的 `Scope`、`WorkspaceDir` 字段与 `ResolveActiveSkills`。
- Spec 3（Command 系统）依赖本 Spec 的 workdir 扫描时机。
- Spec 6（前端 UI）需要展示 `local_file` source 的只读 skill。
