<script setup lang="ts">
/**
 * SessionEditDialog — Session 完整信息查看 / 编辑弹窗
 *
 * 从左侧 SessionDock 的"重命名"入口（✎）打开。与旧的行内重命名不同，这里
 * 以弹窗形式展示 session 的完整元数据，并允许修改可编辑字段：
 *   - Session ID（只读，便于用户复制定位）
 *   - Name（可编辑，必填）
 *   - Workspace（可编辑：沿用当前 / 切到 auto / 切到自定义路径）
 *
 * 不可编辑字段（status / project / tokens / 时间戳 / 轮次）以只读信息行展示，
 * 让用户在改名/改工作目录时也能看到 session 的整体状态。未来新增字段可
 * 直接在 dialog-body 里追加 form-row，不需要改 emit 协议。
 *
 * Props:
 *   - visible: 是否显示
 *   - session: 目标 session（null 时弹窗不渲染内容）
 *   - projectName: session 所属 project 的展示名（只读 badge）
 *
 * Emits:
 *   - close: 取消/关闭
 *   - save: 提交，payload { name, workspaceMode, customPath }
 *     workspaceMode: 'keep'（不改 workspace）| 'auto'（清空，回退 auto/project）| 'custom'（自定义路径）
 *     customPath: 仅在 mode==='custom' 时有意义
 */
import { ref, computed, watch } from 'vue'
import type { Session } from '@/composables/useSessionStore'

const props = defineProps<{
  visible: boolean
  session: Session | null
  projectName: string
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'save', payload: { name: string; workspaceMode: 'keep' | 'auto' | 'custom'; customPath: string }): void
}>()

/** 表单状态。每次弹窗打开时由 watch(visible) 从 props.session 重新初始化。 */
const name = ref('')
const mode = ref<'keep' | 'auto' | 'custom'>('keep')
const customPath = ref('')
const error = ref<string | null>(null)
const saving = ref(false)

/** 当前 session 的 workspace 展示文案：体现 workspace_dir 与 auto 标志的关系。 */
const currentWorkspaceText = computed(() => {
  const s = props.session
  if (!s) return ''
  if (s.workspaceDir) return s.workspaceDir
  if (s.workspaceAuto) return 'Auto（回退到 project workspace 或 ./workspace/session-<id>）'
  return '未设置'
})

const autoPreview = computed(() => './workspace/session-<id>')

/** mode=auto 的说明：清空 workspace_dir，回退到 project working_directory 或自动生成。 */
const autoDescription = computed(() => {
  if (props.session?.projectId) {
    return `清空 workspace，回退到 project working_directory；若 project 未设置则使用 ${autoPreview.value}`
  }
  return `清空 workspace，使用自动生成的 ${autoPreview.value}`
})

const canSubmit = computed(() => {
  if (saving.value) return false
  if (!name.value.trim()) return false
  if (mode.value === 'custom' && !customPath.value.trim()) return false
  return true
})

/** 从 session 初始化表单。打开弹窗时调用。 */
function initFromSession() {
  const s = props.session
  if (!s) return
  name.value = s.name
  customPath.value = s.workspaceDir || ''
  // 默认 keep：不改动 workspace，避免用户只想改名时误清空工作目录。
  mode.value = 'keep'
  error.value = null
}

function handleClose() {
  if (saving.value) return
  emit('close')
}

function handleSubmit() {
  if (!canSubmit.value) return
  error.value = null
  // 自定义模式：trim 后还要求非空（canSubmit 已保证），传具体路径。
  // auto 模式：customPath 传空串，后端清空 workspace_dir。
  // keep 模式：customPath 不被使用，由父级决定是否带 workspace_dir 字段。
  const customPathValue = mode.value === 'custom' ? customPath.value.trim() : ''
  emit('save', {
    name: name.value.trim(),
    workspaceMode: mode.value,
    customPath: customPathValue,
  })
}

// 父级会在保存成功/失败后控制 visible；这里只在 visible 变 true 时初始化表单。
watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      initFromSession()
    } else {
      saving.value = false
    }
  },
)

// session 对象引用变化（同一弹窗复用、切到另一个 session）时也重新初始化。
watch(
  () => props.session?.id,
  () => {
    if (props.visible) initFromSession()
  },
)

/** 暴露给父级：保存失败时由父级调用以显示错误并保持弹窗打开。 */
function failWith(msg: string) {
  error.value = msg
  saving.value = false
}
defineExpose({ failWith })

function formatTs(ts: number): string {
  if (!ts) return '-'
  try {
    return new Date(ts).toLocaleString()
  } catch {
    return String(ts)
  }
}
</script>

<template>
  <Teleport to="body">
    <div v-if="visible && session" class="dialog-overlay" @click.self="handleClose">
      <div class="dialog-panel">
        <div class="dialog-header">
          <h3 class="dialog-title">Session 详情</h3>
          <button class="dialog-close" @click="handleClose" title="Close">×</button>
        </div>

        <div class="dialog-body">
          <!-- 只读信息区：Session ID / Project / Status / 统计。让用户在编辑时
               也能看到 session 的整体状态，避免脱离上下文盲改。 -->
          <div class="readonly-grid">
            <div class="readonly-row">
              <span class="readonly-label">Session ID</span>
              <code class="readonly-value mono">{{ session.id }}</code>
            </div>
            <div class="readonly-row">
              <span class="readonly-label">Project</span>
              <span class="readonly-value">{{ projectName || session.projectId || '-' }}</span>
            </div>
            <div class="readonly-row">
              <span class="readonly-label">Status</span>
              <span class="readonly-value">
                <span class="status-chip" :class="`status--${session.status}`">{{ session.status }}</span>
              </span>
            </div>
            <div class="readonly-row">
              <span class="readonly-label">Tokens</span>
              <span class="readonly-value mono">{{ session.totalTokens.toLocaleString() }}</span>
            </div>
            <div class="readonly-row">
              <span class="readonly-label">Turns</span>
              <span class="readonly-value mono">{{ session.turnCount }}</span>
            </div>
            <div class="readonly-row">
              <span class="readonly-label">Created</span>
              <span class="readonly-value mono">{{ formatTs(session.createdAt) }}</span>
            </div>
            <div class="readonly-row">
              <span class="readonly-label">Updated</span>
              <span class="readonly-value mono">{{ formatTs(session.updatedAt) }}</span>
            </div>
          </div>

          <div class="divider" />

          <!-- 可编辑区 -->
          <div v-if="error" class="form-error">{{ error }}</div>

          <div class="form-row">
            <label class="form-label" for="session-edit-name">Name <span class="required">*</span></label>
            <input
              id="session-edit-name"
              v-model="name"
              type="text"
              class="form-input"
              placeholder="Session name"
            />
          </div>

          <div class="form-row">
            <label class="form-label">Workspace</label>
            <div class="readonly-row readonly-row--inline">
              <span class="readonly-label">当前</span>
              <span class="readonly-value mono">{{ currentWorkspaceText }}</span>
            </div>
            <div class="mode-options">
              <button
                class="mode-option"
                :class="{ active: mode === 'keep' }"
                @click="mode = 'keep'"
              >
                <span class="mode-dot" />
                <span class="mode-label">保持不变</span>
              </button>
              <button
                class="mode-option"
                :class="{ active: mode === 'auto' }"
                @click="mode = 'auto'"
              >
                <span class="mode-dot" />
                <span class="mode-label">Auto / 回退 project</span>
              </button>
              <button
                class="mode-option"
                :class="{ active: mode === 'custom' }"
                @click="mode = 'custom'"
              >
                <span class="mode-dot" />
                <span class="mode-label">自定义路径</span>
              </button>
            </div>
            <div class="mode-description">
              <template v-if="mode === 'keep'">不修改 workspace。</template>
              <template v-else-if="mode === 'auto'">{{ autoDescription }}</template>
              <template v-else>切换到下方指定的服务器路径，目录不存在会自动创建。仅切换指针，不迁移已有文件。</template>
            </div>
          </div>

          <div v-if="mode === 'custom'" class="form-row">
            <label class="form-label" for="session-edit-path">Server Path <span class="required">*</span></label>
            <input
              id="session-edit-path"
              v-model="customPath"
              type="text"
              class="form-input mono"
              placeholder="/tmp/my-session-workspace"
            />
          </div>
        </div>

        <div class="dialog-footer">
          <button class="btn-secondary" @click="handleClose" :disabled="saving">Cancel</button>
          <button class="btn-primary" :disabled="!canSubmit" @click="handleSubmit">
            {{ saving ? 'Saving...' : 'Save' }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script lang="ts">
// 额外导出 save payload 类型，供父组件类型标注使用。
export interface SessionEditSavePayload {
  name: string
  workspaceMode: 'keep' | 'auto' | 'custom'
  customPath: string
}
</script>

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
  max-width: 520px;
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

/* ---- 只读信息区 ---- */
.readonly-grid {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.readonly-row {
  display: flex;
  align-items: baseline;
  gap: 10px;
}

.readonly-row--inline {
  margin-bottom: 6px;
}

.readonly-label {
  flex-shrink: 0;
  width: 72px;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  font-weight: 600;
  color: var(--text-muted, #5c6675);
}

.readonly-value {
  font-size: 13px;
  color: var(--text-secondary, #9aa3b2);
  word-break: break-all;
}

.mono {
  font-family: var(--font-mono, ui-monospace, monospace);
}

.status-chip {
  display: inline-block;
  font-family: var(--font-mono, monospace);
  font-size: 0.65rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  padding: 1px 6px;
  border-radius: 8px;
}

.status-chip.status--running {
  color: var(--accent-running, #00e5ff);
  background: rgba(0, 229, 255, 0.1);
}

.status-chip.status--completed {
  color: var(--accent-success, #39ff14);
  background: rgba(57, 255, 20, 0.1);
}

.status-chip.status--failed {
  color: var(--accent-danger, #ff4d4d);
  background: rgba(255, 77, 77, 0.1);
}

.status-chip.status--empty {
  color: var(--text-muted, #5c6675);
  background: rgba(255, 255, 255, 0.04);
}

.divider {
  height: 1px;
  background: var(--border-default, rgba(255, 255, 255, 0.1));
  margin: 2px 0;
}

/* ---- 可编辑区 ---- */
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

.required {
  color: var(--accent-danger, #ff4d4d);
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

.form-error {
  background: rgba(231, 76, 60, 0.15);
  border: 1px solid rgba(255, 107, 107, 0.32);
  color: var(--accent-danger, #ff4d4d);
  padding: 8px 12px;
  border-radius: var(--radius-md, 6px);
  font-size: 12px;
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

.mode-option:hover {
  background: var(--bg-hover, #202632);
  color: var(--text-primary, #e8ebf0);
}

.mode-option.active {
  border-color: var(--border-active, rgba(0, 229, 255, 0.4));
  color: var(--accent-running, #00e5ff);
  background: rgba(0, 229, 255, 0.05);
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
  transition: background 0.15s, opacity 0.15s, filter 0.15s;
  border: none;
}

.btn-secondary {
  background: var(--bg-hover, #202632);
  color: var(--text-secondary, #ccc);
}

.btn-secondary:hover:not(:disabled) {
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

.btn-secondary:disabled,
.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
