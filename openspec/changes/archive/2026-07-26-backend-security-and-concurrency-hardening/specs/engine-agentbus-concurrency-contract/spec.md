# engine-agentbus-concurrency-contract Specification

## ADDED Requirements

### Requirement: AgentBus listener writes to message history atomically
Engine SHALL ensure that any write to the `messages` slice by the AgentBus listener goroutine is synchronized with writes made by the main ReAct loop. The listener SHALL be guaranteed to complete (or the engine state SHALL be safe to discard) before `Engine.Run` returns.

#### Scenario: Concurrent agent message arrives while loop appends tool result
- **WHEN** the AgentBus listener receives an external agent message at the same time the main loop appends a tool result to `messages`
- **THEN** the resulting `messages` slice has deterministic ordering (no lost writes, no interleaved partial appends) and no data race is reported by `-race`

#### Scenario: AgentBus listener exits before Run returns
- **WHEN** `Engine.Run` completes (success, failure, cancellation, or panic capture)
- **THEN** the AgentBus listener goroutine has exited before `Run` returns control to the caller
