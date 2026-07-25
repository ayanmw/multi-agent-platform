# dynamic-tool-shell-injection-prevention Specification

## ADDED Requirements

### Requirement: DynamicTool shell command shall not be executed through shell interpolation
DynamicTool shell type SHALL parse the command template into a program name and a list of arguments. Template placeholders `{param}` SHALL be replaced, then the command SHALL be executed via `exec.CommandContext(ctx, program, args...)` without invoking an intermediate shell.

#### Scenario: Simple command with one parameter
- **WHEN** a dynamic tool has command `"echo {message}"` and receives input `{"message": "hello"}`
- **THEN** the platform executes `echo hello` as a single argument to `/bin/echo` (or OS equivalent) without spawning a shell

#### Scenario: Parameter containing shell metacharacters is not interpreted
- **WHEN** a dynamic tool receives input `{"name": "; rm -rf /"}` for command `"echo {name}"`
- **THEN** the literal string `"; rm -rf /"` is passed as a single argument and no shell command is executed

#### Scenario: Missing parameter preserves placeholder
- **WHEN** a dynamic tool command contains `{missing}` and the input map lacks that key
- **THEN** the placeholder `{missing}` remains in the argument value and the command executes without failure caused by placeholder replacement

### Requirement: Legacy shell-template behavior is deprecated
DynamicTool shell command strings that rely on shell syntax (`|`, `&&`, `;`, `$(...)`, backticks) SHALL fail or produce an explicit error unless a future explicit `shell: true` flag is present. This requirement documents the deprecation of raw `sh -c` execution for dynamic tools.

#### Scenario: Command template with pipe syntax
- **WHEN** a dynamic tool command contains `|`, `&&`, `;`, or `$(...)` without the future `shell: true` flag
- **THEN** execution returns an error indicating that complex shell syntax is not supported in dynamic tool commands
