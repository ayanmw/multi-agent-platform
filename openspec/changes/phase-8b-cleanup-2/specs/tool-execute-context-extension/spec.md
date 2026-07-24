# tool-execute-context-extension Specification

## Purpose
本规格定义 tool 执行上下文的扩展能力：在原有 `ExecuteContext` 中引入运行期标识（TaskID / AgentID / StepIdx / SessionID）、事件总线、LLM Provider，以及基于 `Workdir` 的工作目录控制，使 Engine 能向 tool 注入可信的运行时上下文，同时保持 `Tool.Execute(input)` 接口向后兼容。

## ADDED Requirements

### Requirement: ExecuteContext carries runtime identifiers
The system SHALL extend `tool.ExecuteContext` to include `TaskID`, `AgentID`, `StepIdx`, and `SessionID`.

#### Scenario: Engine constructs ExecuteContext for tool calls
- **WHEN** the engine invokes a tool via `ExecuteWithCtx`
- **THEN** it SHALL populate `TaskID`, `AgentID`, `StepIdx`, and `SessionID` from the current run context.

---

### Requirement: ExecuteContext carries EventBus
The system SHALL extend `tool.ExecuteContext` to include an `event.Bus` instance.

#### Scenario: Tool emits events during execution
- **WHEN** a tool implementation needs to send observable events
- **THEN** it SHALL use `ctx.EventBus.SendEvent(...)` with the identifiers from `ExecuteContext`.

---

### Requirement: ExecuteContext carries LLM provider
The system SHALL extend `tool.ExecuteContext` to include an `llm.Provider` instance.

#### Scenario: Tool needs internal LLM summarization
- **WHEN** a tool implementation requires an LLM call
- **THEN** it SHALL use `ctx.LLMProvider.Chat(...)` and report resulting usage.

---

### Requirement: ExecuteContext.Workdir controls tool CWD and path resolution
The system SHALL use `ExecuteContext.Workdir` as the authoritative working directory for builtin tools (`run_shell`, `write_file`, `read_file`) and dynamic shell tools. When `ctx.Workdir` is non-empty, it SHALL override any default CWD or relative-path base directory. LLM-provided `input["workdir"]` at the Engine layer SHALL be overwritten by the trusted `WorkdirHolder` value before tool invocation.

#### Scenario: run_shell uses ctx.Workdir as default CWD
- **WHEN** the engine invokes `run_shell` without an explicit `workdir` in input and with `ctx.Workdir=/tmp/ws`
- **THEN** the shell command SHALL execute with CWD `/tmp/ws`.

#### Scenario: write_file resolves relative path against ctx.Workdir
- **WHEN** the engine invokes `write_file` with `path="out.txt"` and `ctx.Workdir=/tmp/ws`
- **THEN** the file SHALL be created at `/tmp/ws/out.txt`.

#### Scenario: LLM-forged workdir is overridden by trusted WorkdirHolder
- **WHEN** the active worktree path is `/tmp/wt` and the LLM supplies `input["workdir"]="/etc"`
- **THEN** the engine SHALL overwrite `input["workdir"]` with `/tmp/wt` and invoke the tool with `ctx.Workdir=/tmp/wt`, preventing workdir escape.

#### Scenario: Dynamic shell tool honors ctx.Workdir
- **WHEN** the engine invokes a dynamic shell tool via `ExecuteWithCtx` with `ctx.Workdir=/tmp/ws`
- **THEN** the shell command SHALL run with CWD `/tmp/ws`.

---

### Requirement: Engine-level tool result usage aggregation
The system SHALL recognize a `_llm_usage` field in tool results and add its token counts to the current task's total usage.

#### Scenario: Tool returns _llm_usage
- **WHEN** a tool result includes `_llm_usage` with numeric token fields
- **THEN** the engine SHALL add those values to `e.tokenUsage.PromptTokens`, `CompletionTokens`, and `TotalTokens`.

#### Scenario: Tool result lacks _llm_usage
- **WHEN** a tool result does not include `_llm_usage`
- **THEN** the engine SHALL treat it as a regular tool call and not modify usage.

## MODIFIED Requirements

### Requirement: Engine invokes tools via ExecuteWithCtx
The system SHALL change `runtime.Engine.executeToolCall` to call `tool.Registry.ExecuteWithCtx` instead of `Registry.Execute`, passing a fully populated `ExecuteContext`.

#### Scenario: Engine executes any tool
- **WHEN** the engine needs to run a tool during a step
- **THEN** it SHALL call `ExecuteWithCtx` with the current `TaskID`, `AgentID`, `StepIdx`, `SessionID`, `Workdir`, `EventBus`, and `LLMProvider`.

