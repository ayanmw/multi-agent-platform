<script setup lang="ts">
import { ref, computed, watch } from 'vue'

/**
 * NewSessionDialog — 新建 Session 确认弹窗
 *
 * 说明：
 * - project 有 working_directory 时，默认选择 "Project workspace"，
 *   提交时不传 workspace_dir，后端/session 保持为空，运行时回退到 project working_directory。
 * - project 无 working_directory 时，默认选择 "Auto workspace"，
 *   显示 ./workspace/session-{id} 并传 workspace_dir 给后端。
 * - "Custom path" 允许用户显式指定路径。
 *
 * props:
 *   - visible: 是否显示
 *   - projectId: 目标 project id
 *   - projectName: 目标 project 名称（展示用）
 *   - projectWorkingDirectory: project 的 working_directory，可能为空
 *
 * emits:
 *   - close: 取消/关闭
 *   - create: 确认创建，payload { name, workspaceDir }
 */
const props = defineProps<{
  visible: boolean
  projectId: string
  projectName: string
  projectWorkingDirectory: string
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'create', payload: { name: string; workspaceDir: string }): void
}>()

const name = ref('')
const mode = ref<'project' | 'auto' | 'custom'>('project')
const customPath = ref('')

const autoPathPreview = computed(() => {
  // 仅作预览，真实目录由后端确定；一致起见这里用相同规则。
  return `./workspace/session-<id>`
})

const resolvedDescription = computed(() => {
  if (mode.value === 'project') {
    if (props.projectWorkingDirectory) {
      return `Use project workspace: ${props.projectWorkingDirectory}`
    }
    return 'No project workspace configured. Will fall back to auto workspace.'
  }
  if (mode.value === 'auto') {
    return `Auto workspace: ${autoPathPreview.value}`
  }
  return 'Use custom server path.'
})

const canSubmit = computed(() => {
  if (mode.value === 'custom') {
    return customPath.value.trim().length > 0
  }
  return true
})

function reset() {
  name.value = ''
  customPath.value = ''
  mode.value = props.projectWorkingDirectory ? 'project' : 'auto'
}

function handleClose() {
  reset()
  emit('close')
}

function handleSubmit() {
  if (!canSubmit.value) return
  let workspaceDir = ''
  if (mode.value === 'auto') {
    // 自动模式：让后端生成 ./workspace/session-{id}/，前端不传具体路径。
    // 传一个标记明显是 auto，但保持与现有 API 兼容：传空即 auto。
    workspaceDir = ''
  } else if (mode.value === 'custom') {
    workspaceDir = customPath.value.trim()
  }
  // project 模式也传空，让后端/session workspace_dir 为空，运行时回退 project.working_directory。
  emit('create', { name: name.value.trim(), workspaceDir })
  reset()
}

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      reset()
    }
  },
)
</script>

<template>
  <Teleport to="body">
    <div v-if="visible" class="dialog-overlay" @click.self="handleClose">
      <div class="dialog-panel">
        <div class="dialog-header">
          <h3 class="dialog-title">New Session</h3>
          <button class="dialog-close" @click="handleClose" title="Close">×</button>
        </div>

        <div class="dialog-body">
          <div class="form-row">
            <label class="form-label">Project</label>
            <div class="project-badge">
              {{ projectName || projectId }}
            </div>
          </div>

          <div class="form-row">
            <label class="form-label" for="new-session-name">Session Name (optional)</label>
            <input
              id="new-session-name"
              v-model="name"
              type="text"
              class="form-input"
              placeholder="New Session"
            />
          </div>

          <div class="form-row">
            <label class="form-label">Workspace</label>
            <div class="mode-options">
              <button
                class="mode-option"
                :class="{ active: mode === 'project' }"
                :disabled="!projectWorkingDirectory"
                @click="mode = 'project'"
              >
                <span class="mode-dot" />
                <span class="mode-label">Project workspace</span>
              </button>
              <button
                class="mode-option"
                :class="{ active: mode === 'auto' }"
                @click="mode = 'auto'"
              >
                <span class="mode-dot" />
                <span class="mode-label">Auto workspace</span>
              </button>
              <button
                class="mode-option"
                :class="{ active: mode === 'custom' }"
                @click="mode = 'custom'"
              >
                <span class="mode-dot" />
                <span class="mode-label">Custom path</span>
              </button>
            </div>
            <div class="mode-description">
              {{ resolvedDescription }}
            </div>
          </div>

          <div v-if="mode === 'custom'" class="form-row">
            <label class="form-label" for="new-session-custom-path">Server Path</label>
            <input
              id="new-session-custom-path"
              v-model="customPath"
              type="text"
              class="form-input"
              placeholder="/tmp/my-session-workspace"
            />
          </div>
        </div>

        <div class="dialog-footer">
          <button class="btn-secondary" @click="handleClose">Cancel</button>
          <button class="btn-primary" :disabled="!canSubmit" @click="handleSubmit">
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
  max-width: 480px;
  max-height: calc(100vh - 48px);
  background: var(--bg-panel, #18181b);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: var(--radius-lg, 8px);
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
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background: var(--bg-elevated, #1e1e22);
  flex-shrink: 0;
}

.dialog-title {
  margin: 0;
  font-size: 16px;
  color: var(--text-primary, #e0e0e0);
}

.dialog-close {
  background: none;
  border: none;
  color: var(--text-muted, #888);
  font-size: 22px;
  cursor: pointer;
  line-height: 1;
}

.dialog-close:hover {
  color: var(--text-primary, #fff);
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
  color: var(--text-muted, #888);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  font-weight: 600;
}

.project-badge {
  display: inline-flex;
  align-self: flex-start;
  padding: 6px 10px;
  background: var(--bg-elevated, #1e1e22);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: var(--radius-md, 6px);
  color: var(--text-secondary, #9aa3b2);
  font-size: 13px;
}

.form-input {
  background: var(--bg-canvas, #0b0d10);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: var(--radius-md, 6px);
  color: var(--text-primary, #ddd);
  padding: 8px 10px;
  font-size: 13px;
  outline: none;
  font-family: inherit;
}

.form-input:focus {
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
}

.mode-options {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.mode-option {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  background: var(--bg-canvas, #0b0d10);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: var(--radius-md, 6px);
  color: var(--text-secondary, #9aa3b2);
  font-size: 13px;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s, border-color 0.15s, color 0.15s;
}

.mode-option:hover:not(:disabled) {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
}

.mode-option.active {
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
  color: var(--accent-running, #00e5ff);
  background: rgba(0, 229, 255, 0.05);
}

.mode-option:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.mode-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 2px solid var(--text-muted, #5c6675);
  flex-shrink: 0;
  transition: border-color 0.15s, background 0.15s;
}

.mode-option.active .mode-dot {
  border-color: var(--accent-running, #00e5ff);
  background: var(--accent-running, #00e5ff);
}

.mode-description {
  font-size: 12px;
  color: var(--text-muted, #5c6675);
  line-height: 1.5;
  min-height: 1.5em;
}

.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 12px 16px;
  border-top: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  background: var(--bg-elevated, #1e1e22);
  flex-shrink: 0;
}

.btn-secondary,
.btn-primary {
  padding: 6px 14px;
  border-radius: var(--radius-md, 6px);
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
  border: none;
}

.btn-secondary {
  background: var(--bg-hover, #202632);
  color: var(--text-secondary, #ccc);
}

.btn-secondary:hover {
  background: var(--bg-canvas, #333);
  color: var(--text-primary, #fff);
}

.btn-primary {
  background: var(--accent-running, #00e5ff);
  color: var(--text-on-accent, #07090c);
}

.btn-primary:hover:not(:disabled) {
  filter: brightness(1.1);
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
