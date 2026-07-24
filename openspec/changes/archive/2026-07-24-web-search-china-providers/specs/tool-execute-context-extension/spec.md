## ADDED Requirements

### Requirement: ExecuteContext carries runtime identifiers
The system SHALL extend `tool.ExecuteContext` to include `TaskID`, `AgentID`, `StepIdx`, and `SessionID`.

#### Scenario: Engine constructs ExecuteContext for tool calls
- **WHEN** the engine invokes a tool via `ExecuteWithCtx`
- **THEN** it SHALL populate `TaskID`, `AgentID`, `StepIdx`, and `SessionID` from the current run context.

---

### Requirement: ExecuteContext carries EventBus
The system SHALL extend `tool.ExecuteContext` to include an `event.Bus` instance.

#### Scenario: Tool emits events during execution
- **WHEN** a tool implementation needs to send observable events
- **THEN** it SHALL use `ctx.EventBus.SendEvent(...)` with the identifiers from `ExecuteContext`.

---

### Requirement: ExecuteContext carries LLM provider
The system SHALL extend `tool.ExecuteContext` to include an `llm.Provider` instance.

#### Scenario: Tool needs internal LLM summarization
- **WHEN** a tool implementation requires an LLM call
- **THEN** it SHALL use `ctx.LLMProvider.Chat(...)` and report resulting usage.

---

### Requirement: Engine-level tool result usage aggregation
The system SHALL recognize a `_llm_usage` field in tool results and add its token counts to the current task's total usage.

#### Scenario: Tool returns _llm_usage
- **WHEN** a tool result includes `_llm_usage` with numeric token fields
- **THEN** the engine SHALL add those values to `e.tokenUsage.PromptTokens`, `CompletionTokens`, and `TotalTokens`.

#### Scenario: Tool result lacks _llm_usage
- **WHEN** a tool result does not include `_llm_usage`
- **THEN** the engine SHALL treat it as a regular tool call and not modify usage.
