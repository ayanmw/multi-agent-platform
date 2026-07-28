<!--
  InjectedSkillBadge.vue

  在 ContextWindowPanel 的 message timeline 中标记某条 system message 是否来自
  Skill 注入。hover 显示模板名，保持视觉轻量。
-->
<script setup lang="ts">
defineProps<{
  templateName: string
}>()
</script>

<template>
  <span
    class="skill-badge"
    :title="`Injected skill template: ${templateName}`"
  >
    skill
  </span>
</template>

<style scoped>
.skill-badge {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  padding: 0.125rem 0.5rem;
  border-radius: var(--radius-md, 0.375rem);
  background: hsla(260, 85%, 60%, 0.15);
  border: 1px solid hsla(260, 85%, 60%, 0.35);
  color: hsla(260, 85%, 72%, 1);
  font-size: 0.625rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  cursor: help;
}
</style>
