<!-- ApprovalDialog — modal dialog for policy-based approval requests
     Shows when the backend emits a system_info event with type="approval_required".
     Props:
       approvalId: unique ID for this approval request
       tool: the tool name being intercepted
       reason: why the tool call was intercepted (e.g. "DangerousCommandRule")
       input: the tool call arguments/parameters
       autoApprove: if true, auto-approve without showing the dialog
       visible: whether the dialog is shown
       error: optional error message — when set, renders error styling and disables buttons
              (F6: used to surface approval timeout / lost connection scenarios)
     Emits:
       approve: user clicked Approve (auto-approve also emits this)
       deny: user clicked Deny or countdown expired
       close: dialog was dismissed
-->
<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useFocusTrap } from '../composables/useFocusTrap'

const props = defineProps<{
  approvalId: string
  tool: string
  reason: string
  input: Record<string, any>
  autoApprove: boolean
  visible: boolean
  error?: string
  /** Policy rule that triggered the approval request */
  rule?: string
  /** Tool namespace */
  namespace?: string
  /** Tool risk/capability tags */
  tags?: string[]
}>()

const emit = defineEmits<{
  approve: [approvalId: string]
  deny: [approvalId: string]
  close: []
}>()

const dialogRef = ref<HTMLElement | null>(null)
const approveBtnRef = ref<HTMLElement | null>(null)

useFocusTrap({
  containerRef: dialogRef,
  visible: { get value() { return props.visible && !props.autoApprove } },
  close: () => emit('close'),
  initialFocus: approveBtnRef,
})

const countdown = ref(30)
let timer: ReturnType<typeof setInterval> | null = null

function startTimer() {
  stopTimer()
  timer = setInterval(() => {
    countdown.value--
    if (countdown.value <= 0) {
      stopTimer()
      emit('deny', props.approvalId)
    }
  }, 1000)
}

function stopTimer() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

// Watch for visibility changes: reset countdown when shown
watch(() => props.visible, (newVal) => {
  if (newVal) {
    countdown.value = 30
    if (props.autoApprove) {
      // Auto-approve immediately — don't start the timer
      emit('approve', props.approvalId)
    } else {
      startTimer()
    }
  } else {
    stopTimer()
  }
})

// F6: 当外部传入 error 时，停止倒计时（状态由父组件驱动，按钮已禁用）
watch(() => props.error, (err) => {
  if (err) {
    stopTimer()
  }
})

onMounted(() => {
  if (props.visible) {
    countdown.value = 30
    if (props.autoApprove) {
      emit('approve', props.approvalId)
    } else {
      startTimer()
    }
  }
})

onUnmounted(() => {
  stopTimer()
})

/** Format the input object as a readable string */
function formatInput(obj: Record<string, any>): string {
  try {
    return JSON.stringify(obj, null, 2)
  } catch {
    return String(obj)
  }
}

/** Truncate long input strings for display */
function truncateInput(s: string, maxLen = 500): string {
  if (s.length <= maxLen) return s
  return s.slice(0, maxLen) + '...'
}

/** Check whether a tag indicates elevated risk */
function isRiskTag(tag: string): boolean {
  const riskTags = ['destructive', 'dangerous', 'write', 'exec', 'shell', 'network', 'mcp']
  return riskTags.includes(tag.toLowerCase())
}

/** Resolve the short tool name without its namespace */
function toolShortName(name: string): string {
  const idx = name.indexOf('/')
  return idx > 0 ? name.slice(idx + 1) : name
}
</script>

<template>
  <Transition name="approval-fade">
    <div v-if="visible && !autoApprove" class="approval-overlay" @click.self="emit('close')">
      <div
        ref="dialogRef"
        class="approval-dialog"
        :class="{ 'approval-dialog-error': error }"
        role="dialog"
        aria-modal="true"
        aria-labelledby="approval-title"
      >
        <!-- Header -->
        <div class="approval-header">
          <span class="approval-icon" aria-hidden="true">&#9888;</span>
          <div class="approval-title-group">
            <h3 id="approval-title" class="approval-title">Approval Required</h3>
            <span v-if="namespace" class="approval-namespace">{{ namespace }}</span>
            <span class="approval-tool-name">{{ toolShortName(tool) }}</span>
            <span
              v-for="tag in tags"
              :key="tag"
              class="approval-tag"
              :class="{ 'approval-tag-risk': isRiskTag(tag) }"
            >{{ tag }}</span>
          </div>
          <span
            v-if="!error"
            class="approval-countdown"
            :class="{ 'countdown-warn': countdown <= 10 }"
            aria-live="polite"
          >
            {{ countdown }}s
          </span>
          <span v-else class="approval-countdown approval-countdown-error" aria-hidden="true">&#10007;</span>
        </div>

        <!-- F6: Error banner — shown when error prop is set (e.g. approval timed out) -->
        <div v-if="error" class="approval-error-banner">
          <span class="approval-error-icon" aria-hidden="true">&#9888;</span>
          <span class="approval-error-text">{{ error }}</span>
        </div>

        <!-- Rule source -->
        <div v-if="rule" class="approval-section">
          <span class="approval-label">Triggered Rule</span>
          <p class="approval-rule">{{ rule }}</p>
        </div>

        <!-- Reason -->
        <div class="approval-section">
          <span class="approval-label">Reason</span>
          <p class="approval-reason">{{ reason }}</p>
        </div>

        <!-- Command / Parameters -->
        <div class="approval-section">
          <span class="approval-label">Parameters</span>
          <pre class="approval-params"><code>{{ truncateInput(formatInput(input)) }}</code></pre>
        </div>

        <!-- Actions — disabled when error is set -->
        <div class="approval-actions">
          <button
            class="approval-btn deny-btn"
            :disabled="!!error"
            @click="emit('deny', approvalId)"
          >
            &#10007; Deny
          </button>
          <button
            ref="approveBtnRef"
            class="approval-btn approve-btn"
            :disabled="!!error"
            @click="emit('approve', approvalId)"
          >
            &#10003; Approve
          </button>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
/* Overlay — full-screen semi-transparent backdrop */
.approval-overlay {
  position: fixed;
  inset: 0;
  background: var(--overlay-bg, rgba(0, 0, 0, 0.6));
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(2px);
}

/* Dialog card */
.approval-dialog {
  background: var(--bg-elevated, #1e1e1e);
  border: 1px solid var(--border-default, #444);
  border-radius: 12px;
  width: 520px;
  max-width: 90vw;
  max-height: 80vh;
  overflow-y: auto;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.6);
}

/* Header */
.approval-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border-default, #333);
  background: rgba(240, 160, 48, 0.12);
  border-radius: 12px 12px 0 0;
}

.approval-icon {
  font-size: 24px;
  color: var(--accent-warning, #f0a030);
  flex-shrink: 0;
}

.approval-title-group {
  flex: 1;
}

.approval-title {
  margin: 0;
  font-size: 16px;
  color: var(--text-primary, #e0e0e0);
  font-weight: 600;
}

.approval-namespace {
  display: inline-block;
  font-size: 11px;
  color: var(--text-muted, #888);
  background: var(--bg-hover, #333);
  padding: 2px 8px;
  border-radius: 4px;
  margin-right: 6px;
}

.approval-tool-name {
  font-size: 12px;
  color: var(--accent-warning, #f0a030);
  font-family: var(--font-mono);
  background: rgba(240, 160, 48, 0.1);
  padding: 1px 8px;
  border-radius: 4px;
}

.approval-tag {
  display: inline-block;
  font-size: 10px;
  color: var(--text-secondary, #aaa);
  background: var(--bg-hover, #2a2a2a);
  border: 1px solid var(--border-default, #3a3a3a);
  padding: 1px 6px;
  border-radius: 10px;
  margin-left: 4px;
  text-transform: lowercase;
}

.approval-tag-risk {
  color: var(--accent-danger, #e74c3c);
  background: rgba(231, 76, 60, 0.12);
  border-color: rgba(231, 76, 60, 0.4);
}

.approval-countdown {
  font-size: 18px;
  font-weight: 700;
  color: var(--text-muted, #888);
  font-variant-numeric: tabular-nums;
  min-width: 40px;
  text-align: right;
}

.approval-countdown.countdown-warn {
  color: var(--accent-danger, #e74c3c);
  animation: countdown-pulse 0.5s ease-in-out infinite alternate;
}

/* F6: Error state — replaces countdown with a red ✗ marker */
.approval-countdown-error {
  color: var(--accent-danger, #e74c3c);
  font-size: 22px;
}

.approval-dialog-error {
  border-color: var(--accent-danger, #c62828);
  box-shadow: 0 8px 32px rgba(198, 40, 40, 0.35);
}

.approval-dialog-error .approval-header {
  background: rgba(231, 76, 60, 0.12);
  border-color: rgba(231, 76, 60, 0.3);
}

/* F6: Error banner shown above the reason section */
.approval-error-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 20px;
  background: rgba(231, 76, 60, 0.12);
  border-bottom: 1px solid rgba(231, 76, 60, 0.3);
  color: var(--accent-danger, #e74c3c);
  font-size: 13px;
  font-weight: 500;
}

.approval-error-icon {
  font-size: 16px;
  flex-shrink: 0;
}

.approval-error-text {
  line-height: 1.4;
}

@keyframes countdown-pulse {
  from { opacity: 1; }
  to { opacity: 0.5; }
}

/* Sections */
.approval-section {
  padding: 12px 20px;
  border-bottom: 1px solid var(--border-default, #2a2a2a);
}

.approval-label {
  display: block;
  font-size: 11px;
  color: var(--text-muted, #888);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 6px;
}

.approval-reason {
  margin: 0;
  font-size: 13px;
  color: var(--text-primary, #d4d4d4);
  line-height: 1.5;
}

.approval-params {
  margin: 0;
  background: var(--bg-canvas, #141414);
  border: 1px solid var(--border-default, #333);
  border-radius: 6px;
  padding: 10px 12px;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--text-secondary, #c0c0c0);
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 200px;
  overflow-y: auto;
}

.approval-params code {
  font-family: inherit;
}

/* Actions */
.approval-actions {
  display: flex;
  gap: 12px;
  padding: 16px 20px;
  justify-content: flex-end;
}

.approval-btn {
  padding: 10px 24px;
  border: none;
  border-radius: 8px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s, transform 0.1s;
  font-family: inherit;
}

.approval-btn:active {
  transform: scale(0.97);
}

/* F6: disabled state when error is set */
.approval-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
  transform: none;
}

.approval-btn:disabled:hover {
  background: inherit;
}

.approve-btn {
  background: rgba(46, 125, 50, 0.85);
  color: var(--text-primary, #fff);
}

.approve-btn:hover:not(:disabled) {
  background: rgba(56, 142, 60, 0.95);
}

.deny-btn {
  background: rgba(198, 40, 40, 0.85);
  color: var(--text-primary, #fff);
}

.deny-btn:hover:not(:disabled) {
  background: rgba(211, 47, 47, 0.95);
}

/* Transition */
.approval-fade-enter-active,
.approval-fade-leave-active {
  transition: opacity 0.2s;
}

.approval-fade-enter-from,
.approval-fade-leave-to {
  opacity: 0;
}
</style>
