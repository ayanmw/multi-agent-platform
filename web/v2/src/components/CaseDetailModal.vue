<!-- CaseDetailModal — shows detailed information about a preset case
     Props:
       caseData: the Case object to display
       visible: whether the modal is shown

     Emits:
       close: user clicked close or backdrop
       run: user clicked the Run button inside the modal
       edit: user clicked the Edit button (custom cases only)

     Shows:
       - Full description, category, tags
       - Default input text (if available)
       - System prompt preview
       - Contract details: goal, scope, permissions, timeout, cost budget, allowed tools, token budget, max steps
       - Acceptance criteria with type, target, and description
-->
<script setup lang="ts">
import type { Case } from '../types/case'

defineProps<{
  caseData: Case | null
  visible: boolean
}>()

defineEmits<{
  close: []
  run: [caseId: string]
  edit: [caseId: string]
}>()
</script>

<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible && caseData" class="modal-overlay" @click.self="$emit('close')">
        <div class="modal-content">
          <!-- Header -->
          <div class="modal-header">
            <div class="modal-header-left">
              <span class="modal-icon">{{ caseData.icon }}</span>
              <div>
                <h2 class="modal-title">{{ caseData.name }}</h2>
                <span class="modal-category">{{ caseData.category }}</span>
              </div>
            </div>
            <button class="modal-close-btn" @click="$emit('close')" title="Close">✕</button>
          </div>

          <div class="modal-body">
            <!-- Description -->
            <section class="modal-section">
              <h3>Description</h3>
              <p>{{ caseData.description }}</p>
            </section>

            <!-- Tags -->
            <section class="modal-section">
              <h3>Tags</h3>
              <div class="modal-tags">
                <span v-for="tag in caseData.tags" :key="tag" class="modal-tag">{{ tag }}</span>
              </div>
            </section>

            <!-- Default Input -->
            <section v-if="caseData.default_input" class="modal-section">
              <h3>Default Input</h3>
              <pre class="modal-code">{{ caseData.default_input }}</pre>
            </section>

            <!-- System Prompt -->
            <section v-if="caseData.system_prompt" class="modal-section">
              <h3>System Prompt</h3>
              <pre class="modal-code modal-code-scroll">{{ caseData.system_prompt }}</pre>
            </section>

            <!-- Contract -->
            <section v-if="caseData.contract" class="modal-section">
              <h3>Task Contract</h3>
              <div class="contract-grid">
                <div v-if="caseData.contract.goal" class="contract-item">
                  <span class="contract-label">Goal</span>
                  <span class="contract-value">{{ caseData.contract.goal }}</span>
                </div>
                <div v-if="caseData.contract.scope" class="contract-item">
                  <span class="contract-label">Scope</span>
                  <span class="contract-value"><code>{{ caseData.contract.scope }}</code></span>
                </div>
                <div v-if="caseData.contract.allowed_tools?.length" class="contract-item">
                  <span class="contract-label">Allowed Tools</span>
                  <div class="contract-tools">
                    <code v-for="t in caseData.contract.allowed_tools" :key="t">{{ t }}</code>
                  </div>
                </div>
                <div v-if="caseData.contract.token_budget" class="contract-item">
                  <span class="contract-label">Token Budget</span>
                  <span class="contract-value">{{ caseData.contract.token_budget.toLocaleString() }}</span>
                </div>
                <div v-if="caseData.contract.max_steps" class="contract-item">
                  <span class="contract-label">Max Steps</span>
                  <span class="contract-value">{{ caseData.contract.max_steps }}</span>
                </div>
                <div v-if="caseData.contract.timeout_seconds" class="contract-item">
                  <span class="contract-label">Timeout</span>
                  <span class="contract-value">{{ caseData.contract.timeout_seconds }}s</span>
                </div>
                <div v-if="caseData.contract.cost_budget_usd" class="contract-item">
                  <span class="contract-label">Cost Budget</span>
                  <span class="contract-value">${{ caseData.contract.cost_budget_usd.toFixed(4) }}</span>
                </div>
                <div v-if="caseData.contract.permissions" class="contract-item permissions">
                  <span class="contract-label">Permissions</span>
                  <div class="contract-permissions">
                    <span
                      v-for="(allowed, key) in caseData.contract.permissions"
                      :key="key"
                      :class="['permission-pill', allowed ? 'allowed' : 'denied']"
                    >
                      {{ allowed ? '✓' : '✕' }} {{ key.replace(/^allow_/, '').replace(/_/g, ' ') }}
                    </span>
                  </div>
                </div>
              </div>
            </section>

            <!-- Acceptance Criteria -->
            <section v-if="caseData.contract?.acceptance_criteria?.length" class="modal-section">
              <h3>Acceptance Criteria</h3>
              <div class="ac-list">
                <div
                  v-for="(ac, i) in caseData.contract.acceptance_criteria"
                  :key="i"
                  class="ac-item"
                >
                  <span class="ac-type">{{ ac.type }}</span>
                  <span v-if="ac.target" class="ac-target">{{ ac.target }}</span>
                  <span class="ac-desc">{{ ac.description }}</span>
                  <span v-if="ac.expected" class="ac-expected">expected: {{ ac.expected }}</span>
                </div>
              </div>
            </section>
          </div>

          <!-- Footer -->
          <div class="modal-footer">
            <button class="modal-cancel-btn" @click="$emit('close')">Cancel</button>
            <button
              v-if="!caseData.is_builtin"
              class="modal-edit-btn"
              @click="$emit('edit', caseData.id)"
            >
              ✎ Edit
            </button>
            <button class="modal-run-btn" @click="$emit('run', caseData.id)">▶ Run</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10000;
  padding: 20px;
}

.modal-content {
  background: #252525;
  border: 1px solid #444;
  border-radius: 12px;
  max-width: 640px;
  width: 100%;
  max-height: 85vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  padding: 20px 20px 0;
}

.modal-header-left {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.modal-icon {
  font-size: 32px;
  line-height: 1;
}

.modal-title {
  font-size: 18px;
  font-weight: 700;
  color: #e0e0e0;
  margin: 0;
}

.modal-category {
  font-size: 11px;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.modal-close-btn {
  background: none;
  border: none;
  color: #888;
  font-size: 18px;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  transition: color 0.2s, background 0.2s;
}

.modal-close-btn:hover {
  color: #fff;
  background: #333;
}

.modal-body {
  padding: 16px 20px;
  overflow-y: auto;
  flex: 1;
}

.modal-section {
  margin-bottom: 16px;
}

.modal-section h3 {
  font-size: 12px;
  font-weight: 600;
  color: #999;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 6px;
}

.modal-section p {
  font-size: 13px;
  color: #ccc;
  line-height: 1.6;
}

.modal-tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.modal-tag {
  font-size: 11px;
  color: #aaa;
  background: #333;
  padding: 2px 10px;
  border-radius: 10px;
}

.modal-code {
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 6px;
  padding: 10px;
  font-family: 'Consolas', 'Monaco', monospace;
  font-size: 12px;
  color: #ce9178;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

.modal-code-scroll {
  max-height: 150px;
  overflow-y: auto;
}

/* Contract grid */
.contract-grid {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.contract-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.contract-item.permissions {
  flex-direction: column;
  gap: 6px;
}

.contract-label {
  font-size: 11px;
  color: #888;
  min-width: 100px;
  flex-shrink: 0;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.contract-value {
  font-size: 13px;
  color: #d4d4d4;
}

.contract-tools {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}

.contract-tools code {
  font-size: 11px;
  background: #333;
  padding: 1px 6px;
  border-radius: 4px;
  color: #ce9178;
}

.contract-permissions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.permission-pill {
  font-size: 10px;
  padding: 2px 6px;
  border-radius: 4px;
  text-transform: capitalize;
}

.permission-pill.allowed {
  background: rgba(81, 207, 102, 0.15);
  color: #51cf66;
}

.permission-pill.denied {
  background: rgba(231, 76, 60, 0.15);
  color: #e74c3c;
}

/* Acceptance criteria */
.ac-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ac-item {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 6px 10px;
  background: #1e1e1e;
  border-radius: 6px;
  border: 1px solid #333;
  flex-wrap: wrap;
}

.ac-type {
  font-size: 10px;
  font-weight: 600;
  color: #4a9eff;
  background: #1a2a3a;
  padding: 1px 6px;
  border-radius: 4px;
  white-space: nowrap;
  font-family: 'Consolas', monospace;
}

.ac-target {
  font-size: 11px;
  color: #ce9178;
  font-family: 'Consolas', monospace;
  white-space: nowrap;
}

.ac-desc {
  font-size: 12px;
  color: #ccc;
  line-height: 1.4;
  flex: 1;
}

.ac-expected {
  font-size: 10px;
  color: #888;
  font-style: italic;
}

/* Footer */
.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 16px 20px;
  border-top: 1px solid #333;
}

.modal-cancel-btn {
  padding: 8px 20px;
  background: #333;
  color: #ccc;
  border: 1px solid #444;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s;
}

.modal-cancel-btn:hover {
  background: #444;
}

.modal-edit-btn {
  padding: 8px 20px;
  background: #2a2a2a;
  color: #ccc;
  border: 1px solid #555;
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s, color 0.2s;
}

.modal-edit-btn:hover {
  background: #3a3a3a;
  color: #4a9eff;
  border-color: #4a9eff;
}

.modal-run-btn {
  padding: 8px 24px;
  background: #4a9eff;
  color: #fff;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
}

.modal-run-btn:hover {
  background: #3a8eef;
}

/* Transition */
.modal-enter-active,
.modal-leave-active {
  transition: all 0.25s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .modal-content,
.modal-leave-to .modal-content {
  transform: scale(0.95);
}
</style>