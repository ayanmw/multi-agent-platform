# Skills 系统 Review 报告

> 审查范围：Skills 系统完整改造（6 份 OpenSpec change + 跨阶段集成一致性）
> 审查方式：多 agent 对抗式 review（find → verify → synthesize），每条发现经对抗式自检
> 审查日期：2026-07-28（Spec5/6）/ 2026-07-29（Spec1/2/3/4 + 集成补跑）
> 覆盖状态：✅ 6 个维度全部完成（Spec1/2/3/4/5/6 + Integration）

---

## 整体评价

Skills 系统的核心骨架（领域模型、registry、store、loader、renderer、Engine 注入、context_window 统计、前端 SkillManager/CommandBar）已全部落地，白盒可观测链路打通，mock 回归 21/21 不受影响。fork-on-edit、delete shadow 恢复 built_in、sourceRank 优先级（local_db > local_file > built_in）、context_window_snapshot.skill_blocks 字段契约等关键接缝在 REST 与 Agent Tool 双路径一致。

但对抗式审查确认 **3 条 critical + 6 条 high** 的功能性断裂，集中在三条跨 Spec 根因：

1. **临时命令 skill 永不注入 Engine**（Spec1↔Spec3 接缝）：`registerTemporarySkill` 把命令 prompt 注册为 `scope=session`，而 `ResolveActiveSkills` 对 session scope 固定 `return false`。Spec3 最核心的成功标准——"无关联 skill 的命令把自身 prompt 作为临时 system_prompt 注入当前 run"——在真实 run 中完全落空，前端只看到 prefill 文本，LLM 收不到任何命令指令。
2. **orchestrator 子 agent 完全无 Skill 注入**（Spec1↔多 Agent 接缝）：`orchestrator.runAgent` 构造 EngineConfig 时不设 SkillRegistry/ActiveSkills/SkillVariables，所有 worker agent 的 system prompt 不含任何 Skill Instructions，leader 有 skill 而 worker 无 skill，能力不对等。
3. **Agent Tool 越权防护变量从未注入**（Spec1↔Spec4 接缝）：`skill/update_local` 读 `input["_project_id"]` 做跨 project 越权检查，但 Engine 只注入 `workdir/session_id/task_id`，从不注入 `_project_id`，且 skill tool 未实现 CtxTool 拿不到 `ExecuteContext.Variables`。`callerProjectID` 恒空 → 越权分支永不触发，任意 session 可改其它 project 的 skill。

另有三类系统性信号：

- **"tasks.md 误标完成"模式**：多个 spec 的 tasks.md 标 [x] 但代码未实现或测试未覆盖（Spec1 task 8 "E2E 运行期注入"只测函数返回、Spec2 task 5 "resolveSession 调 LoadForWorkdir" 只在 handleSessions 一处、Spec4 task 9 "越权修改测试" 缺失、Spec6 handleTriggerSkill 偏离 spec 11）。task 勾选缺乏代码级验证把关。
- **前端测试"同义反复"+"死 stub"模式**：useSkills.spec.ts 用本地重实现代替被测函数，ManageContent.test.ts 仍 stub 已替换的 SkillPanel，command/invoke 测试注释自述 "Can't do without database store" 跳过关键断言。测试通过但零回归价值。
- **workdir 判定三处语义不一致**：`ResolveActiveSkills` 用 `isSubDirOrEqual`（规范化 + 分隔符感知），`GET /api/skills?workdir=` 用精确等值，`isCommandScopeAllowed` 用裸 `strings.HasPrefix`。同一 workdir 在列表/运行期/invoke 三处返回不同 skill 集合，出现"列表看不到但能 invoke""列表看不到但运行期注入"的不一致。

---

## 严重度分级总览

| 严重度 | 数量 | 说明 |
|--------|------|------|
| Critical | 3 | 核心功能在真实路径下完全失效 |
| High | 6 | 关键能力断裂或越权/数据污染 |
| Medium | 16 | 偏离 spec、一致性缺陷、测试缺口 |
| Low | 14 | UX 提示、边界场景、死代码、测试覆盖 |

---

## Critical（3 条）

### C1. 临时命令 skill 永不注入 Engine（Spec1↔Spec3 接缝）
- **文件**：`internal/skill/registry.go:115-117` + `cmd/server/api_skill_command.go:267`
- **失败场景**：用户经 CommandBar 输入 `/ops:new` → invoke → `registerTemporarySkill` 注册 `cmd:ops:new`（scope=session, state=enabled）→ 同一 run 的 `EngineConfig.ActiveSkills = ResolveActiveSkills(registry, projectID, workspaceDir)` → `skillMatchesScope` 对 session scope 显式 `return false`（注释"本次预留，不注入"）→ `cmd:ops:new` 不在 ActiveSkills → Engine NewEngine 跳过其 system_prompt 模板渲染 → 命令 prompt 完全丢失。spec 核心成功标准"无关联 skill 的命令把自身 prompt 作为临时 system_prompt 注入当前 run"落空。
- **建议**：三选一——(a) 临时命令 skill 改注册为 `scope=global`（符合"本次 run 全局可见"语义）；(b) `ResolveActiveSkills` 增加 session scope 白名单参数，invoke 路径把 temporary_skill_id 显式追加到 ActiveSkills；(c) invoke 直接返回 skill_id 由 runner 显式追加。推荐 (a)，最小改动且语义清晰。

### C2. orchestrator 子 agent 完全无 Skill 注入（Spec1↔多 Agent 接缝）
- **文件**：`internal/orchestrator/orchestrator.go:1030-1079`
- **失败场景**：leader 调 `dispatch_sub_agent` 启动 worker → `runAgent` 构造 EngineConfig 时 SkillRegistry/ActiveSkills/SkillVariables 三字段均为零值 → Engine NewEngine 中 `cfg.SkillRegistry == nil` → 跳过整个 skill 注入块 → worker 的 system prompt 不含任何 Skill Instructions，即使该 project 下有 enabled 的 project scope skill。leader 有 skill、worker 无 skill，能力不对等。Spec1 成功标准"全局 skill 对所有 session 生效"在编排路径失效。
- **建议**：`orchestrator.Orchestrator` 增加 `skillRegistry` 字段 + `SetSkillRegistry` 方法（对齐 `SetWorkspace` 模式），`cmd/server` 在 `RunBlocking` 前调用；`runAgent` 的 EngineConfig 注入 `SkillRegistry: o.skillRegistry`、`ActiveSkills: skill.ResolveActiveSkills(o.skillRegistry, projectID, workspaceDir)`、`SkillVariables: {project_id, session_id, workspace_dir}`，与 `runner.go:1003-1010` 对齐。

### C3. Agent Tool 越权防护变量从未注入（Spec1↔Spec4 接缝）
- **文件**：`internal/skill/tools.go:344` + `internal/runtime/engine.go:2065-2070`
- **失败场景**：LLM 调 `skill/update_local {id:"proj-skill", updates:{scope:"project", project_id:"other-project"}}` → Engine `executeToolCall` 注入 `args["session_id"]/task_id/workdir` 但不注入 `args["_project_id"]` → `skillUpdateLocalTool.Execute`（未实现 CtxTool，拿不到 `ExecuteContext.Variables`）中 `callerProjectID = input["_project_id"] = ""` → 越权检查 `if updates.ProjectID != "" && callerProjectID != ""` 不触发 → LLM 可把任意 project scope skill 的 project_id 改成任意值，跨 project 越权修改成功。spec.md:65 与 verify.md:43 明确要求的"project scope 越权修改被拒绝"既未实现也无测试。
- **建议**：在 Engine `executeToolCall` 中与 session_id 同级注入 `args["_project_id"] = e.cfg.SkillVariables["project_id"]`（类型断言 string）；或让 skill tool 实现 `CtxTool.ExecuteWithCtx` 从 `ctx.Variables["project_id"]` 读取。补一条从 proj-Y 调用修改 proj-X skill 应返回 error 的测试。

---

## High（6 条）

### H1. resolveSession 等四个 session 入口不触发 workdir 扫描（Spec2↔运行入口接缝）
- **文件**：`cmd/server/persistence.go:170-222`
- **失败场景**：`globalSkillLoader.LoadForWorkdir` 只在 `handleSessions POST`（api.go:644）一处调用。但 `/api/tasks` action=chat、action=multi-agent、`/api/run-case` 匿名 session、cron `start_task` 都走 `resolveSession` 创建 session 并绑定 WorkspaceDir，从不触发 `LoadForWorkdir`。在 `<workdir>/.claude/skills/foo/SKILL.md` 放好 skill，用 `/api/tasks` 发 chat，registry 不会发现该 skill，ActiveSkills 也不含它，直到手动 `POST /api/skills/scan`。spec 明确要求"创建/解析 session 后调用 LoadForWorkdir"。
- **建议**：在 `resolveSession` 新建 session 成功且 `workspaceDir != ""` 时调一次 `globalSkillLoader.LoadForWorkdir(workspaceDir, "")`，统一所有无 session 入口的 workdir 扫描。

### H2. REST enable/disable 未拦截 local_file，污染 DB（Spec2）
- **文件**：`cmd/server/api_skill.go:480-540`
- **失败场景**：`POST /api/skills/:id/disable` 当 id 是 source=local_file 的 skill 时，handler 无 source 拦截（仅 PUT/DELETE 有 403），直接 `registry.UpdateState` + `store.Save`。`Store.Save` 是 `INSERT OR REPLACE INTO skills`，DB 出现一条 source=local_file、state=disabled 的记录。重启后 `LoadAll` 经 `store.ListAll()` 注册这条 disabled 记录，随后 `LoadGlobal` 重扫文件用 sourceRank 相等的 local_file 覆盖回 enabled——禁用状态重启后丢失，且 DB 残留与文件系统重复的 local_file 行。
- **建议**：`handleEnableSkill/handleDisableSkill` 开头加 `if s.Source == skill.SkillSourceLocalFile { 403 "edit the file directly" }`，与 PUT/DELETE 路径拦截对齐。

### H3. CommandBar ↑/↓ 导航因 App.vue 未监听事件而失效（Spec3）
- **文件**：`web/v2/src/App.vue:1115`
- **失败场景**：CommandBar.handleKeydown `emit('update:pickerSelectedIndex', props.pickerSelectedIndex + 1)`，但 App.vue 三处 `<CommandBar>` 只绑 `:picker-selected-index="skillPickerSelectedIndex"` 单向 prop，无 `@update:pickerSelectedIndex` 监听。`skillPickerSelectedIndex` 永远停在 0，SkillPicker 高亮不随 ↓/↑ 移动，Enter 只能选第一项。spec R6.1 的 ↑/↓/Enter/Esc 形同虚设。
- **建议**：三处 `<CommandBar>` 加 `@update:picker-selected-index="skillPickerSelectedIndex = $event"`，与 `v-model:picker-open` 同步模式对齐。

### H4. CommandRegistry 无 workdir 命名空间，同名命令互相覆盖（Spec3）
- **文件**：`internal/skill/command_registry.go:21`
- **失败场景**：全局 `.claude/commands/greet.md` 产生 ID=greet（scope=global），项目 `/proj/.claude/commands/greet.md` 也产生 ID=greet（scope=project）。`byID` 是单维 map，`Register` 按 ID 覆盖。LoadAll 先注册全局，session 创建触发 LoadForWorkdir 注册项目 greet → 覆盖 global 版本 → global greet 永久丢失。design.md 明确要求"按 workdir 隔离"。
- **建议**：按 design 备选 key 格式 `global:<id>` / `project:<workdir>:<id>` 存储，Get/ListForWorkdir 对外按裸 ID 解析但内部按 scope+workdir 命名空间隔离。

### H5. skill_command_* 事件前端未订阅，命令列表不实时刷新（Spec2↔Spec6 接缝）
- **文件**：`internal/skill/events.go:41-47` + `web/v2/src/types/events.ts:7-93` + `web/v2/src/composables/useSkillEvents.ts:20-27`
- **失败场景**：后端 CommandLoader 广播 `skill_command_loaded/unloaded/changed`，但前端 EventType 联合与 `useSkillEvents.SKILL_EVENT_TYPES` 白名单均未包含。事件到前端被 `isSkillEvent` 过滤丢弃，命令列表不实时刷新，用户必须手动 reload。
- **建议**：events.ts 追加三个 `skill_command_*` 类型，`useSkillEvents` 收录并在 onEvent 中触发 `useSkillCommands.loadCommands` 刷新。

### H6. POST /api/skills 拒绝 session scope，但 spec 明确允许 local_db 设置 session（Spec1）
- **文件**：`cmd/server/api_skill.go:255-257`
- **失败场景**：spec.md:94 明确"local_db 可设置 scope=project 或 session"，但 `api_skill.go:255` 校验 `if scope != global && scope != project → 400`，把 session 也拒绝。前端 SkillForm.vue:40 仍把 Session 作为可选选项，用户选 Session 提交直接 400。spec 与实现不一致，前端 UX 死路。
- **建议**：放行 `SkillScopeSession`（与 ResolveActiveSkills 的"excluded for now"语义一致：可写入、不注入），PUT 路径同步放行；或修 spec.md 措辞澄清"session 本次不实现存储"。推荐前者。

---

## Medium（16 条）

### M1. POST /api/skills/scan-config 调 Reload() 清空项目级 local_file skill（Spec2）
- **文件**：`cmd/server/api_skill.go:604-610`
- **失败场景**：用户在 workdir 放了 `.claude/skills/foo/SKILL.md` 已加载。调 `POST /api/skills/scan-config` 关闭 `.agents/skills` → `loader.Reload()` 先 unregister 所有非 built_in（含全部 local_file project skill），再只重跑 `LoadGlobal`（不重扫 workdir）→ foo skill 从 registry 消失，直到手动 scan 或新建 session。spec 明说"不改变已扫描 workdir"，实际清空了。
- **建议**：刷新改为只 unregister source=local_file 且 scope=global 的 skill 再 LoadGlobal，保留 project scope；不要复用 Reload。

### M2. frontmatter 正则不兼容 CRLF（Spec2 + Spec3 共同）
- **文件**：`internal/skill/file_loader.go:225` + `internal/skill/command_loader.go:118`
- **失败场景**：Windows 编辑器默认 CRLF，`---\r\nname: foo\r\n---\r\nbody`。`frontmatterRegex = (?s)^---\n(.*?)\n---\n(.*)$` 只认 `\n`，整条不匹配，回退把整段含 `---` 分隔符的原文作为 system_prompt 模板，frontmatter 字段全丢，skill 以目录名注册，Engine 注入带字面 `---`。
- **建议**：正则改 `(?s)^---\r?\n(.*?)\r?\n---\r?\n(.*)$`，或匹配前 `strings.ReplaceAll(content, "\r\n", "\n")`。两个 loader 同步修。

### M3. 空_frontmatter（`---\n---\n`）不匹配正则，分隔符被当 body（Spec2）
- **文件**：`internal/skill/file_loader.go:225,245-253`
- **失败场景**：SKILL.md 内容 `---\n---\n`，正则不匹配，回退把 `---\n---` 当 body 写进 Templates[0].Content，最终字面字符串渲染进 system prompt。测试只断言 `reg.Get("foo")` 存在而通过。
- **建议**：用更宽松正则让空 frontmatter 匹配，body 为空时跳过模板创建或标记 invalid。

### M4. POST /api/skills/scan 的 unloaded 恒为 0（Spec2）
- **文件**：`cmd/server/api_skill.go:657-668`
- **失败场景**：spec 要求返回 `{scanned_workdirs, loaded, unloaded}`，代码 `unloaded := 0` 注释"占位"，`loaded` 是 refresh 后总数（非本次新增）。调用方无法判断本次卸载了多少。
- **建议**：`FileLoader.RefreshAll` 内部统计卸载数，通过返回值暴露给 handler 据实返回。

### M5. registerSkillRoutes 未挂权限控制（Spec2）
- **文件**：`cmd/server/server.go:480`
- **失败场景**：同 server 里 `/api/agents`、`/api/tools`、cron 状态切换都加了 `auth.RequireRoleFunc(RoleAdmin)`，唯独 `registerSkillRoutes` 整组（含 scan-config 改全局配置、scan 触发全量重扫）无 auth。任意未认证客户端可改扫描配置、触发 IO 与事件风暴。
- **建议**：对 POST /api/skills/scan-config、scan、create、enable/disable 加 RoleAdmin 检查。

### M6. handleTriggerSkill 仅 prefill 未自动发送（Spec6）
- **文件**：`web/v2/src/App.vue:921`
- **失败场景**：用户点 Trigger → handleTriggerSkill 只 enableSkill 并设 `prefilledCommand='/id '`，期望立即发出任务，实际只填入输入框，必须再手动 Ctrl+Enter。与 spec 11 相反。
- **建议**：enableSkill 成功后直接 `handleSend('/' + id + ' ', {...})`，保留 prefill 作可选回退。

### M7. SkillForm 编辑模式 parameters 静默丢失（Spec6）
- **文件**：`web/v2/src/components/SkillForm.vue:114`
- **失败场景**：用户只改 parameters JSON（templates 未动）→ templatesJson 对比相等 → if 分支整段跳过 → `changes.parameters` 不赋值 → PUT 不含 parameters，修改丢失。
- **建议**：把 parameters 变更检测独立：`if (parametersJson.value !== JSON.stringify(props.skill.parameters||[],null,2)) changes.parameters = parameters`，不与 templates 绑定同一 if。

### M8. useSkills 事件同步路径未测试覆盖（Spec6）
- **文件**：`web/v2/src/composables/__tests__/useSkills.spec.ts:203`
- **失败场景**：若 ensureSubscribed/onEvent 路径有 bug（data.state 缺失、skill_unloaded 未触发 removeSkill、skill_rendered 未写入 injectedSkillBlocks），测试不会发现；当前测试等同直接赋值。
- **建议**：mock useWebSocket.onEvent，模拟分发 skill_enabled/disabled/unloaded/rendered 事件，断言 skills.value 与 enabledIds/injectedSkillBlocks 增量更新。

### M9. isCommandScopeAllowed 用裸 HasPrefix，与 ListForWorkdir 语义不一致（Spec1↔Spec2↔Spec3 接缝）
- **文件**：`cmd/server/api_skill_command.go:291-298`
- **失败场景**：`cmd.WorkspaceDir=/repo/proj`，invoke 传 `workdir=/repo/proj-evil` → ListForWorkdir 的 `isSubDirOrEqual` 返回 false（分隔符感知）不列出，但 invoke 的 `strings.HasPrefix("/repo/proj-evil","/repo/proj")=true` 放行 → "列表看不到但能 invoke"，且 workdir 越权。
- **建议**：`isCommandScopeAllowed` 改调 `skill.isSubDirOrEqual`（导出或移公共包），统一三处 workdir 判定。

### M10. GET /api/skills?workdir= 精确等值，与 ResolveActiveSkills 子目录语义不一致（Spec1↔Spec2 接缝）
- **文件**：`cmd/server/api_skill.go:139` + `internal/skill/registry.go:113-114`
- **失败场景**：project skill `WorkspaceDir=/repo`，session workdir=`/repo/subdir` → ResolveActiveSkills 的 `isSubDirOrEqual` 判 true 运行期注入，但前端 `GET /api/skills?workdir=/repo/subdir` 精确等值过滤掉 → UI 看不到却实际生效的 skill，无法管理。
- **建议**：GET ?workdir= 改用 `isSubDirOrEqual`，与运行期同语义。

### M11. workdir 判定三处实现不一致（Integration，汇总 M9+M10）
- 三处：`ResolveActiveSkills.isSubDirOrEqual`（规范化+分隔符感知）、`GET /api/skills?workdir=`（精确等值）、`isCommandScopeAllowed`（裸 HasPrefix）。同一 workdir 在列表/运行期/invoke 返回不同 skill 集合。
- **建议**：抽一个公共 `MatchWorkdir(skillDir, workdir)` 函数，三处共用。

### M12. UnloadForWorkdir 复用 ListForWorkdir，会把全局命令一并删除（Spec3）
- **文件**：`internal/skill/command_loader.go:78`
- **失败场景**：`UnloadForWorkdir(workdir)` 遍历 `ListForWorkdir(workdir)`（同时返回 global + 匹配 project），Unregister 把所有全局命令也删掉。紧接着只重扫当前 workdir，全局命令不会重新加载。每次 session 切换丢一次全局命令库。
- **建议**：`UnloadForWorkdir` 只卸载 `Scope==project && WorkspaceDir==workdir` 的条目，跳过 global；或新增 `ListProjectForWorkdir`。

### M13. isValidCommandID 已实现但从未调用，frontmatter id 无字符校验（Spec3）
- **文件**：`internal/skill/command_registry.go:92`
- **失败场景**：frontmatter 写 `command: ../evil` 或 `id: a b c`，parseCommandFile 直接采用，Register 照单全收，可能引发路由匹配异常或 skill ID 碰撞。`isValidCommandID` 是死代码。
- **建议**：parseCommandFile 生成 id 后、Register 前调 `isValidCommandID(id)`，非法则回退路径生成。

### M14. invoke 启用关联 skill 时 SkillID 不存在则静默忽略（Spec3）
- **文件**：`cmd/server/api_skill_command.go:208`
- **失败场景**：frontmatter 写 `skill: openspec-new-change` 但该 skill 不在 registry。`enableSkillByID` 的 `Get` 失败直接 `return nil`，响应 `enabled_skill_ids` 仍 append 该 ID，前端以为启用成功，实际 ActiveSkills 不含它。
- **建议**：Get 失败时返回 error 让 invoke 报 4xx/5xx，或从 enabled_skill_ids 剔除并加 warning。

### M15. Agent Tool 路径完全不广播 skill 事件（Spec4↔Spec6 接缝）
- **文件**：`internal/skill/tools.go:658`
- **失败场景**：LLM 在 run 中调 skill/create_local、update_local、delete_local、enable、disable 成功后不广播 skill_loaded/changed/unloaded/enabled/disabled。前端 useSkills 列表不刷新，用户在 SkillManager 看不到 LLM 刚创建的 skill，直到手动刷新。注释"事件由调用方/REST 广播"但 Engine executeToolCall 调 skill tool 后不补发。
- **建议**：给 skill tool 注入 EventBus（对齐 worktree tool 的 WorktoolDeps.Bus 模式），成功后广播；或 Engine 识别 namespace=="skill" 的 tool 结果后补发。

### M16. runner.go 用包级 globalSkillRegistry 而非 r.Deps.SkillRegistry（Spec1）
- **文件**：`cmd/server/runner.go:1003`
- **失败场景**：AgentDeps 已有 SkillRegistry 字段且已注入，但 runAgentLoopWithTurn/Recover 直接读包级 globalSkillRegistry，绕过 Deps。测试隔离不自然（skill_e2e_test.go 不得不手动设 globalSkillRegistry）。与 Phase 8-A 依赖注入收口原则相悖。
- **建议**：runner.go:344 与 1003 改 `r.Deps.SkillRegistry`，同步修 spec.md 措辞。

---

## Low（14 条）

### L1. findMatchingSkillBlock 恒返回首个 block，所有 system message 误标 badge（Spec5）
- `web/v2/src/components/ContextWindowPanel.vue:60` — 改为聚合 badge（"skill: N injected"）或后端为 system message 加 skill_origin 元数据。

### L2. skill_rendered 在无模板注入时仍广播空 skill_blocks（Spec5）
- `internal/runtime/engine.go:639` — 把 `bus.SendEvent` 移入 `if len(injectedBlocks) > 0` 守卫。

### L3. estimateSkillBlockTokens 每 block 各加 5 开销，多块场景高估（Spec5）
- `internal/runtime/engine.go:697` — 非首块省略 +5 overhead，或标注"含 per-block 估算开销"。

### L4. 缺 NewEngine 负向测试（Spec5）
- `internal/runtime/engine_skill_test.go:76` — 新增 TestEngineSkillRenderedNotBroadcastWhenInactive（nil registry、空 ActiveSkills、含不存在 ID 三场景）。

### L5. SkillForm 暴露 session scope 但后端拒绝（Spec6，与 H6 相关）
- `web/v2/src/components/SkillForm.vue:40` — session option 加 '(coming soon)' 禁用，或随 H6 一起放行。

### L6. SkillManager 编辑 built_in 无 Fork 语义提示（Spec6）
- `web/v2/src/components/SkillManager.vue:196` — SkillForm 检测 source==='built_in'，标题改 'Fork Skill'，按钮 'Fork & Save'，加提示条。

### L7. ManageContent 测试 stub 仍引用旧 SkillPanel（Spec6）
- `web/v2/src/components/ManageContent.test.ts:160` — stub 改 SkillManager，补 skills tab 路由断言。

### L8. Renderer nil-safe 测试缺失 + SkillParameter.Default 未实现（Spec1）
- `internal/skill/renderer.go:24` — 加 TestRenderNilSafe；评估 spec 的"preferring SkillParameter.Default"是否本 Spec 范围，若是需 Render 接收 Parameters 回退 Default。

### L9. Recover 路径无 `<cwd>/workspace` 兜底，与 chat 路径行为不一致（Spec1）
- `cmd/server/runner.go:268-284` — 抽 helper 让 Recover 与 runAgentLoopWithTurn 共用 workspaceDir 解析。

### L10. forked_from 在二次编辑已存在 shadow 时返回 false（Spec4）
- `internal/skill/tools.go:368` — 改 `existing.Source == built_in || IsShadowOfBuiltIn(existing)`，让 LLM 二次编辑仍知底层是 shadow。

### L11. skill/enable / skill/disable Agent Tool 缺 scope 越权检查（Spec4）
- `internal/skill/tools.go:615` — toggleSkill 读 callerProjectID，`skill.Scope==project && callerProjectID != skill.ProjectID` 则拒绝。与 C3 同步修复。

### L12. 启动时未把默认 skill_scan_dirs 写入 settings（Spec2）
- `cmd/server/main.go:949-978` — 启动时若 GetSetting 为空，SetSetting 写入 JSON 化 DefaultSkillScanDirs；或修 tasks.md 注明以运行时回退代替落库。

### L13. invoke body 解析错误静默忽略（Spec3）
- `cmd/server/api_skill_command.go:191` — Decode err 时 400 'invalid json body'，允许 query param workdir fallback。

### L14. 测试缺口汇总
- `internal/skill/file_loader_test.go:86` — 未覆盖 `.agent/skills` 与 `.opencode/skills` 模板。
- `internal/skill/file_loader_test.go:116` — 未断言 skill_unloaded/loaded 事件广播（用例传 nil bus）。
- `internal/skill/command_loader_test.go:24` — 未覆盖全局/项目同名 ID 冲突、CRLF、临时 skill 注入端到端。
- `cmd/server/api_skill_command_test.go:97` — invoke skill 关联分支未断言（注释自述跳过）。
- `internal/skill/tools_test.go:347` — 未断言 skill/list/search 的 summary 裁剪（templates/parameters 不泄露）。
- `cmd/server/api_skill_project_test.go:101` — E2E 测试只断言 ResolveActiveSkills 函数返回，未启动 runner 验证 EngineConfig.ActiveSkills 真实注入。

---

## 驳回汇总

- 8 条（首轮 Spec5/6 限流批次）标注 "verdict missing"——对抗式验证阶段未产出有效裁决，无法据以成立。
- 2 条经实质核查后驳回：useSkills/useSkillEvents 双订阅实为冗余死代码而非状态分叉、spec 引用夸大（design.md 无 §8 单一事件源条款）。
- 附带建议：清理 `useSkills.injectedSkillBlocks` 与 `useSkillEvents.enabledSkillIds` 两处死代码状态。

---

## 跨 Spec 衔接良好的点（seam strengths）

- `context_window_snapshot.skill_blocks` 字段名（skill_id/template_name/estimated_tokens/char_count）与前端 SkillBlock 类型、useSkillEvents/useSkills 事件处理完全对齐，无字段名漂移。
- fork-on-edit 逻辑在 REST `handleUpdateSkill` 与 Agent Tool `skillUpdateLocalTool` 双路径一致：built_in→local_db shadow、SourceURL 清空、IsLocalEditable=true；delete shadow 恢复 built_in 两路径也一致。
- sourceRank 权威顺序 `local_db(3) > local_file(2) > built_in(1)` 在 file_loader.go:198 定义后，LoadAll 顺序（builtins→store→fileLoader）与 registerOrUpdate 的 rank 比较共同保证同 ID 跨来源覆盖可预测。
- skill_rendered 事件仅在 `len(ActiveSkills)>0 且 SkillRegistry 非 nil` 时广播，blockData 与 injectedBlocks 1:1 构建，context_window_snapshot 的 toLLMSkillBlocks 也基于同一 injectedSkillBlocks，两条事件链路同源。

---

## 优先级动作清单

| 优先级 | 动作 | 文件 | 对应条目 |
|--------|------|------|----------|
| **P0** | 临时命令 skill 改 scope=global 或 ActiveSkills 显式追加 | registry.go:115 / api_skill_command.go:267 | C1 |
| **P0** | orchestrator 子 agent 注入 SkillRegistry/ActiveSkills/SkillVariables | orchestrator.go:1030 | C2 |
| **P0** | Engine 注入 _project_id 或 skill tool 实现 CtxTool | engine.go:2065 / tools.go:344 | C3 |
| **P0** | resolveSession 新建 session 调 LoadForWorkdir | persistence.go:170 | H1 |
| **P0** | REST enable/disable 拦截 local_file | api_skill.go:480 | H2 |
| **P0** | CommandBar @update:pickerSelectedIndex 监听 | App.vue:1115 | H3 |
| **P1** | CommandRegistry 按 scope+workdir 命名空间存储 | command_registry.go:21 | H4 |
| **P1** | 前端订阅 skill_command_* 事件 | events.ts / useSkillEvents.ts | H5 |
| **P1** | 放行 session scope 或修 spec 措辞 | api_skill.go:255 | H6 |
| **P1** | 修复 SkillForm parameters 提交丢失 | SkillForm.vue:114 | M7 |
| **P1** | 修复 handleTriggerSkill 偏离 spec | App.vue:921 | M6 |
| **P1** | scan-config 不复用 Reload，保留 project skill | api_skill.go:604 | M1 |
| **P1** | frontmatter 正则兼容 CRLF（两个 loader） | file_loader.go:225 / command_loader.go:118 | M2 |
| **P1** | 抽公共 MatchWorkdir，三处 workdir 判定统一 | api_skill.go:139 / api_skill_command.go:291 | M9/M10/M11 |
| **P2** | Agent Tool 路径广播 skill 事件 | tools.go:658 | M15 |
| **P2** | UnloadForWorkdir 只卸载 project scope | command_loader.go:78 | M12 |
| **P2** | isValidCommandID 接入注册路径 | command_registry.go:92 | M13 |
| **P2** | invoke SkillID 不存在时报错或剔除 | api_skill_command.go:208 | M14 |
| **P2** | scan unloaded 据实返回 | api_skill.go:657 | M4 |
| **P2** | registerSkillRoutes 加 RoleAdmin | server.go:480 | M5 |
| **P2** | 空 frontmatter 处理 | file_loader.go:225 | M3 |
| **P2** | 补 useSkills 事件同步测试 | useSkills.spec.ts:203 | M8 |
| **P2** | runner.go 改用 r.Deps.SkillRegistry | runner.go:1003 | M16 |
| **P3** | skill_rendered 空广播守卫 | engine.go:639 | L2 |
| **P3** | token 估算 per-block overhead | engine.go:697 | L3 |
| **P3** | NewEngine 负向测试 | engine_skill_test.go:76 | L4 |
| **P3** | ContextWindowPanel badge 聚合 | ContextWindowPanel.vue:60 | L1 |
| **P3** | SkillForm built_in fork 提示 | SkillManager.vue:196 | L6 |
| **P3** | ManageContent.test stub 更新 | ManageContent.test.ts:160 | L7 |
| **P3** | Renderer nil-safe 测试 + Default 评估 | renderer.go:24 | L8 |
| **P3** | Recover workspace 兜底 helper | runner.go:268 | L9 |
| **P3** | forked_from 二次编辑语义 | tools.go:368 | L10 |
| **P3** | skill/enable/disable scope 检查（随 C3） | tools.go:615 | L11 |
| **P3** | 启动写入默认 scan_dirs 或修 tasks | main.go:949 | L12 |
| **P3** | invoke body 解析错误返回 400 | api_skill_command.go:191 | L13 |
| **P3** | 测试缺口补齐（见 L14 汇总） | 多文件 | L14 |

---

## tasks.md 误标完成汇总

| Spec | Task | 标记 | 实际 |
|------|------|------|------|
| Spec1 | task 8 "E2E 创建 project scope skill 并验证运行期注入" | [x] | 只断言 ResolveActiveSkills 函数返回，未启动 runner 验证 EngineConfig 注入 |
| Spec1 | task 6 "runAgentLoopWithTurn 与 Recover 两个入口注入" | [x] | orchestrator 子 agent 第三入口未注入 |
| Spec2 | task 5 "api.go 创建/解析 session 后调用 LoadForWorkdir" | [x] | 仅 handleSessions POST 一处，resolveSession 等四入口未调 |
| Spec2 | task 1 "默认写入 skill_scan_dirs = 4 目录 JSON" | [x] | main.go 未 SetSetting，靠运行时回退 |
| Spec2 | task 6 "load/unload/refresh 时发送 skill_* 事件" | [x] | 测试均传 nil bus，事件广播路径无覆盖 |
| Spec4 | task 9 "测试 — tools_test.go" | [x] | spec.md:65 要求的 project scope 越权修改测试缺失 |
| Spec4 | task 4 "skill/enable/disable — scope 权限检查" | [x] | scope 权限检查未实现 |
| Spec4 | verify.md:43 "project scope 越权修改被拒绝" | — | 防护代码失效且无测试 |
| Spec6 | handleTriggerSkill 按 spec 11 自动发送 | [x] | 仅 prefill 未发送 |

---

## 后续建议

1. **P0 先行**：C1/C2/C3 + H1/H2/H3 是真实路径下的功能失效或越权/污染，建议作为单独修复 change 优先落地，每条配回归测试（尤其 C3 的越权拒绝测试、C1 的临时 skill 注入端到端断言）。
2. **workdir 判定统一**（M9/M10/M11）是跨 Spec 的系统性小重构，建议抽公共函数一次性收敛，消除"列表/运行期/invoke 三处不一致"。
3. **tasks.md 把关机制**：在 review checklist 中加入"task 勾选必须附代码级证据（文件:行号或测试名）"，避免"标 [x] 但未实现"再发生。
4. **前端测试质量**：在 review checklist 中加入"断言是否真正触达被测路径"，杜绝同义反复与死 stub。
5. **CRLF frontmatter（M2）** 在 Windows 开发环境下影响所有 `.claude/skills` 与 `.claude/commands` 文件，建议优先修。
