package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ayanmw/multi-agent-platform/internal/skill"
)

// registerSkillCommandRoutes 挂载 SkillCommand REST API。
//   GET    /api/skill-commands?workdir=&project_id=&q=
//   GET    /api/skill-commands/:id
//   POST   /api/skill-commands/:id/invoke
func registerSkillCommandRoutes(mux *http.ServeMux, hub eventBroadcaster, skillLoader *skill.Loader, skillStore *skill.Store, skillRegistry *skill.Registry) {
	mux.HandleFunc("/api/skill-commands", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		handleListSkillCommands(w, r, skillLoader)
	})

	mux.HandleFunc("/api/skill-commands/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/skill-commands/")
		if path == "" {
			writeJSONError(w, "command ID required", http.StatusBadRequest)
			return
		}

		if r.Method == http.MethodGet {
			handleGetSkillCommand(w, r, skillLoader, path)
			return
		}

		if r.Method == http.MethodPost {
			if suffix, ok := strings.CutSuffix(path, "/invoke"); ok {
				handleInvokeSkillCommand(w, r, hub, skillLoader, skillStore, skillRegistry, suffix)
				return
			}
		}

		writeJSONError(w, "GET or POST /invoke only", http.StatusMethodNotAllowed)
	})
}

// skillCommandListResponse 是 GET /api/skill-commands 的返回结构。
type skillCommandListResponse struct {
	Commands []skillCommandResponse `json:"commands"`
}

// skillCommandResponse 是 skill command 的 JSON 表示。
type skillCommandResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Scope        string   `json:"scope"`
	WorkspaceDir string   `json:"workspace_dir"`
	ProjectID    string   `json:"project_id"`
	SourcePath   string   `json:"source_path"`
	SkillID      string   `json:"skill_id"`
	Tags         []string `json:"tags"`
	Icon         string   `json:"icon"`
}

// skillCommandDetailResponse 是 GET /api/skill-commands/:id 的返回结构。
type skillCommandDetailResponse struct {
	skillCommandResponse
	Prompt string `json:"prompt"`
}

// invokeSkillCommandResponse 是 POST /api/skill-commands/:id/invoke 的返回结构。
type invokeSkillCommandResponse struct {
	EnabledSkillIDs    []string `json:"enabled_skill_ids"`
	TemporarySkillID   string   `json:"temporary_skill_id"`
	Warnings           []string `json:"warnings"`
}

func toSkillCommandResponse(cmd skill.SkillCommand) skillCommandResponse {
	return skillCommandResponse{
		ID:           cmd.ID,
		Name:         cmd.Name,
		Description:  cmd.Description,
		Scope:        string(cmd.Scope),
		WorkspaceDir: cmd.WorkspaceDir,
		ProjectID:    cmd.ProjectID,
		SourcePath:   cmd.SourcePath,
		SkillID:      cmd.SkillID,
		Tags:         cmd.Tags,
		Icon:         cmd.Icon,
	}
}

// commandRegistry 返回 loader 中 commandLoader 的 registry；nil safe。
func commandRegistry(skillLoader *skill.Loader) *skill.CommandRegistry {
	if skillLoader == nil || skillLoader.CommandLoader() == nil {
		return nil
	}
	return skillLoader.CommandLoader().Registry()
}

// handleListSkillCommands 处理 GET /api/skill-commands。
func handleListSkillCommands(w http.ResponseWriter, r *http.Request, skillLoader *skill.Loader) {
	workdir := strings.TrimSpace(r.URL.Query().Get("workdir"))
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	registry := commandRegistry(skillLoader)
	if registry == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(skillCommandListResponse{Commands: []skillCommandResponse{}})
		return
	}

	var cmds []skill.SkillCommand
	if workdir != "" {
		cmds = registry.ListForWorkdir(workdir)
	} else {
		cmds = registry.List("")
	}

	result := make([]skillCommandResponse, 0, len(cmds))
	for _, cmd := range cmds {
		if q != "" && !matchCommandKeyword(cmd, q) {
			continue
		}
		result = append(result, toSkillCommandResponse(cmd))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(skillCommandListResponse{Commands: result})
}

// matchCommandKeyword 按 ID/name/description/tags 匹配关键词。
func matchCommandKeyword(cmd skill.SkillCommand, q string) bool {
	if strings.Contains(strings.ToLower(cmd.ID), q) {
		return true
	}
	if strings.Contains(strings.ToLower(cmd.Name), q) {
		return true
	}
	if strings.Contains(strings.ToLower(cmd.Description), q) {
		return true
	}
	for _, tag := range cmd.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// handleGetSkillCommand 处理 GET /api/skill-commands/:id，包含 prompt 全文。
func handleGetSkillCommand(w http.ResponseWriter, r *http.Request, skillLoader *skill.Loader, id string) {
	registry := commandRegistry(skillLoader)
	if registry == nil {
		writeJSONError(w, "command loader unavailable", http.StatusServiceUnavailable)
		return
	}

	cmd, ok := registry.Get(id)
	if !ok {
		writeJSONError(w, "command not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(skillCommandDetailResponse{
		skillCommandResponse: toSkillCommandResponse(cmd),
		Prompt:               cmd.Prompt,
	})
}

// handleInvokeSkillCommand 处理 POST /api/skill-commands/:id/invoke。
// 权限检查：project scope 命令需当前 session workdir 匹配。
func handleInvokeSkillCommand(w http.ResponseWriter, r *http.Request, hub eventBroadcaster, skillLoader *skill.Loader, skillStore *skill.Store, skillRegistry *skill.Registry, id string) {
	registry := commandRegistry(skillLoader)
	if registry == nil {
		writeJSONError(w, "command loader unavailable", http.StatusServiceUnavailable)
		return
	}

	cmd, ok := registry.Get(id)
	if !ok {
		writeJSONError(w, "command not found", http.StatusNotFound)
		return
	}

	var req struct {
		Workdir string `json:"workdir"`
	}
	decodeErr := json.NewDecoder(r.Body).Decode(&req)
	if decodeErr != nil {
		// 空 body（EOF）允许通过，workdir 回退到 query param。
		if decodeErr == io.EOF {
			req.Workdir = strings.TrimSpace(r.URL.Query().Get("workdir"))
		} else {
			writeJSONError(w, "invalid json body", http.StatusBadRequest)
			return
		}
	}
	workdir := strings.TrimSpace(req.Workdir)

	// project scope 命令需要 workdir 匹配。
	if cmd.Scope == skill.SkillCommandScopeProject {
		if workdir == "" || !isCommandScopeAllowed(workdir, cmd.WorkspaceDir) {
			writeJSONError(w, "command not available for this workspace", http.StatusForbidden)
			return
		}
	}

	resp := invokeSkillCommandResponse{
		EnabledSkillIDs:  []string{},
		TemporarySkillID: "",
		Warnings:         []string{},
	}

	// 启用关联 skill。
	if cmd.SkillID != "" {
		enabled, err := enableSkillByID(hub, skillStore, skillRegistry, cmd.SkillID)
		if err != nil {
			writeJSONError(w, "enable skill: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if enabled {
			resp.EnabledSkillIDs = append(resp.EnabledSkillIDs, cmd.SkillID)
		} else {
			resp.Warnings = append(resp.Warnings, fmt.Sprintf("associated skill %q not found in registry, skipped", cmd.SkillID))
			if strings.TrimSpace(cmd.Prompt) == "" {
				writeJSONError(w, "command has no executable content: associated skill not found and no prompt", http.StatusBadRequest)
				return
			}
		}
	}

	// 将 prompt 注册为临时 skill。
	if strings.TrimSpace(cmd.Prompt) != "" {
		tmpID := registerTemporarySkill(hub, skillRegistry, cmd)
		resp.TemporarySkillID = tmpID
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// enableSkillByID 按 ID 启用 skill；返回 (enabled, error)，其中 enabled=false 表示未实际启用（skill 不存在）
// 但不属于错误（调用方据此决定是否 warn 跳过）。
func enableSkillByID(hub eventBroadcaster, store *skill.Store, registry *skill.Registry, id string) (bool, error) {
	s, ok := registry.Get(id)
	if !ok {
		return false, nil
	}
	if s.State == skill.SkillStateEnabled {
		return true, nil
	}
	registry.UpdateState(id, skill.SkillStateEnabled)
	s.State = skill.SkillStateEnabled
	s.UpdatedAt = time.Now().Unix()
	if store != nil {
		if err := store.Save(&s); err != nil {
			return false, err
		}
	}
	broadcastSkillEvent(hub, skill.EventSkillEnabled, id, map[string]any{
		"id":    id,
		"state": string(skill.SkillStateEnabled),
	})
	return true, nil
}

// registerTemporarySkill 把 command 的 prompt 注册为 source=command_temporary 的启用 skill。
func registerTemporarySkill(hub eventBroadcaster, registry *skill.Registry, cmd skill.SkillCommand) string {
	if registry == nil {
		return ""
	}
	tmpID := "cmd:" + cmd.ID
	renderer := skill.NewRenderer()
	now := time.Now().Unix()
	s := skill.Skill{
		ID:              tmpID,
		Version:         "1.0.0",
		DisplayName:     cmd.Name,
		Description:     "temporary command skill for " + cmd.ID,
		Source:          skill.SkillSourceLocalFile,
		SourceURL:       cmd.SourcePath,
		IsLocalEditable: false,
		State:           skill.SkillStateEnabled,
		Scope:           skill.SkillScopeSession,
		WorkspaceDir:    cmd.WorkspaceDir,
		ProjectID:       cmd.ProjectID,
		Templates: []skill.SkillTemplate{
			{
				Name:       "system_prompt",
				Content:    cmd.Prompt,
				Variables:  renderer.ExtractVariables(cmd.Prompt),
				IsRequired: true,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	registry.Register(s)
	broadcastSkillEvent(hub, skill.EventSkillLoaded, tmpID, map[string]any{
		"id":     tmpID,
		"source": string(s.Source),
		"state":  string(s.State),
	})
	return tmpID
}

// isCommandScopeAllowed 判断 workdir 是否匹配 command 的 workspace_dir。
// 复用 skill.MatchWorkdir（与 ResolveActiveSkills / GET /api/skills?workdir= 同语义），
// 避免裸 strings.HasPrefix 导致 "/repo/proj-evil" 被当作 "/repo/proj" 子目录放行（review M9）。
func isCommandScopeAllowed(workdir, commandWorkdir string) bool {
	if commandWorkdir == "" {
		return true
	}
	if workdir == "" {
		return false
	}
	return skill.MatchWorkdir(commandWorkdir, workdir)
}
