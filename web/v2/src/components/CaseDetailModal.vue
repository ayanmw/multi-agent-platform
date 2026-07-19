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
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
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
  color: var(--text-primary);
  margin: 0;
}

.modal-category {
  font-size: 11px;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.modal-close-btn {
  background: none;
  border: none;
  color: var(--text-secondary);
  font-size: 18px;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  transition: color 0.2s, background 0.2s;
}

.modal-close-btn:hover {
  color: var(--text-primary);
  background: var(--bg-elevated);
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
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
  margin-bottom: 6px;
}

.modal-section p {
  font-size: 13px;
  color: var(--text-primary);
  line-height: 1.6;
}

.modal-tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.modal-tag {
  font-size: 11px;
  color: var(--text-secondary);
  background: var(--bg-elevated);
  padding: 2px 10px;
  border-radius: 10px;
}

.modal-code {
  background: var(--bg-canvas);
  border: 1px solid var(--bg-elevated);
  border-radius: 6px;
  padding: 10px;
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--accent-warning);
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
  color: var(--text-secondary);
  min-width: 100px;
  flex-shrink: 0;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.contract-value {
  font-size: 13px;
  color: var(--text-primary);
}

.contract-tools {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}

.contract-tools code {
  font-size: 11px;
  background: var(--bg-elevated);
  padding: 1px 6px;
  border-radius: 4px;
  color: var(--accent-warning);
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
  background: rgba(57, 255, 20, 0.15);
  color: var(--accent-success);
}

.permission-pill.denied {
  background: rgba(255, 77, 77, 0.15);
  color: var(--accent-danger);
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
  background: var(--bg-canvas);
  border-radius: 6px;
  border: 1px solid var(--bg-elevated);
  flex-wrap: wrap;
}

.ac-type {
  font-size: 10px;
  font-weight: 600;
  color: var(--accent-running);
  background: rgba(0, 229, 255, 0.10);
  padding: 1px 6px;
  border-radius: 4px;
  white-space: nowrap;
  font-family: var(--font-mono);
}

.ac-target {
  font-size: 11px;
  color: var(--accent-warning);
  font-family: var(--font-mono);
  white-space: nowrap;
}

.ac-desc {
  font-size: 12px;
  color: var(--text-primary);
  line-height: 1.4;
  flex: 1;
}

.ac-expected {
  font-size: 10px;
  color: var(--text-secondary);
  font-style: italic;
}

/* Footer */
.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 16px 20px;
  border-top: 1px solid var(--bg-elevated);
}

.modal-cancel-btn {
  padding: 8px 20px;
  background: var(--bg-elevated);
  color: var(--text-primary);
  border: 1px solid var(--border-default);
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s;
}

.modal-cancel-btn:hover {
  background: var(--border-default);
}

.modal-edit-btn {
  padding: 8px 20px;
  background: var(--bg-panel);
  color: var(--text-primary);
  border: 1px solid var(--border-default);
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s, color 0.2s;
}

.modal-edit-btn:hover {
  background: var(--bg-hover);
  color: var(--accent-running);
  border-color: var(--accent-running);
}

.modal-run-btn {
  padding: 8px 24px;
  background: var(--accent-running);
  color: var(--text-on-accent);
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s, filter 0.2s;
}

.modal-run-btn:hover {
  background: var(--accent-running);
  filter: brightness(1.1);
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