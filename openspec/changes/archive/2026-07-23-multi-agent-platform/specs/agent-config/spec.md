## ADDED Requirements

### Requirement: Agent CRUD Operations
The system SHALL provide full Create, Read, Update, Delete operations for Agent configurations via REST API.

#### Scenario: Create a new Agent
- **WHEN** a client sends a POST request to `/api/agents` with name, system_prompt, model, api_endpoint, and api_key
- **THEN** the system SHALL validate the input, persist the Agent to SQLite, and return the created Agent with its generated id

#### Scenario: List all Agents
- **WHEN** a client sends a GET request to `/api/agents`
- **THEN** the system SHALL return all registered Agents, excluding the raw api_key values (returning masked keys or key references only)

#### Scenario: Update an Agent
- **WHEN** a client sends a PUT request to `/api/agents/{id}` with updated fields
- **THEN** the system SHALL update the Agent in SQLite and return the updated configuration

#### Scenario: Delete an Agent
- **WHEN** a client sends a DELETE request to `/api/agents/{id}`
- **THEN** the system SHALL remove the Agent from SQLite (soft delete or hard delete) and return success

### Requirement: Multi-API Configuration
Each Agent SHALL support its own independent LLM API configuration.

#### Scenario: Agents with different API endpoints
- **WHEN** two Agents are configured with different api_endpoint values
- **THEN** each Agent SHALL route its LLM calls to its own configured endpoint

#### Scenario: Agents with different models
- **WHEN** two Agents use different model values (e.g., deepseek-v4-flash vs gpt-4)
- **THEN** each Agent SHALL use its own configured model for LLM calls

### Requirement: Tool Allowlist per Agent
Each Agent SHALL have a configurable list of allowed Tools.

#### Scenario: Agent with restricted tools
- **WHEN** an Agent is configured with tools: ["write_file", "read_file"] (no run_shell)
- **THEN** the system SHALL only execute write_file and read_file for that Agent, rejecting any other tool invocation with an error
