/** LLM Provider / Model 类型定义 */

/** 模型选择模式：固定单模型 或 自动路由 */
export type ModelSelectionMode = 'single_model' | 'auto_route'

/** 模型层级，与后端 llm.ModelTier 对应 */
export type ModelTier = 'free' | 'efficient' | 'lightweight' | 'standard' | 'premium'

/** Provider 快照（来自 GET /api/providers） */
export interface LLMProvider {
  name: string
  type: string
  endpoint: string
  healthy: boolean
  last_sync_at?: string
  last_sync_error?: string
}

/** 模型画像（来自 GET /api/models/prices） */
export interface LLMModel {
  provider: string
  model_id: string
  display_name: string
  tier: ModelTier | string
  capabilities: string[]
  input_price: number
  output_price: number
  max_context_window: number
  max_output_tokens: number
  fallback_model: string
  rate_limit_rpm: number
  avg_latency_ms: number
  missing: boolean
  source: string
  updated_at_ms: number
}
