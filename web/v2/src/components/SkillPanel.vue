/**
 * SkillPanel.vue
 *
 * Skill 系统可视化入口。展示可用 skill 卡片，每个卡片可点击 Run 触发；
 * 同时提供手动输入框，允许直接键入 skill 命令并触发。
 */
<script setup lang="ts">
import { ref } from 'vue'
import type { Skill } from '@/composables/useSkills'

interface Props {
  /** Skill 列表，默认使用 useSkills 中返回的静态列表 */
  skills?: Skill[]
}

const props = withDefaults(defineProps<Props>(), {
  skills: () => [],
})

const emit = defineEmits<{
  (e: 'trigger', command: string): void
}>()

const manualInput = ref('')

function runSkill(command: string) {
  emit('trigger', command)
}

function runManual() {
  const cmd = manualInput.value.trim()
  if (!cmd) return
  emit('trigger', cmd)
  manualInput.value = ''
}

const displaySkills = props.skills.length
  ? props.skills
  : ([
      {
        id: 'graphify',
        name: 'Graphify',
        command: '/graphify',
        description: '将任意输入转换为结构化知识图谱。',
      },
      {
        id: 'verify',
        name: 'Verify',
        command: '/verify',
        description: '对当前上下文中的声明进行多源核验。',
      },
      {
        id: 'research',
        name: 'Research',
        command: '/research',
        description: '深度研究并生成带引用的综合报告。',
      },
    ] as Skill[])
</script>

<template>
  <div class="skill-panel">
    <div class="skill-header">
      <h3 class="skill-title">Skills</h3>
      <p class="skill-subtitle">触发可复用 prompt 包</p>
    </div>

    <ul class="skill-list">
      <li
        v-for="skill in displaySkills"
        :key="skill.id"
        class="skill-card"
      >
        <div class="skill-card-main">
          <div class="skill-card-name">{{ skill.name }}</div>
          <code class="skill-card-command">{{ skill.command }}</code>
          <div class="skill-card-desc">{{ skill.description }}</div>
        </div>
        <button
          type="button"
          class="skill-run-btn focus-glow"
          @click="runSkill(skill.command)"
        >
          Run
        </button>
      </li>
    </ul>

    <div class="skill-manual">
      <label for="skill-manual-input" class="skill-manual-label">Manual command</label>
      <div class="skill-manual-row">
        <input
          id="skill-manual-input"
          v-model="manualInput"
          type="text"
          class="skill-manual-input focus-glow"
          placeholder="/graphify ..."
          @keydown.enter.prevent="runManual"
        />
        <button
          type="button"
          class="skill-run-btn focus-glow"
          @click="runManual"
        >
          Run
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.skill-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-md);
  height: 100%;
}

.skill-header {
  padding-bottom: var(--space-sm);
  border-bottom: 1px solid var(--border-subtle);
}

.skill-title {
  margin: 0;
  font-family: var(--font-display);
  font-size: 1rem;
  font-weight: 600;
  color: var(--text-primary);
  text-transform: uppercase;
  letter-spacing: 0.04em;
}

.skill-subtitle {
  margin: var(--space-xs) 0 0;
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.75rem;
}

.skill-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-sm);
}

.skill-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-md);
  padding: var(--space-sm) var(--space-md);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  background: var(--bg-elevated);
  transition:
    border-color var(--transition-fast),
    background var(--transition-fast);
}

.skill-card:hover {
  border-color: var(--accent-skill);
  background: rgba(255, 107, 53, 0.05);
}

.skill-card-main {
  min-width: 0;
}

.skill-card-name {
  font-family: var(--font-display);
  font-weight: 600;
  color: var(--text-primary);
}

.skill-card-command {
  display: inline-block;
  margin-top: 2px;
  color: var(--accent-skill);
  font-size: 0.75rem;
}

.skill-card-desc {
  margin-top: var(--space-xs);
  color: var(--text-secondary);
  font-size: 0.75rem;
  line-height: 1.4;
}

.skill-run-btn {
  flex-shrink: 0;
  padding: 6px 14px;
  border: 1px solid var(--accent-skill);
  border-radius: var(--radius-sm);
  background: rgba(255, 107, 53, 0.1);
  color: var(--accent-skill);
  font-family: var(--font-display);
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  transition: all var(--transition-fast);
}

.skill-run-btn:hover {
  background: var(--accent-skill);
  color: var(--bg-canvas);
}

.skill-manual {
  margin-top: auto;
  padding-top: var(--space-md);
  border-top: 1px solid var(--border-subtle);
}

.skill-manual-label {
  display: block;
  margin-bottom: var(--space-xs);
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: 0.7rem;
  text-transform: uppercase;
}

.skill-manual-row {
  display: flex;
  gap: var(--space-sm);
}

.skill-manual-input {
  flex: 1;
  padding: var(--space-sm) var(--space-md);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-sm);
  background: var(--bg-canvas);
  color: var(--text-primary);
  font-family: var(--font-mono);
  font-size: 0.85rem;
}

.skill-manual-input::placeholder {
  color: var(--text-muted);
}

.skill-manual-input:focus {
  border-color: var(--accent-skill);
  outline: none;
}
</style>
