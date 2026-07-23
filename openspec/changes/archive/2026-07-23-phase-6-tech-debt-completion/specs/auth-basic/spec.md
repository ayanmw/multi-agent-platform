## ADDED Requirements

### Requirement: API Key Authentication
The system SHALL support API Key authentication for inbound requests, validating keys against stored credentials.

#### Scenario: Valid API Key
- **WHEN** a request includes a valid API Key in the Authorization header
- **THEN** the system SHALL authenticate the request and allow access

#### Scenario: Invalid API Key
- **WHEN** a request includes an invalid or missing API Key
- **THEN** the system SHALL return HTTP 401 Unauthorized

#### Scenario: API Key scopes
- **WHEN** a request is made with a scoped API Key
- **THEN** the system SHALL enforce scope-based access control (e.g., read-only vs read-write)

## ADDED Requirements

### Requirement: User and Role Model
The system SHALL maintain a User and Role model stored in SQLite, supporting multiple users with different permission levels.

#### Scenario: Create user with role
- **WHEN** an admin creates a new user
- **THEN** the system SHALL assign the user a role (admin, user, viewer)

#### Scenario: Role-based access control
- **WHEN** a user performs an action
- **THEN** the system SHALL check the user's role permissions before allowing the action

## ADDED Requirements

### Requirement: API Key CRUD for Users
The system SHALL allow users to create, list, and revoke their own API Keys via REST API.

#### Scenario: User creates API Key
- **WHEN** a user requests a new API Key
- **THEN** the system SHALL generate a secure random key, hash it, and return the plaintext key once

#### Scenario: User revokes API Key
- **WHEN** a user revokes an existing API Key
- **THEN** the system SHALL immediately invalidate the key and reject future requests
