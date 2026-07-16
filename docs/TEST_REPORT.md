# Multi-Agent Platform — 全方位端到端测试报告

> 生成日期：2026-07-12  
> 评测范围：5 个维度端到端评测（WS 事件流 / Policy 安全门 / 多 Agent 编排 / Auth / Case 回归）  
> 评测方式：5 个串行子 agent，每个独立端口 + 独立临时 DB，mock LLM 模式为主  
> 后端版本：commit `231e403`（feat: Phase 2.5 UI 修复批次 — F5/F6/F10/F11）

---

## 0. 评测总览

| 维度 | 脚本 | PASS | FAIL | SKIP | 严重发现数 |
|------|------|------|------|------|-----------|
| A. WebSocket 事件流 | `scripts/ws-smoke.go` | 2 | 1 | 0 | 1 |
| B. Policy 安全门 | `scripts/policy-smoke.sh` | 3 | 2 | 2 | 2 |
| C. 多 Agent 编排 | `scripts/multi-agent-smoke.sh` | 7 | 5 | 0 | 4 |
| D. Auth 开启模式 | `scripts/auth-smoke.sh` | 16 | 0 | 1 | 0（2 中危设计缺口）|
| E. 6 预设 Case 回归 | `scripts/cases-regression.sh` | 6 | 0 | 0 | 0（2 高危设计问题）|
| **合计** | | **34** | **8** | **3** | **7 严重 + 4 中危** |

**结论**：入口层（HTTP 响应、事件流广播、mock 回归、auth 写保护）基本可用；**持久化层与并发层存在多个严重 bug**，多 Agent 编排和 Policy 安全门的端到端可靠性不足，前端无法仅靠 HTTP API 还原完整执行状态。

---

## 1. 维度 A — WebSocket 事件流

### 实测事件序列
**dialogue（纯对话）**：
```
task_started → agent_ready → step_started → llm_thinking →
llm_delta → llm_message_complete → step_complete → agent_status →
observation → task_completed   (10 条)
```
**research（带 tool_call）**：
```
task_started → agent_ready → step_started → llm_thinking →
llm_message_complete → step_complete → agent_status →
step_started → tool_call_started → tool_call_output → tool_call_complete →
observation → step_complete → step_started → llm_thinking → llm_delta →
llm_message_complete → step_complete → agent_status → observation →
task_completed   (21 条)
```

### 结论
- ✅ 核心事件相对顺序符合设计，字段完整（task_id/agent_id/session_id/input/output）。
- ✅ tool_call 三联事件（started→output→complete）顺序正确。
- ❌ **cancel/pause/resume 控制消息未实现**：`cmd/server/main.go:84` 明确 `TODO: Phase 4+ — implement actual engine control via context cancellation`，controlHandler 只处理 approve/deny，三种控制 action 被静默忽略。前端无法中止运行中的任务。
- ⚠️ 设计序列未列出的扩展事件：`agent_ready`（engine.go:499）、`agent_status`（engine.go:623，携带 usage）、`session_status`（main.go:937）——属 Phase 6-D 增强，非 bug，建议补文档。
- ⚠️ "最终答案 observation" 在 step_complete 之后发送（engine.go:680），与设计序列"observation → step_complete"相反，属合法变体。

---

## 2. 维度 B — Policy 安全门

### 拦截矩阵
| 规则 | 测试输入 | 结果 | 证据 |
|------|---------|------|------|
| DangerousCommandRule | `rm -rf /tmp/...` | ✅ PASS | task=failed，目标目录仍存在 → 被审批超时拦截 |
| PathTraversalRule | `write_file ../../../etc/passwd` | ✅ PASS | task=failed → 含 `..` 路径被拦截 |
| FileScopeRule | `write_file /etc/passwd`（Unix 绝对路径） | ❌ **FAIL** | task=completed，文件落到 `项目根/etc/passwd` |
| ApprovalRule | `write_file /etc/...` | ✅ PASS | task=failed + 文件未创建 → 30s 审批超时拒绝 |
| 控制测试 | `echo safe` | ✅ PASS | task=completed → Policy 不误杀 |
| TokenBudgetRule | — | ⏭ SKIP | DefaultContract.TokenBudget=0，API 无参数覆盖 |
| ToolWhitelistRule | — | ⏭ SKIP | DefaultContract.AllowedTools=nil，API 无参数覆盖 |

### 严重发现
1. **[严重-Windows] FileScopeRule 对 Unix 绝对路径放行**：`harness.go:541` 用 `filepath.IsAbs`，Windows 上对 `/etc/passwd` 返回 false，随后 `filepath.Join` 并入 scope，文件被写到项目 `etc/` 目录。**实测确认文件落盘**。跨平台安全漏洞。
2. **[设计] Engine 把所有 ErrBlockedByPolicy 转审批流程**：`engine.go:1213-1222` 将硬性安全拦截转为 `ErrApprovalRequired`，导致每次拦截都要等 30s 审批超时才失败。PathTraversal/FileScope/ToolWhitelist 应立即失败。
3. **[可观测性] Policy 拦截原因未持久化**：`handleApprovalRequired` 不调 `saveStep`，被拦截的 tool_call step 不出现在 `GET /api/tasks?id=` 的 steps 中，`final_result` 为空，历史回放无法还原拦截事件。
4. **[规则缺口] CostBudgetRule 未加入 PolicyChain**：`cost_budget_rule.go` 已实现有单测，但 `main.go:822-830` 的链里没有它，端到端不生效。
5. **[API 缺口] /api/tasks body 无法设置 TaskContract 字段**：Scope/AllowedTools/TokenBudget/CostBudgetUSD/Permissions 只能来自 preset cases 或 DefaultContract，限制 policy 可测试性与灵活性（TokenBudget/ToolWhitelist 端到端不可触发的根因）。
6. **[误判] isHighRiskFilePath 子串匹配**：`approval.go:530` 用 `strings.Contains(path,"/etc/")`，`./etc/x`（项目内子目录）不匹配，可绕过审批。
7. **[审批超时] DangerousCommandRule 命中后也走 30s 审批**：`rm -rf` 这类命令应硬拦截，不该给"审批通过即可执行"的口子（除非显式 `AllowShellDangerous`）。

---

## 3. 维度 C — 多 Agent 编排

### 实测响应
| case_type | agent_count | agent_ids | root status |
|-----------|-------------|-----------|-------------|
| multi_agent | 2 | [researcher, writer] | completed |
| code_gen | 1 | [coder] | completed |
| default | 1 | [default] | completed |

入口层（HTTP 响应、TaskDecomposer 拆分）基本可用。

### 严重发现（持久化 + 并发层）
1. **[严重] child_tasks 永远空**：`orchestrator.runAgent`（orchestrator.go:256）创建子任务时未调 `SaveTaskMeta` 设置 `parent_task_id`，`QueryChildTasks` 查 `WHERE parent_task_id=root` 永远空。前端无法看到子任务树。
2. **[严重] 子任务记录因 SQLITE_BUSY 丢失**：多 agent 并行 goroutine 共享 `*sql.DB`，`db.Init` 未设 `PRAGMA busy_timeout`/WAL，并发 INSERT 报 `database is locked (5)`，子任务和 steps 都可能丢失。
3. **[严重] step ID 碰撞**：`persistence.go:36` 用 `step_{taskID}_{stepIdx}_{type}` 作主键，多 agent 并行时 stepIdx 都从 0 开始，日志报 `UNIQUE constraint failed: steps.id`，部分 agent steps 丢失。
4. **[严重] root task agent_ids 为空**：`resolveSession` 先用空 agentIDs 创建 root task，后续 `SaveTask` 因主键冲突失败（`InsertTask` 无 `ON CONFLICT`），root task 的 `agent_ids` 永远 `[]`。

### 设计缺口
5. 子任务 status 永远 running（engine.updateTask 用 rootTaskID，不更新子任务）。
6. steps 全挂在 root taskID 下，子任务查询 steps 为空，无法独立回放。
7. `Strategy` 字段（pipeline/parallel）无效，`RunBlocking` 无视总是并行。
8. `AgentBus.sendAgentMessage` 无调用方，agent 间无消息传递，researcher 成果不传给 writer。
9. 竞态：root task 状态由最后完成的 agent 覆盖。
10. `task_started` 事件 `agent_id="orchestrator"` 与子 agent 混合，前端需特殊处理。
11. 无 agent_ids 合法性校验。

---

## 4. 维度 D — Auth 开启模式

### 测试矩阵（16/16 PASS）
| 场景 | 期望 | 实际 |
|------|------|------|
| 无 token POST 受保护端点 | 401 | 401 ✅ |
| admin token 访问 | 非 401 | 400（input 空）✅ |
| 创建新 api key | 201 + key | 201 ✅ |
| 新 key 访问 | 非 401 | 400 ✅ |
| 吊销新 key | 200 | 200 ✅ |
| 已吊销 key 再访问 | 401 | 401 ✅ |
| 健康端点无 token | 200 | 200 ✅ |
| 错误/空/缺前缀 token | 401 | 401 ✅ |
| DELETE 不存在 key | 404 | 404 ✅ |

Bearer 校验链路、key 生命周期（创建/列出/吊销）、双保险吊销校验（SQL `WHERE revoked_at IS NULL` + `IsRevoked()` 兜底）全链路工作正常。

### 设计缺口（无运行时 FAIL，但中危）
1. **[中危] GET 请求一律豁免**：`auth_http.go:91` `if !requiresAuth || r.Method == http.MethodGet` 短路放行所有 GET。`REQUIRE_AUTH=true` 下 `GET /api/tasks`、`GET /api/costs`、`GET /api/memories` 等无 token 可访问。
2. **[中危] `GET /api/auth/api-keys` 无 token 可列出所有 key 元数据**（含 prefix 前 12 字符），离线碰撞成本下降。建议纳入 protectedRoutes 或按 user_id 过滤。
3. **[中危] 无 RBAC/role 校验**：`auth.Role` 定义了 admin/user/viewer 且有单测，但中间件与 handler 从未读取或校验 role。无"admin 专属端点"概念，所有有效 token 权限等同。
4. **[低危] `handleAPIKeyByID` 错误的 `errors.Is` 判断**（`auth_http.go:236`）：`errors.Is(err, errors.New("api key not found"))` 永远 false，靠 `strings.Contains` 兜底才工作。代码异味。

---

## 5. 维度 E — 6 预设 Case mock 回归

### 回归矩阵（6/6 PASS）
| Case | status | steps | tool_call | total_tokens | cost_records | final_result |
|------|--------|-------|-----------|--------------|--------------|--------------|
| code-gen | completed | 4 | write_file | 193 | 2 | 非空 ✅ |
| dialogue | completed | 2 | none | 156 | 1 | 非空 ✅ |
| research | completed | 4 | run_shell | 176 | 2 | 非空 ✅ |
| multi-agent | completed | 2 | none | 287 | 1 | 非空 ✅ |
| long-task | completed | 2 | no | 110 | 1 | 非空 ✅ |
| tool-error | failed | 24 | run_shell | 880 | 8 | 空（failed）✅ |

6 个内置 mock case 在隔离环境下稳定通过，usage 写入链路通，cost 持久化正常。

### 设计问题
1. **[高危] `executeShell` 非零退出码返回 `(result, nil)` 而非 error**（`builtin.go:175-189`）：tool-error mock 无法触发 `tool_call_failed` 事件分支，实际走 `max_steps_exceeded`。Engine 错误处理路径在 mock 模式下从未被验证。真实场景下 shell 失败不终止 ReAct Loop（设计选择，让 LLM 自决重试），但与 case 命名/意图不符。
2. **[高危] FileScopeRule Windows 路径缺陷再现**（同维度 B 发现 1）：code-gen 的 `/tmp/mock_gen.go` 被写到 `项目根/tmp/mock_gen.go`，未触发拦截。已在评测中清理。
3. **[中] `callIndexByCase` 响应序列耗尽后取最后一个**（`mock_provider.go:89-94`）：单响应脚本（tool-error/dialogue）在多轮 ReAct 中无限重复最后响应，靠 max_steps 终止。long-task 单 text 响应同样循环 2 次后由 max_steps 结束。
4. **[低] `cases.All()` 只返回 5 个 case**，缺 tool-error：`/api/cases` 不暴露它，只能靠 `?case=tool-error` query 触发，前端 case 列表无法一键启动。
5. **[低] mock `usage.CompletionTokens` 对 tool_call 用 `len(fmt.Sprintf("%v", ToolCalls))` 估算**：包含结构字符，非真实 token，数值不可用作真实成本核算（验证了链路通，但数值不准）。

---

## 6. 跨维度问题汇总（按严重度排序）

### 🔴 严重（影响功能正确性 / 安全）
| # | 问题 | 维度 | 位置 |
|---|------|------|------|
| S1 | FileScopeRule 在 Windows 上对 Unix 绝对路径放行，文件可写到项目任意子目录 | B, E | harness.go:541 |
| S2 | child_tasks 永远空，前端无法还原多 agent 子任务树 | C | orchestrator.go:256 |
| S3 | 子任务记录因 SQLITE_BUSY 丢失（未设 busy_timeout/WAL） | C | database.go |
| S4 | step ID 碰撞，多 agent 并行时部分 steps 丢失 | C | persistence.go:36 |
| S5 | root task agent_ids 永远空（SaveTask 主键冲突） | C | persistence.go:80 |
| S6 | cancel/pause/resume 控制消息未实现 | A | main.go:84 |
| S7 | Engine 把硬性安全拦截转为 30s 审批超时，应立即失败 | B | engine.go:1213 |

### 🟡 中危（设计缺口 / 可观测性）
| # | 问题 | 维度 | 位置 |
|---|------|------|------|
| M1 | Policy 拦截原因未持久化，历史回放无法还原拦截事件 | B | engine.go:1315 |
| M2 | CostBudgetRule 未加入 PolicyChain，端到端不生效 | B | main.go:822 |
| M3 | /api/tasks body 无法设置 TaskContract 字段，限制 policy 可测性 | B, E | main.go:243 |
| M4 | Auth GET 请求一律豁免，敏感读端点暴露 | D | auth_http.go:91 |
| M5 | 无 RBAC/role 校验，所有有效 token 权限等同 | D | auth_http.go |
| M6 | GET /api/auth/api-keys 无 token 可枚举 key 元数据 | D | auth_http.go |
| M7 | AgentBus/Strategy 未生效，多 agent 间无消息传递 | C | orchestrator.go |
| M8 | executeShell 非零退出码不报 error，tool-error case 名实不符 | E | builtin.go:175 |

### 🟢 低危（代码异味 / 文档偏差）
- isHighRiskFilePath 子串匹配可被相对路径绕过（approval.go:530）
- DangerousCommandRule 命中后走审批而非硬拦截
- mock usage 对 tool_call 用字符串长度估算
- cases.All() 缺 tool-error
- handleAPIKeyByID 错误的 errors.Is 判断（auth_http.go:236）
- task_id 按秒级时间戳生成，1 秒内并发碰撞
- 设计序列未列出 agent_ready/agent_status/session_status 扩展事件

---

## 7. 已确认可用的能力

尽管存在上述问题，以下能力经端到端验证**确实可用**，前端可放心依赖：
- ✅ HTTP REST 全部端点基础可用（冒烟 46 PASS）
- ✅ WebSocket 事件流广播正确，核心事件序列与字段完整
- ✅ MockProvider 6 个内置 case 稳定回归，case_id 精确匹配 + 关键词回退
- ✅ ReAct Loop 单 agent 执行链路完整（think → tool_call → observe → final）
- ✅ Auth 写操作保护 + Bearer 校验 + key 吊销全链路
- ✅ Cost/Metrics 记录链路通（数值精度问题见已知 bug）
- ✅ TaskDecomposer 入口拆分逻辑
- ✅ 危险命令（rm -rf）、路径穿越（..）、/etc/ 审批在 mock 下被拦截

---

## 8. 修复优先级建议

**Phase 7 前必修（阻塞前端多 agent 渲染）**：
1. S3 SQLite busy_timeout + WAL（一行 PRAGMA，解锁所有并发写问题）
2. S4 step ID 加 agent_id 或 uuid 后缀避免碰撞
3. S2 orchestrator.runAgent 调 SaveTaskMeta 设 parent_task_id
4. S5 SaveTask 改 `INSERT OR REPLACE` 或拆分 create/update

**安全加固（阻塞生产部署）**：
5. S1 FileScopeRule 跨平台绝对路径判定（用 `filepath.IsAbs` + Unix 路径显式拒绝）
6. S7 区分 ErrBlockedByPolicy（立即失败）与 ErrApprovalRequired（走审批）
7. M4/M6 收紧 Auth GET 豁免范围，保护敏感读端点

**UX/可观测性**：
8. S6 实现 cancel/pause/resume（维护 task_id → CancelFunc 映射）
9. M1 Policy 拦截原因持久化到 task.final_result 或新表
10. M3 /api/tasks 支持传 TaskContract 字段

---

## 9. 评测脚本清单

| 脚本 | 维度 | 运行 |
|------|------|------|
| `scripts/ws-smoke.go` | A | `go run scripts/ws-smoke.go` |
| `scripts/policy-smoke.sh` | B | `bash scripts/policy-smoke.sh` |
| `scripts/multi-agent-smoke.sh` | C | `bash scripts/multi-agent-smoke.sh` |
| `scripts/auth-smoke.sh` | D | `bash scripts/auth-smoke.sh` |
| `scripts/cases-regression.sh` | E | `bash scripts/cases-regression.sh` |
| `scripts/smoke-test.sh` | 基础冒烟 | `bash scripts/smoke-test.sh` |

所有脚本独立端口 + 独立临时 DB，跑完自动清理，不污染 `data/` 目录。

---

## 10. 真实 LLM 第一轮冒烟（2026-07-13）

> 评测脚本：`scripts/real-llm-smoke.sh`（端口 18200，独立临时 DB，`LLM_USE_MOCK=false` 走 `.env` 真实 endpoint `deepseek-v4-flash-local`）
> 评测方式：leader 调度，1 个子 agent 创建脚本并真实运行，5 个场景串行（控费，不并发）
> 断言哲学：真实 LLM 输出不可预测，改用"结构/状态断言"（任务终态、事件流完整、无 panic、usage 非零），不做内容断言；LLM 行为不可控项（是否生成 tool_call、生成哪个工具）用 SKIP 而非 FAIL
> 当前代码版本：commit `560322b`（真实 LLM 修复批次，2026-07-16）

### 10.0 结果总览

| 场景 | 终态 | 耗时 | 关键证据 |
|------|------|------|---------|
| 1 write_file | completed | 4s | tool_calls=2（write_file+read_file），hello.txt 落盘到 session workspace，tokens=3677 |
| 2 run_shell | completed | 4s | tool_calls=1（run_shell），echo 放行无 POLICY BLOCK |
| 3 multi-agent(2) | failed | 8s | 两个 agent 都 `max steps (3) exceeded`，child_tasks=[] |
| 4 dialogue | completed | 35s | tokens=4943，cost record_count=1 但 usd=0；**Router 未触发**（修复前） |
| 5 run-case | completed | 14s | tokens=2405，正常 |
| G1 无 panic | — | — | 服务日志无 panic/goroutine/nil pointer |

**首轮结果**：PASS=16 / FAIL=1 / SKIP=0，4/5 场景 completed，真实 LLM 链路基本通路。FAIL 唯一项是场景4 Router 触发检查——根因是 Router 死代码（见 10.1 R1）。

### 10.1 发现的真实问题（按严重度）

#### 🔴 R1【严重】Router 在 chat 路径完全未接入（死代码）
- **位置**：`cmd/server/main.go` runAgentLoopWithTurn / `internal/orchestrator/orchestrator.go` runAgent 构建 EngineConfig 时未设置 `Router`/`Registry`/`Providers`；`internal/runtime/engine.go:1115` 条件 `e.cfg.Router != nil && e.cfg.Registry != nil` 永远 false
- **影响**：Phase 6 路线图承诺的分层模型路由 / 意图分类 / `model_routed` 事件从未执行，`llm.NewRouter` 全仓库无调用方
- **状态**：✅ 已修复（task #1）。main.go 启动时构造 Router 沿调用链穿透注入；mock 模式用 `builtin:router-classifier` mock script 不打真实 API

#### 🔴 R1-回归【严重】Router 激活后真实 LLM 全场景 timeout
- **现象**：Router 激活后，场景1 91s 内产生 **1347 条** `[Router] Selected model: deepseek-v4-flash`（无 `-local`），token 无权访问 → 403 → 死循环到超时
- **根因**：DefaultProfiles 注册标准名 `deepseek-v4-flash`，Router 选中它，而 `.env` token 只能访问 `deepseek-v4-flash-local`
- **状态**：✅ 已修复。main.go 先注册 `cfg.LLMModel` 克隆 profile 让 Router 选中 `-local`；修复后 Router 日志 1347 → 9 条，无 403

#### 🔴 R2【严重】isRepeatingError 对 403 永不触发（死循环无法终止）
- **位置**：`internal/runtime/engine.go:1023` 精确匹配 `lastError == errFingerprint`
- **根因**：403 错误含每次不同的 `request id: 20260714...aclHY4az`，指纹每次都变 → 永不判重复 → 死循环到超时（1347 条刷屏根因）
- **状态**：✅ 已修复。新增 `normalizeErrorFingerprint` 归一化 request id，`isRepeatingError`/`recordFeedbackError` 走归一化

#### 🟡 R3【中】steps.id UNIQUE 碰撞（单 agent 真实路径也复现）
- **位置**：`cmd/server/persistence.go:40` step ID = `step_{taskID}_{agentID}_{stepIdx}_{type}` 四元组
- **根因**：`engine.go:834` `for _, tc := range toolCalls` 循环内每个 tool call 执行前都 saveStep(think)，stepIdx 未自增 → 一次 LLM 返回 N 个 tool_calls 时 think 保存 N 次 → `UNIQUE constraint failed: steps.id (1555)`，step 记录丢失
- **状态**：✅ 已修复（task #2）。step ID 加 uuid 后缀 `step_..._{type}_{uuid}`，1555 计数归零

#### 🟡 R4【中】真实花钱但成本记录 CostCents=0
- **位置**：`internal/cost/cost_tracker.go` BuildRecordFromProfile + `internal/llm/model_profile.go:271` DefaultProfiles
- **根因**：模型名 `-local` 后缀不匹配 + DefaultProfiles `deepseek-v4-flash` OutputPrice=0.29 笔误（官方 0.28）
- **状态**：✅ 已修复（task #3，并 commit a2475e7）。DefaultProfiles 修正为官方价（flash $0.14/$0.28，pro $0.435/$0.87）+ 前端 `ModelPricesDialog` 可查看/修改模型价格；D3 浮点改造后 `CostUSD` 提升为主字段，小对话不再截断为 0。cost_cents 列仍保留为 round(USD*100) 的兼容 derived 字段。

#### 🟢 R5【观察】reasoning 模型 4096 MaxTokens 撞上限、Result 空
- **现象**：`deepseek-v4-flash-local` 后端是 Step-3.7-Flash（reasoning 模型），4096 token 全用在思维链、正文为空但标 completed
- **状态**：📋 设计优化计划（task #7，REAL_LLM_DESIGN_PLAN.md D4），未修复，非 bug

#### 🟢 R5.5【已修复】真实 LLM 适配：reasoning 字段解析 / classifier MaxTokens
- **现象**：真实 LLM 下 classifier MaxTokens=10 导致 reasoning 模型 Content 全空、分类失败；Step-3.x `reasoning` 字段缺失时 think 阶段把空 content 当作 final answer
- **状态**：✅ 已修复（commit a2475e7）。classifier MaxTokens 10→512；Content 空时回退 Message.Reasoning；`ChatStream` 解析 `reasoning` 字段并入 content 累积。

#### 🟢 R6【噪声】迁移日志每次启动刷 13+ 行 duplicate column
- **状态**：✅ 已修复（task #4）。`migrate.go` 静默跳过 duplicate column 错误

### 10.2 mock 覆盖不到、真实 LLM 才暴露的代码路径

| 路径 | file:line | mock 为何测不到 |
|------|-----------|----------------|
| SSE 流式分块解析 | `internal/llm/openai_provider.go:120-254` | MockProvider 直接返回完整字符串 |
| tool_call delta 累积 | `openai_provider.go:207-218` | mock tool_call 是一次性完整 JSON |
| `reasoning` 字段处理（reasoning_content / reasoning） | `openai_provider.go` / `client.go` | mock 不含 reasoning |
| 真实 usage 提取 | `openai_provider.go:182-188` | mock 用估算 usage |
| Router 意图分类 + tier 回退 | `internal/llm/router.go:192` | mock classifier 不真实 |
| 错误指纹归一化 | `engine.go:1062` | mock 不产生带 request id 的真实错误 |
| WS broadcast 背压 | `internal/ws/hub.go:79-93` | mock delta 频率低填不满 256 缓冲 |
| Router fallback chain 403 死循环 | `main.go:241-258` / `engine.go:1278-1340` | mock 模型名无权限问题 |

### 10.3 第二轮验证（R1/R2/R3/R6 修复后）

`bash scripts/real-llm-smoke.sh`（2026-07-15） → **PASS=17 / FAIL=0 / SKIP=0 全绿**

- Router 日志 1347 → 9 条，选中 `deepseek-v4-flash-local`，无 403、无死循环
- `Failed to save step: UNIQUE constraint` 计数 = 0（R3 修复）
- 迁移日志 duplicate column = 0（R6 修复）
- 场景1 write_file 落盘成功，task 保存 5 条 step 不再丢失
- cost model 名对齐 `deepseek-v4-flash-local`（R4-OnLLMUsage 修复），cost_cents 仍为 0（待 D3 浮点改造生效）

### 10.4 第三轮验证（D1 Router fallback + D3 cost 浮点改造后，2026-07-16）

`bash scripts/real-llm-smoke.sh` → **PASS=17 / FAIL=0 / SKIP=0 全绿**

| 场景 | 终态 | 关键证据 |
|------|------|---------|
| 1 write_file | completed | 3s，tool_calls=1（write_file），hello.txt 落盘，tokens=2357 |
| 2 run_shell | completed | 2s，tool_calls=1（run_shell），无 POLICY BLOCK |
| 3 multi-agent(2) | **failed** | 64s，Router 正确 fallback：deepseek-v4-pro 403 → deepseek-v4-flash-local；但 researcher 因 `web_search` 工具未注册反复报错，writer 因 malformed JSON 回传导致 API 400 |
| 4 dialogue | completed | 12s，tokens=5027，`total_cost_usd=0.0008509`（D3 浮点生效） |
| 5 run-case | completed | 12s，dialogue case 正常 |

D1 验证结论：
- ✅ Router 意图分类命中 multi-agent 后选择 pro tier，403 后 fallback chain 正确回退到 `cfg.LLMModel`（deepseek-v4-flash-local）
- ✅ `isRepeatingError` 归一化生效，无 403 死循环

D3 验证结论：
- ✅ `/api/costs.total_cost_usd` 返回非 0 浮点值（0.00085 USD 级），小对话不再截断为 0
- ✅ record_count=3，cost 记录链路完整

新增真实问题（场景3 multi-agent 未 green）：

#### 🔴 R7【严重】multi-agent researcher 系统提示硬依赖未注册的 `web_search` 工具
- **位置**：`internal/orchestrator/orchestrator.go:565` TaskDecomposer 的 researcher prompt 包含 `"Use web_search and read_file tools to gather data"`
- **影响**：真实 LLM 下 researcher 调用 `web_search`，Tool Registry 返回 `tool not found: web_search`，被 Engine 判为 repeated error → agent failed；multi-agent root task 因此 failed
- **状态**：🔄 修复中（task #21）。方案： researcher prompt 改为不指定具体工具，或仅使用已注册工具（read_file / write_file / run_shell）

#### 🟡 R8【中】malformed tool_call 被原样回传到 conversation history，导致后续 think 400
- **位置**：`internal/runtime/engine.go:889-898` tool 执行失败/成功时都直接把 `tc` 原样 append 到 `e.messages`，若 `tc.Function.Arguments` 是不完整 JSON，下一个 LLM round-trip 会触发 `400 Unterminated string`
- **影响**：writer 在 researcher 失败后继续执行，因 conversation history 中携带未闭合 JSON，API 400 → repeated LLM error → failed
- **状态**：🔄 修复中（task #21）。方案：executeTool 回写 messages 前对无法解析的 Arguments 做 sanitize（替换为合法占位 JSON 或清空），保留 tool_call_failed 的 observation 给 LLM 即可

#### 🟢 R9【观察】Router classifier 在场景1/4 选择 efficient tier
- 真实 dialogue/write_file 意图被归类为 `simple_chat` → `efficient` tier → `deepseek-v4-flash-local`，符合预期；multi-agent intent 能正确走到 pro tier 并 fallback，说明 D1 的 FallbackModel 重定向生效


*真实 LLM 第一轮报告更新：2026-07-13*  
*真实 LLM 修复批次合并+D1/D3 状态同步：2026-07-15*  
*评测执行：1 个子 agent 创建+运行（leader 调度，串行控费）*  
*真实 LLM endpoint：`deepseek-v4-flash-local`（aicoding.dobest.com 代理，后端 Step-3.7-Flash）*

---

*mock 端对端评测：2026-07-10*  
*后端 commit：`231e403`*
