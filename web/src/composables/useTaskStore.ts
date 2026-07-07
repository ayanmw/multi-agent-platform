import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import { useSessionStore } from './useSessionStore'
import type { SessionStatus } from './useSessionStore'
import type { AgentEvent, TaskState, TaskStatus, AgentState, Step, StepType, StepStatus, ToolCallData } from '../types/events'

/** Per-task reactive cache */
const taskCache = ref<Record<string, TaskState>>({})

/** ID of the task currently being viewed */
const activeTaskId = ref<string | null>(null)

/** Whether a task has been sent but not yet confirmed by WebSocket events */
const isTaskPending = ref(false)

/** Remember the last user input so we can retry/continue a task */
const lastUserInput = ref<string>('')

/** Pending approval request from system_info event (set by handleEvent, consumed by App.vue) */
export interface PendingApproval {
  approvalId: string
  taskId: string
  agentId: string
  tool: string
  reason: string
  input: Record<string, any>
}
const pendingApproval = ref<PendingApproval | null>(null)

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
  const { updateSession } = useSessionStore()

  // Initialize event listener once
  if (!initialized) {
    initialized = true
    onEvent(handleEvent)
  }

  /**
   * Ensure a task exists in the cache.
   */
  function ensureTask(taskId: string): TaskState {
    if (!taskCache.value[taskId]) {
      taskCache.value[taskId] = {
        id: taskId,
        status: 'running',
        finalResult: null,
        totalTokens: 0,
        agents: {},
        startedAt: Date.now(),
      }
    }
    return taskCache.value[taskId]!
  }

  /**
   * Route an incoming WebSocket event to the correct state mutation.
   */
  function handleEvent(evt: AgentEvent) {
    // Clear pending state now that we have a real event from the server
    if (isTaskPending.value) {
      isTaskPending.value = false
    }

    const taskId = evt.task_id
    if (!taskId) return

    if (evt.type === 'task_started') {
      activeTaskId.value = taskId
      ensureTask(taskId)
      updateSession((evt.data.session_id as string) || '', {
        rootTaskId: taskId,
        status: 'running',
        userInput: (evt.data.input as string) || '',
      })
    }

    const task = taskCache.value[taskId]
    if (!task) return

    const agentId = evt.agent_id || 'agent_default'

    // Ensure agent state exists
    if (!task.agents[agentId]) {
      task.agents[agentId] = {
        id: agentId,
        name: agentId,
        model: (evt.data.model as string) || 'unknown',
        steps: [],
        color: AGENT_COLORS[colorIdx++ % AGENT_COLORS.length],
      }
    }
    const agent = task.agents[agentId]

    switch (evt.type) {
      case 'task_started':
        task.status = 'running'
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
          durationMs: 0,
          startedAt: Date.now(),
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
          lastStep.durationMs = Date.now() - lastStep.startedAt
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
          lastStep.durationMs = Date.now() - lastStep.startedAt
        }
        break
      }

      case 'tool_call_failed': {
        const lastStep = getLastStep(agent)
        if (lastStep && lastStep.type === 'tool_call') {
          lastStep.status = 'failed'
          lastStep.durationMs = Date.now() - lastStep.startedAt
        }
        break
      }

      case 'observation': {
        agent.steps.push({
          index: agent.steps.length,
          type: 'observation',
          status: 'completed',
          thinking: (evt.data.content as string) || '',
          toolCall: null,
          tokens: 0,
          durationMs: 0,
          startedAt: 0,
        })
        break
      }

      case 'step_complete': {
        // step status already handled by earlier events
        break
      }

      case 'task_completed': {
        task.status = 'completed'
        task.finalResult = (evt.data.result as string) || null
        task.totalTokens = (evt.data.total_tokens as number) || 0
        // Update session status to completed
        const sid = (evt.data.session_id as string) || ''
        if (sid) {
          updateSession(sid, { status: 'completed' })
        }
        break
      }

      case 'task_failed':
        task.status = 'failed'
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
          task.finalResult = msg
          if (evt.data.error) {
            task.finalResult += `\n${evt.data.error}`
          }
          // Update session status to failed
          const sid = (evt.data.session_id as string) || ''
          if (sid) {
            updateSession(sid, { status: 'failed' })
          }
        }
        break

      case 'session_status': {
        const sid = (evt.data.session_id as string) || ''
        const newStatus = (evt.data.status as string) || ''
        if (sid && newStatus) {
          updateSession(sid, { status: newStatus as SessionStatus })
        }
        break
      }

      case 'agent_status': {
        agent.currentStep = (evt.data.current_step as number) ?? agent.currentStep
        agent.maxSteps = (evt.data.max_steps as number) ?? agent.maxSteps
        agent.tokenUsage = {
          promptTokens: (evt.data.prompt_tokens as number) || 0,
          promptCacheHitTokens: (evt.data.prompt_cache_hit_tokens as number) || 0,
          promptCacheMissTokens: (evt.data.prompt_cache_miss_tokens as number) || 0,
          completionTokens: (evt.data.completion_tokens as number) || 0,
          totalTokens: (evt.data.total_tokens as number) || 0,
        }

        if (task) {
          task.totalTokens = Object.values(task.agents).reduce(
            (sum, a) => sum + (a.tokenUsage?.totalTokens || 0),
            0
          )
          task.tokenUsage = {
            promptTokens: Object.values(task.agents).reduce(
              (sum, a) => sum + (a.tokenUsage?.promptTokens || 0),
              0
            ),
            promptCacheHitTokens: Object.values(task.agents).reduce(
              (sum, a) => sum + (a.tokenUsage?.promptCacheHitTokens || 0),
              0
            ),
            promptCacheMissTokens: Object.values(task.agents).reduce(
              (sum, a) => sum + (a.tokenUsage?.promptCacheMissTokens || 0),
              0
            ),
            completionTokens: Object.values(task.agents).reduce(
              (sum, a) => sum + (a.tokenUsage?.completionTokens || 0),
              0
            ),
            totalTokens: task.totalTokens,
          }
        }
        break
      }

      case 'system_info': {
        const infoType = evt.data.type as string
        if (infoType === 'approval_required') {
          pendingApproval.value = {
            approvalId: (evt.data.approval_id as string) || '',
            taskId: evt.task_id,
            agentId: evt.agent_id || 'agent_default',
            tool: (evt.data.tool as string) || 'unknown',
            reason: (evt.data.reason as string) || 'Policy block',
            input: (evt.data.input as Record<string, any>) || {},
          }
        }
        break
      }

      case 'system_error': {
        // Log system errors to console for debugging
        console.error('[System Error]', evt.data.message || 'Unknown system error', evt.data)
        break
      }

      default:
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
  async function startTask(
    input: string,
    options: {
      agentId?: string
      systemPrompt?: string
      maxSteps?: number
      sessionId?: string
    } = {}
  ): Promise<{ sessionId: string; taskId: string }> {
    lastUserInput.value = input
    isTaskPending.value = true
    const safetyTimeout = setTimeout(() => {
      if (isTaskPending.value) {
        isTaskPending.value = false
      }
    }, 15000)

    try {
      const body: Record<string, unknown> = {
        action: 'chat',
        agent_id: options.agentId || 'agent_default',
        input,
        session_id: options.sessionId || '',
      }
      if (options.systemPrompt) body.system_prompt = options.systemPrompt
      if (options.maxSteps && options.maxSteps > 0) body.max_steps = options.maxSteps

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

      const data = (await resp.json()) as { session_id: string; task_id: string }
      activeTaskId.value = data.task_id
      ensureTask(data.task_id)
      return { sessionId: data.session_id, taskId: data.task_id }
    } catch (err) {
      isTaskPending.value = false
      clearTimeout(safetyTimeout)
      throw err
    }
  }

  /** Start a task from a preset case by case ID. */
  async function startTaskWithCase(
    caseId: string,
    options: { agentId?: string; sessionId?: string } = {}
  ): Promise<{ sessionId: string; taskId: string }> {
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
          agent_id: options.agentId || 'agent_default',
          input: '',
          session_id: options.sessionId || '',
        }),
      })

      if (!resp.ok) {
        isTaskPending.value = false
        clearTimeout(safetyTimeout)
        const errText = await resp.text()
        throw new Error(`Failed to start case: ${resp.status} ${errText}`)
      }

      const data = (await resp.json()) as { session_id: string; task_id: string }
      activeTaskId.value = data.task_id
      ensureTask(data.task_id)
      return { sessionId: data.session_id, taskId: data.task_id }
    } catch (err) {
      isTaskPending.value = false
      clearTimeout(safetyTimeout)
      throw err
    }
  }

  /**
   * Start a new turn in an existing multi-turn session.
   * POSTs to /api/sessions/{sessionId}/chat — used for subsequent turns
   * after the session already has a root task.
   */
  async function startTurn(
    input: string,
    options: {
      sessionId: string
      agentId?: string
      maxSteps?: number
    }
  ): Promise<{ sessionId: string; taskId: string; turnIndex: number }> {
    lastUserInput.value = input
    isTaskPending.value = true
    const safetyTimeout = setTimeout(() => {
      if (isTaskPending.value) isTaskPending.value = false
    }, 15000)

    try {
      const body: Record<string, unknown> = {
        input,
        agent_id: options.agentId || 'agent_default',
      }
      if (options.maxSteps && options.maxSteps > 0) body.max_steps = options.maxSteps

      const resp = await fetch(`/api/sessions/${encodeURIComponent(options.sessionId)}/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })

      if (!resp.ok) {
        isTaskPending.value = false
        clearTimeout(safetyTimeout)
        const errText = await resp.text()
        throw new Error(`Failed to start turn: ${resp.status} ${errText}`)
      }

      const data = (await resp.json()) as { session_id: string; task_id: string; turn_index: number }
      activeTaskId.value = data.task_id
      ensureTask(data.task_id)
      return { sessionId: data.session_id, taskId: data.task_id, turnIndex: data.turn_index }
    } catch (err) {
      isTaskPending.value = false
      clearTimeout(safetyTimeout)
      throw err
    }
  }

  /** Start a multi-agent task via /api/multi-agent. */
  async function startMultiAgentTask(
    input: string,
    options: { caseType?: string; sessionId?: string } = {}
  ): Promise<{ sessionId: string; taskId: string }> {
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
          case_type: options.caseType || '',
          session_id: options.sessionId || '',
        }),
      })

      if (!resp.ok) {
        isTaskPending.value = false
        clearTimeout(safetyTimeout)
        const errText = await resp.text()
        throw new Error(`Failed to start multi-agent task: ${resp.status} ${errText}`)
      }

      const data = (await resp.json()) as { session_id: string; task_id: string }
      activeTaskId.value = data.task_id
      ensureTask(data.task_id)
      return { sessionId: data.session_id, taskId: data.task_id }
    } catch (err) {
      isTaskPending.value = false
      clearTimeout(safetyTimeout)
      throw err
    }
  }

  /** Clear the active task reference without deleting data */
  function clearActiveTask() {
    activeTaskId.value = null
  }

  /** Set which task is being viewed */
  function setActiveTaskId(taskId: string | null) {
    activeTaskId.value = taskId
  }

  /** Load a task from the backend into the cache, hydrating agents and steps */
  async function loadTask(taskId: string): Promise<void> {
    console.log('[useTaskStore] loadTask started, taskId:', taskId)
    const resp = await fetch(`/api/tasks?id=${encodeURIComponent(taskId)}`)
    console.log('[useTaskStore] loadTask response:', resp.status, resp.statusText)
    if (!resp.ok) {
      throw new Error(`Failed to load task: ${resp.status}`)
    }
    const data = (await resp.json()) as {
      task: {
        id: string
        user_input: string
        status: string
        agent_ids: string[]
        final_result: string
        total_tokens: number
        started_at: string
        completed_at: string | null
        session_id: string
        parent_task_id: string
        is_root: boolean
      }
      steps: Array<{
        id: string
        task_id: string
        agent_id: string
        step_index: number
        type: string
        status: string
        content: string
        tool_name: string
        tool_input: Record<string, unknown>
        tool_output: string
        duration_ms: number
        token_used: number
      }>
      child_tasks: Array<{
        id: string
        user_input: string
        status: string
        agent_ids: string[]
        final_result: string
        total_tokens: number
        started_at: string
        completed_at: string | null
        session_id: string
        parent_task_id: string
        is_root: boolean
      }>
    }

    const task = data.task
    const steps = data.steps || []
    const childTasks = data.child_tasks || []

    // Build TaskState from persisted data
    const taskState: TaskState = {
      id: task.id,
      status: (task.status as TaskStatus) || 'completed',
      finalResult: task.final_result || null,
      totalTokens: task.total_tokens || 0,
      agents: {},
      startedAt: task.started_at ? new Date(task.started_at).getTime() : Date.now(),
      tokenUsage: {
        promptTokens: 0,
        promptCacheHitTokens: 0,
        promptCacheMissTokens: 0,
        completionTokens: 0,
        totalTokens: task.total_tokens || 0,
      },
    }

    // Group steps by agent_id to rebuild agent states
    const agentStepsMap = new Map<string, typeof steps>()
    const agentModelMap = new Map<string, string>()
    for (const step of steps) {
      const aid = step.agent_id || 'agent_default'
      if (!agentStepsMap.has(aid)) {
        agentStepsMap.set(aid, [])
      }
      agentStepsMap.get(aid)!.push(step)
    }

    // Build AgentState for each agent
    let colorIdx = 0
    for (const [agentId, agentSteps] of agentStepsMap) {
      // Sort steps by step_index
      agentSteps.sort((a, b) => a.step_index - b.step_index)

      const agentState: AgentState = {
        id: agentId,
        name: agentId,
        model: agentModelMap.get(agentId) || 'unknown',
        steps: agentSteps.map((s): Step => {
          const stepType: StepType =
            s.type === 'tool_call' ? 'tool_call' :
            s.type === 'observation' ? 'observation' : 'think'
          return {
            index: s.step_index,
            type: stepType,
            status: (s.status as StepStatus) || 'completed',
            thinking: stepType === 'think' ? (s.content || '') : '',
            toolCall: stepType === 'tool_call' ? {
              name: s.tool_name || 'unknown',
              input: s.tool_input || {},
              output: s.tool_output || '',
              duration: s.duration_ms || 0,
            } : null,
            tokens: s.token_used || 0,
            durationMs: s.duration_ms || 0,
            startedAt: 0, // historical steps don't have per-step timestamps
          }
        }),
        color: AGENT_COLORS[colorIdx++ % AGENT_COLORS.length],
      }

      taskState.agents[agentId] = agentState
    }

    // Also process child tasks — add their agents too
    for (const childTask of childTasks) {
      if (childTask.agent_ids) {
        for (const aid of childTask.agent_ids) {
          if (!taskState.agents[aid]) {
            taskState.agents[aid] = {
              id: aid,
              name: aid,
              model: 'unknown',
              steps: [],
              color: AGENT_COLORS[colorIdx++ % AGENT_COLORS.length],
            }
          }
        }
      }
      // Aggregate child task tokens
      taskState.totalTokens += childTask.total_tokens || 0
      if (taskState.tokenUsage) {
        taskState.tokenUsage.totalTokens = taskState.totalTokens
      }
    }

    // Store in cache and set as active
    taskCache.value[taskId] = taskState
    activeTaskId.value = taskId
    console.log('[useTaskStore] loadTask done, taskState:', {
      id: taskState.id,
      status: taskState.status,
      agentCount: Object.keys(taskState.agents).length,
      totalSteps: Object.values(taskState.agents).reduce((s, a) => s + a.steps.length, 0),
      totalTokens: taskState.totalTokens,
      finalResult: taskState.finalResult?.substring(0, 100),
    })
  }

  /** Pause the active task */
  function pauseTask() {
    if (!activeTaskId.value) return
    const task = taskCache.value[activeTaskId.value]
    if (!task) return
    const agents = Object.keys(task.agents)
    for (const agentId of agents) {
      sendControl({ action: 'pause', task_id: task.id, agent_id: agentId })
    }
  }

  /** Resume the active task */
  function resumeTask() {
    if (!activeTaskId.value) return
    const task = taskCache.value[activeTaskId.value]
    if (!task) return
    const agents = Object.keys(task.agents)
    for (const agentId of agents) {
      sendControl({ action: 'resume', task_id: task.id, agent_id: agentId })
    }
  }

  /** Cancel the active task */
  function cancelTask() {
    if (!activeTaskId.value) return
    const task = taskCache.value[activeTaskId.value]
    if (!task) return
    const agents = Object.keys(task.agents)
    for (const agentId of agents) {
      sendControl({ action: 'cancel', task_id: task.id, agent_id: agentId })
    }
  }

  /** Approve a pending policy block — sent via WebSocket control message */
  function approveTask(approvalId: string, taskId: string, agentId: string) {
    sendControl({
      action: 'approve',
      task_id: taskId,
      agent_id: agentId,
      approval_id: approvalId,
    })
    pendingApproval.value = null
  }

  /** Deny a pending policy block — sent via WebSocket control message */
  function denyTask(approvalId: string, taskId: string, agentId: string) {
    sendControl({
      action: 'deny',
      task_id: taskId,
      agent_id: agentId,
      approval_id: approvalId,
    })
    pendingApproval.value = null
  }

  return {
    // Reactive state
    taskCache,
    activeTaskId,
    isTaskPending,
    wsStatus: status,
    lastUserInput,
    pendingApproval,

    // Actions
    connect,
    disconnect,
    startTask,
    startTurn,
    startTaskWithCase,
    startMultiAgentTask,
    clearActiveTask,
    setActiveTaskId,
    loadTask,
    pauseTask,
    resumeTask,
    cancelTask,
    approveTask,
    denyTask,
  }
}
