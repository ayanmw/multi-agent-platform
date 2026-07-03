## ADDED Requirements

### Requirement: Tool Registration
The system SHALL allow Tools to be registered both programmatically (at startup) and at runtime (via API/LLM self-description).

#### Scenario: Programmatic tool registration at startup
- **WHEN** the server starts up
- **THEN** all built-in tools SHALL be registered into the ToolRegistry with their name, description, JSON Schema parameters, and handler function

#### Scenario: Runtime tool registration via API
- **WHEN** a client sends a registration request with tool name, description, schema, and handler reference
- **THEN** the system SHALL validate the schema, persist the tool to SQLite, and make it available for Agent use immediately

#### Scenario: LLM self-describes a new tool
- **WHEN** an Agent's LLM response indicates it needs a new tool
- **THEN** the system SHALL parse the tool definition, register it, and use it in subsequent steps

### Requirement: Tool Execution
The system SHALL execute registered Tools with input validation and return structured results.

#### Scenario: Successful tool execution
- **WHEN** a Tool is invoked with valid parameters matching its JSON Schema
- **THEN** the system SHALL execute the tool handler, capture the output, and return it as a structured result

#### Scenario: Tool execution with invalid parameters
- **WHEN** a Tool is invoked with parameters that fail JSON Schema validation
- **THEN** the system SHALL NOT execute the handler, return a validation error, and emit a tool_call_complete event with error details

#### Scenario: Tool execution failure
- **WHEN** a Tool handler returns an error
- **THEN** the system SHALL capture the error message, emit a tool_call_complete event with the error, and pass the error back to the LLM for recovery

### Requirement: Built-in Tools
The system SHALL provide the following built-in Tools:

#### Scenario: run_shell tool
- **WHEN** an Agent uses the `run_shell` tool with a command string
- **THEN** the system SHALL execute the command in the OS shell and return stdout, stderr, and exit code

#### Scenario: write_file tool
- **WHEN** an Agent uses the `write_file` tool with a path and content
- **THEN** the system SHALL create the file under the storage directory and return the file path and size

#### Scenario: read_file tool
- **WHEN** an Agent uses the `read_file` tool with a file path
- **THEN** the system SHALL read the file content and return it as a string
