# Tasks: 文件系统 Skill 扫描器

- [ ] 1. 新增 `settings` 表与 CRUD
  - migration 创建表
  - `GetSetting` / `SetSetting`
  - 默认写入 `skill_scan_dirs` = 4 个目录 JSON

- [ ] 2. 新增 `internal/skill/file_loader.go`
  - `FileLoader` 结构
  - `LoadGlobal` / `LoadForWorkdir` / `RefreshAll`
  - frontmatter 解析
  - Markdown 正文 → `system_prompt` 模板

- [ ] 3. 修改 `internal/skill/loader.go`
  - `LoadAll` 调用全局文件扫描
  - 新增 `LoadForWorkdir`
  - 新增 `RefreshAll`
  - 重扫时正确卸载旧 `local_file` skill

- [ ] 4. REST API 扩展 `cmd/server/api_skill.go`
  - `GET /api/skills/scan-config`
  - `POST /api/skills/scan-config`
  - `POST /api/skills/scan`

- [ ] 5. 启动与 session hook
  - `main.go` 调用 `LoadAll`
  - `api.go` 创建/解析 session 后调用 `LoadForWorkdir`

- [ ] 6. 事件广播
  - load/unload/refresh 时发送 `skill_loaded` / `skill_unloaded` / `skill_changed`

- [ ] 7. 测试
  - `internal/skill/file_loader_test.go`
  - `cmd/server/api_skill_scan_test.go`

- [ ] 8. 验证
  - `go test ./internal/skill ./cmd/server`
  - `go build ./...`
  - mock regression 21/21 PASS
