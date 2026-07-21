/**
 * ManageContent 集成测试
 *
 * 覆盖 Cases tab 的状态机与子组件事件冒泡：
 * - 点击 CaseCard view → 打开 CaseDetailModal
 * - CaseDetailModal @run → emit('run-case') 到上层 + 关闭 detail
 * - CaseDetailModal @edit → 打开 CaseForm、关闭 detail
 * - "+ New Case" → 打开 CaseForm (formCase=null)
 * - CaseForm @save (新建) → caseStore.createCase + 关闭 form
 * - CaseForm @save (编辑) → caseStore.updateCase(id) + 关闭 form
 * - CaseCard @run 直接冒泡为 run-case
 *
 * 通过 stub 子组件 + 直接驱动 stub emit 来隔离 CaseDetailModal/CaseForm 内部，
 * 专注验证 ManageContent 的编排逻辑。CaseStore 用真实模块 + fetch mock。
 */
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import type { Case } from '@/types/case'

// --- Stub 子组件 ---
// CaseCard：渲染一个 button，点击触发指定事件，便于模拟用户交互
const CaseCardStub = defineComponent({
  name: 'CaseCard',
  props: { caseData: Object, disabled: Boolean },
  emits: ['run', 'view', 'edit', 'delete', 'toggle-tag'],
  setup(_, { emit }) {
    return () =>
      h('div', { class: 'case-card-stub', 'data-testid': 'case-card' }, [
        h('button', { class: 'stub-run', onClick: () => emit('run', _.caseData?.id) }, 'run'),
        h('button', { class: 'stub-view', onClick: () => emit('view', _.caseData?.id) }, 'view'),
        h('button', { class: 'stub-edit', onClick: () => emit('edit', _.caseData?.id) }, 'edit'),
        h('button', { class: 'stub-delete', onClick: () => emit('delete', _.caseData?.id) }, 'delete'),
      ])
  },
})

// CaseDetailModal：暴露按钮触发 close/run/edit
const CaseDetailModalStub = defineComponent({
  name: 'CaseDetailModal',
  props: { caseData: Object, visible: Boolean },
  emits: ['close', 'run', 'edit'],
  setup(props, { emit }) {
    return () =>
      props.visible
        ? h('div', { 'data-testid': 'detail-modal' }, [
            h('button', { class: 'dm-run', onClick: () => emit('run', (props.caseData as Case | null)?.id) }, 'run'),
            h('button', { class: 'dm-edit', onClick: () => emit('edit', (props.caseData as Case | null)?.id) }, 'edit'),
            h('button', { class: 'dm-close', onClick: () => emit('close') }, 'close'),
          ])
        : null
  },
})

// CaseForm：暴露按钮触发 close/save
const CaseFormStub = defineComponent({
  name: 'CaseForm',
  props: { caseData: Object, visible: Boolean },
  emits: ['close', 'save'],
  setup(props, { emit }) {
    return () =>
      props.visible
        ? h('div', { 'data-testid': 'form-modal' }, [
            h('button', {
              class: 'fm-save',
              onClick: () => emit('save', { name: 'New', category: 'go' }),
            }, 'save'),
            h('button', { class: 'fm-close', onClick: () => emit('close') }, 'close'),
          ])
        : null
  },
})

// CaseFilter / 其它 tab 内容 stub 成空
const CaseFilterStub = defineComponent({
  name: 'CaseFilter',
  emits: ['toggle-tag', 'set-category', 'clear-filters'],
  render: () => h('div', { class: 'case-filter-stub' }),
})

const EmptyStub = defineComponent({ name: 'Empty', render: () => null })

// ManageTabs stub：渲染默认 slot，并提供一个按钮把 activeTab 切到 cases，
// 便于测试驱动 v-model 切换。通过 emit('update:activeTab', 'cases') 通知父级。
const ManageTabsStub = defineComponent({
  name: 'ManageTabs',
  props: { activeTab: { type: String, default: 'memory' } },
  emits: ['update:activeTab'],
  setup(_, { slots, emit }) {
    return () =>
      h('div', { class: 'manage-tabs-stub' }, [
        h('button', {
          class: 'tabs-goto-cases',
          onClick: () => emit('update:activeTab', 'cases'),
        }, 'go-cases'),
        slots.default?.(),
      ])
  },
})

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }) as unknown as Response
}

const CASE_A: Case = {
  id: 'a', name: 'A', description: '', icon: '', category: 'go',
  system_prompt: '', default_input: '', tags: [], is_builtin: true,
  created_at: '', updated_at: '', contract: { goal: '', scope: '', max_steps: 10 },
}

async function mountInspector() {
  vi.resetModules()
  // 先 doMock useToast，再 resetModules 后 import 会拿到 mocked 版本
  vi.doMock('@/composables/useToast', () => ({
    useToast: () => ({
      showError: vi.fn(),
      showInfo: vi.fn(),
      dismissToast: vi.fn(),
      toasts: { value: [] },
    }),
  }))

  // fetch：loadCases GET 返回 [CASE_A]；createCase POST；updateCase PUT；deleteCase DELETE
  const fetchCalls: Array<{ url: string; init?: RequestInit }> = []
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    fetchCalls.push({ url, init })
    const method = init?.method?.toUpperCase() || 'GET'
    if (url === '/api/cases' && method === 'GET') return jsonResponse([CASE_A])
    if (url === '/api/cases' && method === 'POST') return jsonResponse({ ...CASE_A, id: 'new1' })
    if (url.startsWith('/api/cases/') && method === 'PUT') return jsonResponse(CASE_A)
    if (url.startsWith('/api/cases/') && method === 'DELETE') return jsonResponse({})
    return jsonResponse({})
  }) as unknown as typeof globalThis.fetch

  // 动态 import 拿到应用了 doMock 的 ManageContent
  const ManageContent = (await import('./ManageContent.vue')).default
  const useCaseStore = (await import('@/composables/useCaseStore')).useCaseStore
  const store = useCaseStore()
  await store.loadCases()
  // fetch mock 已触发 reload，清除调用记录，便于后续断言 CRUD 的网络请求
  fetchCalls.length = 0

  const wrapper = mount(ManageContent, {
    global: {
      stubs: {
        CaseCard: CaseCardStub,
        CaseDetailModal: CaseDetailModalStub,
        CaseForm: CaseFormStub,
        CaseFilter: CaseFilterStub,
        ManageTabs: ManageTabsStub,
        MemoryBrowser: EmptyStub,
        RAGPreviewPanel: EmptyStub,
        ContextWindowPanel: EmptyStub,
        AgentConfig: EmptyStub,
        ProjectConfig: EmptyStub,
        SkillPanel: EmptyStub,
        CronManager: EmptyStub,
      },
    },
  })
  // 切到 cases tab：通过 stub 提供的按钮触发 update:activeTab
  await wrapper.find('.tabs-goto-cases').trigger('click')
  await flushPromises()
  return { wrapper, store, fetchCalls }
}

beforeEach(() => {
  vi.resetModules()
  globalThis.fetch = vi.fn() as unknown as typeof globalThis.fetch
  vi.spyOn(window, 'confirm').mockReturnValue(true)
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.doUnmock('@/composables/useToast')
})
describe('ManageContent — Cases tab 状态机', () => {
  it('CaseCard view → 打开 CaseDetailModal', async () => {
    const { wrapper } = await mountInspector()
    await wrapper.find('.stub-view').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="detail-modal"]').exists()).toBe(true)
  })

  it('CaseDetailModal run → emit run-case + 关闭 detail', async () => {
    const { wrapper } = await mountInspector()
    await wrapper.find('.stub-view').trigger('click')
    await flushPromises()
    await wrapper.find('.dm-run').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('run-case')).toBeTruthy()
    expect(wrapper.emitted('run-case')![0]).toEqual(['a'])
    expect(wrapper.find('[data-testid="detail-modal"]').exists()).toBe(false)
  })

  it('CaseDetailModal edit → 打开 CaseForm、关闭 detail', async () => {
    const { wrapper } = await mountInspector()
    await wrapper.find('.stub-view').trigger('click')
    await flushPromises()
    await wrapper.find('.dm-edit').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="detail-modal"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="form-modal"]').exists()).toBe(true)
  })

  it('CaseCard edit → 直接打开 CaseForm', async () => {
    const { wrapper } = await mountInspector()
    await wrapper.find('.stub-edit').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="form-modal"]').exists()).toBe(true)
  })

  it('CaseCard run → emit run-case 到上层', async () => {
    const { wrapper } = await mountInspector()
    await wrapper.find('.stub-run').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('run-case')![0]).toEqual(['a'])
  })

  it('+ New Case → 打开 CaseForm，save 后调用 createCase 并关闭', async () => {
    const { wrapper, fetchCalls } = await mountInspector()
    await wrapper.find('.case-new-btn').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="form-modal"]').exists()).toBe(true)

    await wrapper.find('.fm-save').trigger('click')
    await flushPromises()
    // 用网络层断言 POST /api/cases
    const postCall = fetchCalls.find(c => c.init?.method === 'POST')
    expect(postCall).toBeTruthy()
    expect(postCall!.url).toBe('/api/cases')
    expect(wrapper.find('[data-testid="form-modal"]').exists()).toBe(false)
    // createCase 内部 POST 后会 GET reload
    expect(fetchCalls.some(c => !c.init?.method || c.init?.method === 'GET')).toBe(true)
  })

  it('CaseCard edit 后 save → 调用 updateCase(id)', async () => {
    const { wrapper, fetchCalls } = await mountInspector()
    await wrapper.find('.stub-edit').trigger('click')
    await flushPromises()
    await wrapper.find('.fm-save').trigger('click')
    await flushPromises()
    // 用网络层断言 PUT /api/cases/a（spy 在新模块实例上对组件无效）
    const putCall = fetchCalls.find(c => c.init?.method === 'PUT')
    expect(putCall).toBeTruthy()
    expect(putCall!.url).toBe('/api/cases/a')
    expect(wrapper.find('[data-testid="form-modal"]').exists()).toBe(false)
  })

  it('CaseCard delete → 调用 caseStore.deleteCase', async () => {
    const { wrapper, fetchCalls } = await mountInspector()
    await wrapper.find('.stub-delete').trigger('click')
    await flushPromises()
    const delCall = fetchCalls.find(c => c.init?.method === 'DELETE')
    expect(delCall).toBeTruthy()
    expect(delCall!.url).toBe('/api/cases/a')
  })

  it('CaseForm close → 关闭 form 不调用 CRUD', async () => {
    const { wrapper, store } = await mountInspector()
    await wrapper.find('.case-new-btn').trigger('click')
    await flushPromises()
    const createSpy = vi.spyOn(store, 'createCase')
    await wrapper.find('.fm-close').trigger('click')
    await flushPromises()
    expect(createSpy).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="form-modal"]').exists()).toBe(false)
  })
})
