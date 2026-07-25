# agent-config-permissions Specification

## Purpose
TBD - created by archiving change agent-config-permissions-and-v2-auto-approval. Update Purpose after archive.
## Requirements
### Requirement: Agent config can declare default task permissions
The system SHALL allow each Agent record to store a `config.permissions` object that mirrors `TaskPermissions` fields. These permissions SHALL be persisted in the existing `agents.config` JSON column without requiring a schema migration.

#### Scenario: Creating an agent with default permissions
- **WHEN** a client POSTs `/api/agents` with `config.permissions.allow_network=true`
- **THEN** the created Agent record stores that permission in `config` and returns it in the response

#### Scenario: Updating an agent's default permissions
- **WHEN** a client PUTs `/api/agents/{id}` with `config.permissions.allow_shell=true`
- **THEN** the existing Agent's `config` is updated and subsequent tasks started with this agent inherit `AllowShell=true`

### Requirement: Agent default permissions merge into TaskContract at run time
When starting a task via `/api/tasks` (chat) or `/api/run-case`, the system SHALL merge the selected Agent's `config.permissions` into the resolved `TaskContract.Permissions` using OR semantics. The case-level or request-level permission SHALL NOT be overridden to `false` by an agent default.

#### Scenario: Agent grants network permission for a case that defaults to false
- **WHEN** the `web-research` case sets `AllowNetwork=false` and the selected Agent's `config.permissions.allow_network=true`
- **THEN** the effective contract has `AllowNetwork=true` and `core/web_search` is allowed without approval

#### Scenario: Agent with empty permissions does not change case permissions
- **WHEN** the selected Agent has no `config.permissions` field
- **THEN** the effective contract uses only case/request-level permissions

### Requirement: Worker approval delegation falls back to user approval
The system SHALL NOT terminate a worker task with "worker 未配置 supervisor" when leader delegation is unavailable. Instead, the Engine SHALL fall back to the user approval path (`handleApprovalRequired`) so that a connected front-end can prompt the user.

#### Scenario: Worker triggers network approval without supervisor wiring
- **WHEN** a worker Engine hits `ErrApprovalRequired` and either `SupervisorDecisionHandler` is nil or `SupervisorSubTaskID` is empty
- **THEN** the Engine emits `approval_required` and waits for a user decision via the configured `ApprovalHandler`

#### Scenario: Worker delegation times out
- **WHEN** a worker Engine requests leader approval and the leader does not respond within the configured timeout
- **THEN** the Engine falls back to user approval instead of returning the timeout as a fatal error

### Requirement: v2 front-end can auto-approve low-risk policy requests
The v2 front-end SHALL support automatic approval of `approval_required` events that originate from `TagPolicyRule` and only involve tags in the user-configured auto-approval set. Destructive tags (`exec`, `exec:dangerous`, `filesystem:destructive`, `filesystem:delete`, `filesystem:write`, `shell`) SHALL require explicit user action before they can be added to the auto-approval set.

#### Scenario: Network tool request is auto-approved in v2
- **WHEN** the front-end receives `system_info` with `type=approval_required`, `rule=TagPolicyRule`, configured auto-approval tags include `network`, and the tag list contains only `network`
- **THEN** the front-end automatically sends a `approve` control message for that `approval_id` without opening the dialog

#### Scenario: Dangerous command request still requires manual confirmation
- **WHEN** the front-end receives `system_info` with `type=approval_required`, `rule=TagPolicyRule`, and tag `exec:dangerous` while `exec:dangerous` is NOT in the configured auto-approval set
- **THEN** the ApprovalDialog is shown and the user must manually approve or deny

### Requirement: v2 AgentConfig UI exposes default permission toggles
The v2 Agent configuration panel SHALL display toggle controls for the five permission bits: `allow_network`, `allow_file_write`, `allow_file_delete`, `allow_shell`, and `allow_shell_dangerous`. Changes SHALL be persisted as part of the Agent's `config` object.

#### Scenario: Enabling network permission for an agent in v2
- **WHEN** a user opens the Agent editor, checks "Allow Network", and saves
- **THEN** the PUT `/api/agents/{id}` request includes `config.permissions.allow_network=true`

