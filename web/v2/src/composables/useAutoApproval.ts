import { ref, computed, watch } from 'vue'
import { useWebSocket } from './useWebSocket'

/**
 * Auto-approval policy store.
 *
 * «自动审批» 的策略保存在浏览器 LocalStorage 中，键为 `map_v2_auto_approval_tags`。
 * 只有用户显式选中的标签才会让审批自动通过；空集合表示关闭自动审批。
 *
 * 判定规则（前后端一致）：
 *  - 仅当 rule === 'TagPolicyRule'
 *  - approval tags 非空
 *  - approval 的每一个 tag 都出现在 configuredTags 中
 */

// 所有可作为候选的审批标签。低风险标签默认选中；高风险标签允许用户勾选，但 UI 会给出警告。
export const AUTO_APPROVAL_TAG_OPTIONS = [
  { tag: 'network', label: 'Network', description: '网络请求、web_search 等', risk: 'low' },
  { tag: 'mcp', label: 'MCP', description: 'MCP 工具调用', risk: 'low' },
  { tag: 'exec', label: 'Exec', description: '命令执行类工具', risk: 'high' },
  { tag: 'exec:dangerous', label: 'Exec Dangerous', description: 'rm -rf、sudo 等危险命令', risk: 'high' },
  { tag: 'shell', label: 'Shell', description: '通用 shell 执行', risk: 'high' },
  { tag: 'shell:dangerous', label: 'Shell Dangerous', description: '危险 shell 模式', risk: 'high' },
  { tag: 'filesystem:write', label: 'Write File', description: '文件写入', risk: 'high' },
  { tag: 'filesystem:delete', label: 'Delete File', description: '文件删除', risk: 'high' },
  { tag: 'filesystem:destructive', label: 'Destructive FS', description: '破坏性文件操作', risk: 'high' },
] as const

export const LOW_RISK_AUTO_APPROVAL_TAGS = new Set<string>(['network', 'mcp'])
export const HIGH_RISK_AUTO_APPROVAL_TAGS = new Set<string>([
  'exec', 'exec:dangerous', 'shell', 'shell:dangerous',
  'filesystem:destructive', 'filesystem:delete', 'filesystem:write',
])

const STORAGE_KEY = 'map_v2_auto_approval_tags'

function loadPersistedTags(): string[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return Array.from(LOW_RISK_AUTO_APPROVAL_TAGS)
    const parsed = JSON.parse(raw)
    if (Array.isArray(parsed) && parsed.every(t => typeof t === 'string')) {
      // 过滤掉不再支持的 tag
      const valid = new Set<string>(AUTO_APPROVAL_TAG_OPTIONS.map(o => o.tag))
      return parsed.filter((t: string) => valid.has(t))
    }
  } catch {
    // ignore corrupt storage
  }
  return Array.from(LOW_RISK_AUTO_APPROVAL_TAGS)
}

export function persistTags(tags: string[]) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(tags))
  } catch {
    // ignore storage errors (e.g. private mode)
  }
}

const selectedTags = ref<string[]>(loadPersistedTags())

// 初始化 WebSocket 一次，用于把自动审批标签同步到后端。
let wsControlInitialized = false
function ensureControlInit() {
  if (wsControlInitialized) return
  wsControlInitialized = true
  // useWebSocket 在模块级返回共享实例；此处只能安全地在 runtime 调用 connect。
  const { sendControl } = useWebSocket()
  sendControl({
    action: 'set_auto_approval_tags',
    task_id: '',
    agent_id: '',
    tags: selectedTags.value,
  })
}

watch(selectedTags, (tags) => {
  persistTags(tags)
  ensureControlInit()
  const { sendControl } = useWebSocket()
  sendControl({
    action: 'set_auto_approval_tags',
    task_id: '',
    agent_id: '',
    tags,
  })
}, { deep: true })

export const autoApprovalEnabled = computed(() => selectedTags.value.length > 0)

/** 当前配置的标签集合（Set，用于快速判定） */
export function getAutoApprovalTagSet(): Set<string> {
  return new Set(selectedTags.value)
}

/**
 * 判断一个 approval_required 请求是否应该被自动批准。
 *
 * @param rule - 触发审批的规则名，如 'TagPolicyRule'、'ApprovalRule'
 * @param tags - 工具携带的风险/能力标签
 * @param configuredTags - 用户已开启自动审批的标签集合
 */
export function shouldAutoApprove(rule: string | undefined, tags: string[] | undefined, configuredTags: Set<string>): boolean {
  if (rule !== 'TagPolicyRule') return false
  const actualTags = tags || []
  if (actualTags.length === 0) return false
  return actualTags.every(tag => configuredTags.has(tag))
}

export function useAutoApproval() {
  function toggleTag(tag: string) {
    const idx = selectedTags.value.indexOf(tag)
    if (idx >= 0) {
      selectedTags.value.splice(idx, 1)
    } else {
      selectedTags.value.push(tag)
    }
  }

  function selectAll() {
    selectedTags.value = AUTO_APPROVAL_TAG_OPTIONS.map(o => o.tag)
  }

  function clearAll() {
    selectedTags.value = []
  }

  return {
    selectedTags,
    options: AUTO_APPROVAL_TAG_OPTIONS,
    enabled: autoApprovalEnabled,
    toggleTag,
    selectAll,
    clearAll,
    shouldAutoApprove: (rule?: string, tags?: string[]) => shouldAutoApprove(rule, tags, getAutoApprovalTagSet()),
  }
}
