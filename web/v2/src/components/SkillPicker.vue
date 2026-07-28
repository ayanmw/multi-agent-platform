<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue'
import type { SkillCommand } from '../types/skill'

interface Props {
  /** 是否显示 picker */
  open: boolean
  /** 待选命令列表 */
  commands: SkillCommand[]
  /** 当前选中下标 */
  selectedIndex?: number
}

const props = withDefaults(defineProps<Props>(), {
  selectedIndex: 0,
})

const emit = defineEmits<{
  /** 用户确认选择某个命令 */
  (e: 'select', cmd: SkillCommand): void
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

const safeIndex = computed(() => {
  if (!props.commands.length) return -1
  return Math.max(0, Math.min(props.selectedIndex, props.commands.length - 1))
})

const grouped = computed(() => {
  const groups = new Map<string, SkillCommand[]>()
  for (const cmd of props.commands) {
    const g = cmd.scope === 'global' ? 'Global' : 'Project'
    if (!groups.has(g)) groups.set(g, [])
    groups.get(g)!.push(cmd)
  }
  const result: { label: string; items: SkillCommand[] }[] = []
  for (const [label, items] of groups) {
    result.push({ label, items })
  }
  return result
})

function select(cmd: SkillCommand) {
  emit('select', cmd)
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
    const cmd = props.commands[safeIndex.value]
    if (cmd) select(cmd)
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
    v-if="open && commands.length"
    ref="listRef"
    class="skill-picker"
    tabindex="-1"
    @keydown="onKeydown"
    @blur="onBlur"
  >
    <div class="skill-picker-header">
      <span class="skill-picker-title">Commands</span>
      <span class="skill-picker-hint">↑↓ Enter · Esc close</span>
    </div>
    <template v-for="group in grouped" :key="group.label">
      <div class="skill-picker-group">{{ group.label }}</div>
      <div
        v-for="(cmd, idx) in group.items"
        :key="cmd.id"
        ref="(el) => { if (el) itemRefs[commands.indexOf(cmd)] = el as HTMLElement }"
        class="skill-picker-item"
        :class="{ active: safeIndex === commands.indexOf(cmd) }"
        tabindex="0"
        @click="select(cmd)"
      >
        <div class="skill-picker-id">/{{ cmd.id }}</div>
        <div class="skill-picker-name">{{ cmd.name }}</div>
        <div v-if="cmd.description" class="skill-picker-desc">{{ cmd.description }}</div>
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
