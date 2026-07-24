## ADDED Requirements

### Requirement: Mobile top bar does not overflow
The mobile top bar SHALL fit within 375px–768px viewports and SHALL expose only primary actions in the header.

#### Scenario: Viewport width is 375px
- **WHEN** the viewport width is 375px
- **THEN** no TopBar content overflows or causes horizontal scroll

#### Scenario: Non-critical top bar actions are reachable
- **WHEN** the user taps the "More" button on the mobile TopBar
- **THEN** a bottom sheet opens containing Theme, MCP, Recent Mods, Model Prices, Keyboard Tips and Version switcher

### Requirement: Mobile bottom navigation reaches management panels
The mobile bottom navigation SHALL provide direct tabs for Stage, Sessions, Files, Manage and Cron.

#### Scenario: Tapping Manage tab
- **WHEN** the user taps the "Manage" tab in mobile bottom navigation
- **THEN** the Manage tab view opens full screen or as a bottom sheet and the CommandBar is hidden

#### Scenario: Tapping Cron tab
- **WHEN** the user taps the "Cron" tab in mobile bottom navigation
- **THEN** the Cron tab view opens and the CommandBar is hidden

### Requirement: Floating panels become bottom sheets on mobile
The Manage flyout and Context flyout SHALL render as bottom sheets or full-screen drawers on mobile viewports instead of anchored dropdowns.

#### Scenario: Opening Manage on mobile
- **WHEN** the user opens Manage from either the TopBar "More" section or the mobile "Manage" tab
- **THEN** the panel slides up from the bottom or opens full screen, is dismissible, and stays within the viewport

#### Scenario: Opening Context on mobile
- **WHEN** the user taps the Context button in the CommandBar on mobile
- **THEN** the Context panel opens as a bottom sheet or full-screen drawer instead of a left/right anchored flyout

### Requirement: Mobile command input follows layout flow
The CommandBar on mobile SHALL not use `position: fixed`; it SHALL be part of the root flex layout, and the main stage SHALL reserve enough bottom padding.

#### Scenario: Mobile keyboard opens
- **WHEN** the on-screen keyboard opens while the user is focused on the command input
- **THEN** the latest main-stage content remains reachable by scrolling

#### Scenario: Non-stage tab hides command bar
- **WHEN** the active mobile tab is Sessions, Files, Manage or Cron
- **THEN** the CommandBar is not rendered and the tab content uses the full available height

### Requirement: Inspector dialog is mobile-friendly
The Inspector dialog opened from mobile Manage SHALL cover the full viewport with a clearly reachable close control.

#### Scenario: Closing inspector dialog on mobile
- **WHEN** the Inspector dialog is open on a mobile viewport
- **THEN** the close button is at least 44×44px, tapping the overlay closes it, and the keyboard ESC shortcut also closes it
