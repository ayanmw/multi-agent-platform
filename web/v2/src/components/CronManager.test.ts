/**
 * CronManager 组件测试
 *
 * 覆盖点：
 * - 挂载时调用 loadCrons 拉取列表
 * - 顶部"+ 新建"按钮打开 CronForm（formVisible=true，formCron=null）
 * - 行操作"编辑"打开 CronForm（formCron=该 cron）
 * - 行操作 enable/disable/pause/resume 调用 useCrons.setStatus
 * - "触发"调用 useCrons.triggerCron
 * - "删除"调用 useCrons.deleteCron
 * - "历史"切换执行历史抽屉显隐
 * - 状态过滤与搜索过滤
 * - CronForm @save 新建 → createCron；编辑 → updateCron
 * - CronForm @close 关闭表单
 *
 * 通过 stub CronForm / CronExecutions + mock useCrons 隔离子组件与网络。
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h, ref } from 'vue'
import type { Cron, CronExecution } from '@/types/cron'

// --- mock useCrons / useCronEvents / useToast ---
// 用模块级 ref 持有 spy，便于断言；每次 test 用 resetModules 重建模块状态。
let cronStoreMock: Record<string, ReturnType<typeof vi.fn> & { mockReset: () => void }> & {
  crons: ReturnType<typeof ref<Cron[]>>
}

const useCronsMock = vi.fn(() => cronStoreMock)
const useCronEventsMock = vi.fn(() => ({ cronEvents: ref([]), stats: ref({}), clear: vi.fn(), filter: vi.fn() }))
const useToastMock = vi.fn(() => ({
  showError: vi.fn(),
  showInfo: vi.fn(),
  dismissToast: vi.fn(),
  toasts: { value: [] },
}))

vi.mock('@/composables/useCrons', () => ({ useCrons: useCronsMock }))
vi.mock('@/composables/useCronEvents', () => ({ useCronEvents: useCronEventsMock }))
vi.mock('@/composables/useToast', () => ({ useToast: useToastMock }))
vi.mock('@/composables/useAgentStore', () => ({
  useAgentStore: () => ({
    agents: ref([]),
    availableTools: ref([]),
    loadAgents: vi.fn(async () => {}),
    loadAvailableTools: vi.fn(async () => {}),
  }),
}))

// CronForm stub：暴露 save/close 按钮
const CronFormStub = defineComponent({
  name: 'CronForm',
  props: { cronData: Object, visible: Boolean },
  emits: ['close', 'save'],
  setup(props, { emit }) {
    return () =>
      props.visible
        ? h('div', { 'data-testid': 'cron-form' }, [
            h('button', {
              class: 'cf-save-new',
              onClick: () => emit('save', { name: 'New', schedule_type: 'interval', action_type: 'notify_session', action_payload: {} }),
            }, 'save-new'),
            h('button', {
              class: 'cf-save-edit',
              onClick: () => emit('save', { name: 'Updated' }),
            }, 'save-edit'),
            h('button', { class: 'cf-close', onClick: () => emit('close') }, 'close'),
          ])
        : null
  },
})

const CronExecutionsStub = defineComponent({
  name: 'CronExecutions',
  props: { cronId: String, visible: Boolean },
  render: () => h('div', { 'data-testid': 'cron-executions' }, 'executions'),
})

function makeCron(overrides: Partial<Cron> = {}): Cron {
  return {
    id: 'cron_a', name: 'A', description: '', schedule_type: 'interval',
    cron_expr: '1h', display_type: 'interval', timezone: '', once_at: '',
    action_type: 'notify_session', action_payload: {}, status: 'enabled',
    allow_concurrent: false, source: 'user', owner: '',
    last_triggered_at: null, next_trigger_at: null, last_execution_id: '',
    trigger_count: 0, created_at: 0, updated_at: 0,
    ...overrides,
  }
}

function freshStore() {
  return {
    crons: ref<Cron[]>([]),
    loading: ref(false),
    stats: ref({ enabled: 0, disabled: 0, paused: 0, total: 0 }),
    loadCrons: vi.fn(async () => { /* mock */ }),
    refreshCrons: vi.fn(async () => {}),
    getCron: vi.fn(async () => makeCron()),
    loadExecutions: vi.fn(async () => []),
    executionsOf: vi.fn(() => []),
    createCron: vi.fn(async (input: unknown) => ({ ...makeCron({ id: 'cron_new' }), ...(input as object) }) as Cron),
    updateCron: vi.fn(async (_id: string, input: unknown) => ({ ...makeCron(), ...(input as object) }) as Cron),
    deleteCron: vi.fn(async () => {}),
    setStatus: vi.fn(async (_id: string, status: string) => makeCron({ status: status as Cron['status'] })),
    triggerCron: vi.fn(async () => ({ id: 'exec_1', status: 'completed' }) as unknown as CronExecution),
    cleanExecutions: vi.fn(async () => 0),
    upsertLocal: vi.fn(),
    removeLocal: vi.fn(),
    setExecutions: vi.fn(),
  }
}

async function mountManager(initialCrons: Cron[] = [makeCron()]) {
  vi.resetModules()
  cronStoreMock = freshStore() as never
  cronStoreMock.crons.value = initialCrons
  useCronsMock.mockClear()
  useCronEventsMock.mockClear()
  useToastMock.mockClear()

  const CronManager = (await import('./CronManager.vue')).default
  const wrapper = mount(CronManager, {
    global: {
      stubs: {
        CronForm: CronFormStub,
        CronExecutions: CronExecutionsStub,
      },
    },
  })
  await flushPromises()
  return { wrapper, store: cronStoreMock }
}

beforeEach(() => {
  vi.resetModules()
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('CronManager — 加载与列表', () => {
  it('挂载时调用 loadCrons', async () => {
    const { store } = await mountManager()
    expect(store.loadCrons).toHaveBeenCalled()
  })

  it('渲染 cron 行', async () => {
    const { wrapper } = await mountManager([makeCron({ id: 'cron_a', name: 'Alpha' }), makeCron({ id: 'cron_b', name: 'Beta' })])
    const rows = wrapper.findAll('.cron-row')
    // 第一行是 head
    expect(rows.length).toBe(3)
    expect(wrapper.text()).toContain('Alpha')
    expect(wrapper.text()).toContain('Beta')
  })

  it('空列表显示空态', async () => {
    const { wrapper } = await mountManager([])
    expect(wrapper.find('.cron-empty').exists()).toBe(true)
  })
})

describe('CronManager — 新建 / 编辑表单', () => {
  it('点 + 新建 打开 CronForm，formCron=null', async () => {
    const { wrapper } = await mountManager()
    await wrapper.find('.cron-new-btn').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cron-form"]').exists()).toBe(true)
    expect(wrapper.findComponent(CronFormStub).props('cronData')).toBeNull()
  })

  it('点 编辑 打开 CronForm 并传入该 cron', async () => {
    const { wrapper } = await mountManager([makeCron({ id: 'cron_a', name: 'Alpha' })])
    // 跳过 head 行
    const editBtn = wrapper.findAll('button.op-btn').find(b => b.text() === '编辑')!
    await editBtn.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cron-form"]').exists()).toBe(true)
    expect(wrapper.findComponent(CronFormStub).props('cronData')).toMatchObject({ id: 'cron_a' })
  })

  it('CronForm save（新建）→ createCron + 关闭', async () => {
    const { wrapper, store } = await mountManager()
    await wrapper.find('.cron-new-btn').trigger('click')
    await flushPromises()
    await wrapper.find('.cf-save-new').trigger('click')
    await flushPromises()
    expect(store.createCron).toHaveBeenCalled()
    expect(store.updateCron).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="cron-form"]').exists()).toBe(false)
  })

  it('CronForm save（编辑）→ updateCron + 关闭', async () => {
    const { wrapper, store } = await mountManager([makeCron({ id: 'cron_a' })])
    await wrapper.findAll('button.op-btn').find(b => b.text() === '编辑')!.trigger('click')
    await flushPromises()
    await wrapper.find('.cf-save-edit').trigger('click')
    await flushPromises()
    expect(store.updateCron).toHaveBeenCalledWith('cron_a', expect.objectContaining({ name: 'Updated' }))
    expect(store.createCron).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="cron-form"]').exists()).toBe(false)
  })

  it('CronForm close → 关闭表单', async () => {
    const { wrapper, store } = await mountManager()
    await wrapper.find('.cron-new-btn').trigger('click')
    await flushPromises()
    await wrapper.find('.cf-close').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cron-form"]').exists()).toBe(false)
    expect(store.createCron).not.toHaveBeenCalled()
  })
})

describe('CronManager — 行操作', () => {
  it('点 暂停 → setStatus(id, paused)', async () => {
    const { wrapper, store } = await mountManager([makeCron({ id: 'cron_a', status: 'enabled' })])
    const pauseBtn = wrapper.findAll('button.op-btn').find(b => b.text() === '暂停')!
    await pauseBtn.trigger('click')
    await flushPromises()
    expect(store.setStatus).toHaveBeenCalledWith('cron_a', 'paused')
  })

  it('点 触发 → triggerCron(id)', async () => {
    const { wrapper, store } = await mountManager([makeCron({ id: 'cron_a' })])
    const triggerBtn = wrapper.findAll('button.op-btn').find(b => b.text() === '触发')!
    await triggerBtn.trigger('click')
    await flushPromises()
    expect(store.triggerCron).toHaveBeenCalledWith('cron_a')
  })

  it('点 删除 → deleteCron(id)', async () => {
    const { wrapper, store } = await mountManager([makeCron({ id: 'cron_a' })])
    const delBtn = wrapper.findAll('button.op-btn').find(b => b.text() === '删除')!
    await delBtn.trigger('click')
    await flushPromises()
    expect(store.deleteCron).toHaveBeenCalledWith('cron_a')
  })

  it('点 历史 → 展开执行历史抽屉', async () => {
    const { wrapper } = await mountManager([makeCron({ id: 'cron_a' })])
    const histBtn = wrapper.findAll('button.op-btn').find(b => b.text() === '历史')!
    await histBtn.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cron-executions"]').exists()).toBe(true)
    expect(wrapper.findComponent(CronExecutionsStub).props('cronId')).toBe('cron_a')
  })

  it('再次点 历史 → 收起抽屉', async () => {
    const { wrapper } = await mountManager([makeCron({ id: 'cron_a' })])
    const histBtn = () => wrapper.findAll('button.op-btn').find(b => b.text() === '历史')!
    await histBtn().trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cron-executions"]').exists()).toBe(true)
    await histBtn().trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="cron-executions"]').exists()).toBe(false)
  })
})

describe('CronManager — 过滤', () => {
  it('按状态过滤', async () => {
    const { wrapper } = await mountManager([
      makeCron({ id: 'a', name: 'A', status: 'enabled' }),
      makeCron({ id: 'b', name: 'B', status: 'paused' }),
    ])
    const select = wrapper.find('.cron-filter-select')
    await select.setValue('paused')
    await flushPromises()
    const rows = wrapper.findAll('.cron-row').filter(r => !r.classes('cron-row--head'))
    expect(rows.length).toBe(1)
    expect(wrapper.text()).toContain('B')
    expect(wrapper.text()).not.toContain('A')
  })

  it('按名称搜索', async () => {
    const { wrapper } = await mountManager([
      makeCron({ id: 'a', name: 'Alpha' }),
      makeCron({ id: 'b', name: 'Beta' }),
    ])
    await wrapper.find('.cron-filter-input').setValue('alp')
    await flushPromises()
    const rows = wrapper.findAll('.cron-row').filter(r => !r.classes('cron-row--head'))
    expect(rows.length).toBe(1)
    expect(wrapper.text()).toContain('Alpha')
  })
})
