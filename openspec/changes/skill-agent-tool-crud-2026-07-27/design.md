# Design: Skill Agent Tool 完整 CRUD

## 关键决策

1. **LLM 可见信息分层**
   - `skill/list` 与 `skill/search` 只返回 summary：id, display_name, description, source, scope, project_id, tags, state。
   - `skill/get` 返回完整结构：含 templates、parameters、permissions 等。
   - 避免把大 prompt 塞进每次 tool result，浪费 token。

2. **built_in 修改策略：fork shadow**
   - 规则：built_in 与 local_file 不允许直接 PUT/DELETE。
   - 当 LLM 或前端尝试更新 built_in skill（例如 `builtin-code-helper`）时：
     1. 创建/保存一个 `source=local_db`、相同 ID、相同原始数据的副本。
     2. 应用更新到副本，并设置 `scope` 为用户指定的 scope（默认 global）。
     3. registry 中该 ID 后续指向 shadow；`ResolveActiveSkills` 输出相同 ID，但内容已是修改版。
   - 删除该 shadow 时，从 registry 卸载 shadow，built_in 重新出现。
   - 若存在 shadow，前端 manage 面板显示 "forked from built_in" 提示。

3. **Agent Tool 输入输出**
   - `skill/get`
     - 输入：`id`
     - 输出：完整 JSON skill（控制长度，避免超大 prompt 撑爆；若模板非常大可截断或提示）。
   - `skill/update_local`
     - 输入：`id`、`updates` JSON 对象（支持部分更新：display_name、description、tags、templates、parameters、scope、project_id）。
     - 输出：操作结果与最终 skill summary。
   - `skill/enable` / `skill/disable`
     - 输入：`id`
     - 输出：成功/失败原因。
     - 对 built_in 也允许 enable/disable（只改内存状态与 store，不删除本体）。
   - `skill/search`
     - 输入：`q`、`source`、`scope`（可选）
     - 输出：summary 列表。
   - `skill/delete_local`
     - 已存在，补充对 built_in shadow 的删除逻辑。

4. **权限隔离**
   - `skill/update_local`、`skill/delete_local`、`skill/enable`、`skill/disable` 对 `local_file` 无效，返回错误。
   - 对 `scope=project` 的 skill，Tool 执行时应检查当前 run 的 projectID。若 skill 属于其它 project，拒绝修改。
   - 由于 Agent Tool 在 Engine 内执行时通过 `ExecuteContext` 拿不到 session/project（当前没有），需要扩展 `ExecuteContext` 或 tool 初始化时绑定 session info。
   - **方案**：在 `cmd/server/main.go` 注册 skill tools 时传入一个回调 `currentProjectID() string`，或直接在 tool 内部读取 `ExecuteContext.Variables["project_id"]`。Engine 已在 `EngineConfig.SkillVariables` 中填充变量；扩展 `ExecuteContext` 增加 `Variables map[string]any`，将 `SkillVariables` 透传给 tool。这是最小侵入方案。

5. **变量透传**
   - 在 `internal/runtime/engine.go` 构造 tool input 前，把 `cfg.SkillVariables` 或其他共享变量放进 `ExecuteContext.Variables`。
   - Skill tools 从 `ExecuteContext.Variables["project_id"]` / `["session_id"]` / `["workspace_dir"]` 读取上下文。
