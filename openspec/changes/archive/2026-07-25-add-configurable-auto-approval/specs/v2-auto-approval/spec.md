## MODIFIED Requirements

### Requirement: v2 front-end supports automatic approval for low-risk policy requests
The v2 front-end SHALL automatically approve `approval_required` system_info events when the triggering rule is `TagPolicyRule` and all tool tags involved are in the user-configured auto-approval set. Any tag outside the configured set SHALL force manual confirmation. The default configured set SHALL be `{network, mcp}`.

#### Scenario: web_search network approval is auto-approved
- **WHEN** the front-end receives `system_info` with `type=approval_required`, `rule=TagPolicyRule`, `tool=core/web_search`, configured auto-approval tags include `network`, and `tags=["network","websearch"]`
- **THEN** the front-end immediately sends a control message `action: approve` with the corresponding `approval_id` and does not render the ApprovalDialog

#### Scenario: shell command approval stays manual
- **WHEN** the front-end receives `system_info` with `type=approval_required`, `rule=TagPolicyRule`, and `tags=["exec"]` while `exec` is NOT in the configured auto-approval set
- **THEN** the ApprovalDialog is rendered and remains open until the user clicks Approve or Deny

### Requirement: Auto-approval does not bypass unknown or mixed-risk requests
If an approval request includes tags that are not recognized as low-risk, or if the triggering rule is not `TagPolicyRule`, the front-end SHALL treat it as requiring manual approval. A request matches only when every tag is in the configured auto-approval set.

#### Scenario: ApprovalRule request is always manual
- **WHEN** the front-end receives `system_info` with `type=approval_required` and `rule=ApprovalRule`
- **THEN** the ApprovalDialog is shown and no automatic approval occurs

#### Scenario: Mixed-risk tags require manual confirmation even if some are allowed
- **WHEN** the front-end receives `system_info` with `rule=TagPolicyRule` and `tags=["network","exec"]` while only `network` is in the configured auto-approval set
- **THEN** the ApprovalDialog is shown because not all tags are allowed
