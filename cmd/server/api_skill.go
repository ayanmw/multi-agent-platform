package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
)

// registerSkillRoutes 把 Skill 管理 REST API 路由挂载到 mux。
//
// 路由总览：
//   GET    /api/skills              — 列出 registry 中所有 skill（?source=xxx 可选过滤）
//   GET    /api/skills/search?q=    — 按 id/display_name/description/tags 关键词搜索
//   POST   /api/skills              — 创建 local_db skill
//   GET    /api/skills/:id          — 返回单个 skill 详情
//   PUT    /api/skills/:id          — 更新 local editable skill
//   DELETE /api/skills/:id          — 删除 local editable skill
//   POST   /api/skills/:id/enable   — 启用 skill（同步 registry 与 store 状态）
//   POST   /api/skills/:id/disable  — 禁用 skill
//   GET    /api/skills/scan-config  — 返回当前启用的扫描目录
//   POST   /api/skills/scan-config  — 更新启用的扫描目录
//   POST   /api/skills/scan         — 强制刷新所有文件系统 skill
//
// 所有 handler 直接操作传入的 skillStore / skillRegistry，避免与全局变量耦合，
// 方便在测试中传入隔离实例。
func registerSkillRoutes(mux *http.ServeMux, hub eventBroadcaster, skillStore *skill.Store, skillRegistry *skill.Registry, skillLoader *skill.Loader, settingStore skill.SettingStore) {
	mux.HandleFunc("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListSkills(w, r, skillRegistry)
		case http.MethodPost:
			handleCreateSkill(w, r, hub, skillStore, skillRegistry)
		default:
			writeJSONError(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/skills/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		handleSearchSkills(w, r, skillRegistry)
	})

	mux.HandleFunc("/api/skills/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/skills/")
		if path == "" {
			writeJSONError(w, "skill ID required", http.StatusBadRequest)
			return
		}

		// 精确子路由：scan / scan-config 有独立 handler，避免与 skill-id 含 "/" 冲突。
		if path == "scan" && r.Method == http.MethodPost {
			handleScanSkills(w, r, skillLoader)
			return
		}
		if path == "scan-config" {
			switch r.Method {
			case http.MethodGet:
				handleGetScanConfig(w, r, settingStore)
			case http.MethodPost:
				handleSetScanConfig(w, r, settingStore, skillLoader)
			default:
				writeJSONError(w, "GET or POST only", http.StatusMethodNotAllowed)
			}
			return
		}

		// POST /api/skills/:id/enable | /disable
		// 注意：skill id 允许包含 "/"（如 "user/test-skill"），因此只能用
		// 后缀匹配识别子资源，而不能用 strings.Contains(path, "/") 判断。
		if r.Method == http.MethodPost {
			if suffix, ok := strings.CutSuffix(path, "/enable"); ok {
				handleEnableSkill(w, r, hub, skillStore, skillRegistry, suffix)
				return
			}
			if suffix, ok := strings.CutSuffix(path, "/disable"); ok {
				handleDisableSkill(w, r, hub, skillStore, skillRegistry, suffix)
				return
			}
		}

		// 其它子路径（例如 /api/skills/foo/bar/baz）按非法资源处理。
		// 但合法 skill id 本身可含 "/"，故只对未知后缀 + 非 GET/PUT/DELETE 的
		// 请求返回 404；常规 CRUD 仍把整段 path 当作 id 处理。
		switch r.Method {
		case http.MethodGet:
			handleGetSkill(w, r, skillRegistry, path)
		case http.MethodPut:
			handleUpdateSkill(w, r, hub, skillStore, skillRegistry, path)
		case http.MethodDelete:
			handleDeleteSkill(w, r, hub, skillStore, skillRegistry, path)
		default:
			writeJSONError(w, "GET, PUT, or DELETE only", http.StatusMethodNotAllowed)
		}
	})
}

// eventBroadcaster 是 hub.SendEvent 的最小接口约束，避免直接依赖 *ws.Hub。
// 用接口形式也方便单测中传入伪实现。
type eventBroadcaster interface {
	SendEvent(evt event.Event)
}

// handleListSkills 处理 GET /api/skills，返回 registry 中的全部 skill。
// 可通过 ?source=built_in|local_db|local_file|market|mcp 过滤。
// 新增可选 ?scope=global|project|session&project_id=...&workdir=... 过滤。
// 未传过滤参数时返回所有 skill，保持向后兼容。
func handleListSkills(w http.ResponseWriter, r *http.Request, registry *skill.Registry) {
	var source *skill.SkillSource
	if s := strings.TrimSpace(r.URL.Query().Get("source")); s != "" {
		src := skill.SkillSource(s)
		source = &src
	}
	scopeFilter := strings.TrimSpace(r.URL.Query().Get("scope"))
	projectIDFilter := strings.TrimSpace(r.URL.Query().Get("project_id"))
	workdirFilter := strings.TrimSpace(r.URL.Query().Get("workdir"))

	skills := registry.List(source)
	if skills == nil {
		skills = []skill.Skill{}
	}

	if scopeFilter != "" || projectIDFilter != "" || workdirFilter != "" {
		filtered := make([]skill.Skill, 0, len(skills))
		for _, s := range skills {
			if scopeFilter != "" && string(s.Scope) != scopeFilter {
				continue
			}
			if projectIDFilter != "" && s.ProjectID != projectIDFilter {
				continue
			}
			if workdirFilter != "" && !skill.MatchWorkdir(s.WorkspaceDir, workdirFilter) {
				continue
			}
			filtered = append(filtered, s)
		}
		skills = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(skills)
}

// handleSearchSkills 处理 GET /api/skills/search?q=xxx。
// 命中规则：id、display_name、description、tags 任一字段包含关键词（大小写不敏感）。
func handleSearchSkills(w http.ResponseWriter, r *http.Request, registry *skill.Registry) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	all := registry.List(nil)
	if q == "" {
		if all == nil {
			all = []skill.Skill{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(all)
		return
	}

	var result []skill.Skill
	for _, s := range all {
		if matchSkillKeyword(s, q) {
			result = append(result, s)
		}
	}
	if result == nil {
		result = []skill.Skill{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// matchSkillKeyword 判断 skill 是否包含小写关键词 q。
func matchSkillKeyword(s skill.Skill, q string) bool {
	if strings.Contains(strings.ToLower(s.ID), q) {
		return true
	}
	if strings.Contains(strings.ToLower(s.DisplayName), q) {
		return true
	}
	if strings.Contains(strings.ToLower(s.Description), q) {
		return true
	}
	for _, tag := range s.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// handleGetSkill 处理 GET /api/skills/:id，返回单个 skill 详情。
// 不存在返回 404。
func handleGetSkill(w http.ResponseWriter, r *http.Request, registry *skill.Registry, id string) {
	s, ok := registry.Get(id)
	if !ok {
		writeJSONError(w, "skill not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

// skillCreateRequest 是 POST /api/skills 的请求体。
// 字段命名与 skill.Skill 的 JSON 标签保持一致（snake_case）。
type skillCreateRequest struct {
	ID           string                   `json:"id"`
	DisplayName  string                   `json:"display_name"`
	Description  string                   `json:"description"`
	Content      string                   `json:"content"`
	Parameters   []skill.SkillParameter   `json:"parameters"`
	Variables    map[string]any           `json:"variables"`
	Tags         []string                 `json:"tags"`
	Authors      []string                 `json:"authors"`
	Scope        string                   `json:"scope"`
	ProjectID    string                   `json:"project_id"`
	WorkspaceDir string                   `json:"workspace_dir"`
}

// handleCreateSkill 处理 POST /api/skills，创建一条 local_db skill。
// 创建成功后同时写入 store（持久化）与 registry（内存）。
func handleCreateSkill(w http.ResponseWriter, r *http.Request, hub eventBroadcaster, store *skill.Store, registry *skill.Registry) {
	var req skillCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		writeJSONError(w, "id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.DisplayName) == "" {
		writeJSONError(w, "display_name is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		writeJSONError(w, "content is required", http.StatusBadRequest)
		return
	}
	if registry.Exists(id) {
		writeJSONError(w, "skill already exists: "+id, http.StatusBadRequest)
		return
	}

	scope := skill.SkillScope(strings.TrimSpace(req.Scope))
	if scope == "" {
		scope = skill.SkillScopeGlobal
	}
	// 允许 global/project/session 三种 scope（review H6）：
	// session scope 可写入存储，但 ResolveActiveSkills 对 session scope 暂不注入
	// （仅经 ResolveActiveSkillsWithExtra 的 extraIDs 显式纳入当前 run）。
	// 与 spec.md:94 "local_db 可设置 scope=project 或 session" 对齐。
	if scope != skill.SkillScopeGlobal && scope != skill.SkillScopeProject && scope != skill.SkillScopeSession {
		writeJSONError(w, "invalid scope: "+string(scope)+" (allowed: global, project, session)", http.StatusBadRequest)
		return
	}

	renderer := skill.NewRenderer()
	variables := renderer.ExtractVariables(req.Content)

	now := time.Now().Unix()
	s := skill.Skill{
		ID:              id,
		Version:         "1.0.0",
		DisplayName:     strings.TrimSpace(req.DisplayName),
		Description:     req.Description,
		Authors:         req.Authors,
		Tags:            req.Tags,
		Source:          skill.SkillSourceLocalDB,
		IsLocalEditable: true,
		Scope:           scope,
		ProjectID:       strings.TrimSpace(req.ProjectID),
		WorkspaceDir:    strings.TrimSpace(req.WorkspaceDir),
		Templates: []skill.SkillTemplate{
			{
				Name:       "system_prompt",
				Content:    req.Content,
				Variables:  variables,
				IsRequired: true,
			},
		},
		Parameters: req.Parameters,
		State:      skill.SkillStateEnabled,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if store != nil {
		if err := store.Save(&s); err != nil {
			writeJSONError(w, "save skill: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	registry.Register(s)

	broadcastSkillEvent(hub, skill.EventSkillLoaded, s.ID, map[string]any{
		"id":     s.ID,
		"source": string(s.Source),
		"state":  string(s.State),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(s)
}

// skillUpdateRequest 是 PUT /api/skills/:id 的请求体。
// 所有字段可选；仅 display_name/description/content/parameters/scope/project_id/workspace_dir 会被更新。
type skillUpdateRequest struct {
	DisplayName  *string                 `json:"display_name"`
	Description  *string                 `json:"description"`
	Content      *string                 `json:"content"`
	Parameters   []skill.SkillParameter  `json:"parameters"`
	Scope        *string                 `json:"scope"`
	ProjectID    *string                 `json:"project_id"`
	WorkspaceDir *string                 `json:"workspace_dir"`
}

// handleUpdateSkill 处理 PUT /api/skills/:id，仅允许修改 local editable skill。
// 内置 skill 或非 editable 返回 403；不存在返回 404。
func handleUpdateSkill(w http.ResponseWriter, r *http.Request, hub eventBroadcaster, store *skill.Store, registry *skill.Registry, id string) {
	existing, ok := registry.Get(id)
	if !ok {
		writeJSONError(w, "skill not found", http.StatusNotFound)
		return
	}

	var req skillUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 内置 skill 允许 fork 为 local_db shadow；local_file 禁止修改；其它不可编辑返回 403。
	switch existing.Source {
	case skill.SkillSourceBuiltIn:
		// 自动 fork 为 local_db shadow：保持同 ID 覆盖，后续 system prompt 生效。
		existing.Source = skill.SkillSourceLocalDB
		existing.IsLocalEditable = true
		existing.SourceURL = ""
	case skill.SkillSourceLocalFile:
		writeJSONError(w, "skill loaded from local file cannot be edited via API; edit the file directly", http.StatusForbidden)
		return
	case skill.SkillSourceLocalDB:
		// local_db 直接修改，无需 fork。
	default:
		if !existing.IsLocalEditable {
			writeJSONError(w, "skill is not local editable", http.StatusForbidden)
			return
		}
	}

	updated := existing
	if req.DisplayName != nil {
		if strings.TrimSpace(*req.DisplayName) == "" {
			writeJSONError(w, "display_name cannot be empty", http.StatusBadRequest)
			return
		}
		updated.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.Description != nil {
		updated.Description = *req.Description
	}
	if req.Content != nil {
		if strings.TrimSpace(*req.Content) == "" {
			writeJSONError(w, "content cannot be empty", http.StatusBadRequest)
			return
		}
		renderer := skill.NewRenderer()
		variables := renderer.ExtractVariables(*req.Content)
		// 替换 system_prompt 模板；保留其它模板不动。
		var templates []skill.SkillTemplate
		replaced := false
		for _, tmpl := range updated.Templates {
			if tmpl.Name == "system_prompt" {
				templates = append(templates, skill.SkillTemplate{
					Name:       "system_prompt",
					Content:    *req.Content,
					Variables:  variables,
					IsRequired: true,
				})
				replaced = true
				continue
			}
			templates = append(templates, tmpl)
		}
		if !replaced {
			templates = append([]skill.SkillTemplate{{
				Name:       "system_prompt",
				Content:    *req.Content,
				Variables:  variables,
				IsRequired: true,
			}}, templates...)
		}
		updated.Templates = templates
	}
	if req.Parameters != nil {
		updated.Parameters = req.Parameters
	}
	if req.Scope != nil {
		sc := skill.SkillScope(strings.TrimSpace(*req.Scope))
		if sc != "" && sc != skill.SkillScopeGlobal && sc != skill.SkillScopeProject && sc != skill.SkillScopeSession {
			writeJSONError(w, "invalid scope: "+string(sc), http.StatusBadRequest)
			return
		}
		updated.Scope = sc
	}
	if req.ProjectID != nil {
		updated.ProjectID = strings.TrimSpace(*req.ProjectID)
	}
	if req.WorkspaceDir != nil {
		updated.WorkspaceDir = strings.TrimSpace(*req.WorkspaceDir)
	}
	updated.UpdatedAt = time.Now().Unix()

	if store != nil {
		if err := store.Save(&updated); err != nil {
			writeJSONError(w, "save skill: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	registry.Register(updated)

	broadcastSkillEvent(hub, skill.EventSkillChanged, updated.ID, map[string]any{
		"id":    updated.ID,
		"state": string(updated.State),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// handleDeleteSkill 处理 DELETE /api/skills/:id。
// local_db 可删除；local_db shadow of built_in 删除后恢复内置版本；
// built_in 本体与 local_file 禁止删除。
func handleDeleteSkill(w http.ResponseWriter, r *http.Request, hub eventBroadcaster, store *skill.Store, registry *skill.Registry, id string) {
	existing, ok := registry.Get(id)
	if !ok {
		writeJSONError(w, "skill not found", http.StatusNotFound)
		return
	}
	// local_file 禁止删除；built_in 本体禁止删除（shadow 可删）；local_db shadow 可删。
	switch existing.Source {
	case skill.SkillSourceLocalFile:
		writeJSONError(w, "skill loaded from local file cannot be deleted via API; remove the file directly", http.StatusForbidden)
		return
	case skill.SkillSourceBuiltIn:
		writeJSONError(w, "built-in skill cannot be deleted; delete its local shadow to restore", http.StatusForbidden)
		return
	}

	if store != nil {
		if err := store.Delete(id); err != nil {
			writeJSONError(w, "delete skill: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	registry.Unregister(id)
	// 若删除的是 built_in 的 local_db shadow，恢复内置版本。
	if skill.IsShadowOfBuiltIn(existing) {
		for _, builtin := range skill.DefaultBuiltins() {
			if builtin.ID == id {
				registry.Register(*builtin)
				break
			}
		}
	}

	broadcastSkillEvent(hub, skill.EventSkillUnloaded, id, map[string]any{
		"id": id,
	})

	w.WriteHeader(http.StatusNoContent)
}

// handleEnableSkill 处理 POST /api/skills/:id/enable。
// 同时更新 registry 内存状态与 store 持久化状态，保证重启后状态一致。
func handleEnableSkill(w http.ResponseWriter, r *http.Request, hub eventBroadcaster, store *skill.Store, registry *skill.Registry, id string) {
	s, ok := registry.Get(id)
	if !ok {
		writeJSONError(w, "skill not found", http.StatusNotFound)
		return
	}
	if s.Source == skill.SkillSourceLocalFile {
		writeJSONError(w, "local_file skill is read-only; edit the file directly", http.StatusForbidden)
		return
	}
	if s.State == skill.SkillStateEnabled {
		// 幂等：已经是启用状态，直接返回当前值。
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s)
		return
	}

	registry.UpdateState(id, skill.SkillStateEnabled)
	s.State = skill.SkillStateEnabled
	s.UpdatedAt = time.Now().Unix()
	if store != nil {
		if err := store.Save(&s); err != nil {
			writeJSONError(w, "save skill: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	broadcastSkillEvent(hub, skill.EventSkillEnabled, id, map[string]any{
		"id":    id,
		"state": string(skill.SkillStateEnabled),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

// handleDisableSkill 处理 POST /api/skills/:id/disable。
func handleDisableSkill(w http.ResponseWriter, r *http.Request, hub eventBroadcaster, store *skill.Store, registry *skill.Registry, id string) {
	s, ok := registry.Get(id)
	if !ok {
		writeJSONError(w, "skill not found", http.StatusNotFound)
		return
	}
	if s.Source == skill.SkillSourceLocalFile {
		writeJSONError(w, "local_file skill is read-only; edit the file directly", http.StatusForbidden)
		return
	}
	if s.State == skill.SkillStateDisabled {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s)
		return
	}

	registry.UpdateState(id, skill.SkillStateDisabled)
	s.State = skill.SkillStateDisabled
	s.UpdatedAt = time.Now().Unix()
	if store != nil {
		if err := store.Save(&s); err != nil {
			writeJSONError(w, "save skill: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	broadcastSkillEvent(hub, skill.EventSkillDisabled, id, map[string]any{
		"id":    id,
		"state": string(skill.SkillStateDisabled),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

// scanConfigResponse 是 GET /api/skills/scan-config 的响应。
type scanConfigResponse struct {
	EnabledDirs []string `json:"enabled_dirs"`
}

// handleGetScanConfig 返回当前启用的扫描目录模板。
// settings 未初始化或未配置时返回全部默认目录。
func handleGetScanConfig(w http.ResponseWriter, r *http.Request, store skill.SettingStore) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	dirs := skill.DefaultSkillScanDirs
	if store != nil {
		val, err := store.GetSetting("skill_scan_dirs")
		if err == nil && strings.TrimSpace(val) != "" {
			var configured []string
			if json.Unmarshal([]byte(val), &configured) == nil {
				dirs = filterValidScanDirs(configured)
			}
		}
	}
	if dirs == nil {
		dirs = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(scanConfigResponse{EnabledDirs: dirs})
}

// handleSetScanConfig 保存启用的扫描目录模板，并重扫全局文件系统 skill。
// 仅接受 DefaultSkillScanDirs 子集，防止写入非法路径模板。
func handleSetScanConfig(w http.ResponseWriter, r *http.Request, store skill.SettingStore, loader *skill.Loader) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if store == nil {
		writeJSONError(w, "settings store unavailable", http.StatusServiceUnavailable)
		return
	}
	var req scanConfigResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	filtered := filterValidScanDirs(req.EnabledDirs)
	if len(filtered) == 0 {
		writeJSONError(w, "enabled_dirs must be a non-empty subset of default scan dirs", http.StatusBadRequest)
		return
	}
	payload, err := json.Marshal(filtered)
	if err != nil {
		writeJSONError(w, "failed to marshal dirs", http.StatusInternalServerError)
		return
	}
	if err := store.SetSetting("skill_scan_dirs", string(payload)); err != nil {
		writeJSONError(w, "save setting: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 保存后只重扫全局层文件系统 skill；project scope local_file skill 保留不动
	// （review M1：spec 要求"不改变已扫描 workdir"，旧实现复用 Reload 会清空 project skill）。
	if loader != nil {
		loader.FileLoader().RefreshGlobal(loader.GlobalDir())
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(scanConfigResponse{EnabledDirs: filtered})
}

// handleScanSkills 强制刷新所有文件系统 skill。
// 收集所有已知非空 session workspace_dir，调用 Loader.RefreshAll。
func handleScanSkills(w http.ResponseWriter, r *http.Request, loader *skill.Loader) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if loader == nil {
		writeJSONError(w, "skill loader unavailable", http.StatusServiceUnavailable)
		return
	}

	sessions, err := db.QuerySessions(0, "")
	if err != nil {
		writeJSONError(w, "list sessions: "+err.Error(), http.StatusInternalServerError)
		return
	}

	workdirs := make([]string, 0, len(sessions))
	workdirProjectIDs := make(map[string]string)
	for _, sess := range sessions {
		wd := sess.WorkspaceDir
		if wd == "" && sess.ProjectID != "" {
			if proj, pErr := db.QueryProjectByID(sess.ProjectID); pErr == nil && proj.WorkingDirectory != "" {
				wd = proj.WorkingDirectory
			}
		}
		if wd == "" {
			continue
		}
		workdirs = append(workdirs, wd)
		if sess.ProjectID != "" {
			workdirProjectIDs[wd] = sess.ProjectID
		}
	}

	if err := loader.RefreshAll(workdirs, workdirProjectIDs); err != nil {
		writeJSONError(w, "refresh failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	loaded := 0
	unloaded := 0 // 完成 refresh 前被移除的 skill 数已在 Loader 内部处理，这里仅作统计占位。
	for range loader.Registry().List(&localFileSource) {
		loaded++
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"scanned_workdirs": len(workdirs),
		"loaded":           loaded,
		"unloaded":         unloaded,
	})
}

var localFileSource = skill.SkillSourceLocalFile

// filterValidScanDirs 仅保留在 DefaultSkillScanDirs 中出现的目录模板。
func filterValidScanDirs(dirs []string) []string {
	valid := make(map[string]bool)
	for _, d := range skill.DefaultSkillScanDirs {
		valid[d] = true
	}
	filtered := make([]string, 0, len(dirs))
	seen := make(map[string]bool)
	for _, d := range dirs {
		if valid[d] && !seen[d] {
			filtered = append(filtered, d)
			seen[d] = true
		}
	}
	return filtered
}

// broadcastSkillEvent 通过 hub 广播 skill 状态变化事件，便于前端实时刷新。
// hub 为 nil 时跳过，方便单测中不依赖 ws.Hub。
func broadcastSkillEvent(hub eventBroadcaster, eventType, skillID string, data map[string]any) {
	if hub == nil {
		return
	}
	hub.SendEvent(event.NewEvent(eventType, "", "server", 0, data))
}

// GetEnabledSkillIDs 返回 registry 中所有处于 enabled 状态的 skill id。
// 用于 EngineConfig.ActiveSkills 注入，让运行时引擎知道哪些 skill 模板需要渲染。
func GetEnabledSkillIDs(registry *skill.Registry) []string {
	if registry == nil {
		return nil
	}
	var ids []string
	for _, s := range registry.List(nil) {
		if s.State == skill.SkillStateEnabled {
			ids = append(ids, s.ID)
		}
	}
	return ids
}
