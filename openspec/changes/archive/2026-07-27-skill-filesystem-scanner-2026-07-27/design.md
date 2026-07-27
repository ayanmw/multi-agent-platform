# Design: 文件系统 Skill 扫描器

## 关键决策

1. **目录模板**
   使用 4 个可启用/禁用的模板：
   - `.claude/skills`
   - `.agents/skills`
   - `.agent/skills`
   - `.opencode/skills`

   每个模板挂载于一个 baseDir：
   - 全局扫描使用 `globalBaseDirs()`（server CWD，未来可扩展为 `$HOME/.config/...`）。
   - 项目级扫描使用 `workdir` 作为 baseDir。

2. **Skill 文件结构**
   ```
   <base>/.claude/skills/<skill-id>/SKILL.md
   ```
   - `skill-id` = 目录名。
   - `SKILL.md` 含 YAML frontmatter 与 Markdown 正文。
   - frontmatter 字段：
     - `id`：可选，默认目录名。
     - `name` / `display_name`
     - `description`
     - `tags`：数组
     - `scope`：可选，默认 `project`
     - `project_id`：可选
     - `is_local_editable`：固定 false（文件系统 skill 只读）。
     - `template_name`：可选，默认 `system_prompt`。
   - Markdown 正文作为 `templates[0].Content`，模板名默认 `system_prompt`。

3. **全局 vs 项目级判定**
   - 当 baseDir 是 server CWD 或其父级时，视为 global，生成 `scope=global`。
   - 当 baseDir 是 session workdir 时，生成 `scope=project`，`workspace_dir=workdir`。
   - 显式 frontmatter `scope=global` 可覆盖默认。

4. **热加载策略（MVP）**
   - 第一版不引入 fsnotify（避免跨平台复杂度）。
   - 启动时扫描一次 global。
   - 每次创建/解析 session 时扫描其 workdir。
   - 提供 `POST /api/skills/scan` 接口进行全量重扫（global + 所有已知 workdir）。
   - 后续迭代可替换为 `fsnotify.Watcher`。

5. **去重与优先级**
   - 文件系统 skill source = `local_file`；DB skill source = `local_db`。
   - 同 ID _skill_ 优先级：`local_db` > `local_file` > `built_in`。
   - 文件系统 skill 变更后，若用户已创建同名 `local_db` shadow，则显示 shadow 版本。

6. **配置持久化**
   - 新增 `settings` 表（key/value）或扩展 `config.Config`。
   - 推荐新增 migration 创建 `settings(
     key TEXT PRIMARY KEY,
     value TEXT
   )`。
   - 启动时读取 `skill_scan_dirs` 配置；未设置时使用默认值（4 个目录全部启用）。

7. **事件广播**
   - 扫描到新增 skill → `skill_loaded` + `skill_changed`
   - skill 文件被删除/更新后刷新 → `skill_unloaded` + 若内容变化则 `skill_changed`
   - TaskID = sessionID 或 `"global"`，AgentID = `"skill-loader"`
