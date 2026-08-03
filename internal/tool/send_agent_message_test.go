package tool

import (
	"testing"
)

// fakeAgentMessageSender 记录所有经 SendAgentMessageTo 转发的调用，用于断言
// send_agent_message 工具把 LLM 的输入正确转交给了底层 sender（N1-02 工具单测）。
type fakeAgentMessageSender struct {
	calls        []agentMsgCall
	nextResponse bool
}

type agentMsgCall struct {
	toAgentID string
	subTaskID string
	msgType   string
	content   string
}

func (f *fakeAgentMessageSender) SendAgentMessageTo(toAgentID, subTaskID, msgType, content string) bool {
	f.calls = append(f.calls, agentMsgCall{toAgentID, subTaskID, msgType, content})
	return f.nextResponse
}

// TestSendAgentMessageToolValidForward 验证合法输入被转发给 sender，且返回的
// delivered 标记与转发参数完全对应。
func TestSendAgentMessageToolValidForward(t *testing.T) {
	sender := &fakeAgentMessageSender{nextResponse: true}
	tool := NewSendAgentMessageTool(sender)

	res, err := tool.Execute(map[string]any{
		"to_agent_id": "agent_writer",
		"sub_task_id": "root_agent_writer",
		"msg_type":    "request",
		"content":     "请整理这段代码的单元测试",
	})
	if err != nil {
		t.Fatalf("合法调用返回错误: %v", err)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("sender 收到 %d 次调用，期望 1", len(sender.calls))
	}
	c := sender.calls[0]
	if c.toAgentID != "agent_writer" || c.subTaskID != "root_agent_writer" || c.msgType != "request" || c.content != "请整理这段代码的单元测试" {
		t.Fatalf("转发参数错误: %+v", c)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("返回类型 = %T，期望 map", res)
	}
	if m["delivered"] != true {
		t.Errorf("delivered = %v，期望 true", m["delivered"])
	}
	if m["to_agent_id"] != "agent_writer" {
		t.Errorf("to_agent_id = %v，期望 agent_writer", m["to_agent_id"])
	}
}

// TestSendAgentMessageToolDefaults 验证缺省字段的稳妥处理：省略 sub_task_id
// 与 msg_type 时分别回退为空串与 "request"。
func TestSendAgentMessageToolDefaults(t *testing.T) {
	sender := &fakeAgentMessageSender{nextResponse: true}
	tool := NewSendAgentMessageTool(sender)

	if _, err := tool.Execute(map[string]any{
		"to_agent_id": "leader",
		"content":     "纯文本观察",
	}); err != nil {
		t.Fatalf("缺省字段调用返回错误: %v", err)
	}
	c := sender.calls[0]
	if c.subTaskID != "" {
		t.Errorf("sub_task_id = %q，期望空串（缺省）", c.subTaskID)
	}
	if c.msgType != "request" {
		t.Errorf("msg_type = %q，期望 request（缺省）", c.msgType)
	}
}

// TestSendAgentMessageToolRejected 验证 sender 返回 false（目标不可达 / bus 禁用）
// 时，工具返回 delivered=false 且附 reason，而非报错。
func TestSendAgentMessageToolRejected(t *testing.T) {
	sender := &fakeAgentMessageSender{nextResponse: false}
	tool := NewSendAgentMessageTool(sender)

	res, err := tool.Execute(map[string]any{
		"to_agent_id": "ghost_agent",
		"content":     "致不存在的 agent",
	})
	if err != nil {
		t.Fatalf("被拒投递应返回结果而非错误: %v", err)
	}
	m := res.(map[string]any)
	if m["delivered"] != false {
		t.Errorf("delivered = %v，期望 false", m["delivered"])
	}
	if m["reason"] == nil || m["reason"] == "" {
		t.Errorf("被拒投递应带 reason 说明")
	}
}

// TestSendAgentMessageToolValidation 验证字段校验：缺 to_agent_id / 缺 content /
// 非法 msg_type 都必须报错。
func TestSendAgentMessageToolValidation(t *testing.T) {
	cases := []map[string]any{
		{"content": "无目标"},                      // 缺 to_agent_id
		{"to_agent_id": "leader"},                  // 缺 content
		{"to_agent_id": "leader", "content": "x", "msg_type": "chat"}, // 非法 msg_type
	}
	for i, input := range cases {
		tool := NewSendAgentMessageTool(&fakeAgentMessageSender{nextResponse: true})
		if _, err := tool.Execute(input); err == nil {
			t.Fatalf("用例 %d 应通过校验报错，却成功返回", i)
		}
	}
}

// TestSendAgentMessageToolNilSender 验证 sender 为 nil 时返回明确错误，
// 避免工具在 agent 未持有 AgentBus 的异常路径下静默失败。
func TestSendAgentMessageToolNilSender(t *testing.T) {
	tool := NewSendAgentMessageTool(nil)
	if _, err := tool.Execute(map[string]any{
		"to_agent_id": "leader",
		"content":     "x",
	}); err == nil {
		t.Fatalf("nil sender 应返回错误")
	}
}
