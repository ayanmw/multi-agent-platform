# v2-mobile-a11y-touch Specification

## Purpose

Define touch target and accessible-name requirements for mobile and accessibility-critical controls in web/v2.

## MODIFIED Requirements

### Requirement: Touch targets meet minimum size on mobile

All interactive controls on mobile viewports SHALL have a touch target of at least 44×44 CSS pixels.

#### Scenario: TopBar icon buttons on mobile

- **WHEN** the user views the app on a mobile viewport
- **THEN** every icon button in the TopBar has a rendered size or extended hit area of at least 44×44px

#### Scenario: Mobile bottom tab buttons

- **WHEN** the user views the app on a mobile viewport
- **THEN** each bottom-tab button has a hit target of at least 44×44px

#### Scenario: Inspector dialog close button

- **WHEN** the Inspector dialog is open on mobile
- **THEN** the close button is at least 44×44px

#### Scenario: SessionDock action buttons on mobile

- **WHEN** the user taps a project header action or session row action in SessionDock on mobile
- **THEN** each action button has a hit target of at least 44×44px

#### Scenario: FileTreeNode row on mobile

- **WHEN** the user views the file tree on mobile
- **THEN** every `.tree-row` is at least 44px tall and the open-in-new-tab button has a 44×44px hit area

### Requirement: Icon-only controls expose accessible names

All buttons that contain only icons or emoji SHALL expose an accessible name via `aria-label` or visually hidden text.

#### Scenario: TopBar emoji icon buttons

- **WHEN** a screen reader focuses a TopBar icon button
- **THEN** the button announces its purpose ("MCP Server", "Recent Mods", "Model Prices", etc.)

#### Scenario: CommandBar emoji buttons

- **WHEN** a screen reader focuses the Options or Context button in the CommandBar
- **THEN** the button announces "Options" or "Open Context Window" respectively

### Requirement: Mobile text remains readable

On mobile viewports, body text SHALL not be smaller than 14px and code/pre text SHALL wrap instead of causing horizontal overflow.

#### Scenario: Step card content on mobile

- **WHEN** a tool call step renders a code block on a 375px viewport
- **THEN** the code text wraps (`pre-wrap`) and does not create horizontal scroll

#### Scenario: Bottom tab labels

- **WHEN** the user views the mobile bottom navigation
- **THEN** tab labels are at least 12px tall

### Requirement: Respect reduced-motion preference

All non-essential animations SHALL respect `prefers-reduced-motion: reduce`.

#### Scenario: Reduced motion enabled

- **WHEN** the user has reduced motion enabled
- **THEN** the agent status pulse animation and flyout/bottom-sheet transitions are disabled or shortened to near-instant

## ADDED Requirements

### Requirement: Status indicators do not rely solely on color or emoji

Status chips, role badges, and agent state indicators SHALL provide either visible text labels or screen-reader-only text conveying the state, in addition to color and emoji/icon.

#### Scenario: Running status chip

- **WHEN** a screen reader user focuses a status chip showing a green dot and running emoji
- **THEN** the accessible name announces "running" or includes a visually hidden "running" label

#### Scenario: Color-blind user identifies success vs failure

- **WHEN** a user cannot distinguish the success/failure colors
- **THEN** distinct icons and text labels still communicate success or failure

### Requirement: Toast live region is atomic

The Toast container SHALL use `aria-atomic="true"` so multiple rapid toasts are announced as discrete messages.

#### Scenario: Multiple toasts in quick succession

- **WHEN** several toasts appear within a short time
- **THEN** each toast is announced separately rather than concatenated into one string

### Requirement: Dock rail and file tree are keyboard operable

The dock rail toggle and file tree rows SHALL be reachable via keyboard and activatable with Enter or Space.

#### Scenario: Keyboard user toggles dock

- **WHEN** a keyboard user tabs to the dock rail toggle and presses Enter
- **THEN** the dock opens or closes

#### Scenario: Keyboard user expands a directory

- **WHEN** a keyboard user focuses a directory row in the file tree and presses Enter or Space
- **THEN** the directory expands or collapses
