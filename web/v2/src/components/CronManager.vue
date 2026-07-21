<!-- CronManager.vue — 管理 Dialog 内 Cron tab 的主面板

     功能：
       - 列出全部 cron（name / schedule / action_type / status / next / last / 操作）
       - 顶部"新建"按钮
       - 行操作：enable / disable / pause / resume / trigger / history / edit / delete
       - 选中某行时在右侧/下方展开 CronExecutions 查看其执行历史

     数据流：
       - 打开时 loadCrons()；写操作后由 useCronEvents 事件回写 useCrons 本地缓存
       - 状态切换 / 删除 / trigger 调用 useCrons 对应方法，失败用 toast 提示

     Emits:
       - 无对外事件；CRUD 全部在内部消化。
-->
<script setup lang="ts">
import { ref, computed, watch, onMounted, nextTick } from 'vue'
import { useCrons } from '@/composables/useCrons'
import { useCronEvents } from '@/composables/useCronEvents'
import { useToast } from '@/composables/useToast'
import CronForm from './CronForm.vue'
import CronExecutions from './CronExecutions.vue'
import type { Cron, CronStatus, CreateCronInput, UpdateCronInput } from '@/types/cron'

const props = defineProps<{
  /** 父级希望直接定位到的 cron id（一次性）。用于从 CronDockPanel 跳转。 */
  focusCronId?: string
}>()

const {
  crons,
  loading,
  stats,
  loadCrons,
  createCron,
  updateCron,
  deleteCron,
  setStatus,
  triggerCron,
} = useCrons()

// 激活事件聚合（注册 WS 监听 + 暴露计数）。
useCronEvents()

const { showError, showInfo } = useToast()

/** 列表过滤参数。 */
const statusFilter = ref<CronStatus | ''>('')
const query = ref('')

const filteredCrons = computed(() => {
  let list = crons.value
  if (statusFilter.value) list = list.filter(c => c.status === statusFilter.value)
  const q = query.value.trim().toLowerCase()
  if (q) {
    list = list.filter(c =>
      c.name.toLowerCase().includes(q) ||
      c.id.toLowerCase().includes(q) ||
      (c.description || '').toLowerCase().includes(q),
    )
  }
  return list
})

// ---- 表单弹窗 ----
const formVisible = ref(false)
const formCron = ref<Cron | null>(null)

function openCreate() {
  formCron.value = null
  formVisible.value = true
}

function openEdit(c: Cron) {
  formCron.value = c
  formVisible.value = true
}

async function handleSave(req: CreateCronInput | UpdateCronInput) {
  try {
    if (formCron.value) {
      await updateCron(formCron.value.id, req as UpdateCronInput)
      showInfo('定时器已更新')
    } else {
      await createCron(req as CreateCronInput)
      showInfo('定时器已创建')
    }
    formVisible.value = false
  } catch (err) {
    showError(`保存定时器失败: ${err instanceof Error ? err.message : String(err)}`)
  }
}

// ---- 行操作 ----
async function handleSetStatus(c: Cron, status: CronStatus) {
  try {
    await setStatus(c.id, status)
    showInfo(`已切换为 ${status}`)
  } catch (err) {
    showError(`状态切换失败: ${err instanceof Error ? err.message : String(err)}`)
  }
}

async function handleTrigger(c: Cron) {
  try {
    const exec = await triggerCron(c.id)
    showInfo(`已触发，执行 ID: ${exec.id}（${exec.status}）`)
    // 触发后刷新执行历史面板：强制重挂 CronExecutions 以重新拉取。
    if (selectedCronId.value === c.id) {
      executionsVisible.value = false
      await nextTick()
      executionsVisible.value = true
    }
  } catch (err) {
    showError(`触发失败: ${err instanceof Error ? err.message : String(err)}`)
  }
}

async function handleDelete(c: Cron) {
  if (!confirm(`确认删除定时器 "${c.name}"？其执行历史将一并清除。`)) return
  try {
    await deleteCron(c.id)
    showInfo('定时器已删除')
    if (selectedCronId.value === c.id) selectedCronId.value = ''
  } catch (err) {
    showError(`删除失败: ${err instanceof Error ? err.message : String(err)}`)
  }
}

// ---- 执行历史抽屉 ----
const selectedCronId = ref('')
const executionsVisible = ref(false)

function toggleHistory(c: Cron) {
  if (selectedCronId.value === c.id && executionsVisible.value) {
    executionsVisible.value = false
    selectedCronId.value = ''
  } else {
    selectedCronId.value = c.id
    executionsVisible.value = true
  }
}

const selectedCron = computed(() =>
  selectedCronId.value ? crons.value.find(c => c.id === selectedCronId.value) : null,
)

// ---- 加载 ----
async function reload() {
  try {
    await loadCrons()
  } catch {
    // loadCrons 内部已 showError
  }
}

onMounted(reload)

// 父级传入 focusCronId 时，打开对应执行历史并尝试滚动到该行。
watch(
  () => props.focusCronId,
  (id) => {
    if (!id) return
    selectedCronId.value = id
    executionsVisible.value = true
  },
  { immediate: true },
)

// ---- 展示辅助 ----
function statusClass(s: CronStatus): string {
  return `cron-status--${s}`
}

function scheduleSummary(c: Cron): string {
  if (c.schedule_type === 'once') return `once @ ${c.once_at || '-'}`
  if (c.schedule_type === 'cron') return c.cron_expr || '-'
  return `every ${c.cron_expr || '-'}`
}

function formatTime(ms: number | null | undefined): string {
  if (!ms) return '-'
  const d = new Date(ms)
  if (isNaN(d.getTime())) return '-'
  return d.toLocaleString()
}

/** 根据 cron 当前状态给出可用的状态切换操作列表。 */
function statusActions(c: Cron): Array<{ label: string; status: CronStatus }> {
  switch (c.status) {
    case 'enabled':
      return [{ label: '暂停', status: 'paused' }, { label: '禁用', status: 'disabled' }]
    case 'paused':
      return [{ label: '恢复', status: 'enabled' }, { label: '禁用', status: 'disabled' }]
    case 'disabled':
      return [{ label: '启用', status: 'enabled' }]
  }
  return []
}
</script>

<template>
  <div class="cron-manager">
    <div class="cron-header">
      <div class="cron-title-row">
        <h3 class="panel-title">定时器</h3>
        <span class="cron-count">{{ crons.length }}</span>
        <span class="cron-stats">
          <span class="stat-chip stat--enabled">启用 {{ stats.enabled }}</span>
          <span class="stat-chip stat--paused">暂停 {{ stats.paused }}</span>
          <span class="stat-chip stat--disabled">禁用 {{ stats.disabled }}</span>
        </span>
      </div>
      <button class="cron-new-btn" title="新建定时器" @click="openCreate">+ 新建</button>
    </div>

    <div class="cron-filter">
      <input
        v-model="query"
        class="cron-filter-input"
        type="text"
        placeholder="搜索名称 / 描述 / ID…"
      />
      <select v-model="statusFilter" class="cron-filter-select" title="按状态过滤">
        <option value="">全部状态</option>
        <option value="enabled">enabled</option>
        <option value="paused">paused</option>
        <option value="disabled">disabled</option>
      </select>
      <button class="cron-refresh-btn" :disabled="loading" @click="reload">
        {{ loading ? '加载中…' : '刷新' }}
      </button>
    </div>

    <div v-if="loading && crons.length === 0" class="cron-loading">Loading...</div>
    <div v-else-if="filteredCrons.length === 0" class="cron-empty">
      没有匹配的定时器。点击右上角"+ 新建"创建一个。
    </div>

    <div v-else class="cron-table">
      <div class="cron-row cron-row--head">
        <span class="col-name">名称 / 调度</span>
        <span class="col-action">动作</span>
        <span class="col-status">状态</span>
        <span class="col-next">下次触发</span>
        <span class="col-last">上次触发</span>
        <span class="col-ops">操作</span>
      </div>
      <div
        v-for="c in filteredCrons"
        :key="c.id"
        class="cron-row"
        :class="{ 'cron-row--selected': selectedCronId === c.id }"
      >
        <span class="col-name">
          <span class="cron-name">{{ c.name }}</span>
          <span class="cron-schedule">{{ scheduleSummary(c) }}</span>
          <span v-if="c.allow_concurrent" class="cron-cc">并发</span>
        </span>
        <span class="col-action">
          <span class="cron-action-type">{{ c.action_type }}</span>
        </span>
        <span class="col-status">
          <span class="cron-status" :class="statusClass(c.status)">{{ c.status }}</span>
        </span>
        <span class="col-next">{{ formatTime(c.next_trigger_at) }}</span>
        <span class="col-last">{{ formatTime(c.last_triggered_at) }}</span>
        <span class="col-ops">
          <button
            v-for="a in statusActions(c)"
            :key="a.label"
            class="op-btn"
            :title="a.label"
            @click="handleSetStatus(c, a.status)"
          >{{ a.label }}</button>
          <button class="op-btn op-trigger" title="手动触发" @click="handleTrigger(c)">触发</button>
          <button
            class="op-btn"
            :class="{ 'op-btn--active': selectedCronId === c.id && executionsVisible }"
            title="执行历史"
            @click="toggleHistory(c)"
          >历史</button>
          <button class="op-btn" title="编辑" @click="openEdit(c)">编辑</button>
          <button class="op-btn op-delete" title="删除" @click="handleDelete(c)">删除</button>
        </span>
      </div>
    </div>

    <!-- 执行历史抽屉：选中某 cron 时在列表下方展开 -->
    <div v-if="executionsVisible && selectedCron" class="cron-history-drawer">
      <div class="drawer-header">
        <span class="drawer-title">执行历史 — {{ selectedCron.name }}</span>
        <button class="drawer-close" @click="executionsVisible = false; selectedCronId = ''">✕</button>
      </div>
      <CronExecutions :cron-id="selectedCron.id" :visible="executionsVisible" />
    </div>

    <CronForm
      :cron-data="formCron"
      :visible="formVisible"
      @close="formVisible = false"
      @save="handleSave"
    />
  </div>
</template>

<style scoped>
.cron-manager {
  display: flex; flex-direction: column;
  height: 100%; gap: var(--space-sm);
  padding: var(--space-md); overflow: hidden;
}
.cron-header {
  display: flex; align-items: center; justify-content: space-between;
  flex-shrink: 0;
}
.cron-title-row { display: flex; align-items: center; gap: var(--space-sm); }
.panel-title {
  margin: 0; font-family: var(--font-display);
  font-size: 0.85rem; font-weight: 600; color: var(--text-primary);
  text-transform: uppercase; letter-spacing: 0.04em;
}
.cron-count {
  font-family: var(--font-mono); font-size: 0.7rem; color: var(--text-muted);
  background: var(--bg-elevated); padding: 2px 8px; border-radius: 10px;
}
.cron-stats { display: flex; gap: 6px; margin-left: 4px; }
.stat-chip {
  font-family: var(--font-mono); font-size: 0.62rem;
  padding: 2px 6px; border-radius: 8px;
  border: 1px solid var(--border-subtle); color: var(--text-muted);
}
.stat--enabled { color: var(--accent-success, #00e676); border-color: rgba(57,255,20,0.25); }
.stat--paused { color: var(--accent-warning, #ffab00); border-color: rgba(255,171,0,0.25); }
.stat--disabled { color: var(--text-muted); }
.cron-new-btn {
  background: var(--accent-running); color: var(--text-on-accent, #0b0d10);
  border: none; border-radius: var(--radius-md);
  padding: 0.3rem 0.75rem; font-size: 0.75rem; font-weight: 600;
  font-family: var(--font-display); cursor: pointer;
  transition: filter 0.15s;
}
.cron-new-btn:hover { filter: brightness(1.1); }
.cron-filter {
  display: flex; gap: var(--space-sm); flex-shrink: 0; flex-wrap: wrap;
}
.cron-filter-input {
  flex: 1; min-width: 160px;
  background: var(--bg-elevated); border: 1px solid var(--border-default);
  color: var(--text-primary); font-size: 0.8rem;
  padding: var(--space-sm); border-radius: var(--radius-md); outline: none;
  font-family: var(--font-mono);
}
.cron-filter-input:focus { border-color: var(--accent-running); }
.cron-filter-select {
  background: var(--bg-elevated); border: 1px solid var(--border-default);
  color: var(--text-primary); font-size: 0.75rem;
  padding: var(--space-sm); border-radius: var(--radius-md); outline: none;
}
.cron-refresh-btn {
  background: var(--bg-elevated); border: 1px solid var(--border-default);
  border-radius: var(--radius-md); color: var(--text-secondary);
  font-size: 0.72rem; font-weight: 600; font-family: var(--font-display);
  padding: 0.25rem 0.625rem; cursor: pointer;
  transition: border-color 0.15s, color 0.15s;
}
.cron-refresh-btn:hover:not(:disabled) { border-color: var(--accent-running); color: var(--accent-running); }
.cron-refresh-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.cron-loading, .cron-empty {
  padding: var(--space-xl); text-align: center;
  color: var(--text-muted); font-size: 0.8rem;
}
.cron-table { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 2px; }
.cron-row {
  display: grid;
  grid-template-columns: 1.6fr 1fr 0.8fr 1fr 1fr 2fr;
  gap: var(--space-sm);
  padding: 0.5rem 0.6rem;
  font-size: 0.78rem;
  border-radius: var(--radius-sm);
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  align-items: center;
}
.cron-row--head {
  background: var(--bg-panel); border-color: var(--border-default);
  font-family: var(--font-display); font-weight: 600;
  text-transform: uppercase; letter-spacing: 0.04em;
  font-size: 0.65rem; color: var(--text-muted);
  position: sticky; top: 0; z-index: 1;
}
.cron-row--selected { border-color: var(--accent-running); background: rgba(0,229,255,0.04); }
.col-name { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.cron-name { color: var(--text-primary); font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cron-schedule { font-family: var(--font-mono); font-size: 0.68rem; color: var(--text-muted); }
.cron-cc {
  display: inline-block; width: fit-content;
  font-family: var(--font-mono); font-size: 0.58rem;
  padding: 1px 4px; border-radius: 4px;
  color: var(--accent-running); border: 1px solid rgba(0,229,255,0.25);
  background: rgba(0,229,255,0.06);
}
.col-action .cron-action-type {
  font-family: var(--font-mono); font-size: 0.68rem; color: var(--text-secondary);
  padding: 2px 6px; border-radius: 4px; background: var(--bg-panel);
  border: 1px solid var(--border-subtle);
}
.col-status .cron-status {
  font-family: var(--font-mono); font-size: 0.68rem; font-weight: 600;
  padding: 2px 6px; border-radius: 6px; text-align: center; display: inline-block;
  color: var(--text-muted); border: 1px solid var(--border-subtle);
}
.cron-status--enabled { color: var(--accent-success, #00e676); border-color: rgba(57,255,20,0.25); background: rgba(57,255,20,0.06); }
.cron-status--paused { color: var(--accent-warning, #ffab00); border-color: rgba(255,171,0,0.25); background: rgba(255,171,0,0.06); }
.cron-status--disabled { color: var(--text-muted); }
.col-next, .col-last { font-family: var(--font-mono); font-size: 0.68rem; color: var(--text-secondary); }
.col-ops { display: flex; gap: 4px; flex-wrap: wrap; }
.op-btn {
  background: var(--bg-panel); border: 1px solid var(--border-default);
  border-radius: var(--radius-sm); color: var(--text-secondary);
  font-size: 0.66rem; font-weight: 600; font-family: var(--font-display);
  padding: 0.2rem 0.45rem; cursor: pointer;
  transition: border-color 0.15s, color 0.15s;
}
.op-btn:hover { border-color: var(--accent-running); color: var(--accent-running); }
.op-btn--active { border-color: var(--accent-running); color: var(--accent-running); background: rgba(0,229,255,0.08); }
.op-trigger:hover { color: var(--accent-success, #00e676); border-color: var(--accent-success, #00e676); }
.op-delete:hover { color: var(--accent-danger, #ff5252); border-color: var(--accent-danger, #ff5252); }
.cron-history-drawer {
  flex-shrink: 0; height: 280px;
  display: flex; flex-direction: column;
  border-top: 1px solid var(--border-default);
  background: var(--bg-panel);
  border-radius: var(--radius-md);
  overflow: hidden;
}
.drawer-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: var(--space-sm) var(--space-md);
  border-bottom: 1px solid var(--border-default);
  background: var(--bg-elevated);
  flex-shrink: 0;
}
.drawer-title {
  font-family: var(--font-display); font-size: 0.75rem; font-weight: 600;
  color: var(--text-primary); text-transform: uppercase; letter-spacing: 0.04em;
}
.drawer-close {
  background: none; border: none; color: var(--text-muted);
  font-size: 0.9rem; cursor: pointer; padding: 0 4px;
}
.drawer-close:hover { color: var(--text-primary); }
</style>
