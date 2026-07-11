// Event system type definitions — mirrors the Go backend's pkg/event/event.go

/** Event type enum — aligned with Go backend's event types */
export type EventType =
  // Lifecycle events
  | 'agent_ready'
  | 'task_started'
  | 'task_completed'
  | 'task_failed'
  // Step events
  | 'step_started'
  | 'step_complete'
  // LLM events
  | 'llm_thinking'
  | 'llm_delta'
  | 'llm_message_complete'
  // Tool events
  | 'tool_call_started'
  | 'tool_call_output'
  | 'tool_call_complete'
  | 'tool_call_failed'
  // Observation
  | 'observation'
  // Status event for real-time metrics (Phase 4+)
  | 'agent_status'
  // System events
  | 'system_info'
  | 'system_error'
  | 'session_status'

/** Raw event from the WebSocket — matches Go's Event struct */
export interface AgentEvent {
  event_id: string
  task_id: string
  agent_id: string
  step_index: number
  type: EventType
  timestamp: number
  data: Record<string, unknown>
}

/** Step type — think or tool_call */
export type StepType = 'think' | 'tool_call' | 'observation'

/** Step status */
export type StepStatus = 'running' | 'completed' | 'failed'

/** Token usage details per agent / per task */
export interface TokenUsage {
  /** Total prompt (input) tokens */
  promptTokens: number
  /** Tokens read from cache (cheap) */
  promptCacheHitTokens: number
  /** Tokens not in cache */
  promptCacheMissTokens: number
  /** Completion (output) tokens */
  completionTokens: number
  /** Total tokens = prompt + completion */
  totalTokens: number
}

/** Tool call data stored in a step */
export interface ToolCallData {
  name: string
  input: Record<string, unknown>
  output: string
  duration: number
  /** Internal: track whether input is currently formatted */
  _inputFormatted?: boolean
  /** Internal: original compact JSON before formatting */
  _inputCompact?: string
  /** Internal: track whether output is currently formatted */
  _outputFormatted?: boolean
  /** Internal: original raw output before formatting */
  _outputRaw?: string
}

/** A single step in the agent's execution tree */
export interface Step {
  index: number
  type: StepType
  status: StepStatus
  /** Accumulated thinking text from llm_delta events */
  thinking: string
  /** Tool call data (only for tool_call steps) */
  toolCall: ToolCallData | null
  /** Token usage for this step */
  tokens: number
  /** Actual duration of this step in ms (set when step completes) */
  durationMs: number
  /** Timestamp when this step started (ms since epoch, internal tracking) */
  startedAt: number
}

/** An agent within a task */
export interface AgentState {
  id: string
  name: string
  model: string
  steps: Step[]
  /** Display color for this agent in multi-agent view */
  color?: string
  /** Max ReAct loop steps for this agent */
  maxSteps?: number
  /** Current step index (tool execution rounds) */
  currentStep?: number
  /** Detailed token usage for this agent */
  tokenUsage?: TokenUsage
}

/** Task status
 *  - 'idle': task/session exists but hasn't started executing (DB may return this)
 *  - 'running': agent is actively executing
 *  - 'completed': finished successfully
 *  - 'failed': finished with error
 */
export type TaskStatus = 'idle' | 'running' | 'completed' | 'failed'

/** The top-level task state */
export interface TaskState {
  id: string
  status: TaskStatus
  /** Final result text (only when completed) */
  finalResult: string | null
  /** Total tokens consumed across all agents */
  totalTokens: number
  /** Map of agent_id → AgentState */
  agents: Record<string, AgentState>
  /** Timestamp when the task was started (ms since epoch) */
  startedAt: number
  /** Detailed token usage across all agents */
  tokenUsage?: TokenUsage
}

/** Control message sent from client to server via WebSocket */
export interface ClientControlMessage {
  action: 'pause' | 'resume' | 'cancel' | 'approve' | 'deny'
  task_id: string
  agent_id: string
  [key: string]: unknown
}

/** Chat request sent to POST /api/tasks */
export interface ChatRequest {
  action: 'chat'
  agent_id: string
  input: string
  system_prompt?: string
  max_steps?: number
  session_id?: string
}

/** Chat response from POST /api/tasks */
export interface ChatResponse {
  session_id: string
  task_id: string
  agent_id: string
  action: string
}

/** Session summary returned from GET /api/sessions */
export interface SessionSummary {
  id: string
  name: string
  root_task_id: string | null
  status: 'empty' | 'running' | 'completed' | 'failed'
  user_input: string
  total_tokens: number
  created_at: number
  updated_at: number
}

/** Session detail from GET /api/sessions/:id */
export interface SessionDetail {
  session: SessionSummary
  tasks: TaskSummary[]
}

/** Task summary for session lists */
export interface TaskSummary {
  id: string
  user_input: string
  status: TaskStatus
  total_tokens: number
  started_at: number
  is_root: boolean
  session_id: string
  parent_task_id: string | null
}
