package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/ayanmw/multi-agent-platform/internal/harness"
	"github.com/ayanmw/multi-agent-platform/internal/tool"
)

// historyMarker 是 cmd/server buildHistoryContext 生成的历史块标题。
// 测试用它来断言「运行时 prompt 携带历史、持久化 prompt 不携带历史」。
const historyMarker = "## Previous Conversation History"

// recordingSessionWriter 记录 Engine 写入 session_messages 的全部消息，
// 供 N0-02（多轮历史自复制）回归断言使用。
type recordingSessionWriter struct {
	mu   sync.Mutex
	msgs []SessionMessageRecord
}

func (w *recordingSessionWriter) write(m SessionMessageRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, m)
	return nil
}

// roleContent 返回首条匹配 role 的消息内容；不存在时返回空串与 false。
func (w *recordingSessionWriter) roleContent(role string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, m := range w.msgs {
		if m.Role == role {
			return m.Content, true
		}
	}
	return "", false
}

// newSessionHistoryEngine 构造一个可直接 Run 的最小 Engine：
// fakeJudgeProvider 一步返回最终答案（无 tool_calls），因此 ReAct loop
// 只跑一轮即结束，便于确定性断言持久化内容。
func newSessionHistoryEngine(t *testing.T, cfg EngineConfig, w *recordingSessionWriter) *Engine {
	t.Helper()
	if cfg.Model == "" {
		cfg.Model = "fake-model"
	}
	if cfg.Provider == nil {
		cfg.Provider = &fakeJudgeProvider{resp: "final answer"}
	}
	if cfg.Contract.Goal == "" {
		cfg.Contract = harness.TaskContract{Goal: "session history test", Scope: "."}
	}
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 3
	}
	if w != nil {
		cfg.SessionMessageWriter = w.write
	}
	return NewEngine(cfg, tool.NewRegistry(), &recordingBus{}, "task-session-history")
}

// TestPersistedSystemPrompt_PrefersBaseline 验证 persistedSystemPrompt 的
// 优先级：配置了 BaseSystemPrompt 时返回基线，否则回退运行时 prompt。
func TestPersistedSystemPrompt_PrefersBaseline(t *testing.T) {
	runtimePrompt := historyMarker + "\n\n[user]: 上一轮问题\n\nYou are a helpful assistant."
	baseline := "You are a helpful assistant."

	withBaseline := newSessionHistoryEngine(t, EngineConfig{
		AgentID:          "agent_default",
		SystemPrompt:     runtimePrompt,
		BaseSystemPrompt: baseline,
	}, nil)
	if got := withBaseline.persistedSystemPrompt(); got != baseline {
		t.Fatalf("配置 BaseSystemPrompt 时持久化 prompt = %q，期望基线 %q", got, baseline)
	}

	// 未配置 BaseSystemPrompt → 回退运行时 prompt（单轮场景两者等价）。
	withoutBaseline := newSessionHistoryEngine(t, EngineConfig{
		AgentID:      "agent_default",
		SystemPrompt: baseline,
	}, nil)
	if got := withoutBaseline.persistedSystemPrompt(); got != baseline {
		t.Fatalf("未配置 BaseSystemPrompt 时持久化 prompt = %q，期望回退到 %q", got, baseline)
	}
}

// TestRun_PersistsBaselineSystemPromptWithoutHistory 是 N0-02 的核心回归：
// 运行时 system prompt 携带上一轮历史文本，但写回 session_messages 的那份
// 必须是干净基线——否则下一轮读历史时会把历史再套一层，形成自复制。
func TestRun_PersistsBaselineSystemPromptWithoutHistory(t *testing.T) {
	baseline := "You are a helpful assistant."
	runtimePrompt := historyMarker + "\n\n### Turn 1\n\n[user]: 我叫小明\n\n## Current Task\n\n" + baseline

	w := &recordingSessionWriter{}
	e := newSessionHistoryEngine(t, EngineConfig{
		AgentID:          "agent_default",
		SessionID:        "sess-1",
		TurnIndex:        1,
		SystemPrompt:     runtimePrompt,
		BaseSystemPrompt: baseline,
	}, w)

	if _, _, err := e.Run(context.Background(), "我叫什么名字？"); err != nil {
		t.Fatalf("Run 失败: %v", err)
	}

	got, ok := w.roleContent("system")
	if !ok {
		t.Fatal("session_messages 中缺少 system 消息")
	}
	if strings.Contains(got, historyMarker) {
		t.Fatalf("持久化的 system prompt 仍携带历史文本（自复制未修复）:\n%s", got)
	}
	if got != baseline {
		t.Fatalf("持久化的 system prompt = %q，期望基线 %q", got, baseline)
	}

	// 运行时 prompt 仍必须携带历史，否则 LLM 看不到上下文——修复不能矫枉过正。
	e.msgMu.Lock()
	runtimeSystem := e.messages[0].Content
	e.msgMu.Unlock()
	if !strings.Contains(runtimeSystem, historyMarker) {
		t.Fatalf("运行时 system prompt 丢失了历史文本:\n%s", runtimeSystem)
	}
}

// TestRun_PersistedSystemPromptStableAcrossTurns 验证多轮下持久化的 system
// prompt 长度恒定：不随轮次线性膨胀（PLAN N0-02 的验证标准）。
func TestRun_PersistedSystemPromptStableAcrossTurns(t *testing.T) {
	baseline := "You are a helpful assistant."
	history := ""
	var sizes []int

	for turn := 0; turn < 3; turn++ {
		runtimePrompt := baseline
		if history != "" {
			runtimePrompt = history + "\n\n" + baseline
		}

		w := &recordingSessionWriter{}
		e := newSessionHistoryEngine(t, EngineConfig{
			AgentID:          "agent_default",
			SessionID:        "sess-multi",
			TurnIndex:        turn,
			SystemPrompt:     runtimePrompt,
			BaseSystemPrompt: baseline,
		}, w)
		if _, _, err := e.Run(context.Background(), "第 N 轮输入"); err != nil {
			t.Fatalf("turn %d Run 失败: %v", turn, err)
		}

		persisted, ok := w.roleContent("system")
		if !ok {
			t.Fatalf("turn %d 缺少持久化的 system 消息", turn)
		}
		sizes = append(sizes, len(persisted))

		// 模拟下一轮的历史回灌：仅回灌非 system 消息（对齐 buildHistoryContext）。
		var sb strings.Builder
		sb.WriteString(historyMarker + "\n\n")
		w.mu.Lock()
		for _, m := range w.msgs {
			if m.Role == "system" {
				continue
			}
			sb.WriteString("[" + m.Role + "]: " + m.Content + "\n\n")
		}
		w.mu.Unlock()
		history = sb.String()
	}

	for i, size := range sizes {
		if size != len(baseline) {
			t.Fatalf("turn %d 持久化 system prompt 长度 = %d，期望恒为基线长度 %d（发生膨胀）", i, size, len(baseline))
		}
	}
}
