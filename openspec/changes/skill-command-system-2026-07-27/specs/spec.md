# Spec: Skill Command 系统

## 新增/修改文件

### 领域模型与事件

1. `internal/skill/skill.go`
   - 新增类型 `SkillCommand`：
     ```go
     type SkillCommand struct {
         ID           string
         Name         string
         Description  string
         SourcePath   string
         Scope        string // global/project
         WorkspaceDir string
         ProjectID    string
         SkillID      string // optional
         Prompt       string // Markdown body as temporary system_prompt
         Tags         []string
         Icon         string
         CommandKey   string // optional, overrides ID
     }
     ```
   - 新增 `EventSkillCommandLoaded` / `EventSkillCommandUnloaded` / `EventSkillCommandChanged` 常量（`events.go` 也可，看已有结构）。

2. `internal/skill/events.go`
   - 新增 command 相关事件常量。

### Command 扫描器

3. 新增 `internal/skill/command_loader.go`
   - `CommandLoader` 结构与 `FileLoader` 类似：
     ```go
     type CommandLoader struct {
         registry *Registry
         bus      EventBus
     }
     ```
   - 方法：
     - `NewCommandLoader(registry, bus) *CommandLoader`
     - `LoadGlobal(baseDir string) error`
     - `LoadForWorkdir(workdir, projectID string) error`
     - `UnloadForWorkdir(workdir string) error`
     - `RefreshAll(workdirs []string) error`
   - 解析 `.claude/commands/**/*.md`：
     - 路径作为 `SourcePath`。
     - ID 生成：frontmatter `command` → frontmatter `id` → 相对路径 `/` 替换为 `:`。
     - Markdown 正文作为 `Prompt`；frontmatter `skill` 作为 `SkillID`。

4. 新增 `internal/skill/command_registry.go`
   - `CommandRegistry` 或复用 `skill.Registry`？
   - **推荐复用**：Command 不是 skill，应独立内存结构；但可以通过 `skill.Registry.SetCommand(id, cmd)` 扩展 registry？
   - 更干净：独立 `CommandRegistry`，提供 `Register`、`Unregister`、`Get`、`List`、`ListForWorkdir`。

### Loader 集成

5. `internal/skill/loader.go`
   - `Loader` 持有 `commandLoader *CommandLoader`。
   - `LoadAll` 后调用命令全局扫描。
   - `LoadForWorkdir(workdir, projectID)` 在扫描 skill 文件后扫描 commands。
   - `RefreshAll` 重扫时一并卸载/重载 commands。

### REST API

6. 新增 `cmd/server/api_skill_command.go`
   - `GET /api/skill-commands`
     - query：`workdir`、`project_id`、`q`。
     - 从 command registry 过滤，返回命令列表（ID、name、description、scope、source_path、skill_id、tags）。
   - `GET /api/skill-commands/:id`
     - 返回单个命令详情，包含 prompt 全文（用于前端详情展示）。
   - `POST /api/skill-commands/:id/invoke`
     - 从 registry 找到命令。
     - 若 command.SkillID != ""，调用 skill store/registry enable（同 `handleEnableSkill`）。
     - 若 command.Prompt != "", 将其作为临时 skill 注册到 registry（source=`command_temporary` 或保留 `local_file`），ID 为 command.ID（或生成 `cmd:<id>`），默认启用。
     - 返回 `{enabled_skill_ids:[...], temporary_skill_id:string}`。
     - 若命令来自 project scope 但当前 session workdir 不匹配，返回 403。

7. `cmd/server/main.go`
   - 启动时调用 `Loader.LoadAll()` 后会加载全局 commands。

8. `cmd/server/api.go`
   - 创建/解析 session workdir 后调用 `skillLoader.LoadForWorkdir`（已含命令扫描）。

### 前端组件

9. 新增 `web/v2/src/types/skill.ts`
   - `SkillCommand` 完整类型。

10. 新增 `web/v2/src/composables/useSkillCommands.ts`
    - `loadCommands(workdir?)` 调用 `GET /api/skill-commands`。
    - 支持搜索与 refresh。

11. 新增 `web/v2/src/components/SkillPicker.vue`
    - 浮层，列出命令/技能；支持分组显示（按 scope/source）、↑/↓/Enter/Esc。

12. 修改 `web/v2/src/components/CommandBar.vue`
    - 监听 `text` 变化：若输入以 `/` 开头且光标在首段，显示 `SkillPicker`。
    - `submit()` 时：如果当前被 picker 选中，不重复发送；否则正常 emit。

13. 修改 `web/v2/src/App.vue`
    - 保持现有 `/skill-id` 解析逻辑。
    - 当 CommandBar 通过 picker 选中 command 时，接收 command ID，调用 `POST /api/skill-commands/:id/invoke`。
    - invoke 成功后把剩余文本作为 input 发送（走正常 startTask/startTurn）。

## 安全与权限

- `POST /api/skill-commands/:id/invoke` 要校验当前 session 是否有权访问 project scope 命令。
- 临时 skill 不应持久化到 DB；source 用特殊标记，重启后消失。

## 测试

- `internal/skill/command_loader_test.go`：测试 ID 生成、frontmatter 解析、scope。
- `cmd/server/api_skill_command_test.go`：测试 list、invoke、临时 skill 注入。
- `web/v2/src/components/__tests__/SkillPicker.spec.ts`：测试 `/` 触发与选择。
