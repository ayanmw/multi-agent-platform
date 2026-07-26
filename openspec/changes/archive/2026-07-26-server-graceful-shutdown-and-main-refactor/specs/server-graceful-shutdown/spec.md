## ADDED Requirements

### Requirement: Server process shuts down gracefully on termination signals
The system SHALL exit the server process within a bounded time after receiving `SIGINT` or `SIGTERM`.

#### Scenario: Ctrl+C triggers graceful shutdown
- **WHEN** the server process is running and a `SIGINT` signal is sent
- **THEN** the server stops accepting new HTTP connections, closes the WebSocket hub, stops the cron scheduler, stops the memory heartbeat, and closes the MCP manager within 5 seconds

#### Scenario: SIGTERM triggers graceful shutdown
- **WHEN** the server process is running and a `SIGTERM` signal is sent
- **THEN** the server performs the same graceful shutdown sequence as `SIGINT`

### Requirement: Shutdown closer registry is testable
The system SHALL provide a shutdown manager that accepts closer functions and invokes them in registration order with a configurable total timeout.

#### Scenario: Multiple closers execute in order
- **WHEN** three closers are registered with the shutdown manager and `Shutdown` is called
- **THEN** closer 1, closer 2, and closer 3 are called exactly once in that order

#### Scenario: Shutdown honors total timeout
- **WHEN** a closer blocks longer than the configured total timeout
- **THEN** the shutdown manager logs a warning and returns without waiting indefinitely

### Requirement: Heartbeat can be stopped and awaited
The system SHALL provide a `Heartbeat.Stop()` method that cancels the background loop and waits for it to fully terminate.

#### Scenario: Stop waits for Beat to finish
- **WHEN** `Heartbeat.Stop()` is called while a beat cycle is in progress
- **THEN** the current beat cycle completes or is cancelled, and the background goroutine exits before `Stop()` returns

#### Scenario: No beat runs after stop
- **WHEN** `Heartbeat.Stop()` has returned
- **THEN** no further beat cycles are scheduled or executed
