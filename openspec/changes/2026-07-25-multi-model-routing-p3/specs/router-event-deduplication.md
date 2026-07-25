# Spec: Router Event Deduplication

## 概述

定义 `Router.Select` 产生的事件中哪些需要去重、哪些允许重复。

## 事件分类

| 事件 | 是否去重 | 去重键 | 原因 |
|------|---------|--------|------|
| `intent_classified` | 是 | `taskID + "/" + agentID` | 同一 task 生命周期内 intent 通常不变 |
| `model_routed` | 否 | - | 每次 think 选择模型都可能变化 |
| `model_rate_limited` | 否 | - | 限流状态动态变化，需要每次暴露 |

## 实现

在 `Router` 中增加：

```go
type Router struct {
    // ... 现有字段
    emitted map[string]bool
    emitMu  sync.Mutex
}
```

`SetBroadcaster` 时初始化 `emitted = make(map[string]bool)`。

`emitOnce(key string, eventType string, data map[string]any)`：

1. 检查 `r.broadcaster == nil`，为 nil 直接返回。
2. 上锁后检查 `r.emitted[key]`，已存在返回；否则设置并发送事件。

`intent_classified` 调用：

```go
r.emitOnce(fmt.Sprintf("%s/%s/intent_classified", r.taskID, r.agentID), "intent_classified", data)
```

## 测试要求

- 同一 router、相同 taskID/agentID、连续调用两次 `Select`，`intent_classified` 只产生一次。
- 不同 taskID 或不同 agentID，应产生独立的事件。
- `model_rate_limited` 不受去重影响。
