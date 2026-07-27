## 1. Set up dotenv subpackage

- [ ] 1.1 Create `internal/config/dotenv/dotenv.go` with cache, `LoadEnvFile`, `ReloadEnvCache`, `Getenv`, `LookupEnv`, priority switch, reset, and `ApplyEnvFileToOS`.
- [ ] 1.2 Add `github.com/joho/godotenv` to `go.mod` and run `go mod tidy`.
- [ ] 1.3 Add convenience APIs: `GetenvWithDefault`, `MustBool`, `MustInt`.

## 2. Refactor internal/config forwarding layer

- [ ] 2.1 Replace `internal/config/env.go` contents with thin wrappers/aliases to `internal/config/dotenv` while preserving existing exported names.
- [ ] 2.2 Add comment on top of `internal/config/env.go` recommending new code to import `internal/config/dotenv` directly.

## 3. Update tests and cross-package consumers

- [ ] 3.1 Create `internal/config/dotenv/dotenv_test.go` covering priority, source reporting, godotenv syntax (quotes/comments/empty lines/export), and helper APIs.
- [ ] 3.2 Update `internal/tool/web_search_gemini_test.go` to use `dotenv.Getenv("GEMINI_API_KEY")` instead of `os.Getenv`.
- [ ] 3.3 Keep `internal/config/env_test.go` to verify forwarded `config.Getenv` still works.

## 4. Verification

- [ ] 4.1 Run `go build ./...`.
- [ ] 4.2 Run `go test ./internal/config/... ./internal/tool/... ./cmd/server/... -count=1`.
- [ ] 4.3 Inspect that `internal/tool` no longer imports `internal/config` in tests.

## 5. Documentation and lifecycle

- [ ] 5.1 Update `roadmaps/ROADMAP.md` with dotenv package refactor entry.
- [ ] 5.2 Commit with message `Phase dotenv-package: 提取 .env 层到独立 dotenv 子包并引入 godotenv`.
