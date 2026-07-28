<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useSkills } from '@/composables/useSkills'
import { useToast } from '@/composables/useToast'
import type { CreateSkillRequest, Skill, SkillScope, UpdateSkillRequest } from '@/types/skill'

interface Props {
  skill?: Skill | null
}

const props = withDefaults(defineProps<Props>(), {
  skill: null,
})

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved'): void
}>()

const show = computed(() => props.skill !== undefined)

const { createSkill, updateSkill, refresh } = useSkills()
const { showError, showInfo } = useToast()

const isEdit = computed(() => !!props.skill)

const id = ref('')
const displayName = ref('')
const description = ref('')
const tagsText = ref('')
const scope = ref<SkillScope>('global')
const projectId = ref('')
const templatesJson = ref('[]')
const parametersJson = ref('[]')
const formError = ref('')

const availableScopes: { value: SkillScope; label: string }[] = [
  { value: 'global', label: 'Global' },
  { value: 'project', label: 'Project' },
  { value: 'session', label: 'Session' },
]

/**
 * 重置表单。编辑时从 skill 反序列化，新建时给出默认 system_prompt 模板。
 */
function resetForm() {
  formError.value = ''
  if (props.skill) {
    id.value = props.skill.id
    displayName.value = props.skill.display_name || ''
    description.value = props.skill.description || ''
    tagsText.value = (props.skill.tags || []).join(', ')
    scope.value = props.skill.scope || 'global'
    projectId.value = props.skill.project_id || ''
    templatesJson.value = JSON.stringify(props.skill.templates || [], null, 2)
    parametersJson.value = JSON.stringify(props.skill.parameters || [], null, 2)
  } else {
    id.value = ''
    displayName.value = ''
    description.value = ''
    tagsText.value = ''
    scope.value = 'global'
    projectId.value = ''
    templatesJson.value = JSON.stringify([{ name: 'system_prompt', content: '', variables: [], is_required: true }], null, 2)
    parametersJson.value = '[]'
  }
}

watch(() => props.skill, resetForm, { immediate: true })

function parseTags(text: string): string[] {
  return text
    .split(/[,\n]/)
    .map(s => s.trim())
    .filter(Boolean)
}

function validateJSON(text: string, field: string): unknown {
  try {
    return JSON.parse(text)
  } catch (err) {
    throw new Error(`${field} 不是合法 JSON: ${err instanceof Error ? err.message : String(err)}`)
  }
}

function validateTemplates(parsed: unknown): void {
  if (!Array.isArray(parsed)) {
    throw new Error('templates 必须是数组')
  }
  const hasSystemOrTask = parsed.some((t: any) => t?.name === 'system_prompt' || t?.name === 'task_prompt')
  if (!hasSystemOrTask) {
    throw new Error('templates 中至少需要 system_prompt 或 task_prompt')
  }
}

async function handleSubmit() {
  formError.value = ''
  try {
    const templates = validateJSON(templatesJson.value, 'Templates')
    validateTemplates(templates)
    const parameters = validateJSON(parametersJson.value, 'Parameters')
    if (!Array.isArray(parameters)) throw new Error('parameters 必须是数组')

    const tags = parseTags(tagsText.value)

    if (isEdit.value) {
      const changes: UpdateSkillRequest = {
        display_name: displayName.value,
        description: description.value,
        scope: scope.value,
        project_id: projectId.value,
      }
      // 编辑时若用户改了 templates/parameters 则一起提交；否则只改元数据。
      if (templatesJson.value !== JSON.stringify(props.skill!.templates || [], null, 2)) {
        // 后端 PUT 只接受 content / parameters；templates 通过 content 字段更新 system_prompt。
        // 这里把第一个 system_prompt / task_prompt 的 content 作为 content 字段。
        const first = (templates as any[]).find((t: any) => t?.name === 'system_prompt' || t?.name === 'task_prompt')
        if (first) changes.content = first.content
        changes.parameters = parameters as any[]
      }
      await updateSkill(props.skill!.id, changes)
      showInfo('Skill 已更新')
    } else {
      const req: CreateSkillRequest = {
        id: id.value.trim(),
        display_name: displayName.value,
        description: description.value,
        content: (templates as any[]).find((t: any) => t?.name === 'system_prompt')?.content || '',
        parameters: parameters as any[],
        tags,
        scope: scope.value,
      }
      if (projectId.value) req.project_id = projectId.value
      await createSkill(req)
      showInfo('Skill 已创建')
    }

    await refresh()
    emit('saved')
  } catch (err) {
    formError.value = err instanceof Error ? err.message : String(err)
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="skill-form">
      <div v-if="show" class="skill-form-overlay" @click.self="emit('close')">
        <div class="skill-form-panel" role="dialog" aria-modal="true">
          <header class="skill-form-header">
            <h3 class="skill-form-title">{{ isEdit ? 'Edit Skill' : 'New Skill' }}</h3>
            <button class="skill-form-close" aria-label="关闭" @click="emit('close')">×</button>
          </header>

          <div class="skill-form-body">
            <div v-if="formError" class="skill-form-error">{{ formError }}</div>

            <label class="skill-field">
              <span class="skill-field-label">ID</span>
              <input v-model="id" type="text" class="skill-field-input" :disabled="isEdit" placeholder="my-skill" />
            </label>

            <label class="skill-field">
              <span class="skill-field-label">Display Name</span>
              <input v-model="displayName" type="text" class="skill-field-input" placeholder="My Skill" />
            </label>

            <label class="skill-field">
              <span class="skill-field-label">Description</span>
              <textarea v-model="description" class="skill-field-textarea" rows="2" placeholder="简短描述..." />
            </label>

            <label class="skill-field">
              <span class="skill-field-label">Tags <small>(逗号或换行分隔)</small></span>
              <textarea v-model="tagsText" class="skill-field-textarea" rows="2" placeholder="code, refactor" />
            </label>

            <div class="skill-field-row">
              <label class="skill-field">
                <span class="skill-field-label">Scope</span>
                <select v-model="scope" class="skill-field-select">
                  <option v-for="opt in availableScopes" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
                </select>
              </label>

              <label class="skill-field">
                <span class="skill-field-label">Project ID</span>
                <input v-model="projectId" type="text" class="skill-field-input" placeholder="(optional)" />
              </label>
            </div>

            <label class="skill-field">
              <span class="skill-field-label">Templates JSON</span>
              <textarea v-model="templatesJson" class="skill-field-textarea skill-field-code" rows="8" placeholder='[{"name":"system_prompt","content":"..."}]' />
            </label>

            <label class="skill-field">
              <span class="skill-field-label">Parameters JSON</span>
              <textarea v-model="parametersJson" class="skill-field-textarea skill-field-code" rows="6" placeholder='[]' />
            </label>
          </div>

          <footer class="skill-form-footer">
            <button class="skill-form-btn" @click="emit('close')">Cancel</button>
            <button class="skill-form-btn skill-form-btn--primary" @click="handleSubmit">
              {{ isEdit ? 'Save' : 'Create' }}
            </button>
          </footer>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.skill-form-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.72);
  backdrop-filter: blur(3px);
  z-index: 230;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}

.skill-form-panel {
  width: 90vw;
  max-width: 560px;
  height: 85vh;
  background: var(--bg-canvas);
  border: 1px solid var(--border-default);
  border-radius: 14px;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: 0 30px 90px rgba(0, 0, 0, 0.7);
}

.skill-form-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-md) var(--space-lg);
  border-bottom: 1px solid var(--border-default);
  background: var(--bg-elevated);
}

.skill-form-title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 1rem;
  color: var(--text-primary);
}

.skill-form-close {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: 6px;
  color: var(--text-secondary);
  font-size: 1.2rem;
  width: 32px;
  height: 32px;
  cursor: pointer;
}

.skill-form-close:hover {
  color: var(--text-primary);
  border-color: var(--accent-running);
}

.skill-form-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-md) var(--space-lg);
  display: flex;
  flex-direction: column;
  gap: var(--space-sm);
}

.skill-form-error {
  padding: var(--space-sm);
  border: 1px solid rgba(255, 77, 77, 0.3);
  background: rgba(255, 77, 77, 0.08);
  color: var(--accent-danger);
  border-radius: var(--radius-md);
  font-size: 0.8rem;
}

.skill-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.skill-field-label {
  font-family: var(--font-display);
  font-size: 0.7rem;
  font-weight: 600;
  text-transform: uppercase;
  color: var(--text-muted);
  letter-spacing: 0.04em;
}

.skill-field-label small {
  font-weight: 400;
  text-transform: none;
}

.skill-field-input,
.skill-field-select,
.skill-field-textarea {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  padding: 0.5rem 0.75rem;
  font-size: 0.85rem;
  font-family: var(--font-mono);
  outline: none;
}

.skill-field-input:focus,
.skill-field-select:focus,
.skill-field-textarea:focus {
  border-color: var(--accent-running);
  box-shadow: 0 0 0 3px rgba(0, 229, 255, 0.08);
}

.skill-field-input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.skill-field-textarea {
  resize: vertical;
}

.skill-field-code {
  font-size: 0.78rem;
}

.skill-field-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-sm);
}

.skill-form-footer {
  flex-shrink: 0;
  padding: var(--space-sm) var(--space-lg);
  border-top: 1px solid var(--border-default);
  background: var(--bg-elevated);
  display: flex;
  justify-content: flex-end;
  gap: var(--space-sm);
}

.skill-form-btn {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-family: var(--font-display);
  font-size: 0.75rem;
  font-weight: 600;
  padding: 6px 14px;
  cursor: pointer;
  transition: all 0.15s;
}

.skill-form-btn:hover {
  border-color: var(--accent-running);
  color: var(--accent-running);
}

.skill-form-btn--primary {
  color: var(--accent-running);
  border-color: var(--accent-running);
  background: rgba(0, 229, 255, 0.08);
}

.skill-form-enter-active,
.skill-form-leave-active {
  transition: opacity 0.2s ease;
}

.skill-form-enter-from,
.skill-form-leave-to {
  opacity: 0;
}

@media (max-width: 767px) {
  .skill-form-overlay {
    padding: 0;
  }
  .skill-form-panel {
    width: 100vw;
    height: 100vh;
    border-radius: 0;
  }
  .skill-field-row {
    grid-template-columns: 1fr;
  }
}
</style>
