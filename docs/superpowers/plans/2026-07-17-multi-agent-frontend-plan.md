# 多 Agent 实现现状与前端表现规划报告

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 基于当前 `D:\Claude-Code-MultiAgent` 代码库，梳理多 Agent 并发的后端实现现状，明确前端在可观测性、可控性、可操作性方面的缺口，并给出下一阶段的实现规划。

**Architecture:** 后端 Orchestrator 已支持 goroutine 并行/顺序两种策略和 AgentBus 消息传递；前端通过 WebSocket EventBus 按 `{task_id, agent_id, step_index}` 路由事件，用 `taskCache` + `AgentTree` 渲染多棵树。本规划将补齐多 Agent 特有的启动入口、运行期控制、协作可视化和策略配置界面。

**Tech Stack:** Go 1.25 + gorilla/websocket, Vue 3 + Vite + TypeScript, SQLite, Prometheus metrics.

---

## 0. 现状诊断

### 0.1 后端：多 Agent 已经可以跑

1. **多 Agent 入口**  
   - `cmd/server/main.go:920` 有 `POST /api/multi-agent`，`cmd/server/main.go:494` 有 `action: "multi-agent"`。  
   - LLM/tool registry/AgentBus/CheckpointManager/Router 都在 `cmd/server/main.go:400` 一次性注入 `orchestrator.New(...)`。  
2. **编排器**  
   - `internal/orchestrator/orchestrator.go:167` 的 `RunBlocking` 支持 `"parallel"` / `"sequential"`。  
   - `"pipeline"` 策略在注释中有但代码未实现，目前 sequential 承担链式场景。  
   - 每个 agent 一个 Engine（`orchestrator.go:283`），共享 ws.Hub，子任务 ID 固定为 `{rootTaskID}_{agentID}`。  
3. **AgentBus 已接入 ReAct Loop**  
   - `internal/runtime/engine.go:602` 启动 listener goroutine，把其它 agent 消息以 `[Agent xxx]: content` 形式注入 conversation。  
   - `orchestrator.go:206` 的 `OutputTo` 在 parallel 模式下把结果转发给目标 agent；`orchestrator.go:189` 在 sequential 模式下把结果传给下一个 agent。  
4. **策略/分解**  
   - `TaskDecomposer.Decompose` 是硬编码规则（`multi_agent` / `code_gen` / default），无法自定义、无法在线编辑。  
   - `AgentSpec` 支持 `ParentAgentID`、`OutputTo`、`AllowedTools`、`Contract`、`Model`，但前端没有配置界面。  

### 0.2 前端：多 Agent 能看但不方便用

1. **能看**  
   - `useTaskStore.ts` 的 `taskCache` 按 `task_id` 聚合，按 `agent_id` 分 `AgentState`，所以多 agent 自然渲染为多棵树。  
   - `TurnItem.vue:199` 用 `v-for` 遍历 `Object.values(task.agents)`。  
   - `AgentTree.vue` 有颜色区分、token 提示、durations。  
2. **不方便用**  
   - **入口隐藏**：UI 上发送普通 chat 不会触发 multi-agent；`startMultiAgentTask` 只在 `useTaskStore.ts:695` 封装 `/api/multi-agent`，但 `App.vue` 的 `handleSend` 不会调用它。  
   - **无法配置**：创建/编辑 agent 已有 `AgentConfig.vue`，但 multi-agent 模式、OutputTo、策略、工具白名单都没有 UI 入口。  
   - **可控性差**：取消只按 `task_id` 取消 root task，子 agent 的 `cancelRegistry` 以 `rootTaskID` 为 key（`cmd/server/main.go:114`），子任务无法单独取消/暂停/恢复。  
   - **协作不可视**：AgentBus 发送的 `agent_message_sent` / `agent_message_received` 事件在前端被忽略（`useTaskStore.ts` 的 switch 没有处理 `system_info type=agent_message_*`）。  - **历史回放缺关系**：`loadTask` 只加载 root task 的 steps 和一级 child_tasks，不会递归加载孙任务/跨 task 的 agent 消息。  

### 0.3 关键代码位置速查

| 能力 | 文件 | 行号 |
|------|------|------|
| 多 Agent API 入口 | `cmd/server/main.go` | 920-1060 |
| Orchestrator 并行/顺序调度 | `internal/orchestrator/orchestrator.go` | 167-260 |
| AgentBus + Engine 注入 | `internal/runtime/engine.go` | 602-645, 1952-1980 |
| WebSocket 控制消息 | `internal/ws/hub.go:33`, `cmd/server/main.go:88` | 33, 88-130 |
| 前端事件路由 | `web/src/composables/useTaskStore.ts` | 161-495 |
| 多树渲染 | `web/src/components/TurnItem.vue` | 199 |
| Agent 配置页 | `web/src/components/AgentConfig.vue` | 整个文件 |
| Session/Turn 时间线 | `web/src/components/TurnList.vue`, `TurnItem.vue` | 整个文件 |

---

## 1. 改进目标

让多 Agent 协作成为**一等公民**：

1. **易用**：用户发送任务时可以选择走 multi-agent 模式，并且能选择/预览分解策略。  
2. **可配**：在 AgentConfig 之外提供多 Agent 工作流配置（agent 角色、执行顺序、消息传递、工具白名单）。  
3. **可控**：允许按 root-task 取消全部子任务，也允许单独取消/暂停/恢复某个子 agent。  
4. **可观测**：AgentBus 消息、策略变更、模型路由决策要在前端有独立面板显示。  
5. **可回放**：历史 multi-agent 任务能完整还原 task → child task → agent 消息链路。  

---

## 2. 规划概览

将工作拆为 6 个独立任务，每个任务可单独验证、提交。任务按依赖顺序排列。

| 任务 | 主题 | 关键文件 | 验证方式 |
|------|------|---------|---------|
| 1 | 后端：子任务独立取消/暂停/恢复 + cancelRegistry 按子任务索引 | `cmd/server/main.go`, `internal/ws/hub.go`, `internal/runtime/engine.go` | curl 取消子任务，前端收到子 task_failed |
| 2 | 后端：AgentBus 消息持久化与 API 查询 | `internal/orchestrator/agentbus.go` 新增, `pkg/db/database.go` schema 新增, `cmd/server/main.go` API | 运行 multi-agent 后 GET /api/tasks/{id}/agent-messages 返回消息列表 |
| 3 | 后端：LLM 动态任务分解 + pipeline 策略 | `internal/orchestrator/decomposer.go` 新增, `internal/orchestrator/orchestrator.go` | POST /api/multi-agent 对复杂输入返回 3+ agent spec |
| 4 | 前端：Multi-Agent 启动入口与模式切换 | `web/src/components/TaskInput.vue`, `web/src/App.vue` | UI 上出现 "Multi-Agent" 开关/按钮，普通 chat 可切换多 agent |
| 5 | 前端：AgentBus 协作可视化面板 | `web/src/components/AgentBusTimeline.vue` 新增, `useTaskStore.ts` | 多 agent 运行时显示 agent 间消息流 |
| 6 | 前端：子任务独立控制与策略配置 | `web/src/components/MultiAgentWorkflowEditor.vue` 新增 | 可勾选/拖拽 agent、设置策略、OutputTo、工具白名单 |

---

## 3. 任务详细规划

### Task 1: 子任务独立取消/暂停/恢复

**Files:**
- Modify: `cmd/server/main.go:38-119`, `cmd/server/main.go:546-552`（multi-agent goroutine 内）
- Modify: `internal/runtime/engine.go:659-676`（context 检查位置可不变，但需要更多控制点）
- Modify: `internal/ws/hub.go:33-38`（`ClientControlMsg` 增加字段）

**现状问题：**
- `cmd/server/main.go:41` 的 `cancelRegistry` 以 `taskID` 为 key。multi-agent 时只注册了 root taskID 的 cancel func，子任务即使自己在 runAgent 里注册了也可能互相覆盖。  
- `ws.ClientControlMsg` 目前没有 `agent_id` / `sub_task_id` 字段，前端无法细化控制。  
- pause/resume 在 `cmd/server/main.go:120` 只是返回 501。  

**实现思路：**
- 让 `cancelRegistry` 的 key 变成 `task_id` 或 `task_id/agent_id` 两种形式。  
- Engine 内部增加 pause channel / resume channel，Pause 时不取消 context，而是阻塞 step 循环；Resume 时继续。  - WebSocket control handler 根据 msg 中 `agent_id` 是否为空区分 root/child 控制目标。  

**拆分步骤：**

- [ ] **Step 1.1: 写 Engine 的 Pause/Resume 单元测试**

在 `internal/runtime/engine_test.go` 新增（如不存在则新建）：

```go
func TestEnginePauseResume(t *testing.T) {
    // 使用 mock provider，让 think 慢一点
    cfg := EngineConfig{AgentID: "test", MaxSteps: 5}
    tools := tool.NewRegistry()
    bus := &mockBus{}
    engine := NewEngine(cfg, tools, bus, "task_pause_test")

    ctx := context.Background()
    // 启动 Run in goroutine
    done := make(chan struct{})
    var err error
    go func() {
        _, _, err = engine.Run(ctx, "count to 3")
        close(done)
    }()

    time.Sleep(50 * time.Millisecond)
    engine.Pause()
    time.Sleep(50 * time.Millisecond)
    engine.Resume()
    <-done

    if err != nil {
        t.Fatalf("engine run failed: %v", err)
    }
}
```

> 该测试先失败，因为 `Engine.Pause/Resume` 不存在。

- [ ] **Step 1.2: 编译执行看失败**

Run: `cd D:/Claude-Code-MultiAgent && go test ./internal/runtime -run TestEnginePauseResume -v`

Expected: FAIL，undefined `engine.Pause`。

- [ ] **Step 1.3: 在 Engine 增加 Pause/Resume 机制**

`internal/runtime/engine.go` 在 `Engine` 结构体新增：

```go
pauseCh  chan struct{}
resumeCh chan struct{}
paused   atomic.Bool
```

初始化：`NewEngine` 中 `pauseCh: make(chan struct{}), resumeCh: make(chan struct{})`。  
新增方法：

```go
func (e *Engine) Pause() {
    if e.paused.CompareAndSwap(false, true) {
        e.bus.SendEvent(event.NewEvent("agent_status", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
            "status": "paused",
        }))
    }
}

func (e *Engine) Resume() {
    if e.paused.CompareAndSwap(true, false) {
        close(e.resumeCh)
        e.resumeCh = make(chan struct{})
        e.bus.SendEvent(event.NewEvent("agent_status", e.taskID, e.cfg.AgentID, e.stepIdx, map[string]any{
            "status": "running",
        }))
    }
}
```

在 `Run` 的每次循环开头（`for e.stepIdx < e.cfg.MaxSteps {` 之后）插入：

```go
if e.paused.Load() {
    select {
    case <-e.resumeCh:
    case <-ctx.Done():
        // ... 走取消分支
    }
}
```

- [ ] **Step 1.4: 调整 cancelRegistry 支持子任务 key**

`cmd/server/main.go`：

```go
// cancelRegistry key 规则：root task 用 taskID；子 agent 用 taskID + "/" + agentID
var cancelRegistry sync.Map // string -> context.CancelFunc
func storeCancel(taskID, agentID string, cancel context.CancelFunc) {
    if agentID == "" || agentID == "orchestrator" {
        cancelRegistry.Store(taskID, cancel)
    } else {
        cancelRegistry.Store(taskID+"/"+agentID, cancel)
        // root task 也保留一份，用于统一取消
        cancelRegistry.Store(taskID, cancel)
    }
}
func loadCancel(taskID, agentID string) (context.CancelFunc, bool) {
    key := taskID
    if agentID != "" && agentID != "orchestrator" {
        key = taskID + "/" + agentID
    }
    v, ok := cancelRegistry.Load(key)
    if ok {
        return v.(context.CancelFunc), true
    }
    // fallback 到 root
    v, ok = cancelRegistry.Load(taskID)
    if ok {
        return v.(context.CancelFunc), true
    }
    return nil, false
}
```

在 `runAgentLoopWithTurn` 里把 `cancelRegistry.Store(taskID, cancel)` 替换为 `storeCancel(taskID, agentID, cancel)`，删除也改为 `cancelRegistry.Delete(taskID)` 和 `cancelRegistry.Delete(taskID + "/" + agentID)`。  
在 `orchestrator.runAgent` 中同样对子 task 调用 store 逻辑。

- [ ] **Step 1.5: WebSocket ControlHandler 细化**

`internal/ws/hub.go:33` 的 `ClientControlMsg` 增加 `AgentID string`：

```go
type ClientControlMsg struct {
    Action     string `json:"action"`
    TaskID     string `json:"task_id"`
    AgentID    string `json:"agent_id"`
    ApprovalID string `json:"approval_id"`
}
```

`cmd/server/main.go` control handler：

```go
case "cancel":
    if msg.TaskID == "" { ... }
    if cancelFn, ok := loadCancel(msg.TaskID, msg.AgentID); ok {
        cancelFn()
    } else {
        // unknown task
    }
case "pause":
    if msg.TaskID == "" { break }
    // 通过 taskCache 不知道的子任务：让 engine 自己监听？
    // Phase 7: 这里先只广播 pause 事件，由 engine 内部处理
```

暂停/恢复需要在 Engine 暴露外部接口。可以在每个 task goroutine 里把 engine 实例存到一个新注册表 `engineRegistry`，key 与 cancelRegistry 相同，control handler 找到 engine 调用 `Pause/Resume`。

- [ ] **Step 1.6: 前端事件类型补全**

`web/src/types/events.ts` 在 `EventType` 里增加 `'agent_status' value 'paused'` 的说明注释即可，当前已经支持 `agent_status`。  
`useTaskStore.ts:421` 的 `agent_status` case 增加 `if (evt.data.status === 'paused') { agent.status = 'paused' } else { agent.status = 'running' }`。

- [ ] **Step 1.7: 验证**

Run: `curl -X POST http://localhost:8080/api/multi-agent -H 'Content-Type: application/json' -d '{"input":"research and write a report about Go","case_type":"multi_agent"}'`  
得到 task_id 后，发送 WS 控制消息：`{"action":"cancel","task_id":"task_xxx","agent_id":"agent_researcher"}`。  
Expected: 只取消 researcher，writer 继续执行或失败（取决于 sequential 是否卡住）。

- [ ] **Step 1.8: 提交**

```bash
git add internal/runtime/engine.go internal/ws/hub.go cmd/server/main.go web/src/types/events.ts web/src/composables/useTaskStore.ts
# 如有新增文件：internal/runtime/engine_test.go 或 internal/runtime/*_test.go
git commit -m "Phase 7-A: per-agent pause/resume/cancel control"
```

---

### Task 2: AgentBus 消息持久化与查询

**Files:**
- Create: `internal/orchestrator/agentbus.go`（把 message 发送时的持久化/历史查询拆出来）
- Modify: `pkg/db/database.go`（新增 agent_messages 表）
- Modify: `pkg/db/persistence.go`（查看/增加保存方法位置）
- Modify: `cmd/server/main.go`（新增 REST endpoint）

**现状：**  
`AgentBus` 消息只在内存队列里，任务结束后无法回放 agent 间协作；前端也无法显示。

**拆分步骤：**

- [ ] **Step 2.1: 新增 DB migration：agent_messages 表**

`pkg/db/database.go` 新增 migration：

```sql
CREATE TABLE IF NOT EXISTS agent_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,
    from_agent_id TEXT NOT NULL,
    to_agent_id TEXT NOT NULL,
    msg_type TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_agent_messages_task_id ON agent_messages(task_id);
```

- [ ] **Step 2.2: 新增 Persistence 接口方法**

在 `internal/runtime/persistence.go`（或 `pkg/db/persistence.go`）新增：

```go
type AgentBusMessage struct {
    TaskID      string
    FromAgentID string
    ToAgentID   string
    Type        string
    Content     string
    Metadata    map[string]string
}

type Persistence interface {
    // ... 现有方法
    SaveAgentMessage(msg AgentBusMessage) error
    LoadAgentMessages(taskID string) ([]AgentBusMessage, error)
}
```

- [ ] **Step 2.3: 在 AgentBus 中持久化消息**

创建 `internal/orchestrator/agentbus.go`：

```go
package orchestrator

import (
    "sync"
    "time"

    "github.com/anmingwei/multi-agent-platform/internal/runtime"
)

type PersistentAgentBus struct {
    inner  *AgentBus
    store  AgentBusStore
}

type AgentBusStore interface {
    SaveAgentMessage(msg runtime.AgentBusMessage) error
}

func NewPersistentAgentBus(inner *AgentBus, store AgentBusStore) *PersistentAgentBus {
    b := &PersistentAgentBus{inner: inner, store: store}
    // wrap SendMessage to persist
    return b
}
```

或者更直接：在 `AgentBus.SendMessage` 里通过 callback 持久化。由于 `AgentBus` 是简单 struct，可以在 `orchestrator.New(...)` 时注入一个 writer。

- [ ] **Step 2.4: 新增 REST API**

`cmd/server/main.go`：

```go
http.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
    path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
    if strings.HasSuffix(path, "/agent-messages") {
        taskID := strings.TrimSuffix(path, "/agent-messages")
        handleGetAgentMessages(w, r, taskID)
        return
    }
    // ... existing handlers
})
```

- [ ] **Step 2.5: 验证**

Run: `go test ./pkg/db` 确保 schema 升级不破坏旧数据。  
Run multi-agent 任务后： `curl http://localhost:8080/api/tasks/task_xxx/agent-messages` 返回 JSON 列表，非空。

- [ ] **Step 2.6: 提交**

```bash
git add pkg/db/database.go internal/runtime/persistence.go internal/orchestrator/agentbus.go cmd/server/main.go
git commit -m "Phase 7-B: persist and query agent-bus messages"
```

---

### Task 3: LLM 动态任务分解 + Pipeline 策略

**Files:**
- Create: `internal/orchestrator/decomposer.go`
- Modify: `internal/orchestrator/orchestrator.go:178-222`

**现状：**  
`TaskDecomposer` 是硬编码的 `switch case`，无法表达 `"pipeline"` 策略；AgentSpec 字典也有限。

**拆分步骤：**

- [ ] **Step 3.1: 为分解器写失败测试**

`internal/orchestrator/decomposer_test.go`：

```go
func TestLLMDecomposer(t *testing.T) {
    cfg := config.Load() // 或构造最小 cfg
    d := NewLLMDecomposer(cfg)
    result := d.Decompose("设计一个电商网站：前端 Vue，后端 Go，数据库 SQLite，还要部署文档", "pipeline")
    if len(result.Agents) < 3 {
        t.Fatalf("expected at least 3 agents, got %d", len(result.Agents))
    }
    if result.Strategy != "pipeline" {
        t.Fatalf("expected pipeline strategy, got %s", result.Strategy)
    }
}
```

Run 失败，undefined `NewLLMDecomposer`。

- [ ] **Step 3.2: 实现 LLMDecomposer**

`internal/orchestrator/decomposer.go`：

```go
package orchestrator

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/anmingwei/multi-agent-platform/internal/config"
    "github.com/anmingwei/multi-agent-platform/internal/llm"
)

type LLMDecomposer struct {
    cfg      *config.Config
    provider llm.Provider
}

func NewLLMDecomposer(cfg *config.Config, provider llm.Provider) *LLMDecomposer {
    return &LLMDecomposer{cfg: cfg, provider: provider}
}

func (d *LLMDecomposer) Decompose(input, requestedStrategy string) (*DecomposeResult, error) {
    // 当没有 provider 或 LLMUseMock 时回退规则分解
    if d.provider == nil || d.cfg.LLMUseMock {
        return (&TaskDecomposer{}).Decompose(input, requestedStrategy), nil
    }
    prompt := buildDecompositionPrompt(input, requestedStrategy)
    resp, err := d.provider.Chat(llm.ChatRequest{...})
    // 解析 JSON，返回 DecomposeResult
}

func buildDecompositionPrompt(input, strategy string) string {
    return fmt.Sprintf(`Given the user request, break it into agent roles.
Output strictly JSON with fields: strategy, agents (array of {agent_id, name, system_prompt, input, allowed_tools, output_to}).
Strategy must be one of parallel/sequential/pipeline.
Input: %s
Preferred strategy: %s`, input, strategy)
}
```

若 LLM 返回非 JSON，catch error 后返回规则分解结果，避免失败。

- [ ] **Step 3.3: 实现 pipeline 策略**

case `"pipeline"`：在 agent 之间建立 chain，前一个的 `OutputTo` 设为后一个。

```go
if strategy == "pipeline" {
    for i := 0; i < len(specs)-1; i++ {
        specs[i].OutputTo = append(specs[i].OutputTo, specs[i+1].AgentID)
    }
    strategy = "parallel" // 底层仍然用 parallel + OutputTo 实现
}
```

- [ ] **Step 3.4: 接入 multi-agent endpoint**

`cmd/server/main.go:951`：

```go
var decomposer Decomposer
if cfg.LLMUseMock {
    decomposer = orchestrator.NewTaskDecomposer()
} else {
    decomposer = orchestrator.NewLLMDecomposer(cfg, routerClassifier)
}
result := decomposer.Decompose(req.Input, req.CaseType)
```

- [ ] **Step 3.5: 验证**

Run: `bash scripts/context-window-smoke.sh` 或针对 mock 模式跑默认分解。  
Run real LLM 模式：POST `/api/multi-agent` 查看返回的 `agent_ids` 数量 ≥ 3。

- [ ] **Step 3.6: 提交**

```bash
git add internal/orchestrator/decomposer.go internal/orchestrator/orchestrator.go cmd/server/main.go
git commit -m "Phase 7-C: LLM task decomposition and pipeline strategy"
```

---

### Task 4: 前端 Multi-Agent 启动入口与模式切换

**Files:**
- Modify: `web/src/components/TaskInput.vue`
- Modify: `web/src/App.vue:566-600`（handleSend）

**拆分步骤：**

- [ ] **Step 4.1: 给 TaskInput 增加 Multi-Agent 开关**

`web/src/components/TaskInput.vue` props/emits 更新：

```ts
const props = defineProps<{
  disabled: boolean
  isRunning: boolean
  isPending: boolean
  enableMultiAgent?: boolean
}>()

const emit = defineEmits<{
  send: [text: string, options: SendOptions]
  // ...
  update:enableMultiAgent: [v: boolean]
}>()
```

在 options-row 增加切换按钮：

```html
<button
  class="options-toggle"
  :class="{ active: props.enableMultiAgent }"
  @click="emit('update:enableMultiAgent', !props.enableMultiAgent)"
  title="Use multi-agent mode"
>
  🤖 Multi-Agent
</button>
```

- [ ] **Step 4.2: App.vue 接收状态并调用 multi-agent**

`App.vue` 增加 `const useMultiAgent = ref(false)`，绑定到 `TaskInput` 和 `MetricsPanel`（可选）。  
`handleSend` 改为：

```ts
if (useMultiAgent.value) {
    await startMultiAgentTask(text, { sessionId: session.id, timeoutSeconds: options.timeoutSeconds })
} else if (!session) { ... }
```

- [ ] **Step 4.3: 验证**

Run `npm run build` 通过，启动后 UI 上 "Send" 旁边出现 "🤖 Multi-Agent" 按钮，点击高亮后发送走 `/api/multi-agent`。

- [ ] **Step 4.4: 提交**

```bash
git add web/src/components/TaskInput.vue web/src/App.vue
git commit -m "Phase 7-D: frontend multi-agent launch toggle"
```

---

### Task 5: AgentBus 协作可视化面板

**Files:**
- Create: `web/src/components/AgentBusTimeline.vue`
- Modify: `web/src/types/events.ts`
- Modify: `web/src/composables/useTaskStore.ts:465`
- Modify: `web/src/components/TurnItem.vue`

**拆分步骤：**

- [ ] **Step 5.1: 新增事件类型与数据结构**

`web/src/types/events.ts`：

```ts
export interface AgentBusEventData {
  type: 'agent_message_sent' | 'agent_message_received'
  from_agent: string
  to_agent: string
  msg_type: string
  content: string
}
```

- [ ] **Step 5.2: TaskState 中保存 AgentBus 消息**

`TaskState` 增加：

```ts
export interface TaskState {
  // ...
  agentMessages?: AgentBusEventData[]
}
```

`useTaskStore.ts` 增加 `system_info` 对 `agent_message_sent/received` 的处理：

```ts
if (infoType === 'agent_message_sent' || infoType === 'agent_message_received') {
  if (!task.agentMessages) task.agentMessages = []
  task.agentMessages.push({
    type: infoType,
    from_agent: evt.data.from_agent as string,
    to_agent: evt.data.to_agent as string,
    msg_type: evt.data.msg_type as string,
    content: evt.data.content as string,
  })
}
```

- [ ] **Step 5.3: 创建 AgentBusTimeline 组件**

`web/src/components/AgentBusTimeline.vue`：

```vue
<script setup lang="ts">
import type { AgentBusEventData } from '../types/events'
const props = defineProps<{ messages: AgentBusEventData[] }>()
</script>
<template>
  <div class="agent-bus-timeline">
    <div v-for="(msg, idx) in messages" :key="idx" class="bus-message" :class="msg.type">
      <span class="bus-from">{{ msg.from_agent }}</span>
      <span class="bus-arrow">→</span>
      <span class="bus-to">{{ msg.to_agent }}</span>
      <span class="bus-type">{{ msg.msg_type }}</span>
      <pre class="bus-content">{{ msg.content }}</pre>
    </div>
  </div>
</template>
<style scoped>...</style>
```

- [ ] **Step 5.4: 在 Task/Turn 视图挂载组件**

`TurnItem.vue` turn-body 底部增加：

```html
<div v-if="task.agentMessages && task.agentMessages.length > 0" class="turn-agent-bus">
  <h4>Agent Collaboration</h4>
  <AgentBusTimeline :messages="task.agentMessages" />
</div>
```

- [ ] **Step 5.5: 验证**

启动 multi-agent sequential（researcher → writer），UI 中看到 researcher 出现后 writer 的 AgentTree 顶部显示来自 researcher 的消息。

- [ ] **Step 5.6: 提交**

```bash
git add web/src/components/AgentBusTimeline.vue web/src/types/events.ts web/src/composables/useTaskStore.ts web/src/components/TurnItem.vue
git commit -m "Phase 7-E: visualize agent-bus messages in UI"
```

---

### Task 6: 子任务独立控制与多 Agent 工作流编辑器

**Files:**
- Create: `web/src/components/MultiAgentWorkflowEditor.vue`
- Modify: `web/src/App.vue`
- Modify: `web/src/composables/useTaskStore.ts:977-1008`

**拆分步骤：**

- [ ] **Step 6.1: 设计 Agent 工作流配置类型**

`web/src/types/agent.ts`（如无则新建）：

```ts
export interface WorkflowAgentSpec {
  agentId: string
  name: string
  systemPrompt: string
  input: string
  allowedTools?: string[]
  outputTo?: string[]
  model?: string
}

export interface WorkflowConfig {
  strategy: 'parallel' | 'sequential' | 'pipeline'
  agents: WorkflowAgentSpec[]
}
```

- [ ] **Step 6.2: 实现 MultiAgentWorkflowEditor 组件**

基础版支持：
- 策略下拉（parallel/sequential/pipeline）。
- 可新增/删除 agent，每个 agent 可编辑 name、system prompt、input、allowed tools。  
- Pipeline 模式下自动设置 `outputTo` 为下一个 agent。  
- 保存时 emit 配置。

- [ ] **Step 6.3: 在 App.vue 打开编辑器的入口**

TaskInput 的 options-row 增加：

```html
<button class="options-toggle" @click="showWorkflowEditor = true">🛠 Workflow</button>
```

App.vue 增加 `const showWorkflowEditor = ref(false)`。

- [ ] **Step 6.4: useTaskStore 支持直接提交 agent specs**

修改 `startMultiAgentTask` 的签名：

```ts
async function startMultiAgentTask(
  input: string,
  options: { caseType?: string; sessionId?: string; timeoutSeconds?: number; agents?: WorkflowAgentSpec[] } = {}
) { ... }
```

POST body 增加：`agents: options.agents`。

- [ ] **Step 6.5: 子任务单独控制按钮**

`AgentTree.vue` header 增加：

```html
<button v-if="isRunning" class="agent-control-btn" @click.stop="emit('cancel', agent.id)">Cancel</button>
<button v-if="isRunning" class="agent-control-btn" @click.stop="emit('pause', agent.id)">Pause</button>
```

`TurnItem.vue` 透传到 App.vue，App.vue 调用 `cancelTask(agentId)`（需要扩展为带 agent 参数）。  
`useTaskStore.ts` 的 `pauseTask/resumeTask/cancelTask` 增加可选 `agentId` 参数，发送 WS 控制消息时带上 `agent_id`。

- [ ] **Step 6.6: 验证**

UI 上打开 Workflow 编辑器添加 researcher/writer/reviewer 三个 agent，保存后发送；运行中点击 researcher 的 Cancel 只取消它。  
`npm run build` 通过。

- [ ] **Step 6.7: 提交**

```bash
git add web/src/components/MultiAgentWorkflowEditor.vue web/src/types/agent.ts web/src/App.vue web/src/components/TaskInput.vue web/src/components/AgentTree.vue web/src/components/TurnItem.vue web/src/composables/useTaskStore.ts
git commit -m "Phase 7-F: multi-agent workflow editor and per-agent controls"
```

---

## 4. 跨任务集成验证

### 4.1 端到端冒烟

1. 启动后端：`go run ./cmd/server`（确保 `.env` 中 `LLM_USE_MOCK=false` 可选）。  
2. 启动前端：`cd web && npm run dev`。  
3. 新建 session，点 “🤖 Multi-Agent” → “🛠 Workflow”，添加 `researcher`（allowed_tools: read_file, run_shell）→ `writer`（allowed_tools: write_file） → `reviewer`（allowed_tools: read_file），策略选 `pipeline`。  
4. 输入 "用 Go 写一个快速排序并加单元测试"，发送。  
5. 期望：出现 researcher → writer → reviewer 三个 AgentTree；Agent Collaboration 面板里显示 `researcher → writer` 和 `writer → reviewer` 的消息；token、duration、模型名每个 agent 独立显示。  
6. 点击 researcher 的 Cancel，只该 agent 失败，writer/reviewer 收到 sendAgentMessage 失败消息但继续执行（依赖底层行为，这里只验证 cancel 事件只下发到指定 agent）。  

### 4.2 回归检查

- `go test ./...` 通过。  
- `cd web && npm run build` 通过。  
- `go vet ./...` 无新增 warning。  

---

## 5. 风险和回滚

| 风险 | 缓解 |
|------|------|
| Engine Pause/Resume 增加锁竞争 | 用 `atomic.Bool` + channel，保持无锁读；单测覆盖快速 pause/resume |
| LLM 分解返回非 JSON 导致 panic | 解析失败回退规则分解，并记录 warning |
| 子任务 cancel key 变更导致旧前端控制失效 | 保留 fallback 到 root task 的 cancel 逻辑 |
| Workflow 编辑器字段多，移动端体验差 | 先用 overlay 版本，Phase 8 再适配移动端抽屉 |

---

## 6. 后续可选增强

1. **Trace 视图**：把 `context_window_snapshot` 与 AgentBus 消息合并成时间线，类似 OpenTelemetry trace。  
2. **动态重排**：在 editor 里拖拽 agent 顺序，自动更新 `pipeline` output_to。  
3. **AgentMarket**：从 `/api/agents` 拉取已有 agent 作为模板拖入 workflow。  
4. **审批按 agent**：高风险操作可在 editor 里指定是否需要人工审批。
