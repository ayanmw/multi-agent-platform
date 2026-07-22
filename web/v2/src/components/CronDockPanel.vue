<!-- CronDockPanel.vue — 右侧可折叠的 Cron 侧边面板

     功能（只读为主 + 快捷跳转）：
       - 显示与当前 active session 相关的 cron：
         action_payload.session_id == activeSessionId（notify_session / start_task），
         或无 session 绑定的全局 cron
       - 实时触发流：最近 N 条 cron_* 事件（来自 useCronEvents）
       - 每条相关 cron 有"管理"按钮，emit open-manage 让 App 打开 ManageFlyout cron tab

     Props:
       - open: 是否展开
       - sessionId: 当前 active session id

     Emits:
       - update:open
       - open-manage: 请求打开管理大 Dialog 并定位到 cron tab（可选带 cron id）
-->
<script setup lang="ts">
import { computed, watch, onMounted } from 'vue'
import { useCrons } from '@/composables/useCrons'
import { useCronEvents } from '@/composables/useCronEvents'
import type { Cron } from '@/types/cron'
import type { AgentEvent } from '@/types/events'

const props = defineProps<{
  open: boolean
  sessionId: string
}>()

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  (e: 'open-manage', cronId?: string): void
}>()

const { crons, loadCrons } = useCrons()
const { cronEvents } = useCronEvents()

/** 与当前 session 相关的 cron：payload.session_id 命中，或无 session 绑定。 */
const relatedCrons = computed<Cron[]>(() => {
  const sid = props.sessionId
  return crons.value.filter(c => {
    const sid2 = (c.action_payload?.session_id as string | undefined) || ''
    return !sid2 || sid2 === sid
  })
})

/** 最近触发流：取最近 20 条 execution_* / triggered 事件，倒序展示。 */
const recentFlow = computed<AgentEvent[]>(() => {
  const flowTypes = [
    'cron_triggered',
    'cron_execution_started',
    'cron_execution_completed',
    'cron_execution_failed',
    'cron_execution_skipped',
    'cron_missed',
    'cron_notification',
  ]
  return cronEvents.value
    .filter(e => flowTypes.includes(e.type))
    .slice(-20)
    .reverse()
})

onMounted(() => {
  loadCrons().catch(() => {})
})

// session 切换时刷新（相关 cron 集合会变）。
watch(
  () => props.sessionId,
  () => {
    loadCrons().catch(() => {})
  },
)

function close() {
  emit('update:open', false)
}

function openManage(cronId?: string) {
  emit('open-manage', cronId)
}

function statusClass(s: Cron['status']): string {
  return `dock-status--${s}`
}

function eventLabel(type: string): string {
  return type.replace('cron_', '').replace('execution_', '')
}

function eventClass(type: string): string {
  if (type.includes('failed')) return 'flow-event--failed'
  if (type.includes('skipped')) return 'flow-event--skipped'
  if (type.includes('missed')) return 'flow-event--missed'
  if (type.includes('completed')) return 'flow-event--completed'
  return 'flow-event--info'
}
</script>

<template>
  <Transition name="cron-dock">
    <div v-if="open" class="cron-dock">
      <div class="dock-header">
        <span class="dock-title">⏰ Cron</span>
        <div class="dock-actions">
          <button class="dock-btn" title="管理全部定时器" @click="openManage()">管理</button>
          <button class="dock-close" @click="close" title="收起">✕</button>
        </div>
      </div>

      <div class="dock-section">
        <div class="section-label">
          <span>相关定时器</span>
          <span class="section-count">{{ relatedCrons.length }}</span>
        </div>
        <div v-if="relatedCrons.length === 0" class="dock-empty">
          当前 session 无相关定时器。
        </div>
        <ul v-else class="dock-list">
          <li v-for="c in relatedCrons" :key="c.id" class="dock-item">
            <div class="dock-item-main" @click="openManage(c.id)">
              <span class="dock-item-name">{{ c.name }}</span>
              <span class="dock-item-schedule">{{ c.schedule_type }} · {{ c.cron_expr || c.once_at }}</span>
            </div>
            <span class="dock-status" :class="statusClass(c.status)">{{ c.status }}</span>
          </li>
        </ul>
      </div>

      <div class="dock-section dock-flow-section">
        <div class="section-label">
          <span>实时触发流</span>
          <span class="section-count">{{ recentFlow.length }}</span>
        </div>
        <div v-if="recentFlow.length === 0" class="dock-empty">
          暂无触发事件。
        </div>
        <ul v-else class="flow-list">
          <li v-for="ev in recentFlow" :key="ev.event_id" class="flow-item">
            <span class="flow-type" :class="eventClass(ev.type)">{{ eventLabel(ev.type) }}</span>
            <span class="flow-cron">{{ ev.task_id }}</span>
          </li>
        </ul>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.cron-dock {
  display: flex; flex-direction: column;
  height: 100%; gap: var(--space-sm);
  padding: var(--space-sm);
  background: var(--bg-panel);
  border-left: 1px solid var(--border-default);
  overflow: hidden;
  font-family: var(--font-mono);
}
.dock-header {
  display: flex; align-items: center; justify-content: space-between;
  padding-bottom: var(--space-sm);
  border-bottom: 1px solid var(--border-default);
  flex-shrink: 0;
}
.dock-title {
  font-family: var(--font-display); font-size: 0.8rem; font-weight: 600;
  color: var(--text-primary); text-transform: uppercase; letter-spacing: 0.04em;
}
.dock-actions { display: flex; gap: 6px; align-items: center; }
.dock-btn {
  background: var(--bg-elevated); border: 1px solid var(--border-default);
  border-radius: var(--radius-sm); color: var(--accent-running);
  font-family: var(--font-display); font-size: 0.66rem; font-weight: 600;
  padding: 0.15rem 0.5rem; cursor: pointer;
  transition: border-color 0.15s;
}
.dock-btn:hover { border-color: var(--accent-running); }
.dock-close {
  background: none; border: none; color: var(--text-muted);
  cursor: pointer; font-size: 0.9rem; padding: 0 4px;
}
.dock-close:hover { color: var(--text-primary); }
.dock-section { display: flex; flex-direction: column; gap: var(--space-xs); min-height: 0; }
.dock-flow-section { flex: 1; min-height: 0; }
.section-label {
  display: flex; align-items: center; justify-content: space-between;
  font-family: var(--font-display); font-size: 0.62rem; font-weight: 600;
  text-transform: uppercase; letter-spacing: 0.04em; color: var(--text-muted);
}
.section-count {
  font-family: var(--font-mono); font-size: 0.6rem;
  color: var(--text-muted); background: var(--bg-elevated);
  padding: 1px 6px; border-radius: 8px;
}
.dock-empty {
  font-size: 0.7rem; color: var(--text-muted);
  padding: var(--space-sm); text-align: center;
}
.dock-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; overflow-y: auto; max-height: 40%; }
.dock-item {
  display: flex; align-items: center; justify-content: space-between; gap: 6px;
  padding: 0.4rem 0.5rem; border-radius: var(--radius-sm);
  background: var(--bg-elevated); border: 1px solid var(--border-subtle);
}
.dock-item-main { display: flex; flex-direction: column; gap: 1px; min-width: 0; cursor: pointer; }
.dock-item-main:hover .dock-item-name { color: var(--accent-running); }
.dock-item-name { font-size: 0.72rem; color: var(--text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dock-item-schedule { font-size: 0.62rem; color: var(--text-muted); }
.dock-status {
  font-size: 0.58rem; font-weight: 600; padding: 1px 5px; border-radius: 5px;
  color: var(--text-muted); border: 1px solid var(--border-subtle); flex-shrink: 0;
}
.dock-status--enabled { color: var(--accent-success, #00e676); border-color: rgba(57,255,20,0.25); }
.dock-status--paused { color: var(--accent-warning, #ffab00); border-color: rgba(255,171,0,0.25); }
.dock-status--disabled { color: var(--text-muted); }
.flow-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 3px; overflow-y: auto; flex: 1; }
.flow-item {
  display: flex; align-items: center; gap: 6px;
  padding: 0.25rem 0.4rem; font-size: 0.66rem;
  border-radius: var(--radius-sm); background: var(--bg-elevated);
}
.flow-type {
  font-weight: 600; padding: 1px 5px; border-radius: 4px;
  font-size: 0.58rem; flex-shrink: 0;
  color: var(--text-muted); border: 1px solid var(--border-subtle);
}
.flow-event--completed { color: var(--accent-success, #00e676); border-color: rgba(57,255,20,0.25); }
.flow-event--failed { color: var(--accent-danger, #ff5252); border-color: rgba(255,77,77,0.25); }
.flow-event--skipped, .flow-event--missed { color: var(--accent-warning, #ffab00); border-color: rgba(255,171,0,0.25); }
.flow-event--info { color: var(--accent-running, #00e5ff); border-color: rgba(0,229,255,0.25); }
.flow-cron { color: var(--text-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.cron-dock-enter-active, .cron-dock-leave-active { transition: opacity 0.18s ease, transform 0.18s ease; }
.cron-dock-enter-from, .cron-dock-leave-to { opacity: 0; transform: translateX(8px); }
</style>
