## ADDED Requirements

### Requirement: Health Check Endpoint
The system SHALL expose a `/healthz` HTTP endpoint that returns the service health status.

#### Scenario: Healthy service
- **WHEN** all subsystems are operational
- **THEN** `/healthz` SHALL return HTTP 200 with JSON `{"status": "healthy", "checks": {...}}`

#### Scenario: Unhealthy subsystem
- **WHEN** a required subsystem (database, LLM provider) is unavailable
- **THEN** `/healthz` SHALL return HTTP 503 with the failing component identified

## ADDED Requirements

### Requirement: Structured Logging
The system SHALL emit structured log entries with consistent fields: timestamp, level, component, message, and context.

#### Scenario: Structured log entry
- **WHEN** a component logs an event
- **THEN** the log entry SHALL include `ts`, `level`, `component`, `msg`, and optional `task_id`, `agent_id`, `step_idx`

#### Scenario: Log level filtering
- **WHEN** the logging level is set to "warn"
- **THEN** only warn, error, and fatal messages SHALL be emitted

## ADDED Requirements

### Requirement: Metrics Endpoint
The system SHALL expose a `/metrics` HTTP endpoint in Prometheus text format with key operational metrics.

#### Scenario: Metrics response includes task counts
- **WHEN** `/metrics` is requested
- **THEN** the response SHALL include counters for tasks started, completed, failed, and active

#### Scenario: Metrics response includes cost totals
- **WHEN** cost tracking is enabled
- **THEN** the response SHALL include total cost by model and tier
