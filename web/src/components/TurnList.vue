<!-- TurnList.vue — multi-turn conversation timeline container
     Renders a list of TurnItem components, each representing one
     conversation turn in the current session.

     Default behavior: the latest turn is expanded, historical turns
     are collapsed so the user can see the timeline at a glance.

     Used by: App.vue (replaces direct AgentTree rendering)
-->
<script setup lang="ts">
import type { TaskState } from '../types/events'
import TurnItem from './TurnItem.vue'

const props = defineProps<{
  turns: Array<{
    task: TaskState
    userInput: string
  }>
}>()

// Default expand the last turn (if there are multiple)
const defaultExpandedTurn = props.turns.length > 0 ? props.turns.length - 1 : 0
</script>

<template>
  <div class="turn-list">
    <div v-if="turns.length === 0" class="turn-list-empty">
      No conversation turns yet. Send a message to start.
    </div>
    <TurnItem
      v-for="(turn, idx) in turns"
      :key="turn.task.id"
      :task="turn.task"
      :turn-index="idx"
      :user-input="turn.userInput"
      :is-default-expanded="idx === defaultExpandedTurn"
    />
  </div>
</template>

<style scoped>
.turn-list {
  /* Container for the timeline — no extra styling needed */
}

.turn-list-empty {
  text-align: center;
  padding: 40px 20px;
  color: #888;
  font-size: 13px;
}
</style>