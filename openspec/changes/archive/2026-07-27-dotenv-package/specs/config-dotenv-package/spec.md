## ADDED Requirements

### Requirement: dotenv package exposes self-contained env loading and priority lookup

The `internal/config/dotenv` package SHALL provide functions for loading a `.env` file into an in-memory cache, querying values with a configurable priority between file cache and OS environment variables, and reporting the source of a value.

#### Scenario: Default dotenv priority lookup
- **WHEN** a `.env` file containing `FOO=fromfile` has been loaded and the OS environment also defines `FOO=fromenv`
- **THEN** `dotenv.Getenv("FOO")` SHALL return `"fromfile"`

#### Scenario: OS-first priority lookup
- **WHEN** OS-first priority has been set and the OS environment defines `FOO=fromenv` while the loaded `.env` file defines `FOO=fromfile`
- **THEN** `dotenv.Getenv("FOO")` SHALL return `"fromenv"`

#### Scenario: Report value source
- **WHEN** both `.env` and OS define `FOO`
- **THEN** `dotenv.LookupEnv("FOO")` SHALL report `InDotEnv=true`, `InOS=true`, and the value selected by current priority.

### Requirement: dotenv package supports standard dotenv syntax

The `internal/config/dotenv` package SHALL parse `.env` files using `github.com/joho/godotenv` and support quoted values, comments, empty lines, and the `export` prefix.

#### Scenario: Quoted value without literal quotes
- **WHEN** a `.env` file contains `FOO="bar"`
- **THEN** `dotenv.Getenv("FOO")` SHALL return `bar` without the surrounding quotes.

#### Scenario: Inline comments are ignored
- **WHEN** a `.env` file contains `FOO=bar # comment`
- **THEN** `dotenv.Getenv("FOO")` SHALL return `bar`.

### Requirement: config package remains backward compatible

`internal/config` SHALL continue to expose the existing env-related functions and `LookupEnvResult` type, forwarding to `internal/config/dotenv` so callers do not break.

#### Scenario: Existing caller uses config.Getenv
- **WHEN** any existing code calls `config.Getenv("FOO")`
- **THEN** it SHALL behave identically to `dotenv.Getenv("FOO")` and not require source modification.

### Requirement: tool test can read GEMINI_API_KEY without importing config

`internal/tool/web_search_gemini_test.go` SHALL read `GEMINI_API_KEY` via `internal/config/dotenv` instead of `os.Getenv` or `internal/config`, avoiding import cycles.

#### Scenario: Real Gemini smoke test uses dotenv key
- **WHEN** `TestRealGeminiSearch` runs in non-short mode with `GEMINI_API_KEY` set in `.env`
- **THEN** it SHALL pick up the key through `dotenv.Getenv("GEMINI_API_KEY")` and attempt the real network call.
