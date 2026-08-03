// orchestrator_test.go —— Orchestrator 内部行为单元测试。
//
// AgentBus 测试只验证 AgentBus 的管道行为（RegisterHandler / SendMessage /
// SetPersistFn），不启动完整的 Engine 或 SQLite。涉及真实 db 包的持久化
// 行为由 pkg/db/persistence_test.go 以及 migration 测试覆盖。
//
// runAgent model 解析测试会初始化一个内存 SQLite，验证 orchestrator 能按
// DB 中的 agent 记录解析 effectiveModel。

package orchestrator

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/internal/config"
	"github.com/ayanmw/multi-agent-platform/internal/tool"
	"github.com/ayanmw/multi-agent-platform/internal/ws"
	"github.com/ayanmw/multi-agent-platform/pkg/db"
)

// TestAgentBus_DeliversToRegisteredHandler 验证 SendMessage 会同步调用
// 为目标 agent 注册的 handler。
func TestAgentBus_DeliversToRegisteredHandler(t *testing.T) {
	bus := NewAgentBus()

	var got AgentMessage
	var mu sync.Mutex
	ready := make(chan struct{})
	bus.RegisterHandler("agent_b", func(msg AgentMessage) {
		mu.Lock()
		got = msg
		mu.Unlock()
		close(ready)
	})

	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_a",
		ToAgentID:   "agent_b",
		Type:        "request",
		Content:     "hello",
	})

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("handler not invoked within 2s")
	}

	mu.Lock()
	defer mu.Unlock()
	if got.FromAgentID != "agent_a" || got.ToAgentID != "agent_b" || got.Content != "hello" {
		t.Errorf("delivered message mismatch: %+v", got)
	}
	if got.Timestamp.IsZero() {
		t.Error("Timestamp not set by SendMessage")
	}
}

// TestAgentBus_PersistFnHookFired 确认 SetPersistFn 安装的 hook 会对
// 每次 SendMessage（无论已投递还是入队）都触发。
func TestAgentBus_PersistFnHookFired(t *testing.T) {
	bus := NewAgentBus()

	var seen []AgentMessage
	var mu sync.Mutex
	bus.SetPersistFn(func(msg AgentMessage) error {
		mu.Lock()
		seen = append(seen, msg)
		mu.Unlock()
		return nil
	})

	// 注册一个 handler，使第二次发送能立即投递；第一次发送的目标未注册，
	// 会被入队。两次发送都应触发持久化 hook。
	bus.RegisterHandler("agent_b", func(AgentMessage) {})

	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_a",
		ToAgentID:   "ghost_agent",
		Type:        "observation",
		Content:     "queued",
	})
	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_a",
		ToAgentID:   "agent_b",
		Type:        "response",
		Content:     "delivered",
	})

	// 持久化 hook 在 goroutine 中运行；短暂轮询直到两条消息都被记录。
	// 顺序不确定，因此按集合成员判断而不是按位置。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("persist hook fired %d times, want 2", len(seen))
	}
	got := map[string]bool{}
	for _, m := range seen {
		got[m.Content] = true
		if m.Timestamp.IsZero() {
			t.Errorf("persisted message missing Timestamp: %+v", m)
		}
	}
	if !got["queued"] || !got["delivered"] {
		t.Errorf("missing persisted messages: %v", got)
	}
}

// TestAgentBus_NoPersistWhenHookNil 用于说明：默认（nil）hook 是 no-op，
// SendMessage 仍能正常工作。
func TestAgentBus_NoPersistWhenHookNil(t *testing.T) {
	bus := NewAgentBus()
	bus.SendMessage(AgentMessage{
		FromAgentID: "a",
		ToAgentID:   "b",
		Content:     "x",
	})
	// 只断言没有 panic —— 通过即说明 nil hook 被正确处理。
}

// TestAgentBus_RoutesBySubTask 验证当消息携带不同的 subTaskID 时，
// 精确 (agentID, subTaskID) handler 能收到消息，而 agentID-only
// handler 不会收到。Phase 7-J。
func TestAgentBus_RoutesBySubTask(t *testing.T) {
	bus := NewAgentBus()

	fallbackCalled := make(chan struct{}, 1)
	exactCalled := make(chan AgentMessage, 1)

	bus.RegisterHandler("agent_a", func(msg AgentMessage) {
		close(fallbackCalled)
	})
	bus.RegisterHandlerBySubTask("agent_a", "sub-x", func(msg AgentMessage) {
		exactCalled <- msg
	})

	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_b",
		ToAgentID:   "agent_a",
		SubTaskID:   "sub-x",
		Type:        "request",
		Content:     "to sub-x",
	})

	select {
	case msg := <-exactCalled:
		if msg.Content != "to sub-x" {
			t.Errorf("exact handler content = %q, want to sub-x", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exact (agentID, subTaskID) handler not called")
	}

	select {
	case <-fallbackCalled:
		t.Fatal("agentID-only fallback should not receive a sub-task specific message")
	case <-time.After(100 * time.Millisecond):
		// 预期：fallback 未被调用。
	}
}

// TestAgentBus_FallsBackToAgentIDOnly 验证当存在针对其它 subTaskID 的
// 精确 handler 时，携带空 SubTaskID 的消息会被投递给 agentID-only
// handler。
func TestAgentBus_FallsBackToAgentIDOnly(t *testing.T) {
	bus := NewAgentBus()

	fallbackCalled := make(chan AgentMessage, 1)
	exactCalled := make(chan struct{}, 1)

	bus.RegisterHandler("agent_a", func(msg AgentMessage) {
		fallbackCalled <- msg
	})
	bus.RegisterHandlerBySubTask("agent_a", "sub-x", func(msg AgentMessage) {
		close(exactCalled)
	})

	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_b",
		ToAgentID:   "agent_a",
		Type:        "request",
		Content:     "broadcast",
	})

	select {
	case msg := <-fallbackCalled:
		if msg.Content != "broadcast" {
			t.Errorf("fallback content = %q, want broadcast", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agentID-only fallback not called")
	}

	select {
	case <-exactCalled:
		t.Fatal("exact handler should not receive a message without SubTaskID")
	case <-time.After(100 * time.Millisecond):
		// 预期：exact handler 未被调用。
	}
}

// TestAgentBus_ConcurrentSameAgentIDDifferentSubTask 验证 Phase 7-H2 阶段 6
// (MA7) 的核心隔离契约：两个同名 agent（例如两个 session 各自的 "agent_writer"）
// 在不同 subTaskID 下注册的 handler 互不串台——发给 (agent_writer, subA) 的消息
// 不会进入 (agent_writer, subB) 的 handler，反之亦然。
//
// 背景：worker 以前走 agentID-only RegisterHandler，后注册的会覆盖前者，导致并发
// session 同名 worker 的 AgentBus 消息被错误投递。现在 worker 也按 SubTaskID 注册。
func TestAgentBus_ConcurrentSameAgentIDDifferentSubTask(t *testing.T) {
	bus := NewAgentBus()

	gotA := make(chan AgentMessage, 1)
	gotB := make(chan AgentMessage, 1)

	bus.RegisterHandlerBySubTask("agent_writer", "subA", func(msg AgentMessage) {
		gotA <- msg
	})
	bus.RegisterHandlerBySubTask("agent_writer", "subB", func(msg AgentMessage) {
		gotB <- msg
	})

	// 发往 subA 的消息只能进 gotA。
	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_researcher", ToAgentID: "agent_writer",
		SubTaskID: "subA", Type: "observation", Content: "for A",
	})
	// 发往 subB 的消息只能进 gotB。
	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_researcher", ToAgentID: "agent_writer",
		SubTaskID: "subB", Type: "observation", Content: "for B",
	})

	select {
	case msg := <-gotA:
		if msg.Content != "for A" {
			t.Errorf("subA handler got %q, want for A", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subA handler not called")
	}
	select {
	case msg := <-gotB:
		if msg.Content != "for B" {
			t.Errorf("subB handler got %q, want for B", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subB handler not called")
	}

	// 交叉检查：两个 channel 各自只收到一条，没有串台。
	select {
	case extra := <-gotA:
		t.Errorf("subA handler received stray message: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case extra := <-gotB:
		t.Errorf("subB handler received stray message: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestAgentBus_WorkerUnregisterBySubTask 验证 worker 退出时按 subTaskID 注销，
// 不会误删同名但不同 subTaskID 的 handler（MA7 配套清理正确性）。
func TestAgentBus_WorkerUnregisterBySubTask(t *testing.T) {
	bus := NewAgentBus()

	gotB := make(chan AgentMessage, 1)
	bus.RegisterHandlerBySubTask("agent_writer", "subA", func(AgentMessage) {})
	bus.RegisterHandlerBySubTask("agent_writer", "subB", func(msg AgentMessage) {
		gotB <- msg
	})

	// subA 退出：只应删除 (agent_writer, subA)，subB 仍可投递。
	bus.UnregisterHandlerBySubTask("agent_writer", "subA")

	bus.SendMessage(AgentMessage{
		FromAgentID: "x", ToAgentID: "agent_writer",
		SubTaskID: "subB", Type: "observation", Content: "still alive",
	})
	select {
	case msg := <-gotB:
		if msg.Content != "still alive" {
			t.Errorf("subB got %q, want still alive", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unregistering subA should not affect subB delivery")
	}
}

// TestRunAgent_UsesDBAgentModel 验证：当子 agent 的 DB 记录配置 Model="minimax-m2.5"
// 且 spec.Model 为空时，runAgent 解析出的 effectiveModel 与传给 EngineConfig.Model
// 的 model 都应是该 DB 模型，而非 cfg.LLMModel。该测试不跑完整 Engine，仅通过
// agent_ready 事件捕获最终选用的 model。
func TestRunAgent_UsesDBAgentModel(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	if err := db.Init(dbPath); err != nil {
		t.Fatalf("db.Init: %v", err)
	}
	defer db.Close()

	agentID := "test_child_agent"
	agentModel := "minimax-m2.5"
	if err := db.InsertAgent(db.InsertAgentOptions{
		ID:    agentID,
		Name:  "Test Child Agent",
		Model: agentModel,
	}); err != nil {
		t.Fatalf("insert agent: %v", err)
	}

	bus := NewAgentBus()
	agentBusAdapter := NewAgentBusAdapter(bus)

	hub := ws.NewHub()
	go hub.Run()
	defer func() { _ = hub.Shutdown(context.Background()) }()

	client := hub.RegisterTestClient("test-client")
	defer hub.UnregisterTestClient(client)

	cfg := &config.Config{
		LLMModel:    "deepseek-v4-flash",
		LLMUseMock:  true,
		LLMEndpoint: "http://mock",
		LLMAPIKey:   "mock",
	}
	tools := tool.NewRegistry()
	o := New(hub, cfg, tools, nil, agentBusAdapter, nil, nil, nil, nil)

	rootTaskID := "task_root_123"
	spec := AgentSpec{
		AgentID:      agentID,
		Name:         "Test Child Agent",
		SystemPrompt: "You are a test agent.",
		// 使用内置 dialogue 脚本关键字，确保 mock provider 在没有 case ID 时仍能命中脚本。
		Input: "hi",
		Model: "",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	var seenModel string
	var mu sync.Mutex
	go func() {
		o.RunWithCallback(ctx, rootTaskID, []AgentSpec{spec}, func(r AgentResult) {})
		close(done)
	}()

	for {
		select {
		case evt := <-client.Send:
			if evt.Type == "agent_ready" {
				model, _ := evt.Data["model"].(string)
				if model != "" {
					mu.Lock()
					seenModel = model
					mu.Unlock()
					cancel()
					<-done
					goto assertModel
				}
			}
		case <-done:
			goto assertModel
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for agent_ready event")
		}
	}

assertModel:
	mu.Lock()
	defer mu.Unlock()
	if seenModel != agentModel {
		t.Fatalf("agent_ready model = %q, want %q", seenModel, agentModel)
	}
}

// TestAgentBus_WorkerToLeaderRouting 是 N0-01 的投递侧回归：
// worker 以 (ToAgentID="leader", SubTaskID=rootTaskID) 为目标发送时，消息必须
// 立即进入 leader Engine 注册的精确 handler；而 ToAgentID 为空的旧行为只会
// 让消息滞留在待投递队列中（永远匹配不到任何 handler），这正是修复前
// approval_request 送不达 leader 的根因。
func TestAgentBus_WorkerToLeaderRouting(t *testing.T) {
	bus := NewAgentBus()
	const rootTaskID = "task-root"

	delivered := make(chan AgentMessage, 2)
	// leader Engine 按 (leader, rootTaskID) 注册（见 runtime.Engine.Run）。
	bus.RegisterHandlerBySubTask("leader", rootTaskID, func(msg AgentMessage) {
		delivered <- msg
	})

	// 修复后的发送目标：精确命中 leader handler。
	bus.SendMessage(AgentMessage{
		FromAgentID:   "agent_writer",
		FromSubTaskID: rootTaskID + "_agent_writer",
		ToAgentID:     "leader",
		SubTaskID:     rootTaskID,
		Type:          "approval_request",
		Content:       "请审批 run_shell",
	})

	select {
	case msg := <-delivered:
		if msg.Type != "approval_request" || msg.FromAgentID != "agent_writer" {
			t.Fatalf("投递到 leader 的消息不符: type=%q from=%q", msg.Type, msg.FromAgentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker → leader 的消息未被投递")
	}

	// 修复前的发送目标（空 ToAgentID）：匹配不到任何 handler，只会入队滞留。
	bus.SendMessage(AgentMessage{
		FromAgentID: "agent_writer",
		ToAgentID:   "",
		SubTaskID:   rootTaskID,
		Type:        "approval_request",
		Content:     "空目标消息",
	})

	select {
	case msg := <-delivered:
		t.Fatalf("空目标消息不应被投递，却收到: %q", msg.Content)
	case <-time.After(100 * time.Millisecond):
		// 预期：未投递。
	}

	bus.mu.RLock()
	queued := len(bus.queue)
	bus.mu.RUnlock()
	if queued != 1 {
		t.Fatalf("待投递队列长度 = %d，期望 1（空目标消息滞留其中）", queued)
	}
}
