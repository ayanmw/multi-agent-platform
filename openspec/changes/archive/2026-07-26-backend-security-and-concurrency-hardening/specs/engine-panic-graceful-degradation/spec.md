# engine-panic-graceful-degradation Specification

## ADDED Requirements

### Requirement: Engine internal panic must not propagate outside Run
Engine SHALL capture any panic occurring within `Engine.Run` (including LLM client, tool execution, event bus, persistence callbacks), emit a `task_failed` event, persist task status as `failed`, clean up in-memory context snapshot, and return an error. The panic SHALL NOT be re-raised to the caller.

#### Scenario: Tool executor panics during a run
- **WHEN** a registered tool raises a panic inside `ExecuteWithCtx`
- **THEN** `Engine.Run` returns a non-nil error describing the panic, sends a `task_failed` event with reason `"panic"`, updates task persistence status to `"failed"`, and does not propagate the panic to the caller

#### Scenario: Event bus callback panics during event emission
- **WHEN** `EventBus.SendEvent` triggers a panic in a downstream observer
- **THEN** the panic is captured within `Engine.Run`, reported via `task_failed`, and `Engine.Run` returns an error

#### Scenario: Conserved stack trace for diagnostics
- **WHEN** a panic is captured inside `Engine.Run`
- **THEN** the returned error string includes the panic value and a runtime stack trace produced by `debug.Stack()`
