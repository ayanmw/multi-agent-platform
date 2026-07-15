<!-- CaseForm — modal form to create or edit a custom case
     Props:
       caseData: an existing Case to edit, or null to create a new case
       visible: whether the modal is shown

     Emits:
       close: user cancelled or closed the modal
       save: the form is valid and should be persisted.
             Emits CreateCaseRequest when creating, UpdateCaseRequest when editing.

     Behavior:
       - Resets form fields from caseData when opened (visible false -> true)
       - Parses acceptance criteria textarea: one per line, format type|target|description
       - Validates that name and category are non-empty
       - Tags are edited as comma-separated and normalized to trimmed, non-empty values
-->
<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import type { Case, CreateCaseRequest, UpdateCaseRequest, AcceptanceCriterion } from '../types/case'

const props = defineProps<{
  caseData: Case | null
  visible: boolean
}>()

const emit = defineEmits<{
  close: []
  save: [req: CreateCaseRequest | UpdateCaseRequest]
}>()

const name = ref('')
const category = ref('')
const icon = ref('')
const description = ref('')
const systemPrompt = ref('')
const defaultInput = ref('')
const maxSteps = ref(10)
const tagsText = ref('')
const goal = ref('')
const acceptanceCriteriaText = ref('')
const error = ref<string | null>(null)

const isEditing = computed(() => props.caseData !== null)
const modalTitle = computed(() => (isEditing.value ? 'Edit Case' : 'Create Case'))

/** Reset form fields from the provided case or default values */
function resetForm(c: Case | null) {
  if (c) {
    name.value = c.name
    category.value = c.category
    icon.value = c.icon
    description.value = c.description
    systemPrompt.value = c.system_prompt
    defaultInput.value = c.default_input
    maxSteps.value = c.contract.max_steps
    tagsText.value = c.tags.join(', ')
    goal.value = c.contract.goal
    acceptanceCriteriaText.value = formatAcceptanceCriteria(c.contract.acceptance_criteria)
  } else {
    name.value = ''
    category.value = ''
    icon.value = ''
    description.value = ''
    systemPrompt.value = ''
    defaultInput.value = ''
    maxSteps.value = 10
    tagsText.value = ''
    goal.value = ''
    acceptanceCriteriaText.value = ''
  }
  error.value = null
}

/** Convert acceptance criteria to textarea format */
function formatAcceptanceCriteria(list?: AcceptanceCriterion[]): string {
  if (!list || list.length === 0) return ''
  return list
    .map(ac => {
      const parts = [ac.type, ac.target]
      if (ac.description) parts.push(ac.description)
      return parts.join('|')
    })
    .join('\n')
}

/** Parse acceptance criteria textarea lines into typed objects */
function parseAcceptanceCriteria(text: string): AcceptanceCriterion[] {
  const result: AcceptanceCriterion[] = []
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const parts = trimmed.split('|')
    if (parts.length < 2) continue
    const [type, target, description] = parts
    result.push({
      type: type.trim(),
      target: target.trim(),
      description: description ? description.trim() : '',
    })
  }
  return result
}

/** Normalize comma-separated tags into a clean array */
function parseTags(text: string): string[] {
  return text
    .split(',')
    .map(t => t.trim())
    .filter(t => t.length > 0)
}

/** Reset form whenever the modal opens */
watch(
  () => props.visible,
  (visible, prevVisible) => {
    if (visible && !prevVisible) {
      resetForm(props.caseData)
    }
  }
)

/** Validate and emit save event */
function handleSave() {
  if (!name.value.trim()) {
    error.value = 'Name is required'
    return
  }
  if (!category.value.trim()) {
    error.value = 'Category is required'
    return
  }

  const contract = {
    goal: goal.value.trim(),
    max_steps: maxSteps.value,
    acceptance_criteria: parseAcceptanceCriteria(acceptanceCriteriaText.value),
  }

  const tags = parseTags(tagsText.value)

  if (props.caseData) {
    const req: UpdateCaseRequest = {
      name: name.value.trim(),
      category: category.value.trim(),
      icon: icon.value.trim() || undefined,
      description: description.value.trim() || undefined,
      system_prompt: systemPrompt.value.trim() || undefined,
      default_input: defaultInput.value.trim() || undefined,
      contract,
      tags,
    }
    emit('save', req)
  } else {
    const req: CreateCaseRequest = {
      name: name.value.trim(),
      category: category.value.trim(),
      icon: icon.value.trim() || undefined,
      description: description.value.trim() || undefined,
      system_prompt: systemPrompt.value.trim() || undefined,
      default_input: defaultInput.value.trim() || undefined,
      contract,
      tags,
    }
    emit('save', req)
  }
}

/** Close the modal without saving */
function handleClose() {
  emit('close')
}
</script>

<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="modal-overlay" @click.self="handleClose">
        <div class="modal-content">
          <div class="modal-header">
            <h2 class="modal-title">{{ modalTitle }}</h2>
            <button class="modal-close-btn" @click="handleClose" title="Close">✕</button>
          </div>

          <div class="modal-body">
            <div v-if="error" class="form-error">{{ error }}</div>

            <div class="form-grid">
              <div class="form-field">
                <label for="case-name">Name <span class="required">*</span></label>
                <input id="case-name" v-model="name" type="text" placeholder="Case name" />
              </div>

              <div class="form-field">
                <label for="case-category">Category <span class="required">*</span></label>
                <input id="case-category" v-model="category" type="text" placeholder="e.g. Web Dev" />
              </div>

              <div class="form-field">
                <label for="case-icon">Icon</label>
                <input id="case-icon" v-model="icon" type="text" placeholder="e.g. 🚀" />
              </div>

              <div class="form-field">
                <label for="case-max-steps">Max Steps</label>
                <input id="case-max-steps" v-model.number="maxSteps" type="number" min="1" />
              </div>
            </div>

            <div class="form-field">
              <label for="case-tags">Tags</label>
              <input
                id="case-tags"
                v-model="tagsText"
                type="text"
                placeholder="comma, separated, tags"
              />
            </div>

            <div class="form-field">
              <label for="case-goal">Goal</label>
              <input
                id="case-goal"
                v-model="goal"
                type="text"
                placeholder="High-level goal for this case"
              />
            </div>

            <div class="form-field">
              <label for="case-description">Description</label>
              <textarea id="case-description" v-model="description" rows="3" placeholder="Short description"></textarea>
            </div>

            <div class="form-field">
              <label for="case-system-prompt">System Prompt</label>
              <textarea
                id="case-system-prompt"
                v-model="systemPrompt"
                rows="4"
                placeholder="System prompt sent to the agent"
              ></textarea>
            </div>

            <div class="form-field">
              <label for="case-default-input">Default Input</label>
              <textarea
                id="case-default-input"
                v-model="defaultInput"
                rows="3"
                placeholder="Default user input"
              ></textarea>
            </div>

            <div class="form-field">
              <label for="case-acceptance">
                Acceptance Criteria
                <span class="hint">one per line: type|target|description</span>
              </label>
              <textarea
                id="case-acceptance"
                v-model="acceptanceCriteriaText"
                rows="4"
                placeholder="exact_match|output.txt|content matches expected"
              ></textarea>
            </div>
          </div>

          <div class="modal-footer">
            <button class="modal-cancel-btn" @click="handleClose">Cancel</button>
            <button class="modal-save-btn" @click="handleSave">Save</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10000;
  padding: 20px;
}

.modal-content {
  background: #252525;
  border: 1px solid #444;
  border-radius: 12px;
  max-width: 640px;
  width: 100%;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid #333;
}

.modal-title {
  font-size: 16px;
  font-weight: 700;
  color: #e0e0e0;
  margin: 0;
}

.modal-close-btn {
  background: none;
  border: none;
  color: #888;
  font-size: 18px;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  transition: color 0.2s, background 0.2s;
}

.modal-close-btn:hover {
  color: #fff;
  background: #333;
}

.modal-body {
  padding: 16px 20px;
  overflow-y: auto;
  flex: 1;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 14px 20px;
  border-top: 1px solid #333;
}

.form-error {
  background: rgba(231, 76, 60, 0.1);
  border: 1px solid rgba(231, 76, 60, 0.3);
  color: #e74c3c;
  font-size: 12px;
  padding: 8px 10px;
  border-radius: 6px;
  margin-bottom: 12px;
}

.form-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 12px;
}

.form-field label {
  font-size: 11px;
  color: #888;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.form-field label .required {
  color: #e74c3c;
}

.form-field label .hint {
  text-transform: none;
  color: #666;
  font-weight: 400;
  margin-left: 4px;
}

.form-field input,
.form-field textarea {
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 6px;
  padding: 8px 10px;
  color: #ddd;
  font-size: 13px;
  outline: none;
  transition: border-color 0.2s;
  font-family: inherit;
}

.form-field input:focus,
.form-field textarea:focus {
  border-color: #4a9eff;
}

.form-field textarea {
  resize: vertical;
  line-height: 1.4;
}

.modal-cancel-btn {
  padding: 8px 20px;
  background: #333;
  color: #ccc;
  border: 1px solid #444;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s;
}

.modal-cancel-btn:hover {
  background: #444;
}

.modal-save-btn {
  padding: 8px 24px;
  background: #4a9eff;
  color: #fff;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
}

.modal-save-btn:hover {
  background: #3a8eef;
}

.modal-enter-active,
.modal-leave-active {
  transition: all 0.25s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .modal-content,
.modal-leave-to .modal-content {
  transform: scale(0.95);
}
</style>
