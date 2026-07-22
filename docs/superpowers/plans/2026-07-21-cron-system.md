# Cron / 定时器系统 实现计划

> **For agentic workers:** 每个任务独立可测、TDD、频繁提交。步骤用 `- [ ]` 跟踪。
> 设计文档：`docs/superpowers/specs/2026-07-21-cron-system-design.md`（必读，所有契约以此为准）。
> 分支：`phase-7-cron`。

**Goal:** 为平台新增独立定时器子系统 `internal/cron/`，支持 Agent tool + Web UI 双入口创建，4 种 action_type，完全事件化，v2 前端管理页 + 侧边 dock。

**Architecture:** `internal/cron/` 平级新模块（model / store / scheduler / executor / action / service / tools）；`cmd/server` 抽出 `startChatTask` 供 cron 复用；`pkg/db` 加 migration v26；`pkg/event` 加 `cron_*` 常量；前端 v2 加 `CronManager` + `CronDockPanel` + 2 个 composable。

**Tech Stack:** Go 1.25 / `github.com/robfig/cron/v3`（秒级，6 域）/ modernc.org/sqlite / Vue 3 + TS / Vitest。

---

## 文件结构

后端新增：
- `internal/cron/model.go` — 领域类型（Cron / Execution / ActionType / ScheduleType / Status / ActionPayload 子结构）
- `internal/cron/events.go` — `cron_*` 事件常量（注：实际常量放 `pkg/event`，这里放 helper 构造函数）
- `internal/cron/store.go` — `Store` + `DBStore` 接口（仿 `internal/todo/store.go`，打破循环依赖）
- `internal/cron/scheduler.go` — `Scheduler`：robfig/cron 包装，启动加载 + 增量同步
- `internal/cron/executor.go` — `Executor`：单次触发编排，串行/skip，模板渲染，发事件，记 execution
- `internal/cron/action.go` — `ActionRunner`：4 种 action_type 执行
- `internal/cron/template.go` — 模板渲染（`text/template`，注入 Now/PrevTrigger/PrevStatus/PrevResult/Count/CronID/CronName）
- `internal/cron/service.go` — `Service`：CRUD + 状态操作 + 手动触发，CRUD 后通知 Scheduler
- `internal/cron/tools.go` — 4 个 Agent Tool（`cron/create` `cron/list` `cron/delete` `cron/trigger`）
- `internal/cron/store_test.go` / `scheduler_test.go` / `executor_test.go` / `action_test.go` / `template_test.go` / `tools_test.go` / `service_test.go`

后端修改：
- `pkg/db/migrate.go` 或 `pkg/db/cron.go`（新文件，`init()` 注册 v26）— 建 `crons` + `cron_executions` 表
- `pkg/db/cron.go` — `DBStore` 的 pkg/db 侧实现（InsertCron / UpdateCron / ... / InsertExecution / ListExecutions / CleanExecutions）
- `pkg/event/event.go` — 加 `cron_*` 常量
- `internal/config/config.go` — 加 `CronEnabled` / `CronAllowedTools` / `CronWebhookTimeoutSeconds` / `CronMaxResultChars`
- `cmd/server/main.go` — 抽 `startChatTask`；初始化 cron Service+Scheduler；注册 cron tools；注册 `/api/crons*` 路由
- `cmd/server/cron_api.go`（新文件）— REST handler
- `cmd/server/cron_api_test.go` — API 测试
- `go.mod` / `go.sum` — `go get github.com/robfig/cron/v3`

前端新增（仅 v2）：
- `web/v2/src/types/cron.ts` — 类型
- `web/v2/src/composables/useCrons.ts` — CRUD + 状态操作
- `web/v2/src/composables/useCronEvents.ts` — 订阅 `cron_*` 事件
- `web/v2/src/components/CronManager.vue` — ManageFlyout tab 内容（列表 + 操作）
- `web/v2/src/components/CronForm.vue` — 新增/编辑表单
- `web/v2/src/components/CronExecutions.vue` — 执行历史 + 清理
- `web/v2/src/components/CronDockPanel.vue` — 侧边只读 dock
- 对应 `.test.ts`

前端修改：
- `web/v2/src/types/events.ts` — EventType 加 `cron_*`
- `web/v2/src/components/ManageTabs.vue` — tabs 加 `cron`
- `web/v2/src/components/ManageContent.vue` — activeTab==='cron' 渲染 CronManager
- `web/v2/src/App.vue` — 加右侧 CronDockPanel（可折叠），跳转 ManageFlyout cron tab

文档：
- `CLAUDE.md` — Cron 系统章节
- `roadmaps/ROADMAP.md` — Phase 7 cron 条目

---

## 关键契约（所有任务遵守）

### 事件常量（`pkg/event/event.go`）
```
EventCronCreated / EventCronUpdated / EventCronDeleted
EventCronEnabled / EventCronDisabled / EventCronPaused / EventCronResumed
EventCronTriggered / EventCronExecutionStarted / EventCronExecutionCompleted
EventCronExecutionFailed / EventCronExecutionSkipped / EventCronMissed
EventCronNotification
```
值即字符串 `"cron_created"` 等。事件 `TaskID` 字段填 `cron_id`，`AgentID` 填触发 agent_id 或 `"cron"`。

### Cron 领域模型（`internal/cron/model.go`）
```go
type ScheduleType string // "cron" | "interval" | "once"
type ActionType string   // "start_task" | "script" | "webhook" | "notify_session"
type Status string       // "enabled" | "disabled" | "paused"
type ExecStatus string   // "running" | "completed" | "failed" | "skipped" | "missed"

type Cron struct {
    ID, Name, Description string
    ScheduleType ScheduleType
    CronExpr, Timezone, OnceAt string
    DisplayType string // preset|interval|cron|once
    ActionType ActionType
    ActionPayload map[string]any
    Status Status
    AllowConcurrent bool
    Source, Owner string
    LastTriggeredAt, NextTriggerAt *time.Time
    LastExecutionID string
    TriggerCount int
    CreatedAt, UpdatedAt time.Time
}

type Execution struct {
    ID, CronID string
    TriggeredAt time.Time
    Status ExecStatus
    Reason, RenderedInput, ResultSummary, TaskID, SessionID, Error string
    DurationMS int
    CreatedAt time.Time
}

type StartTaskPayload struct {
    AgentID, SessionID, Input, SystemPrompt string
    MaxSteps, TimeoutSeconds int
    Scope string; AllowedTools []string
    TokenBudget int; CostBudgetUSD float64; CaseID string
}
type ScriptPayload struct {
    ToolCalls []ScriptToolCall `json:"tool_calls"`
}
type ScriptToolCall struct {
    Tool string; Input map[string]any; Approval bool
}
type WebhookPayload struct {
    Method, URL string; Headers map[string]string; Body string; TimeoutSeconds int
}
type NotifySessionPayload struct {
    SessionID, Message, EventType string
}
```

### DBStore 接口（`internal/cron/store.go`）
```go
type DBStore interface {
    InsertCron(c Cron) error
    UpdateCron(c Cron) error
    DeleteCron(id string) error
    GetCron(id string) (Cron, error)
    ListCrons(filter ListFilter) ([]Cron, error)
    InsertExecution(e Execution) error
    UpdateExecution(e Execution) error
    GetExecution(id string) (Execution, error)
    ListExecutions(filter ExecListFilter) ([]Execution, error)
    CleanExecutions(filter CleanFilter) (int, error)
}
```

### Service 对外方法（`internal/cron/service.go`）
```go
func NewService(store DBStore, bus EventBus, sched *Scheduler, runner *ActionRunner) *Service
func (s *Service) Create(input CreateInput) (*Cron, error)
func (s *Service) Update(id string, input UpdateInput) (*Cron, error)
func (s *Service) Delete(id string) error
func (s *Service) Get(id string) (*Cron, error)
func (s *Service) List(filter ListFilter) ([]Cron, error)
func (s *Service) SetStatus(id string, status Status) (*Cron, error) // enable/disable/pause/resume 走这个
func (s *Service) Trigger(id string, overrideInput string) (*Execution, error) // 手动触发
func (s *Service) ListExecutions(filter ExecListFilter) ([]Execution, error)
func (s *Service) CleanExecutions(filter CleanFilter) (int, error)
```

### startChatTask（`cmd/server/main.go` 抽出）
```go
type StartTaskOpts struct {
    AgentID, Input, SystemPrompt, SessionID string
    MaxSteps, TimeoutSeconds int
    Scope string; AllowedTools []string
    TokenBudget int; CostBudgetUSD float64; CaseID string
}
func startChatTask(opts StartTaskOpts) (sessionID, taskID string, err error)
```
把 `handleTasksRoot` 的 `case "chat"` 核心逻辑搬进来；原 handler 构造 opts 后调用它。cron 的 start_task action 也调用它。

### Agent Tools（`internal/cron/tools.go`）
namespace=`cron`，用 `tool.NewBuiltinTool`。`cron/create` 入参：name, description, schedule_type, cron_expr, once_at, timezone, display_type, action_type, action_payload, allow_concurrent。`cron/list`：status?。`cron/delete`：id。`cron/trigger`：id, override_input?。

### 前端事件类型（`web/v2/src/types/events.ts`）
EventType 联合追加：`'cron_created' | 'cron_updated' | 'cron_deleted' | 'cron_enabled' | 'cron_disabled' | 'cron_paused' | 'cron_resumed' | 'cron_triggered' | 'cron_execution_started' | 'cron_execution_completed' | 'cron_execution_failed' | 'cron_execution_skipped' | 'cron_missed' | 'cron_notification'`

---

## 任务列表

### Task 1: 加 robfig/cron 依赖 + pkg/event 事件常量 + config 字段

**Files:** `go.mod` `go.sum`(自动) `pkg/event/event.go` `internal/config/config.go`

- [ ] 运行 `cd D:/Claude-Code-MultiAgent && go get github.com/robfig/cron/v3@latest && go mod tidy`
- [ ] 在 `pkg/event/event.go` 的 const 块追加 14 个 `EventCron*` 常量（值见契约）
- [ ] 在 `internal/config/config.go` 的 `Config` 结构体追加字段：
  ```go
  CronEnabled bool     // CRON_ENABLED 默认 true
  CronAllowedTools []string // CRON_ALLOWED_TOOLS 默认 run_shell,read_file,write_file,fetch_url
  CronWebhookTimeoutSeconds int // 默认 10
  CronMaxResultChars int // 默认 2000
  ```
  在 `Load()` 中用 `parseEnvBoolDefault` / `parseEnvStringSliceDefault` / `parseEnvIntDefault` 读取（沿用文件里已有的 helper 命名；若 helper 名不同则用文件里实际的）。
- [ ] 测试：`pkg/event/event_test.go` 验证常量值非空且唯一（`"cron_"` 前缀）。`internal/config/config_test.go`（若已有则追加 case）验证默认值。
- [ ] `go build ./...` 通过，`go test ./pkg/event/... ./internal/config/...` 通过。
- [ ] Commit: `Phase 7-cron: robfig/cron 依赖 + cron 事件常量 + config 字段`

### Task 2: DB migration v26 + pkg/db 实现 DBStore

**Files:** `pkg/db/cron.go`(新) `pkg/db/migrate.go`(不改，用 init 注册)

- [ ] 新建 `pkg/db/cron.go`，`init()` 追加 migration v26（建 `crons` + `cron_executions` 两表 + 索引，schema 见设计文档 4.1/4.2；注意 `splitSQL` 已存在）。
- [ ] 实现 `pkg/db` 侧 CRUD：`InsertCron/UpdateCron/DeleteCron/GetCron/ListCrons/InsertExecution/UpdateExecution/GetExecution/ListExecutions/CleanExecutions`。`ActionPayload` 存 JSON（`json.Marshal`）。时间字段用 `sql.NullTime` 或 INTEGER unix（与 skill.go 风格一致——skill.go 用 INTEGER unix，这里也用 INTEGER unix 秒，避免 DATETIME 解析问题）。
- [ ] TDD：`pkg/db/cron_test.go`——用临时内存 sqlite（仿 `pkg/db/skill_test.go` 的 setup）跑 CRUD + 过滤 + 清理。先写失败测试再实现。
- [ ] `go test ./pkg/db/...` 通过。
- [ ] Commit: `Phase 7-cron: crons/cron_executions 表 migration + DBStore 实现`

### Task 3: cron 领域模型 + Store 接口

**Files:** `internal/cron/model.go` `internal/cron/store.go`

- [ ] `model.go`：按契约定义全部类型 + `ListFilter`/`ExecListFilter`/`CleanFilter`/`CreateInput`/`UpdateInput` 结构 + 常量 + `ActionType` 的 `IsValid()` / `ScheduleType.IsValid()`。
- [ ] `store.go`：定义 `DBStore` interface + `Store` 薄封装 + `EventBus` interface（仿 todo）。`Store` 方法转调 DBStore。
- [ ] TDD：`internal/cron/model_test.go` 验证 IsValid。
- [ ] `go build ./internal/cron/...` 通过，`go test ./internal/cron/...` 通过。
- [ ] Commit: `Phase 7-cron: cron 领域模型与 Store 接口`

### Task 4: 模板渲染

**Files:** `internal/cron/template.go` `internal/cron/template_test.go`

- [ ] 实现 `RenderTemplate(tmpl string, ctx TemplateContext) string`：用 `text/template`，注入 `.Now .PrevTrigger .PrevStatus .PrevResult .Count .CronID .CronName`。渲染失败返回原始 tmpl（不 error）。提供 `RenderMap(payload map[string]any, ctx) map[string]any`：递归对 map/slice 里的 string 值渲染。
- [ ] TDD：测试占位符替换、缺失变量保留、非法模板保留原文、嵌套 map 渲染。
- [ ] Commit: `Phase 7-cron: 模板渲染(Now/PrevTrigger/PrevStatus/PrevResult/Count)`

### Task 5: ActionRunner（4 种 action）

**Files:** `internal/cron/action.go` `internal/cron/action_test.go`

- [ ] 定义 `ActionRunner`：
  ```go
  type TaskStarter func(opts StartTaskParams) (taskID, sessionID string, err error)
  // StartTaskParams 与 StartTaskOpts 字段对齐，但 Input 已渲染好
  type ActionRunner struct {
    tools *tool.Registry
    allowedTools map[string]bool
    webhookTimeout time.Duration
    maxResultChars int
    bus runtime.EventBus  // 用于 notify_session 广播 + 写 session message
    startTask TaskStarter
  }
  func (r *ActionRunner) Run(ctx context.Context, c Cron, rendered map[string]any) (Result, error)
  type Result struct { TaskID, SessionID, Summary string }
  ```
- [ ] `start_task`：解析 `StartTaskPayload`，调 `startTask`，Result.TaskID/SessionID 回填，Summary="task started: <taskID>"。
- [ ] `script`：遍历 `ToolCalls`，每个 tool 名必须在 `allowedTools` 白名单，否则 error；调 `tools.Execute`，approval 字段首期忽略（只记录）；汇总结果为 Summary（截断 maxResultChars）。
- [ ] `webhook`：模板已渲染好 method/url/headers/body；`http.Client` 带 timeout；非 2xx → error（含 response status）；Summary=resp status。
- [ ] `notify_session`：校验 session_id 非空；广播 `EventCronNotification` 事件（data: session_id, message, cron_id）；同时写一条 session message（通过注入的 `SessionMessageWriter` 接口，避免直接依赖 db）。Summary="notified session <id>"。
- [ ] `SessionMessageWriter` 接口：`InsertSystemMessage(sessionID, content string) error`，由 cmd/server 注入 db 实现。
- [ ] TDD：用 mock TaskStarter + mock Registry + mock bus + httptest.Server 测 4 种 action + 白名单拒绝 + webhook 非 2xx + session 失效。先写失败测试。
- [ ] Commit: `Phase 7-cron: ActionRunner(start_task/script/webhook/notify_session)`

### Task 6: Scheduler

**Files:** `internal/cron/scheduler.go` `internal/cron/scheduler_test.go`

- [ ] `Scheduler` 包装 `robfig/cron/v3`：
  ```go
  type Scheduler struct {
    c *cron.Cron
    store DBStore
    exec *Executor
    mu sync.Mutex
    entries map[string]cron.EntryID // cron_id -> entry
  }
  func NewScheduler(store DBStore, exec *Executor) *Scheduler
  func (s *Scheduler) Start(ctx context.Context) error // 从 DB 加载所有 enabled，逐条 Add；invalid expr 记 missed
  func (s *Scheduler) Add(c Cron) error
  func (s *Scheduler) Remove(cronID string)
  func (s *Scheduler) Update(c Cron) error // remove + add
  func (s *Scheduler) Stop()
  ```
- [ ] 用 `cron.New(cron.WithSeconds())` 启用秒级 6 域。回调里调 `exec.Execute(ctx, cronID)`。
- [ ] `once` 类型：robfig/cron 不直接支持 once；用 `@every` 不合适。实现：once 类型不进 robfig，由 Scheduler 单独维护一个 `time.AfterFunc` map，到点触发后自动 Remove。cron/interval 走 robfig（interval 存成 `@every Ns` 或转 cron）。
  - 实际：`schedule_type=='once'` 时用 `time.AfterFunc(until, ...)`；`'cron'` 用 robfig AddFunc；`'interval'` 把 `cron_expr` 形如 `30s`/`5m` 转成 `@every 30s`。
- [ ] TDD：mock Executor，测 Add/Remove/Update/once 触发（用短 once_at 如 1s 后）/invalid expr 记 missed/启动加载。用 `cron.WithChain(cron.SkipIfStillRunning(...))` 实现串行？——不，串行 skip 逻辑放 Executor 里（需要记录状态），Scheduler 只管到点调 exec。
- [ ] Commit: `Phase 7-cron: Scheduler(robfig/cron 秒级 + once AfterFunc + 启动加载)`

### Task 7: Executor

**Files:** `internal/cron/executor.go` `internal/cron/executor_test.go`

- [ ] `Executor`：
  ```go
  type Executor struct {
    store DBStore
    runner *ActionRunner
    bus runtime.EventBus
    maxResultChars int
    mu sync.Mutex
    running map[string]bool // cron_id -> 是否在跑（串行 skip 用）
  }
  func (e *Executor) Execute(ctx context.Context, cronID string) // 被 scheduler 调
  func (e *Executor) ExecuteOnce(ctx context.Context, cronID, overrideInput string) (*Execution, error) // 手动触发用
  ```
- [ ] `Execute` 流程：
  1. 读 cron；若 status!=enabled → skip（manual trigger 除外）。
  2. 串行判定：若 `!AllowConcurrent` 且 `running[cronID]==true` → 记 `skipped` execution + 发 `cron_execution_skipped`，return。
  3. 取上次 execution（PrevTrigger/PrevStatus/PrevResult）。
  4. 构造 TemplateContext，渲染 action_payload。
  5. 建 Execution 记录（status=running）存 DB + 发 `cron_triggered` + `cron_execution_started`。
  6. set running=true；调 `runner.Run`；set running=false。
  7. 成功 → UpdateExecution(status=completed, summary, task_id, duration) + 发 `cron_execution_completed`；失败 → status=failed + error + 发 `cron_execution_failed`。
  8. Update cron 的 last_triggered_at/trigger_count/last_execution_id/next_trigger_at。
- [ ] `ExecuteOnce`：手动触发，忽略串行 skip 与 status 检查（除非 disabled），支持 overrideInput 覆盖 start_task input。
- [ ] TDD：mock store + mock runner + mock bus，测：正常完成、失败、串行 skip、disabled skip、模板渲染传入 runner、cron meta 更新。
- [ ] Commit: `Phase 7-cron: Executor(触发编排+串行skip+模板+事件+execution记录)`

### Task 8: Service

**Files:** `internal/cron/service.go` `internal/cron/service_test.go`

- [ ] 按契约实现 `Service`：Create/Update/Delete/Get/List/SetStatus/Trigger/ListExecutions/CleanExecutions。
- [ ] Create：生成 ID（`cron_`+uuid）、校验 schedule_type/action_type、校验 cron_expr 可被 robfig 解析（用 `cron.NewParser(cron.Second... )` 试 parse）、status 默认 enabled、存 DB、调 `Scheduler.Add`、发 `cron_created`。若 scheduler 为 nil（CRON_ENABLED=false）则跳过 Add。
- [ ] SetStatus：enable→enabled+Scheduler.Add；disable→disabled+Scheduler.Remove；pause→paused+Remove；resume→enabled+Add。发对应事件。
- [ ] Delete：Scheduler.Remove + DB.Delete + 发 `cron_deleted`。
- [ ] Trigger：调 `Executor.ExecuteOnce`。
- [ ] 校验：action_type 必填、start_task 的 agent_id 必填、notify_session 的 session_id 必填、webhook 的 url 必填。
- [ ] TDD：mock DBStore + mock Scheduler + mock Executor + mock bus，测 CRUD + 状态机 + 校验失败 + 事件发出。
- [ ] Commit: `Phase 7-cron: Service(CRUD+状态机+校验+事件)`

### Task 9: Agent Tools

**Files:** `internal/cron/tools.go` `internal/cron/tools_test.go`

- [ ] `RegisterCronTools(reg *tool.Registry, svc *Service)`：注册 4 个 `tool.NewBuiltinTool`（namespace `cron`）。
- [ ] `cron/create`：参数 schema 见契约；调 `svc.Create`，返回新 cron JSON。
- [ ] `cron/list`：调 `svc.List`，返回数组。
- [ ] `cron/delete`：调 `svc.Delete`。
- [ ] `cron/trigger`：调 `svc.Trigger`，返回 execution。
- [ ] TDD：mock Service，测 4 个 tool 的输入解析 + 调用 + 错误透传。
- [ ] Commit: `Phase 7-cron: Agent Tools(cron/create/list/delete/trigger)`

### Task 10: 抽 startChatTask + main.go 接入 cron

**Files:** `cmd/server/main.go` `cmd/server/cron_api.go`(新)

- [ ] 把 `handleTasksRoot` 的 `case "chat"` 核心启动逻辑抽成 `startChatTask(opts StartTaskOpts) (sessionID, taskID string, err error)`，原 handler 构造 opts 调用它。保持行为不变（先确保 `go build` + 现有测试通过）。
- [ ] main.go 初始化段（仿 todo/skill 段）：
  - 若 `cfg.CronEnabled && db.DB != nil`：构造 `cronStore`（pkg/db 实现）、`actionRunner`（注入 toolRegistry、cfg 白名单、`startChatTask` 作为 TaskStarter、db 作为 SessionMessageWriter）、`executor`、`scheduler`、`service`。
  - `scheduler.Start(ctx)`。
  - `cron.RegisterCronTools(toolRegistry, service)`。
  - 把 service 提为包级 `globalCronService` 供 handler 用。
- [ ] `cron_api.go`：注册 `/api/crons*` 路由（在 main.go 用 `http.HandleFunc` 或在 cron_api.go 暴露 `RegisterCronAPI(mux, svc)`——项目用 `http.HandleFunc` 全局，所以直接在 cron_api.go 里 `http.HandleFunc`，由 main 调 `cron.RegisterRoutes()`）。实现设计文档第 7 节全部端点。JSON 编解码，错误 400/404/500。
- [ ] `startChatTask` 的 TaskStarter 适配：cron 传 `StartTaskParams` → 转 `StartTaskOpts`，Input 前加 `[cron:<id>:<name>] ` 前缀。
- [ ] 手动测试：`go build ./cmd/server && go test ./cmd/server/...` 通过。
- [ ] Commit: `Phase 7-cron: startChatTask 重构 + main.go 接入 + REST API`

### Task 11: cmd/server cron API 测试 + smoke

**Files:** `cmd/server/cron_api_test.go`

- [ ] 用 `httptest` + 真实内存 sqlite + mock provider（沿用项目测试 helper）测：创建 cron → 列表 → 启用/禁用 → 手动 trigger start_task（mock provider）→ execution completed → 执行历史查询 → 清理。
- [ ] 若项目有 smoke 脚本，加一个 cron smoke（mock 模式）。
- [ ] `go test ./cmd/server/... -run Cron` 通过。
- [ ] Commit: `Phase 7-cron: cron REST API 集成测试`

### Task 12: 前端 cron 类型 + composables

**Files:** `web/v2/src/types/cron.ts` `web/v2/src/composables/useCrons.ts` `web/v2/src/composables/useCronEvents.ts` `web/v2/src/types/events.ts`(改)

- [ ] `types/cron.ts`：镜像后端模型（Cron / Execution / CreateInput / UpdateInput / 各种 Payload）。
- [ ] `types/events.ts`：EventType 追加 14 个 `cron_*`。
- [ ] `useCrons.ts`：模块级 reactive state + fetch 封装（list/create/update/delete/enable/disable/pause/resume/trigger/listExecutions/cleanExecutions）。仿 `useSkills.ts` / `useCaseStore.ts` 风格。
- [ ] `useCronEvents.ts`：`onEvent` 订阅 `cron_*`，维护最近 N=50 条执行流 reactive 列表 + 暴露 `cronEvents` ref。
- [ ] TDD：`useCrons.test.ts`（mock fetch）、`useCronEvents.test.ts`（mock WS 事件）。用 vitest。
- [ ] `cd web/v2 && npm run test -- --run` 通过（至少新测试通过）。
- [ ] Commit: `Phase 7-cron: 前端 cron 类型 + useCrons/useCronEvents`

### Task 13: 前端 CronManager + CronForm + CronExecutions

**Files:** `web/v2/src/components/CronManager.vue` `CronForm.vue` `CronExecutions.vue` + `.test.ts`

- [ ] `CronManager.vue`：列表表格（name/schedule/action_type/status/next/last/操作）+ 顶部"新建"按钮 + 行操作按钮（enable/disable/pause/resume/trigger/history/delete）。
- [ ] `CronForm.vue`：schedule_type 切换（preset 标签下拉 / interval 输入 / cron 6 域输入 / once datetime），切换为自由 cron 后 display_type=cron 不回退 preset；action_type 切换 4 种 payload 表单。
- [ ] `CronExecutions.vue`：按 cron/状态/时间过滤 + 手动清理按钮 + 分页。
- [ ] TDD：组件测试渲染 + 操作调用 composable。
- [ ] Commit: `Phase 7-cron: 前端 CronManager/CronForm/CronExecutions`

### Task 14: 前端 ManageFlyout cron tab + CronDockPanel + App.vue 接入

**Files:** `web/v2/src/components/ManageTabs.vue`(改) `ManageContent.vue`(改) `CronDockPanel.vue`(新) `App.vue`(改) + 测试

- [ ] `ManageTabs.vue` tabs 加 `{ id: 'cron', label: 'Cron' }`。
- [ ] `ManageContent.vue`：import CronManager，activeTab==='cron' 渲染它。
- [ ] `CronDockPanel.vue`：只读，显示与当前 active session 相关的 cron（`action_payload.session_id == activeSessionId` 或无 session 绑定）+ 实时触发流（useCronEvents）+ 每条"跳转 ManageFlyout cron tab 定位该 cron"按钮（emit 事件给 App 打开 flyout 并设 initialTab='cron'）。
- [ ] `App.vue`：加右侧可折叠 CronDockPanel（新增 `rightCronOpen` ref + toggle），桌面/平板布局都加；跳转按钮 → 打开 ManageFlyout 并 initialTab='cron'。
- [ ] TDD：`ManageTabs.test.ts`/`ManageContent.test.ts` 追加 cron tab case；`CronDockPanel.test.ts`。
- [ ] `npm run build` 通过。
- [ ] Commit: `Phase 7-cron: 前端 ManageFlyout cron tab + 侧边 CronDockPanel`

### Task 15: 文档 + real_llm smoke + 最终验证

**Files:** `CLAUDE.md` `roadmaps/ROADMAP.md` `scripts/cron-smoke.sh`(可选)

- [ ] `CLAUDE.md`：加 "## Cron 系统" 章节（核心概念/REST API/Agent Tools/前端触发/事件），风格对齐 Skill 章节。
- [ ] `roadmaps/ROADMAP.md`：Phase 7 加 cron 条目。
- [ ] real_llm smoke：手动跑一次 server，创建 cron（`*/30 * * * * *` 或 30s interval）触发 start_task，确认事件流 + execution 记录 + 前端可见。记录结果到 commit message。
- [ ] `go test ./...` 全绿；`cd web/v2 && npm run test -- --run && npm run build` 全绿。
- [ ] Commit: `Phase 7-cron: 文档 + real_llm smoke 验证`
- [ ] 最终：`git log --oneline phase-7-cron ^main` 确认 15 个提交。

---

## 自检

- 4 种 action_type 全实现：Task 5 ✅
- 三入口（REST/Tool/Event）：Task 9/10/Event 常量 Task 1 ✅
- 前端两处（ManageTab + Dock）：Task 13/14 ✅
- 串行 skip / 离线 missed / 模板：Task 4/6/7 ✅
- 测试全覆盖（unit+mock+real_llm+前端）：各任务 ✅
