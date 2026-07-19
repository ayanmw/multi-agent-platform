<!-- ProjectConfig — project configuration form component
     Replaces the main content area to manage project settings.

     Features:
       - Edit project name, description, working directory
       - Create new project or update existing one
       - Delete project (with confirmation)
       - Back button to return to main view

     Emits:
       back: user clicked the back button to return to main view
-->
<script setup lang="ts">
import { ref, computed } from 'vue'
import { useProjectStore, type ProjectRequest } from '../composables/useProjectStore'

const emit = defineEmits<{
  back: []
}>()

const { activeProject, updateProject, createProject, deleteProject, setActiveProject, projects } = useProjectStore()

/** Whether we are creating a new project (no active project or active is 'default') */
const isNewProject = computed(() => !activeProject.value || activeProject.value.id === 'default')

const form = ref<ProjectRequest>({
  name: activeProject.value?.name || '',
  description: activeProject.value?.description || '',
  working_directory: activeProject.value?.working_directory || '',
})

const saving = ref(false)
const error = ref<string | null>(null)

/** Save the project — create or update depending on mode */
async function save() {
  saving.value = true
  error.value = null
  try {
    if (activeProject.value && activeProject.value.id !== 'default') {
      const updated = await updateProject(activeProject.value.id, form.value)
      setActiveProject(updated.id)
    } else {
      const created = await createProject(form.value)
      setActiveProject(created.id)
    }
    emit('back')
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Save failed'
  } finally {
    saving.value = false
  }
}

/** Delete the active project (not allowed for 'default') */
async function remove() {
  if (!activeProject.value || activeProject.value.id === 'default') return
  if (!confirm(`Delete project "${activeProject.value.name}" and all its sessions?`)) return
  try {
    await deleteProject(activeProject.value.id)
    emit('back')
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Delete failed'
  }
}

/** Cancel and go back */
function cancel() {
  emit('back')
}
</script>

<template>
  <div class="project-config">
    <div class="config-header">
      <button class="back-btn" @click="cancel" title="Back">&larr; Back</button>
      <h2 class="config-title">{{ isNewProject ? 'Create Project' : 'Project Settings' }}</h2>
    </div>

    <form class="config-form" @submit.prevent="save">
      <div class="form-group">
        <label class="form-label" for="project-name">Project Name</label>
        <input
          id="project-name"
          v-model="form.name"
          type="text"
          class="form-input"
          placeholder="My Project"
          required
        />
      </div>

      <div class="form-group">
        <label class="form-label" for="project-desc">Description</label>
        <textarea
          id="project-desc"
          v-model="form.description"
          class="form-input form-textarea"
          placeholder="What is this project about?"
          rows="3"
        ></textarea>
      </div>

      <div class="form-group">
        <label class="form-label" for="project-dir">Working Directory</label>
        <input
          id="project-dir"
          v-model="form.working_directory"
          type="text"
          class="form-input"
          placeholder="/home/user/projects/my-app"
        />
        <span class="form-hint">Default working directory for shell commands in this project</span>
      </div>

      <div v-if="error" class="form-error">{{ error }}</div>

      <div class="form-actions">
        <button type="submit" class="btn-save" :disabled="saving || !form.name.trim()">
          {{ saving ? 'Saving...' : (isNewProject ? 'Create Project' : 'Save Changes') }}
        </button>
        <button type="button" class="btn-cancel" @click="cancel">Cancel</button>
        <button
          v-if="!isNewProject"
          type="button"
          class="btn-delete"
          @click="remove"
          :disabled="saving"
        >
          Delete Project
        </button>
      </div>
    </form>
  </div>
</template>

<style scoped>
.project-config {
  max-width: 640px;
  margin: 0 auto;
  padding: 20px;
}

.config-header {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 24px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border-primary);
}

.back-btn {
  background: #333;
  border: 1px solid #444;
  color: #ccc;
  font-size: 13px;
  padding: 4px 12px;
  border-radius: 6px;
  cursor: pointer;
  transition: background 0.2s;
}

.back-btn:hover {
  background: #444;
  color: #fff;
}

.config-title {
  font-size: 18px;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0;
}

.config-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
}

.form-input {
  background: var(--bg-primary);
  border: 1px solid var(--border-primary);
  border-radius: 6px;
  padding: 8px 12px;
  font-size: 14px;
  color: var(--text-primary);
  font-family: var(--font-sans);
  transition: border-color 0.2s;
}

.form-input:focus {
  outline: none;
  border-color: var(--accent-blue);
}

.form-textarea {
  resize: vertical;
  min-height: 60px;
}

.form-hint {
  font-size: 11px;
  color: var(--text-muted);
}

.form-error {
  background: rgba(231, 76, 60, 0.1);
  border: 1px solid rgba(231, 76, 60, 0.3);
  border-radius: 6px;
  padding: 8px 12px;
  font-size: 13px;
  color: var(--accent-red);
}

.form-actions {
  display: flex;
  gap: 10px;
  margin-top: 8px;
  flex-wrap: wrap;
}

.btn-save {
  background: var(--accent-blue);
  color: #fff;
  border: none;
  border-radius: 6px;
  padding: 8px 16px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
}

.btn-save:hover:not(:disabled) {
  background: #3a8eef;
}

.btn-save:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-cancel {
  background: #333;
  border: 1px solid #444;
  color: #ccc;
  border-radius: 6px;
  padding: 8px 16px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s;
}

.btn-cancel:hover {
  background: #444;
  color: #fff;
}

.btn-delete {
  background: transparent;
  border: 1px solid #4a2a2a;
  color: var(--accent-red);
  border-radius: 6px;
  padding: 8px 16px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s;
  margin-left: auto;
}

.btn-delete:hover:not(:disabled) {
  background: rgba(231, 76, 60, 0.1);
}

.btn-delete:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>