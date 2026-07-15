<!-- MemoryEventsTimeline.vue — renders a chronological timeline of memory-related
     AgentEvents so that every RAG/memory operation is visible in the UI.
-->
<script setup lang="ts">
import { computed } from 'vue'
import type { AgentEvent, EventType } from '../types/events'

const props = defineProps<{
  events: AgentEvent[]
}>()

const emit = defineEmits<{
  (e: 'select-memory', id: string): void
}>()

const memoryEventTypes: EventType[] = [
  'memory_created',
  'memory_updated',
  'memory_deleted',
  'memory_promoted',
  'memory_summarize_started',
  'memory_summarize_completed',
  'memory_summarize_failed',
  'memory_recall_performed',
  'heartbeat_beat',
]

const colorMap: Record<string, string> = {
  memory_created: '#2ecc71',
  memory_updated: '#3498db',
  memory_deleted: '#e74c3c',
  memory_promoted: '#9b59b6',
  memory_summarize_started: '#f1c40f',
  memory_summarize_completed: '#f1c40f',
  memory_summarize_failed: '#e67e22',
  heartbeat_beat: '#7f8c8d',
  memory_recall_performed: '#1abc9c',
}

const iconMap: Record<string, string> = {
  memory_created: '+',
  memory_updated: '↻',
  memory_deleted: '×',
  memory_promoted: '↑',
  memory_summarize_started: '◌',
  memory_summarize_completed: '✓',
  memory_summarize_failed: '!',
  heartbeat_beat: '♥',
  memory_recall_performed: '🔍',
}

const sortedEvents = computed(() => {
  return [...props.events]
    .filter(e => memoryEventTypes.includes(e.type))
    .sort((a, b) => b.timestamp - a.timestamp)
})

function formatTime(ts: number): string {
  return new Date(ts).toLocaleTimeString()
}

function formatType(type: string): string {
  return type.replace(/_/g, ' ')
}

function getMemoryId(event: AgentEvent): string | undefined {
  const d = event.data || {}
  return (d.memory_id as string) || (d.id as string)
}

function getSummary(event: AgentEvent): string {
  const d = event.data || {}
  if (typeof d.summary === 'string') return d.summary
  if (typeof d.content === 'string') return d.content.slice(0, 80)
  if (typeof d.query === 'string') return d.query.slice(0, 80)
  return formatType(event.type)
}

function handleMemoryClick(id: string | undefined) {
  if (id) emit('select-memory', id)
}
</script>

<template>
  <div class="timeline-panel">
    <div class="timeline-header">
      <h3 class="timeline-title">Memory Events</h3>
      <span class="timeline-count">{{ sortedEvents.length }}</span>
    </div>

    <div v-if="sortedEvents.length === 0" class="timeline-empty">
      No memory events yet.
    </div>

    <ul v-else class="timeline-list">
      <li
        v-for="event in sortedEvents"
        :key="event.event_id"
        class="timeline-item"
      >
        <div
          class="timeline-dot"
          :style="{ background: colorMap[event.type] || '#999', boxShadow: `0 0 8px ${colorMap[event.type] || '#999'}40` }"
        >
          <span class="timeline-icon">{{ iconMap[event.type] || '•' }}</span>
        </div>
        <div class="timeline-body">
          <div class="timeline-meta">
            <span class="timeline-type" :style="{ color: colorMap[event.type] || '#aaa' }">
              {{ formatType(event.type) }}
            </span>
            <span class="timeline-time">{{ formatTime(event.timestamp) }}</span>
          </div>
          <div class="timeline-summary">{{ getSummary(event) }}</div>
          <div v-if="getMemoryId(event)" class="timeline-memory-id">
            memory:
            <button
              class="id-link"
              @click="handleMemoryClick(getMemoryId(event))"
            >
              {{ getMemoryId(event) }}
            </button>
          </div>
        </div>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.timeline-panel {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  height: 100%;
  overflow: hidden;
}

.timeline-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-shrink: 0;
}

.timeline-title {
  margin: 0;
  font-size: 15px;
  color: #e0e0e0;
}

.timeline-count {
  padding: 2px 8px;
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 10px;
  font-size: 11px;
  color: #aaa;
}

.timeline-empty {
  color: #888;
  font-size: 13px;
  text-align: center;
  padding: 40px 12px;
}

.timeline-list {
  list-style: none;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.timeline-item {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 10px;
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 8px;
}

.timeline-dot {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: #fff;
  font-size: 11px;
  font-weight: 700;
}

.timeline-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.timeline-meta {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.timeline-type {
  font-size: 12px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.timeline-time {
  font-size: 11px;
  color: #666;
}

.timeline-summary {
  font-size: 12px;
  color: #ccc;
  line-height: 1.4;
  word-break: break-word;
}

.timeline-memory-id {
  font-size: 11px;
  color: #666;
  font-family: var(--font-mono, monospace);
}

.id-link {
  background: none;
  border: none;
  color: #4a9eff;
  cursor: pointer;
  font-family: inherit;
  font-size: inherit;
  padding: 0;
  text-decoration: underline;
}

.id-link:hover {
  color: #7fbf7f;
}
</style>
