# Spec: Skill Agent Tool 完整 CRUD

## 新增/修改文件

### ExecuteContext 扩展

1. `internal/tool/builtin.go` 或 `executor.go`
   - 检查当前 `ExecuteContext` 是否已有 `Variables map[string]any`；若没有，新增该字段。
   - `Registry.ExecuteWithCtx` 负责把 engine 的 `SkillVariables`（或更通用的 `ToolVariables`）写入 `ExecuteContext.Variables`。
   - 该改动也影响 `worktree.go` 等现有 tool；需确认不破坏。

### Tool 实现

2. `internal/skill/tools.go` 大改
   - 保持 `skill/create_local`、`skill/delete_local`、`skill/list`。
   - 新增 `SkillGetTool`：
     - 参数 `id`
     - 检查 registry，返回完整 skill 字符串（建议对 templates 做 JSON 序列化后输出）。
   - 新增 `SkillUpdateLocalTool`：
     - 参数 `id`, `updates` JSON
     - 若 id 存在于 built_in 且不存在 local_db shadow：
       1. 获取 built_in 原始数据。
       2. 深拷贝为 source=`local_db` 的新 skill（同 ID）。
       3. 应用 updates。
       4. store.Save + registry.Register。
     - 若已存在 local_db shadow：直接修改 shadow。
     - 若 id 不存在：返回错误。
   - 新增 `SkillEnableTool` / `SkillDisableTool`：
     - 若 skill 是 `local_file`：返回 403，提示只能修改文件系统。
     - 对 built_in：UpdateState + 不存 DB（built_in 状态不持久），或存 DB 时 source=`local_db` 的 shadow 状态。
     - 对 local_db：UpdateState + store.Save。
     - 广播 `skill_enabled` / `skill_disabled`。
   - 新增 `SkillSearchTool`：
     - 参数 `q`, `source`, `scope`
     - 调用 registry.List + 过滤，返回 summary 列表字符串。
   - 修改 `SkillDeleteLocalTool`：
     - 若目标是 local_db shadow of built_in：store.Delete + registry.Unregister，built_in 恢复。
     - 若目标是 built_in：返回 403。
     - 若目标是 local_file：返回 403。

3. 每个 tool 从 `ExecuteContext.Variables` 读取 `project_id` / `workspace_dir`，用于 scope 校验。

### REST API 同步

4. `cmd/server/api_skill.go`
   - 修改 `handleUpdateSkill`：
     - 若原 skill source 为 built_in 且同 ID 不存在 local_db shadow，自动 fork 为 local_db。
     - 返回 200 与最终 skill summary。
   - 修改 `handleDeleteSkill`：
     - 若目标是 built_in，返回 403。
     - 若目标是 local_db shadow of built_in，删除 shadow，恢复 built_in。
     - 若目标是 local_file，返回 403。
   - 保持 `handleEnableSkill` / `handleDisableSkill` 不变（对 built_in 也允许启用/禁用，状态存在内存）。

### 注册

5. `cmd/server/main.go`
   - 注册新增 Agent Tools：`skill/get`、`skill/update_local`、`skill/enable`、`skill/disable`、`skill/search`。

### 测试

6. `internal/skill/tools_test.go` 新增
   - create → get → update → enable/disable → delete 流程。
   - built_in fork shadow 测试。
   - project scope 越权修改测试。

7. `cmd/server/api_skill_test.go` 更新
   - built_in fork shadow REST 测试。

## 非目标

- 不实现 LLM 对 command 的 CRUD（Spec 3）。
- 不实现 skill 统计（Spec 5）。
