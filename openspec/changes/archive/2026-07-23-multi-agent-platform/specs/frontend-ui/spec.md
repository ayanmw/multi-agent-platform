## ADDED Requirements

### Requirement: WebSocket Client Connection
The frontend SHALL establish and maintain a WebSocket connection to the Go backend for real-time event streaming.

#### Scenario: Connection on page load
- **WHEN** the user navigates to the application
- **THEN** the system SHALL establish a WebSocket connection to `/ws`, receive a session_id, and display the connection status

#### Scenario: Auto-reconnect on disconnect
- **WHEN** the WebSocket connection is lost
- **THEN** the system SHALL attempt to reconnect with exponential backoff (1s, 2s, 4s, max 30s) and restore the session state upon reconnection

### Requirement: Case Cards Panel
The frontend SHALL display a grid of preset Task Cases that users can click to initiate Agent Tasks.

#### Scenario: Display available cases
- **WHEN** the page loads
- **THEN** the system SHALL fetch and display all available preset cases as cards with title, description, and a "Run" button

#### Scenario: Initiate task from Case Card
- **WHEN** a user clicks "Run" on a Case Card
- **THEN** the system SHALL send a WebSocket message to start the Task with the Case's configuration, and switch the view to the Task's real-time visualization

### Requirement: Agent Tree Visualization
The frontend SHALL render a hierarchical tree view showing all Agents and their execution Steps in real-time.

#### Scenario: Real-time step rendering
- **WHEN** a StepStarted event is received
- **THEN** the system SHALL add a new step node to the Agent's tree, initially in "running" state

#### Scenario: Streaming text in think step
- **WHEN** LLMDelta events arrive during a think step
- **THEN** the system SHALL append the delta to the step's displayed text in real-time with a typewriter effect

#### Scenario: Tool call display
- **WHEN** a ToolCallComplete event is received
- **THEN** the system SHALL render the tool call node with input parameters (expandable) and output result (expandable), with execution duration displayed

#### Scenario: Step collapse/expand
- **WHEN** a user clicks a step node
- **THEN** the system SHALL toggle the visibility of the step's full content without collapsing other steps

### Requirement: Configuration Pages
The frontend SHALL provide pages for managing Agent configurations and Tool registrations.

#### Scenario: Agent configuration CRUD
- **WHEN** a user navigates to the Agents settings page
- **THEN** the system SHALL display a list of all Agents with edit/delete actions, and a form to create new Agents
