## ADDED Requirements

### Requirement: Backend approval handler supports an auto-approval policy
The WebSocket approval handler SHALL accept an optional `AutoApprovalPolicy` that decides whether an `approval_required` request can be automatically approved without a front-end decision.

#### Scenario: Request matches auto-approval policy after 5s
- **WHEN** the handler is waiting for a front-end decision on a `TagPolicyRule` request whose tags are all contained in the configured auto-approval set
- **THEN** after 5 seconds without a decision the handler SHALL automatically approve the request, clean up pending state, and return `approved=true`

#### Scenario: Request does not match auto-approval policy
- **WHEN** the handler is waiting for a front-end decision on a request that is not `TagPolicyRule` or includes tags outside the configured set
- **THEN** the handler SHALL continue waiting for the full remaining timeout (25 seconds by default) and return `approved=false` on timeout

### Requirement: Backend and front-end auto-approval rules are consistent
The same decision function SHALL be used by both front-end and back-end: auto-approve only when `rule === 'TagPolicyRule'`, the request has at least one tag, and every tag is present in the configured auto-approval set.

#### Scenario: Empty auto-approval set disables backend auto-approval
- **WHEN** the configured auto-approval tag set is empty
- **THEN** the backend SHALL NOT automatically approve any request during the 5-second grace window
