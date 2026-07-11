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

const props = defineProps<{
  approvalId: string
  tool: string
  reason: string
  input: Record<string, any>
  autoApprove: boolean
  visible: boolean
  error?: string
}>()

const emit = defineEmits<{
  approve: [approvalId: string]
  deny: [approvalId: string]
  close: []
}>()

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
</script>

<template>
  <Transition name="approval-fade">
    <div v-if="visible && !autoApprove" class="approval-overlay" @click.self="emit('close')">
      <div class="approval-dialog" :class="{ 'approval-dialog-error': error }">
        <!-- Header -->
        <div class="approval-header">
          <span class="approval-icon">&#9888;</span>
          <div class="approval-title-group">
            <h3 class="approval-title">Approval Required</h3>
            <span class="approval-tool-name">{{ tool }}</span>
          </div>
          <span
            v-if="!error"
            class="approval-countdown"
            :class="{ 'countdown-warn': countdown <= 10 }"
          >
            {{ countdown }}s
          </span>
          <span v-else class="approval-countdown approval-countdown-error">&#10007;</span>
        </div>

        <!-- F6: Error banner — shown when error prop is set (e.g. approval timed out) -->
        <div v-if="error" class="approval-error-banner">
          <span class="approval-error-icon">&#9888;</span>
          <span class="approval-error-text">{{ error }}</span>
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
  background: rgba(0, 0, 0, 0.6);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(2px);
}

/* Dialog card */
.approval-dialog {
  background: #1e1e1e;
  border: 1px solid #444;
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
  border-bottom: 1px solid #333;
  background: #2a1a0a;
  border-radius: 12px 12px 0 0;
}

.approval-icon {
  font-size: 24px;
  color: #f0a030;
  flex-shrink: 0;
}

.approval-title-group {
  flex: 1;
}

.approval-title {
  margin: 0;
  font-size: 16px;
  color: #e0e0e0;
  font-weight: 600;
}

.approval-tool-name {
  font-size: 12px;
  color: #f0a030;
  font-family: var(--font-mono);
  background: rgba(240, 160, 48, 0.1);
  padding: 1px 8px;
  border-radius: 4px;
}

.approval-countdown {
  font-size: 18px;
  font-weight: 700;
  color: #888;
  font-variant-numeric: tabular-nums;
  min-width: 40px;
  text-align: right;
}

.approval-countdown.countdown-warn {
  color: #e74c3c;
  animation: countdown-pulse 0.5s ease-in-out infinite alternate;
}

/* F6: Error state — replaces countdown with a red ✗ marker */
.approval-countdown-error {
  color: #e74c3c;
  font-size: 22px;
}

.approval-dialog-error {
  border-color: #c62828;
  box-shadow: 0 8px 32px rgba(198, 40, 40, 0.35);
}

.approval-dialog-error .approval-header {
  background: #2e1a1a;
  border-color: #4a2a2a;
}

/* F6: Error banner shown above the reason section */
.approval-error-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 20px;
  background: rgba(231, 76, 60, 0.12);
  border-bottom: 1px solid rgba(231, 76, 60, 0.3);
  color: #e74c3c;
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
  border-bottom: 1px solid #2a2a2a;
}

.approval-label {
  display: block;
  font-size: 11px;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 6px;
}

.approval-reason {
  margin: 0;
  font-size: 13px;
  color: #d4d4d4;
  line-height: 1.5;
}

.approval-params {
  margin: 0;
  background: #141414;
  border: 1px solid #333;
  border-radius: 6px;
  padding: 10px 12px;
  font-family: var(--font-mono);
  font-size: 12px;
  color: #c0c0c0;
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
  background: #2e7d32;
  color: #fff;
}

.approve-btn:hover {
  background: #388e3c;
}

.deny-btn {
  background: #c62828;
  color: #fff;
}

.deny-btn:hover {
  background: #d32f2f;
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