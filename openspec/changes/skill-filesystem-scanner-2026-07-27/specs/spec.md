# Spec: 文件系统 Skill 扫描器

## 新增/修改文件

### 数据模型与 schema

1. `internal/skill/skill.go`（已随 Spec 1 修改）
   - 确认 `SourceURL` 存储 skill 文件绝对路径。

2. `pkg/db/database.go` 或 `pkg/db/settings.go`（新增）
   - 新增 migration 创建 `settings` 表：
     ```sql
     CREATE TABLE IF NOT EXISTS settings (
         key TEXT PRIMARY KEY,
         value TEXT NOT NULL,
         updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
     );
     ```
   - 新增 `GetSetting(key)`、`SetSetting(key, value)`。

### 文件扫描实现

3. 新增 `internal/skill/file_loader.go`
   - 类型：
     ```go
     type FileLoader struct {
         registry *Registry
         store    *Store
         settings SettingStore
         bus      EventBus // 可为 nil
     }
     
     type FileSkillInfo struct { ... }
     
     var DefaultSkillScanDirs = []string{
         ".claude/skills",
         ".agents/skills",
         ".agent/skills",
         ".opencode/skills",
     }
     ```
   - 方法：
     - `NewFileLoader(registry, store, settings, bus) *FileLoader`
     - `(fl *FileLoader) LoadGlobal(baseDir string) error`
     - `(fl *FileLoader) LoadForWorkdir(workdir, projectID string) error`
     - `(fl *FileLoader) RefreshAll(workdirs []string) error`
     - `parseSkillFile(path string, scope, workdir, projectID string) (*Skill, error)`
     - `getEnabledScanDirs() ([]string, error)`
   - `parseSkillFile` 用 `yaml.v3`（如项目未引入则复用已有 yaml 库）解析 frontmatter；正文用 regex `(?s)^---\n(.*?)\n---\n(.*)$` 分离。
   - 生成 `Skill{
     Source: SkillSourceLocalFile,
     SourceURL: path,
     IsLocalEditable: false,
     State: SkillStateEnabled, // 文件系统 skill 默认启用；用户可 disabled 改为本地 shadow？MVP：文件系统 skill 只能在前端"禁用"，实际会落 local_db shadow 或者仅记录 disabled list。本次简化：文件系统 skill 不可禁用；若需禁用，用户应删除文件或创建 local_db shadow。
   }`。

### Loader 集成

4. 修改 `internal/skill/loader.go`
   - `Loader.LoadAll` 流程：
     1. built_in
     2. DB local_db
     3. `fileLoader.LoadGlobal(globalBaseDir)`
   - 新增 `Loader.LoadForWorkdir(workdir, projectID)`：
     1. 先卸载 `source=local_file` 且 `workspace_dir=workdir` 的旧 skill（避免重扫时残留）。
     2. 调用 `fileLoader.LoadForWorkdir(workdir, projectID)`。
   - 新增 `Loader.RefreshAll(workdirs)`：
     1. 卸载所有 `local_file`。
     2. 重扫 global + 所有 workdirs。

### REST API

5. 修改 `cmd/server/api_skill.go`
   - 新增 `GET /api/skills/scan-config`
     - 返回 `{enabled_dirs: [...]}`。
   - 新增 `POST /api/skills/scan-config`
     - body `{enabled_dirs: [...]}`，校验必须是 DefaultSkillScanDirs 子集；存入 settings。
     - 保存后重新加载 global（不改变已扫描 workdir）。
   - 新增 `POST /api/skills/scan`
     - 收集所有已知 session 的 `workspace_dir`（排除空/auto）。
     - 调用 `skillLoader.RefreshAll(workdirs)`。
     - 返回 `{scanned_workdirs: int, loaded: int, unloaded: int}`。
   - `GET /api/skills` 对 `source=local_file` 正常返回。

### 启动与 Session Hook

6. 修改 `cmd/server/main.go`
   - 在 skill loader 构造后调用 `Loader.LoadAll()`；内部会先执行全局文件扫描。
   - 将 `skillLoader` 保存为 `globalSkillLoader`，供 runner/refresh API 使用。

7. 修改 `cmd/server/api.go`
   - 在创建 session 或 `resolveWorkspaceDir` 后，若最终 workdir 非空且非 auto，调用 `globalSkillLoader.LoadForWorkdir(workdir, projectID)`。
   - 在 `GET /api/sessions/:id` 某天也按需加载（或确保创建时已加载）。

### 事件

8. `internal/skill/events.go` 已经定义相关事件常量，本 spec 确保 Loader 在 load/unload 时调用 `bus.SendEvent`。

## 测试策略

- 新增 `internal/skill/file_loader_test.go`：
  - 在临时目录创建 `.claude/skills/test/SKILL.md`，断言 `LoadGlobal` 后 registry 中存在。
  - 测试 frontmatter 覆盖 scope、name、tags。
  - 测试关闭某个扫描目录后该目录 skill 不加载。
  - 测试 `RefreshAll` 后删除的 skill 被卸载。
- 新增 `cmd/server/api_skill_scan_test.go`：
  - 创建临时 workdir + skill 文件，调用 `POST /api/skills/scan`，验证 API 返回与 registry 状态。
