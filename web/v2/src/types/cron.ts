// Cron 子系统类型定义 — 与后端 internal/cron 结构对齐。
//
// 镜像 internal/cron/model.go 的 Cron / Execution / ScheduleType / ActionType /
// Status / ExecStatus 与各 ActionPayload 子结构，供 useCrons / useCronEvents /
// CronManager / CronForm / CronExecutions / CronDockPanel 共用。

/** 调度规则类型 */
export type ScheduleType = 'cron' | 'interval' | 'once'

/** 触发动作类型 */
export type ActionType = 'start_task' | 'script' | 'webhook' | 'notify_session'

/** cron 状态 */
export type CronStatus = 'enabled' | 'disabled' | 'paused'

/** 执行记录状态 */
export type ExecStatus = 'running' | 'completed' | 'failed' | 'skipped' | 'missed'

/** 前端展示类型（preset | interval | cron | once） */
export type DisplayType = 'preset' | 'interval' | 'cron' | 'once' | string

/** 来源 */
export type CronSource = 'user' | 'agent' | string

/** 定时器领域对象，对齐后端 Cron */
export interface Cron {
  id: string
  name: string
  description: string
  schedule_type: ScheduleType
  cron_expr: string
  display_type: DisplayType
  timezone: string
  once_at: string
  action_type: ActionType
  action_payload: Record<string, unknown>
  status: CronStatus
  allow_concurrent: boolean
  source: CronSource
  owner: string
  last_triggered_at: number | null
  next_trigger_at: number | null
  last_execution_id: string
  trigger_count: number
  created_at: number
  updated_at: number
}

/** 单次执行记录，对齐后端 Execution */
export interface CronExecution {
  id: string
  cron_id: string
  triggered_at: number
  status: ExecStatus
  reason: string
  rendered_input: string
  result_summary: string
  task_id: string
  session_id: string
  duration_ms: number
  error: string
  created_at: number
}

/** start_task action 的 payload */
export interface StartTaskPayload {
  agent_id: string
  session_id?: string
  input: string
  system_prompt?: string
  max_steps?: number
  timeout_seconds?: number
  scope?: string
  allowed_tools?: string[]
  token_budget?: number
  cost_budget_usd?: number
  case_id?: string
}

/** script action 的单个 tool 调用 */
export interface ScriptToolCall {
  tool: string
  input: Record<string, unknown>
  approval?: boolean
}

/** script action 的 payload */
export interface ScriptPayload {
  tool_calls: ScriptToolCall[]
}

/** webhook action 的 payload */
export interface WebhookPayload {
  method: string
  url: string
  headers?: Record<string, string>
  body?: string
  timeout_seconds?: number
}

/** notify_session action 的 payload */
export interface NotifySessionPayload {
  session_id: string
  message: string
  event_type?: string
}

/** 创建 cron 请求体，对齐后端 CreateInput */
export interface CreateCronInput {
  name: string
  description?: string
  schedule_type: ScheduleType
  cron_expr?: string
  once_at?: string
  timezone?: string
  display_type?: string
  action_type: ActionType
  action_payload: Record<string, unknown>
  allow_concurrent?: boolean
  source?: string
}

/** 更新 cron 请求体，所有字段可选（后端用指针判断 nil） */
export interface UpdateCronInput {
  name?: string
  description?: string
  schedule_type?: ScheduleType
  cron_expr?: string
  once_at?: string
  timezone?: string
  display_type?: string
  action_type?: ActionType
  action_payload?: Record<string, unknown>
  allow_concurrent?: boolean
}

/** 列表过滤参数 */
export interface CronListFilter {
  status?: string
  action_type?: string
  source?: string
  q?: string
}

/** 执行历史过滤参数 */
export interface ExecListFilter {
  cron_id?: string
  status?: string
  limit?: number
  offset?: number
}

/** 执行历史清理过滤参数 */
export interface CleanExecFilter {
  cron_id?: string
  status?: string
}
