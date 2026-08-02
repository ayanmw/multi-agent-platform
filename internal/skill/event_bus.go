package skill

import (
	"github.com/ayanmw/multi-agent-platform/pkg/event"
)

// eventBroadcaster 是 EventBus 所需的最小发送接口。
// 与 cmd/server/api_skill.go 中的同名接口保持一致，避免重复定义。
type eventBroadcaster interface {
	SendEvent(evt event.Event)
}

// SkillEventBus 把文件系统扫描的内部事件适配为 pkg/event.Event 并发送到 ws.Hub。
// 它实现 FileLoader.EventBus 接口，将 TaskID 设为 sessionID 或 "global"，
// AgentID 固定为 "skill-loader"，让前端能统一订阅 skill_* 事件。
type SkillEventBus struct {
	hub eventBroadcaster
}

// NewSkillEventBus 创建 SkillEventBus。hub 为 nil 时所有广播静默跳过。
func NewSkillEventBus(hub eventBroadcaster) *SkillEventBus {
	return &SkillEventBus{hub: hub}
}

// SendEvent 实现 FileLoader 所需的最小 EventBus 接口。
// data 中应携带 id / source / state 等字段。
func (b *SkillEventBus) SendEvent(data map[string]any) {
	if b.hub == nil || data == nil {
		return
	}
	eventType, _ := data["event_type"].(string)
	if eventType == "" {
		return
	}
	skillID, _ := data["id"].(string)
	taskID, _ := data["task_id"].(string)
	if taskID == "" {
		taskID = globalSkillTaskID
	}
	// 克隆 data 避免修改调用方 map。
	payload := make(map[string]any, len(data)+1)
	for k, v := range data {
		payload[k] = v
	}
	payload["skill_id"] = skillID
	b.hub.SendEvent(event.NewEvent(eventType, taskID, "skill-loader", 0, payload))
}
