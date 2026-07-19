// Case management type definitions — mirrors the backend case model

/** A single acceptance criterion used to evaluate task completion */
export interface AcceptanceCriterion {
  type: string
  target: string
  expected?: string
  description: string
}

/** Contract/constraint block embedded in every case */
export interface TaskContract {
  goal: string
  scope: string
  allowed_tools?: string[]
  token_budget?: number
  max_steps: number
  permissions?: {
    allow_network?: boolean
    allow_file_delete?: boolean
    allow_file_write?: boolean
    allow_shell?: boolean
    allow_shell_dangerous?: boolean
  }
  acceptance_criteria?: AcceptanceCriterion[]
  tags?: string[]
  metadata?: Record<string, string>
  cost_budget_usd?: number
  timeout_seconds?: number
  auto_approve_policy?: boolean
}

/** Preset case definition returned by GET /api/cases */
export interface Case {
  id: string
  name: string
  description: string
  icon: string
  category: string
  system_prompt: string
  default_input: string
  contract: TaskContract
  tags: string[]
  is_builtin: boolean
  created_at: string
  updated_at: string
}

/** Request body for POST /api/cases */
export interface CreateCaseRequest {
  name: string
  description?: string
  icon?: string
  category: string
  system_prompt?: string
  default_input?: string
  contract?: Partial<TaskContract>
  tags?: string[]
}

/** Request body for PUT /api/cases/:id */
export interface UpdateCaseRequest {
  name?: string
  description?: string
  icon?: string
  category?: string
  system_prompt?: string
  default_input?: string
  contract?: Partial<TaskContract>
  tags?: string[]
}

/** Result emitted by the backend task_evaluated event */
export interface EvaluationResult {
  case_id: string
  passed: boolean
  score: number
  reason: string
}
