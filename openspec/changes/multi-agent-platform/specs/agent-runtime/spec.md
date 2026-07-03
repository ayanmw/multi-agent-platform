## ADDED Requirements

### Requirement: Agent ReAct Loop Execution
The system SHALL execute an iterative ReAct (Reason + Act) loop for each Agent until the LLM returns a final answer or the maximum step count is reached.

#### Scenario: Simple single-step response
- **WHEN** a user sends a message and the Agent requires no tool calls
- **THEN** the system SHALL send the message to the LLM, stream the response back to the client, and mark the task as completed

#### Scenario: Multi-step tool call loop
- **WHEN** the LLM returns a function_call in its response
- **THEN** the system SHALL execute the tool, append the result to the conversation history, and loop back to the LLM for the next step

#### Scenario: Maximum step limit reached
- **WHEN** the Agent has executed N consecutive steps without reaching a final answer (N configurable, default 10)
- **THEN** the system SHALL stop the loop, send a TaskFailed event with reason "max_steps_exceeded", and mark the task as failed

### Requirement: Agent State Tracking
The system SHALL track and broadcast the Agent's internal state at each step of the loop.

#### Scenario: Step state transitions
- **WHEN** an Agent begins a new loop iteration
- **THEN** the system SHALL emit a StepStarted event with step_index, then emit LLMThinking, LLMDelta/ToolCall*, and final observation events as the step progresses

#### Scenario: Parallel Agent state isolation
- **WHEN** multiple Agents run concurrently within the same Task
- **THEN** each Agent's state SHALL be tracked independently under its own agent_id, with step_index relative to that Agent

### Requirement: Agent Configuration Binding
Each Agent SHALL be bound to a specific LLM configuration (endpoint, API key, model, temperature) at runtime.

#### Scenario: Different API endpoints for different Agents
- **WHEN** two Agents in the same Task use different LLM configurations
- **THEN** each Agent SHALL independently call its own configured LLM endpoint without interference

#### Scenario: Agent configuration loaded from persistence
- **WHEN** a Task is started with a named Agent configuration
- **THEN** the system SHALL load the Agent's configuration from the database and apply it before the first LLM call
