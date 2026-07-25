<!-- RoutingPanel.vue — Inspector 中的多模型路由决策面板

     展示当前 active task / session 的路由事件流：
       - intent 分类结果（primary/secondary intents、confidence、needs_tools）
       - model_routed 主模型与 fallback
       - model_fallback_used / model_rate_limited / cost_budget_exceeded 状态

     Props:
       - taskId: 当前 active task id（空字符串时展示全部事件）
-->
<script setup lang="ts">
import { computed } from 'vue'
import { useRouteEvents } from '@/composables/useRouteEvents'
import type { AgentEvent } from '@/types/events'

const props = defineProps<{
  taskId: string
}>()

const { routeEvents, decisionsByTask } = useRouteEvents()

const filteredEvents = computed<AgentEvent[]>(() => {
  if (!props.taskId) return routeEvents.value
  return routeEvents.value.filter(
    e => e.task_id === props.taskId || e.sub_task_id === props.taskId
  )
})

const currentDecision = computed(() => {
  const decisions = Object.values(decisionsByTask.value)
  return decisions.find(d => d.taskId === props.taskId || d.subTaskId === props.taskId)
})

function eventClass(type: string): string {
  switch (type) {
    case 'model_routed': return 'routing-event--routed'
    case 'intent_classified': return 'routing-event--intent'
    case 'model_fallback_used': return 'routing-event--fallback'
    case 'model_rate_limited': return 'routing-event--rate-limited'
    case 'cost_budget_exceeded': return 'routing-event--budget'
    default: return 'routing-event--default'
  }
}

function eventLabel(type: string): string {
  return type.replace(/_/g, ' ')
}

function formatTime(ts: number): string {
  const d = new Date(ts)
  return d.toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
</script>

<template>
  <div class="routing-panel">
    <div class="routing-header">
      <h3 class="panel-title">Routing Decision</h3>
      <span class="event-count">{{ filteredEvents.length }}</span>
    </div>

    <div v-if="currentDecision" class="routing-decision">
      <div class="decision-row">
        <span class="decision-label">Intent</span>
        <span class="decision-value decision-value--accent">{{ currentDecision.intent || '-' }}</span>
      </div>
      <div class="decision-row">
        <span class="decision-label">Model</span>
        <span class="decision-value">{{ currentDecision.model || '-' }}</span>
      </div>
      <div class="decision-row">
        <span class="decision-label">Tier</span>
        <span class="decision-value tier-badge">{{ currentDecision.tier || '-' }}</span>
      </div>
      <div v-if="currentDecision.fallback" class="decision-row">
        <span class="decision-label">Fallback</span>
        <span class="decision-value">{{ currentDecision.fallback }}</span>
      </div>
      <div v-if="currentDecision.reason" class="decision-reason">
        {{ currentDecision.reason }}
      </div>
    </div>

    <div v-if="filteredEvents.length === 0" class="routing-empty">
      暂无路由事件。运行一个 task 后，Router 会在这里展示 intent 分类与模型选择。
    </div>
    <ul v-else class="routing-list">
      <li v-for="ev in filteredEvents" :key="ev.event_id" class="routing-item">
        <div class="routing-item-header">
          <span class="routing-type" :class="eventClass(ev.type)">{{ eventLabel(ev.type) }}</span>
          <span class="routing-time">{{ formatTime(ev.timestamp) }}</span>
        </div>
        <div class="routing-item-body">
          <template v-if="ev.type === 'intent_classified'">
            <span class="routing-detail"><strong>{{ ev.data.primary_intent }}</strong></span>
            <span v-if="ev.data.confidence !== undefined" class="routing-detail muted">
              confidence {{ Number(ev.data.confidence).toFixed(2) }}
            </span>
            <span v-if="Array.isArray(ev.data.needs_tools) && ev.data.needs_tools.length" class="routing-detail muted">
              tools: {{ ev.data.needs_tools.join(', ') }}
            </span>
          </template>
          <template v-else-if="ev.type === 'model_routed'">
            <span class="routing-detail"><strong>{{ ev.data.model }}</strong> ({{ ev.data.tier }})</span>
            <span v-if="ev.data.fallback" class="routing-detail muted">fallback {{ ev.data.fallback }}</span>
          </template>
          <template v-else-if="ev.type === 'model_fallback_used'">
            <span class="routing-detail">{{ ev.data.primary }} → <strong>{{ ev.data.fallback }}</strong></span>
            <span class="routing-detail muted">{{ ev.data.reason }}</span>
          </template>
          <template v-else-if="ev.type === 'model_rate_limited'">
            <span class="routing-detail">{{ ev.data.model }} <span class="muted">{{ ev.data.reason }}</span></span>
          </template>
          <template v-else-if="ev.type === 'cost_budget_exceeded'">
            <span class="routing-detail">${{ Number(ev.data.current_cost_usd || 0).toFixed(6) }} / ${{ Number(ev.data.max_cost_usd || 0).toFixed(6) }}</span>
            <span class="routing-detail muted">{{ ev.data.reason }}</span>
          </template>
          <template v-else>
            <span class="routing-detail muted">{{ JSON.stringify(ev.data) }}</span>
          </template>
        </div>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.routing-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: var(--space-md);
  gap: var(--space-sm);
  overflow: hidden;
}
.routing-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-shrink: 0;
}
.panel-title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 0.85rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-primary);
}
.event-count {
  font-family: var(--font-mono);
  font-size: 0.65rem;
  color: var(--text-muted);
  background: var(--bg-elevated);
  padding: 2px 6px;
  border-radius: 8px;
}
.routing-decision {
  display: flex;
  flex-direction: column;
  gap: var(--space-xs);
  padding: var(--space-sm);
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
  flex-shrink: 0;
}
.decision-row {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  font-size: 0.75rem;
}
.decision-label {
  width: 4rem;
  color: var(--text-muted);
  font-family: var(--font-display);
  text-transform: uppercase;
  font-size: 0.62rem;
  letter-spacing: 0.04em;
}
.decision-value {
  color: var(--text-primary);
  font-family: var(--font-mono);
}
.decision-value--accent {
  color: var(--accent-running);
}
.tier-badge {
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  font-size: 0.62rem;
  text-transform: uppercase;
}
.decision-reason {
  margin-top: var(--space-xs);
  font-size: 0.68rem;
  color: var(--text-muted);
  line-height: 1.4;
  word-break: break-word;
}
.routing-empty {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 0.75rem;
  color: var(--text-muted);
  text-align: center;
  padding: var(--space-lg);
}
.routing-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-xs);
  overflow-y: auto;
  flex: 1;
}
.routing-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: var(--space-sm);
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-md);
}
.routing-item-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-sm);
}
.routing-type {
  font-family: var(--font-display);
  font-size: 0.6rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  padding: 2px 6px;
  border-radius: 4px;
  border: 1px solid var(--border-default);
  color: var(--text-muted);
}
.routing-event--routed { color: var(--accent-running); border-color: rgba(0,229,255,0.25); }
.routing-event--intent { color: var(--accent-success); border-color: rgba(57,255,20,0.25); }
.routing-event--fallback { color: var(--accent-warning); border-color: rgba(255,171,0,0.25); }
.routing-event--rate-limited { color: var(--accent-danger); border-color: rgba(255,77,77,0.25); }
.routing-event--budget { color: var(--accent-danger); border-color: rgba(255,77,77,0.25); }
.routing-time {
  font-family: var(--font-mono);
  font-size: 0.6rem;
  color: var(--text-muted);
}
.routing-item-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.routing-detail {
  font-size: 0.72rem;
  color: var(--text-primary);
  word-break: break-word;
}
.routing-detail.muted {
  color: var(--text-muted);
}
</style>
