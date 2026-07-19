<!-- SkillPicker — 通过 `/` 触发的 Skill 搜索悬浮面板

     数据流：
       TaskInput 监听 textarea 输入，发现 `/` 触发字符后挂载本组件
       → 组件调用 GET /api/skills/search?q={keyword} 拉取匹配列表
       → 用户用 ↑/↓ 选择、Enter 确认、Esc 关闭
       → 选中后 emit('select', skill)，由父组件把 `/skill-id ` 填入输入框

     为什么独立成组件：
       Skill 检索与键盘导航逻辑较复杂，独立后可被任意输入控件复用
       (未来 WorkflowEditor、CaseForm 也可能需要)。同时让 TaskInput
       专注于"发送任务"这一核心职责，符合白盒设计的职责分离原则。
-->
<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, nextTick } from 'vue'

/** 后端返回的 Skill 对象，字段对齐 internal/skill.Skill 的 JSON tag。 */
export interface Skill {
  id: string
  version?: string
  display_name: string
  description: string
  authors?: string[]
  tags?: string[]
  source: string
  state: string
  is_local_editable?: boolean
}

const props = defineProps<{
  /** 当前搜索关键词（已去掉前导 `/`）。空字符串表示列出全部。 */
  query: string
  /** 是否可见。父组件控制挂载/卸载。 */
  visible: boolean
}>()

const emit = defineEmits<{
  /** 用户选中某个 skill。父组件据此把 `/skill-id ` 填入输入框。 */
  select: [skill: Skill]
  /** 用户取消（Esc 或点击外部）。 */
  cancel: []
}>()

const skills = ref<Skill[]>([])
const loading = ref(false)
const error = ref('')
const selectedIndex = ref(0)

// 搜索节流句柄。避免每个按键都打一次后端，减轻 registry 锁竞争。
let debounceTimer: ReturnType<typeof setTimeout> | null = null
// 当前活跃的 fetch controller，用于丢弃过期响应。
let abortController: AbortController | null = null

/** 根据 query 拉取匹配的 skill 列表。空 query 返回全部。 */
async function fetchSkills(q: string) {
  // 取消上一次未完成的请求，避免乱序响应覆盖最新结果。
  if (abortController) {
    abortController.abort()
  }
  abortController = new AbortController()
  loading.value = true
  error.value = ''
  try {
    const url = `/api/skills/search?q=${encodeURIComponent(q)}`
    const resp = await fetch(url, { signal: abortController.signal })
    if (!resp.ok) {
      error.value = `搜索失败: ${resp.status} ${resp.statusText}`
      skills.value = []
      return
    }
    const data = (await resp.json()) as Skill[]
    skills.value = Array.isArray(data) ? data : []
    // 列表变化后重置高亮到第一项，避免越界。
    selectedIndex.value = 0
  } catch (err) {
    // abort 是预期行为，不算错误。
    if ((err as Error).name === 'AbortError') return
    error.value = err instanceof Error ? err.message : '网络错误'
    skills.value = []
  } finally {
    loading.value = false
  }
}

/** 节流封装：300ms 内的连续输入只触发一次后端请求。 */
function scheduleFetch(q: string) {
  if (debounceTimer) {
    clearTimeout(debounceTimer)
  }
  debounceTimer = setTimeout(() => {
    fetchSkills(q)
  }, 300)
}

// 监听 query 变化触发节流搜索。immediate=true 让面板首次弹出时立即列出全部。
watch(
  () => props.query,
  (q) => {
    if (!props.visible) return
    scheduleFetch(q)
  },
  { immediate: true },
)

// 面板刚挂载时主动拉一次，覆盖 query 未变化的初始场景。
watch(
  () => props.visible,
  (v) => {
    if (v) {
      scheduleFetch(props.query)
    }
  },
)

/** 确认当前选中项。 */
function confirmSelection() {
  const s = skills.value[selectedIndex.value]
  if (s) {
    emit('select', s)
  }
}

/** 移动高亮索引；wrap-around 让上下键形成循环。 */
function moveSelection(delta: number) {
  const n = skills.value.length
  if (n === 0) return
  selectedIndex.value = (selectedIndex.value + delta + n) % n
  // 确保高亮项可见。
  nextTick(() => {
    const el = document.querySelector('.skill-item.selected') as HTMLElement | null
    if (el) {
      el.scrollIntoView({ block: 'nearest' })
    }
  })
}

/** 父组件通过 ref 调用：把键盘事件转交面板处理。
 *  返回 true 表示事件已被消费（父组件应阻止默认行为）。 */
function handleKeydown(e: KeyboardEvent): boolean {
  if (!props.visible) return false
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault()
      moveSelection(1)
      return true
    case 'ArrowUp':
      e.preventDefault()
      moveSelection(-1)
      return true
    case 'Enter':
      e.preventDefault()
      confirmSelection()
      return true
    case 'Escape':
      e.preventDefault()
      emit('cancel')
      return true
    default:
      return false
  }
}

defineExpose({ handleKeydown })

onMounted(() => {
  scheduleFetch(props.query)
})

onUnmounted(() => {
  if (debounceTimer) {
    clearTimeout(debounceTimer)
  }
  if (abortController) {
    abortController.abort()
  }
})

/** 把 source 枚举转成中文标签，便于用户理解来源。 */
function sourceLabel(src: string): string {
  switch (src) {
    case 'built_in':
      return '内置'
    case 'local_file':
      return '本地文件'
    case 'local_db':
      return '本地数据库'
    case 'market':
      return '集市'
    case 'mcp':
      return 'MCP'
    default:
      return src
  }
}
</script>

<template>
  <div v-if="visible" class="skill-picker" @click.self="emit('cancel')">
    <div class="skill-picker-header">
      <span class="skill-picker-title">🎯 Skill 搜索</span>
      <span class="skill-picker-hint">↑↓ 选择 · Enter 确认 · Esc 取消</span>
    </div>

    <!-- 加载中 -->
    <div v-if="loading" class="skill-picker-empty">搜索中...</div>

    <!-- 错误 -->
    <div v-else-if="error" class="skill-picker-error">
      ⚠️ {{ error }}
    </div>

    <!-- 空结果 -->
    <div v-else-if="skills.length === 0" class="skill-picker-empty">
      没有匹配的 Skill。输入 `/` 后跟关键词可继续搜索。
    </div>

    <!-- 列表 -->
    <ul v-else class="skill-list">
      <li
        v-for="(s, i) in skills"
        :key="s.id"
        class="skill-item"
        :class="{ selected: i === selectedIndex }"
        @mouseenter="selectedIndex = i"
        @click="() => { selectedIndex = i; confirmSelection() }"
      >
        <div class="skill-item-main">
          <span class="skill-id">/{{ s.id }}</span>
          <span class="skill-name">{{ s.display_name }}</span>
        </div>
        <div class="skill-item-desc">{{ s.description || '（无描述）' }}</div>
        <div class="skill-item-meta">
          <span class="skill-source" :class="`src-${s.source}`">{{ sourceLabel(s.source) }}</span>
          <span v-if="s.state" class="skill-state" :class="s.state">{{ s.state }}</span>
        </div>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.skill-picker {
  position: absolute;
  bottom: 100%;
  left: 0;
  right: 0;
  margin-bottom: 6px;
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.6);
  max-height: 320px;
  display: flex;
  flex-direction: column;
  z-index: 50;
  overflow: hidden;
}

.skill-picker-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 12px;
  border-bottom: 1px solid #333;
  background: #252525;
  flex-shrink: 0;
}

.skill-picker-title {
  font-size: 12px;
  font-weight: 600;
  color: #d4d4d4;
}

.skill-picker-hint {
  font-size: 10px;
  color: #888;
}

.skill-picker-empty,
.skill-picker-error {
  padding: 16px 12px;
  font-size: 12px;
  color: #aaa;
  text-align: center;
}

.skill-picker-error {
  color: #ff8a80;
}

.skill-list {
  list-style: none;
  margin: 0;
  padding: 4px 0;
  overflow-y: auto;
  flex: 1;
}

.skill-item {
  padding: 8px 12px;
  cursor: pointer;
  transition: background 0.12s;
  border-left: 2px solid transparent;
}

.skill-item:hover,
.skill-item.selected {
  background: rgba(74, 158, 255, 0.12);
  border-left-color: #4a9eff;
}

.skill-item-main {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.skill-id {
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 12px;
  color: #4a9eff;
  font-weight: 600;
}

.skill-name {
  font-size: 12px;
  color: #d4d4d4;
  font-weight: 500;
}

.skill-item-desc {
  font-size: 11px;
  color: #999;
  margin-top: 2px;
  line-height: 1.4;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.skill-item-meta {
  display: flex;
  gap: 6px;
  margin-top: 4px;
  align-items: center;
}

.skill-source,
.skill-state {
  font-size: 9px;
  text-transform: uppercase;
  font-weight: 600;
  padding: 1px 5px;
  border-radius: 8px;
  background: #333;
  color: #aaa;
}

.skill-state.enabled {
  background: rgba(81, 207, 102, 0.2);
  color: #51cf66;
}

.skill-state.disabled {
  background: rgba(231, 76, 60, 0.2);
  color: #e74c3c;
}

.skill-source.src-built_in {
  background: rgba(74, 158, 255, 0.2);
  color: #4a9eff;
}
</style>
