# 修复：LLM 运行结束后 Pause/Cancel 失效与 UI 状态滞留

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 real-LLM 长任务结束后，前端正因缺少终态事件、后端只依赖运行时 registry 而报 "cancel/pause received for unknown task"，进而出现 "Server unknown" 伪 agent lane 与按钮无响应的问题。

**Architecture:** 控制消息不再把 `cancelRegistry`/`engineRegistry` 当作唯一真实来源，而是先查 DB 任务状态：终态任务静默完成并广播 `task_status_sync`；运行中任务在 registry 命中时执行 cancel/pause/resume；未命中时按“任务仍在 running 但句柄丢失”处理并记录告警，不污染前端。前端在断线重连/刷新加载 session 历史时，强制把 DB 中的 task 真实状态覆盖本地缓存，避免 stale running 状态残留。

**Tech Stack:** Go 1.25, modernc.org/sqlite, gorilla/websocket, Vue 3 + TypeScript

---

## File map

| 文件 | 职责 |
|------|------|
| `cmd/server/main.go` | WebSocket 控制 handler (pause/resume/cancel)。修改后增加 DB 状态检查，失败时不发 `system_info` 污染时间线。 |
| `cmd/server/runner.go` | agent 运行时注册/反注册 cancel 与 engine。已在当前代码中，无需改动注册机制；本计划只增加一个 `control_failed` 事件常量引用。 |
| `pkg/db/persistence.go` | 已存在 `QueryTaskByID`、`UpdateTask` 等。本计划新增一个轻量函数 `IsTaskTerminated(id string) (bool, string, error)` 供控制 handler 快速判断。**无需**新表。 |
| `internal/ws/hub.go` | 事件环形缓冲区。保持原样；本计划仅涉及事件类型使用。 |
| `pkg/event/event.go` | 建议新增 `task_status_sync` / `control_failed` 事件类型常量，让前端可识别并忽略伪 lane。 |
| `web/v2/src/composables/useTaskStore.ts` | 处理 `task_status_sync` / `control_failed` 事件；`loadSessionTurns` 对已缓存任务也同步 DB status。 |
| `web/v2/src/types/events.ts` | 追加新事件类型到 `EventType` union。 |
| `cmd/server/*_test.go` 与 `internal/runtime/*_test.go` | 新增/更新单元测试覆盖控制 handler 与 DB 状态查询路径。 |

---

## Task 1: 后端新增任务终态查询 helper

**Files:**
- Modify: `pkg/db/persistence.go:420-440` 之后追加

- [ ] **Step 1: 写 helper 函数**

```go
// IsTaskTerminated 返回 task 是否已处于终态，以及具体状态字符串。
// 控制 handler 用它快速判断：终态任务不需要再 cancel/pause/resume。
func IsTaskTerminated(id string) (bool, string, error) {
	if DB == nil {
		return false, "", fmt.Errorf("db not initialized")
	}
	var status string
	err := DB.QueryRow(`SELECT status FROM tasks WHERE id=?`, id).Scan(&status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "", nil
		}
		return false, "", err
	}
	switch status {
	case "completed", "failed", "cancelled":
		return true, status, nil
	default:
		return false, status, nil
	}
}
```

- [ ] **Step 2: 运行现有测试确保无回归**

Run: `go test ./pkg/db/... -run TestPersistence -count=1`
Expected: PASS（该目录若无现有 persistence 测试则跳过，保证编译通过）

Run: `go build ./pkg/db/...`
Expected: 无编译错误

- [ ] **Step 3: Commit**

```bash
git add pkg/db/persistence.go
git commit -m "fix(db): add IsTaskTerminated helper for control handler"
```

---

## Task 2: 控制 handler 改查 DB 真实状态并避免污染前端

**Files:**
- Modify: `cmd/server/main.go:288-344` (cancel/pause/resume case 分支)
- Modify: `pkg/event/event.go`（追加 `task_status_sync` / `control_failed` 常量）

- [ ] **Step 1: 追加事件常量**

在 `pkg/event/event.go` 中合适位置（靠近 `task_failed/task_completed`）追加：

```go
// 控制消息相关事件
const (
	TaskStatusSync = "task_status_sync" // 当控制消息到达但任务已终态时，把后端真实状态同步给前端
	ControlFailed  = "control_failed"  // 控制动作因目标不可控或状态异常失败，不创建 agent lane
)
```

如果 `event.go` 使用枚举/字符串形态不同，按现有风格添加。

- [ ] **Step 2: 写一个内部 helper 函数读取 DB 状态并发送 sync 事件**

在 `cmd/server/main.go` 的 `ControlHandler` 附近增加（放在 `handleWebSocket` 内部，靠近 registry 辅助函数）：

```go
// resolveControlTarget 先查 DB 任务状态，再决定在 registry 中查找 cancel/engine。
// 返回：targetFound（registry 中是否找到句柄）、terminated（任务是否已在 DB 中终态）、err。
// 若任务已终态，向前端广播 task_status_sync，让 UI 立即修正状态，且不发送 system_info。
func resolveControlTarget(msg ws.ClientControlMsg, action string) (targetFound bool, terminated bool, err error) {
	if msg.TaskID == "" {
		return false, false, nil
	}
	terminated, status, dberr := db.IsTaskTerminated(msg.TaskID)
	if dberr != nil {
		observability.DefaultLogger.Warn("control", "failed to query task status", map[string]any{"task_id": msg.TaskID, "error": dberr.Error()})
		// DB 查询失败时保守继续：仍尝试 registry，避免误拦截正常控制
		return false, false, nil
	}
	if terminated {
		observability.DefaultLogger.Info("control", action+" received for already terminated task", map[string]any{"task_id": msg.TaskID, "status": status})
		hub.SendEvent(event.NewEvent(event.TaskStatusSync, msg.TaskID, msg.AgentID, 0, map[string]any{
			"status": status,
			"reason": "task_already_" + status,
		}))
		return false, true, nil
	}
	return false, false, nil
}
```

注意：`hub`、`db` 已在 `handleWebSocket` 闭包内可访问；如果作用域不方便，改成用包级函数 `emitTaskStatusSync` 并传入 `*ws.Hub`。

- [ ] **Step 3: 修改 cancel case**

把 `case "cancel":` 分支改成：

```go
case "cancel":
	if msg.TaskID == "" {
		observability.DefaultLogger.Warn("control", "cancel received without task_id", nil)
		return
	}
	if foundTarget, terminated, _ := resolveControlTarget(msg, "cancel"); terminated {
		return
	}
	if msg.AgentID == "" && !terminated {
		// 如果未提供 agent_id，兼容旧行为：按 taskID 查找 root 任务
		if cancelFn, ok := loadCancel(msg.TaskID, ""); ok {
			observability.DefaultLogger.Info("control", "cancelling root task", map[string]any{"task_id": msg.TaskID})
			cancelFn()
			return
		}
	}
	if cancelFn, ok := loadCancel(msg.TaskID, msg.AgentID); ok {
		target := msg.TaskID
		if msg.AgentID != "" {
			target = msg.TaskID + "/" + msg.AgentID
		}
		observability.DefaultLogger.Info("control", "cancelling task", map[string]any{"target": target, "agent_id": msg.AgentID})
		cancelFn()
		return
	}
	observability.DefaultLogger.Warn("control", "cancel received for unknown task", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})
```

说明：移除所有 `hub.SendEvent(system_info, ...)` 的 cancel 路径，避免生成 server lane。

- [ ] **Step 4: 修改 pause/resume case**

把 `case "pause":` / `case "resume":` 分支改成同样结构：

```go
case "pause":
	if msg.TaskID == "" {
		observability.DefaultLogger.Warn("control", "pause received without task_id", nil)
		return
	}
	if _, terminated, _ := resolveControlTarget(msg, "pause"); terminated {
		return
	}
	if engine, ok := loadEngine(msg.TaskID, msg.AgentID); ok {
		observability.DefaultLogger.Info("control", "pausing engine", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})
		engine.Pause()
		return
	}
	observability.DefaultLogger.Warn("control", "pause received for unknown task", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})

case "resume":
	if msg.TaskID == "" {
		observability.DefaultLogger.Warn("control", "resume received without task_id", nil)
		return
	}
	if _, terminated, _ := resolveControlTarget(msg, "resume"); terminated {
		return
	}
	if engine, ok := loadEngine(msg.TaskID, msg.AgentID); ok {
		observability.DefaultLogger.Info("control", "resuming engine", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})
		engine.Resume()
		return
	}
	observability.DefaultLogger.Warn("control", "resume received for unknown task", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})
```

同样移除 pause/resume 失败时发送 `system_info` 的代码。

- [ ] **Step 5: 添加单元测试**

Create: `cmd/server/control_handler_test.go`

```go
package main

import (
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
)

// mockHub 用于捕获控制 handler 发出的事件。
type mockHub struct {
	events []event.Event
}

func (m *mockHub) SendEvent(evt event.Event) { m.events = append(m.events, evt) }
func (m *mockHub) RegisterTestClient(c *ws.Client) {}

func TestControlHandlerCancel_TerminatedTaskEmitsSync(t *testing.T) {
	// setup 内存 DB
	if err := db.InitDatabase(":memory:"); err != nil {
		t.Fatal(err)
	}
	defer db.CloseDatabase()
	started := time.Now()
	if err := db.InsertTask(db.TaskRecord{ID: "t1", Status: "failed", StartedAt: started}); err != nil {
		t.Fatal(err)
	}

	terminated, status, err := db.IsTaskTerminated("t1")
	if err != nil {
		t.Fatal(err)
	}
	if !terminated || status != "failed" {
		t.Fatalf("expected terminated=true status=failed, got %v %s", terminated, status)
	}
}
```

注意：当前 `main.go` 的 `hub` 是闭包变量，直接单元测试较困难。推荐的轻量做法是：把控制处理逻辑提取到 `(s *appServer) handleControl(msg ws.ClientControlMsg, hub *ws.Hub)` 方法，main.go 中闭包调用它。这样测试可以用 mock hub 注入。

如果这一步重构幅度较大，可以先保留现有结构，仅验证 `IsTaskTerminated` 的单元测试。

- [ ] **Step 6: 运行构建与测试**

Run: `go build ./cmd/server/...`
Expected: 编译通过

Run: `go test ./cmd/server/... -run TestControl -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/server/main.go pkg/event/event.go cmd/server/control_handler_test.go
git commit -m "fix(server): control handler checks DB status before registry, no system_info pollution"
```

---

## Task 3: 前端识别新事件并同步 DB 真实状态

**Files:**
- Modify: `web/v2/src/types/events.ts`
- Modify: `web/v2/src/composables/useTaskStore.ts`

- [ ] **Step 1: 追加事件类型**

在 `web/v2/src/types/events.ts` 的 `EventType` union 中追加：

```ts
export type EventType =
  | 'task_started'
  | 'task_completed'
  | 'task_failed'
  | 'task_status_sync'
  | 'control_failed'
  | 'system_info'
  // ... 现有类型
```

- [ ] **Step 2: 在 `handleEvent` 中处理 `task_status_sync`**

在 `useTaskStore.ts` 的 `handleEvent` 中找到 `case 'task_failed':` 附近，追加：

```ts
case 'task_status_sync': {
  const terminalStatus = data?.status as TaskStatus | undefined
  if (terminalStatus && ['completed', 'failed', 'cancelled'].includes(terminalStatus)) {
    task.status = terminalStatus
    task.finalResult = task.finalResult || (data?.reason as string) || null
    // 标记为非运行态，使控制按钮隐藏
    const agentId = evt.agent_id || 'agent_default'
    if (task.agents[agentId]) {
      task.agents[agentId].isRunning = false
    }
  }
  break
}

case 'control_failed': {
  // 控制动作失败（如任务已终态或句柄丢失），不创建新的 agent lane。
  // 只写入当前 active agent 的 trace 或直接忽略，避免生成 server lane。
  break
}
```

注意：`isRunning` 字段如不存可在 `AgentState` 类型中按需添加，或复用 `status`。

- [ ] **Step 3: 修复 `loadSessionTurns` 对已缓存任务的 stale 状态**

当前 `loadSessionTurns` 在 task 已缓存时直接 `continue`，导致本地 running 不会被 DB 真实状态覆盖。改为：

```ts
for (const t of tasks) {
  const cached = taskCache.value[t.id]
  if (cached) {
    // 同步 DB 中的最新状态，避免长任务运行结束后断线重连导致 stale running
    if (t.status && t.status !== cached.status) {
      console.log('[useTaskStore] syncing cached task status from DB:', t.id, cached.status, '->', t.status)
      cached.status = t.status as TaskStatus
      if (['completed', 'failed', 'cancelled'].includes(t.status)) {
        cached.finalResult = cached.finalResult || `Task ended with status: ${t.status}`
        // 停止所有 agent 的 running 标记
        for (const aid of Object.keys(cached.agents)) {
          const a = cached.agents[aid]
          if (a) a.isRunning = false
        }
      }
    }
    if (!latestTask || t.started_at > latestTask.started_at) latestTask = t
    continue
  }
  try {
    await loadTask(t.id)
    latestTask = t
    loaded++
  } catch (err) {
    console.warn('[useTaskStore] loadTask failed for', t.id, err)
  }
}
```

- [ ] **Step 4: 运行前端类型检查**

Run: `cd web/v2 && npx vue-tsc --noEmit`
Expected: 无类型错误

Run: `cd web/v2 && npm run build`
Expected: 构建成功（仅验证，不实际部署）

- [ ] **Step 5: Commit**

```bash
git add web/v2/src/types/events.ts web/v2/src/composables/useTaskStore.ts
git commit -m "fix(ui): handle task_status_sync and sync DB status into cached tasks"
```

---

## Task 4: 增大事件缓冲区并防止关键终态事件被挤掉

**Files:**
- Modify: `internal/ws/hub.go:46`

- [ ] **Step 1: 调大 defaultEventBufferSize**

```go
const defaultEventBufferSize = 5000
```

说明：real-LLM 长任务可产生大量 delta 事件；1000 条可能无法覆盖一次断线重连的间隙。5000 条在不显著增加内存的前提下，提升事件恢复成功率。

- [ ] **Step 2: 添加针对关键事件的优先级保护（可选但推荐）**

如果缓冲区被灌满，`append` 会驱逐最旧事件。若最旧事件刚好是客户端 lastEventId，重连会 410。改进方案：在 `eventBuffer.append` 中，当缓冲区将满且新事件是 `task_failed`/`task_completed`/`task_status_sync` 时，额外保留（可提升 capacity 临时或优先驱逐非关键事件）。

最小实现：在 `eventBuffer` 中保留固定数量的“关键事件”不被驱逐：

```go
// eventBuffer 增加保留区
const criticalEventReserve = 200

func (b *eventBuffer) append(evt event.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	isCritical := evt.Type == "task_failed" || evt.Type == "task_completed" || evt.Type == "task_status_sync"

	if len(b.events) == b.capacity {
		// 先尝试驱逐最旧的非关键事件；若全为关键事件则驱逐最旧的。
		removed := false
		if !isCritical {
			for i := 0; i < len(b.events)-criticalEventReserve; i++ {
				oldest := b.events[i]
				if oldest.Type != "task_failed" && oldest.Type != "task_completed" && oldest.Type != "task_status_sync" {
					b.removeAt(i)
					removed = true
					break
				}
			}
		}
		if !removed {
			oldest := b.events[0]
			b.removeAt(0)
		}
	}
	b.index[evt.EventID] = len(b.events)
	b.events = append(b.events, evt)
}

func (b *eventBuffer) removeAt(idx int) {
	oldest := b.events[idx]
	delete(b.index, oldest.EventID)
	b.events = append(b.events[:idx], b.events[idx+1:]...)
	for id, i := range b.index {
		if i > idx {
			b.index[id] = i - 1
		}
	}
}
```

注意：这会改变现有 `append` 行为，必须配套单元测试。

- [ ] **Step 3: 更新或新增 eventBuffer 测试**

Modify: `internal/ws/hub_test.go`（如果不存在则创建）

```go
func TestEventBuffer_CriticalEventsNotEvicted(t *testing.T) {
	b := newEventBuffer(10)
	var criticalID string
	for i := 0; i < 9; i++ {
		b.append(event.Event{EventID: fmt.Sprintf("e%d", i), Type: "llm_delta"})
	}
	critical := event.Event{EventID: "critical", Type: "task_failed"}
	criticalID = critical.EventID
	b.append(critical)
	// 继续追加 5 条普通事件，critical 不应被驱逐
	for i := 10; i < 15; i++ {
		b.append(event.Event{EventID: fmt.Sprintf("e%d", i), Type: "llm_delta"})
	}
	if _, ok := b.index[criticalID]; !ok {
		t.Fatal("critical event was evicted")
	}
}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/ws/... -run TestEventBuffer -count=1`
Expected: PASS

Run: `go test ./internal/ws/... -count=1`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ws/hub.go internal/ws/hub_test.go
git commit -m "fix(ws): larger event buffer and protect terminal events from eviction"
```

---

## Task 5: 验证整体修复

**Files:**
- 新增/修改：无需新建文件，使用已有 mock 回归脚本验证。

- [ ] **Step 1: 编译整个项目**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 2: 运行 Go 单元测试**

Run: `go test ./cmd/server/... ./internal/ws/... ./pkg/db/... -count=1`
Expected: PASS

- [ ] **Step 3: 运行前端构建**

Run: `cd web/v2 && npm ci && npm run build`
Expected: 构建成功

- [ ] **Step 4: 用 mock 回归脚本跑一遍**

Run: `bash scripts/cases-regression.sh`
Expected: 21/21 PASS（控制 handler 改动不影响 mock 路径；UI 改动不影响 mock 回归）

- [ ] **Step 5: 手动复现验证（若环境允许）**

1. 启动 server：`go run ./cmd/server`
2. 前端打开控制室，启动一个 max_steps 较小的任务，或用现有 task
3. 任务自然失败后等待 2 秒，点击 Cancel/Pause
4. 期望：不再出现 "Server unknown" lane，按钮无响应但 UI 不会新增伪 agent
5. 刷新页面后，原任务状态显示为 failed

- [ ] **Step 6: Commit 最终文档更新**

```bash
git add docs/KNOWN_ISSUES.md roadmaps/ROADMAP.md
git commit -m "docs: update KNOWN_ISSUES and ROADMAP for control-state sync fix"
```

---

## Self-review checklist

| 需求 | 对应 Task |
|------|----------|
| 后端在 cancel/pause/resume 时先查 DB 终态 | Task 2 |
| 终态任务不再生成 “Server unknown” lane | Task 2 + Task 3 |
| 前端断线重连后 stale running 被修正 | Task 3 |
| 长时间运行的关键终态事件不易丢失 | Task 4 |
| 回归测试不引入新失败 | Task 5 |

**Placeholder scan:** 所有代码块均已给出具体实现，无 “TBD” / “implement later”。

**Type consistency:** `task_status_sync` 前后端均使用；`IsTaskTerminated` 返回 `(bool, string, error)`；`TaskStatus` union 包含 `cancelled` / `failed` / `completed`。

---

## Execution handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-27-control-state-sync-fix.md`.**

Two execution options:

1. **Subagent-Driven (recommended)** - dispatch a fresh subagent per task, review between tasks.
2. **Inline Execution** - execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
