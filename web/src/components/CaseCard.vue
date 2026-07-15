<!-- CaseCard — displays a preset task case with one-click run button
     Props:
       caseData: the Case object from the API
       disabled: whether the button is disabled (during task execution)

     Emits:
       run: user clicked the Run button, emits the case ID
       view: user clicked the card body (not the Run button), emits the case ID
       toggle-tag: user clicked a tag pill, emits the tag value
       edit: user clicked the edit button, emits the case ID
       delete: user clicked the delete button, emits the case ID

     Behavior:
       - Clicking the card body (excluding the Run button) emits 'view' for detail modal
       - Clicking the Run button emits 'run' directly
       - Clicking a tag emits 'toggle-tag' instead of 'view'
       - Built-in cases hide edit/delete actions and show a builtin badge
-->
<script setup lang="ts">
import type { Case } from '../types/case'

defineProps<{
  caseData: Case
  disabled: boolean
}>()

const emit = defineEmits<{
  run: [caseId: string]
  view: [caseId: string]
  'toggle-tag': [tag: string]
  edit: [caseId: string]
  delete: [caseId: string]
}>()

/** Handle tag click without triggering the card view event */
function handleTagClick(tag: string, event: MouseEvent) {
  event.stopPropagation()
  emit('toggle-tag', tag)
}
</script>

<template>
  <div class="case-card" @click="emit('view', caseData.id)">
    <div class="case-card-header">
      <span class="case-icon">{{ caseData.icon }}</span>
      <div class="case-card-title">
        <div class="case-title-row">
          <h3>{{ caseData.name }}</h3>
          <span v-if="caseData.is_builtin" class="builtin-badge">builtin</span>
        </div>
        <span class="case-category">{{ caseData.category }}</span>
      </div>
    </div>
    <p class="case-description">{{ caseData.description }}</p>
    <div class="case-card-footer">
      <div class="case-tags">
        <span
          v-for="tag in caseData.tags"
          :key="tag"
          class="case-tag"
          @click.stop="handleTagClick(tag, $event)"
        >
          {{ tag }}
        </span>
      </div>
      <div class="case-actions">
        <button
          v-if="!caseData.is_builtin"
          class="case-action-btn edit"
          title="Edit"
          @click.stop="emit('edit', caseData.id)"
        >
          ✎
        </button>
        <button
          v-if="!caseData.is_builtin"
          class="case-action-btn delete"
          title="Delete"
          @click.stop="emit('delete', caseData.id)"
        >
          🗑
        </button>
        <button
          class="case-run-btn"
          :disabled="disabled"
          @click.stop="emit('run', caseData.id)"
        >
          ▶ Run
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.case-card {
  background: #252525;
  border: 1px solid #333;
  border-radius: 8px;
  padding: 14px;
  transition: border-color 0.2s, background 0.2s;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.case-card:hover {
  border-color: #4a9eff;
}

.case-card-header {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.case-icon {
  font-size: 24px;
  flex-shrink: 0;
  line-height: 1;
}

.case-card-title {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}

.case-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.case-card-title h3 {
  font-size: 14px;
  font-weight: 600;
  color: #e0e0e0;
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.case-category {
  font-size: 10px;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.builtin-badge {
  font-size: 9px;
  color: #aaa;
  background: #333;
  border: 1px solid #444;
  padding: 1px 5px;
  border-radius: 8px;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.case-description {
  font-size: 12px;
  color: #999;
  line-height: 1.5;
  margin: 0;
  flex: 1;
}

.case-card-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 4px;
  gap: 8px;
}

.case-tags {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}

.case-tag {
  font-size: 10px;
  color: #888;
  background: #333;
  padding: 1px 6px;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.case-tag:hover {
  background: #3a3a3a;
  color: #4a9eff;
}

.case-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.case-action-btn {
  background: transparent;
  border: none;
  font-size: 13px;
  cursor: pointer;
  padding: 4px;
  border-radius: 4px;
  opacity: 0.6;
  transition: opacity 0.15s, background 0.15s;
}

.case-action-btn:hover {
  opacity: 1;
  background: #333;
}

.case-action-btn.edit:hover {
  color: #4a9eff;
}

.case-action-btn.delete:hover {
  color: #e74c3c;
}

.case-run-btn {
  padding: 6px 16px;
  background: #4a9eff;
  color: #fff;
  border: none;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
  white-space: nowrap;
  flex-shrink: 0;
}

.case-run-btn:hover:not(:disabled) {
  background: #3a8eef;
}

.case-run-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
</style>