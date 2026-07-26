# tool-shell-workdir-scope-validation Specification

## ADDED Requirements

### Requirement: run_shell cmd.Dir must be validated against allowed scope
`run_shell` SHALL validate that `ExecuteContext.Workdir` is either empty or lies within the allowed file scope before setting `cmd.Dir`. The allowed scope is defined as the session workspace directory or the current active worktree path. An empty workdir SHALL retain the existing backward-compatible behavior.

#### Scenario: Workdir points inside session workspace
- **WHEN** `run_shell` is invoked with `ExecuteContext.Workdir` equal to or nested under the session `WorkspaceDir`
- **THEN** the command executes with `cmd.Dir` set to that workdir

#### Scenario: Workdir points inside active worktree
- **WHEN** `run_shell` is invoked with `ExecuteContext.Workdir` equal to or nested under the current active worktree path
- **THEN** the command executes with `cmd.Dir` set to that workdir

#### Scenario: Workdir outside allowed scope
- **WHEN** `run_shell` is invoked with `ExecuteContext.Workdir` outside both session workspace and active worktree
- **THEN** `run_shell` returns an error observation and does not execute the command

#### Scenario: Empty workdir
- **WHEN** `run_shell` is invoked with `ExecuteContext.Workdir == ""`
- **THEN** the tool uses the legacy behavior (input workdir or process CWD) without scope validation
