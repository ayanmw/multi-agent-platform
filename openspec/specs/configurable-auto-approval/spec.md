# configurable-auto-approval Specification

## Purpose
TBD - created by archiving change add-configurable-auto-approval. Update Purpose after archive.
## Requirements
### Requirement: v2 Options flyout exposes configurable auto-approval settings
The v2 front-end SHALL provide an "自动审批" (Auto-approve) section inside the CommandBar Options flyout. This section SHALL list selectable risk tags, provide a "select all / clear all" toggle, and only enable auto-approval when at least one tag is selected.

#### Scenario: Enabling auto-approval for low-risk tags
- **WHEN** the user opens Options, checks the "自动审批" toggle area, and selects at least the `network` tag
- **THEN** the configuration is saved to LocalStorage and subsequent `approval_required` events for `TagPolicyRule` with only `network`/`mcp` tags are automatically approved

#### Scenario: Disabling auto-approval by clearing all tags
- **WHEN** the user unchecks every tag in the auto-approval section
- **THEN** auto-approval is considered OFF and ALL `approval_required` events SHALL show the ApprovalDialog without automatic approve

#### Scenario: One-click select all
- **WHEN** the user clicks the "全选" button in the auto-approval section
- **THEN** all available risk tags are selected and auto-approval becomes active for any matching `TagPolicyRule` request

#### Scenario: One-click clear all
- **WHEN** the user clicks the "清空" button when at least one tag is selected
- **THEN** all tags are unchecked, auto-approval is OFF, and the section visually indicates disabled state

