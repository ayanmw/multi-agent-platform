// useTaskStore — reactive task state management composable
//
// This is the central state manager for the frontend. It:
//  1. Listens for WebSocket events via useWebSocket().onEvent()
//  2. Routes events to the correct agent/step based on task_id, agent_id, step_index
//  3. Maintains reactive TaskState that Vue components can render
//  4. Provides helper functions to start tasks and send control messages
//
// Event routing logic (mirrors the Go backend's ReAct loop):
//   task_started        → create TaskState, set status='running'
//   agent_ready         → register agent in task.agents
//   step_started        → create new Step object in agent.steps[]
//   llm_thinking/delta  → append to current step's thinking text
//   llm_message_complete → mark current step as completed
//   tool_call_started   → create ToolCallData on current step
//   tool_call_output    → set tool output on current step
//   tool_call_complete  → set tool duration, mark step completed
//   observation         → create observation step (LLM feedback)
//   task_completed      → set final result + total tokens
//   task_failed         → set status='failed' with reason
//
// Design rationale:
//   - All state is reactive (Vue refs) so components auto-update on changes
//   - Multiple agents are tracked independently in task.agents map
//   - Steps are ordered by index, not by arrival time (handles out-of-order events)
//   - The store is a singleton — one instance per app, shared via provide/inject

import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import type { AgentEvent, TaskState, AgentState, Step, ToolCallData } from '../types/events'

/** The reactive task state — null when no task is active */
const task = ref<TaskState | null>(null)

/** Remember the last user input so we can retry/continue a task */
const lastUserInput = ref<string>('')

/** Whether a task has been sent but not yet confirmed by WebSocket events */
const isTaskPending = ref(false)

/** Whether the store has been initialized (WebSocket listener registered) */
let initialized = false

// Agent color palette for multi-agent view
const AGENT_COLORS = [
  '#4a9eff', // blue
  '#51cf66', // green
  '#f0a030', // orange
  '#9b59b6', // purple
  '#e74c3c', // red
  '#1abc9c', // teal
  '#e67e22', // dark orange
  '#3498db', // light blue
]
let colorIdx = 0

export function useTaskStore() {
  const { status, connect, disconnect, sendControl, onEvent } = useWebSocket()

  // Initialize event listener once
  if (!initialized) {
    initialized = true
    onEvent(handleEvent)
  }

  /**
   * Route an incoming WebSocket event to the correct state mutation.
   * This is the event router — it maps event types to state changes.
   */
  function handleEvent(evt: AgentEvent) {
    // Clear pending state now that we have a real event from the server
    if (isTaskPending.value) {
      isTaskPending.value = false
    }

    // First event for a task initializes the task state
    if (!task.value && evt.type === 'task_started') {
      task.value = {
        id: evt.task_id,
        status: 'running',
        finalResult: null,
        totalTokens: 0,
        agents: {},
        startedAt: Date.now(),
      }
    }

    // If we don't have a task yet, ignore events (shouldn't happen in normal flow)
    if (!task.value) return

    const agentId = evt.agent_id || 'agent_default'

    // Ensure agent state exists
    if (!task.value.agents[agentId]) {
      task.value.agents[agentId] = {
        id: agentId,
        name: agentId,
        model: (evt.data.model as string) || 'unknown',
        steps: [],
        color: AGENT_COLORS[colorIdx++ % AGENT_COLORS.length],
      }
    }
    const agent = task.value.agents[agentId]

    switch (evt.type) {
      case 'task_started':
        task.value.status = 'running'
        break

      case 'agent_ready':
        agent.name = (evt.data.agent_name as string) || agentId
        agent.model = (evt.data.model as string) || agent.model
        agent.maxSteps = (evt.data.max_steps as number) || agent.maxSteps
        break

      case 'step_started': {
        const stepType = (evt.data.type as Step['type']) || 'think'
        agent.steps.push({
          index: agent.steps.length,
          type: stepType,
          status: 'running',
          thinking: '',
          toolCall: null,
          tokens: 0,
        })
        break
      }

      case 'llm_thinking':
      case 'llm_delta': {
        const lastStep = getLastStep(agent)
        if (lastStep && lastStep.type === 'think') {
          lastStep.thinking += (evt.data.content as string) || ''
        }
        break
      }

      case 'llm_message_complete': {
        const lastStep = getLastStep(agent)
        if (lastStep && lastStep.type === 'think') {
          lastStep.status = 'completed'
        }
        break
      }

      case 'tool_call_started': {
        const lastStep = getLastStep(agent)
        if (lastStep && lastStep.type === 'tool_call') {
          lastStep.toolCall = {
            name: (evt.data.tool as string) || 'unknown',
            input: (evt.data.args as Record<string, unknown>) || {},
            output: '',
            duration: 0,
          }
        }
        break
      }

      case 'tool_call_output': {
        const lastStep = getLastStep(agent)
        if (lastStep && lastStep.toolCall) {
          // Serialize result for display — if it's an object, pretty-print as JSON
          const result = evt.data.result
          lastStep.toolCall.output =
            typeof result === 'string' ? result : JSON.stringify(result, null, 2)
        }
        break
      }

      case 'tool_call_complete': {
        const lastStep = getLastStep(agent)
        if (lastStep && lastStep.toolCall) {
          lastStep.toolCall.duration = (evt.data.duration_ms as number) || 0
          lastStep.status = 'completed'
        }
        break
      }

      case 'tool_call_failed': {
        const lastStep = getLastStep(agent)
        if (lastStep && lastStep.type === 'tool_call') {
          lastStep.status = 'failed'
        }
        break
      }

      case 'observation': {
        // Observation is displayed as a separate step showing what the LLM saw
        agent.steps.push({
          index: agent.steps.length,
          type: 'observation',
          status: 'completed',
          thinking: (evt.data.content as string) || '',
          toolCall: null,
          tokens: 0,
        })
        break
      }

      case 'step_complete': {
        // The step_complete event marks the end of a step
        // The step's status is already set by tool_call_complete or llm_message_complete
        break
      }

      case 'task_completed': {
        task.value.status = 'completed'
        task.value.finalResult = (evt.data.result as string) || null
        task.value.totalTokens = (evt.data.total_tokens as number) || 0
        break
      }

      case 'task_failed':
        task.value.status = 'failed'
        {
          const reason = (evt.data.reason as string) || 'unknown error'
          const maxSteps = evt.data.max_steps as number | undefined
          const currentStep = evt.data.current_step as number | undefined
          let msg = `Task failed: ${reason}`
          if (reason === 'max_steps_exceeded' && maxSteps !== undefined) {
            msg = `Task failed: max steps (${maxSteps}) exceeded`
            if (currentStep !== undefined) {
              msg += ` at step ${currentStep}`
            }
          }
          task.value.finalResult = msg
          if (evt.data.error) {
            task.value.finalResult += `\n${evt.data.error}`
          }
        }
        break

      case 'agent_status': {
        // Real-time token usage and step progress update
        agent.currentStep = (evt.data.current_step as number) ?? agent.currentStep
        agent.maxSteps = (evt.data.max_steps as number) ?? agent.maxSteps
        agent.tokenUsage = {
          promptTokens: (evt.data.prompt_tokens as number) || 0,
          promptCacheHitTokens: (evt.data.prompt_cache_hit_tokens as number) || 0,
          promptCacheMissTokens: (evt.data.prompt_cache_miss_tokens as number) || 0,
          completionTokens: (evt.data.completion_tokens as number) || 0,
          totalTokens: (evt.data.total_tokens as number) || 0,
        }

        // Also update task-level token usage from aggregate
        if (task.value) {
          task.value.totalTokens = Object.values(task.value.agents).reduce(
            (sum, a) => sum + (a.tokenUsage?.totalTokens || 0),
            0
          )
          task.value.tokenUsage = {
            promptTokens: Object.values(task.value.agents).reduce(
              (sum, a) => sum + (a.tokenUsage?.promptTokens || 0),
              0
            ),
            promptCacheHitTokens: Object.values(task.value.agents).reduce(
              (sum, a) => sum + (a.tokenUsage?.promptCacheHitTokens || 0),
              0
            ),
            promptCacheMissTokens: Object.values(task.value.agents).reduce(
              (sum, a) => sum + (a.tokenUsage?.promptCacheMissTokens || 0),
              0
            ),
            completionTokens: Object.values(task.value.agents).reduce(
              (sum, a) => sum + (a.tokenUsage?.completionTokens || 0),
              0
            ),
            totalTokens: task.value.totalTokens,
          }
        }
        break
      }

      default:
        // Unknown event types are silently ignored
        break
    }
  }

  /** Get the last step in the agent's step array */
  function getLastStep(agent: AgentState): Step | null {
    if (agent.steps.length === 0) return null
    return agent.steps[agent.steps.length - 1]
  }

  /**
   * Start a chat task by POSTing to /api/tasks.
   * The WebSocket events will update the task state in real time.
   */
  async function startTask(input: string, agentId?: string, systemPrompt?: string, maxSteps?: number): Promise<string> {
    // Remember input for retry/continue actions
    lastUserInput.value = input
    isTaskPending.value = true
    // Safety timeout: clear pending state after 15s even if no event arrives
    const safetyTimeout = setTimeout(() => {
      if (isTaskPending.value) {
        isTaskPending.value = false
      }
    }, 15000)

    try {
      const body: Record<string, unknown> = {
        action: 'chat',
        agent_id: agentId || 'agent_default',
        input,
      }
      if (systemPrompt) body.system_prompt = systemPrompt
      if (maxSteps && maxSteps > 0) body.max_steps = maxSteps

      const resp = await fetch('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })

      if (!resp.ok) {
        isTaskPending.value = false
        clearTimeout(safetyTimeout)
        const errText = await resp.text()
        throw new Error(`Failed to start task: ${resp.status} ${errText}`)
      }

      const data = await resp.json()
      return data.task_id as string
    } catch (err) {
      isTaskPending.value = false
      clearTimeout(safetyTimeout)
      throw err
    }
  }

  /** Clear the current task state (for starting a new task) */
  function clearTask() {
    task.value = null
  }

  /**
   * Start a task from a preset case by case ID.
   * The case's system prompt and default input are loaded from the backend.
   */
  async function startTaskWithCase(caseId: string, agentId?: string): Promise<string> {
    isTaskPending.value = true
    const safetyTimeout = setTimeout(() => {
      if (isTaskPending.value) {
        isTaskPending.value = false
      }
    }, 15000)

    try {
      const resp = await fetch(`/api/tasks?case=${encodeURIComponent(caseId)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action: 'chat',
          agent_id: agentId || 'agent_default',
          // Input and system_prompt are filled by the backend from the case definition
          input: '', // backend fills from case
        }),
      })

      if (!resp.ok) {
        isTaskPending.value = false
        clearTimeout(safetyTimeout)
        const errText = await resp.text()
        throw new Error(`Failed to start case: ${resp.status} ${errText}`)
      }

      const data = await resp.json()
      return data.task_id as string
    } catch (err) {
      isTaskPending.value = false
      clearTimeout(safetyTimeout)
      throw err
    }
  }

  /** Pause the current task */
  function pauseTask() {
    if (!task.value) return
    const agents = Object.keys(task.value.agents)
    for (const agentId of agents) {
      sendControl({ action: 'pause', task_id: task.value.id, agent_id: agentId })
    }
  }

  /** Resume the current task */
  function resumeTask() {
    if (!task.value) return
    const agents = Object.keys(task.value.agents)
    for (const agentId of agents) {
      sendControl({ action: 'resume', task_id: task.value.id, agent_id: agentId })
    }
  }

  /** Cancel the current task */
  function cancelTask() {
    if (!task.value) return
    const agents = Object.keys(task.value.agents)
    for (const agentId of agents) {
      sendControl({ action: 'cancel', task_id: task.value.id, agent_id: agentId })
    }
  }

  /**
   * Start a multi-agent orchestration task.
   * Sends the input to /api/multi-agent which decomposes the task
   * and runs multiple agents concurrently.
   */
  async function startMultiAgentTask(input: string, caseType?: string): Promise<string> {
    isTaskPending.value = true
    const safetyTimeout = setTimeout(() => {
      if (isTaskPending.value) {
        isTaskPending.value = false
      }
    }, 15000)

    try {
      const resp = await fetch('/api/multi-agent', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          input,
          case_type: caseType || '',
        }),
      })

      if (!resp.ok) {
        isTaskPending.value = false
        clearTimeout(safetyTimeout)
        const errText = await resp.text()
        throw new Error(`Failed to start multi-agent task: ${resp.status} ${errText}`)
      }

      const data = await resp.json()
      return data.task_id as string
    } catch (err) {
      isTaskPending.value = false
      clearTimeout(safetyTimeout)
      throw err
    }
  }

  return {
    // Reactive state — components should treat this as read-only
    task,
    isTaskPending,
    wsStatus: status,
    lastUserInput,

    // Actions
    connect,
    disconnect,
    startTask,
    startTaskWithCase,
    startMultiAgentTask,
    clearTask,
    pauseTask,
    resumeTask,
    cancelTask,
  }
}