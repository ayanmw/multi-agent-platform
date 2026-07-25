## 1. Shared auto-approval decision logic

- [x] 1.1 Create `web/v2/src/composables/useAutoApproval.ts` with `AUTO_APPROVAL_TAGS`, `defaultAutoApprovalTags()`, `useAutoApproval()` reactive store backed by LocalStorage, and exported `shouldAutoApprove(rule, tags, configuredTags)`.
- [x] 1.2 Add unit tests `useAutoApproval.spec.ts` covering all-tags-match, mixed-risk, empty set, non-TagPolicyRule, and one-tag cases.

## 2. Options flyout UI for auto-approval

- [x] 2.1 Add `autoApprovalTags` prop / v-model to `OptionsFlyout.vue`, render checkbox list with `network`, `mcp`, and destructive tags (`exec`, `exec:dangerous`, `shell`, `shell:dangerous`, `filesystem:destructive`, `filesystem:delete`, `filesystem:write`).
- [x] 2.2 Add "全选" / "清空" buttons and a top indicator that shows "自动审批已开启 / 已关闭" based on whether at least one tag is selected.
- [x] 2.3 Wire `CommandBar.vue` to use `useAutoApproval()` and pass tags into `OptionsFlyout`; load persisted tags on mount.

## 3. Front-end approval flow uses configured tags

- [x] 3.1 Refactor `useTaskStore.ts` to import `shouldAutoApprove` and `autoApprovalTags` from `useAutoApproval.ts`; replace hard-coded `LOW_RISK_TAGS`/`HIGH_RISK_TAGS` check.
- [x] 3.2 Update `useTaskStore.autoapprove.spec.ts` to use the shared `shouldAutoApprove` function and cover configured-tag behavior.

## 4. Backend auto-approval policy

- [x] 4.1 Define `internal/harness/auto_approval.go` with `AutoApprovalPolicy` struct and `ShouldAutoApprove(rule string, tags []string, allowed map[string]struct{}) bool`.
- [x] 4.2 Add `AutoApproveTags map[string]struct{}` field to `WebSocketApprovalHandler`; add `SetAutoApproveTags(tags []string)` method.
- [x] 4.3 Modify `WebSocketApprovalHandler.WaitForDecision` to perform a first 5-second wait; if no decision arrives and the request matches auto-approval policy, approve and return; otherwise continue waiting for the remaining timeout (default 25s) and then reject.
- [x] 4.4 Add tests in `internal/harness/approval_test.go` or `auto_approval_test.go` for 5s auto-approve, no-match falls through to timeout, empty set disables auto-approve, and channel still receives front-end decision.

## 5. Backend receives and updates auto-approval tags

- [x] 5.1 Add WebSocket control message handler for `action: set_auto_approval_tags` in `internal/ws/hub.go` or `cmd/server/server.go` to update the session/user-level `WebSocketApprovalHandler` policy.
- [x] 5.2 Emit the current tags to the server when `OptionsFlyout` opens/changes so the front-end LocalStorage and backend policy stay in sync.
- [x] 5.3 Ensure unknown/legacy clients without this message still behave as before (empty auto-approval set).

## 6. Integration and smoke testing

- [x] 6.1 Run `go test ./internal/harness/...` and ensure new approval tests pass.
- [x] 6.2 Run `cd web/v2 && npm run test:unit` and ensure `useAutoApproval.spec.ts` plus existing `useTaskStore.autoapprove.spec.ts` pass.
- [x] 6.3 Start the dev server and manually verify: open Options → toggle auto-approval tags → run a case that triggers `core/web_search` → confirm no dialog appears.
- [x] 6.4 Run `go build ./...` and check no compile errors.

## 7. Documentation and OpenSpec sync

- [x] 7.1 Update `CLAUDE.md` Change Log table with this change summary.
- [x] 7.2 Update `roadmaps/ROADMAP.md` if any Phase / task status changes.
- [x] 7.3 Mark tasks complete in `openspec/changes/add-configurable-auto-approval/tasks.md`.
