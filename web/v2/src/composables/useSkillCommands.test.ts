import { describe, it, expect, vi, beforeEach } from 'vitest'
import { nextTick } from 'vue'
import { useSkillCommands } from './useSkillCommands'

describe('useSkillCommands', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    global.fetch = vi.fn()
  })

  it('loads commands from backend', async () => {
    const fresh = useSkillCommands()
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        commands: [
          { id: 'ops:new', name: 'New', description: '', scope: 'project', workspace_dir: '/p', project_id: '', source_path: '', skill_id: '', tags: [], icon: '' },
        ],
      }),
    } as Response)

    await fresh.loadCommands('/p')
    expect(fresh.commands.value).toHaveLength(1)
    expect(fresh.commands.value[0].id).toBe('ops:new')
  })

  it('filters commands by query', async () => {
    const fresh = useSkillCommands()
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        commands: [
          { id: 'ops:new', name: 'New', description: '', scope: 'project', workspace_dir: '/p', project_id: '', source_path: '', skill_id: '', tags: [], icon: '' },
          { id: 'ops:fix', name: 'Fix', description: '', scope: 'project', workspace_dir: '/p', project_id: '', source_path: '', skill_id: '', tags: [], icon: '' },
        ],
      }),
    } as Response)

    await fresh.loadCommands('/p')
    expect(fresh.filteredCommands.value.length).toBeGreaterThan(0)
    fresh.query.value = 'fix'
    await nextTick()
    expect(fresh.filteredCommands.value.map((c) => c.id)).toEqual(['ops:fix'])
  })

  it('invokes command', async () => {
    const fresh = useSkillCommands()
    vi.mocked(global.fetch).mockResolvedValueOnce({
      ok: true,
      json: async () => ({ enabled_skill_ids: [], temporary_skill_id: 'cmd:ops:new' }),
    } as Response)

    const result = await fresh.invokeCommand('ops:new', '/p')
    expect(result.temporary_skill_id).toBe('cmd:ops:new')
  })
})
