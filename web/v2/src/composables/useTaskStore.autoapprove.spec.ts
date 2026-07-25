import { describe, it, expect } from 'vitest'
import { ref } from 'vue'

// Mirror the auto-approval logic from useTaskStore for unit testing.
const LOW_RISK_TAGS = new Set(['network', 'mcp'])
const HIGH_RISK_TAGS = new Set([
  'exec', 'exec:dangerous', 'shell', 'shell:dangerous',
  'filesystem:destructive', 'filesystem:delete', 'filesystem:write',
])

interface PendingApproval {
  rule?: string
  tags?: string[]
}

function shouldAutoApprove(approval: PendingApproval): boolean {
  if (approval.rule !== 'TagPolicyRule') return false
  const tags = approval.tags || []
  if (tags.length === 0) return false
  if (tags.some(tag => HIGH_RISK_TAGS.has(tag))) return false
  return tags.every(tag => LOW_RISK_TAGS.has(tag))
}

describe('shouldAutoApprove', () => {
  it('approves TagPolicyRule with only network tag', () => {
    expect(shouldAutoApprove({ rule: 'TagPolicyRule', tags: ['network'] })).toBe(true)
  })

  it('approves TagPolicyRule with network and mcp tags', () => {
    expect(shouldAutoApprove({ rule: 'TagPolicyRule', tags: ['network', 'mcp'] })).toBe(true)
  })

  it('rejects ApprovalRule regardless of tags', () => {
    expect(shouldAutoApprove({ rule: 'ApprovalRule', tags: ['network'] })).toBe(false)
  })

  it('rejects shell/exec tags', () => {
    expect(shouldAutoApprove({ rule: 'TagPolicyRule', tags: ['exec'] })).toBe(false)
    expect(shouldAutoApprove({ rule: 'TagPolicyRule', tags: ['shell'] })).toBe(false)
  })

  it('rejects mixed-risk tags', () => {
    expect(shouldAutoApprove({ rule: 'TagPolicyRule', tags: ['network', 'exec'] })).toBe(false)
  })

  it('rejects empty tags', () => {
    expect(shouldAutoApprove({ rule: 'TagPolicyRule', tags: [] })).toBe(false)
  })
})
