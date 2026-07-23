## ADDED Requirements

### Requirement: SQLite Schema Initialization
The system SHALL create all required tables on first startup and verify schema integrity on subsequent startups.

#### Scenario: First startup
- **WHEN** the application starts with an empty data directory
- **THEN** the system SHALL create a new SQLite database file and initialize all tables (agents, tasks, steps, tools, conversations, files)

#### Scenario: Subsequent startup with existing database
- **WHEN** the application starts with an existing database
- **THEN** the system SHALL verify the schema version and apply any pending migrations

### Requirement: Task and Step Persistence
The system SHALL persist all Task executions and individual Step details to SQLite in real-time.

#### Scenario: Task creation
- **WHEN** a new Task is initiated
- **THEN** the system SHALL insert a record in the tasks table with initial status "running"

#### Scenario: Step logging
- **WHEN** each Agent step completes (think, tool_call, observation)
- **THEN** the system SHALL insert a record in the steps table with task_id, agent_id, step_index, type, content, and timing

#### Scenario: Task completion
- **WHEN** a Task finishes (success or failure)
- **THEN** the system SHALL update the tasks record with final status, final_result, total_tokens, and completed_at timestamp

### Requirement: Conversation History Persistence
The system SHALL persist the full conversation turn history (user/assistant/tool/system messages) to SQLite.

#### Scenario: Message logging
- **WHEN** each message is sent to or received from the LLM
- **THEN** the system SHALL insert a record in the conversations table with role and content

#### Scenario: Conversation replay
- **WHEN** a user opens a previous Task
- **THEN** the system SHALL load all conversation messages and reconstruct the full context

### Requirement: File Storage
The system SHALL store files written by Agents in the local filesystem under the `storage/` directory, with metadata tracked in SQLite.

#### Scenario: File creation
- **WHEN** the write_file tool creates a file
- **THEN** the system SHALL save the file under storage/{task_id}/ and insert metadata into the files table
