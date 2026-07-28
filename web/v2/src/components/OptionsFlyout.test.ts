import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import OptionsFlyout from './OptionsFlyout.vue'

// useFlyoutResize 依赖 DOM 尺寸操作；测试中 stub 掉避免布局相关副作用。
vi.mock('@/composables/useFlyoutResize', () => ({
  useFlyoutResize: () => ({
    size: { value: { width: null as number | null, height: null as number | null } },
    isResizing: { value: false },
    startResize: vi.fn(),
    resetSize: vi.fn(),
  }),
}))

const AGENTS = [
  { id: 'a-1', name: 'Alpha', model: 'deepseek-v4', tools: ['read_file'], is_default: false },
  { id: 'a-2', name: 'Beta', model: 'gpt-4o', tools: ['run_shell'], is_default: true },
  { id: 'a-3', name: 'Gamma', model: 'claude-sonnet', tools: [], is_default: false },
]

function mountOpen(props: Record<string, unknown> = {}) {
  const wrapper = mount(OptionsFlyout, {
    props: {
      open: true,
      maxSteps: 30,
      timeoutSeconds: 0,
      multiAgent: false,
      agents: AGENTS,
      availableTools: [] as { name: string; description: string }[],
      anchorRect: new DOMRect(100, 100, 40, 40),
      autoApprovalTags: [] as string[],
      ...props,
    },
    attachTo: document.body,
  })
  return wrapper
}

describe('OptionsFlyout.vue', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('emits update:modelValue with the default agent id when opened without modelValue', async () => {
    const wrapper = mountOpen()
    await nextTick()
    await nextTick()

    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    const emitted = wrapper.emitted('update:modelValue') as unknown[][]
    expect(emitted[emitted.length - 1][0]).toBe('a-2')
  })

  it('uses modelValue when provided and does not override it on open', async () => {
    const wrapper = mountOpen({ modelValue: 'a-3' })
    await nextTick()
    await nextTick()

    const emitted = wrapper.emitted('update:modelValue') as unknown[][]
    // 浮窗打开时不应覆盖外部已传入的 modelValue。
    expect(emitted).toBeFalsy()

    const select = wrapper.find('.agent-select')
    expect((select.element as HTMLSelectElement).value).toBe('a-3')
  })

  it('emits update:modelValue with the chosen agent id when selection changes', async () => {
    const wrapper = mountOpen({ modelValue: 'a-1' })
    await nextTick()
    await nextTick()

    const select = wrapper.find('.agent-select')
    await select.setValue('a-2')

    const emitted = wrapper.emitted('update:modelValue') as unknown[][]
    expect(emitted).toBeTruthy()
    expect(emitted[emitted.length - 1][0]).toBe('a-2')
  })
})
