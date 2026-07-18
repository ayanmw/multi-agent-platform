import { ref } from 'vue'
import type { AgentEvent } from '../types/events'

export interface SpanNode {
  trace_id: string
  span_id: string
  parent_span_id?: string
  operation: string
  agent_id: string
  duration_ms: number
  status: string
  attributes?: Record<string, any>
  children: SpanNode[]
}

const spans = ref<SpanNode[]>([])

function buildTree(flat: SpanNode[]): SpanNode[] {
  const map = new Map<string, SpanNode>()
  const roots: SpanNode[] = []
  flat.forEach(node => {
    node.children = []
    map.set(node.span_id, node)
  })
  flat.forEach(node => {
    if (node.parent_span_id && map.has(node.parent_span_id)) {
      map.get(node.parent_span_id)!.children.push(node)
    } else if (!node.parent_span_id) {
      roots.push(node)
    }
  })
  return roots
}

export function useTraceStore() {
  const onEvent = (evt: AgentEvent) => {
    if (evt.type !== 'trace_span') return
    const node = evt.data as unknown as SpanNode
    spans.value.push(node)
    spans.value = buildTree(spans.value.slice(-1000))
  }
  return { spans, onEvent }
}
