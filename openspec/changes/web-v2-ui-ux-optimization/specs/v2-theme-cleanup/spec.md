# v2-theme-cleanup Specification

## Purpose

Remove hard-coded color values and emoji-only iconography from web/v2 components to align with the design-token theme system and improve consistency across all themes.

## ADDED Requirements

### Requirement: KeyboardTips uses theme tokens

The KeyboardTips component SHALL use CSS custom properties from the theme system instead of hard-coded hex values.

#### Scenario: Switching themes while keyboard tips are open

- **WHEN** the user switches between dark and light themes with the keyboard tips visible
- **THEN** the keyboard tips background, border, and text colors adapt to the active theme without hard-coded dark values

### Requirement: ContextWindowPanel translucent surfaces use theme tokens

All translucent backgrounds, overlays, and gradients in ContextWindowPanel SHALL reference theme tokens that derive from the current surface color.

#### Scenario: Viewing ContextWindowPanel in solar light theme

- **WHEN** the user opens the ContextWindowPanel while the solar light theme is active
- **THEN** translucent panels do not appear gray or dirty and remain readable

### Requirement: Emoji-only action buttons are replaced with SVG icons

Action buttons that currently use only emoji symbols SHALL be replaced with SVG icons from the chosen icon library while preserving the existing `aria-label`.

#### Scenario: TopBar action buttons with icons

- **WHEN** the user views the TopBar on any platform
- **THEN** action buttons render as consistent SVG icons instead of system emoji

#### Scenario: CommandBar action buttons with icons

- **WHEN** the user views the CommandBar on any platform
- **THEN** the Options and Context buttons render as consistent SVG icons

### Requirement: ContextFlyout resize cursor matches drag direction

The ContextFlyout SHALL use `ew-resize` for horizontal resize handles and `ns-resize` for vertical resize handles, and SHALL NOT apply a single global cursor override to all elements during resize.

#### Scenario: Resizing ContextFlyout width

- **WHEN** the user drags the ContextFlyout width handle
- **THEN** the cursor shows `ew-resize` and the rest of the page does not show `ns-resize`

### Requirement: Spurious formatting issues are resolved

Obvious formatting drift such as missing spaces around CSS values SHALL be corrected.

#### Scenario: Format check passes

- **WHEN** the project runs its formatter/linter
- **THEN** no formatting-only errors remain in the touched components
