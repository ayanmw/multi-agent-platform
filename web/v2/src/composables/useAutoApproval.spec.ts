import { describe, it, expect, vi } from 'vitest'
import { shouldAutoApprove, LOW_RISK_AUTO_APPROVAL_TAGS, persistTags, AUTO_APPROVAL_TAG_OPTIONS } from './useAutoApproval'

const storage: Record<string, string> = {}
const mockStorage = {
  getItem: (key: string) => storage[key] ?? null,
  setItem: (key: string, value: string) => { storage[key] = value },
  removeItem: (key: string) => { delete storage[key] },
}
Object.defineProperty(globalThis, 'localStorage', { value: mockStorage })

describe('shouldAutoApprove', () => {
  it('approves TagPolicyRule with only network tag', () => {
    expect(shouldAutoApprove('TagPolicyRule', ['network'], LOW_RISK_AUTO_APPROVAL_TAGS)).toBe(true)
  })

  it('approves TagPolicyRule with network and mcp tags', () => {
    expect(shouldAutoApprove('TagPolicyRule', ['network', 'mcp'], LOW_RISK_AUTO_APPROVAL_TAGS)).toBe(true)
  })

  it('rejects ApprovalRule regardless of tags', () => {
    expect(shouldAutoApprove('ApprovalRule', ['network'], LOW_RISK_AUTO_APPROVAL_TAGS)).toBe(false)
  })

  it('rejects shell/exec tags when not allowed', () => {
    expect(shouldAutoApprove('TagPolicyRule', ['exec'], LOW_RISK_AUTO_APPROVAL_TAGS)).toBe(false)
    expect(shouldAutoApprove('TagPolicyRule', ['shell'], LOW_RISK_AUTO_APPROVAL_TAGS)).toBe(false)
  })

  it('rejects mixed-risk tags when only some are allowed', () => {
    expect(shouldAutoApprove('TagPolicyRule', ['network', 'exec'], LOW_RISK_AUTO_APPROVAL_TAGS)).toBe(false)
  })

  it('rejects empty tags', () => {
    expect(shouldAutoApprove('TagPolicyRule', [], LOW_RISK_AUTO_APPROVAL_TAGS)).toBe(false)
  })

  it('approves high-risk tag only when user explicitly allows it', () => {
    const configured = new Set(['network', 'exec'])
    expect(shouldAutoApprove('TagPolicyRule', ['exec'], configured)).toBe(true)
    expect(shouldAutoApprove('TagPolicyRule', ['exec:dangerous'], configured)).toBe(false)
  })
})

describe('persistTags', () => {
  it('serializes tag list to localStorage', () => {
    persistTags(['network', 'mcp'])
    expect(localStorage.getItem('map_v2_auto_approval_tags')).toBe(JSON.stringify(['network', 'mcp']))
  })
})

describe('AUTO_APPROVAL_TAG_OPTIONS', () => {
  it('contains network and mcp as low risk', () => {
    const low = AUTO_APPROVAL_TAG_OPTIONS.filter(o => o.risk === 'low').map(o => o.tag)
    expect(low).toContain('network')
    expect(low).toContain('mcp')
  })

  it('contains destructive tags as high risk', () => {
    const tags = AUTO_APPROVAL_TAG_OPTIONS.map(o => o.tag)
    expect(tags).toContain('filesystem:destructive')
    expect(tags).toContain('exec:dangerous')
  })
})
