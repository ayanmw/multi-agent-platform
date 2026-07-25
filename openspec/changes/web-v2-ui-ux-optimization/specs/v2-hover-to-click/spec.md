# v2-hover-to-click Specification

## Purpose

Convert hover-only interactive patterns to work reliably on touch devices while preserving hover enhancements for desktop users.

## ADDED Requirements

### Requirement: ThemePalette opens on click or tap

The ThemePalette panel SHALL open on click or tap of the trigger and SHALL remain open until the user clicks outside the panel, selects a theme, or taps an explicit close control.

#### Scenario: Touch user changes theme

- **WHEN** the user taps the theme trigger on a touch device
- **THEN** the theme palette opens and stays open so the user can select a theme

#### Scenario: Desktop hover preview does not prevent click

- **WHEN** a desktop user hovers and then clicks the theme trigger
- **THEN** the palette remains open after mouseleave until an explicit close action

### Requirement: Mobile bottom sheet header stays accessible

The MobileBottomSheet SHALL keep its close control in a fixed or sticky header separate from the scrollable body.

#### Scenario: Short viewport with long content

- **WHEN** a mobile bottom sheet contains content taller than the available viewport
- **THEN** the close button remains visible at the top of the sheet and is not scrolled out of view

### Requirement: Hover-only tool buttons expose visible controls on touch

Any action button that appears only on hover for desktop SHALL be permanently visible on coarse-pointer devices.

#### Scenario: File tree open-in-new-tab on touch

- **WHEN** the user interacts with the file tree on a touch device
- **THEN** the open-in-new-tab button is always visible for each file row
