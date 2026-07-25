# v2-tablet-layout Specification

## Purpose

Define how the web/v2 control room lays out the command input area and main stage on tablet viewports (768–1023px) to prevent overlapping, double-rendering, or content being hidden by fixed-position bars.

## ADDED Requirements

### Requirement: Tablet layout uses flex flow for the command area

On tablet viewports the CommandBar SHALL remain inside the document flow of the center column and SHALL NOT use `position: fixed`.

#### Scenario: Tablet viewport at 900px

- **WHEN** the viewport width is between 768px and 1023px
- **THEN** the CommandBar is the last flex child of the center column, the center column height is `calc(100dvh - var(--topbar-h))`, and no duplicate command area space is reserved below it

### Requirement: Mobile layout keeps fixed command area with stage padding

On mobile viewports the CommandBar MAY use `position: fixed; bottom: 0` as today, and the main stage SHALL reserve bottom padding equal to the combined height of CommandBar, MobileNav, and safe-area inset.

#### Scenario: Mobile viewport at 375px

- **WHEN** the viewport width is less than 768px and the stage tab is active
- **THEN** the user can scroll the stage to the end and the final timeline item is fully visible above the bottom bars

### Requirement: Tablet center column does not reserve fixed command height twice

When the command area is rendered inside the flex flow, the parent layout SHALL NOT also reserve `var(--cmd-h)` via margin or padding for a fixed bar that is not fixed.

#### Scenario: Switching from desktop to tablet

- **WHEN** the user resizes the browser from 1100px to 900px
- **THEN** the command area stays attached to the bottom of the center column and the bottom of the main stage aligns flush with the command area
