# v2-touch-target-expansion Specification

## Purpose

Ensure interactive elements in SessionDock and FileTreeNode meet the 44×44 CSS pixel minimum touch target size on mobile and coarse-pointer devices.

## ADDED Requirements

### Requirement: SessionDock action buttons meet 44px touch target

All action buttons in the SessionDock project headers and session rows SHALL have a rendered or extended touch target of at least 44×44px on mobile viewports.

#### Scenario: Tapping project collapse chevron on mobile

- **WHEN** the user taps the project collapse chevron on a mobile viewport
- **THEN** the hit target is at least 44×44px and the tap reliably toggles the project

#### Scenario: Tapping session edit and delete actions on mobile

- **WHEN** the user taps the edit or delete icons next to a session row
- **THEN** each icon has at least a 44×44px hit area and does not require precision tapping

### Requirement: FileTreeNode rows meet 44px minimum height

Every `.tree-row` in the file tree SHALL have a minimum height of 44px and vertically center its content.

#### Scenario: Browsing files on mobile

- **WHEN** the user views the Files tab on a mobile viewport
- **THEN** each file/directory row is at least 44px tall and text is vertically centered

### Requirement: Open-in-new-tab button is reachable on touch devices

The open-in-new-tab button on FileTreeNode rows SHALL be visible and reachable on coarse-pointer devices even though it is hover-only on desktop.

#### Scenario: Touch user opens file in new tab

- **WHEN** the user views the file tree on a touch device
- **THEN** the open-in-new-tab button is visible without hovering and has a 44×44px hit area
