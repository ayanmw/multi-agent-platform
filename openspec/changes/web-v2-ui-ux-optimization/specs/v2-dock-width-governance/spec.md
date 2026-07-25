# v2-dock-width-governance Specification

## Purpose

Define width constraints and automatic collapse behavior for the desktop right-side dock zone (Files dock and Cron dock) so that the main stage always keeps a usable minimum width.

## ADDED Requirements

### Requirement: Cron dock has explicit width bounds

The Cron dock SHALL render with a fixed preferred width, a minimum width, and a maximum width in desktop and tablet layouts.

#### Scenario: Cron dock opens on desktop

- **WHEN** the user opens the Cron dock
- **THEN** its rendered width is between 240px and 360px inclusive and it does not shrink below 240px when the window is narrow

### Requirement: Right-side dock zone has a combined width cap

The combined width of the Files dock and Cron dock SHALL NOT exceed 45vw or 720px, whichever is smaller, on desktop viewports.

#### Scenario: Narrow desktop at 1024px with both docks open

- **WHEN** the viewport is 1024px wide and both Files and Cron docks are visible
- **THEN** the combined right zone width does not exceed 45% of the viewport and the main stage retains at least 600px of usable width

### Requirement: Narrow desktop auto-collapses side panels to protect main stage

When the main stage width falls below 600px because side panels are open, the system SHALL automatically collapse the Cron dock first, then the Files dock if necessary, and notify the user.

#### Scenario: Resizing window with Files and Cron open

- **WHEN** the user narrows the viewport until main stage width is below 600px
- **THEN** Cron dock auto-collapses, a Toast appears explaining the action, and Files dock remains visible unless the stage is still below 600px

### Requirement: Manual expansion restores previously collapsed docks

The user SHALL be able to reopen a dock that was automatically collapsed, unless doing so would again push the main stage below the minimum width.

#### Scenario: User reopens Cron dock after auto-collapse

- **WHEN** the user clicks the Cron dock toggle after it was auto-collapsed
- **THEN** if enough space exists Cron dock reopens; otherwise a Toast explains why it cannot reopen
