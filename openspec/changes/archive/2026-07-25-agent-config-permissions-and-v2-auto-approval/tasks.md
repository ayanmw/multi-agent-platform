## 1. Backend Agent config support

- [x] 1.1 Update `internal/agent/agent.go` to define `AgentConfig`/`AgentPermissions` helper structs (optional, for typed access)
- [x] 1.2 Update `cmd/server/api.go` `agentRequest` struct to include `config` field
- [x] 1.3 Update `cmd/server/api.go` `handleAgents` (POST) to persist `req.Config`
- [x] 1.4 Update `cmd/server/api.go` `handleAgentByID` (PUT) to persist `req.Config`
- [x] 1.5 Verify GET `/api/agents` and GET `/api/agents/{id}` already return full `config` from `AgentRecord`

## 2. Runtime permission merge

- [x] 2.1 Create helper `applyAgentPermissions(contract *harness.TaskContract, cfg map[string]any)` in `cmd/server/runner.go`
- [x] 2.2 Call the helper in `cmd/server/tasks_api.go` `startChatTask` after contract is finalized
- [x] 2.3 Call the helper in `cmd/server/api.go` `handleRunCase` after contract is finalized
- [x] 2.4 Add unit/integration test in `cmd/server` verifying agent with `config.permissions.allow_network=true` produces `AllowNetwork=true`

## 3. Approval delegation fallback

- [x] 3.1 Modify `internal/runtime/approval_delegation.go` `handleApprovalDelegation` to fall back to `e.handleApprovalRequired` when supervisor is missing or delegation times out
- [x] 3.2 Add unit test for missing supervisor fallback path
- [x] 3.3 Add unit test for delegation timeout fallback path

## 4. v2 frontend Agent permissions UI

- [x] 4.1 Update `web/v2/src/composables/useAgentStore.ts` `AgentRequest` to include `config`
- [x] 4.2 Update `web/v2/src/composables/useAgentStore.ts` `defaultAgentRequest()` to initialize `config.permissions`
- [x] 4.3 Update `web/v2/src/components/AgentConfig.vue` form model to load/save `config.permissions`
- [x] 4.4 Add permission checkboxes UI block to `AgentConfig.vue` with risk labels

## 5. v2 frontend auto-approval

- [x] 5.1 Update `web/v2/src/composables/useTaskStore.ts` to determine whether a pending approval should auto-approve based on `rule` and `tags`
- [x] 5.2 Update `web/v2/src/App.vue` to compute and pass `autoApprove` to `ApprovalDialog`
- [x] 5.3 Ensure auto-approval still emits the `approve` control message and clears the timer

## 6. Verification

- [x] 6.1 Run `go test ./...` and fix any failures
- [x] 6.2 Run `cd web/v2 && npm run build` and fix any type/build errors
- [ ] 6.3 Manual smoke test: create agent with AllowNetwork=true, run `web-research` case, confirm no manual approval dialog
- [ ] 6.4 Manual smoke test: run case without network permission but with frontend open, confirm ApprovalDialog appears for network request
- [x] 6.5 Update `roadmaps/ROADMAP.md` if needed

## 7. OpenSpec close-out

- [x] 7.1 Run `openspec verify-change agent-config-permissions-and-v2-auto-approval` (verified via `openspec validate`)
- [ ] 7.2 Run `openspec archive-change agent-config-permissions-and-v2-auto-approval`
