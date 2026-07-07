## ADDED Requirements

### Requirement: Provider Context Propagation
The system SHALL support context propagation through the Provider interface to enable cancellation and timeout control for LLM API calls.

#### Scenario: Task cancellation during LLM call
- **WHEN** a running task is cancelled by the user
- **THEN** all in-flight LLM API calls SHALL receive a cancellation signal and terminate gracefully

#### Scenario: Timeout enforcement
- **WHEN** a LLM API call exceeds the configured timeout
- **THEN** the HTTP request SHALL be cancelled via context and return a timeout error

#### Scenario: Fallback context inheritance
- **WHEN** a primary model fails and fallback is attempted
- **THEN** the fallback request SHALL inherit the parent context's cancellation signal

## ADDED Requirements

### Requirement: ChatRequest Context Field
The `ChatRequest` struct SHALL include an optional `Context context.Context` field that providers use for HTTP request cancellation.

#### Scenario: Provider creates request with context
- **WHEN** a provider receives a ChatRequest with Context set
- **THEN** the provider SHALL use `http.NewRequestWithContext` to bind the context to the HTTP request

#### Scenario: Provider handles nil context
- **WHEN** a provider receives a ChatRequest with Context == nil
- **THEN** the provider SHALL fall back to `http.NewRequest` (backward compatible behavior)

## ADDED Requirements

### Requirement: Backward Compatibility
The addition of Context to ChatRequest SHALL be backward compatible — existing code that does not set Context SHALL continue to work without modification.

#### Scenario: Existing Engine code without Context
- **WHEN** the Engine calls ChatStream with a ChatRequest that has Context == nil
- **THEN** the provider SHALL proceed with default timeout behavior
