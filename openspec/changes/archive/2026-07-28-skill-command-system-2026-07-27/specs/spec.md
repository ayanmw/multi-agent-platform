# Spec: Skill Command 系统

## ADDED Requirements

### Requirement: R1 Command 领域模型

- **R1.1** 系统 SHALL 新增 `SkillCommand` 类型，字段：`ID`、`Name`、`Description`、`SourcePath`、`Scope`、`WorkspaceDir`、`ProjectID`、`SkillID`、`Prompt`、`Tags`、`Icon`、`CommandKey`。
- **R1.2** 系统 SHALL 新增事件常量 `EventSkillCommandLoaded` / `EventSkillCommandUnloaded` / `EventSkillCommandChanged`。

#### Scenario: command model events
- Given 项目事件系统已存在
- When 扫描 `.claude/commands/**/*.md`
- Then 应广播 command 加载/卸载/变更事件

### Requirement: R2 Command 扫描器

- **R2.1** 系统 SHALL 新增 `CommandLoader`，扫描 `.claude/commands/**/*.md`。
- **R2.2** 系统 SHALL 按优先级生成 Command ID：frontmatter `command` → frontmatter `id` → 相对路径 `/` 替换为 `:`。
- **R2.3** 系统 SHALL 将 Markdown 正文作为 `Prompt`，frontmatter `skill` 作为 `SkillID`。
- **R2.4** 系统 SHALL 支持全局加载 `LoadGlobal` 与项目级加载 `LoadForWorkdir`。

#### Scenario: scan commands
- Given `.claude/commands/ops/new.md` 存在
- When 调用 `CommandLoader.LoadForWorkdir(workdir, projectID)`
- Then registry 中应出现 ID 为 `ops:new` 的 command

### Requirement: R3 Command 注册表

- **R3.1** 系统 SHALL 新增独立 `CommandRegistry`，提供 `Register`/`Unregister`/`Get`/`List`/`ListForWorkdir`。
- **R3.2** 系统 SHALL 保证项目 scope command 仅对匹配 workdir 可见。

#### Scenario: scope filtering
- Given 项目级 command `ops:new`
- When 调用 `ListForWorkdir` 传入不匹配目录
- Then 返回列表中不应包含该项目 command

### Requirement: R4 Loader 集成

- **R4.1** 系统 SHALL 让 `Loader` 持有 `CommandLoader`。
- **R4.2** 系统 SHALL 在 `LoadAll` 后调用 command 全局扫描。
- **R4.3** 系统 SHALL 在 `LoadForWorkdir` 中 skill 文件扫描后扫描 commands。
- **R4.4** 系统 SHALL 在 `RefreshAll` 中卸载并重载 commands。

#### Scenario: loader integration
- Given server 启动后调用 `LoadAll`
- When 全局存在 `.claude/commands/**/*.md`
- Then command registry 应加载全局 commands

### Requirement: R5 REST API

- **R5.1** 系统 SHALL 实现 `GET /api/skill-commands`，支持 `workdir`、`project_id`、`q` 过滤。
- **R5.2** 系统 SHALL 实现 `GET /api/skill-commands/:id`，返回 command 详情（含 prompt）。
- **R5.3** 系统 SHALL 实现 `POST /api/skill-commands/:id/invoke`：
  - 若 `SkillID != ""` 则启用对应 skill；
  - 若 `Prompt != ""` 则注册临时 skill `cmd:<id>` 并启用；
  - 返回 `{enabled_skill_ids, temporary_skill_id}`。
- **R5.4** 系统 SHALL 在项目 scope command 调用时校验当前 session workdir。

#### Scenario: invoke command with prompt
- Given command `ops:new` 含 prompt
- When POST `/api/skill-commands/ops:new/invoke`
- Then registry 中应新增并启用 `cmd:ops:new`，响应含临时 skill ID

### Requirement: R6 前端集成

- **R6.1** 系统 SHALL 让 `SkillPicker` 在输入 `/` 时浮出，支持 ↑/↓/Enter/Esc。
- **R6.2** 系统 SHALL 在选中 command 后预填充 `/command-id `，自动调用 invoke。
- **R6.3** 系统 SHALL 在 invoke 成功后发送剩余文本。

#### Scenario: user triggers skill picker
- Given 用户在 CommandBar 输入 `/`
- When SkillPicker 打开并选择 `ops:new`
- Then 输入框应变为 `/ops:new `，invoke 成功后继续发送剩余文本

## MODIFIED Requirements

### Requirement: M1 Loader command 扫描

- **M1.1** 系统 SHALL 在 `internal/skill/loader.go` 中增加 command 扫描调用。

#### Scenario: loader scans commands on start
- Given server 启动
- When `LoadAll` 执行完成
- Then command registry 应包含全局 commands

## REMOVED Requirements

- 无
