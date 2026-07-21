// events.go — cron_* 事件构造 helper。
//
// 事件常量定义在 pkg/event；本文件提供统一的事件构造函数，确保所有 cron 事件
// 的字段约定一致：TaskID 填 cron_id，AgentID 填触发 agent_id 或 "cron"。
package cron

import (
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// newCronEvent 构造一个 cron 事件。taskID 填 cron_id，agentID 填触发者。
func newCronEvent(eventType, cronID, agentID string, data map[string]any) event.Event {
	if data == nil {
		data = make(map[string]any)
	}
	if _, ok := data["cron_id"]; !ok && cronID != "" {
		data["cron_id"] = cronID
	}
	e := event.NewEvent(eventType, cronID, agentID, 0, data)
	return e
}
