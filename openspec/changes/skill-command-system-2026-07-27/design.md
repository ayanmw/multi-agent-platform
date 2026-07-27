# Design: Skill Command 系统

## 关键决策

1. **命令 = 独立触发器**
   - 每个 `.claude/commands/**/*.md` 是一个命令。
   - frontmatter：
     - `id`：可选，手动指定；默认从相对路径生成冒号分层 ID。
     - `name`：展示名。
     - `description`：说明。
     - `skill`：可选，关联的 skill ID。
     - `command`：可选，若指定则覆盖自动生成的命令 ID（特殊格式可再次覆盖）。
     - `tags`：数组。
     - `icon`：可选。
   - Markdown 正文作为命令的 `prompt`：
     - 若 `skill` 已指定，命令执行时先启用关联 skill，prompt 也会作为额外 `system_prompt` 注入？
     - 建议 MMO：命令正文作为**临时 skill** 注入；若同时 `skill` 指定，则启用该 skill + 临时 skill 都注入。

2. **ID 生成规则**
   - 文件 `.claude/commands/ops/new.md`：
     1. 若 frontmatter `id` 存在，直接使用。
     2. 否则 relative path（去掉 `.md`）的 `/` 替换为 `:` → `ops:new`。
     3. frontmatter `command` 优先级最高（覆盖 id）。
   - 不同 workdir 下命令 ID 可能冲突：registry 中命令按 workdir 隔离；REST API 返回时附带 `workspace_dir` / `scope`，前端按来源分组展示。

3. **命令注册表**
   - 新增 `internal/skill/command_registry.go`：
     ```go
     type CommandRegistry struct {
         mu sync.RWMutex
         byID map[string]*SkillCommand // key 格式："global:ops:new" 或 "project:<workdir>:ops:new"
     }
     ```
   - 或者 key 仅使用命令 ID，但 commands 列表 API 按 scope/workdir 过滤返回。

4. **扫描与生命周期**
   - 启动时扫描全局 `.claude/commands`。
   - 每次 session workdir 解析后扫描项目 `.claude/commands`。
   - 刷新 skill 扫描时一并刷新 commands。
   - 命令变更广播 `skill_command_loaded` / `skill_command_unloaded` / `skill_command_changed`（新增事件常量）。

5. **执行流程**
   1. 用户输入 `/ops:new hello world`。
   2. 前端从 `useSkillCommands` 列表中找到 command。
   3. 前端调用 `POST /api/skill-commands/ops:new/invoke` 或复用现有 `enableSkill`？
      - **推荐**：提供一个后端 invoke 接口，统一处理启用 + 临时 skill 注册；接口返回启用结果与临时 skill ID。
      - 前端拿到结果后，把剩余文本作为 user input 发送，并在该次 run 的 EngineConfig 中把临时 skill 加入 `ActiveSkills`。
   4. 或者简化：前端不调用后端 invoke，而是直接把 `/ops:new` 中的 `ops:new` 视为 skill ID 调用 `enableSkill`；对于无关联 skill 的命令，在后端把命令 ID 映射为临时 skill。
      - 选择：**后端 invoke** 更统一，也便于 LLM 解析命令。

6. **与 Engine 的集成**
   - 临时 skill 的注入通过 EngineConfig `ActiveSkills` + SkillRegistry 实现。
   - `cmd/server/runner.go` 在构造 EngineConfig 前，若检测到本次 user input 以 command ID 开头，应在 `ActiveSkills` 中追加 command 关联 skill 与临时 skill。
   - 更简洁：前端在 send 时把 command ID 作为 `command_id` 字段加入 ChatRequest body，`runner.go` 据此启用。
   - 但当前 `ChatRequest` 没有 `command_id` 字段。MVP 推荐：前端调用 invoke 后，把 command 关联的临时 skill ID 与 skill ID 传给后端；或者后端 invoke 直接把 command 对应的 skill 启用（不持久化），当前 run 生效。

   **最终推荐**：
   - 前端 CommandBar 检测到 `/` 后向 `GET /api/skill-commands` 请求补全。
   - 用户选中 command 并发送，前端调用 `POST /api/skill-commands/:id/invoke`。
   - 后端返回：
     ```json
     {
       "enabled_skill_ids": ["openspec-new-change"],
       "temporary_skill_id": "cmd:ops:new:<uuid>"
     }
     ```
   - 前端将剩余文本作为 user input，连同 `enabled_skill_ids` 和 `temporary_skill_id` 以某种方式通知 backend（当前 backend 并没有根据单次请求临时改 ActiveSkills 的通道）。

   为了避免 runner 接口大改，**MVP 最简方案**：
   - `POST /api/skill-commands/:id/invoke` 启 skill 并注册临时 skill，返回成功。
   - 前端立即调用普通 send；由于 skill 已被启用、临时 skill 已在 registry 中，当前 run 直接生效。
   - 临时 skill 在 session 下一次 run 前可自动清理，或保留到 session 结束（MVP 不清理）。

7. **LLM 视角**
   - LLM 可通过 Agent Tool `skill/list` / `skill/search` 查看 skill summary，但 command 列表不应默认暴露给 LLM（command 是前端快捷方式）。必要时可新增 `skill_command/list` Tool，但本次先不实现，保持 LLM 只关注 skill。
