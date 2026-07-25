# approval-delegation-fallback Specification

## Purpose
TBD - created by archiving change agent-config-permissions-and-v2-auto-approval. Update Purpose after archive.
## Requirements
### Requirement: Worker-to-leader approval delegation falls back to user approval
The Engine's `handleApprovalDelegation` method SHALL treat a missing or unresolvable supervisor as a recoverable condition and route the approval request to the same user-approval path used by root agents. The fallback SHALL only occur when the Engine has a non-nil `ApprovalHandler`; otherwise it MAY return an error because there is no channel to resolve the decision.

#### Scenario: Worker without supervisor handler falls back to user
- **WHEN** `handleApprovalDelegation` is called and `e.cfg.SupervisorDecisionHandler == nil`
- **THEN** the method calls `e.handleApprovalRequired` instead of returning "worker 未配置 supervisor，无法委托审批"

#### Scenario: Worker delegation request times out waiting for leader
- **WHEN** `RequestDelegatedApproval` returns `decided=false` due to a timeout
- **THEN** `handleApprovalDelegation` falls back to `handleApprovalRequired` if `e.approvalHandler != nil`

