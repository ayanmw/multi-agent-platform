# Proposal: Skill Command 系统（.claude/commands）

## 问题

当前前端 `CommandBar` 不识别 `/` 触发；Claude Code 用户期望项目下的 `.claude/commands/**/*.md` 能作为命令被调用，命令 ID 用冒号分层（如 `/ops:new`），方便按目录组织。命令应作为"独立触发器"：可以关联已有 skill，也可以自带 prompt 作为临时 skill 注入上下文。

## 目标

1. 设计并实现 `SkillCommand` 领域模型与 `CommandLoader`。
2. 扫描 `.claude/commands/**/*.md`（全局 + 每个 workdir）。
3. 命令 ID 用冒号分层，例如 `.claude/commands/ops/new.md` → ID `ops:new`。
4. 提供 `GET /api/skill-commands?workdir=&project_id=&q=` API。
5. 提供 `POST /api/skill-commands/:id/invoke` API，用于启用关联 skill 或临时注入 prompt。
6. 前端 `CommandBar` 输入 `/` 时弹出命令选择器，选中后预填充 `/command-id `，调用启 skill 流程。

## 成功标准

- 创建 `.claude/commands/ops/new.md` 后，CommandBar 输入 `/` 能看到 `ops:new`。
- 选中 `/ops:new` 发送时，会启用关联 skill，剩余文本作为 user input。
- 无关联 skill 的命令会把自身 prompt 作为临时 system_prompt 注入当前 run。
- `go test ./internal/skill ./cmd/server` 通过；mock 回归 21/21 PASS。

## 关联变更

- 依赖 Spec 1（scope/project）的 workdir 隔离与 `ResolveActiveSkills`。
- 依赖 Spec 2（filesystem scanner）的 workdir 扫描时机与 loader 结构。
- Spec 6（前端 UI）依赖本 Spec 的 `/api/skill-commands` 与 invoke 流程。
