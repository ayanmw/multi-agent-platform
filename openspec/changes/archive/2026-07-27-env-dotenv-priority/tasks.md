## 1. Env layer implementation

- [x] 1.1 Create `internal/config/env.go` with cache, `Getenv`, `LookupEnv`, `LoadEnvFile`, `ReloadEnvCache`, `SetDotEnvFirst`, `SetOSFirst`, `ResetEnvCache`, and `ApplyEnvFileToOS`.
- [x] 1.2 Implement priority switch controlled by `.env` key `ENV_FILE_PRIORITY=os` and explicit API calls.
- [x] 1.3 Add unit tests `internal/config/env_test.go`.

## 2. Replace config reading points

- [x] 2.1 Update `internal/config/config.go` `Load()` to call `ReloadEnvCache()` and use package `Getenv`.
- [x] 2.2 Refactor legacy `loadEnvFile` to delegate to `ApplyEnvFileToOS`.
- [x] 2.3 Update `cmd/server/main.go` `LOG_LEVEL`, `LOG_FILE`, `REQUIRE_AUTH`, and `isTruthyEnv` to use `config.Getenv`.
- [x] 2.4 Update `internal/tool/web_search_gemini_test.go` real smoke test to prefer `config.Getenv` with `os.Getenv` fallback.

## 3. Verification

- [x] 3.1 Run `go test ./internal/config` and verify all env tests pass.
- [x] 3.2 Run `go test ./internal/tool ./cmd/server -count=1` and verify no regression.
- [x] 3.3 Run `go build ./...` and verify compilation.

## 4. OpenSpec & docs

- [x] 4.1 Create `proposal.md`, `design.md`, `specs/env-dotenv-priority/spec.md`, and `tasks.md`.
- [x] 4.2 Validate and archive the OpenSpec change.
- [x] 4.3 Commit all changes to Git.
