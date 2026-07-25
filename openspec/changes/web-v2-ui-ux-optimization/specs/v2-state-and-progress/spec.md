# v2-state-and-progress Specification

## Purpose

Fix frontend state handling and progress indication so users see accurate, non-misleading UI state instead of placeholder values or direct store mutations.

## ADDED Requirements

### Requirement: Session switch clears task cache through store method

The App.vue component SHALL NOT directly mutate `taskCache.value`. When switching sessions, it SHALL call a store method to clear cache entries for the relevant session.

#### Scenario: User switches session

- **WHEN** the user switches from one session to another
- **THEN** App.vue invokes `taskStore.clearCacheForSession(sessionId)` instead of deleting entries directly

### Requirement: CommandBar progress is accurate or indeterminate

The CommandBar progress indicator SHALL display either the real task progress or an indeterminate animation. It SHALL NOT display a fixed placeholder percentage.

#### Scenario: Task is running with known max steps

- **WHEN** a task is running and the current step index and maximum steps are known
- **THEN** the progress bar shows `currentStepIndex / maxSteps` as a percentage

#### Scenario: Task is running without step progress data

- **WHEN** a task is running but step progress cannot be determined
- **THEN** the progress bar shows an indeterminate animation instead of a fixed value

### Scenario: Pending state shows indeterminate progress

- **WHEN** a task is pending
- **THEN** the progress bar shows an indeterminate animation
