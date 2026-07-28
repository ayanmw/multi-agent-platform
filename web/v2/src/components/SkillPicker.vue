<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue'
import type { Skill, SkillCommand, SkillPickerItem } from '../types/skill'

interface Props {
  /** 是否显示 picker */
  open: boolean
  /** 命令列表（.claude/commands） */
  commands: SkillCommand[]
  /** Skill 列表 */
  skills: Skill[]
  /** 当前选中下标 */
  selectedIndex?: number
}

const props = withDefaults(defineProps<Props>(), {
  selectedIndex: 0,
})

const emit = defineEmits<{
  /** 用户确认选择某个命令 */
  (e: 'select', item: SkillPickerItem): void
  /** 请求关闭 picker */
  (e: 'close'): void
  /** 键盘事件透传，便于父组件做 ↑/↓ 导航 */
  (e: 'keydown', evt: KeyboardEvent): void
}>()

function close() {
  emit('close')
}

const listRef = ref<HTMLElement | null>(null)
const itemRefs = ref<HTMLElement[]>([])

/** 扁平化的全部待选项：Commands 优先，再 Skills，再 Built-in Skills */
const items = computed<SkillPickerItem[]>(() => {
  const result: SkillPickerItem[] = []
  for (const cmd of props.commands || []) {
    result.push({
      kind: 'command',
      id: cmd.id,
      name: cmd.name,
      description: cmd.description,
      command: cmd,
    })
  }
  for (const skill of props.skills || []) {
    result.push({
      kind: 'skill',
      id: skill.id,
      name: skill.display_name || skill.id,
      description: skill.description,
      source: skill.source as SkillPickerItem['source'],
      state: skill.state,
      skill,
    })
  }
  return result
})

const safeIndex = computed(() => {
  const len = items.value.length
  if (!len) return -1
  return Math.max(0, Math.min(props.selectedIndex, len - 1))
})

interface Group {
  label: string
  items: SkillPickerItem[]
}

const grouped = computed<Group[]>(() => {
  const groups = new Map<string, SkillPickerItem[]>()
  for (const item of items.value) {
    // 分组策略：命令 -> Commands；skill 非 built_in -> Skills；skill built_in -> Built-in
    let label = 'Unknown'
    if (item.kind === 'command') {
      label = 'Commands'
    } else if (item.skill?.source === 'built_in') {
      label = 'Built-in'
    } else {
      label = 'Skills'
    }
    if (!groups.has(label)) groups.set(label, [])
    groups.get(label)!.push(item)
  }

  const labelOrder = ['Commands', 'Skills', 'Built-in']
  const result: Group[] = []
  for (const label of labelOrder) {
    const groupItems = groups.get(label)
    if (groupItems) result.push({ label, items: groupItems })
  }
  return result
})

function select(item: SkillPickerItem) {
  emit('select', item)
}

function onKeydown(e: KeyboardEvent) {
  emit('keydown', e)
  if (!props.open) return
  if (e.key === 'Escape') {
    e.preventDefault()
    close()
    return
  }
  if (e.key === 'Enter') {
    e.preventDefault()
    const item = items.value[safeIndex.value]
    if (item) select(item)
    return
  }
}

function onBlur(e: FocusEvent) {
  // 失焦到 picker 外部时关闭。
  const target = e.relatedTarget as HTMLElement | null
  if (!listRef.value?.contains(target)) {
    emit('close')
  }
}

watch(safeIndex, () => {
  nextTick(() => {
    const el = itemRefs.value[safeIndex.value]
    el?.scrollIntoView({ block: 'nearest' })
  })
})

watch(
  () => props.open,
  (open) => {
    if (open) {
      nextTick(() => {
        itemRefs.value[safeIndex.value]?.focus()
      })
    }
  },
)
</script>

<template>
  <div
    v-if="open && items.length"
    ref="listRef"
    class="skill-picker"
    tabindex="-1"
    @keydown="onKeydown"
    @blur="onBlur"
  >
    <div class="skill-picker-header">
      <span class="skill-picker-title">Skills & Commands</span>
      <span class="skill-picker-hint">↑↓ Enter · Esc close</span>
    </div>
    <template v-for="group in grouped" :key="group.label">
      <div class="skill-picker-group">{{ group.label }}</div>
      <div
        v-for="(item, idx) in group.items"
        :key="item.id + '-' + item.kind"
        ref="(el) => { if (el) itemRefs[items.indexOf(item)] = el as HTMLElement }"
        class="skill-picker-item"
        :class="{ active: safeIndex === items.indexOf(item) }"
        tabindex="0"
        @click="select(item)"
      >
        <div class="skill-picker-id">
          <span v-if="item.kind === 'command'" class="kind-badge kind-badge--command">/</span>
          <span v-else-if="item.source === 'built_in'" class="kind-badge kind-badge--built-in">★</span>
          <span v-else class="kind-badge kind-badge--skill">S</span>
          /{{ item.id }}
        </div>
        <div class="skill-picker-name">{{ item.name }}</div>
        <div v-if="item.description" class="skill-picker-desc">{{ item.description }}</div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.skill-picker {
  position: absolute;
  bottom: calc(100% + 8px);
  left: 0;
  right: 0;
  max-height: 320px;
  overflow-y: auto;
  background: var(--bg-panel, #11141a);
  border: 1px solid var(--border-default, rgba(255, 255, 255, 0.1));
  border-radius: var(--radius-md, 10px);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
  z-index: 60;
  outline: none;
}

.skill-picker-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-bottom: 1px solid var(--border-default, rgba(255, 255, 255, 0.08));
  color: var(--text-muted, #5c6675);
  font-size: 0.7rem;
  font-family: var(--font-mono, monospace);
  text-transform: uppercase;
}

.skill-picker-group {
  padding: 4px 12px;
  color: var(--accent-skill, #ff6b35);
  font-size: 0.65rem;
  font-family: var(--font-mono, monospace);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.skill-picker-item {
  padding: 8px 12px;
  cursor: pointer;
  border-left: 3px solid transparent;
  transition: background 0.12s;
}

.skill-picker-item:hover,
.skill-picker-item.active {
  background: rgba(255, 107, 53, 0.08);
  border-left-color: var(--accent-skill, #ff6b35);
}

.skill-picker-id {
  font-family: var(--font-mono, monospace);
  font-size: 0.75rem;
  color: var(--accent-skill, #ff6b35);
  display: flex;
  align-items: center;
  gap: 4px;
}

.kind-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  font-size: 0.65rem;
  border-radius: 3px;
  flex-shrink: 0;
}

.kind-badge--command {
  color: var(--accent-running, #00e5ff);
  background: rgba(0, 229, 255, 0.12);
}

.kind-badge--built-in {
  color: var(--accent-warning, #ffb800);
  background: rgba(255, 184, 0, 0.12);
}

.kind-badge--skill {
  color: var(--text-muted, #5c6675);
  background: var(--bg-elevated);
}

.skill-picker-name {
  font-weight: 600;
  color: var(--text-primary, #e8ebf0);
  font-size: 0.85rem;
}

.skill-picker-desc {
  color: var(--text-secondary, #9aa3b2);
  font-size: 0.75rem;
  margin-top: 2px;
}
</style>
