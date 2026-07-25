# v2-dialog-focus-trap Specification

## Purpose

Define focus management and ARIA behavior for modal dialogs and drawer overlays so that keyboard and screen-reader users can operate them safely.

## ADDED Requirements

### Requirement: Dialogs capture focus and return it on close

When a modal dialog or overlay opens, focus SHALL move to the first focusable element or the dialog title, and on close focus SHALL return to the element that opened it.

#### Scenario: Opening inspector dialog

- **WHEN** the user opens the Inspector dialog
- **THEN** focus moves inside the dialog and the Escape key closes it

#### Scenario: Closing inspector dialog

- **WHEN** the user closes the Inspector dialog
- **THEN** focus returns to the button or control that triggered the dialog

### Requirement: Focus remains inside the dialog while it is open

Pressing Tab while a modal dialog is open SHALL cycle focus among focusable elements inside the dialog and SHALL NOT move focus to background content.

#### Scenario: Tabbing inside approval dialog

- **WHEN** the Approval dialog is open and the user presses Tab repeatedly
- **THEN** focus loops within the dialog controls and never reaches the TopBar or stage

### Requirement: Dialogs expose correct ARIA roles

All modal dialogs and full-screen drawers SHALL have `role="dialog"`, `aria-modal="true"`, and an accessible label via `aria-labelledby` or `aria-label`.

#### Scenario: Screen reader announces dialog

- **WHEN** a screen reader user opens a dialog
- **THEN** the dialog is announced as a modal and its title is read aloud
