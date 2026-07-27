# Design: Skill Scope / Project / Workdir 基础设施

## 关键决策

1. **Scope 取值**
   - `global`：所有 session 都可见。
   - `project`：仅当 session 的 `project_id` 匹配，或 `workspace_dir` 位于项目工作目录下时可见。
   - `session`：预留，未来用于运行期通过 Agent Tool 临时创建的 session-only skill。本次不实现存储，但 schema 要先支持。

2. **作用域判定优先级**
   - 运行时注入：从 registry 中取出所有 `global` skill + 当前 session/project/workdir 匹配的 `project` skill，按 ID 去重（`project` 覆盖 `global`）。
   - built_in 默认 `scope=global`。
   - `local_db` 创建时可指定 `scope`；未指定默认 `global`。
   - `local_file` 默认 `scope=project`（因为来自项目目录）；全局文件系统 skill（如 `~/.claude/skills`）为 `global`。

3. **Workdir 匹配规则**
   - 一个 project 的 "project directory" 可以使用 `projects.config` 或 `projects.working_directory`。
   - 对于 `project` skill，只要 `session.WorkspaceDir` == skill.WorkspaceDir，或 session workdir 是 skill.WorkspaceDir 的子目录，即匹配。
   - 未来可扩展为将 project 配置中的 `root_pattern` 作为边界，MVP 阶段使用前缀匹配。

4. **SkillVariables 注入**
   - 在 `AgentRunner.runAgentLoopWithTurn` 中构造 `EngineConfig` 时，填充：
     - `project_id`
     - `project_name`（通过 `db.QueryProjectByID` 获取）
     - `session_id`
     - `workspace_dir`
   - 这些变量会传给 `skill.Renderer` 渲染每个启用 skill 的模板。

5. **向后兼容**
   - 现有 `local_db` skill 在 migration 后 `scope` 默认 `'global'`，`project_id` 默认 `''`。
   - 现有 API 调用若不带 `scope`，默认按 `global` 处理或不过滤（保持原 `GET /api/skills?source=` 行为）。
