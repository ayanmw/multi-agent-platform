<!-- CaseCard — displays a preset task case with one-click run button
     Props:
       case: the Case object from the API
       disabled: whether the button is disabled (during task execution)

     Emits:
       run: user clicked the Run button, emits the case ID
-->
<script setup lang="ts">
defineProps<{
  caseData: {
    id: string
    name: string
    description: string
    icon: string
    category: string
    tags: string[]
  }
  disabled: boolean
}>()

defineEmits<{
  run: [caseId: string]
}>()
</script>

<template>
  <div class="case-card">
    <div class="case-card-header">
      <span class="case-icon">{{ caseData.icon }}</span>
      <div class="case-card-title">
        <h3>{{ caseData.name }}</h3>
        <span class="case-category">{{ caseData.category }}</span>
      </div>
    </div>
    <p class="case-description">{{ caseData.description }}</p>
    <div class="case-card-footer">
      <div class="case-tags">
        <span v-for="tag in caseData.tags" :key="tag" class="case-tag">{{ tag }}</span>
      </div>
      <button
        class="case-run-btn"
        :disabled="disabled"
        @click="$emit('run', caseData.id)"
      >
        ▶ Run
      </button>
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
}

.case-card-title h3 {
  font-size: 14px;
  font-weight: 600;
  color: #e0e0e0;
  margin: 0;
}

.case-category {
  font-size: 10px;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
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