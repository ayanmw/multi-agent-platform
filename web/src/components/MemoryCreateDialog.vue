<!-- MemoryCreateDialog.vue — modal dialog for creating a new memory record.
     Teleported to body so it renders above overlays and sidebars.
-->
<script setup lang="ts">
import { ref, computed } from 'vue'
import type { CreateMemoryPayload } from '../composables/useMemoryStore'

const props = defineProps<{
  visible: boolean
  projectId: string
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'create', payload: CreateMemoryPayload): void
}>()

const memoryTypes = [
  'preference',
  'rule',
  'fact',
  'lesson',
  'reflection',
  'session_summary',
]

const scopeOptions = ['project', 'global', 'session']

const content = ref('')
const type = ref('preference')
const scope = ref('project')
const sessionId = ref('')
const confidence = ref(0.8)
const tier = ref('semantic')

const isValid = computed(() => {
  return content.value.trim().length > 0 && confidence.value >= 0 && confidence.value <= 1
})

function reset() {
  content.value = ''
  type.value = 'preference'
  scope.value = 'project'
  sessionId.value = ''
  confidence.value = 0.8
  tier.value = 'semantic'
}

function handleClose() {
  reset()
  emit('close')
}

function handleSubmit() {
  if (!isValid.value) return
  const payload: CreateMemoryPayload = {
    project_id: props.projectId,
    scope: scope.value,
    type: type.value,
    tier: tier.value,
    content: content.value.trim(),
    confidence: Number(confidence.value),
  }
  if (scope.value === 'session' && sessionId.value.trim()) {
    // Backend scope endpoint expects session_id inside the create body? Not documented,
    // so keep payload minimal and rely on scope field.
    // If the backend requires session_id, consumers can extend payload.
  }
  emit('create', payload)
  handleClose()
}
</script>

<template>
  <Teleport to="body">
    <div v-if="visible" class="dialog-overlay" @click.self="handleClose">
      <div class="dialog-panel">
        <div class="dialog-header">
          <h3 class="dialog-title">Create Memory</h3>
          <button class="dialog-close" @click="handleClose" title="Close">×</button>
        </div>

        <div class="dialog-body">
          <div class="form-row">
            <label class="form-label">Type</label>
            <div class="type-chips">
              <button
                v-for="t in memoryTypes"
                :key="t"
                :class="['type-chip', { active: type === t }]"
                @click="type = t"
              >
                {{ t }}
              </button>
            </div>
          </div>

          <div class="form-row">
            <label class="form-label">Scope</label>
            <select v-model="scope" class="form-select">
              <option v-for="s in scopeOptions" :key="s" :value="s">{{ s }}</option>
            </select>
          </div>

          <div v-if="scope === 'session'" class="form-row">
            <label class="form-label">Session ID</label>
            <input v-model="sessionId" type="text" class="form-input" placeholder="Optional session ID" />
          </div>

          <div class="form-row">
            <label class="form-label">Tier</label>
            <select v-model="tier" class="form-select">
              <option value="semantic">Semantic</option>
              <option value="consolidated">Consolidated</option>
            </select>
          </div>

          <div class="form-row">
            <label class="form-label">Confidence</label>
            <input
              v-model.number="confidence"
              type="number"
              min="0"
              max="1"
              step="0.05"
              class="form-input form-input-number"
            />
          </div>

          <div class="form-row">
            <label class="form-label">Content</label>
            <textarea
              v-model="content"
              class="form-textarea"
              rows="6"
              placeholder="Enter memory content..."
            ></textarea>
          </div>
        </div>

        <div class="dialog-footer">
          <button class="btn-secondary" @click="handleClose">Cancel</button>
          <button class="btn-primary" :disabled="!isValid" @click="handleSubmit">
            Create
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.dialog-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.72);
  z-index: 1100;
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 24px;
  backdrop-filter: blur(2px);
}

.dialog-panel {
  width: 100%;
  max-width: 560px;
  max-height: calc(100vh - 48px);
  background: var(--bg-primary, #18181b);
  border: 1px solid var(--border-primary, #333);
  border-radius: 10px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
}

.dialog-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 16px;
  border-bottom: 1px solid var(--border-primary, #333);
  background: var(--bg-secondary, #1e1e22);
  flex-shrink: 0;
}

.dialog-title {
  margin: 0;
  font-size: 16px;
  color: #e0e0e0;
}

.dialog-close {
  background: none;
  border: none;
  color: #888;
  font-size: 22px;
  cursor: pointer;
  line-height: 1;
}

.dialog-close:hover {
  color: #fff;
}

.dialog-body {
  padding: 16px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.form-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-label {
  font-size: 12px;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  font-weight: 600;
}

.form-select,
.form-input,
.form-textarea {
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 6px;
  color: #ddd;
  padding: 8px 10px;
  font-size: 13px;
  outline: none;
  font-family: inherit;
}

.form-select:focus,
.form-input:focus,
.form-textarea:focus {
  border-color: #4a9eff;
}

.form-input-number {
  max-width: 100px;
}

.form-textarea {
  resize: vertical;
  line-height: 1.5;
}

.type-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.type-chip {
  padding: 4px 10px;
  background: #2a2a2a;
  border: 1px solid #444;
  border-radius: 12px;
  color: #aaa;
  font-size: 12px;
  cursor: pointer;
  transition: all 0.15s;
  text-transform: capitalize;
}

.type-chip:hover {
  border-color: #666;
  color: #ddd;
}

.type-chip.active {
  background: #4a9eff;
  border-color: #4a9eff;
  color: #fff;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 12px 16px;
  border-top: 1px solid var(--border-primary, #333);
  background: var(--bg-secondary, #1e1e22);
  flex-shrink: 0;
}

.btn-secondary,
.btn-primary {
  padding: 6px 14px;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
  border: none;
}

.btn-secondary {
  background: #333;
  color: #ccc;
}

.btn-secondary:hover {
  background: #444;
  color: #fff;
}

.btn-primary {
  background: #4a9eff;
  color: #fff;
}

.btn-primary:hover:not(:disabled) {
  background: #3a8eef;
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
