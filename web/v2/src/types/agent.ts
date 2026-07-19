/** Multi-agent workflow configuration types (Phase 7-F) */

export interface WorkflowAgentSpec {
  /** Unique agent ID used in events and persistence */
  agentId: string
  /** Display name shown in the UI */
  name: string
  /** System prompt for this agent */
  systemPrompt: string
  /** Per-agent input override; defaults to the user's main input */
  input?: string
  /** Tools this agent is allowed to use */
  allowedTools?: string[]
  /** Agents that should receive this agent's final result (for parallel mode) */
  outputTo?: string[]
  /** Model override; empty means use the configured default */
  model?: string
}

export interface WorkflowConfig {
  /** Execution strategy */
  strategy: 'parallel' | 'sequential' | 'pipeline'
  /** Ordered list of agents in the workflow */
  agents: WorkflowAgentSpec[]
}
