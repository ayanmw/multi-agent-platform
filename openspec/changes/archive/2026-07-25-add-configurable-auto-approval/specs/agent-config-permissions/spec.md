## MODIFIED Requirements

### Requirement: v2 front-end can auto-approve low-risk policy requests
The v2 front-end SHALL support automatic approval of `approval_required` events that originate from `TagPolicyRule` and only involve tags in the user-configured auto-approval set. Destructive tags (`exec`, `exec:dangerous`, `filesystem:destructive`, `filesystem:delete`, `filesystem:write`, `shell`) SHALL require explicit user action before they can be added to the auto-approval set.

#### Scenario: Network tool request is auto-approved in v2
- **WHEN** the front-end receives `system_info` with `type=approval_required`, `rule=TagPolicyRule`, configured auto-approval tags include `network`, and the tag list contains only `network`
- **THEN** the front-end automatically sends a `approve` control message for that `approval_id` without opening the dialog

#### Scenario: Dangerous command request still requires manual confirmation
- **WHEN** the front-end receives `system_info` with `type=approval_required`, `rule=TagPolicyRule`, and tag `exec:dangerous` while `exec:dangerous` is NOT in the configured auto-approval set
- **THEN** the ApprovalDialog is shown and the user must manually approve or deny
