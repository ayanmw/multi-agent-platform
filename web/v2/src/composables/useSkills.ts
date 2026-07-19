/**
 * useSkills.ts
 *
 * Skill 系统前端复用逻辑：维护可用 skill 列表，提供 TypeScript 类型与触发入口。
 * 当前为占位实现，未来可接入 GET /api/skills/search 与后端执行通道。
 */

/** 单个 Skill 的元数据 */
export interface Skill {
  id: string
  name: string
  command: string
  description: string
}

/** 内置 skill 静态列表 */
export function useSkills() {
  const skills: Skill[] = [
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
  ]

  /**
   * triggerSkill — 触发一个 skill 命令。
   *
   * 当前仅打印日志并返回 Promise.resolve；
   * TODO: Phase 7 接入后端执行通道（WebSocket / REST）。
   */
  async function triggerSkill(command: string): Promise<void> {
    // eslint-disable-next-line no-console
    console.log('[useSkills] trigger:', command)
    // TODO: Phase 7 — 调用后端 skill 执行 API
  }

  return {
    skills,
    triggerSkill,
  }
}
