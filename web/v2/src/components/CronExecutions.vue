<!-- CronExecutions.vue — Cron 执行历史面板

     Props:
       cronId: 指定 cron 的 id；为空时展示全局执行历史
       visible: 是否显示（用于父级 v-if 控制，便于测试）

     功能：
       - 按 cron / 状态 / 时间过滤执行历史
       - 手动清理（DELETE /api/crons/executions）
       - 分页（limit / offset）
       - 数据来自 useCrons().loadExecutions / cleanExecutions

     数据流：
       cronId 变化或 visible 打开时主动 loadExecutions；
       cron_execution_* 事件由 useCronEvents 防抖刷新 useCrons 列表，
       本面板通过 watch executionsOf(cronId) 自动同步。
-->
<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useCrons } from '@/composables/useCrons'
import type { CronExecution, ExecStatus } from '@/types/cron'

const props = defineProps<{
  cronId?: string
  visible?: boolean
}>()

const { loadExecutions, cleanExecutions, executionsOf } = useCrons()

const statusFilter = ref('')
const limit = ref(20)
const offset = ref(0)
const loading = ref(false)
const error = ref('')

/** 当前面板使用的 executions 列表。cronId 给定时按该 cron 缓存读取。 */
const executions = computed<CronExecution[]>(() => {
  // 全局模式（无 cronId）下 executionsOf('') 存的是最近一次 loadExecutions 的结果。
  return executionsOf(props.cronId || '')
})

const total = computed(() => executions.value.length)

/** 触发一次加载。 */
async function reload() {
  loading.value = true
  error.value = ''
  try {
    await loadExecutions({
      cron_id: props.cronId,
      status: statusFilter.value || undefined,
      limit: limit.value,
      offset: offset.value,
    })
  } catch (err) {
    error.value = err instanceof Error ? err.message : '加载失败'
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.visible, props.cronId, statusFilter.value, limit.value, offset.value],
  () => {
    if (props.visible === false) return
    reload()
  },
  { immediate: true },
)

/** 清理：按当前过滤条件（cron_id + status）调用 cleanExecutions。 */
async function handleClean() {
  const target = props.cronId ? `该定时器的${statusFilter.value || '全部'}` : `全部${statusFilter.value || ''}执行历史`
  if (!confirm(`确认清理 ${target} 执行记录？此操作不可撤销。`)) return
  try {
    const n = await cleanExecutions({
      cron_id: props.cronId,
      status: statusFilter.value || undefined,
    })
    // reload 会重新拉取
    await reload()
    console.log(`[CronExecutions] cleaned ${n} records`)
  } catch (err) {
    error.value = err instanceof Error ? err.message : '清理失败'
  }
}

function statusClass(s: ExecStatus): string {
  return `exec-status--${s}`
}

function formatTime(ms: number | null | undefined): string {
  if (!ms) return '-'
  const d = new Date(ms)
  if (isNaN(d.getTime())) return String(ms)
  return d.toLocaleString()
}

function formatDuration(ms: number): string {
  if (!ms) return '-'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(2)}s`
}
</script>

<template>
  <div class="cron-executions">
    <div class="exec-toolbar">
      <select v-model="statusFilter" class="exec-select" title="按状态过滤">
        <option value="">全部状态</option>
        <option value="running">running</option>
        <option value="completed">completed</option>
        <option value="failed">failed</option>
        <option value="skipped">skipped</option>
        <option value="missed">missed</option>
      </select>
      <select v-model.number="limit" class="exec-select" title="每页条数">
        <option :value="20">20 条</option>
        <option :value="50">50 条</option>
        <option :value="100">100 条</option>
      </select>
      <button class="exec-refresh-btn" :disabled="loading" @click="reload">
        {{ loading ? '加载中…' : '刷新' }}
      </button>
      <button class="exec-clean-btn" @click="handleClean" title="按当前过滤条件清理">清理</button>
      <span class="exec-count">共 {{ total }} 条</span>
    </div>

    <div v-if="error" class="exec-error">{{ error }}</div>

    <div v-if="!loading && executions.length === 0" class="exec-empty">
      暂无执行记录。
    </div>

    <div v-else class="exec-list">
      <div class="exec-row exec-row--head">
        <span class="col-time">触发时间</span>
        <span class="col-status">状态</span>
        <span class="col-input">渲染输入</span>
        <span class="col-result">结果 / 错误</span>
        <span class="col-duration">耗时</span>
      </div>
      <div v-for="ex in executions" :key="ex.id" class="exec-row">
        <span class="col-time">{{ formatTime(ex.triggered_at) }}</span>
        <span class="col-status" :class="statusClass(ex.status)">{{ ex.status }}</span>
        <span class="col-input" :title="ex.rendered_input">{{ ex.rendered_input || '-' }}</span>
        <span class="col-result" :title="ex.error || ex.result_summary">
          <template v-if="ex.error">{{ ex.error }}</template>
          <template v-else>{{ ex.result_summary || '-' }}</template>
        </span>
        <span class="col-duration">{{ formatDuration(ex.duration_ms) }}</span>
      </div>
    </div>

    <div v-if="executions.length > 0" class="exec-pager">
      <button class="pager-btn" :disabled="offset === 0" @click="offset = Math.max(0, offset - limit)">上一页</button>
      <span class="pager-info">offset {{ offset }} · limit {{ limit }}</span>
      <button class="pager-btn" :disabled="executions.length < limit" @click="offset = offset + limit">下一页</button>
    </div>
  </div>
</template>

<style scoped>
.cron-executions {
  display: flex; flex-direction: column;
  height: 100%; gap: var(--space-sm);
  padding: var(--space-md); overflow: hidden;
}
.exec-toolbar {
  display: flex; align-items: center; gap: var(--space-sm);
  flex-shrink: 0; flex-wrap: wrap;
}
.exec-select {
  background: var(--bg-elevated); border: 1px solid var(--border-default);
  border-radius: var(--radius-md); color: var(--text-primary);
  padding: 0.25rem 0.5rem; font-size: 0.75rem; outline: none;
}
.exec-refresh-btn,
.exec-clean-btn {
  background: var(--bg-elevated); border: 1px solid var(--border-default);
  border-radius: var(--radius-md); color: var(--text-secondary);
  font-size: 0.72rem; font-weight: 600; font-family: var(--font-display);
  padding: 0.25rem 0.625rem; cursor: pointer;
  transition: border-color 0.15s, color 0.15s, background 0.15s;
}
.exec-refresh-btn:hover:not(:disabled),
.exec-clean-btn:hover {
  border-color: var(--accent-running); color: var(--accent-running);
}
.exec-clean-btn:hover { color: var(--accent-danger); border-color: var(--accent-danger); }
.exec-refresh-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.exec-count {
  font-family: var(--font-mono); font-size: 0.7rem; color: var(--text-muted);
  margin-left: auto;
}
.exec-error {
  padding: var(--space-sm) var(--space-md);
  background: rgba(255, 82, 82, 0.1);
  border: 1px solid rgba(255, 82, 82, 0.3);
  color: var(--accent-danger, #ff5252);
  border-radius: var(--radius-md);
  font-size: 0.75rem;
}
.exec-empty {
  padding: var(--space-xl); text-align: center;
  color: var(--text-muted); font-size: 0.8rem;
}
.exec-list { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 2px; }
.exec-row {
  display: grid;
  grid-template-columns: 150px 80px 1fr 1fr 70px;
  gap: var(--space-sm);
  padding: 0.4rem 0.5rem;
  font-size: 0.75rem;
  border-radius: var(--radius-sm);
  background: var(--bg-elevated);
  border: 1px solid var(--border-subtle);
  align-items: center;
}
.exec-row--head {
  background: var(--bg-panel); border-color: var(--border-default);
  font-family: var(--font-display); font-weight: 600;
  text-transform: uppercase; letter-spacing: 0.04em;
  font-size: 0.65rem; color: var(--text-muted);
  position: sticky; top: 0; z-index: 1;
}
.col-time { font-family: var(--font-mono); color: var(--text-secondary); }
.col-status {
  font-family: var(--font-mono); font-size: 0.68rem; font-weight: 600;
  text-align: center; padding: 1px 4px; border-radius: 6px;
  color: var(--text-muted); border: 1px solid var(--border-subtle);
}
.exec-status--completed { color: var(--accent-success, #00e676); border-color: rgba(57,255,20,0.25); background: rgba(57,255,20,0.06); }
.exec-status--failed { color: var(--accent-danger, #ff5252); border-color: rgba(255,77,77,0.25); background: rgba(255,77,77,0.06); }
.exec-status--running { color: var(--accent-running, #00e5ff); border-color: rgba(0,229,255,0.25); background: rgba(0,229,255,0.06); }
.exec-status--skipped { color: var(--accent-warning, #ffab00); border-color: rgba(255,171,0,0.25); background: rgba(255,171,0,0.06); }
.exec-status--missed { color: var(--text-muted); }
.col-input, .col-result {
  min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  color: var(--text-secondary);
}
.col-result { color: var(--text-primary); }
.col-duration { font-family: var(--font-mono); color: var(--text-muted); text-align: right; }
.exec-pager {
  display: flex; align-items: center; justify-content: center; gap: var(--space-sm);
  flex-shrink: 0; padding-top: var(--space-sm);
}
.pager-btn {
  background: var(--bg-elevated); border: 1px solid var(--border-default);
  border-radius: var(--radius-md); color: var(--text-secondary);
  font-size: 0.72rem; padding: 0.2rem 0.6rem; cursor: pointer;
}
.pager-btn:hover:not(:disabled) { color: var(--accent-running); border-color: var(--accent-running); }
.pager-btn:disabled { opacity: 0.4; cursor: not-allowed; }
.pager-info { font-family: var(--font-mono); font-size: 0.7rem; color: var(--text-muted); }
</style>
