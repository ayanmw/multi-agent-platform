## ADDED Requirements

### Requirement: AgentRunner exposes Recover
The system SHALL add `AgentRunner.Recover(ctx context.Context, spec RecoverSpec) (string, error)` as the single entry point for recovering a task from its checkpoint.

#### Scenario: Recover a checkpointed task
- **WHEN** a client POST `/api/checkpoints/recover` with a valid `task_id`
- **THEN** `handleRecoverCheckpoint` SHALL call `s.newRunner().Recover(ctx, RecoverSpec{TaskID: task_id})` and return the recovered agent ID or an error.

### Requirement: Recover builds a complete EngineConfig
The system SHALL, inside `AgentRunner.Recover`, construct an `EngineConfig` that contains the same fields as a normal chat run (`SkillRegistry`, `ActiveSkills`, `AgentBus`, `WorkingMemory`, `SessionMessageWriter`, `ActiveTodos`, `WorkspaceDir`, `Tracer`, `RootTraceCtx`, cost callbacks, etc.) derived from `AgentDeps` and the checkpointed task/session.

#### Scenario: Recovered agent has skill injection
- **WHEN** a task is recovered from checkpoint
- **THEN** the resumed engine SHALL receive the same `SkillRegistry` and enabled skill IDs as a fresh run for that session/agent.

#### Scenario: Recovered agent writes session messages
- **WHEN** the resumed engine produces assistant or tool messages
- **THEN** those messages SHALL be persisted to `session_messages` via the same writer used in normal chat runs.

#### Scenario: Recovered agent uses the original workspace directory
- **WHEN** a task is recovered
- **THEN** the engine SHALL run with `WorkspaceDir` set to the task's session workspace directory.

### Requirement: Recover reports task lifecycle events
The system SHALL emit `task_started` and, upon completion or failure, `task_completed`/`task_failed` events for a recovered run, analogous to normal runs.

#### Scenario: Successful recovery reaches completion
- **WHEN** the recovered engine finishes without error
- **THEN** the system SHALL broadcast `task_completed` with the final result and delete the consumed checkpoint.

#### Scenario: Recovery failure is observable
- **WHEN** the recovered engine fails (checkpoint missing, load error, or max steps exceeded)
- **THEN** the system SHALL return an error from `AgentRunner.Recover` and/or broadcast `task_failed` with a reason.

### Requirement: Recover distinguishes missing checkpoint
The system SHALL return a 404 Not Found when the checkpoint file for the requested task does not exist, instead of 500.

#### Scenario: Recover with non-existent task
- **WHEN** `/api/checkpoints/recover` is called with a `task_id` whose checkpoint file is missing
- **THEN** the handler SHALL respond with HTTP 404 and a clear message.
