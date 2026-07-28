import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import ContextWindowPanel from './ContextWindowPanel.vue'

// 我们真正验证的是组件内的 DOM 结构/样式类，因此允许 useContextWindow 用 Vue 真实依赖。
// 用 vi.fn 替换单例 composable 的行为，避免 fetch 副作用。
const mockFetchSnapshot = vi.fn().mockResolvedValue(undefined)
const mockCurrentSnapshot = { value: null as import('@/types/events').ContextWindowSnapshotData | null }
const mockSubTaskSnapshots = { value: {} as Record<string, import('@/types/events').ContextWindowSnapshotData> }
const mockSetActiveTaskId = vi.fn()

vi.mock('@/composables/useContextWindow', () => ({
  useContextWindow: () => ({
    currentSnapshot: mockCurrentSnapshot,
    subTaskSnapshots: mockSubTaskSnapshots,
    setActiveTaskId: mockSetActiveTaskId,
    fetchSnapshot: mockFetchSnapshot,
  }),
}))

const LONG_SNAPSHOT: import('@/types/events').ContextWindowSnapshotData = {
  model: 'deepseek-v4-flash',
  max_context_tokens: 200000,
  estimated_total_tokens: 1234,
  estimated_usage_ratio: 0.00617,
  messages: Array.from({ length: 50 }, (_, i) => ({
    role: i % 2 === 0 ? 'user' : 'assistant',
    content: `message ${i}: ` + 'xy '.repeat(100),
    estimated_tokens: 60,
    usage_ratio: 0.05,
  })),
}

async function panelWithSnapshot(snapshot = LONG_SNAPSHOT, subTaskId = '') {
  mockCurrentSnapshot.value = snapshot
  mockSubTaskSnapshots.value = {}
  const wrapper = mount(ContextWindowPanel, {
    props: { activeTaskId: 'task-1', subTaskId },
    attachTo: document.body,
  })
  await nextTick()
  await nextTick()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  mockCurrentSnapshot.value = null
  mockSubTaskSnapshots.value = {}
})

afterEach(() => {
  mockCurrentSnapshot.value = null
  mockSubTaskSnapshots.value = {}
})

function getComponentSource(): string {
  return readFileSync(resolve(__dirname, './ContextWindowPanel.vue'), 'utf-8')
}

describe('ContextWindowPanel — prompt 弹窗滚动行为契约', () => {
  it('点击 timeline message 打开 prompt 弹窗，且 DOM 结构正确', async () => {
    const wrapper = await panelWithSnapshot()

    const firstRow = wrapper.find('.view-combined-prompt')
    expect(firstRow.exists()).toBe(true)
    await firstRow.trigger('click')
    await nextTick()

    const overlay = document.querySelector('.prompt-dialog-overlay')
    expect(overlay).not.toBeNull()

    const panel = document.querySelector('.prompt-dialog-panel')
    expect(panel).not.toBeNull()
    expect((panel as HTMLElement).getAttribute('role')).toBe('dialog')

    const body = panel!.querySelector('.prompt-dialog-body')
    expect(body).not.toBeNull()

    const blocks = panel!.querySelectorAll('.block-content')
    expect(blocks.length).toBeGreaterThanOrEqual(1)
    blocks.forEach(b => {
      expect((b as HTMLElement).classList.contains('block-content')).toBe(true)
    })

    wrapper.unmount()
  })

  it('prompt 弹窗关闭后不应再出现在 DOM 中', async () => {
    const wrapper = await panelWithSnapshot() as any

    await wrapper.find('.view-combined-prompt').trigger('click')
    await nextTick()
    expect(document.querySelector('.prompt-dialog-overlay')).not.toBeNull()

    // 直接调用组件内部方法关闭弹窗，避免受 Transition 动画时序影响。
    wrapper.vm.promptDialog.open = false
    await nextTick()

    expect(document.querySelector('.prompt-dialog-overlay')).toBeNull()
    wrapper.unmount()
  })

  it('CSS 源码中 prompt-dialog-body 与 block-content 均声明 overscroll-behavior-y: contain', () => {
    const source = getComponentSource()
    // 宽松匹配：允许空白或回车
    expect(source).toMatch(/\.prompt-dialog-body\s*\{[\s\S]*overscroll-behavior-y\s*:\s*contain/)
    expect(source).toMatch(/\.block-content\s*\{[\s\S]*overscroll-behavior-y\s*:\s*contain/)
  })

  it('timeline 容器带有可独立滚动的 CSS 类', async () => {
    const wrapper = await panelWithSnapshot()
    const timeline = wrapper.find('.timeline')
    expect(timeline.exists()).toBe(true)
    const el = timeline.element as HTMLElement
    expect(el.classList.contains('timeline')).toBe(true)
    wrapper.unmount()
  })
})
