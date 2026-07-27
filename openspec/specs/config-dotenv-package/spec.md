# config-dotenv-package Specification

## Purpose
TBD - created by archiving change dotenv-package. Update Purpose after archive.
## Requirements
### Requirement: dotenv package exposes self-contained env loading and priority lookup

The `internal/config/dotenv` package SHALL provide a `Dotenv` type that loads a `.env` file into an in-memory cache, queries values with a configurable priority between file cache and OS environment variables, and reports the source of a value. The package SHALL also provide package-level functions that operate on a process-wide default instance for backward compatibility.

#### Scenario: Default dotenv priority lookup on an instance
- **WHEN** a `.env` file containing `FOO=fromfile` has been loaded into a new `Dotenv` instance and the OS environment also defines `FOO=fromenv`
- **THEN** `d.Getenv("FOO")` SHALL return `"fromfile"`

#### Scenario: OS-first priority lookup on an instance
- **WHEN** OS-first priority has been set on a `Dotenv` instance and the OS environment defines `FOO=fromenv` while the loaded `.env` file defines `FOO=fromfile`
- **THEN** `d.Getenv("FOO")` SHALL return `"fromenv"`

#### Scenario: Report value source on an instance
- **WHEN** both `.env` and OS define `FOO` in a `Dotenv` instance
- **THEN** `d.LookupEnv("FOO")` SHALL report `InDotEnv=true`, `InOS=true`, and the value selected by current priority.

#### Scenario: Independent instances do not share state
- **WHEN** `Dotenv` instance A has `FOO=fromA` and instance B has `FOO=fromB`
- **THEN** `A.Getenv("FOO")` SHALL return `"fromA"` and `B.Getenv("FOO")` SHALL return `"fromB"`

### Requirement: dotenv package supports standard dotenv syntax

The `internal/config/dotenv` package SHALL parse `.env` files using `github.com/joho/godotenv` and support quoted values, comments, empty lines, and the `export` prefix.

#### Scenario: Quoted value without literal quotes
- **WHEN** a `.env` file contains `FOO="bar"`
- **THEN** `d.Getenv("FOO")` SHALL return `bar` without the surrounding quotes.

#### Scenario: Inline comments are ignored
- **WHEN** a `.env` file contains `FOO=bar # comment`
- **THEN** `d.Getenv("FOO")` SHALL return `bar`.

### Requirement: config package remains backward compatible

`internal/config` SHALL continue to expose the existing env-related functions and `LookupEnvResult` type, forwarding to the `internal/config/dotenv` default instance so callers do not break.

#### Scenario: Existing caller uses config.Getenv
- **WHEN** any existing code calls `config.Getenv("FOO")`
- **THEN** it SHALL behave identically to `dotenv.Getenv("FOO")` and not require source modification.

### Requirement: tool test can read GEMINI_API_KEY without importing config

`internal/tool/web_search_gemini_test.go` SHALL read `GEMINI_API_KEY` via `internal/config/dotenv` instead of `os.Getenv` or `internal/config`, avoiding import cycles.

#### Scenario: Real Gemini smoke test uses dotenv key
- **WHEN** `TestRealGeminiSearch` runs in non-short mode with `GEMINI_API_KEY` set in `.env`
- **THEN** it SHALL pick up the key through `dotenv.Getenv("GEMINI_API_KEY")` and attempt the real network call.

### Requirement: Default instance auto-loads .env on package initialization

The `internal/config/dotenv` package SHALL automatically load the default `.env` file when the package is initialized so that `dotenv.Getenv` and `config.Getenv` have values available without an explicit load call.

#### Scenario: Default instance reads project .env at startup
- **WHEN** a Go process starts with a `.env` file in the working directory containing `FOO=bar`
- **THEN** `dotenv.Getenv("FOO")` SHALL return `"bar"` before any explicit `LoadFile` or `Reload` call.
