<script setup lang="ts">
import { ref, watch } from 'vue'
import type { WorkflowConfig, WorkflowAgentSpec } from '../types/agent'

const props = defineProps<{
  modelValue: WorkflowConfig
}>()

const emit = defineEmits<{
  'update:modelValue': [config: WorkflowConfig]
  save: [config: WorkflowConfig]
  cancel: []
}>()

const local = ref<WorkflowConfig>({
  strategy: props.modelValue.strategy,
  agents: props.modelValue.agents.map(a => ({ ...a })),
})

watch(() => props.modelValue, (val) => {
  local.value = {
    strategy: val.strategy,
    agents: val.agents.map(a => ({ ...a })),
  }
}, { deep: true })

function addAgent() {
  const idx = local.value.agents.length + 1
  local.value.agents.push({
    agentId: `agent_${idx}`,
    name: `Agent ${idx}`,
    systemPrompt: 'You are a helpful AI assistant with access to tools.',
    input: '',
    allowedTools: [],
    outputTo: [],
    model: '',
  })
  syncPipeline()
  emitUpdate()
}

function removeAgent(index: number) {
  local.value.agents.splice(index, 1)
  syncPipeline()
  emitUpdate()
}

function updateStrategy(strategy: WorkflowConfig['strategy']) {
  local.value.strategy = strategy
  syncPipeline()
  emitUpdate()
}

function syncPipeline() {
  if (local.value.strategy === 'pipeline') {
    local.value.agents.forEach((agent, idx) => {
      agent.outputTo = idx < local.value.agents.length - 1 ? [local.value.agents[idx + 1].agentId] : []
    })
  }
}

function emitUpdate() {
  emit('update:modelValue', {
    strategy: local.value.strategy,
    agents: local.value.agents.map(a => ({ ...a })),
  })
}

function handleSave() {
  emit('save', {
    strategy: local.value.strategy,
    agents: local.value.agents.map(a => ({ ...a })),
  })
}

function toggleTool(agent: WorkflowAgentSpec, tool: string) {
  const set = new Set(agent.allowedTools || [])
  if (set.has(tool)) {
    set.delete(tool)
  } else {
    set.add(tool)
  }
  agent.allowedTools = Array.from(set)
  emitUpdate()
}

const AVAILABLE_TOOLS = ['run_shell', 'write_file', 'read_file']
</script>

<template>
  <div class="workflow-editor-overlay" @click.self="emit('cancel')">
    <div class="workflow-editor">
      <div class="editor-header">
        <h3>Multi-Agent Workflow</h3>
        <button class="close-btn" @click="emit('cancel')">×</button>
      </div>

      <div class="editor-body">
        <div class="form-row">
          <label>Strategy</label>
          <select :value="local.strategy" @change="updateStrategy(($event.target as HTMLSelectElement).value as WorkflowConfig['strategy'])">
            <option value="parallel">Parallel</option>
            <option value="sequential">Sequential</option>
            <option value="pipeline">Pipeline</option>
          </select>
        </div>

        <div class="agents-list">
          <div v-for="(agent, idx) in local.agents" :key="idx" class="agent-card">
            <div class="agent-card-header">
              <span class="agent-index">#{{ idx + 1 }}</span>
              <button class="remove-btn" @click="removeAgent(idx)">Remove</button>
            </div>

            <div class="form-row">
              <label>Agent ID</label>
              <input v-model="agent.agentId" type="text" @change="emitUpdate" />
            </div>

            <div class="form-row">
              <label>Name</label>
              <input v-model="agent.name" type="text" @change="emitUpdate" />
            </div>

            <div class="form-row">
              <label>System Prompt</label>
              <textarea v-model="agent.systemPrompt" rows="3" @change="emitUpdate" />
            </div>

            <div class="form-row">
              <label>Input (optional)</label>
              <input v-model="agent.input" type="text" placeholder="Defaults to main input" @change="emitUpdate" />
            </div>

            <div class="form-row">
              <label>Model (optional)</label>
              <input v-model="agent.model" type="text" placeholder="Default model" @change="emitUpdate" />
            </div>

            <div class="form-row">
              <label>Allowed Tools</label>
              <div class="tools-row">
                <button
                  v-for="tool in AVAILABLE_TOOLS"
                  :key="tool"
                  class="tool-toggle"
                  :class="{ active: (agent.allowedTools || []).includes(tool) }"
                  @click="toggleTool(agent, tool)"
                >
                  {{ tool }}
                </button>
              </div>
            </div>

            <div v-if="local.strategy === 'parallel'" class="form-row">
              <label>Output To (agent IDs, comma separated)</label>
              <input
                :value="(agent.outputTo || []).join(', ')"
                type="text"
                @change="agent.outputTo = ($event.target as HTMLInputElement).value.split(',').map(s => s.trim()).filter(Boolean); emitUpdate()"
              />
            </div>
          </div>
        </div>

        <button class="add-agent-btn" @click="addAgent">+ Add Agent</button>
      </div>

      <div class="editor-footer">
        <button class="btn-secondary" @click="emit('cancel')">Cancel</button>
        <button class="btn-primary" @click="handleSave">Save Workflow</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.workflow-editor-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 200;
}

.workflow-editor {
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 8px;
  width: 600px;
  max-width: 90vw;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
}

.editor-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 14px 18px;
  border-bottom: 1px solid #333;
}

.editor-header h3 {
  margin: 0;
  font-size: 16px;
  color: #e0e0e0;
}

.close-btn {
  background: transparent;
  border: none;
  color: #888;
  font-size: 20px;
  cursor: pointer;
}

.editor-body {
  padding: 16px 18px;
  overflow-y: auto;
  flex: 1;
}

.editor-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 14px 18px;
  border-top: 1px solid #333;
}

.form-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 12px;
}

.form-row label {
  font-size: 12px;
  color: #aaa;
}

.form-row input,
.form-row textarea,
.form-row select {
  background: #252525;
  border: 1px solid #444;
  border-radius: 4px;
  color: #d4d4d4;
  padding: 6px 10px;
  font-family: inherit;
  font-size: 13px;
}

.form-row textarea {
  resize: vertical;
}

.agents-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-bottom: 12px;
}

.agent-card {
  background: #252525;
  border: 1px solid #333;
  border-radius: 6px;
  padding: 12px;
}

.agent-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 10px;
}

.agent-index {
  font-weight: 600;
  color: #4a9eff;
}

.remove-btn {
  background: transparent;
  border: 1px solid #ff6b6b;
  color: #ff6b6b;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 11px;
  cursor: pointer;
}

.tools-row {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.tool-toggle {
  background: #333;
  border: 1px solid #444;
  color: #999;
  border-radius: 4px;
  padding: 3px 10px;
  font-size: 11px;
  cursor: pointer;
}

.tool-toggle.active {
  background: #4a9eff;
  color: #fff;
  border-color: #4a9eff;
}

.add-agent-btn {
  width: 100%;
  background: #2a2a2a;
  border: 1px dashed #555;
  color: #aaa;
  border-radius: 6px;
  padding: 10px;
  cursor: pointer;
}

.add-agent-btn:hover {
  background: #333;
  color: #d4d4d4;
}

.btn-primary,
.btn-secondary {
  padding: 8px 16px;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
  border: none;
}

.btn-primary {
  background: #4a9eff;
  color: #fff;
}

.btn-secondary {
  background: #333;
  color: #aaa;
}
</style>
