# env-dotenv-priority Specification

## Purpose
TBD - created by archiving change env-dotenv-priority. Update Purpose after archive.
## Requirements
### Requirement: Getenv prefers .env by default
The system SHALL provide a `config.Getenv(key)` function that returns the value from the `.env` file if the key exists there; otherwise it SHALL fall back to the system environment variable.

#### Scenario: key exists only in .env
- **WHEN** `.env` contains `FOO=fromfile` and the system environment variable `FOO` is not set
- **THEN** `config.Getenv("FOO")` SHALL return `"fromfile"`

#### Scenario: key exists in both .env and system env with default dotenv priority
- **WHEN** `.env` contains `FOO=fromfile` and the system environment variable `FOO=fromenv`
- **THEN** `config.Getenv("FOO")` SHALL return `"fromfile"`

#### Scenario: key exists only in system env
- **WHEN** the system environment variable `FOO=fromenv` and `.env` does not define `FOO`
- **THEN** `config.Getenv("FOO")` SHALL return `"fromenv"`

### Requirement: Priority can be switched to OS-first
The system SHALL allow callers to switch `config.Getenv` to system-environment-first via `config.SetOSFirst()`, falling back to `.env` only when the system environment variable is absent.

#### Scenario: SetOSFirst makes system env win
- **WHEN** `.env` contains `FOO=fromfile`, the system env contains `FOO=fromenv`, and `config.SetOSFirst()` has been called
- **THEN** `config.Getenv("FOO")` SHALL return `"fromenv"`

### Requirement: .env loading does not pollute os.Getenv
The system SHALL load `.env` into an in-memory cache rather than calling `os.Setenv` for every loaded variable.

#### Scenario: LoadEnvFile keeps os env untouched
- **WHEN** `.env` contains `FOO=fromfile` and `config.LoadEnvFile(path)` is called
- **THEN** `os.Getenv("FOO")` SHALL remain unchanged
- **AND** `config.Getenv("FOO")` SHALL return `"fromfile"`

### Requirement: Existing compatibility helper remains available
The system SHALL keep an `ApplyEnvFileToOS(path)` helper that writes `.env` values into system environment variables only when they are not already set, preserving the legacy behavior for consumers that need it.

#### Scenario: ApplyEnvFileToOS writes missing keys only
- **WHEN** `.env` contains `FOO=fromfile` and `BAR=fromfile`, and the system env already contains `FOO=fromenv`
- **THEN** after `ApplyEnvFileToOS(path)`, `os.Getenv("FOO")` SHALL still be `"fromenv"`
- **AND** `os.Getenv("BAR")` SHALL be `"fromfile"`

