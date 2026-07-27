# Spec: Skill Scope / Project / Workdir 基础设施

## ADDED Requirements

### Requirement: Skill 数据模型支持 Scope、ProjectID、WorkspaceDir

The `internal/skill/skill.go` `Skill` struct SHALL add `Scope`, `ProjectID`, and `WorkspaceDir` fields with JSON tags matching the DB column names, and SHALL define `SkillScope` constants for `global`, `project`, and `session`.

```go
type SkillScope string

const (
    SkillScopeGlobal  SkillScope = "global"
    SkillScopeProject SkillScope = "project"
    SkillScopeSession SkillScope = "session"
)

type Skill struct {
    // ... existing fields ...
    Scope        SkillScope `json:"scope"`
    ProjectID    string     `json:"project_id"`
    WorkspaceDir string     `json:"workspace_dir"`
}
```

#### Scenario: Scope 常量序列化
- **WHEN** the backend serializes a Skill to JSON
- **THEN** the `scope` field SHALL be `"global"`, `"project"`, or `"session"`

#### Scenario: 向后兼容默认值
- **WHEN** existing skill rows are read after migration
- **THEN** the `scope` column SHALL default to `"global"`

---

### Requirement: Skills 表 Migration 覆盖三列

`pkg/db/skill.go` SHALL add a new migration that adds `scope`, `project_id`, and `workspace_dir` columns to the `skills` table.

```sql
ALTER TABLE skills ADD COLUMN scope TEXT DEFAULT 'global';
ALTER TABLE skills ADD COLUMN project_id TEXT DEFAULT '';
ALTER TABLE skills ADD COLUMN workspace_dir TEXT DEFAULT '';
```

#### Scenario: 既有数据升级
- **WHEN** existing skill rows are upgraded by the migration
- **THEN** they SHALL automatically receive `scope='global'`, `project_id=''`, `workspace_dir=''`

---

### Requirement: Skill Store CRUD 支持新字段

`internal/skill/store.go` `Save`, `Get`, and `ListAll` SHALL read and write the `scope`, `project_id`, and `workspace_dir` columns.

#### Scenario: 创建 project scope skill
- **WHEN** `POST /api/skills` is called with `scope=project`, `project_id=proj-a`, `workspace_dir=/home/proj-a`
- **THEN** the persisted skill SHALL return the same values on GET

---

### Requirement: 提供 `ResolveActiveSkills` 函数

`internal/skill/registry.go` SHALL implement `ResolveActiveSkills(registry *Registry, projectID, workspaceDir string) []string` with the following behavior:

- Only enabled skill IDs SHALL be returned.
- `global` scoped skills SHALL always match.
- `project` scoped skills SHALL match when their `ProjectID` equals the requested projectID, or when their `WorkspaceDir` is a prefix of or equal to the requested workspaceDir.
- `session` scoped skills SHALL be excluded from results for now.
- Duplicate IDs SHALL be deduplicated with precedence `session > project > global`.

#### Scenario: project skill 按 projectID 激活
- **GIVEN** a registry contains a `global` skill A and a `project` skill A with `project_id=proj-a`
- **WHEN** `ResolveActiveSkills(r, "proj-a", "")` is called
- **THEN** it SHALL return only the project-scoped skill A

#### Scenario: project skill 按 workspace 子目录激活
- **GIVEN** a registry contains a `project` skill B with `workspace_dir=/home/proj/src`
- **WHEN** `ResolveActiveSkills(r, "", "/home/proj/src/sub")` is called
- **THEN** it SHALL return skill B

#### Scenario: session scope 当前不注入
- **GIVEN** a registry contains a `session` skill C
- **WHEN** `ResolveActiveSkills` is called with any parameters
- **THEN** it SHALL NOT return skill C

---

### Requirement: REST API 支持 Scope 查询与写入

`cmd/server/api_skill.go` SHALL support:

- `GET /api/skills` with optional query parameters `scope`, `project_id`, and `workdir`, while preserving `source` and `q`. When no scope filters are provided, it SHALL return all skills.
- `POST /api/skills` with optional body fields `scope`, `project_id`, `workspace_dir`. Only `source=local_db` SHALL be allowed to set `scope=project` or `session`.
- `PUT /api/skills/:id` SHALL allow modifying `scope`, `project_id`, and `workspace_dir` for `local_db` editable skills only.

#### Scenario: 按 scope 过滤列表
- **WHEN** a skill with `scope=project` and `project_id=proj-a` is created
- **THEN** `GET /api/skills?scope=project` SHALL include it, and `GET /api/skills?project_id=proj-a` SHALL return only that skill

#### Scenario: 内置 skill 不可修改 scope
- **WHEN** a PUT request attempts to change the scope of `builtin-code-helper`
- **THEN** the API SHALL return `403 Forbidden`

---

### Requirement: Runner 注入 `ResolveActiveSkills` 与 `SkillVariables`

`cmd/server/runner.go` `runAgentLoopWithTurn` and `Recover` SHALL:

- Resolve `projectID`, `projectName`, and `workspaceDir` from the session/project.
- Set `EngineConfig.ActiveSkills` to `skill.ResolveActiveSkills(globalSkillRegistry, projectID, workspaceDir)`.
- Set `EngineConfig.SkillVariables` to a map containing `project_id`, `project_name`, `session_id`, and `workspace_dir`.

#### Scenario: Skill 模板渲染 project 变量
- **GIVEN** a skill template `Focus on {{project_id}} at {{workspace_dir}}`
- **WHEN** the runtime projectID is `proj-a` and workspaceDir is `/home/proj-a`
- **THEN** the rendered template SHALL be `Focus on proj-a at /home/proj-a`

---

### Requirement: Renderer 对 nil SkillVariables 安全

`internal/skill/renderer.go` `Render` SHALL be nil-safe when `vars` is nil, preferring `SkillParameter.Default` for missing variables and preserving placeholders when no default exists.

#### Scenario: 旧路径无 SkillVariables
- **WHEN** `Render(template, nil)` is called
- **THEN** it SHALL return the original template with placeholders preserved and SHALL NOT panic

---

## MODIFIED Requirements

### Requirement: 内置 Skill 默认 Scope 为 Global

`internal/skill/builtin.go` SHALL set `Scope: SkillScopeGlobal` for all built-in skills.

#### Scenario: 启动加载内置 skill
- **WHEN** the server starts
- **THEN** `builtin-code-helper` and `builtin-error-diagnosis` SHALL have `scope=global`

---

### Requirement: 环境变量优先级修正以支持测试脚本

`cmd/server/main.go` SHALL call `config.SetOSFirst()` during startup so that OS environment variables take precedence over `.env`, and SHALL use the command-line `-port` value directly when provided, preventing `.env` `SERVER_PORT` from overriding test script settings.

#### Scenario: 回归脚本覆盖 SERVER_PORT
- **GIVEN** `.env` contains `SERVER_PORT=30080`
- **WHEN** the regression script sets `SERVER_PORT=18105` and starts the server
- **THEN** the server SHALL listen on port `18105`

---

## REMOVED Requirements

无。

## 文件清单

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/skill/skill.go` | 修改 | 新增 Scope / ProjectID / WorkspaceDir 字段与常量 |
| `internal/skill/registry.go` | 修改 | 新增 `ResolveActiveSkills` 与作用域匹配辅助函数 |
| `pkg/db/skill.go` | 修改 | migration + CRUD |
| `internal/skill/store.go` | 修改 | CRUD 覆盖新字段 |
| `cmd/server/api_skill.go` | 修改 | 查询参数 + 创建/更新 body |
| `cmd/server/runner.go` | 修改 | EngineConfig.ActiveSkills / SkillVariables |
| `internal/skill/renderer.go` | 修改 | nil-safe 渲染 |
| `internal/skill/builtin.go` | 修改 | 内置 skill Scope=global |
| `internal/skill/loader.go` | 修改 | 加载时默认空 scope 转 global |
| `cmd/server/main.go` | 修改 | SetOSFirst + -port 直接生效 |
| `cmd/server/api_skill_project_test.go` | 新增 | project scope E2E 测试 |
| `internal/skill/registry_test.go` | 新增/修改 | ResolveActiveSkills 单元测试 |
| `internal/skill/loader_test.go` | 修改 | in-memory schema 加三列 |
| `internal/skill/store_test.go` | 修改 | in-memory schema 加三列 |
| `internal/skill/tools_test.go` | 修改 | in-memory schema 加三列 |

## 非目标

- 不实现文件系统扫描（Spec 2）。
- 不实现 command system（Spec 3）。
- 不实现 Agent Tool 临时 skill（Spec 4）。
- 不实现 context window skill 统计（Spec 5）。
- 不修改前端 UI（Spec 6）。
