# v2-mobile-a11y-touch Specification

## Purpose
TBD - created by archiving change v2-mobile-usability-fix. Update Purpose after archive.
## Requirements
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

