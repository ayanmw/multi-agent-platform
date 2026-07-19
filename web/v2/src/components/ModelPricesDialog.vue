<!-- ModelPricesDialog.vue — view and edit LLM model pricing profiles
     Renders as a Teleport modal (same overlay pattern as RecentModsDialog).

     Data flow:
       onMounted(open) → useModelPrices.loadModelPrices() → GET /api/models/prices
       user edits input/output price inline → local draft buffer
       user clicks Save → useModelPrices.updateModelPrice(name, draft)
                         → PUT /api/models/prices/{model}
                         → optimistic update of the shared prices ref
       Escape / overlay click / Close button → emit('update:visible', false)

     Why this exists:
       CostTracker computes USD cost from ModelProfile.InputPrice/OutputPrice.
       If a price is 0 (or the model name doesn't match a registered profile),
       /api/costs returns total_cost_usd=0 even when tokens are non-zero.
       This dialog lets an operator inspect and correct prices at runtime
       without rebuilding the binary. Edits are memory-only and reset on
       server restart — the note from the backend is shown in the header.
-->
<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="dialogVisible" class="mp-overlay" @click.self="close">
        <div class="mp-dialog">
          <div class="mp-header">
            <h3>💲 模型价格管理</h3>
            <span class="mp-count">{{ countLabel }}</span>
            <button class="mp-close" @click="close" title="关闭">✕</button>
          </div>

          <!-- Persistence warning from the backend (runtime-only edits). -->
          <div v-if="persistenceNote" class="mp-note">
            ⚠ {{ persistenceNote }}
          </div>

          <div v-if="loading" class="mp-empty">加载中...</div>
          <div v-else-if="error" class="mp-error">加载失败: {{ error }}</div>
          <div v-else-if="prices.length === 0" class="mp-empty">
            注册表中暂无模型 profile
          </div>

          <div v-else class="mp-list">
            <div class="mp-table-head">
              <span class="col-name">模型</span>
              <span class="col-tier">Tier</span>
              <span class="col-in">Input $/1M</span>
              <span class="col-out">Output $/1M</span>
              <span class="col-actions"></span>
            </div>
            <div
              v-for="item in prices"
              :key="item.name"
              class="mp-row"
              :class="{ dirty: isDirty(item.name) }"
            >
              <span class="col-name" :title="item.name">
                <span class="mp-model-name">{{ item.name }}</span>
                <span class="mp-provider">{{ item.provider }}</span>
              </span>
              <span class="col-tier"><span class="mp-tier-badge" :data-tier="item.tier">{{ item.tier }}</span></span>
              <span class="col-in">
                <input
                  type="number"
                  step="0.01"
                  min="0"
                  class="mp-input"
                  :value="draftInput(item.name, item.input_price)"
                  @input="setDraft(item.name, 'input', $event)"
                />
              </span>
              <span class="col-out">
                <input
                  type="number"
                  step="0.01"
                  min="0"
                  class="mp-input"
                  :value="draftOutput(item.name, item.output_price)"
                  @input="setDraft(item.name, 'output', $event)"
                />
              </span>
              <span class="col-actions">
                <button
                  class="mp-save-btn"
                  :disabled="!isDirty(item.name) || saving === item.name"
                  @click="save(item.name)"
                >
                  {{ saving === item.name ? '保存中...' : '保存' }}
                </button>
              </span>
            </div>
          </div>

          <div class="mp-footer">
            <span class="mp-hint">价格仅影响新产生的 cost 记录；改 0 会导致 cost 为 0。</span>
            <button class="mp-close-btn" @click="close">关闭</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch, computed, onMounted, onUnmounted } from 'vue'
import { useModelPrices } from '@/composables/useModelPrices'

/**
 * Props:
 *   visible — v-model style toggle from the parent (App.vue header button).
 * Emits:
 *   update:visible — request parent to close (overlay click / Escape / Close button).
 */
const props = defineProps<{
  visible: boolean
}>()
const emit = defineEmits<{
  'update:visible': [v: boolean]
}>()

const {
  prices,
  loading,
  error,
  persistenceNote,
  loadModelPrices,
  updateModelPrice,
} = useModelPrices()

const dialogVisible = ref(props.visible)
watch(() => props.visible, v => {
  dialogVisible.value = v
  // Refresh on every open so the dialog reflects current registry state
  // (e.g., another operator may have edited a price via curl).
  if (v) {
    loadModelPrices().catch(() => { /* error already in store */ })
    drafts.value = {}
  }
})
watch(dialogVisible, v => { emit('update:visible', v) })

/** Local draft buffer: model name → { input?, output? }.
 *  Only fields the user has touched are stored; untouched fields fall back
 *  to the server value via draftInput/draftOutput. */
const drafts = ref<Record<string, { input?: number; output?: number }>>({})
/** Which model is currently being saved (disables its Save button). */
const saving = ref<string | null>(null)

const countLabel = computed(() => {
  const n = prices.value.length
  return n > 0 ? `${n} 个模型` : '暂无模型'
})

/** Read the draft input price, falling back to the server value when untouched. */
function draftInput(name: string, serverValue: number): number {
  const d = drafts.value[name]
  return d && d.input !== undefined ? d.input : serverValue
}

/** Read the draft output price, falling back to the server value when untouched. */
function draftOutput(name: string, serverValue: number): number {
  const d = drafts.value[name]
  return d && d.output !== undefined ? d.output : serverValue
}

/** A row is "dirty" (Save enabled) when any draft field diverges from the server value. */
function isDirty(name: string): boolean {
  const d = drafts.value[name]
  if (!d) return false
  const server = prices.value.find(p => p.name === name)
  if (!server) return false
  if (d.input !== undefined && d.input !== server.input_price) return true
  if (d.output !== undefined && d.output !== server.output_price) return true
  return false
}

/** Update the draft for a field. Parses the input event as a float; empty → undefined. */
function setDraft(name: string, field: 'input' | 'output', e: Event) {
  const val = (e.target as HTMLInputElement).value
  const parsed = val === '' ? undefined : Number(val)
  if (!drafts.value[name]) drafts.value[name] = {}
  if (parsed === undefined || Number.isNaN(parsed)) {
    drafts.value[name][field] = undefined
  } else {
    drafts.value[name][field] = parsed
  }
}

/** Persist the dirty fields for one model via PUT /api/models/prices/{model}. */
async function save(name: string) {
  const d = drafts.value[name]
  if (!d) return
  saving.value = name
  try {
    const req: { input_price?: number; output_price?: number } = {}
    if (d.input !== undefined) req.input_price = d.input
    if (d.output !== undefined) req.output_price = d.output
    await updateModelPrice(name, req)
    // Clear the draft on success; the shared prices ref is updated optimistically
    // by the store, so the row now shows the new server-confirmed values.
    delete drafts.value[name]
  } catch {
    // error surfaced via store; keep the draft so the user can retry
  } finally {
    saving.value = null
  }
}

function close() {
  dialogVisible.value = false
}

// Escape key closes the dialog (consistent with RecentModsDialog).
onMounted(() => {
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && dialogVisible.value) {
      e.preventDefault()
      close()
    }
  }
  window.addEventListener('keydown', onKey)
  onUnmounted(() => window.removeEventListener('keydown', onKey))
})
</script>

<style scoped>
.mp-overlay {
  position: fixed;
  inset: 0;
  z-index: 900;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
}

.mp-dialog {
  background: #1e1e2e;
  border: 1px solid #313244;
  border-radius: 12px;
  width: min(720px, 94vw);
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.4);
}

.mp-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 18px;
  border-bottom: 1px solid #313244;
  user-select: none;
}

.mp-header h3 {
  margin: 0;
  font-size: 15px;
  color: #cdd6f4;
  font-weight: 600;
}

.mp-count {
  flex: 1;
  font-size: 12px;
  color: #6c7086;
}

.mp-close {
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: #a6adc8;
  font-size: 16px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s;
}

.mp-close:hover {
  background: #313244;
  color: #cdd6f4;
}

.mp-note {
  padding: 8px 18px;
  font-size: 12px;
  color: #f9e2af;
  background: rgba(249, 226, 175, 0.08);
  border-bottom: 1px solid #313244;
}

.mp-empty,
.mp-error {
  padding: 40px 20px;
  text-align: center;
  color: #6c7086;
  font-size: 14px;
}

.mp-error {
  color: #f38ba8;
}

.mp-list {
  overflow-y: auto;
  flex: 1;
  padding: 6px 0;
  max-height: 56vh;
}

.mp-table-head,
.mp-row {
  display: grid;
  grid-template-columns: 2fr 1fr 1.1fr 1.1fr 0.9fr;
  align-items: center;
  gap: 8px;
  padding: 8px 18px;
}

.mp-table-head {
  font-size: 11px;
  color: #6c7086;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  border-bottom: 1px solid #313244;
  position: sticky;
  top: 0;
  background: #1e1e2e;
}

.mp-row {
  font-size: 13px;
  transition: background 0.1s;
}

.mp-row:hover {
  background: #181825;
}

.mp-row.dirty {
  background: rgba(249, 226, 175, 0.06);
}

.col-name {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.mp-model-name {
  font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
  color: #cdd6f4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.mp-provider {
  font-size: 10px;
  color: #585b70;
}

.mp-tier-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 8px;
  background: #313244;
  color: #a6adc8;
  text-transform: uppercase;
}

.mp-tier-badge[data-tier="efficient"] { background: rgba(166, 227, 161, 0.18); color: #a6e3a1; }
.mp-tier-badge[data-tier="standard"] { background: rgba(137, 180, 250, 0.18); color: #89b4fa; }
.mp-tier-badge[data-tier="premium"] { background: rgba(245, 194, 231, 0.18); color: #f5c2e7; }
.mp-tier-badge[data-tier="free"] { background: rgba(108, 112, 134, 0.18); color: #6c7086; }
.mp-tier-badge[data-tier="lightweight"] { background: rgba(249, 226, 175, 0.18); color: #f9e2af; }

.mp-input {
  width: 100%;
  background: #181825;
  border: 1px solid #313244;
  color: #cdd6f4;
  font-size: 13px;
  font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
  padding: 4px 8px;
  border-radius: 6px;
  outline: none;
  box-sizing: border-box;
}

.mp-input:focus {
  border-color: #89b4fa;
}

.mp-save-btn {
  background: #89b4fa;
  border: none;
  color: #1e1e2e;
  border-radius: 6px;
  padding: 5px 12px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.15s;
}

.mp-save-btn:disabled {
  background: #313244;
  color: #585b70;
  cursor: not-allowed;
}

.mp-save-btn:not(:disabled):hover {
  opacity: 0.85;
}

.mp-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 18px;
  border-top: 1px solid #313244;
}

.mp-hint {
  font-size: 11px;
  color: #585b70;
  flex: 1;
}

.mp-close-btn {
  background: #89b4fa;
  border: none;
  color: #1e1e2e;
  border-radius: 6px;
  padding: 6px 18px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.15s;
}

.mp-close-btn:hover {
  opacity: 0.85;
}

/* Fade transition (matches RecentModsDialog) */
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
