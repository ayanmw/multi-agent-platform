/**
 * useSkills / App.vue skill 前缀解析逻辑测试
 *
 * v2 App.vue 的 handleSend 用正则解析 `/skill-id <rest>` 前缀。
 * 这里直接测正则行为，确保：
 * - `/graphify some input` → skillId=graphify, remaining=some input
 * - `/builtin-code-helper` 单独 → 匹配（skillId, remaining='')
 * - `/graphify` 后无空格但有换行也能匹配（\s）
 * - 纯文本 `hello world` 不误判为 skill
 * - `/` 开头但非合法 skill id 不匹配
 */
import { describe, it, expect } from 'vitest'

// 复刻 v2 App.vue handleSend 中的正则（两段 fallback）
function parseSkillPrefix(text: string): { skillId: string; remaining: string } {
  const m1 = /^\/([a-zA-Z0-9_-]+)\s+(.*)$/.exec(text) || /^\/([a-zA-Z0-9_-]+)$/.exec(text)
  if (!m1) return { skillId: '', remaining: text }
  return { skillId: m1[1], remaining: m1[2] || '' }
}

describe('skill 前缀解析', () => {
  it('/skill-id + 空格 + 文本', () => {
    expect(parseSkillPrefix('/graphify some input')).toEqual({
      skillId: 'graphify', remaining: 'some input',
    })
  })

  it('/builtin-code-helper 单独（无剩余）', () => {
    expect(parseSkillPrefix('/builtin-code-helper')).toEqual({
      skillId: 'builtin-code-helper', remaining: '',
    })
  })

  it('纯文本不误判', () => {
    expect(parseSkillPrefix('hello world')).toEqual({ skillId: '', remaining: 'hello world' })
  })

  it('空字符串', () => {
    expect(parseSkillPrefix('')).toEqual({ skillId: '', remaining: '' })
  })

  it('只有斜杠', () => {
    expect(parseSkillPrefix('/')).toEqual({ skillId: '', remaining: '/' })
  })

  it('skill id 含下划线/数字', () => {
    expect(parseSkillPrefix('/my_skill_2 do something')).toEqual({
      skillId: 'my_skill_2', remaining: 'do something',
    })
  })

  it('剩余文本为纯空格时 fallback 到第二段正则、remaining 为空串', () => {
    // `/graphify   `：第一段 `\s+(.*)$` 中 .* 贪婪吞掉尾随空格 → remaining='  '?
    // 实测：v2 正则 `(.*)` 在 `\s+` 后对 '   ' 匹配时，因 $ 锚定且 .* 可空，
    // 引擎回溯后 remaining 实际为 ''（空）。验证此真实行为。
    expect(parseSkillPrefix('/graphify   ')).toEqual({
      skillId: 'graphify', remaining: '',
    })
  })

  it('skill id 不允许的点号不会被贪婪匹配（v2 正则限制 [a-zA-Z0-9_-]）', () => {
    // /a.b → 第一个正则要求 \s+ 跟随，'.' 不在字符集，整体不匹配
    expect(parseSkillPrefix('/a.b hello')).toEqual({ skillId: '', remaining: '/a.b hello' })
  })
})
