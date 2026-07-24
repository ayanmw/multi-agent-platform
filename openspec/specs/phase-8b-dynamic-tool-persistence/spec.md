# phase-8b-dynamic-tool-persistence Specification

## Purpose
TBD - created by archiving change phase-8b-cleanup-2. Update Purpose after archive.
## Requirements
### Requirement: Dynamic tool registration persists complete execution config
The system SHALL persist registered dynamic tools into the v27 `tools` table using `db.InsertToolV2`, including the full `execution_config` derived from the tool type and its type-specific fields (command, url, method, or code).

#### Scenario: Register a shell dynamic tool
- **WHEN** a client POST `/api/tools` with `type=shell` and a `command`
- **THEN** the system SHALL store a v27 record whose `execution_config` contains `{"type":"shell","command":"..."}` and return the registered tool metadata.

#### Scenario: Register an HTTP dynamic tool
- **WHEN** a client POST `/api/tools` with `type=http`, `url`, and `method`
- **THEN** the system SHALL store a v27 record whose `execution_config` contains `{"type":"http","url":"...","method":"..."}`.

#### Scenario: Register an inline dynamic tool
- **WHEN** a client POST `/api/tools` with `type=inline` and `code`
- **THEN** the system SHALL store a v27 record whose `execution_config` contains `{"type":"inline","code":"..."}`.

### Requirement: Dynamic tool registration rejects duplicate CanonicalName
The system SHALL reject a registration request when a tool with the same `CanonicalName` (namespace/name@version) already exists in the registry.

#### Scenario: Register a tool whose canonical name is already present
- **WHEN** a tool with namespace `ns`, name `tool-a`, version `1.0.0` is already registered
- **THEN** an attempt to register another tool with the same namespace/name/version SHALL fail with a clear conflict error.

### Requirement: Dynamic tool deletion removes both DB record and registry entry
The system SHALL delete the v27 `tools` record matching namespace, name, and version using `db.DeleteToolV2`, and also unregister the tool from the in-memory registry by its `CanonicalName`.

#### Scenario: Delete a registered dynamic tool
- **WHEN** a client DELETE `/api/tools/:name?namespace=ns&version=1.0.0`
- **THEN** the matching v27 record SHALL be removed and the registry SHALL no longer list the tool.

### Requirement: Dynamic tools reload from DB on startup
The system SHALL, during service startup, load all `source=local_db` records from the v27 `tools` table via `db.QueryToolsV2()` and `tool.DBToolLoader`, and register each valid record as a `DynamicTool` in the global registry.

#### Scenario: Service restarts after registering a shell tool
- **WHEN** the service restarts after a successful shell dynamic tool registration
- **THEN** the tool SHALL be present in the registry and its execution SHALL produce the expected shell command output.

#### Scenario: Skip legacy records without execution type
- **WHEN** a v27 record has an empty or type-missing `execution_config`
- **THEN** the system SHALL skip loading it with a warning log and not add an unusable tool to the registry.

### Requirement: API compatibility on tool registration response
The system SHALL keep the `/api/tools` registration response fields unchanged even though underlying persistence now uses `InsertToolV2`.

#### Scenario: Register via HTTP
- **WHEN** a client sends a valid registration request
- **THEN** the JSON response SHALL contain the same fields as before 8-B (name, description, parameters, type, command/url/method/code).

