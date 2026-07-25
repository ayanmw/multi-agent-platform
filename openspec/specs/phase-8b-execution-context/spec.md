# phase-8b-execution-context Specification

## Purpose
TBD - created by archiving change phase-8b-cleanup-2. Update Purpose after archive.
## Requirements
### Requirement: Registry exposes ExecuteWithCtx
The system SHALL provide `tool.Registry.ExecuteWithCtx(name string, ctx ExecuteContext, input map[string]any)` so callers can pass a runtime execution context alongside tool input.

#### Scenario: Engine invokes tool with context
- **WHEN** the engine calls `ExecuteWithCtx` with a non-empty `ExecuteContext`
- **THEN** the registry SHALL look up the tool by name and invoke its context-aware executor with the supplied context.

### Requirement: Engine uses ExecuteWithCtx for all tool calls
The system SHALL change `runtime.Engine.executeToolCall` to call `ExecuteWithCtx` instead of `Execute`, passing a context whose `Workdir` is the engine's configured workspace directory.

#### Scenario: Engine executes run_shell in a session workspace
- **WHEN** the engine invokes `run_shell` with no explicit `workdir` in input
- **THEN** the shell command SHALL run in `EngineConfig.WorkspaceDir`.

### Requirement: Builtin executor honors ctx.Workdir
The system SHALL update `BuiltinTool` execution so that, when `ctx.Workdir` is non-empty, it is used as the base directory for resolving relative paths and as the default CWD for `run_shell`.

#### Scenario: write_file with relative path and ctx.Workdir
- **WHEN** `write_file` receives `path="out.txt"` and `ctx.Workdir=/tmp/ws`
- **THEN** the file SHALL be created at `/tmp/ws/out.txt`.

#### Scenario: run_shell CWD from ctx.Workdir
- **WHEN** `run_shell` is invoked with `ctx.Workdir=/tmp/ws` and no input `workdir`
- **THEN** the command SHALL execute with CWD `/tmp/ws`.

### Requirement: Dynamic executor honors ctx.Workdir
The system SHALL ensure that `DynamicExecutor` (used by `DynamicTool`) already respects `ctx.Workdir` for `type=shell` and any other type that resolves filesystem paths.

#### Scenario: Dynamic shell tool with ctx.Workdir
- **WHEN** a dynamic shell tool is executed via `DynamicExecutor.Execute` with `ctx.Workdir=/tmp/ws`
- **THEN** the shell command SHALL run with CWD `/tmp/ws`.

### Requirement: LLM-supplied workdir is overridden by trusted WorkdirHolder
The system SHALL, in Engine-level tool invocation, overwrite `input["workdir"]` with the value from `WorkdirHolder` before calling the tool, so an LLM cannot escape the active worktree by forging a workdir.

#### Scenario: LLM passes workdir outside worktree
- **WHEN** the active worktree path is `/tmp/wt` and the LLM supplies `input["workdir"]="/etc"`
- **THEN** the tool SHALL receive `ctx.Workdir=/tmp/wt` (or `input["workdir"]` set to `/tmp/wt`) and operate inside the worktree.

### Requirement: Tool.Execute remains backward compatible
The system SHALL keep the public `tool.Tool` interface signature `Execute(input map[string]any) (any, error)` unchanged.

#### Scenario: Existing external caller uses Tool.Execute
- **WHEN** code outside the engine calls `tool.Execute(input)` directly
- **THEN** it SHALL continue to compile and behave as before, using an empty `ExecuteContext` internally.

