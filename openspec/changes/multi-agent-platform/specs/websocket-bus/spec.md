## ADDED Requirements

### Requirement: WebSocket Connection Lifecycle
The system SHALL accept WebSocket connections at `/ws` and handle the full lifecycle including connection, message exchange, and disconnection.

#### Scenario: Client connects and receives confirmation
- **WHEN** a client opens a WebSocket connection to `/ws`
- **THEN** the system SHALL accept the connection and send a `connected` event with a unique session_id

#### Scenario: Client disconnects
- **WHEN** a WebSocket client closes the connection or network drops
- **THEN** the system SHALL clean up resources, cancel any running Tasks associated with that session, and log the disconnection

### Requirement: AgentEvent Broadcasting
The system SHALL broadcast structured AgentEvent messages over the WebSocket to all subscribed clients in real-time.

#### Scenario: Token streaming during LLM generation
- **WHEN** the LLM generates a token during Agent execution
- **THEN** the system SHALL emit an `llm_delta` event containing the accumulated text delta, associating it with the correct task_id and agent_id

#### Scenario: Tool call result broadcast
- **WHEN** a Tool execution completes
- **THEN** the system SHALL emit a `tool_call_complete` event with the tool name, input parameters, output result, and duration_ms, all associated with the relevant task_id and agent_id

### Requirement: Client Control Commands
The system SHALL accept control commands from the client over the WebSocket.

#### Scenario: Pause command
- **WHEN** a client sends `{"type": "pause", "task_id": "xxx"}`
- **THEN** the system SHALL pause the specified Task, emit a `paused` event, and halt further LLM calls until resumed or cancelled

#### Scenario: Cancel command
- **WHEN** a client sends `{"type": "cancel", "task_id": "xxx"}`
- **THEN** the system SHALL cancel the Task, emit a `cancelled` event, and clean up all associated resources
