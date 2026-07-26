# engine Specification

## Purpose
定义 ReAct Loop 引擎的边界行为与并发契约，确保引擎在内部异常、并发写入、外部事件到达时仍保持稳定与可观测。

## Requirements

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

### Requirement: AgentBus listener writes to message history atomically
Engine SHALL ensure that any write to the `messages` slice by the AgentBus listener goroutine is synchronized with writes made by the main ReAct loop. The listener SHALL be guaranteed to complete (or the engine state SHALL be safe to discard) before `Engine.Run` returns.

#### Scenario: Concurrent agent message arrives while loop appends tool result
- **WHEN** the AgentBus listener receives an external agent message at the same time the main loop appends a tool result to `messages`
- **THEN** the resulting `messages` slice has deterministic ordering (no lost writes, no interleaved partial appends) and no data race is reported by `-race`

#### Scenario: AgentBus listener exits before Run returns
- **WHEN** `Engine.Run` completes (success, failure, cancellation, or panic capture)
- **THEN** the AgentBus listener goroutine has exited before `Run` returns control to the caller

