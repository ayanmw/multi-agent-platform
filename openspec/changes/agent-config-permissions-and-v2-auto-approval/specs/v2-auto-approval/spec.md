## ADDED Requirements

### Requirement: v2 front-end supports automatic approval for low-risk policy requests
The v2 front-end SHALL automatically approve `approval_required` system_info events when the triggering rule is `TagPolicyRule` and all tool tags involved are in the auto-allow list (`network`, `mcp`). Any tag outside this list SHALL force manual confirmation.

#### Scenario: web_search network approval is auto-approved
- **WHEN** the front-end receives `system_info` with `type=approval_required`, `rule=TagPolicyRule`, `tool=core/web_search`, and `tags=["network","websearch"]`
- **THEN** the front-end immediately sends a control message `action: approve` with the corresponding `approval_id` and does not render the ApprovalDialog

#### Scenario: shell command approval stays manual
- **WHEN** the front-end receives `system_info` with `type=approval_required`, `rule=TagPolicyRule`, and `tags=["exec"]`
- **THEN** the ApprovalDialog is rendered and remains open until the user clicks Approve or Deny

### Requirement: Auto-approval does not bypass unknown or mixed-risk requests
If an approval request includes tags that are not recognized as low-risk, or if the triggering rule is not `TagPolicyRule`, the front-end SHALL treat it as requiring manual approval.

#### Scenario: ApprovalRule request is always manual
- **WHEN** the front-end receives `system_info` with `type=approval_required` and `rule=ApprovalRule`
- **THEN** the ApprovalDialog is shown and no automatic approval occurs
