// cron_api.go — Cron 子系统的 REST API 与 startChatTask 复用函数。
//
// 本文件做两件事：
//   1. startChatTask：把 /api/tasks 的 chat action 核心启动逻辑抽成可复用函数，
//      让原 handler 与 cron 的 start_task action 共用同一条 task 启动链路。
//      Phase 8-A 起内部改走 AgentRunner.Run(spec)。
//   2. RegisterCronAPI：注册 /api/crons* 全部 REST 端点，委托给 cron.Service。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anmingwei/multi-agent-platform/internal/cron"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// startChatTaskOpts 是 startChatTask 的参数。
// 与 /api/tasks chat 请求体字段一一对应；cron start_task action 也用它。
type startChatTaskOpts struct {
	AgentID        string
	Input          string
	SystemPrompt   string
	SessionID      string
	MaxSteps       int
	TimeoutSeconds int
	Scope          string
	AllowedTools   []string
	TokenBudget    int
	CostBudgetUSD  float64
	CaseID         string
}

// RegisterCronAPI 注册 /api/crons* 全部 REST 端点，挂载到传入的 mux。
// 用 mux 参数而非 http.DefaultServeMux，便于测试用 httptest.NewServer 隔离。
func RegisterCronAPI(mux *http.ServeMux, svc *cron.Service) {
	if svc == nil {
		return
	}
	mux.HandleFunc("/api/crons", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleCronList(w, r, svc)
		case http.MethodPost:
			handleCronCreate(w, r, svc)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/crons/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/crons/")
		// 子资源：executions
		if path == "executions" || strings.HasPrefix(path, "executions") {
			handleCronExecutions(w, r, svc, strings.TrimPrefix(path, "executions"))
			return
		}
		// /api/crons/:id[/action]
		parts := strings.SplitN(path, "/", 2)
		id := parts[0]
		if id == "" {
			http.Error(w, "cron id required", http.StatusBadRequest)
			return
		}
		if len(parts) == 1 {
			// /api/crons/:id
			switch r.Method {
			case http.MethodGet:
				handleCronGet(w, r, svc, id)
			case http.MethodPut:
				handleCronUpdate(w, r, svc, id)
			case http.MethodDelete:
				handleCronDelete(w, r, svc, id)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		// /api/crons/:id/<action>
		action := parts[1]
		switch action {
		case "enable":
			handleCronSetStatus(w, r, svc, id, cron.StatusEnabled)
		case "disable":
			handleCronSetStatus(w, r, svc, id, cron.StatusDisabled)
		case "pause":
			handleCronSetStatus(w, r, svc, id, cron.StatusPaused)
		case "resume":
			handleCronSetStatus(w, r, svc, id, cron.StatusEnabled)
		case "trigger":
			handleCronTrigger(w, r, svc, id)
		case "executions":
			handleCronExecutionsByCron(w, r, svc, id)
		default:
			http.Error(w, "unknown action", http.StatusNotFound)
		}
	})
}

// writeJSON 写 JSON 响应。
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// writeError 按错误类型选状态码。
func writeCronError(w http.ResponseWriter, err error) {
	if cron.IsNotFound(err) || errors.Is(err, db.ErrCronNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// handleCronList GET /api/crons?status=&action_type=&q=
func handleCronList(w http.ResponseWriter, r *http.Request, svc *cron.Service) {
	q := r.URL.Query()
	list, err := svc.List(cron.ListFilter{
		Status:     q.Get("status"),
		ActionType: q.Get("action_type"),
		Source:     q.Get("source"),
		Query:      q.Get("q"),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, list)
}

// handleCronCreate POST /api/crons
func handleCronCreate(w http.ResponseWriter, r *http.Request, svc *cron.Service) {
	var in cron.CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c, err := svc.Create(in)
	if err != nil {
		writeCronError(w, err)
		return
	}
	writeJSON(w, c)
}

// handleCronGet GET /api/crons/:id
func handleCronGet(w http.ResponseWriter, r *http.Request, svc *cron.Service, id string) {
	c, err := svc.Get(id)
	if err != nil {
		writeCronError(w, err)
		return
	}
	writeJSON(w, c)
}

// handleCronUpdate PUT /api/crons/:id
func handleCronUpdate(w http.ResponseWriter, r *http.Request, svc *cron.Service, id string) {
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	in := cron.UpdateInput{}
	if v, ok := raw["name"].(string); ok {
		in.Name = &v
	}
	if v, ok := raw["description"].(string); ok {
		in.Description = &v
	}
	if v, ok := raw["schedule_type"].(string); ok {
		st := cron.ScheduleType(v)
		in.ScheduleType = &st
	}
	if v, ok := raw["cron_expr"].(string); ok {
		in.CronExpr = &v
	}
	if v, ok := raw["once_at"].(string); ok {
		in.OnceAt = &v
	}
	if v, ok := raw["timezone"].(string); ok {
		in.Timezone = &v
	}
	if v, ok := raw["display_type"].(string); ok {
		in.DisplayType = &v
	}
	if v, ok := raw["action_type"].(string); ok {
		at := cron.ActionType(v)
		in.ActionType = &at
	}
	if v, ok := raw["action_payload"].(map[string]any); ok {
		in.ActionPayload = &v
	}
	if v, ok := raw["allow_concurrent"].(bool); ok {
		in.AllowConcurrent = &v
	}
	c, err := svc.Update(id, in)
	if err != nil {
		writeCronError(w, err)
		return
	}
	writeJSON(w, c)
}

// handleCronDelete DELETE /api/crons/:id
func handleCronDelete(w http.ResponseWriter, r *http.Request, svc *cron.Service, id string) {
	if err := svc.Delete(id); err != nil {
		writeCronError(w, err)
		return
	}
	writeJSON(w, map[string]any{"deleted": id})
}

// handleCronSetStatus POST /api/crons/:id/{enable,disable,pause,resume}
func handleCronSetStatus(w http.ResponseWriter, r *http.Request, svc *cron.Service, id string, status cron.Status) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	c, err := svc.SetStatus(id, status)
	if err != nil {
		writeCronError(w, err)
		return
	}
	writeJSON(w, c)
}

// handleCronTrigger POST /api/crons/:id/trigger  body: {override_input?}
func handleCronTrigger(w http.ResponseWriter, r *http.Request, svc *cron.Service, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	override := ""
	if r.Body != nil {
		var body struct {
			OverrideInput string `json:"override_input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		override = body.OverrideInput
	}
	exec, err := svc.Trigger(id, override)
	if err != nil {
		writeCronError(w, err)
		return
	}
	writeJSON(w, exec)
}

// handleCronExecutionsByCron GET /api/crons/:id/executions?limit=&offset=&status=
func handleCronExecutionsByCron(w http.ResponseWriter, r *http.Request, svc *cron.Service, id string) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	list, err := svc.ListExecutions(cron.ExecListFilter{
		CronID: id,
		Status: q.Get("status"),
		Limit:  parseIntQuery(q.Get("limit")),
		Offset: parseIntQuery(q.Get("offset")),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, list)
}

// handleCronExecutions 处理 /api/crons/executions 全局执行历史端点。
// GET = 列表（带过滤）；DELETE = 清理。
// sub 为 "" 或 "/" 之外的路径片段（此处不深入解析）。
func handleCronExecutions(w http.ResponseWriter, r *http.Request, svc *cron.Service, sub string) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		list, err := svc.ListExecutions(cron.ExecListFilter{
			CronID: q.Get("cron_id"),
			Status: q.Get("status"),
			Limit:  parseIntQuery(q.Get("limit")),
			Offset: parseIntQuery(q.Get("offset")),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, list)
	case http.MethodDelete:
		q := r.URL.Query()
		n, err := svc.CleanExecutions(cron.CleanFilter{
			CronID: q.Get("cron_id"),
			Status: q.Get("status"),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"deleted": n})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// parseIntQuery 把 query 值解析为 int，失败返回 0。
func parseIntQuery(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// appCronStarter 适配 cron.ActionRunner 需要的 TaskStarter 回调。
// 它捕获 *appServer，从而可以调用 s.startChatTask，彻底消除 startChatTask
// 这个包级闭包变量。
type appCronStarter struct {
	s *appServer
}

// Start 实现 cron.TaskStarter。
func (cs *appCronStarter) Start(ctx context.Context, p cron.StartTaskParams) (taskID, sessionID string, err error) {
	_ = ctx
	if cs.s == nil {
		return "", "", fmt.Errorf("cron starter not initialized")
	}
	return cs.s.startChatTask(startChatTaskOpts{
		AgentID:        p.AgentID,
		Input:          p.Input,
		SystemPrompt:   p.SystemPrompt,
		SessionID:      p.SessionID,
		MaxSteps:       p.MaxSteps,
		TimeoutSeconds: p.TimeoutSeconds,
		Scope:          p.Scope,
		AllowedTools:   p.AllowedTools,
		TokenBudget:    p.TokenBudget,
		CostBudgetUSD:  p.CostBudgetUSD,
		CaseID:         p.CaseID,
	})
}