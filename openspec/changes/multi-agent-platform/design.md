# Design Document

## Context

用户需要一个可作为学习工具和产品使用的 AI Agent 系统。核心诉求：
- 实时可视化 Agent 执行过程中的每一步（think → tool_call → observation）
- 多 Agent 并行，每个 Agent 可配置不同的 API endpoint / model
- Go 后端 + Vue 3 前端，校内内部 API (`deepseek-v4-flash`)
- 可持久化、可扩展、可配置

当前状态：无现有代码，从零开始构建。
约束：单个仓库、本地 SQLite、内部 API 有限且模型能力一致。

## Goals / Non-Goals

**Goals:**
- Phase 0-4 内完成可运行 MVP（token 流 → Agent Loop → 多Agent并发 → 预设Cases）
- 后端 Agent Runtime 可独立于前端测试
- 前端实时展示所有 Agent 内部状态，支持展开/折叠/高亮
- SQLite 持久化所有执行历史和 Agent 配置
- 工具运行时注册，AI 可自描述/自注册工具

**Non-Goals:**
- 不接入外部 LLM provider（仅使用内部 API）
- 不实现用户认证/多租户（phase 0-4 范围）
- 不做 RAG 向量检索（Phase 6+ 再来）
- 不用 gRPC（Phase 0-4 用 WebSocket，gRPC 预留抽象接口）
- 不做移动端 UI

## Decisions

### D1: 通信协议 — WebSocket + JSON（Phase 0-4）

**Decision**: 前端使用 WebSocket + JSON 事件，Go 后端使用 `gorilla/websocket`。

**Rationale**:
- Vue 端 ws 协议原生支持，无需 grpc-web + envoy sidecar
- JSON 事件开发/调试直观，troubleshoot 零门槛
- 单个 WebSocket 连接承载所有事件（LLM token + tool 结果 + 状态更新）
- 协议格式设计为可无缝迁移到 gRPC：Go 后端抽象 EventBus 接口

**Alternatives considered**:
- gRPC streaming → 类型安全但 Vue 端需要额外代理层，Phase 0-4 价值不匹配复杂度
- SSE → 简单单向，无法承载客户端控制指令（暂停/取消）

**预留抽象**:
```go
type EventBus interface {
    Send(event Event) error
    OnMessage(handler func(Event))
    Close()
}
// WSAdapter (Phase 0-4) / GRPCAdapter (Phase 6+)
```

### D2: Agent Event Schema — 分层事件流

**Decision**: 定义统一的 `AgentEvent` 结构，所有前后端通信以此为基础。

**Schema**:
```go
type EventType string
const (
    TaskStarted         EventType = "task_started"
    TaskCompleted       EventType = "task_completed"
    TaskFailed          EventType = "task_failed"
    StepStarted         EventType = "step_started"
    LLMThinking         EventType = "llm_thinking"
    LLMToken            EventType = "llm_token"     // 单个 token
    LLMDelta            EventType = "llm_delta"      // 批量增量
    LLMMessageComplete  EventType = "llm_message_complete"
    ToolCallStarted     EventType = "tool_call_started"
    ToolCallInput       EventType = "tool_call_input"
    ToolCallOutput      EventType = "tool_call_output"
    ToolCallComplete    EventType = "tool_call_complete"
    Observation         EventType = "observation"
    Paused              EventType = "paused"
    Cancelled           EventType = "cancelled"
    Error               EventType = "error"
)
```

**Rationale**: 每个事件携带 `task_id` + `agent_id` + `step_index`，前端可独立追踪所有 Agent 的并行状态。`llm_delta` 优先（降低事件密度），`llm_token` 为备用（精确追踪）。

**Frontend State Tree**:
```typescript
interface TaskState {
    id: string; status: 'running' | 'completed' | 'failed';
    agents: Record<string, AgentState>;
    finalResult?: string;
    metrics: { totalTokens: number; totalDuration: number };
}

interface AgentState {
    currentStep: number;
    steps: Step[];
}

interface Step {
    type: 'think' | 'tool_call' | 'observation';
    status: 'running' | 'completed' | 'failed';
    thinking?: string;        // streaming 累加
    toolCall?: { name: string; input: any; output: string; duration: number };
}
```

### D3: SQLite 作为唯一持久化层

**Decision**: 使用 `modernc.org/sqlite`（纯 Go SQLite 实现），所有结构化数据存储于此；文件存储于本地 `storage/` 目录。

**Tables**:
- `agents` — Agent 配置 (name, system_prompt, model, api_endpoint, api_key_ref, tools JSON, config JSON)
- `tasks` — 任务执行记录 (user_input, status, agent_ids JSON, final_result, metrics)
- `steps` — 每步详细日志 (task_id, agent_id, step_index, type, content, tool_name, tool_input, tool_output, duration_ms)
- `tools` — 工具注册表 (name, description, schema JSON, handler_ref, enabled)
- `conversations` — 对话轮次 (task_id, role, content)
- `files` — 文件元数据 (filename, path, size, mime_type, metadata)

**Rationale**: 
- 单文件部署，零运维
- 学习曲线平缓，debug 工具成熟
- Phase 6+ 可无缝迁移到 PostgreSQL (schema 兼容)

**API Key 安全**: Phase 0-4 先明文存 SQLite（内部使用），后续加密。

### D4: Agent Loop — 自制 ReAct Loop + Tool Registry

**Decision**: 自制 ReAct Loop 引擎，而非封装 langchaingo。

**Loop 结构**:
```
Step loop:
1. SEND 用户 input + 历史上下文给 LLM
2. PARSE LLM 返回 (text / tool_call)
3. IF tool_call:
   a. Dispatch 到 Tool Registry
   b. SEND ToolCallStarted + ToolCallInput 事件
   c. 执行工具
   d. SEND ToolCallOutput 事件
   e. 将结果追加到上下文，继续 loop
4. IF text:
   a. SEND LLMDelta 事件（streaming）
   b. 完成后 SEND StepComplete
   c. 判断是否继续 loop 或结束
```

**Rationale**:
- 用户明确说"学习 Agent + Go"，自研才是学习路径
- 最大控制权：可以注入任意中间事件、精确控制状态转换
- edge case 多 → 正是学习的价值

**Tool Registry Interface**:
```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any  // JSON Schema
    Execute(ctx Context, input map[string]any) (any, error)
}

type ToolRegistry struct {
    tools map[string]Tool
}
func (r *ToolRegistry) Register(tool Tool)
func (r *ToolRegistry) Execute(name string, input any) (any, error)
func (r *ToolRegistry) List() []Tool
```

**Built-in Tools (Phase 1)**:
- `run_shell` — 执行 shell 命令（沙箱待 Phase 5+）
- `write_file` — 写入文件到 storage/
- `read_file` — 读取文件

### D5: 内部 API Client — OpenAI SDK 兼容层

**Decision**: 内部 API 完全兼容 OpenAI format，使用 `openai` Go SDK 或手工构建 HTTP client。

**配置**:
```go
type LLMConfig struct {
    Endpoint  string  // https://aicoding.dobest.com/v1
    APIKey    string
    Model     string  // deepseek-v4-flash
    MaxTokens int
    Temperature float32
}

// 每个 Agent 绑定一个 LLMConfig
// 多 Agent 并行时各自独立调用
```

**Streaming**: 使用 `httputil` 或 `net/http` 直接读取 SSE 流，逐 token 解析和转发。

## Risks / Trade-offs

| Risk | Impact | Mitigation |
|------|--------|-----------|
| Function Calling 格式不稳定（deepseek API 可能不完全兼容 OpenAI） | 高 | Phase 1 先手工构建 request/response，format 调试后再决定是否换 SDK |
| 大量 WebSocket 消息导致前端性能问题 | 中 | `llm_delta` 批量事件（每 50ms 或 100 tokens 批量发送），前端虚拟滚动 |
| 多 Agent 并行 SSE 解析复杂度 | 中 | Phase 1 只做单 Agent stream，Phase 4 再加并发 |
| SQLite 写并发（多个 Agent 同时写） | 低 | 单写连接 + mutex，Go 内部 serializes |
| Tool 执行无沙箱（run_shell 危险） | 高 | Phase 1 允许，文档注明，Phase 5+ 加 Docker 沙箱 |
| LLM 超时/重试策略 | 中 | Phase 1 简单超时（30s），Phase 3 加指数退避重试 |

## Migration Plan

Phase 0-4 均为新系统，无迁移。6 阶段独立递进：

```
Phase 0 → Phase 1 → Phase 2 → Phase 3 → Phase 4
 (骨架)    (Agent)    (UI)     (Cases)   (并发)

每个 Phase 完成后，手动验证，确认后再进下一 Phase。
```

## Open Questions

- `run_shell` 的沙箱方案（docker / firecracker / 仅 win 本地？）— Phase 5 再定
- 前端 Vue 状态管理（Pinia vs reactive store）— Phase 2 决定
- Agent 间通信协议（Agent A 调用 Agent B 的输入输出格式）— Phase 4 细化
- 前端 Markdown 渲染库（markdown-it / marked + 自定义组件）— Phase 2 决定
