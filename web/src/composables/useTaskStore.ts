import { ref } from 'vue'
import { useWebSocket } from './useWebSocket'
import { useSessionStore } from './useSessionStore'
import { useToast } from './useToast'
import { useRecentMods } from './useRecentMods'
import type { SessionStatus } from './useSessionStore'
import type { AgentEvent, TaskState, TaskStatus, AgentState, Step, StepType, StepStatus, ToolCallData, AgentBusEventData, ContextWindowSnapshotData } from '../types/events'
import type { EvaluationResult } from '../types/case'

// 模块级引用，用于最近修改记录
const { addItem: addRecentMod } = useRecentMods()
const currentSessionId = ref<string>('')

/** Per-task reactive cache keyed by root task ID. */
const taskCache = ref<Record<string, TaskState>>({})

/** Per-sub-task context window snapshots keyed by sub_task_id (7-G). */
const subTaskSnapshots = ref<Record<string, ContextWindowSnapshotData>>({})

/**
 * Set of task IDs that were created optimistically before backend confirmation.
 * When a task is started, the frontend immediately inserts a placeholder so the
 * UI can display pending state. If the matching `task_started` WebSocket event
 * never arrives (e.g. the request failed/cancelled before the agent emitted it),
 * the placeholder would otherwise leak into the cache and appear in unrelated
 * sessions. This set tracks those IDs so App.vue can prune orphans on session
 * switch and prevent cross-session contamination.
 */
const optimisticTaskIds = new Set<string>()

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
  /** F6: error message shown in the dialog when approval times out */
  error?: string
}
const pendingApproval = ref<PendingApproval | null>(null)

/**
 * F6: approval timeout timer.
 * 后端审批窗口是 30s，前端给 28s 倒计时留 2s 余量，超时后主动 deny 并弹 toast。
 * 如果用户在 28s 内点了 Approve/Deny，handleEvent 里的 system_info approval 分支
 * 会通过 clearApprovalTimer() 清掉这个定时器。
 */
let approvalTimeoutTimer: ReturnType<typeof setTimeout> | null = null
const APPROVAL_TIMEOUT_MS = 28000

/**
 * F6: module-level sendControl reference.
 * startApprovalTimer 在模块作用域执行，无法直接拿到 useWebSocket().sendControl，
 * 因此 useTaskStore 初始化时把 sendControl 注入到这里。
 */
let sendControlFn: ((msg: { action: string; task_id: string; agent_id: string; [key: string]: unknown }) => void) | null = null
let showErrorFn: ((message: string) => void) | null = null

function clearApprovalTimer() {
  if (approvalTimeoutTimer) {
    clearTimeout(approvalTimeoutTimer)
    approvalTimeoutTimer = null
  }
}

function startApprovalTimer(approvalId: string, taskId: string, agentId: string) {
  clearApprovalTimer()
  approvalTimeoutTimer = setTimeout(() => {
    approvalTimeoutTimer = null
    // 超时：若 pendingApproval 还在且是同一个请求，标记错误 + 主动 deny + toast
    if (pendingApproval.value && pendingApproval.value.approvalId === approvalId) {
      pendingApproval.value = {
        ...pendingApproval.value,
        error: '审批请求已超时，操作已被拒绝',
      }
      // 通知后端拒绝（让等待中的 WaitForDecision 解除阻塞）
      if (sendControlFn) {
        sendControlFn({
          action: 'deny',
          task_id: taskId,
          agent_id: agentId,
          approval_id: approvalId,
        })
      }
      // 通过 toast 通知用户
      if (showErrorFn) {
        showErrorFn('审批请求已超时，操作已被拒绝')
      }
      // 短暂展示错误状态后清空对话框
      setTimeout(() => {
        if (pendingApproval.value && pendingApproval.value.approvalId === approvalId) {
          pendingApproval.value = null
        }
      }, 3000)
    }
  }, APPROVAL_TIMEOUT_MS)
}

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

  // F6: 注入 sendControl / showError 给模块级 startApprovalTimer 使用。
  // 一次性注入即可，多次赋值无副作用。
  sendControlFn = sendControl
  if (!showErrorFn) {
    const { showError } = useToast()
    showErrorFn = showError
  }

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
      const task = ensureTask(taskId)
      // A real task_started from the backend supersedes any optimistic
      // placeholder. Bind it to the correct session so it never bleeds into
      // another session's timeline.
      task.sessionId = (evt.data.session_id as string) || task.sessionId || ''
      task.userInput = (evt.data.input as string) || task.userInput || ''
      task.status = 'running'
      optimisticTaskIds.delete(taskId)
      // Capture session ID for recent-mods recording
      if (evt.data.session_id) {
        currentSessionId.value = evt.data.session_id as string
      }
      updateSession((evt.data.session_id as string) || '', {
        rootTaskId: taskId,
        status: 'running',
        userInput: (evt.data.input as string) || '',
      })
    }

    // 7-G: cache context-window snapshots by sub_task_id so each agent instance
    // has an isolated, inspectable view.
    if (evt.type === 'context_window_snapshot') {
      const subTaskID = evt.sub_task_id
      if (subTaskID) {
        subTaskSnapshots.value[subTaskID] = evt.data as unknown as ContextWindowSnapshotData
      }
      // Snapshot events update the context window cache but do not change agent
      // steps or task status, so we can stop here.
      return
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
      case 'task_started': {
        // task_started is handled above before the switch; this branch is kept
        // for exhaustiveness only.
        break
      }

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

          // 记录最近修改：write_file 成功时追加到最近修改列表
          if (lastStep.toolCall.name === 'write_file' && lastStep.toolCall.input) {
            const input = lastStep.toolCall.input as Record<string, unknown>
            const path = typeof input['path'] === 'string' ? input['path'] as string : null
            const output = typeof result === 'object' ? result as Record<string, unknown> : null
            if (path) {
              addRecentMod({
                path,
                success: lastStep.status !== 'failed',
                bytes: typeof output?.['bytes'] === 'number' ? output['bytes'] as number : undefined,
                sessionId: currentSessionId.value || undefined,
              })
            }
          }
        }
        break
      }

      case 'tool_call_complete': {
        const lastStep = getLastStep(agent)
        if (lastStep && lastStep.toolCall) {
          lastStep.toolCall.duration = (evt.data.duration_ms as number) || 0
          lastStep.status = 'completed'
          lastStep.durationMs = Date.now() - lastStep.startedAt

          // 兜底：如果 tool_call_output 没发（或失败了），在 complete 时仍记录
          if (lastStep.toolCall.name === 'write_file' && lastStep.toolCall.input) {
            const input = lastStep.toolCall.input as Record<string, unknown>
            const path = typeof input['path'] === 'string' ? input['path'] as string : null
            if (path) {
              const alreadyRecorded = (lastStep.toolCall.output?.includes('[') ?? false)
              if (!alreadyRecorded) {
                addRecentMod({
                  path,
                  success: true,
                  sessionId: currentSessionId.value || undefined,
                })
              }
            }
          }
        }
        break
      }

      case 'tool_call_failed': {
        const lastStep = getLastStep(agent)
        if (lastStep && lastStep.type === 'tool_call' && lastStep.toolCall) {
          lastStep.status = 'failed'
          lastStep.durationMs = Date.now() - lastStep.startedAt
          // 将错误信息写入 output，让前端 Output 面板也能显示错误详情
          if (evt.data.error && !lastStep.toolCall.output) {
            lastStep.toolCall.output = `[ERROR] ${evt.data.error}`
          }
          // 如果携带了 args，更新 input 显示（让前端能看到 LLM 实际传了什么参数）
          if (evt.data.args && lastStep.toolCall) {
            lastStep.toolCall.input = evt.data.args as Record<string, unknown>
          }
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
        task.durationMs = Date.now() - task.startedAt
        // Update session status to completed
        const sid = (evt.data.session_id as string) || ''
        if (sid) {
          updateSession(sid, { status: 'completed' })
        }
        break
      }

      case 'task_evaluated': {
        task.evaluation = {
          case_id: evt.data.case_id as string,
          passed: evt.data.passed as boolean,
          score: evt.data.score as number,
          reason: evt.data.reason as string,
        } as EvaluationResult
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
          task.durationMs = Date.now() - task.startedAt
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
        const totalTokens = (evt.data.total_tokens as number) ?? undefined
        if (sid && totalTokens !== undefined) {
          updateSession(sid, { totalTokens })
        }
        const durationMs = (evt.data.duration_ms as number) ?? undefined
        if (sid && durationMs !== undefined) {
          updateSession(sid, { durationMs })
        }
        break
      }

      case 'agent_status': {
        // Phase 7-A: 根据后端 agent_status 事件把 AgentState.status 切换到 paused/running，
        // 前端 UI 据此禁用 Cancel/Pause 之外的交互控件。
        const incomingStatus = evt.data.status as string | undefined
        if (incomingStatus === 'paused' || incomingStatus === 'running' ||
          incomingStatus === 'completed' || incomingStatus === 'failed') {
          agent.status = incomingStatus
        }
        agent.currentStep = (evt.data.current_step as number) ?? agent.currentStep
        agent.maxSteps = (evt.data.max_steps as number) ?? agent.maxSteps
        agent.tokenUsage = {
          promptTokens: (evt.data.prompt_tokens as number) || 0,
          promptCacheHitTokens: (evt.data.prompt_cache_hit_tokens as number) || 0,
          promptCacheMissTokens: (evt.data.prompt_cache_miss_tokens as number) || 0,
          completionTokens: (evt.data.completion_tokens as number) || 0,
          totalTokens: (evt.data.total_tokens as number) || 0,
        }
        // Accumulate agent duration from its steps for live display parity.
        agent.durationMs = agent.steps.reduce((sum, s) => sum + (s.durationMs || 0), 0)

        if (task) {
          task.totalTokens = Object.values(task.agents).reduce(
            (sum, a) => sum + (a.tokenUsage?.totalTokens || 0),
            0
          )
          task.durationMs = Object.values(task.agents).reduce(
            (sum, a) => sum + (a.durationMs || 0),
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
          // F6: 启动 28s 超时倒计时，到期自动 deny + toast
          startApprovalTimer(
            pendingApproval.value.approvalId,
            pendingApproval.value.taskId,
            pendingApproval.value.agentId,
          )
        } else if (infoType === 'agent_message_sent' || infoType === 'agent_message_received') {
          const task = taskCache.value[evt.task_id]
          if (task) {
            if (!task.agentMessages) {
              task.agentMessages = []
            }
            task.agentMessages.push({
              type: infoType,
              from_agent: (evt.data.from_agent as string) || evt.agent_id || 'unknown',
              to_agent: (evt.data.to_agent as string) || 'unknown',
              msg_type: (evt.data.msg_type as string) || 'observation',
              content: (evt.data.content as string) || '',
            } as AgentBusEventData)
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
   * Remove optimistic task placeholders that never received a confirming
   * `task_started` WebSocket event. These orphans otherwise leak into the
   * global taskCache and appear as phantom turns in whatever session is
   * currently active.
   */
  function pruneOrphanTasks() {
    if (optimisticTaskIds.size === 0) return
    for (const tid of Array.from(optimisticTaskIds)) {
      const t = taskCache.value[tid]
      // A real task_started flips status from 'idle' to 'running' (and usually
      // populates agents). If it's still idle and empty, it's an orphan.
      if (t && t.status === 'idle' && Object.keys(t.agents).length === 0) {
        delete taskCache.value[tid]
      }
    }
    optimisticTaskIds.clear()
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
      timeoutSeconds?: number
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
      if (options.timeoutSeconds !== undefined && options.timeoutSeconds >= 0) {
        body.timeout_seconds = options.timeoutSeconds
      }

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
      // Optimistic placeholder: backend accepted but authoritative state arrives
      // via WebSocket. Mark it so we can prune orphans that never get confirmed.
      const optimistic = ensureTask(data.task_id)
      optimistic.sessionId = data.session_id || options.sessionId || ''
      optimistic.userInput = input
      optimistic.status = 'idle'
      optimisticTaskIds.add(data.task_id)
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
      const optimistic = ensureTask(data.task_id)
      optimistic.sessionId = data.session_id || options.sessionId || ''
      optimistic.userInput = `Case: ${caseId}`
      optimistic.status = 'idle'
      optimisticTaskIds.add(data.task_id)
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
      timeoutSeconds?: number
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
      if (options.timeoutSeconds !== undefined && options.timeoutSeconds >= 0) {
        body.timeout_seconds = options.timeoutSeconds
      }

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
      // A new turn means the session is active again — reset the status
      // that may have been set to 'failed' by a previous turn's task_failed event.
      updateSession(data.session_id, { status: 'running' })
      activeTaskId.value = data.task_id
      const optimistic = ensureTask(data.task_id)
      optimistic.sessionId = data.session_id || options.sessionId || ''
      optimistic.userInput = input
      optimistic.status = 'idle'
      optimisticTaskIds.add(data.task_id)
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
    options: { caseType?: string; sessionId?: string; timeoutSeconds?: number; agents?: any[] } = {}
  ): Promise<{ sessionId: string; taskId: string }> {
    isTaskPending.value = true
    const safetyTimeout = setTimeout(() => {
      if (isTaskPending.value) {
        isTaskPending.value = false
      }
    }, 15000)

    try {
      const body: Record<string, unknown> = {
        input,
        action: 'multi-agent',
        case_type: options.caseType || '',
        session_id: options.sessionId || '',
        timeout_seconds: options.timeoutSeconds ?? 0,
      }
      if (options.agents && options.agents.length > 0) {
        body.agents = options.agents
      }
      const resp = await fetch('/api/multi-agent', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })

      if (!resp.ok) {
        isTaskPending.value = false
        clearTimeout(safetyTimeout)
        const errText = await resp.text()
        throw new Error(`Failed to start multi-agent task: ${resp.status} ${errText}`)
      }

      const data = (await resp.json()) as { session_id: string; task_id: string }
      activeTaskId.value = data.task_id
      const optimistic = ensureTask(data.task_id)
      optimistic.sessionId = data.session_id || options.sessionId || ''
      optimistic.userInput = input
      optimistic.status = 'idle'
      optimisticTaskIds.add(data.task_id)
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

  /** Load a session's full conversation history into the cache.
   *  Fetches GET /api/sessions/{id} which returns { session, tasks [] },
   *  then hydrates each historical task so sessionTurns can reconstruct every turn. */
  async function loadSessionTurns(sessionId: string): Promise<void> {
    console.log('[useTaskStore] loadSessionTurns:', sessionId)
    const resp = await fetch(`/api/sessions/${encodeURIComponent(sessionId)}`)
    if (!resp.ok) {
      throw new Error(`Failed to load session tasks: ${resp.status}`)
    }
    const data = (await resp.json()) as {
      session: { root_task_id: string | null; turn_count: number }
      tasks: Array<{
        id: string
        user_input: string
        status: string
        started_at: string
      }>
    }
    const tasks = (data.tasks || [])
    // Sort by started_at ASC so turns appear in chronological order
    tasks.sort((a, b) => a.started_at.localeCompare(b.started_at))
    let latestTask: { status: string; started_at: string } | undefined
    // Skip tasks already loaded (e.g. currently running ones that may arrive empty)
    let loaded = 0
    for (const t of tasks) {
      if (taskCache.value[t.id]) {
        // Track latest even if already cached so we can sync session status
        if (!latestTask || t.started_at > latestTask.started_at) latestTask = t
        continue
      }
      try {
        await loadTask(t.id)
        latestTask = t
        loaded++
      } catch (err) {
        console.warn('[useTaskStore] loadTask failed for', t.id, err)
      }
    }
    // Mirror the latest task's status onto the session so the sidebar
    // reflects the current reality (e.g. 'failed' → 'completed' after a
    // successful subsequent turn) instead of stale status pinned by
    // an earlier task_failed/task_completed event.
    if (latestTask && latestTask.status) {
      updateSession(sessionId, { status: latestTask.status as SessionStatus })
    }
    console.log('[useTaskStore] loadSessionTurns done, hydrated', loaded, 'tasks, keys:', Object.keys(taskCache.value))
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
      evaluation: {
        case_id: string
        passed: boolean
        score: number
        reason: string
        evaluated_at: string
      } | null
    }

    const task = data.task
    const steps = data.steps || []
    const childTasks = data.child_tasks || []

    // Build TaskState from persisted data. Always use the backend's session_id
    // so a historical task cannot accidentally inherit the current session.
    const taskState: TaskState = {
      id: task.id,
      sessionId: task.session_id || '',
      userInput: task.user_input || '',
      status: (task.status as TaskStatus) || 'completed',
      finalResult: task.final_result || null,
      totalTokens: task.total_tokens || 0,
      durationMs: (task as any).duration_ms ?? 0,
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

    // Hydrate case evaluation result if present so historical replays display it.
    if (data.evaluation) {
      taskState.evaluation = {
        case_id: data.evaluation.case_id,
        passed: data.evaluation.passed,
        score: data.evaluation.score,
        reason: data.evaluation.reason,
      }
    }

    // Group steps by agent_id to rebuild agent states.
    // Note: persisted steps (DB path) don't carry a per-step model field —
    // the WS real-time path already populates model correctly. For DB replay
    // we fall back to 'unknown' rather than pretend we know it.
    const agentStepsMap = new Map<string, typeof steps>()
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
        model: 'unknown',
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
            // Persisted steps lack per-step timestamps; use the task start time as
            // a common baseline so the timeline shows a real start point instead of 0.
            startedAt: taskState.startedAt,
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
      taskState.totalTokens += childTask.total_tokens || 0
      taskState.durationMs = (taskState.durationMs || 0) + (childTask as any).duration_ms || 0
      if (taskState.tokenUsage) {
        taskState.tokenUsage.totalTokens = taskState.totalTokens
      }
    }

    // Store in cache (do NOT mutate activeTaskId here — the caller decides
    // which task should be active after batch-loading).
    taskCache.value[taskId] = taskState
    console.log('[useTaskStore] loadTask done, taskState:', {
      id: taskState.id,
      status: taskState.status,
      agentCount: Object.keys(taskState.agents).length,
      totalSteps: Object.values(taskState.agents).reduce((s, a) => s + a.steps.length, 0),
      totalTokens: taskState.totalTokens,
      finalResult: taskState.finalResult?.substring(0, 100),
    })
  }

  /** Pause the active task or a specific agent */
  function pauseTask(agentId?: string) {
    if (!activeTaskId.value) return
    const task = taskCache.value[activeTaskId.value]
    if (!task) return
    const agents = agentId ? [agentId] : Object.keys(task.agents)
    for (const id of agents) {
      sendControl({ action: 'pause', task_id: task.id, agent_id: id })
    }
  }

  /** Resume the active task or a specific agent */
  function resumeTask(agentId?: string) {
    if (!activeTaskId.value) return
    const task = taskCache.value[activeTaskId.value]
    if (!task) return
    const agents = agentId ? [agentId] : Object.keys(task.agents)
    for (const id of agents) {
      sendControl({ action: 'resume', task_id: task.id, agent_id: id })
    }
  }

  /** Cancel the active task or a specific agent */
  function cancelTask(agentId?: string) {
    if (!activeTaskId.value) return
    const task = taskCache.value[activeTaskId.value]
    if (!task) return
    const agents = agentId ? [agentId] : Object.keys(task.agents)
    for (const id of agents) {
      sendControl({ action: 'cancel', task_id: task.id, agent_id: id })
    }
  }

  /** Approve a pending policy block — sent via WebSocket control message */
  function approveTask(approvalId: string, taskId: string, agentId: string) {
    clearApprovalTimer()
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
    clearApprovalTimer()
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
    subTaskSnapshots,
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
    loadSessionTurns,
    pruneOrphanTasks,
    pauseTask,
    resumeTask,
    cancelTask,
    approveTask,
    denyTask,
  }
}
