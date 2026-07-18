<script setup lang="ts">
import type { AgentBusEventData } from '../types/events'

const props = defineProps<{
  messages: AgentBusEventData[]
}>()
</script>

<template>
  <div class="agent-bus-timeline">
    <div
      v-for="(msg, idx) in messages"
      :key="idx"
      class="bus-message"
      :class="[msg.type]"
    >
      <div class="bus-header">
        <span class="bus-from">{{ msg.from_agent }}</span>
        <span class="bus-arrow">→</span>
        <span class="bus-to">{{ msg.to_agent }}</span>
        <span class="bus-type">{{ msg.msg_type }}</span>
      </div>
      <pre class="bus-content">{{ msg.content }}</pre>
    </div>
  </div>
</template>

<style scoped>
.agent-bus-timeline {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px;
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 6px;
}

.bus-message {
  padding: 8px 10px;
  border-radius: 4px;
  border-left: 3px solid #4a9eff;
  background: #252525;
}

.bus-message.agent_message_received {
  border-left-color: #51cf66;
}

.bus-header {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  margin-bottom: 4px;
}

.bus-from,
.bus-to {
  font-weight: 600;
  color: #d4d4d4;
}

.bus-arrow {
  color: #888;
}

.bus-type {
  margin-left: auto;
  padding: 1px 6px;
  border-radius: 3px;
  background: #333;
  color: #aaa;
  font-size: 11px;
}

.bus-content {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 12px;
  color: #bbb;
}
</style>
