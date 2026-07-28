/**
 * useSkillCommands.ts
 *
 * SkillCommand 前端复用逻辑：加载、搜索、刷新 .claude/commands 命令列表。
 */

import { computed, ref } from 'vue'
import type { InvokeSkillCommandResult, SkillCommand, SkillCommandDetail } from '../types/skill'

const commands = ref<SkillCommand[]>([])
const loading = ref(false)
const query = ref('')
const error = ref<string | null>(null)

export function useSkillCommands() {
  const loadCommands = async (workdir?: string): Promise<void> => {
    loading.value = true
    error.value = null
    try {
      const params = new URLSearchParams()
      if (workdir) {
        params.set('workdir', workdir)
      }
      const resp = await fetch(`/api/skill-commands?${params.toString()}`)
      if (!resp.ok) {
        throw new Error(`failed to load commands: ${resp.status}`)
      }
      const data = (await resp.json()) as { commands?: SkillCommand[] }
      commands.value = data.commands ?? []
    } catch (err) {
      error.value = err instanceof Error ? err.message : String(err)
      console.warn('[useSkillCommands] load failed:', error.value)
    } finally {
      loading.value = false
    }
  }

  const filteredCommands = computed(() => {
    const q = query.value.trim().toLowerCase()
    if (!q) return commands.value
    return commands.value.filter(
      (c) =>
        c.id.toLowerCase().includes(q) ||
        c.name.toLowerCase().includes(q) ||
        c.description.toLowerCase().includes(q) ||
        c.tags.some((t) => t.toLowerCase().includes(q)),
    )
  })

  const getCommandDetail = async (id: string): Promise<SkillCommandDetail | null> => {
    try {
      const resp = await fetch(`/api/skill-commands/${encodeURIComponent(id)}`)
      if (!resp.ok) {
        throw new Error(`command detail failed: ${resp.status}`)
      }
      return (await resp.json()) as SkillCommandDetail
    } catch (err) {
      console.warn('[useSkillCommands] detail failed:', err)
      return null
    }
  }

  const invokeCommand = async (
    id: string,
    workdir?: string,
  ): Promise<InvokeSkillCommandResult> => {
    const resp = await fetch(`/api/skill-commands/${encodeURIComponent(id)}/invoke`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workdir }),
    })
    if (!resp.ok) {
      const text = await resp.text()
      throw new Error(text || `invoke failed: ${resp.status}`)
    }
    return (await resp.json()) as InvokeSkillCommandResult
  }

  return {
    commands,
    loading,
    query,
    error,
    loadCommands,
    filteredCommands,
    getCommandDetail,
    invokeCommand,
  }
}
