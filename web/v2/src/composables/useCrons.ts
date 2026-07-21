// useCrons — Cron 子系统的前端 Store（CRUD + 状态操作 + 执行历史）。
//
// 数据流：
//  1) 组件（CronManager / CronDockPanel）打开时调用 loadCrons(filter) 主动拉取；
//  2) 任何写操作（create/update/delete/setStatus/trigger）后，后端会广播
//     `cron_*` 事件，useCronEvents 收到后负责刷新本 store，实现免轮询实时同步；
//  3) 本 store 只负责 REST 调用与本地状态维护，事件聚合在 useCronEvents。
//
// 设计与 useTodoStore / useSkills 对齐：模块级 singleton ref，所有组件共享同一实例。
import { ref, computed } from 'vue'
import { useToast } from './useToast'
import type {
  Cron,
  CronExecution,
  CreateCronInput,
  UpdateCronInput,
  CronListFilter,
  ExecListFilter,
  CleanExecFilter,
  CronStatus,
} from '@/types/cron'

/** 模块级 cron 列表，所有组件共享。 */
const crons = ref<Cron[]>([])
/** 模块级执行历史缓存（按 cron_id 分组；"" key 表示全局/未分组）。 */
const executionsByCron = ref<Record<string, CronExecution[]>>({})

const loading = ref(false)

/** 把 CronListFilter 拼成 query string。 */
function buildListQuery(filter?: CronListFilter): string {
  if (!filter) return ''
  const parts: string[] = []
  if (filter.status) parts.push(`status=${encodeURIComponent(filter.status)}`)
  if (filter.action_type) parts.push(`action_type=${encodeURIComponent(filter.action_type)}`)
  if (filter.source) parts.push(`source=${encodeURIComponent(filter.source)}`)
  if (filter.q) parts.push(`q=${encodeURIComponent(filter.q)}`)
  return parts.length ? `?${parts.join('&')}` : ''
}

/** 把 ExecListFilter 拼成 query string。 */
function buildExecQuery(filter?: ExecListFilter): string {
  if (!filter) return ''
  const parts: string[] = []
  if (filter.cron_id) parts.push(`cron_id=${encodeURIComponent(filter.cron_id)}`)
  if (filter.status) parts.push(`status=${encodeURIComponent(filter.status)}`)
  if (filter.limit) parts.push(`limit=${filter.limit}`)
  if (filter.offset) parts.push(`offset=${filter.offset}`)
  return parts.length ? `?${parts.join('&')}` : ''
}

/**
 * useCrons — Cron Store 入口。
 *
 * 暴露：
 *   - crons / loading：响应式列表与加载态
 *   - loadCrons / refreshCrons：拉取列表
 *   - getCron：从本地缓存读单条（命中则不请求）
 *   - createCron / updateCron / deleteCron：CRUD
 *   - setStatus：enable / disable / pause / resume
 *   - triggerCron：手动触发一次执行
 *   - loadExecutions / cleanExecutions：执行历史
 *   - upsertLocal / removeLocal / setExecutions：供 useCronEvents 事件回写
 */
export function useCrons() {
  const { showError } = useToast()

  /** 统计：按 status 分桶。 */
  const stats = computed(() => {
    const s = { enabled: 0, disabled: 0, paused: 0, total: crons.value.length }
    for (const c of crons.value) {
      if (c.status === 'enabled') s.enabled++
      else if (c.status === 'disabled') s.disabled++
      else if (c.status === 'paused') s.paused++
    }
    return s
  })

  /** GET /api/crons —— 拉取列表并写入本地缓存。 */
  async function loadCrons(filter?: CronListFilter): Promise<Cron[]> {
    loading.value = true
    try {
      const resp = await fetch(`/api/crons${buildListQuery(filter)}`)
      if (!resp.ok) {
        const text = await resp.text()
        throw new Error(`Failed to load crons: ${resp.status} ${text}`)
      }
      const data = (await resp.json()) as Cron[]
      crons.value = Array.isArray(data) ? data : []
      return crons.value
    } catch (err) {
      showError(err instanceof Error ? err.message : 'Failed to load crons')
      throw err
    } finally {
      loading.value = false
    }
  }

  /** 重新拉取当前列表（语义别名，便于事件回调调用）。 */
  async function refreshCrons(): Promise<void> {
    await loadCrons()
  }

  /** 从本地缓存读单条；缓存未命中时回退到 GET /api/crons/:id。 */
  async function getCron(id: string): Promise<Cron> {
    const cached = crons.value.find(c => c.id === id)
    if (cached) return cached
    const resp = await fetch(`/api/crons/${encodeURIComponent(id)}`)
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to get cron: ${resp.status} ${text}`)
    }
    return (await resp.json()) as Cron
  }

  /** POST /api/crons —— 创建；成功后乐观插入本地列表头部。 */
  async function createCron(input: CreateCronInput): Promise<Cron> {
    const resp = await fetch('/api/crons', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to create cron: ${resp.status} ${text}`)
    }
    const created = (await resp.json()) as Cron
    upsertLocal(created)
    return created
  }

  /** PUT /api/crons/:id —— 更新；成功后替换本地条目。 */
  async function updateCron(id: string, input: UpdateCronInput): Promise<Cron> {
    const resp = await fetch(`/api/crons/${encodeURIComponent(id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to update cron: ${resp.status} ${text}`)
    }
    const updated = (await resp.json()) as Cron
    upsertLocal(updated)
    return updated
  }

  /** DELETE /api/crons/:id —— 删除；成功后从本地移除。 */
  async function deleteCron(id: string): Promise<void> {
    const resp = await fetch(`/api/crons/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to delete cron: ${resp.status} ${text}`)
    }
    removeLocal(id)
    // 顺带清掉该 cron 的本地执行历史缓存。
    delete executionsByCron.value[id]
  }

  /** POST /api/crons/:id/{enable|disable|pause|resume} —— 状态切换。 */
  async function setStatus(id: string, status: CronStatus): Promise<Cron> {
    const action =
      status === 'enabled' ? 'enable' :
      status === 'disabled' ? 'disable' :
      status === 'paused' ? 'pause' :
      'resume'
    const resp = await fetch(`/api/crons/${encodeURIComponent(id)}/${action}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to ${action} cron: ${resp.status} ${text}`)
    }
    const updated = (await resp.json()) as Cron
    upsertLocal(updated)
    return updated
  }

  /** POST /api/crons/:id/trigger —— 手动触发；返回执行记录。 */
  async function triggerCron(id: string, overrideInput?: string): Promise<CronExecution> {
    const resp = await fetch(`/api/crons/${encodeURIComponent(id)}/trigger`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ override_input: overrideInput ?? '' }),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to trigger cron: ${resp.status} ${text}`)
    }
    return (await resp.json()) as CronExecution
  }

  /** GET /api/crons/:id/executions 或 GET /api/crons/executions —— 执行历史。 */
  async function loadExecutions(filter: ExecListFilter = {}): Promise<CronExecution[]> {
    const resp = filter.cron_id
      ? await fetch(`/api/crons/${encodeURIComponent(filter.cron_id)}/executions${buildExecQuery({ ...filter, cron_id: undefined })}`)
      : await fetch(`/api/crons/executions${buildExecQuery(filter)}`)
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to load executions: ${resp.status} ${text}`)
    }
    const data = (await resp.json()) as CronExecution[]
    const key = filter.cron_id ?? ''
    executionsByCron.value[key] = Array.isArray(data) ? data : []
    return executionsByCron.value[key]
  }

  /** DELETE /api/crons/executions —— 清理执行历史；成功后刷新本地缓存。 */
  async function cleanExecutions(filter: CleanExecFilter = {}): Promise<number> {
    const parts: string[] = []
    if (filter.cron_id) parts.push(`cron_id=${encodeURIComponent(filter.cron_id)}`)
    if (filter.status) parts.push(`status=${encodeURIComponent(filter.status)}`)
    const qs = parts.length ? `?${parts.join('&')}` : ''
    const resp = await fetch(`/api/crons/executions${qs}`, { method: 'DELETE' })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(`Failed to clean executions: ${resp.status} ${text}`)
    }
    const data = (await resp.json()) as { deleted: number }
    // 清掉对应缓存，下次访问会重新拉取。
    if (filter.cron_id) {
      delete executionsByCron.value[filter.cron_id]
    } else {
      executionsByCron.value = {}
    }
    return data.deleted ?? 0
  }

  // ---- 事件回写辅助（供 useCronEvents 调用，避免重复请求）----

  /** 插入或替换本地列表中的某条 cron。 */
  function upsertLocal(cron: Cron): void {
    const idx = crons.value.findIndex(c => c.id === cron.id)
    if (idx >= 0) {
      const next = [...crons.value]
      next[idx] = cron
      crons.value = next
    } else {
      crons.value = [cron, ...crons.value]
    }
  }

  /** 从本地列表移除某条 cron。 */
  function removeLocal(id: string): void {
    crons.value = crons.value.filter(c => c.id !== id)
  }

  /** 直接覆盖某 cron 的执行历史缓存（事件驱动时用）。 */
  function setExecutions(cronId: string, execs: CronExecution[]): void {
    executionsByCron.value[cronId] = execs
  }

  /** 取某 cron 的本地执行历史缓存。 */
  function executionsOf(cronId: string): CronExecution[] {
    return executionsByCron.value[cronId] || []
  }

  return {
    // state
    crons,
    loading,
    stats,
    // queries
    loadCrons,
    refreshCrons,
    getCron,
    loadExecutions,
    executionsOf,
    // mutations
    createCron,
    updateCron,
    deleteCron,
    setStatus,
    triggerCron,
    cleanExecutions,
    // event helpers
    upsertLocal,
    removeLocal,
    setExecutions,
  }
}
