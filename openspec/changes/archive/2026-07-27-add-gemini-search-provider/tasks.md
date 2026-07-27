## 1. Configuration

- [x] 1.1 Add Gemini-related fields to `internal/config/config.go`.
- [x] 1.2 Load `GEMINI_API_KEY`, `GEMINI_ENDPOINT`, `GEMINI_SEARCH_MODEL`, `WEBSEARCH_ENABLE_GEMINI` environment variables in `Config.Load()`.
- [x] 1.3 Wire Gemini config fields in `cmd/server/main.go` `webSearchCfg` initialization.

## 2. Gemini Provider Implementation

- [x] 2.1 Add `gemini` to `WebSearchConfig` and to the doc comments listing supported providers.
- [x] 2.2 Implement `callGemini` and response parsing structs in `internal/tool/web_search_gemini.go`.
- [x] 2.3 Add `gemini` case in `webSearchExecutor` and update `selectWebSearchProvider` to prefer Gemini when configured.
- [x] 2.4 Ensure grounding metadata parsing falls back to plain text if chunks are absent.

## 3. Testing

- [x] 3.1 Add `TestWebSearchGemini` using `httptest` to verify request path, body, and parsed output.
- [x] 3.2 Add fallback test for missing-grounding-chunks path.
- [x] 3.3 Add a real-network smoke test `TestRealGeminiSearch` (default `Skip`) that uses `GEMINI_API_KEY`.
- [x] 3.4 Run `go test ./internal/tool/...` and `go build ./...` until all green.

## 4. Documentation & Archive

- [x] 4.1 Update `roadmaps/ROADMAP.md` with the new capability.
- [x] 4.2 Update ROADMAP version and verify `go build ./...` / `go test ./internal/tool/...`.
- [x] 4.3 Validate and archive change to `openspec/changes/archive/`.
