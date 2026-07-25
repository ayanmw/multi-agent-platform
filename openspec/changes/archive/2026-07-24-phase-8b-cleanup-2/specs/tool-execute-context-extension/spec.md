# tool-execute-context-extension Specification

## Purpose
本规格定义 tool 执行上下文的扩展能力：在原有 `ExecuteContext` 中引入运行期标识（TaskID / AgentID / StepIdx / SessionID）、事件总线、LLM Provider，以及基于 `Workdir` 的工作目录控制，使 Engine 能向 tool 注入可信的运行时上下文，同时保持 `Tool.Execute(input)` 接口向后兼容。

## ADDED Requirements

### Requirement: Engine invokes tools via ExecuteWithCtx
The system SHALL change `runtime.Engine.executeToolCall` to call `tool.Registry.ExecuteWithCtx` instead of `Registry.Execute`, passing a fully populated `ExecuteContext`.

#### Scenario: Engine executes any tool
- **WHEN** the engine needs to run a tool during a step
- **THEN** it SHALL call `ExecuteWithCtx` with the current `TaskID`, `AgentID`, `StepIdx`, `SessionID`, `Workdir`, `EventBus`, and `LLMProvider`.
