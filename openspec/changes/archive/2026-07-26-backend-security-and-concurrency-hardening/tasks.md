# backend-security-and-concurrency-hardening Tasks

## 1. Setup & Common Utilities

- [x] 1.1 Create shared URL validation helper in `internal/cron` for scheme/IP checks (loopback, link-local, private) with env override support
- [x] 1.2 Create shared shell command parser in `internal/tool` for splitting a command string into program + args while preserving `{param}` placeholders
- [x] 1.3 Add `CRON_WEBHOOK_ALLOW_PRIVATE` to `internal/config` config struct

## 2. Batch 1: Engine Panic & Concurrency Hardening

- [x] 2.1 Modify `Engine.Run` in `internal/runtime/engine.go` to capture panic and return an error instead of re-raising it
- [x] 2.2 Include `debug.Stack()` in the panic error string for diagnostics
- [x] 2.3 Add `sync.Mutex` to protect all writes to `Engine.messages`
- [x] 2.4 Ensure AgentBus listener goroutine exits before `Engine.Run` returns
- [x] 2.5 Add unit tests for panic capture in `internal/runtime/engine_test.go` (or new test file)
- [x] 2.6 Add race-free unit tests for AgentBus concurrent message delivery

## 3. Batch 2: Tool Security Hardening

- [x] 3.1 Update `run_shell` in `internal/tool/builtin.go` to validate `ExecuteContext.Workdir` against session workspace / active worktree scope before setting `cmd.Dir`
- [x] 3.2 Return a clear error observation when scope validation fails
- [x] 3.3 Add unit tests for `run_shell` workdir scope allow/reject
- [x] 3.4 Modify `DynamicExecutor.executeShell` in `internal/tool/executor.go` to use parsed program + args instead of `sh -c`
- [x] 3.5 Implement placeholder replacement for command arguments without shell interpretation
- [x] 3.6 Reject command strings containing shell metacharacters (`|`, `&&`, `;`, `$(`, backticks) unless a future `shell: true` flag is present
- [x] 3.7 Add/update unit tests for dynamic tool shell injection prevention
- [x] 3.8 Update cron `runWebhook` in `internal/cron/action.go` to validate scheme and target address
- [x] 3.9 Wire `cfg.CronWebhookAllowPrivate` / env override into `ActionRunner`
- [x] 3.10 Add unit tests for cron webhook SSRF guard

## 4. Batch 3: Resource & State Consistency

- [x] 4.1 Update `RemoveReport` in `internal/workspace/manager.go` to include `BranchRemoved bool` and `Warnings []string`
- [x] 4.2 Capture `git branch -D` failures in `Manager.Remove` and `Manager.RemoveOrphan` non-silently
- [x] 4.3 Update `executeWorktreeExit` to surface branch-removal warnings to the observation
- [x] 4.4 Add collision retry for `Manager.Create` when target worktree path already exists
- [x] 4.5 Add/update unit tests for worktree branch removal and path collision behavior
- [x] 4.6 Make `Executor.doExecute` log errors from `UpdateExecution` and `UpdateCronScheduleMeta`
- [x] 4.7 Optionally include `persisted: false` in cron execution events on persistence failure
- [x] 4.8 Add context timeout to cron `ActionRunner.runScript` tool executions
- [x] 4.9 Add/update unit tests for cron persistence error handling and script timeout

## 5. Batch 4: Hub & Global State Governance

- [x] 5.1 Add `done chan struct{}`, `Shutdown(ctx)` method, and graceful stop path to `internal/ws/hub.go`
- [x] 5.2 Update main.go to call `hub.Shutdown(ctx)` on server shutdown
- [x] 5.3 Add unit tests for Hub graceful shutdown
- [x] 5.4 Introduce `appServer`-scoped registries (cancel/engine/trace) as fields, keeping package globals as compatibility proxies
- [x] 5.5 Refactor new code paths to use `s.*` registry fields instead of package globals
- [x] 5.6 Update tests to use injected registries where feasible

## 6. Verification & Documentation

- [x] 6.1 Run `go vet ./...`
- [x] 6.2 Run `go test ./...` on Windows
- [x] 6.3 Run mock case regression script `scripts/cases-regression.sh` and confirm 21/21 PASS
- [x] 6.4 Update ROADMAP entries for completed batches
- [x] 6.5 Run `openspec-verify-change` after final batch
- [x] 6.6 Commit each batch with message format `Phase security-X: <description>`
