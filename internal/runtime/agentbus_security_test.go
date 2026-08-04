package runtime

import (
	"sync"
	"testing"
)

// recordingAgentBusAuditSink 是测试用的 AgentBus 审计接收器，记录所有
// 经 Record 写入的裁决，以便断言越权发送被拒事件确实落审计（N3-03）。
type recordingAgentBusAuditSink struct {
	mu   sync.Mutex
	recs []map[string]any
}

func (s *recordingAgentBusAuditSink) Record(action, actor, target, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, map[string]any{
		"action": action,
		"actor":  actor,
		"target": target,
		"reason": reason,
	})
}

func (s *recordingAgentBusAuditSink) has(action, target string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.recs {
		if r["action"] == action && r["target"] == target {
			return true
		}
	}
	return false
}

// TestCanSendToBusMatrix 验证 C1-2 通信权限矩阵（N3-03，E4 通信信任边界）。
// 矩阵口径：自身/监督者(leader)始终允许；白名单(OutputTo)内允许；领导者
// 可寻址任意 child；worker 向未声明 OutputTo 的其它 child 拒绝。
func TestCanSendToBusMatrix(t *testing.T) {
	cases := []struct {
		name        string
		agentID     string
		role        AgentRole
		canDispatch bool
		allowed     []string
		supervisor  string
		to          string
		want        bool
	}{
		// 发给自身始终允许。
		{"self", "a", AgentRoleWorker, false, nil, "leader", "a", true},
		// 监督者(leader)始终可达——worker 上报 / 委托审批刚需路径。
		{"worker-to-supervisor", "a", AgentRoleWorker, false, nil, "leader", "leader", true},
		{"worker-to-custom-supervisor", "a", AgentRoleWorker, false, nil, "mgr", "mgr", true},
		// 白名单(OutputTo)内允许——worker→child 经声明的数据流。
		{"worker-to-outputto", "a", AgentRoleWorker, false, []string{"b"}, "leader", "b", true},
		// 领导者可寻址任意 child。
		{"leader-to-any-child", "a", AgentRoleLeader, true, nil, "leader", "b", true},
		// worker 向未声明 OutputTo 的其它 child 拒绝。
		{"worker-to-undeclared-child", "a", AgentRoleWorker, false, []string{"b"}, "leader", "c", false},
		{"worker-to-undeclared-child-empty-allow", "a", AgentRoleWorker, false, nil, "leader", "c", false},
		// 空目标拒绝。
		{"empty-target", "a", AgentRoleLeader, true, nil, "leader", "", false},
	}

	for _, tc := range cases {
		e := &Engine{cfg: EngineConfig{
			AgentID:             tc.agentID,
			Role:                tc.role,
			CanDispatchSubAgents: tc.canDispatch,
			AllowedSendTargets:  tc.allowed,
			SupervisorAgentID:   tc.supervisor,
		}}
		got, _ := e.canSendToBus(tc.to)
		if got != tc.want {
			t.Errorf("%s: canSendToBus(%q) = %v，期望 %v", tc.name, tc.to, got, tc.want)
		}
	}
}

// TestSendAgentMessageToDeniedWhenUnauthorized 验证越权发送在发送侧即被拒：
// 不进入 AgentBus 队列、不发射 agent_message_sent 事件、写审计（N3-03）。
func TestSendAgentMessageToDeniedWhenUnauthorized(t *testing.T) {
	sink := &recordingAgentBusAuditSink{}
	SetAgentBusAuditSink(sink)
	defer SetAgentBusAuditSink(newDefaultAgentBusAuditSink())

	agBus := &recordingAgentBus{}
	evBus := &recordingBus{}
	engine := &Engine{
		cfg: EngineConfig{
			AgentID:            "agent_writer",
			SubTaskID:          "root_agent_writer",
			Role:               AgentRoleWorker,
			AllowedSendTargets: []string{"agent_reviewer"},
			SupervisorAgentID:  DefaultSupervisorAgentID,
		},
		agentBus: agBus,
		bus:      evBus,
	}

	// 越权：worker 向未声明 OutputTo 的其它 child 发送 → 拒绝 + 审计。
	if ok := engine.sendAgentMessageTo("agent_intruder", "", "request", "越权消息"); ok {
		t.Fatalf("越权发送应被拒绝（返回 false），实际 true")
	}
	if got := len(agBus.messages()); got != 0 {
		t.Fatalf("越权消息不应进入 AgentBus，实际 %d 条", got)
	}
	if !sink.has("agentbus_send_denied", "agent/agent_intruder") {
		t.Fatalf("越权发送应写审计 agentbus_send_denied@agent/agent_intruder，实际 %v", sink.recs)
	}
	for _, ev := range evBus.events {
		if ev.Type == "system_info" && ev.Data["type"] == "agent_message_sent" {
			t.Fatalf("越权消息不应发射 agent_message_sent 事件")
		}
	}

	// 声明过的对等体（OutputTo）允许 → 进入 bus。
	if ok := engine.sendAgentMessageTo("agent_reviewer", "", "observation", "已声明"); !ok {
		t.Fatalf("向 OutputTo 声明目标发送应成功，实际 false")
	}
	if got := len(agBus.messages()); got != 1 {
		t.Fatalf("声明目标发送应进入 AgentBus 1 条，实际 %d 条", got)
	}

	// 监督者(leader)允许 → 进入 bus。
	if ok := engine.sendAgentMessageTo(DefaultSupervisorAgentID, "root", "approval_request", "上报"); !ok {
		t.Fatalf("向监督者发送应成功，实际 false")
	}
	if got := len(agBus.messages()); got != 2 {
		t.Fatalf("监督者发送应再进入 AgentBus 1 条，实际共 %d 条", got)
	}
}

// TestClassifyAgentMessageTrust 验证接收侧来源可信度标记（N3-03，E4）。
func TestClassifyAgentMessageTrust(t *testing.T) {
	cases := []struct {
		name    string
		agentID string
		role    AgentRole
		allowed []string
		from    string
		want    string
	}{
		{"self", "a", AgentRoleWorker, nil, "a", "self"},
		{"supervisor-controlled", "a", AgentRoleWorker, nil, "leader", "controlled"},
		{"outputto-controlled", "a", AgentRoleWorker, []string{"b"}, "b", "controlled"},
		{"leader-trusts-child", "a", AgentRoleLeader, nil, "b", "controlled"},
		{"worker-untrusted-peer", "a", AgentRoleWorker, nil, "b", "untrusted"},
	}
	for _, tc := range cases {
		e := &Engine{cfg: EngineConfig{
			AgentID:            tc.agentID,
			Role:               tc.role,
			AllowedSendTargets: tc.allowed,
			SupervisorAgentID:  DefaultSupervisorAgentID,
		}}
		if got := e.classifyAgentMessageTrust(tc.from); got != tc.want {
			t.Errorf("%s: trust(%q) = %q，期望 %q", tc.name, tc.from, got, tc.want)
		}
	}
}
