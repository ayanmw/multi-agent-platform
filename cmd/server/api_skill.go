package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/skill"
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
//
// 所有 handler 直接操作传入的 skillStore / skillRegistry，避免与全局变量耦合，
// 方便在测试中传入隔离实例。
func registerSkillRoutes(mux *http.ServeMux, hub eventBroadcaster, skillStore *skill.Store, skillRegistry *skill.Registry) {
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
func handleListSkills(w http.ResponseWriter, r *http.Request, registry *skill.Registry) {
	var source *skill.SkillSource
	if s := strings.TrimSpace(r.URL.Query().Get("source")); s != "" {
		src := skill.SkillSource(s)
		source = &src
	}
	skills := registry.List(source)
	if skills == nil {
		skills = []skill.Skill{}
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
	ID          string                   `json:"id"`
	DisplayName string                   `json:"display_name"`
	Description string                   `json:"description"`
	Content     string                   `json:"content"`
	Parameters  []skill.SkillParameter   `json:"parameters"`
	Variables   map[string]any           `json:"variables"`
	Tags        []string                 `json:"tags"`
	Authors     []string                 `json:"authors"`
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
// 所有字段可选；仅 display_name/description/content/parameters 会被更新。
type skillUpdateRequest struct {
	DisplayName *string                 `json:"display_name"`
	Description *string                 `json:"description"`
	Content     *string                 `json:"content"`
	Parameters  []skill.SkillParameter  `json:"parameters"`
}

// handleUpdateSkill 处理 PUT /api/skills/:id，仅允许修改 local editable skill。
// 内置 skill 或非 editable 返回 403；不存在返回 404。
func handleUpdateSkill(w http.ResponseWriter, r *http.Request, hub eventBroadcaster, store *skill.Store, registry *skill.Registry, id string) {
	existing, ok := registry.Get(id)
	if !ok {
		writeJSONError(w, "skill not found", http.StatusNotFound)
		return
	}
	if !existing.IsLocalEditable {
		writeJSONError(w, "skill is not local editable", http.StatusForbidden)
		return
	}

	var req skillUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
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

// handleDeleteSkill 处理 DELETE /api/skills/:id，仅允许删除 local editable skill。
func handleDeleteSkill(w http.ResponseWriter, r *http.Request, hub eventBroadcaster, store *skill.Store, registry *skill.Registry, id string) {
	existing, ok := registry.Get(id)
	if !ok {
		writeJSONError(w, "skill not found", http.StatusNotFound)
		return
	}
	if !existing.IsLocalEditable {
		writeJSONError(w, "skill is not local editable", http.StatusForbidden)
		return
	}

	if store != nil {
		if err := store.Delete(id); err != nil {
			writeJSONError(w, "delete skill: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	registry.Unregister(id)

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
