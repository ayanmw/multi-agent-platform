package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/cases"
	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/cost"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/observability"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/skill"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/google/uuid"
)

// TestSkillPromptInjectedE2E 验证 Skill 子系统的端到端 prompt 注入流程：
//
// 流程概览：
//  1. 启动一个最小化的 httptest.Server，复用生产 handleSessionChat handler，
//     把 skillRegistry / hub / cfg / toolRegistry / persist / memRecall 等依赖
//     通过包级 globalSkillRegistry + handler 参数注入。
//  2. 禁用 builtin-code-helper，发起一次 chat，确认 context_window 中 system
//     message 不包含 skill 模板渲染文本（"目标编程语言"）。
//  3. 重新启用 builtin-code-helper，再次 chat，确认 system message 包含该文本。
//
// 为什么走完整 chat 路径而不是直接调用 runAgentLoopWithTurn：
//  1. 真实场景下 SkillRegistry 通过包级 globalSkillRegistry 注入 EngineConfig，
//     只有走完整 chat 才能复用这条注入链路。
//  2. handleSessionChat 已经把 memRecall / costRepo / modelRegistry 等可空依赖
//     全部以参数形式暴露出来，测试中只需把非空依赖填上、其余传 nil 即可。
//  3. 这样能覆盖 Task 8 引入的 SkillRegistry / ActiveSkills 字段在真实 HTTP
//     入口下的渲染逻辑，最贴近线上行为。
func TestSkillPromptInjectedE2E(t *testing.T) {
	// ============================================================
	// 1. 初始化测试数据库 + Skill 子系统
	// ============================================================
	// setupSkillTestDB 会把 db.DB 指向一个临时 SQLite 文件，t.Cleanup 关闭。
	setupSkillTestDB(t)

	skillRegistry := skill.NewRegistry()
	skillStore := skill.NewStore(db.DB)
	skillLoader := skill.NewLoader(skillStore, skillRegistry)
	if err := skillLoader.LoadAll(); err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	// 把 registry 提升为包级变量——runAgentLoopWithTurn 通过 globalSkillRegistry
	// 注入 EngineConfig.SkillRegistry / ActiveSkills。这是生产路径的注入方式，
	// E2E 测试必须复用同一条路径才能验证真实行为。
	globalSkillRegistry = skillRegistry
	t.Cleanup(func() { globalSkillRegistry = nil })

	// 先把两个内置 skill 全部禁用，让第一次 chat 不注入任何 skill prompt。
	// 必须同时禁用 builtin-error-diagnosis，否则它的 system_prompt 也会被
	// Engine 渲染并追加到 system message，导致 "无 skill 注入" 的负向断言失效。
	if !skillRegistry.UpdateState("builtin-code-helper", skill.SkillStateDisabled) {
		t.Fatalf("failed to disable builtin-code-helper initially")
	}
	if !skillRegistry.UpdateState("builtin-error-diagnosis", skill.SkillStateDisabled) {
		t.Fatalf("failed to disable builtin-error-diagnosis initially")
	}

	// ============================================================
	// 2. 构造 Config + ToolRegistry + Hub 等共享依赖
	// ============================================================
	// cfg.LLMUseMock=true 让 CreateProviderFromConfig 走 MockProvider 路径，
	// 不调用真实 LLM API。Endpoint/APIKey 留空即可。
	cfg := &config.Config{
		LLMModel:   "deepseek-v4-flash",
		LLMUseMock: true,
	}
	// 测试中手动构造 Config，未走 config.Load() 的 LoadContractLimits() 流程，
	// 导致所有 ContractLimits 字段为 0。handleSessionChat 会校验 / 钳制请求参数，
	// input length > 0 会触发 bad request。这里显式加载 safe defaults。
	cfg.LoadContractLimits()

	// 注册 builtins + skill 管理工具，与 main.go 启动流程保持一致。
	toolRegistry := tool.NewRegistry()
	tool.RegisterBuiltins(toolRegistry)
	toolRegistry.Register(skill.NewSkillCreateLocalTool(skillStore, skillRegistry))
	toolRegistry.Register(skill.NewSkillDeleteLocalTool(skillStore, skillRegistry))
	toolRegistry.Register(skill.NewSkillListTool(skillRegistry))

	// WebSocket Hub 必须后台运行，handleSessionChat 内部启动的 agent goroutine
	// 会通过 hub.SendEvent 广播事件；Hub 阻塞在 broadcast channel 上没有消费
	// 者会卡死，所以必须开 Run() goroutine。
	hub := ws.NewHub()
	go hub.Run()
	t.Cleanup(func() {
		// Hub 没有显式 Close 方法，依赖进程退出回收。这里仅做标记。
	})

	// DBPersistence 直接读写包级 db.DB，setupSkillTestDB 已注入。
	persist := &DBPersistence{}

	// MemoryRecall 使用 SqliteMemoryDB 适配器；空数据库下 BuildWorkingMemory
	// 返回空 WorkingMemory，FormatForSystemPrompt 产出空字符串，不会污染
	// system prompt。
	memDB := &harness.SqliteMemoryDB{}
	memRecall := harness.NewMemoryRecall(memDB)

	// 可空依赖：approvalHandler 在本次测试不会触发审批（无高危工具调用），
	// 但 handleSessionChat 需要一个非 nil 的实例以便构造 PolicyChain。
	approvalHandler := harness.NewWebSocketApprovalHandler(hub)

	// CheckpointManager 把文件落到 t.TempDir() 下，避免污染工作区。
	checkpointMgr := runtime.NewCheckpointManager(t.TempDir())

	// costRepo / modelRegistry / modelRouter / routerProviders / caseService
	// 都允许传 nil；runAgentLoopWithTurn 中对 nil 做了短路处理。caseService
	// 在 chat 流程中只用于 evaluation repository，普通 chat 不会触发评估。
	var costRepo cost.CostRepository = cost.NewInMemoryCostRepository()
	modelRegistry := llm.NewModelRegistry()
	var modelRouter *llm.Router // nil → Engine 走 cfg.Model 直连
	routerProviders := map[string]llm.Provider{}
	caseService, err := cases.Init(db.DB)
	if err != nil {
		// caseService 不是本次测试的关键依赖，初始化失败也不阻断 E2E 流程。
		t.Logf("cases.Init returned error (continuing with nil caseService): %v", err)
		caseService = nil
	}

	// ============================================================
	// 3. 注入 mock script，让 MockProvider 返回 "hello"
	// ============================================================
	// 使用高 Priority（1000）+ keyword "hello" 命中规则，确保覆盖 builtin
	// dialogue 脚本（builtin priority=100）。脚本返回单条 text 响应，不含
	// tool_calls，ReAct Loop 一步即完成 task_completed。
	mockCleanup := llm.RegisterMockScriptForTest(llm.MockScript{
		ID:         "e2e-skill-inject-hello",
		Priority:   1000,
		MatchInput: []string{"hello"},
		Responses: []llm.MockResponse{
			{
				Type:    llm.MockResponseText,
				Content: "hello",
			},
		},
	})
	t.Cleanup(mockCleanup)

	// ============================================================
	// 4. 挂载路由并启动 httptest.Server
	// ============================================================
	// 构造最小 appServer，挂载测试中需要的 sessions / tasks handler。
	// handleSessionChat、handleGetTask 等已改为 *appServer 方法，
	// 测试必须复用同一实例以保证依赖一致。
	server := &appServer{
		cfg:              cfg,
		hub:              hub,
		toolRegistry:     toolRegistry,
		persist:          persist,
		approvalHandler:  approvalHandler,
		memRecall:        memRecall,
		checkpointMgr:    checkpointMgr,
		memDB:            memDB,
		costRepo:         costRepo,
		modelRegistry:    modelRegistry,
		modelRouter:      modelRouter,
		routerProviders:  routerProviders,
		caseService:      caseService,
		skillRegistry:    skillRegistry,
		skillStore:       skillStore,
	}

	mux := http.NewServeMux()

	// Skill 管理路由：enable / disable 等。
	registerSkillRoutes(mux, hub, skillStore, skillRegistry)

	// Sessions 路由：POST /api/sessions 创建 session。
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		server.handleSessions(w, r)
	})
	// /api/sessions/{id}/chat —— 与 main.go 中路径分发保持一致。
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/chat") {
			server.handleSessionChat(w, r)
			return
		}
		// 其它 sessions 子路径（messages / workspace 等）本次测试不涉及，
		// 直接交给 handleSessionByID 处理；未覆盖的 method 会被它返回 405。
		server.handleSessionByID(w, r)
	})

	// Tasks 路由：GET /api/tasks/{id}/context_window。
	// 复用 main.go 的路径前缀分发逻辑——context_window 子路径由
	// handleGetTaskContextWindow 处理，其余 GET 交给 handleGetTask。
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		if path == "" {
			http.Error(w, "task ID required", http.StatusNotFound)
			return
		}
		if strings.HasSuffix(path, "/context_window") {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			id := strings.TrimSuffix(path, "/context_window")
			server.handleGetTaskContextWindow(w, r, id)
			return
		}
		if r.Method == http.MethodGet {
			r.URL.RawQuery = "id=" + path
			server.handleGetTask(w, r)
			return
		}
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
	})

	// Tracer 必须非 nil，否则 engine.go 中 think() 会跳过 trace span 创建，
	// 但 RootTraceCtx fallback 依赖 tracer.StartRoot——用默认 Tracer 即可。
	_ = observability.NewTracer(2000)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	client := ts.Client()

	// ============================================================
	// 5. 创建 session
	// ============================================================
	createSessionBody, _ := json.Marshal(map[string]any{
		"name":      "skill-e2e",
		"user_input": "hello",
	})
	resp, err := client.Post(ts.URL+"/api/sessions", "application/json", bytes.NewReader(createSessionBody))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		body := readBody(t, resp)
		t.Fatalf("expected 201 creating session, got %d: %s", resp.StatusCode, body)
	}
	var sessResp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(strings.NewReader(readBodyAfterStatus(t, resp))).Decode(&sessResp); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	sessionID := sessResp.SessionID
	if sessionID == "" {
		t.Fatalf("empty session_id in response")
	}

	// ============================================================
	// 6. 第一次 chat：skill 已禁用，system prompt 不应包含 "目标编程语言"
	// ============================================================
	taskID1 := postChat(t, client, ts.URL, sessionID, "hello")
	systemPrompt1 := waitForSystemPrompt(t, client, ts.URL, taskID1)
	// 断言用 "用户使用的编程语言" —— 这是 builtin-code-helper 的 system_prompt
	// 模板内容中的独有短语。任务描述里提到的 "目标编程语言" 是 SkillParameter
	// 的 Description 字段，并不会被渲染进 system prompt；真正注入到 system
	// message 的是模板 Content，因此用模板中的短语作为注入标记。
	if strings.Contains(systemPrompt1, "用户使用的编程语言") {
		t.Fatalf("system prompt should NOT contain skill marker when skill is disabled, got: %s", systemPrompt1)
	}
	t.Logf("[turn 1] system prompt length=%d, skill marker absent ✓", len(systemPrompt1))

	// ============================================================
	// 7. 启用 builtin-code-helper
	// ============================================================
	resp, err = client.Post(ts.URL+"/api/skills/builtin-code-helper/enable", "application/json", nil)
	if err != nil {
		t.Fatalf("enable skill: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("expected 200 enabling skill, got %d: %s", resp.StatusCode, body)
	}
	// 确认 registry 状态已同步——runAgentLoopWithTurn 通过 GetEnabledSkillIDs
	// 读取 registry，必须保证此处状态为 enabled 才能进入 ActiveSkills 列表。
	if s, ok := skillRegistry.Get("builtin-code-helper"); !ok || s.State != skill.SkillStateEnabled {
		t.Fatalf("registry state not synced to enabled after enable API: %+v", s)
	}

	// ============================================================
	// 8. 第二次 chat：skill 已启用，system prompt 应包含 "目标编程语言"
	// ============================================================
	taskID2 := postChat(t, client, ts.URL, sessionID, "hello")
	systemPrompt2 := waitForSystemPrompt(t, client, ts.URL, taskID2)
	// 同上，用模板 Content 中的独有短语验证 skill prompt 已注入。
	if !strings.Contains(systemPrompt2, "用户使用的编程语言") {
		t.Fatalf("system prompt should contain skill marker after enabling skill, got: %s", systemPrompt2)
	}
	t.Logf("[turn 2] system prompt length=%d, skill marker present ✓", len(systemPrompt2))

	// ============================================================
	// 9. 清理：禁用 builtin-code-helper，恢复 registry 默认状态
	// ============================================================
	// 不使用 HTTP 调用而直接操作 registry，避免对 httptest.Server 的额外依赖；
	// 测试结束时 db.DB 会被 setupSkillTestDB 的 cleanup 关闭，HTTP 调用可能
	// 触发 store.Save 失败的噪音日志。
	skillRegistry.UpdateState("builtin-code-helper", skill.SkillStateDisabled)
	skillRegistry.UpdateState("builtin-error-diagnosis", skill.SkillStateDisabled)
}

// postChat 发起一次 POST /api/sessions/{id}/chat 请求并返回新创建的 task_id。
// chat 是异步 goroutine，本函数只负责投递请求并解析响应中的 task_id。
func postChat(t *testing.T, client *http.Client, baseURL, sessionID, input string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"input":     input,
		"max_steps": 30,
	})
	resp, err := client.Post(baseURL+"/api/sessions/"+sessionID+"/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post chat: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		t.Fatalf("expected 200 from chat, got %d: %s", resp.StatusCode, buf.String())
	}
	var chatResp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("decode chat response: %v", err)
	}
	if chatResp.TaskID == "" {
		t.Fatalf("empty task_id in chat response")
	}
	return chatResp.TaskID
}

// waitForSystemPrompt 轮询 GET /api/tasks/{id}/context_window，返回 system 消息
// 的 content。chat 是异步的，引擎需要一点时间才能写入 live snapshot 或
// session_messages，因此最多重试 5 次，每次间隔 200ms；同时首次调用前先 sleep
// 300ms 让引擎有机会跑完第一次 think()。
func waitForSystemPrompt(t *testing.T, client *http.Client, baseURL, taskID string) string {
	t.Helper()
	// 先给引擎一点启动时间。
	time.Sleep(300 * time.Millisecond)

	var lastErr string
	for attempt := 0; attempt < 5; attempt++ {
		resp, err := client.Get(baseURL + "/api/tasks/" + taskID + "/context_window")
		if err != nil {
			lastErr = err.Error()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			// 任务尚未持久化或引擎尚未写入 snapshot，继续重试。
			resp.Body.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			buf := new(bytes.Buffer)
			_, _ = buf.ReadFrom(resp.Body)
			resp.Body.Close()
			lastErr = "status " + resp.Status + ": " + buf.String()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var snapshot llm.ContextWindowSnapshot
		if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
			resp.Body.Close()
			lastErr = err.Error()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		resp.Body.Close()
		// 找第一条 role=system 的消息返回。ReAct Loop 第一步就会写入 system
		// 消息，因此只要 snapshot 非空就一定能拿到。
		for _, m := range snapshot.Messages {
			if m.Role == "system" {
				return m.Content
			}
		}
		lastErr = "no system message in snapshot"
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("waitForSystemPrompt exhausted retries for task=%s: %s", taskID, lastErr)
	return ""
}

// readBodyAfterStatus 在调用方已经读取过 resp.StatusCode 之后，读取并返回 body
// 字符串。这里为了配合 create session 流程：先检查状态码，再解析 body。
func readBodyAfterStatus(t *testing.T, resp *http.Response) string {
	t.Helper()
	// resp.Body 可能已经被 readBody 关闭——这里通过重新读取已缓冲的 body
	// 兼容两种调用顺序。但当前调用方在状态码检查后没有 readBody，所以我们
	// 直接读取 resp.Body。
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.String()
}

// 为了避免未使用 uuid 包导致编译失败，保留一个无副作用的引用。
// 真正的 task_id 由 handleSessionChat 内部的 newTaskID() 生成，不在此处构造。
var _ = uuid.New
