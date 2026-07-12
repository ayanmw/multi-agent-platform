<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="dialogVisible" class="recent-mods-overlay" @click.self="close">
        <div class="recent-mods-dialog">
          <div class="recent-mods-header">
            <h3>🕐 最近修改</h3>
            <span class="recent-mods-count">{{ countLabel }}</span>
            <button class="recent-mods-close" @click="close" title="关闭">✕</button>
          </div>

          <div v-if="sortedItems.length === 0" class="recent-mods-empty">
            <span class="recent-mods-empty-icon">📭</span>
            <p>暂无文件修改记录</p>
            <p class="recent-mods-empty-hint">使用 write_file 工具时，修改记录会自动出现在这里</p>
          </div>

          <div v-else class="recent-mods-list">
            <div
              v-for="item in sortedItems"
              :key="item.key"
              class="recent-mods-item"
            >
              <span class="recent-mods-icon">{{ item.success ? '✅' : '❌' }}</span>
              <span class="recent-mods-path" :title="item.path" @click="handleJump(item)">{{ item.path }}</span>
              <span class="recent-mods-meta">
                <span class="recent-mods-time">{{ formatTime(item.timestamp) }}</span>
                <span v-if="item.bytes !== undefined" class="recent-mods-bytes">{{ item.bytes }}B</span>
              </span>
            </div>
          </div>

          <div class="recent-mods-footer">
            <button class="recent-mods-clear" @click="handleClear" v-if="sortedItems.length > 0">
              清空记录
            </button>
            <button class="recent-mods-close-btn" @click="close">关闭</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch, computed, onMounted, onUnmounted } from 'vue'
import type { RecentModification } from '../composables/useRecentMods'

const props = defineProps<{
  visible: boolean
  items: RecentModification[]
}>()

const emit = defineEmits<{
  'update:visible': [v: boolean]
  clear: []
}>()

const dialogVisible = ref(props.visible)
watch(() => props.visible, v => { dialogVisible.value = v })
watch(dialogVisible, v => { emit('update:visible', v) })

const sortedItems = computed(() => [...props.items].sort((a, b) => b.timestamp - a.timestamp))

const countLabel = computed(() => {
  const n = sortedItems.value.length
  return n > 0 ? `${n} 条记录` : '暂无记录'
})

function close() {
  dialogVisible.value = false
}

function handleClear() {
  emit('clear')
}

function handleJump(item: RecentModification) {
  if (item.sessionId) {
    const encoded = encodeURIComponent(item.path)
    window.open(`/s/${item.sessionId}/${encoded}`, '_blank')
  } else {
    navigator.clipboard?.writeText(item.path).catch(() => {})
  }
  close()
}

/** Format a unix-ms timestamp into a short human label. */
function formatTime(ts: number): string {
  const d = new Date(ts)
  try {
    if (d.toLocaleDateString === undefined) return d.toISOString()
    const now = new Date()
    const isToday = d.getFullYear() === now.getFullYear()
      && d.getMonth() === now.getMonth()
      && d.getDate() === now.getDate()
    const yesterday = new Date(now)
    yesterday.setDate(yesterday.getDate() - 1)
    const isYesterday = d.getFullYear() === yesterday.getFullYear()
      && d.getMonth() === yesterday.getMonth()
      && d.getDate() === yesterday.getDate()
    if (isToday) {
      return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
    }
    if (isYesterday) {
      return '昨天 ' + d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
    }
    return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
      + ' ' + d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  } catch {
    return d.toISOString()
  }
}

// Escape key closes dialog
onMounted(() => {
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'Escape' && dialogVisible.value) {
      e.preventDefault()
      close()
    }
  }
  window.addEventListener('keydown', onKey)
  onUnmounted(() => window.removeEventListener('keydown', onKey))
})
</script>

<style scoped>
.recent-mods-overlay {
  position: fixed;
  inset: 0;
  z-index: 900;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
}

.recent-mods-dialog {
  background: #1e1e2e;
  border: 1px solid #313244;
  border-radius: 12px;
  width: min(560px, 92vw);
  max-height: 70vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.4);
}

.recent-mods-header {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 14px 18px;
  border-bottom: 1px solid #313244;
  user-select: none;
}

.recent-mods-header h3 {
  margin: 0;
  font-size: 15px;
  color: #cdd6f4;
  font-weight: 600;
}

.recent-mods-count {
  flex: 1;
  font-size: 12px;
  color: #6c7086;
}

.recent-mods-close {
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: #a6adc8;
  font-size: 16px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background 0.15s;
}

.recent-mods-close:hover {
  background: #313244;
  color: #cdd6f4;
}

.recent-mods-empty {
  padding: 40px 20px;
  text-align: center;
  color: #6c7086;
}

.recent-mods-empty-icon {
  font-size: 40px;
  display: block;
  margin-bottom: 10px;
}

.recent-mods-empty p {
  margin: 4px 0;
  font-size: 14px;
}

.recent-mods-empty-hint {
  font-size: 12px;
  color: #585b70;
}

.recent-mods-list {
  overflow-y: auto;
  flex: 1;
  padding: 6px 0;
  max-height: 50vh;
}

.recent-mods-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 18px;
  transition: background 0.1s;
}

.recent-mods-item:hover {
  background: #181825;
}

.recent-mods-icon {
  font-size: 14px;
  flex-shrink: 0;
}

.recent-mods-path {
  flex: 1;
  font-size: 13px;
  font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
  color: #a6e3a1;
  cursor: pointer;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  transition: color 0.15s;
}

.recent-mods-path:hover {
  color: #94e2d5;
  text-decoration: underline;
}

.recent-mods-meta {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 2px;
  flex-shrink: 0;
}

.recent-mods-time {
  font-size: 11px;
  color: #6c7086;
  white-space: nowrap;
}

.recent-mods-bytes {
  font-size: 11px;
  color: #585b70;
}

.recent-mods-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 18px;
  border-top: 1px solid #313244;
}

.recent-mods-clear {
  background: transparent;
  border: 1px solid #45475a;
  color: #a6adc8;
  border-radius: 6px;
  padding: 6px 14px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.15s;
}

.recent-mods-clear:hover {
  background: #313244;
  color: #cdd6f4;
}

.recent-mods-close-btn {
  background: #89b4fa;
  border: none;
  color: #1e1e2e;
  border-radius: 6px;
  padding: 6px 18px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: opacity 0.15s;
}

.recent-mods-close-btn:hover {
  opacity: 0.85;
}

/* Fade transition */
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
