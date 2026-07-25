import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { nextTick, ref } from 'vue'
import App from './App.vue'

function setupMainStage(wrapper: ReturnType<typeof mount<typeof App>>) {
  const main = wrapper.find('main.main-stage').element as HTMLElement
  Object.defineProperty(main, 'scrollBy', {
    value: vi.fn(),
    configurable: true,
    writable: true,
  })
  main.style.height = '200px'
  main.style.overflowY = 'auto'
  main.innerHTML = '<div style="height:2000px"></div>'
  Object.defineProperty(main, 'clientHeight', { value: 200, configurable: true })
  Object.defineProperty(main, 'scrollHeight', { value: 2200, configurable: true })
  Object.defineProperty(main, 'scrollTop', { value: 0, configurable: true, writable: true })
  return main
}

function dispatchWheel(target: HTMLElement, deltaY: number) {
  target.dispatchEvent(new WheelEvent('wheel', { deltaY, bubbles: true }))
}

// App.vue 依赖单例 composables，我们需要 stub 它们以避免 WS/路由/Store 副作用。
const mockLayout = {
  isMobile: ref(false),
  isTablet: ref(false),
  isDesktop: ref(true),
  leftDockOpen: ref(true),
  rightFilesOpen: ref(true),
  rightCronOpen: ref(false),
  activeMobileTab: ref('stage'),
  mobileMoreOpen: ref(false),
  isCommandBarVisible: ref(true),
  leftDockWidth: ref(220),
  rightFilesWidth: ref(260),
  rightZoneMax: ref(540),
  canOpenCron: ref(true),
  commandAreaHeight: ref(64),
  setLeftDockWidth: vi.fn(),
  setRightFilesWidth: vi.fn(),
  setCommandAreaHeight: vi.fn(),
  commitWidths: vi.fn(),
  commitCommandHeight: vi.fn(),
  resetWidths: vi.fn(),
  toggleLeftDock: vi.fn(),
  toggleRightFiles: vi.fn(),
  toggleRightCron: vi.fn(() => true),
  setRightCronOpen: vi.fn(),
}

const mockTaskStore = {
  taskCache: ref({}),
  activeTaskId: ref(''),
  isTaskPending: ref(false),
  wsStatus: ref('disconnected'),
  lastUserInput: ref(''),
  pendingApproval: ref(null),
  connect: vi.fn(),
  disconnect: vi.fn(),
  startTask: vi.fn(),
  startTurn: vi.fn(),
  startTaskWithCase: vi.fn(),
  startMultiAgentTask: vi.fn(),
  clearActiveTask: vi.fn(),
  setActiveTaskId: vi.fn(),
  loadSessionTurns: vi.fn(),
  pruneOrphanTasks: vi.fn(),
  clearCacheForSession: vi.fn(),
  pauseTask: vi.fn(),
  resumeTask: vi.fn(),
  cancelTask: vi.fn(),
  approveTask: vi.fn(),
  denyTask: vi.fn(),
}

const mockSessionStore = {
  sessions: ref([]),
  activeSessionId: ref(''),
  activeSession: ref(null),
  loadSessions: vi.fn(),
  createSession: vi.fn(),
  setActiveSession: vi.fn(),
  deleteSession: vi.fn(),
  updateSessionFields: vi.fn(),
}

const mockAgentStore = { agents: ref([]), availableTools: ref([]), loadAgents: vi.fn() }
const mockProjectStore = { projects: ref([]), activeProjectId: ref(''), loadProjects: vi.fn(), setActiveProject: vi.fn() }
const mockToast = { toasts: ref([]), showError: vi.fn(), showInfo: vi.fn(), dismissToast: vi.fn() }
const mockSkills = { loadSkills: vi.fn(), enableSkill: vi.fn() }
const mockTheme = { theme: ref('dark') }
const mockRecentMods = { items: ref([]), toggle: vi.fn(), clear: vi.fn() }
const mockContextWindow = { setActiveTaskId: vi.fn(), clear: vi.fn() }
const mockSessionFiles = { setActiveSession: vi.fn(), refreshDir: vi.fn() }
const mockTodoStore = { activeCount: vi.fn(() => 0), highPriorityCount: vi.fn(() => 0) }
const mockCaseStore = {}

vi.mock('@/composables/useLayout', () => ({ useLayout: () => mockLayout }))
vi.mock('@/composables/useTaskStore', () => ({ useTaskStore: () => mockTaskStore }))
vi.mock('@/composables/useSessionStore', () => ({ useSessionStore: () => mockSessionStore }))
vi.mock('@/composables/useAgentStore', () => ({ useAgentStore: () => mockAgentStore }))
vi.mock('@/composables/useProjectStore', () => ({ useProjectStore: () => mockProjectStore }))
vi.mock('@/composables/useToast', () => ({ useToast: () => mockToast }))
vi.mock('@/composables/useSkills', () => ({ useSkills: () => mockSkills }))
vi.mock('@/composables/useTheme', () => ({ useTheme: () => mockTheme }))
vi.mock('@/composables/useRecentMods', () => ({ useRecentMods: () => mockRecentMods }))
vi.mock('@/composables/useContextWindow', () => ({ useContextWindow: () => mockContextWindow }))
vi.mock('@/composables/useSessionFiles', () => ({ useSessionFiles: () => mockSessionFiles }))
vi.mock('@/composables/useTodoStore', () => ({ useTodoStore: () => mockTodoStore }))
vi.mock('@/composables/useCaseStore', () => ({ useCaseStore: () => mockCaseStore }))
vi.mock('@/composables/useKeyboard', () => ({
  useKeyboard: () => ({ isRunning: ref(false), showTips: ref(false) }),
  SHORTCUTS: [],
}))

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('App.vue — 全局滚轮事件', () => {
  it('鼠标悬停在 context-flyout 的 timeline 上滚动时，不应调用主舞台 scrollBy', async () => {
    const wrapper = mount(App, { attachTo: document.body })
    await flushPromises()
    await nextTick()

    const main = setupMainStage(wrapper)

    // 构造一个 context-flyout 内的 timeline（由于 App 默认不渲染，直接注入模拟元素）。
    const flyout = document.createElement('div')
    flyout.className = 'context-flyout'
    const timeline = document.createElement('div')
    timeline.className = 'timeline'
    timeline.style.height = '100px'
    timeline.style.overflowY = 'auto'
    timeline.innerHTML = '<div style="height:1000px"></div>'
    flyout.appendChild(timeline)
    document.body.appendChild(flyout)

    const scrollBySpy = vi.spyOn(main, 'scrollBy')

    // 在 timeline 内部派发滚轮事件。
    dispatchWheel(timeline, 50)
    await nextTick()

    expect(scrollBySpy).not.toHaveBeenCalled()

    document.body.removeChild(flyout)
    wrapper.unmount()
  })

  it('鼠标悬停在 context-flyout-body 上滚动时，不应调用主舞台 scrollBy', async () => {
    const wrapper = mount(App, { attachTo: document.body })
    await flushPromises()
    await nextTick()

    const main = setupMainStage(wrapper)

    const flyout = document.createElement('div')
    flyout.className = 'context-flyout'
    const body = document.createElement('div')
    body.className = 'context-flyout-body'
    body.style.height = '100px'
    body.style.overflowY = 'auto'
    body.innerHTML = '<div style="height:1000px"></div>'
    flyout.appendChild(body)
    document.body.appendChild(flyout)

    const scrollBySpy = vi.spyOn(main, 'scrollBy')
    dispatchWheel(body, 50)
    await nextTick()

    expect(scrollBySpy).not.toHaveBeenCalled()

    document.body.removeChild(flyout)
    wrapper.unmount()
  })

  it('鼠标悬停在 prompt 详情弹窗上滚动时，不应调用主舞台 scrollBy', async () => {
    const wrapper = mount(App, { attachTo: document.body })
    await flushPromises()
    await nextTick()

    const main = setupMainStage(wrapper)

    const overlay = document.createElement('div')
    overlay.className = 'prompt-dialog-overlay'
    const panel = document.createElement('div')
    panel.className = 'prompt-dialog-panel'
    panel.style.height = '100px'
    panel.style.overflowY = 'auto'
    panel.innerHTML = '<div style="height:1000px"></div>'
    overlay.appendChild(panel)
    document.body.appendChild(overlay)

    const scrollBySpy = vi.spyOn(main, 'scrollBy')
    dispatchWheel(panel, 50)
    await nextTick()

    expect(scrollBySpy).not.toHaveBeenCalled()

    document.body.removeChild(overlay)
    wrapper.unmount()
  })
})
