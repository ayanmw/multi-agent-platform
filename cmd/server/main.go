package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/auth"
	"github.com/anmingwei/multi-agent-platform/internal/cases"
	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/internal/cost"
	"github.com/anmingwei/multi-agent-platform/internal/harness"
	"github.com/anmingwei/multi-agent-platform/internal/llm"
	"github.com/anmingwei/multi-agent-platform/internal/memory"
	"github.com/anmingwei/multi-agent-platform/internal/observability"
	"github.com/anmingwei/multi-agent-platform/internal/orchestrator"
	"github.com/anmingwei/multi-agent-platform/internal/runtime"
	"github.com/anmingwei/multi-agent-platform/internal/skill"
	"github.com/anmingwei/multi-agent-platform/internal/tool"
	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp"
	"github.com/anmingwei/multi-agent-platform/internal/tool/mcp/marketplace"
	"github.com/anmingwei/multi-agent-platform/internal/version"
	"github.com/anmingwei/multi-agent-platform/internal/ws"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
	"github.com/anmingwei/multi-agent-platform/pkg/event"
	"github.com/anmingwei/multi-agent-platform/web"
	"github.com/google/uuid"
)

// cancelRegistry 把 task_id 映射到当前运行中 agent loop 的 context.CancelFunc。
// WebSocket 控制消息可以查找并调用这些函数来取消任务。访问由 sync.Map 同步。
//
// Phase 7-A: key 规则扩展为支持子 agent。root task 使用纯 taskID；
// 子 agent（multi-agent 中的某个 agent）使用 "taskID/agentID" 形式。
// 这样 cancel/pause/resume 控制消息可以通过 agent_id 字段精确到某一个 agent。
var cancelRegistry sync.Map

// engineRegistry 把运行中的 *runtime.Engine 按 key 索引，供 control handler
// 在收到 pause/resume 时调用 Engine.Pause / Engine.Resume。key 与
// cancelRegistry 一致：root 任务用 taskID；子 agent 用 "taskID/agentID"。
var engineRegistry sync.Map

// leaderDispatchEnabled 控制 dispatch_sub_agent 工具是否可用。
// 仅在 leader agent 运行期间置为 true；worker agent 运行时保持 false，
// 从而保证即使 tool Registry 共享，worker 也无法越权派发子 agent。
var leaderDispatchEnabled atomic.Bool

// Package-level 进程可观测性状态 (Phase 7-C)。
var (
	// tracer 是共享的、无依赖的 trace span 收集器。
	tracer = observability.NewTracer(2000)
	// traceRegistry 保存 root trace context，便于 handler 把它传进 Engine。
	traceRegistry sync.Map
)

// globalSkillRegistry 是 Skill 子系统的全局注册表引用。
//
// 在 main() 中初始化后保留为包级变量，让 runAgentLoopWithTurn 等闭包能直接把
// SkillRegistry / ActiveSkills 注入 EngineConfig，而不必把参数一路透传到所有
// handler 签名上（runAgentLoop 已有 20+ 个参数，再加会失控）。
//
// 当 db.DB 未初始化时仍是一个空 registry，避免 nil 解引用。
var globalSkillRegistry *skill.Registry

// storeCancel 注册 task/agent 对应的取消函数。
//
// 行为说明：
//   - 当 agentID 为空或为 "orchestrator" 时，仅以 taskID 为 key；
//   - 否则同时写入两个 key：taskID/agentID（精确查找）与 taskID（统一取消）。
//
// 写入两个 key 的目的是让 "取消整个任务" 与 "仅取消某个子 agent" 都能命中。
func storeCancel(taskID, agentID string, cancel context.CancelFunc) {
	if cancel == nil {
		return
	}
	if agentID == "" || agentID == "orchestrator" {
		cancelRegistry.Store(taskID, cancel)
		return
	}
	cancelRegistry.Store(taskID+"/"+agentID, cancel)
	cancelRegistry.Store(taskID, cancel)
}

// loadCancel 查找指定 task/agent 对应的取消函数。
// 优先以 taskID/agentID 精确查找，未命中时回退到 taskID（兼容旧 root 行为）。
func loadCancel(taskID, agentID string) (context.CancelFunc, bool) {
	if taskID == "" {
		return nil, false
	}
	key := taskID
	if agentID != "" && agentID != "orchestrator" {
		key = taskID + "/" + agentID
	}
	if v, ok := cancelRegistry.Load(key); ok {
		return v.(context.CancelFunc), true
	}
	// 回退到 root task 的 cancel，保证向下兼容。
	if v, ok := cancelRegistry.Load(taskID); ok {
		return v.(context.CancelFunc), true
	}
	return nil, false
}

// removeCancel 同时清除 root key 与 per-agent key，避免 goroutine 退出后残留。
func removeCancel(taskID, agentID string) {
	cancelRegistry.Delete(taskID)
	if agentID != "" && agentID != "orchestrator" {
		cancelRegistry.Delete(taskID + "/" + agentID)
	}
}

// storeEngine 把正在运行的 Engine 实例注册到全局表中，control handler
// 通过它直接调用 Pause / Resume。注意 lock-free（sync.Map）。
func storeEngine(taskID, agentID string, engine *runtime.Engine) {
	if engine == nil {
		return
	}
	if agentID == "" || agentID == "orchestrator" {
		engineRegistry.Store(taskID, engine)
		return
	}
	engineRegistry.Store(taskID+"/"+agentID, engine)
	engineRegistry.Store(taskID, engine)
}

// loadEngine 取出与 task/agent 关联的 Engine 实例，便于 pause/resume 控制。
func loadEngine(taskID, agentID string) (*runtime.Engine, bool) {
	if taskID == "" {
		return nil, false
	}
	key := taskID
	if agentID != "" && agentID != "orchestrator" {
		key = taskID + "/" + agentID
	}
	if v, ok := engineRegistry.Load(key); ok {
		return v.(*runtime.Engine), true
	}
	if v, ok := engineRegistry.Load(taskID); ok {
		return v.(*runtime.Engine), true
	}
	return nil, false
}

// removeEngine 清理 engineRegistry 中的两条记录。
func removeEngine(taskID, agentID string) {
	engineRegistry.Delete(taskID)
	if agentID != "" && agentID != "orchestrator" {
		engineRegistry.Delete(taskID + "/" + agentID)
	}
}

func main() {
	port := flag.String("port", "8080", "HTTP server port")
	flag.Parse()

	// 首先注册 WebSocket 控制 handler。它使用包级 cancelRegistry，
	// 以便 WebSocket 控制消息可以取消运行中的任务。
	// (sync.Map 自带 noCopy 锁；这里取它的地址只是为了副作用。)
	_ = &cancelRegistry

	// Phase 6-D: 根据配置初始化结构化日志级别。
	observability.DefaultLogger.SetLevel(observability.ParseLogLevel(os.Getenv("LOG_LEVEL")))

	// 配置双日志：一份持久化的结构化日志文件用于详细追踪，控制台用于简洁
	// 可读的启动/运行时信息。文件日志使用 JSON (StructuredLogger)；
	// 控制台使用 Go 默认 log 包输出纯文本。LOG_LEVEL 仍然过滤 JSON 文件日志。
	if logPath := os.Getenv("LOG_FILE"); logPath != "" {
		if err := initDualLogging(logPath); err != nil {
			log.Printf("Warning: failed to open log file %s: %v (continuing with console only)", logPath, err)
		}
	}

	// 从 .env 与环境变量加载配置
	cfg, err := config.Load()
	if err != nil {
		observability.DefaultLogger.Error("server", "failed to load config", map[string]any{"error": err.Error()})
		log.Fatalf("Failed to load config: %v", err)
	}
	if *port != "8080" || cfg.ServerPort == "" {
		cfg.ServerPort = *port
	}

	// 初始化 WebSocket Hub
	hub := ws.NewHub()
	go hub.Run()

	approvalHandler := harness.NewWebSocketApprovalHandler(hub)

	observability.DefaultLogger.Info("server", "initializing subsystems", map[string]any{
		"port":      cfg.ServerPort,
		"db_path":   cfg.DBPath,
		"llm_model": cfg.LLMModel,
	})

	// 注册控制 handler，用于客户端的 pause/resume/cancel 及审批决定
	hub.SetControlHandler(func(msg ws.ClientControlMsg) {
		observability.DefaultLogger.Debug("control", "received client control message", map[string]any{
			"action":      msg.Action,
			"task_id":     msg.TaskID,
			"agent_id":    msg.AgentID,
			"approval_id": msg.ApprovalID,
		})

		// Phase 5: 把审批决定路由到 ApprovalHandler
		switch msg.Action {
		case "approve":
			if msg.ApprovalID != "" {
				approvalHandler.HandleDecision(msg.ApprovalID, true)
			}
		case "deny":
			if msg.ApprovalID != "" {
				approvalHandler.HandleDecision(msg.ApprovalID, false)
			}
		case "cancel":
			if msg.TaskID == "" {
				observability.DefaultLogger.Warn("control", "cancel received without task_id", nil)
				hub.SendEvent(event.NewEvent("system_info", "", "server", 0, map[string]any{
					"message": "cancel requires task_id",
				}))
				return
			}
			// Phase 7-A：优先按 agentID 精确取消；未提供 agent_id 时退回到 root 任务级取消。
			if cancelFn, ok := loadCancel(msg.TaskID, msg.AgentID); ok {
				target := msg.TaskID
				if msg.AgentID != "" {
					target = msg.TaskID + "/" + msg.AgentID
				}
				observability.DefaultLogger.Info("control", "cancelling task", map[string]any{"target": target, "agent_id": msg.AgentID})
				cancelFn()
			} else {
				observability.DefaultLogger.Warn("control", "cancel received for unknown task", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})
			}
		case "pause":
			if msg.TaskID == "" {
				observability.DefaultLogger.Warn("control", "pause received without task_id", nil)
				hub.SendEvent(event.NewEvent("system_info", "", "server", 0, map[string]any{
					"message": "pause requires task_id",
				}))
				return
			}
			if engine, ok := loadEngine(msg.TaskID, msg.AgentID); ok {
				observability.DefaultLogger.Info("control", "pausing engine", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})
				engine.Pause()
			} else {
				observability.DefaultLogger.Warn("control", "pause received for unknown task", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})
				hub.SendEvent(event.NewEvent("system_info", msg.TaskID, "server", 0, map[string]any{
					"message":     "pause target not found",
					"action":      "pause",
					"status_code": 404,
				}))
			}
		case "resume":
			if msg.TaskID == "" {
				observability.DefaultLogger.Warn("control", "resume received without task_id", nil)
				hub.SendEvent(event.NewEvent("system_info", "", "server", 0, map[string]any{
					"message": "resume requires task_id",
				}))
				return
			}
			if engine, ok := loadEngine(msg.TaskID, msg.AgentID); ok {
				observability.DefaultLogger.Info("control", "resuming engine", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})
				engine.Resume()
			} else {
				observability.DefaultLogger.Warn("control", "resume received for unknown task", map[string]any{"task_id": msg.TaskID, "agent_id": msg.AgentID})
				hub.SendEvent(event.NewEvent("system_info", msg.TaskID, "server", 0, map[string]any{
					"message":     "resume target not found",
					"action":      "resume",
					"status_code": 404,
				}))
			}
		default:
			observability.DefaultLogger.Warn("control", "unknown control action", map[string]any{"action": msg.Action})
		}
	})

	// 初始化 cost repository（有 SQLite 用 SQLite，否则回退到内存）。
	var costRepo cost.CostRepository = cost.NewInMemoryCostRepository()
	_ = costRepo

	// Auth store 与 API —— 在 DB 初始化之后建立（API key 认证）。
	var authStore auth.APIKeyStore
	var authAPI *auth.AuthAPI

	// 初始化数据库
	var caseService *cases.Service
	if err := db.Init(cfg.DBPath); err != nil {
		observability.DefaultLogger.Warn("database", "db init failed, continuing without persistence", map[string]any{"error": err.Error()})
	} else {
		observability.DefaultLogger.Info("database", "initialized", map[string]any{"path": cfg.DBPath})
		// Phase 7-C: DB 就绪后，把默认 auditor 切换为 SQLite 持久化实现。
		observability.DefaultAuditor = observability.NewSQLiteAuditor(observability.NewMemoryAuditor(10000))

		var repoErr error
		costRepo, repoErr = cost.NewSqliteCostRepository(db.DB)
		if repoErr != nil {
			observability.DefaultLogger.Warn("cost", "failed to create sqlite cost repository, falling back to memory", map[string]any{"error": repoErr.Error()})
			costRepo = cost.NewInMemoryCostRepository()
		}
		// 初始化 auth store，并在首次启动时种入默认 admin + API key。
		if db.DB != nil {
			authStore = auth.NewSqliteAPIKeyStore(db.DB)
			authAPI = auth.NewAuthAPI(authStore)
			seedDefaultAdminIfNeeded(authStore)
			// 在未鉴权模式下建立一个稳定的兜底 user ID。该 seed user 供
			// auth middleware 与 /api/auth/api-keys 在 REQUIRE_AUTH 关闭时使用。
			authAPI.SetSeedUserIDFromStore(authStore)
		}

		// 若不存在则种入默认 agent
		if err := db.SeedDefaultAgent(); err != nil {
			observability.DefaultLogger.Warn("database", "failed to seed default agent", map[string]any{"error": err.Error()})
		}
		// 若不存在则种入默认 project
		if err := db.SeedDefaultProject(); err != nil {
			observability.DefaultLogger.Warn("database", "failed to seed default project", map[string]any{"error": err.Error()})
		}

		// DB 就绪后初始化 case service。
		var svcErr error
		caseService, svcErr = cases.Init(db.DB)
		if svcErr != nil {
			observability.DefaultLogger.Warn("cases", "failed to initialize case service", map[string]any{"error": svcErr.Error()})
		}
	}

	// 初始化 Memory 基础设施 —— Heartbeat 负责事件片段汇总
	memDB := &harness.SqliteMemoryDB{}
	// Phase 6-F: 构建一个由 LLM 驱动的 summarizer，失败时回退到现有的
	// 关键词路径。Provider 复用 engine 的 CreateProviderFromConfig
	// （真实模式用真实 LLM，mock 模式用 mock）。
	summarizerProvider, _ := llm.CreateProviderFromConfig(cfg, cfg.LLMModel, "memory-summarizer")
	keywordAdapter := harness.NewKeywordAdapter(
		nil,
		func(ctx context.Context, taskID string, convs []db.ConversationRecord, steps []db.StepRecord) (string, error) {
			return harness.BuildKeywordEpisodeSummary(memDB, taskID)
		},
	)
	var summarizer harness.LLMSummarizer
	if summarizerProvider != nil {
		summarizer = harness.NewLLMSummarizerImpl(summarizerProvider, cfg.LLMModel, keywordAdapter, nil)
	}
	heartbeat := harness.NewHeartbeat(memDB, summarizer)
	go heartbeat.Start(context.Background())
	log.Println("Memory Heartbeat started (5min interval, adaptive)")

	// 初始化 MemoryRecall，携带 vector store 以支持语义记忆召回。
	// 本地 embedding provider（TF-IDF/one-hot hash）与基于 SQLite 的
	// vector store 让我们可以对已汇总和语义记忆做余弦相似度检索。
	// 向量持久化到 memory_embeddings 表（v16 migration），因此内存索引
	// 可以在启动时通过 SqliteVectorStore.Reload() 重建。
	var embedProvider llm.EmbeddingProvider = llm.NewLocalEmbeddingProvider(2048)
	if params := cfg.EmbeddingProviderParams(); params.Provider != "" && params.Provider != "local" {
		switch params.Provider {
		case "openai":
			endpoint := params.Endpoint
			if endpoint == "" {
				endpoint = "https://api.openai.com/v1"
			}
			model := params.Model
			if model == "" {
				model = "text-embedding-3-small"
			}
			embedProvider = llm.NewOpenAIEmbeddingProvider(endpoint, params.APIKey, model, params.Dimensions)
			observability.DefaultLogger.Info("embedding", "using OpenAI embedding provider", map[string]any{"model": model})
		case "cohere":
			endpoint := params.Endpoint
			if endpoint == "" {
				endpoint = "https://api.cohere.com"
			}
			model := params.Model
			if model == "" {
				model = "embed-english-v3.0"
			}
			embedProvider = llm.NewCohereEmbeddingProvider(endpoint, params.APIKey, model, params.Dimensions)
			observability.DefaultLogger.Info("embedding", "using Cohere embedding provider", map[string]any{"model": model})
		default:
			observability.DefaultLogger.Warn("embedding", "unsupported embedding provider, falling back to local", map[string]any{"provider": params.Provider})
		}
	}
	var vectorStore memory.VectorStore
	if db.DB != nil {
		vs, err := memory.NewSqliteVectorStore(db.DB, embedProvider)
		if err != nil {
			observability.DefaultLogger.Warn("memory", "failed to create sqlite vector store, falling back to in-memory", map[string]any{"error": err.Error()})
			vectorStore = memory.NewInMemoryVectorStore(embedProvider)
		} else {
			vectorStore = vs
		}
	} else {
		vectorStore = memory.NewInMemoryVectorStore(embedProvider)
	}
	memRecall := harness.NewMemoryRecallWithVectorStore(memDB, embedProvider, vectorStore)

	// 增量 memory indexer：新记忆创建时即时 embedding，
	// 而不是启动时重建整个索引。
	memoryIndexer := memory.NewMemoryIndexer(vectorStore, embedProvider, memory.MemoryIndexerOptions{DedupeThreshold: 0.95})
	db.PostInsertMemoryHook = func(memoryID, content string) {
		if err := memoryIndexer.OnMemoryCreated(memoryID, content); err != nil {
			observability.DefaultLogger.Warn("memory-indexer", "failed to index memory", map[string]any{"memory_id": memoryID, "error": err.Error()})
		}
	}

	// 从现有 memory 构建向量索引（尽力而为 —— 失败只会降低召回质量，
	// 不会阻断启动）。
	if err := memRecall.BuildVectorIndex(); err != nil {
		log.Printf("MemoryRecall: failed to build vector index: %v", err)
	}

	// 初始化持久化 adapter
	persist := &DBPersistence{}

	// Phase mock: 初始化 mock script store 并加载内置脚本。
	// 内存 store 始终可用；若 DB 已初始化，则额外创建 SQLite 后端 store，
	// 并把动态脚本加载到默认 store 中，使管理 API 与 MockProvider 共享同一份脚本。
	mockStore := llm.DefaultMockStore
	if db.DB != nil {
		sqliteMockStore := llm.NewSqliteMockScriptStore(db.DB)
		if err := sqliteMockStore.LoadBuiltin(llm.BuiltinMockScripts()); err != nil {
			observability.DefaultLogger.Warn("mock", "failed to seed builtin mock scripts into sqlite", map[string]any{"error": err.Error()})
		}
		dynamicScripts, err := sqliteMockStore.List()
		if err != nil {
			observability.DefaultLogger.Warn("mock", "failed to load dynamic mock scripts", map[string]any{"error": err.Error()})
		} else {
			if err := mockStore.LoadBuiltin(dynamicScripts); err != nil {
				observability.DefaultLogger.Warn("mock", "failed to load dynamic scripts into default store", map[string]any{"error": err.Error()})
			}
		}
	} else {
		if err := mockStore.LoadBuiltin(llm.BuiltinMockScripts()); err != nil {
			observability.DefaultLogger.Warn("mock", "failed to load builtin mock scripts", map[string]any{"error": err.Error()})
		}
	}
	observability.DefaultLogger.Info("mock", "mock provider initialized", map[string]any{
		"use_mock":       cfg.LLMUseMock,
		"real_cases":     cfg.LLMRealCases,
		"mock_endpoints": cfg.LLMMockEndpoints,
	})

	// Phase 6-D: 用默认 profile 初始化 model registry，用于成本追踪和未来的
	// 多 provider 路由。CostTracker 在构建 CostRecord 时通过它解析 tier/价格信息。
	modelRegistry := llm.NewModelRegistry()
	// R1 修复：先注册 cfg.LLMModel 对应的 profile，再注册 DefaultProfiles。
	//
	// 为什么顺序重要：Router.filterCandidates 在目标 tier 内按 registry 注册
	// 顺序取第一个候选（GetByTier 返回 byTier[tier] 的注册顺序）。DefaultProfiles
	// 注册的是 "deepseek-v4-flash"（TierEfficient），若先注册它，simple_chat 意图
	// 会选中 "deepseek-v4-flash"，而实际部署 token 通常只能访问 cfg.LLMModel
	// （如 "deepseek-v4-flash-local"）→ 403 → 死循环。
	//
	// 把 cfg.LLMModel 的克隆 profile 注册在最前，让它成为 TierEfficient 的首选
	// 候选，Router 就会选中 token 实际可访问的模型名。仅在 cfg.LLMModel 未被
	// DefaultProfiles 覆盖时补注（避免重复注册同名 profile）。DefaultProfiles 仍
	// 照常注册，作为其它 tier（如 TierStandard 的 -pro）的成本/能力参考。
	if cfg.LLMModel != "" {
		defaults := llm.DefaultProfiles()
		if modelRegistry.Get(cfg.LLMModel) == nil {
			// 克隆第 0 个 DefaultProfile（TierEfficient、低价）改 Name。
			base := defaults[0]
			localProfile := *base
			localProfile.Name = cfg.LLMModel
			localProfile.FallbackModel = "" // -local 是兜底模型，不再指向自身
			modelRegistry.Register(&localProfile)
			log.Printf("ModelRegistry: registered cfg.LLMModel profile %q (tier=%s, cloned from %s)",
				cfg.LLMModel, localProfile.Tier, base.Name)
		}
	}
	for _, profile := range llm.DefaultProfiles() {
		// D1 修复：把所有非 cfg.LLMModel 的 tier profile 的 FallbackModel 重定向
		// 到 cfg.LLMModel（token 确定可访问的本地兜底模型），而非 DefaultProfiles
		// 里的标准名。否则 multi-agent 路径 Router 选 deepseek-v4-pro（token 无权）
		// → fallback 到标准名 deepseek-v4-flash（也无权）→ 403 死循环。指向
		// cfg.LLMModel 保证任何 tier 失败都能回退到 token 可访问的模型。
		//
		// 必须克隆：DefaultProfiles 返回的是包级共享 *ModelProfile 指针，直接改
		// 其 FallbackModel 会污染 llm.DefaultProfiles() 的全局返回值（其它调用方
		// 如 cost tracker、单测会受影响）。克隆后只改副本。
		if cfg.LLMModel != "" && profile.Name != cfg.LLMModel && profile.FallbackModel != "" {
			cloned := *profile
			cloned.FallbackModel = cfg.LLMModel
			modelRegistry.Register(&cloned)
			continue
		}
		modelRegistry.Register(profile)
	}
	log.Printf("ModelRegistry: loaded %d default profiles", len(llm.DefaultProfiles()))

	// Phase 6 Router: 构建 model router + provider 查找 map。
	//
	// 原因：engine.go:1115 仅在 EngineConfig 携带非 nil 的
	// Router/Registry/Providers 时才激活 Router 代码路径。此前 main.go
	// 虽然构建了 modelRegistry 与 costTracker，但并未接入 EngineConfig，
	// 导致 Phase 6 的动态模型选择 / classifyIntent / model_routed 事件
	// 在 chat 路径中是死代码。我们在启动时构造一次 Router，并在所有
	// chat turn 与 orchestrator 运行之间共享。
	//
	// Mock 安全：classifier 需要一个 Provider 来分类意图。在 mock 模式
	// (LLMUseMock=true) 下绝对不能调用真实 API —— 既会花钱又会破坏
	// 确定性冒烟测试。因此我们复用 engine 同款的 CreateProviderFromConfig
	// 路径构建 classifier（mock 模式下返回 MockProvider），并额外注册
	// 一个 "builtin:router-classifier" mock 脚本，让 classifier 得到
	// 干净的单 token "simple_chat" 回复，而不是误匹配形如用户输入的
	// 对话脚本。真实模式下 classifier 是一个指向 cfg.LLMModel 的真实
	// OpenAI-compatible provider；classifyIntent 以极小（约 10 token）预算
	// 调用其非流式 Chat。
	routerClassifier, errClassifier := llm.CreateProviderFromConfig(cfg, cfg.LLMModel, "router-classifier")
	if errClassifier != nil {
		log.Printf("[Router] failed to create classifier provider (Router will be disabled): %v", errClassifier)
		routerClassifier = nil
	}
	var modelRouter *llm.Router
	routerProviders := map[string]llm.Provider{}
	if routerClassifier != nil {
		// 把配置的默认 model 同时以 provider name 与 model name 两个 key
		// 写入 provider 查找 map —— engine 会先通过 providers[profile.Provider]
		// 解析选中的 profile，再回退到 providers[profile.Name]
		// (engine.go:1141-1147)。
		if p, err := llm.CreateProviderFromConfig(cfg, cfg.LLMModel, ""); err == nil {
			routerProviders[cfg.LLMModel] = p
			// DefaultProfiles 里的模型 provider 都是 "deepseek"；用同一个 key
			// 注册同一个 provider，使 profile.Provider 查找能命中。
			routerProviders["deepseek"] = p
		}
		// 在 mock 模式下注册 classifier mock 脚本，使 classifyIntent
		// 确定性地返回一个合法的 intent token。真实模式下该脚本不会被用到
		// （真实 provider 不读 store）。
		if cfg.LLMUseMock {
			clsScript := llm.MockScript{
				ID:         "builtin:router-classifier",
				CaseID:     "router-classifier",
				Priority:   1000,
				MatchInput: []string{"classify", "category", "intent"},
				Responses: []llm.MockResponse{
					{Type: llm.MockResponseText, Content: "simple_chat"},
				},
			}
			_, _ = llm.DefaultMockStore.Save(clsScript)
		}
		modelRouter = llm.NewRouter(modelRegistry, routerClassifier)
		log.Printf("[Router] enabled (classifier=%s, mock=%t)", routerClassifier.Name(), cfg.LLMUseMock)
	} else {
		log.Printf("[Router] disabled (no classifier provider)")
	}

	// 在创建 dispatcher / tool registry 之前初始化 multi-agent orchestrator，
	// 因为 dispatcher 依赖它。
	orch := orchestrator.New(hub, cfg, nil, persist, nil, nil, modelRouter, modelRegistry, routerProviders)

	// 用内置 tool 与 leader dispatch tool 初始化 tool registry。
	toolRegistry := tool.NewRegistry()
	dispatcher := &orchestratorDispatcher{orch: orch}
	resolveApproval := func(approvalID string, approved bool, reason string) error {
		return runtime.ResolveDelegatedApproval(runtime.DelegatedApprovalDecision{
			ApprovalID: approvalID,
			Approved:   approved,
			Reason:     reason,
		})
	}
	tool.RegisterBuiltinsWithDispatcherAndLeaderTools(toolRegistry, dispatcher, leaderDispatchEnabled.Load, resolveApproval)

	// Phase web_search: 始终用配置好的实例替换占位的 core/web_search。
	// DuckDuckGo 作为零 API key 的兜底方案，即使没配置任何 API provider，
	// 该 tool 也能用。
	webSearchCfg := tool.WebSearchConfig{
		Provider:        cfg.WebSearchProvider,
		DisableDDG:      cfg.WebSearchDisableDDG,
		EnableExa:       cfg.WebSearchEnableExa,
		EnableParallel:  cfg.WebSearchEnableParallel,
		ExaAPIKey:       cfg.WebSearchExaAPIKey,
		ParallelAPIKey:  cfg.WebSearchParallelAPIKey,
		EnableBing:      cfg.WebSearchEnableBing,
		BingAPIKey:      cfg.WebSearchBingAPIKey,
		BingEndpoint:    cfg.WebSearchBingEndpoint,
		EnableGoogle:    cfg.WebSearchEnableGoogle,
		GoogleAPIKey:    cfg.WebSearchGoogleAPIKey,
		GoogleCX:        cfg.WebSearchGoogleCX,
		GoogleEndpoint:  cfg.WebSearchGoogleEndpoint,
		EnableTavily:    cfg.WebSearchEnableTavily,
		TavilyAPIKey:    cfg.WebSearchTavilyAPIKey,
		TavilyEndpoint:  cfg.WebSearchTavilyEndpoint,
		TavilySearchDepth: cfg.WebSearchTavilySearchDepth,
		TavilyIncludeAnswer: cfg.WebSearchTavilyIncludeAnswer,
		EnableBrave:     cfg.WebSearchEnableBrave,
		BraveAPIKey:     cfg.WebSearchBraveAPIKey,
		BraveEndpoint:   cfg.WebSearchBraveEndpoint,
		EnableKimiSearch: cfg.WebSearchEnableKimiSearch,
		EnableGlmSearch: cfg.WebSearchEnableGlmSearch,
		UserAgent:       fmt.Sprintf("multi-agent-platform/%s", version.Version),
	}
	toolRegistry.Unregister("core/web_search")
	toolRegistry.Register(tool.NewWebSearchTool(webSearchCfg))
	observability.DefaultLogger.Info("web_search", "tool wired", map[string]any{
		"provider":        webSearchCfg.Provider,
		"enable_exa":      webSearchCfg.EnableExa,
		"enable_parallel": webSearchCfg.EnableParallel,
		"enable_bing":     webSearchCfg.EnableBing,
		"enable_google":   webSearchCfg.EnableGoogle,
		"enable_tavily":   webSearchCfg.EnableTavily,
		"enable_brave":    webSearchCfg.EnableBrave,
		"fallback":        "duckduckgo",
	})

	// Phase MCP: 初始化 MCP manager 并加载静态 + 持久化的 server。
	// 静态配置来自 MCP_SERVERS；动态 server 存放在 mcp_servers 表中，
	// 可在进程重启后保留。未来 marketplace 安装会复用同一 manager 与持久化层。
	mcpManager := mcp.NewManager(toolRegistry, mcp.DefaultRepository())
	mcpManager.SetChangeNotifier(func(action, serverID string) {
		hub.SendEvent(event.NewEvent(event.EventMcpToolsChanged, "", "server", 0, map[string]any{
			"action":       action,
			"server_id":    serverID,
			"server_count": len(mcpManager.ListServers()),
			"tool_count":   len(toolRegistry.List()),
		}))
	})

	// 注册内置的默认 static market，让前端无需任何外部 marketplace 配置即可
	// 浏览并安装示例 MCP server。
	//
	// 内置示例市场的 stdio 服务器脚本用的是相对路径（如 examples/mcp/time/...）。
	// 当 server 从非项目根目录（如 bin/）启动时，子进程的 cwd 找不到脚本，
	// 会在 initialize 阶段以 stdout EOF 退出。这里探测并注入项目根目录，
	// 让 ResolveConfig 把相对路径解析为绝对路径，从根上消除 cwd 依赖。
	if root := marketplace.DetectProjectRoot(); root != "" {
		marketplace.SetProjectRoot(root)
		log.Printf("MCP marketplace: project root = %s", root)
	} else {
		observability.DefaultLogger.Warn("mcp", "could not detect project root; built-in stdio servers may fail if launched from a different cwd", nil)
	}
	if defaultMarket, err := marketplace.DefaultStaticProvider(); err == nil {
		mcpManager.RegisterMarket(defaultMarket)
		log.Printf("MCP marketplace: registered %s (%s)", defaultMarket.Name(), defaultMarket.DisplayName())
	} else {
		observability.DefaultLogger.Warn("mcp", "failed to load default static market", map[string]any{"error": err.Error()})
	}

	// 注册通过 MCP_MARKETS 配置的远程 MCP market。
	for _, m := range cfg.MCPMarkets {
		if m.URL == "" {
			observability.DefaultLogger.Warn("mcp", "skipping remote market with empty URL", map[string]any{"name": m.Name})
			continue
		}
		provider, err := marketplace.NewURLProvider(m.URL, m.Name)
		if err != nil {
			observability.DefaultLogger.Warn("mcp", "failed to load remote market", map[string]any{"name": m.Name, "url": m.URL, "error": err.Error()})
			continue
		}
		mcpManager.RegisterMarket(provider)
		log.Printf("MCP marketplace: registered remote %s (%s) from %s", provider.Name(), provider.DisplayName(), m.URL)
	}
	mcpCtx, mcpCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer mcpCancel()
	if err := mcpManager.LoadStaticServers(mcpCtx, cfg.MCPServers); err != nil {
		observability.DefaultLogger.Warn("mcp", "failed to load static servers", map[string]any{"error": err.Error()})
	}
	if err := mcpManager.LoadDBServers(mcpCtx); err != nil {
		observability.DefaultLogger.Warn("mcp", "failed to load db servers", map[string]any{"error": err.Error()})
	}

	// Phase 7: 在启动 context 超时前安装配置好的 marketplace package。
	// 单个失败只记录 warning，不阻止 server 启动，因为 package 可能依赖
	// 某些外部命令，而这些命令不一定在每个环境里都存在。
	for _, entry := range cfg.MCPPreinstall {
		_, installed, err := mcpManager.InstallFromMarketIfMissing(mcpCtx, entry.Market, entry.Package)
		if err != nil {
			observability.DefaultLogger.Warn("mcp", "preinstall failed", map[string]any{
				"market":  entry.Market,
				"package": entry.Package,
				"error":   err.Error(),
			})
			continue
		}
		if installed {
			log.Printf("MCP preinstall: installed %s", entry.String())
		} else {
			log.Printf("MCP preinstall: %s already present, skipped", entry.String())
		}
	}

	log.Printf("MCP: %d server(s) configured, %d tool(s) available", len(mcpManager.ListServers()), len(toolRegistry.List()))
	defer mcpManager.Close()

	// Phase 5: run_shell tool 的 Docker sandbox。
	// 启动时检查 Docker 可用性。若可用，把 run_shell tool 包装成
	// SandboxedShellTool。若不可用，记录 warning 并使用直接执行。
	sandboxCfg := tool.DefaultSandboxConfig()
	sandbox := tool.NewSandboxExecutor(sandboxCfg)
	if sandbox.IsAvailable() {
		log.Println("Docker sandbox: enabled — run_shell executes in isolated containers")
		// 用沙箱版本替换内置 run_shell。
		// 先反注册原始 run_shell tool。
		toolRegistry.Unregister("run_shell")
		// 注册沙箱版本，并以原始版本作为兜底。
		sandboxedShell := tool.NewSandboxedShellTool(sandbox, tool.NewRunShellTool())
		toolRegistry.Register(sandboxedShell)
	} else {
		log.Println("Docker sandbox: disabled — Docker not available, using direct execution")
	}
	// Phase 5 预览：按配置为 execute_program 启用沙箱执行。
	// 默认仍是本地执行，以免影响既有部署。
	if cfg.EnableSandbox {
		tool.SetDefaultRunner(tool.NewDockerRunner(cfg.SandboxImage))
		log.Printf("execute_program: sandbox enabled (image=%s)", cfg.SandboxImage)
	} else {
		log.Println("execute_program: local execution")
	}

	log.Printf("Registered %d built-in tools (dispatch_sub_agent enabled for leader)", len(toolRegistry.List()))

	// Phase skill: 初始化 Skill 子系统。
	// 三件套：Registry（内存）、Store（SQLite 持久化）、Loader（启动期加载）。
	// LoadAll 先注册 DefaultBuiltins，再把数据库中持久化的 skill 覆盖进 registry，
	// 保证用户创建的 local_db skill 不会被内置版本"压回"。
	var skillRegistry *skill.Registry
	var skillStore *skill.Store
	var skillLoader *skill.Loader
	if db.DB != nil {
		skillRegistry = skill.NewRegistry()
		skillStore = skill.NewStore(db.DB)
		skillLoader = skill.NewLoader(skillStore, skillRegistry)
		if err := skillLoader.LoadAll(); err != nil {
			observability.DefaultLogger.Warn("skill", "failed to load skills", map[string]any{"error": err.Error()})
		} else {
			log.Printf("Skill subsystem: loaded %d skill(s) into registry", len(skillRegistry.List(nil)))
		}
		// 注册 skill 管理工具（create_local / delete_local / list），让 Agent 也能操作 skill。
		toolRegistry.Register(skill.NewSkillCreateLocalTool(skillStore, skillRegistry))
		toolRegistry.Register(skill.NewSkillDeleteLocalTool(skillStore, skillRegistry))
		toolRegistry.Register(skill.NewSkillListTool(skillRegistry))
	} else {
		// DB 未初始化时仍提供一个空 registry，避免后续 nil 解引用。
		skillRegistry = skill.NewRegistry()
		skillLoader = skill.NewLoader(nil, skillRegistry)
		_ = skillLoader.LoadAll()
		log.Printf("Skill subsystem: disabled (no database)")
	}
	// 把 registry 提升为包级变量，让 runAgentLoopWithTurn / runAgentLoop 等闭包
	// 可以直接读取当前已启用的 skill 列表并注入 EngineConfig。
	globalSkillRegistry = skillRegistry

	// Phase 5: 用于 agent 间通信的 AgentBus。
	// AgentBus 在所有 agent 之间共享，允许 agent 在执行期间互相发送消息。
	agentBus := orchestrator.NewAgentBus()
	agentBusAdapter := orchestrator.NewAgentBusAdapter(agentBus)

	// Phase 7-B: 把每条 AgentBus 消息持久化到 SQLite，让前端能通过
	// GET /api/tasks/:id/agent-messages 拉取完整消息历史。
	// persistFn 在 SendMessage 内部异步触发，因此消息路由不会被存储 I/O 阻塞。
	if persist != nil {
		agentBus.SetPersistFn(func(msg orchestrator.AgentMessage) error {
			return db.InsertAgentMessage(db.AgentBusMessage{
				TaskID:        msg.Metadata["task_id"],
				SubTaskID:     msg.SubTaskID,
				FromSubTaskID: msg.FromSubTaskID,
				FromAgentID:   msg.FromAgentID,
				ToAgentID:     msg.ToAgentID,
				Type:          msg.Type,
				Content:       msg.Content,
				Metadata:      msg.Metadata,
			})
		})
		log.Println("AgentBus: persistence enabled (agent_messages table)")
	}

	// Phase 5: 用于崩溃后任务恢复的 CheckpointManager。
	// 每次 ReAct loop 迭代结束都会保存 checkpoint。
	checkpointMgr := runtime.NewCheckpointManager("data/checkpoints")
	log.Println("CheckpointManager: initialized (data/checkpoints)")

	// 把共享依赖回填给 orchestrator，确保子 agent 能使用同一套工具、AgentBus 与持久化。
	orch.SetTools(toolRegistry)
	orch.SetAgentBus(agentBusAdapter)
	orch.SetPersistence(persist)

	// WebSocket 入口
	http.HandleFunc("/ws", ws.ServeWS(hub))

	// 把原始的 /api/tasks POST handler 保存为闭包，便于在精确的 /api/tasks
	// （以及历史上的 /api/tasks/）两处复用。
	var handleTasksRoot func(http.ResponseWriter, *http.Request)
	_ = handleTasksRoot // 避免注册位置移动后出现"声明未使用"错误

	// API: 启动一个真实 Agent Loop 的 chat 任务、列出任务、获取任务详情、
	// 拉取 context window 快照、创建新任务。
	//
	// 我们把 "/api/tasks/" 注册在 "/api/tasks" 之前，以便子资源路径
	// （如 /api/tasks/:id/context_window）能被更具体的 handler 匹配。
	// Go 的 ServeMux 会先匹配精确前缀，但旧的合并 handler 依赖 root
	// handler 内部检查 r.URL.Path，在 SPA fallback 改动后嵌套路径不能
	// 被可靠路由，因此这里显式分注册。
	http.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
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
			handleGetTaskContextWindow(w, r, id)
			return
		}

		// Phase 7-B: GET /api/tasks/:id/agent-messages —— 任务的 AgentBus 历史。
		if strings.HasSuffix(path, "/agent-messages") {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			id := strings.TrimSuffix(path, "/agent-messages")
			handleGetAgentMessages(w, r, id)
			return
		}

		// GET /api/tasks/:id —— 单个任务详情
		if r.Method == http.MethodGet {
			r.URL.RawQuery = "id=" + path
			handleGetTask(w, r)
			return
		}
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
	})

	http.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		// 精确的 /api/tasks （或 /api/tasks/）是根入口。
		if r.URL.Path == "/api/tasks" || r.URL.Path == "/api/tasks/" {
			// GET /api/tasks —— 列出最近任务，或通过 ?id=xxx 获取单个任务。
			if r.Method == http.MethodGet {
				if r.URL.Query().Get("id") != "" {
					handleGetTask(w, r)
					return
				}
				handleListTasks(w, r)
				return
			}
			handleTasksRoot(w, r)
			return
		}

		// 其它未处理的路径一律返回 404。
		http.Error(w, "task ID required", http.StatusNotFound)
	})

	handleTasksRoot = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		// lookupCase 按 ID 解析用例：优先走 caseService（同时覆盖内置用例与
		// SQLite 持久化的自定义用例），当 caseService 不可用时退回只含内置用例的
		// cases.Get。自定义用例仅存于数据库，cases.Get 无法命中，会令请求丢失
		// case 的 default_input / system_prompt / max_steps 兜底，最终以
		// "input is required for chat action" 400 返回。
		lookupCase := func(caseID string) *cases.Case {
			if caseID == "" {
				return nil
			}
			if caseService != nil {
				c, err := caseService.Get(caseID)
				if err != nil || c == nil {
					return nil
				}
				return c
			}
			return cases.Get(caseID)
		}

		var req struct {
			Action         string                   `json:"action"`
			AgentID        string                   `json:"agent_id"`
			Input          string                   `json:"input"`
			SystemPrompt   string                   `json:"system_prompt"`
			CaseType       string                   `json:"case_type"`
			MaxSteps       int                      `json:"max_steps"`
			TimeoutSeconds int                      `json:"timeout_seconds"`
			SessionID      string                   `json:"session_id"`
			Agents         []orchestrator.AgentSpec `json:"agents"`
			// TaskContract 可选覆盖项 —— 大于 0 / 非空时覆盖默认
			// （或 case 提供）的 contract，让前端能驱动 PolicyChain。
			Scope         string   `json:"scope"`
			AllowedTools  []string `json:"allowed_tools"`
			TokenBudget   int      `json:"token_budget"`
			CostBudgetUSD float64  `json:"cost_budget_usd"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var contract harness.TaskContract
		caseID := r.URL.Query().Get("case")
		if caseID != "" {
			if c := lookupCase(caseID); c != nil {
				contract = c.Contract
				// 请求未提供 input 时使用 case 的默认 input
				if req.Input == "" {
					req.Input = c.DefaultInput
				}
				// 请求未提供 system prompt 时使用 case 的 system prompt
				if req.SystemPrompt == "" {
					req.SystemPrompt = c.SystemPrompt
				}
				// Case 的 contract 自带 MaxSteps/TimeoutSeconds 默认值。
				// 客户端未覆盖时从 case 继承，避免下方校验拒绝合法的
				// case 请求。
				if req.MaxSteps <= 0 {
					req.MaxSteps = c.Contract.MaxSteps
				}
				if req.TimeoutSeconds <= 0 {
					req.TimeoutSeconds = c.Contract.TimeoutSeconds
				}
			}
		}

		// 按服务端强制 contract 限制校验请求 input 长度。
		if len(req.Input) > cfg.ContractLimits.MaxInputLength {
			http.Error(w, fmt.Sprintf("input length exceeds maximum of %d", cfg.ContractLimits.MaxInputLength), http.StatusBadRequest)
			return
		}

		if req.MaxSteps < 1 {
			// 未显式指定 max_steps，也没有 case 上下文 —— 回退到服务端默认值。
			req.MaxSteps = harness.DefaultContract(req.Input).MaxSteps
		}
		if req.MaxSteps > cfg.ContractLimits.MaxSteps {
			req.MaxSteps = cfg.ContractLimits.MaxSteps
		}
		if req.TimeoutSeconds < 0 {
			http.Error(w, "timeout_seconds must be >= 0", http.StatusBadRequest)
			return
		}
		if req.TimeoutSeconds > cfg.ContractLimits.MaxTimeoutSeconds {
			http.Error(w, fmt.Sprintf("timeout_seconds exceeds maximum of %d", cfg.ContractLimits.MaxTimeoutSeconds), http.StatusBadRequest)
			return
		}

		switch req.Action {

		case "multi-agent":
			// req.MaxSteps 已在上方校验并钳制。
			// 按服务端限制校验显式指定的子 agent 数量。
			if len(req.Agents) > cfg.ContractLimits.MaxSubAgents {
				http.Error(w, fmt.Sprintf("agents count exceeds maximum of %d", cfg.ContractLimits.MaxSubAgents), http.StatusBadRequest)
				return
			}

			// Phase 7-H：multi-agent 改为 leader-agent 驱动。
			// 1) 解析/生成 session 与 root task；2）启动一个 Leader Agent；
			// 3) Leader 通过 dispatch_sub_agent 工具决定派哪些子 agent。
			// 若请求体显式提供 agents，则把强制工作流写进 leader 的输入，
			// 保证前端既有行为仍能运行这些 agent。

			// 先生成 session/root task，便于后续子任务树定位。
			sessionID, taskID, err := resolveSession(req.SessionID, req.Input, persist)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if persist != nil {
				persist.SaveTaskMeta(taskID, sessionID, "", true)
				if sessionID != "" {
					sess, err := db.QuerySessionByID(sessionID)
					if err == nil && sess.RootTaskID == "" {
						db.UpdateSession(sessionID, taskID, sess.Status, sess.UserInput)
					}
				}
			}

			hub.SendEvent(event.NewEvent("task_started", taskID, "leader", 0, map[string]any{
				"task_id":    taskID,
				"session_id": sessionID,
				"input":      req.Input,
				"mode":       "leader-driven",
			}))

			// 组装 leader 的输入：如果请求给出了显式 agents，强制要求使用 dispatch_sub_agent。
			leaderInput := req.Input
			if len(req.Agents) > 0 {
				strategy := "parallel"
				for i := range req.Agents {
					if req.Agents[i].Name == "" {
						req.Agents[i].Name = req.Agents[i].AgentID
					}
				}
				workflowJSON, _ := json.Marshal(req.Agents)
				leaderInput += fmt.Sprintf("\n\n[MANDATORY WORKFLOW] You must use the dispatch_sub_agent tool with strategy=%q and agents=%s to complete this task.", strategy, string(workflowJSON))
			}

			go func() {
				// Leader 视情况调用 dispatch_sub_agent；若未调用，将作为普通 chat agent 返回答案。
				leaderSystemPrompt := "You are the Leader agent. You coordinate sub-agents to solve complex tasks. Use the dispatch_sub_agent tool when you need to delegate work to multiple sub-agents. Each sub-agent runs independently; their results are returned as observations. If the task is simple enough, you may answer directly."
				if cfg.LLMUseMock {
					// mock 模式下简化 prompt，避免 mock provider 被过长文本干扰。
					leaderSystemPrompt = "You are the Leader agent. Use dispatch_sub_agent when delegation is needed."
				}
				runAgentLoopWithTurn(hub, taskID, "leader", leaderSystemPrompt, leaderInput, cfg, toolRegistry, persist, harness.DefaultContract(leaderInput), sessionID, approvalHandler, "", agentBusAdapter, checkpointMgr, 0, "", "", costRepo, modelRegistry, modelRouter, routerProviders, caseService)
				removeCancel(taskID, "leader")
				removeEngine(taskID, "leader")
				db.UpdateSessionStatus(sessionID, deriveSessionStatus(sessionID))
				log.Printf("[Multi-Agent] Leader task %s completed", taskID)
			}()

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"session_id":  sessionID,
				"task_id":     taskID,
				"agent_count": 1,
				"agent_ids":   []string{"leader"},
				"status":      "started",
			})
		case "stream-demo":
			sessionID, taskID, err := resolveSession(req.SessionID, "", persist)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			go streamTask(hub, taskID)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"session_id": sessionID,
				"task_id":    taskID,
			})

		case "chat":
			// 检查是否指定了预设 case —— 在校验请求前加载其 contract、
			// 默认 input 与 system prompt。
			// req.MaxSteps 已在上方校验并钳制。
			caseID := r.URL.Query().Get("case")
			if caseID != "" {
				if c := lookupCase(caseID); c != nil {
					contract = c.Contract
					// 请求未提供 input 时使用 case 的默认 input
					if req.Input == "" {
						req.Input = c.DefaultInput
					}
					// 请求未提供 system prompt 时使用 case 的 system prompt
					if req.SystemPrompt == "" {
						req.SystemPrompt = c.SystemPrompt
					}
					// 客户端未覆盖时继承 case 级别的 step/timeout 默认值，
					// 否则下方"步数必须为正"的校验会拒绝 case 运行。
					if req.MaxSteps <= 0 {
						req.MaxSteps = c.Contract.MaxSteps
					}
					if req.TimeoutSeconds <= 0 {
						req.TimeoutSeconds = c.Contract.TimeoutSeconds
					}
				}
			}

			if req.Input == "" {

				http.Error(w, "input is required for chat action", http.StatusBadRequest)
				return
			}

			agentID := req.AgentID
			if agentID == "" {
				agentID = "agent_default"
			}

			systemPrompt := req.SystemPrompt
			if systemPrompt == "" {
				systemPrompt = "You are a helpful AI assistant with access to tools. " +
					"When you need to run commands, read files, or write files, use the available tools. " +
					"Always explain your reasoning before using tools. " +
					"After using tools, analyze the results and continue until the task is complete."
			}

			if contract.Goal == "" {
				contract = harness.DefaultContract(req.Input)
			}
			// 请求显式提供时覆盖 MaxSteps (>0)
			if req.MaxSteps > 0 {
				contract.MaxSteps = req.MaxSteps
			}
			// 请求显式提供时覆盖 timeout (>0)。
			if req.TimeoutSeconds > 0 {
				contract.TimeoutSeconds = req.TimeoutSeconds
			}
			// 请求体提供时覆盖 TaskContract 字段 ——
			// 让前端能驱动 PolicyChain（scope、tools、预算、timeout）。
			if req.Scope != "" {
				if !isAllowedScope(req.Scope, cfg.ContractLimits.Scopes) {
					http.Error(w, fmt.Sprintf("scope %q is not allowed", req.Scope), http.StatusBadRequest)
					return
				}
				contract.Scope = req.Scope
			}
			if len(req.AllowedTools) > 0 {
				contract.AllowedTools = req.AllowedTools
			} else if tools := agentAllowedTools(agentID); len(tools) > 0 {
				contract.AllowedTools = tools
			}
			if req.TokenBudget > 0 {
				contract.TokenBudget = req.TokenBudget
			}
			if req.CostBudgetUSD > 0 {
				contract.CostBudgetUSD = req.CostBudgetUSD
			}

			// 从过往经验为本任务构建 Working Memory
			workingMemory := ""
			if wm, err := memRecall.BuildWorkingMemory("default", req.SessionID, req.Input, 3); err == nil {
				workingMemory = memRecall.FormatForSystemPrompt(wm)
			}

			taskID := newTaskID()
			// Phase 7-C: 为本任务创建 root trace context 并透传给 Engine。
			rootTraceCtx := tracer.StartRoot(taskID, "task")
			traceRegistry.Store(taskID, rootTraceCtx)
			go runAgentLoop(hub, taskID, agentID, systemPrompt, req.Input, cfg, toolRegistry, persist, contract, req.SessionID, approvalHandler, workingMemory, agentBusAdapter, checkpointMgr, caseID, costRepo, modelRegistry, modelRouter, routerProviders, caseService, rootTraceCtx)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"session_id": req.SessionID,
				"task_id":    taskID,
				"agent_id":   agentID,
				"action":     "chat",
			})

		default:
			http.Error(w, "unknown action (use 'stream-demo' or 'chat')", http.StatusBadRequest)
		}
	}

	// Phase 7-C: 可观测性 REST endpoint。
	http.HandleFunc("/api/audit", handleAudit)
	http.HandleFunc("/api/traces", handleTraces)
	http.HandleFunc("/api/replay/tasks/", handleReplay)
	http.HandleFunc("/api/replay/events", func(w http.ResponseWriter, r *http.Request) {
		handleReplayEvents(w, r, hub)
	})

	// Contract 限制 endpoint：暴露服务端强制的 task contract 边界。
	// GET /api/contract-limits
	http.HandleFunc("/api/contract-limits", handleContractLimits(cfg))

	// Agent CRUD API
	http.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		// Agent 写操作仅 admin 可执行。
		if r.Method != http.MethodGet && !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
			return
		}
		handleAgents(w, r)
	})
	http.HandleFunc("/api/agents/", func(w http.ResponseWriter, r *http.Request) {
		if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
			return
		}
		handleAgentByID(w, r)
	})

	// Session API
	http.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		handleSessions(w, r)
	})
	http.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// POST /api/sessions/{id}/chat —— 一个 session 内的多轮对话
		if strings.HasSuffix(path, "/chat") {
			handleSessionChat(w, r, hub, cfg, toolRegistry, persist, approvalHandler, memRecall, agentBusAdapter, checkpointMgr, memDB, costRepo, modelRegistry, modelRouter, routerProviders, caseService)
			return
		}
		// GET /api/sessions/{id}/messages —— session 消息历史
		if strings.HasSuffix(path, "/messages") {
			sessionID := strings.TrimSuffix(path, "/messages")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			handleSessionMessages(w, r, sessionID)
			return
		}
		// GET /api/sessions/{id}/workspace/dir —— 返回 workspace 路径与 auto 标志
		if strings.HasSuffix(path, "/workspace/dir") {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			sessionID := strings.TrimSuffix(path, "/workspace/dir")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			sess, err := db.QuerySessionByID(sessionID)
			if err != nil {
				http.Error(w, "session not found: "+err.Error(), http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"session_id":     sessionID,
				"workspace_dir":  sess.WorkspaceDir,
				"workspace_auto": sess.WorkspaceAuto,
			})
			return
		}
		// GET /api/sessions/{id}/workspace-browse —— 供前端使用的 workspace 浏览信息
		if strings.HasSuffix(path, "/workspace-browse") {
			sessionID := strings.TrimSuffix(path, "/workspace-browse")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			handleSessionWorkspaceBrowse(w, r, sessionID)
			return
		}
		// GET /api/sessions/{id}/workspace-tree?path=<rel> — 列出 session workspace 下
		// 指定相对子目录的文件树（单层 + 目录可递归展开）。仅限本 session 工作目录，
		// 服务端做 path traversal 校验。供 UI v2 右侧文件浏览器使用。
		if strings.HasSuffix(path, "/workspace-tree") {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			sessionID := strings.TrimSuffix(path, "/workspace-tree")
			sessionID = strings.TrimPrefix(sessionID, "/api/sessions/")
			handleSessionWorkspaceTree(w, r, sessionID)
			return
		}
		handleSessionByID(w, r)
	})

	// Project API
	http.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		handleProjects(w, r)
	})
	http.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		handleProjectByID(w, r)
	})

	// Phase 6-D: Cost 查询 API（task/session/project/daily 聚合）。
	// 数据从 CostRepository 读取，因此包含已持久化的记录。
	http.HandleFunc("/api/costs", func(w http.ResponseWriter, r *http.Request) {
		handleCostQuery(w, r, costRepo)
	})

	// Phase 6-D: Health check endpoint（JSON，检查 DB + WS hub）。
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		status := map[string]any{
			"status":    "ok",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"checks": map[string]any{
				"websocket": map[string]string{"status": "ok"},
			},
		}
		dbStatus := "ok"
		if db.DB != nil {
			if err := db.DB.Ping(); err != nil {
				dbStatus = "error: " + err.Error()
				status["status"] = "degraded"
			}
		} else {
			dbStatus = "not initialized"
		}
		status["checks"].(map[string]any)["database"] = map[string]string{"status": dbStatus}

		w.Header().Set("Content-Type", "application/json")
		if status["status"] == "ok" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(status)
	})

	// Phase 6-D: Prometheus 文本格式的 Metrics endpoint。
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, observability.DefaultMetrics.PrometheusText())
	})

	// 保留旧的纯文本 health check 以向后兼容。
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// Auth API endpoint（API key 管理）
	if authAPI == nil {
		authAPI = auth.NewAuthAPI(authStore)
	}
	if authStore != nil {
		authAPI.RegisterRoutes(http.DefaultServeMux)
	}

	// Mock 脚本管理 API（Phase 6 mock provider）。
	// RegisterMockRoutes 在上面的 mock store 初始化之后调用
	// （见 "Phase mock" 块）。该 store 在管理 API 与 MockProvider 之间
	// 通过 llm.DefaultMockStore 共享。
	RegisterMockRoutes(http.DefaultServeMux, mockStore, llm.BuiltinMockScripts())

	// 模型价格管理 API —— 查看/更新 ModelRegistry 价格。
	// GET  /api/models/prices         —— 列出所有 profile 的 InputPrice/OutputPrice
	// PUT  /api/models/prices/{model} —— 更新某模型的价格（仅运行时，重启后重置）
	// 该 registry 与接入 EngineConfig 和 CostTracker 的是同一个共享实例，
	// 因此这里的价格改动对后续所有 cost record 立即生效。
	RegisterModelPriceRoutes(http.DefaultServeMux, modelRegistry)

	// Version API：从 version.txt 返回当前版本号
	http.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(map[string]string{
			"version": version.Version,
		})
	})

	// Session workspace 静态文件服务 —— /s/{session_id}/...
	// 让前端一键访问生成的 HTML/图片等资源。
	http.HandleFunc("/s/", func(w http.ResponseWriter, r *http.Request) {
		// 从 /s/{session_id}/... 中提取 session_id
		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/s/"), "/")
		if len(pathParts) == 0 || pathParts[0] == "" {
			http.Error(w, "session_id required", http.StatusBadRequest)
			return
		}
		sessionID := pathParts[0]

		// 查 session 以验证存在并取回 workspace_dir
		sess, err := db.QuerySessionByID(sessionID)
		if err != nil || sess.WorkspaceDir == "" {
			http.Error(w, "session not found or no workspace", http.StatusNotFound)
			return
		}

		// 安全：确保解析后的路径仍位于 workspace dir 内
		requestPath := filepath.Join(sess.WorkspaceDir, filepath.Join(pathParts[1:]...))
		cleanPath := filepath.Clean(requestPath)
		workspaceRoot := filepath.Clean(sess.WorkspaceDir)
		if !strings.HasPrefix(cleanPath, workspaceRoot) {
			http.Error(w, "path traversal detected", http.StatusForbidden)
			return
		}

		// 提供文件服务
		http.ServeFile(w, r, cleanPath)
	})

	// Cases API：对预设与自定义 case 的完整 CRUD。
	// GET /api/cases —— 列出所有 case，支持按 tag/category 过滤
	// POST /api/cases —— 创建自定义 case
	http.HandleFunc("/api/cases", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if caseService == nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(cases.All())
				return
			}
			handleListCases(w, r, caseService)
		case http.MethodPost:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			handleCreateCase(w, r, caseService)
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})
	// GET /api/cases/{id} —— 单个 case
	// PUT /api/cases/{id} —— 更新自定义 case
	// DELETE /api/cases/{id} —— 删除自定义 case
	// GET /api/cases/{id}/evaluations/{task_id} —— task+case 配对的评估结果
	http.HandleFunc("/api/cases/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/cases/")
		if path == "" || path == "/" {
			http.Error(w, "case ID required", http.StatusBadRequest)
			return
		}

		parts := strings.Split(path, "/")
		id := parts[0]

		// GET /api/cases/{id}/evaluations/{task_id}
		if len(parts) >= 2 && parts[1] == "evaluations" {
			handleGetCaseEvaluation(w, r, id, caseService)
			return
		}

		// 既有的 /api/cases/{id} GET/PUT/DELETE
		if len(parts) > 1 {
			http.Error(w, "invalid case resource", http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if caseService == nil {
				c := cases.Get(id)
				if c == nil {
					http.Error(w, "case not found", http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(c)
				return
			}
			handleGetCase(w, r, id, caseService)
		case http.MethodPut:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			handleUpdateCase(w, r, id, caseService)
		case http.MethodDelete:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			handleDeleteCase(w, r, id, caseService)
		default:
			http.Error(w, "GET, PUT, or DELETE only", http.StatusMethodNotAllowed)
		}
	})

	// Run Case 代理：POST /api/run-case
	// CaseCard 前端使用的薄代理。委托给与 POST /api/tasks 相同的
	// chat-action 逻辑，case_id 从请求体中提取。
	http.HandleFunc("/api/run-case", func(w http.ResponseWriter, r *http.Request) {
		handleRunCase(w, r, hub, cfg, toolRegistry, persist, approvalHandler, memRecall, agentBusAdapter, checkpointMgr, memDB, costRepo, modelRegistry, modelRouter, routerProviders, caseService)
	})

	// MCP 管理 API：动态 add / enable / disable / remove。
	registerMCPRoutes(http.DefaultServeMux, mcpManager)

	// Phase skill: 注册 Skill REST API。
	// 路由实现集中在 api_skill.go，hub 用于广播 skill 状态变化事件。
	registerSkillRoutes(http.DefaultServeMux, hub, skillStore, skillRegistry)

	// 动态 Tool 注册 API (Phase 2+)
	http.HandleFunc("/api/tools", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			handleRegisterTool(w, r, toolRegistry)
		case http.MethodGet:
			handleListTools(w, r, toolRegistry)
		case http.MethodDelete:
			if !auth.RequireRoleFunc(w, r, auth.RoleAdmin) {
				return
			}
			handleDeleteTool(w, r, toolRegistry)
		default:
			http.Error(w, "GET, POST, or DELETE only", http.StatusMethodNotAllowed)
		}
	})

	// Multi-Agent orchestration endpoint (Phase 4)
	// POST /api/multi-agent —— 并发运行多个 agent
	http.HandleFunc("/api/multi-agent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Input          string                   `json:"input"`
			CaseType       string                   `json:"case_type"`       // "multi_agent"、"code_gen" 或空
			MaxSteps       int                      `json:"max_steps"`       // 覆盖所有 agent 的最大步数
			TimeoutSeconds int                      `json:"timeout_seconds"` // 覆盖所有 agent 的超时
			SessionID      string                   `json:"session_id"`
			Agents         []orchestrator.AgentSpec `json:"agents"` // 直接给出的 agent spec（可选）
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// 按服务端强制的 contract 限制校验请求。
		if len(req.Input) > cfg.ContractLimits.MaxInputLength {
			http.Error(w, fmt.Sprintf("input length exceeds maximum of %d", cfg.ContractLimits.MaxInputLength), http.StatusBadRequest)
			return
		}
		if req.MaxSteps < 1 {
			// 未显式指定 max_steps —— 对 multi-agent 请求回退到服务端默认值。
			req.MaxSteps = harness.DefaultContract(req.Input).MaxSteps
		}
		if req.MaxSteps > cfg.ContractLimits.MaxSteps {
			req.MaxSteps = cfg.ContractLimits.MaxSteps
		}
		if req.TimeoutSeconds < 0 {
			http.Error(w, "timeout_seconds must be >= 0", http.StatusBadRequest)
			return
		}
		if req.TimeoutSeconds > cfg.ContractLimits.MaxTimeoutSeconds {
			http.Error(w, fmt.Sprintf("timeout_seconds exceeds maximum of %d", cfg.ContractLimits.MaxTimeoutSeconds), http.StatusBadRequest)
			return
		}
		if len(req.Agents) > cfg.ContractLimits.MaxSubAgents {
			http.Error(w, fmt.Sprintf("agents count exceeds maximum of %d", cfg.ContractLimits.MaxSubAgents), http.StatusBadRequest)
			return
		}

		if req.Input == "" && len(req.Agents) == 0 {
			http.Error(w, "input or agents is required", http.StatusBadRequest)
			return
		}

		// 把任务分解为 agent spec
		var specs []orchestrator.AgentSpec
		strategy := "parallel"
		if len(req.Agents) > 0 {
			specs = enrichAgentSpecAllowedTools(req.Agents)
		} else {
			var decomposer orchestrator.Decomposer
			if cfg.LLMUseMock {
				decomposer = orchestrator.NewTaskDecomposer()
			} else {
				decomposer = orchestrator.NewLLMDecomposer(cfg, routerClassifier)
			}
			result, err := decomposer.Decompose(req.Input, req.CaseType)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			specs = result.Agents
			strategy = result.Strategy
		}

		// 若提供了全局 MaxSteps 覆盖则应用
		if req.MaxSteps > 0 {
			if req.MaxSteps > cfg.ContractLimits.MaxSteps {
				req.MaxSteps = cfg.ContractLimits.MaxSteps
			}
			for i := range specs {
				if specs[i].Contract == nil {
					contract := harness.DefaultContract(specs[i].Input)
					specs[i].Contract = &contract
				}
				specs[i].Contract.MaxSteps = req.MaxSteps
			}
		}

		// 若提供了全局 TimeoutSeconds 覆盖则应用。
		if req.TimeoutSeconds > 0 {
			for i := range specs {
				if specs[i].Contract == nil {
					contract := harness.DefaultContract(specs[i].Input)
					specs[i].Contract = &contract
				}
				specs[i].Contract.TimeoutSeconds = req.TimeoutSeconds
			}
		}

		// 为本次 orchestration 中的所有 agent 构建 Working Memory
		if wm, err := memRecall.BuildWorkingMemory("default", req.SessionID, req.Input, 3); err == nil {
			workingMemory := memRecall.FormatForSystemPrompt(wm)
			for i := range specs {
				specs[i].WorkingMemory = workingMemory
			}
		}

		// 解析或创建 session
		sessionID, taskID, err := resolveSession(req.SessionID, req.Input, persist)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		agentIDs := make([]string, len(specs))
		for i, s := range specs {
			agentIDs[i] = s.AgentID
		}

		// 持久化 orchestrator task
		if persist != nil {
			persist.SaveTask(taskID, req.Input, agentIDs)
			persist.SaveTaskMeta(taskID, sessionID, "", true)
			// 把 root task 绑定到 session，让前端刷新后仍能加载
			if sessionID != "" {
				sess, err := db.QuerySessionByID(sessionID)
				if err == nil && sess.RootTaskID == "" {
					db.UpdateSession(sessionID, taskID, sess.Status, sess.UserInput)
				}
			}
		}

		// 发送 orchestrator task started 事件
		hub.SendEvent(event.NewEvent("task_started", taskID, "orchestrator", 0, map[string]any{
			"task_id":     taskID,
			"session_id":  sessionID,
			"input":       req.Input,
			"agent_ids":   agentIDs,
			"agent_count": len(specs),
			"strategy":    strategy,
		}))

		// 按请求的协调策略启动 agent。
		go func() {
			// Multi-agent orchestration 超时默认 10 分钟。若每个 spec 都有
			// 相同的 TimeoutSeconds 覆盖，则取最小正值作为统一 deadline，
			// 让任务失败可预测；否则回退到硬编码的 10 分钟默认值。
			var timeout time.Duration = 10 * time.Minute
			minTimeout := 0
			for _, s := range specs {
				if s.Contract != nil && s.Contract.TimeoutSeconds > 0 {
					if minTimeout == 0 || s.Contract.TimeoutSeconds < minTimeout {
						minTimeout = s.Contract.TimeoutSeconds
					}
				}
			}
			if minTimeout > 0 {
				timeout = time.Duration(minTimeout) * time.Second
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			storeCancel(taskID, "orchestrator", cancel)
			defer removeCancel(taskID, "orchestrator")
			defer cancel()
			orch.RunBlocking(ctx, taskID, strategy, specs)
			db.UpdateSessionStatus(sessionID, deriveSessionStatus(sessionID))
			log.Printf("[Multi-Agent] Task %s: all agents completed", taskID)
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"session_id":  sessionID,
			"task_id":     taskID,
			"agent_count": len(specs),
			"agent_ids":   agentIDs,
			"max_steps":   req.MaxSteps,
			"status":      "started",
		})
	})

	// Phase 5: 任务恢复的 Checkpoint API endpoint
	// GET /api/checkpoints —— 列出所有可恢复任务
	http.HandleFunc("/api/checkpoints", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		handleListCheckpoints(w, r, checkpointMgr)
	})
	// POST /api/checkpoints/recover —— 从 checkpoint 恢复任务
	http.HandleFunc("/api/checkpoints/recover", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		handleRecoverCheckpoint(w, r, hub, cfg, toolRegistry, persist, approvalHandler, agentBusAdapter, checkpointMgr)
	})

	// Memory API (Phase 6 / Phase 5-B)
	// GET  /api/memories?scope=...&tier=...&type=...&status=...&project=...&limit=...&offset=...
	// POST /api/memories —— 创建 memory
	// GET  /api/memories/{id} —— 获取 memory
	// PUT  /api/memories/{id} —— 更新 memory 的 content/confidence/status
	// DELETE /api/memories/{id} —— 删除 memory
	// PUT  /api/memories/{id}/scope —— 更新 memory scope
	// POST /api/memories/{id}/embed —— 生成并存储 embedding
	// GET  /api/memories/stats —— 项目 memory 统计
	// POST /api/memories/promote —— 手动触发晋升
	// GET  /api/memories/recall?task=xxx&project=default&max=3 —— 召回预览
	memGateway := harness.NewPromotionGate(memDB)
	http.HandleFunc("/api/memories", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListMemories(w, r)
		case http.MethodPost:
			handleCreateMemory(w, r, hub, vectorStore, embedProvider)
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	})
	http.HandleFunc("/api/memories/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/memories/")
		// POST /api/memories/promote —— 手动触发晋升
		if path == "promote" {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", http.StatusMethodNotAllowed)
				return
			}
			handlePromoteMemories(w, r, memGateway)
			return
		}
		// GET /api/memories/recall?task=xxx&project=default&max=3
		if path == "recall" {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			handleRecallPreview(w, r, memRecall)
			return
		}
		// GET /api/memories/stats?project=default
		if path == "stats" {
			if r.Method != http.MethodGet {
				http.Error(w, "GET only", http.StatusMethodNotAllowed)
				return
			}
			handleMemoryStats(w, r)
			return
		}
		// /api/memories/{id}/scope 或 /api/memories/{id} 或 /api/memories/{id}/embed
		parts := strings.Split(path, "/")
		id := parts[0]
		if id == "" {
			http.Error(w, "memory ID required", http.StatusBadRequest)
			return
		}
		switch {
		case len(parts) == 2 && parts[1] == "scope" && r.Method == http.MethodPut:
			handleUpdateMemoryScope(w, r, id)
		case len(parts) == 2 && parts[1] == "embed" && r.Method == http.MethodPost:
			handleMemoryEmbed(w, r, id, hub, vectorStore, embedProvider)
		case len(parts) == 1:
			handleMemoryByID(w, r, id, hub, vectorStore, embedProvider)
		default:
			http.Error(w, "unsupported memory operation", http.StatusMethodNotAllowed)
		}
	})
	// 从嵌入式文件系统提供 Vue SPA（生产模式）。
	// 开发模式下用户可运行 `cd web && npm run dev` 使用 Vite 的 dev server
	// 与 HMR。构建 Go binary 时使用嵌入式 dist/。
	//
	// Phase UI-v2: 同时嵌入 v1 (web/dist) 与 v2 (web/v2/dist)，通过 UI_VERSION
	// 环境变量切换；默认使用 v1，保证现有部署行为不变。
	uiVersion := strings.ToLower(strings.TrimSpace(os.Getenv("UI_VERSION")))
	var activeEmbed embed.FS
	var subDir string
	if uiVersion == "v2" {
		activeEmbed = web.V2Dist
		subDir = "v2/dist"
	} else {
		activeEmbed = web.Dist
		subDir = "dist"
	}
	distFS, err := fs.Sub(activeEmbed, subDir)
	if err != nil {
		log.Printf("Warning: embedded frontend dist not found (version=%s): %v", uiVersion, err)
	} else {
		// 创建一个 file server 来服务嵌入式 dist/ 目录
		fileServer := http.FileServer(http.FS(distFS))

		// SPA fallback：任何未匹配 API 路由或静态文件的请求都返回 index.html
		// （由 Vue Router 处理客户端路由）。
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// API 与 WebSocket 路由由上方注册的各自 handler 处理。
			// 末尾斜杠形式本 handler 看不到（ServeMux 会规范化路径），
			// 但为了清晰与代理兼容性，两种形式都做保护。
			if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws") ||
				r.URL.Path == "/health" || r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
				http.NotFound(w, r)
				return
			}
			if r.URL.Path == "/" || r.URL.Path == "/index.html" || !fileExists(distFS, r.URL.Path) {
				// 为 SPA 客户端路由（如 /agents、/tasks/123）返回 index.html，
				// 但仅当路径未命中 dist/ 中的真实文件时。
				indexFile, err := distFS.Open("index.html")
				if err != nil {
					http.NotFound(w, r)
					return
				}
				defer indexFile.Close()
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				http.ServeContent(w, r, "index.html", time.Time{}, indexFile.(io.ReadSeeker))
				return
			}
			fileServer.ServeHTTP(w, r)
		})
		log.Printf("Frontend embedded: serving from embedded %s/ (UI_VERSION=%s)", subDir, uiVersion)
	}

	requireAuth := os.Getenv("REQUIRE_AUTH") == "true"

	log.Printf("========================================")
	log.Printf("Multi-Agent Platform %s", version.Version)
	log.Printf("========================================")
	log.Printf("Server:      http://localhost:%s", cfg.ServerPort)
	log.Printf("WebSocket:   ws://localhost:%s/ws", cfg.ServerPort)
	log.Printf("API:         http://localhost:%s/api/tasks", cfg.ServerPort)
	log.Printf("Health:      http://localhost:%s/health", cfg.ServerPort)
	log.Printf("LLM:         %s (global mock=%t, model=%s)", cfg.LLMEndpoint, cfg.LLMUseMock, cfg.LLMModel)
	log.Printf("Auth:        %s", map[bool]string{true: "enabled", false: "disabled"}[requireAuth])
	log.Printf("Tools:       %d built-in", len(toolRegistry.List()))
	log.Printf("========================================")

	// 用 auth middleware 包装默认 mux。REQUIRE_AUTH 为 true 时，它保护
	// 改状态的路由和敏感读 endpoint，而公开路由 (/healthz、/metrics、
	// /health) 仍然开放。REQUIRE_AUTH 为 false 时，所有路由都放行，
	// 但会注入 seed user ID。
	handler := auth.NewAuthMiddleware(authStore, authAPI.SeedUserID(), requireAuth, auth.DefaultProtectedRoutes(), auth.DefaultPublicRoutes(), http.DefaultServeMux)

	if err := http.ListenAndServe(":"+cfg.ServerPort, handler); err != nil {
		log.Fatal(err)
	}
}

// orchestratorDispatcher 是 SubAgentDispatcher 在 cmd/server 层的实现。
// 它把 dispatch_sub_agent 工具的调用转发给 orchestrator.RunBlocking。
type orchestratorDispatcher struct {
	orch *orchestrator.Orchestrator
}

// leaderApprovalHandler 把审批请求委托给 supervisor leader Engine。
type leaderApprovalHandler struct {
	leaderSubTaskID string
}

// RequestDelegatedApproval 在 cmd/server 层实现 runtime.ApprovalDelegationHandler。
// 它通过全局 engineRegistry 查找 supervisor leader 的 Engine，在注册表中登记等待
// channel，然后往 leader 发送 AgentBus 审批请求消息，最后等待 leader 调用
// approve/reject_sub_agent_action 工具做出决定。
func (h *leaderApprovalHandler) RequestDelegatedApproval(req runtime.DelegatedApprovalRequest) (bool, bool, error) {
	// 在 engineRegistry 中按 subTaskID 精确查找 supervisor Engine。
	v, ok := engineRegistry.Load(req.SupervisorSubTaskID)
	if !ok {
		log.Printf("[leaderApprovalHandler] supervisor engine not found: %s", req.SupervisorSubTaskID)
		return false, false, fmt.Errorf("supervisor engine not found")
	}
	leaderEngine, ok := v.(*runtime.Engine)
	if !ok {
		return false, false, fmt.Errorf("supervisor engine record invalid")
	}

	ch := make(chan runtime.DelegatedApprovalDecision, 1)
	runtime.RegisterDelegatedApproval(req.ApprovalID, ch)
	defer runtime.UnregisterDelegatedApproval(req.ApprovalID)

	// 通过 leader Engine 的 AgentBus listener 把审批请求作为 user message 注入。
	content, _ := runtime.BuildApprovalDelegationContent(req)
	leaderEngine.SendAgentMessage("approval_request", req.SupervisorSubTaskID, content)

	// 等待 leader 审批决定，带超时回退。
	decision, err := runtime.WaitForDelegatedApproval(req.ApprovalID, 30*time.Second)
	if err != nil {
		log.Printf("[leaderApprovalHandler] 等待 leader 审批超时: %v", err)
		return false, false, err
	}
	return decision.Approved, true, nil
}

// Dispatch 调用 orchestrator 同步运行一组子 agent。
// 在 Phase 7-H 中，leaderSubTaskID 就是 root task ID。
func (d *orchestratorDispatcher) Dispatch(ctx context.Context, leaderSubTaskID, strategy string, agents []tool.SubAgentSpec) ([]tool.SubAgentResult, error) {
	orchSpecs := make([]orchestrator.AgentSpec, len(agents))
	for i, a := range agents {
		orchSpecs[i] = orchestrator.AgentSpec{
			AgentID:      a.AgentID,
			Name:         a.Name,
			SystemPrompt: a.SystemPrompt,
			Input:        a.Input,
			Model:        a.Model,
			AllowedTools: a.AllowedTools,
			OutputTo:     a.OutputTo,
		}
	}
	results := d.orch.RunBlocking(ctx, leaderSubTaskID, strategy, orchSpecs)
	out := make([]tool.SubAgentResult, len(results))
	for i, r := range results {
		out[i] = tool.SubAgentResult{
			AgentID:     r.AgentID,
			Name:        r.Name,
			Status:      r.Status,
			Result:      r.Result,
			TotalTokens: r.TotalTokens,
			Error:       r.Error,
			Duration:    r.Duration,
		}
	}
	return out, nil
}

// agentAllowedTools 从 DB 加载某 agent 配置的 tools。
// 如果 agent 不存在或没有配置 tools，返回 nil，在 Engine 与 PolicyGate
// 中表示"允许所有 tool"。
func agentAllowedTools(agentID string) []string {
	if agentID == "" {
		return nil
	}
	agent, err := db.QueryAgentByID(agentID)
	if err != nil || agent == nil {
		return nil
	}
	return agent.Tools
}

// resolveAllowedTools 返回某任务实际生效的 allowed-tools 列表。
// 请求显式提供的 tool 优先；否则使用 agent 配置的 tool。
// 结果为空表示无限制。
func resolveAllowedTools(reqTools []string, agentID string) []string {
	if len(reqTools) > 0 {
		return reqTools
	}
	return agentAllowedTools(agentID)
}

// isAllowedScope 判断 scope 是否被配置的 contract 限制允许。
// 空 scope 视为允许（回退到默认值）。若未配置任何 scope，则所有 scope 都允许。
func isAllowedScope(scope string, allowed []string) bool {
	if scope == "" {
		return true
	}
	if len(allowed) == 0 {
		return true
	}
	for _, s := range allowed {
		if s == scope {
			return true
		}
	}
	return false
}

// enrichAgentSpecAllowedTools 从 DB 加载每个 spec 对应的 agent，
// 并在 spec 未显式提供 AllowedTools 时补齐。
func enrichAgentSpecAllowedTools(specs []orchestrator.AgentSpec) []orchestrator.AgentSpec {
	for i := range specs {
		if len(specs[i].AllowedTools) > 0 {
			continue
		}
		if tools := agentAllowedTools(specs[i].AgentID); len(tools) > 0 {
			specs[i].AllowedTools = tools
			if specs[i].Contract == nil {
				contract := harness.DefaultContract(specs[i].Input)
				specs[i].Contract = &contract
			}
			specs[i].Contract.AllowedTools = tools
		}
	}
	return specs
}

// runAgentLoop 执行一次 chat 请求的完整 ReAct loop。
// 它是 runAgentLoopWithTurn 在初始（root）轮次上的便捷封装。
// caseID 供 MockProvider 做确定性脚本匹配；LLM_USE_MOCK 为 false
// 或请求未指向预设 case 时被忽略。
// modelRouter/routerProviders 激活 Engine 中的 Phase 6 Router；传 nil
// 则回退到旧的单模型路径。
func runAgentLoop(hub *ws.Hub, taskID, agentID, systemPrompt, userInput string, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, contract harness.TaskContract, sessionID string, approvalHandler harness.ApprovalHandler, workingMemory string, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager, caseID string, costRepo cost.CostRepository, modelRegistry *llm.ModelRegistry, modelRouter *llm.Router, routerProviders map[string]llm.Provider, caseService *cases.Service, rootTraceCtx ...*observability.TraceContext) {
	runAgentLoopWithTurn(hub, taskID, agentID, systemPrompt, userInput, cfg, tools, persist, contract, sessionID, approvalHandler, workingMemory, agentBus, checkpointMgr, 0, "", caseID, costRepo, modelRegistry, modelRouter, routerProviders, caseService, rootTraceCtx...)
}

// runAgentLoopWithTurn 在一个多轮 session 内执行一次 chat 请求的完整
// ReAct loop。它接受 turnIndex 和 parentTaskID 以支持对话中的后续轮次
// （turnIndex >= 0）。只有 turnIndex == 0（首轮）时才做 root task 绑定。
// caseID 是可选的 MockProvider 提示，按精确 case 匹配选择 mock 脚本；
// 为空时 provider 回退到关键词匹配。
// modelRouter 是可选的 Phase 6 Router；非 nil 时（配合 routerProviders）
// Engine 会在每次 LLM 调用前分类 intent 并选择模型 tier。
func runAgentLoopWithTurn(hub *ws.Hub, taskID, agentID, systemPrompt, userInput string, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, contract harness.TaskContract, sessionID string, approvalHandler harness.ApprovalHandler, workingMemory string, agentBus runtime.AgentBus, checkpointMgr *runtime.CheckpointManager, turnIndex int, parentTaskID string, caseID string, costRepo cost.CostRepository, modelRegistry *llm.ModelRegistry, modelRouter *llm.Router, routerProviders map[string]llm.Provider, caseService *cases.Service, rootTraceCtx ...*observability.TraceContext) {
	isRoot := turnIndex == 0

	// Phase 7-H：root agent 默认作为 leader，拥有子 agent 派发与工作流定义权限。
	role := runtime.AgentRoleWorker
	canDispatchSubAgents := false
	canDefineWorkflow := false
	supervisorSubTaskID := ""
	approverMode := "leader"
	if isRoot {
		role = runtime.AgentRoleLeader
		canDispatchSubAgents = true
		canDefineWorkflow = true
		approverMode = "user"
	}

	// 持久化任务创建
	if persist != nil {
		persist.SaveTask(taskID, userInput, []string{agentID})
		persist.SaveTaskMeta(taskID, sessionID, parentTaskID, isRoot)
		// 把 root task 绑定到 session，让前端刷新后仍能加载
		if sessionID != "" && isRoot {
			log.Printf("[runAgentLoopWithTurn] sessionID=%s taskID=%s — checking root_task_id", sessionID, taskID)
			sess, err := db.QuerySessionByID(sessionID)
			if err != nil {
				log.Printf("[runAgentLoopWithTurn] QuerySessionByID error: %v", err)
			} else if sess.RootTaskID == "" {
				log.Printf("[runAgentLoopWithTurn] Setting session %s root_task_id = %s", sessionID, taskID)
				db.UpdateSession(sessionID, taskID, sess.Status, sess.UserInput)
			} else {
				log.Printf("[runAgentLoopWithTurn] Session %s already has root_task_id=%s (skip)", sessionID, sess.RootTaskID)
			}
		}
	}

	// 解析 session 的 workspace 目录，让工具（run_shell、write_file、
	// read_file）以正确的 CWD 执行。每一轮都要读取 —— 不只是 root ——
	// 这样多轮对话的后续轮次才能继承同一个 workspace。
	//
	// 否则 EngineConfig.WorkspaceDir 会保持为空，Engine (engine.go:1330)
	// 永远不会把 "workdir" 注入 tool 参数。write_file 就会把相对路径
	// 以 server 的 CWD 解析，把绝对路径按字面处理（例如 "/tmp/x" 直接
	// 写到 /tmp/x 而不是 session workspace），导致文件永远落不到
	// <cwd>/workspace/session-<id>/ 里。
	workspaceDir := ""
	if sessionID != "" {
		if wsSess, err := db.QuerySessionByID(sessionID); err == nil {
			workspaceDir = wsSess.WorkspaceDir
		}
	}

	// 从 mock/全局配置解析 LLM Provider。Provider 在每个 agent loop 中只
	// 创建一次并传给 Engine，以便 mock 开关 (LLM_USE_MOCK / LLMRealCases /
	// LLMMockEndpoints) 生效。出错时记录日志并回退到 nil；Engine 会再用
	// Endpoint/APIKey/Model 创建一个默认的 OpenAIProvider。
	provider, err := llm.CreateProviderFromConfig(cfg, cfg.LLMModel, caseID)
	if err != nil {
		log.Printf("[runAgentLoopWithTurn] Failed to create provider for case=%q (falling back to default): %v", caseID, err)
		provider = nil
	}

	// 构建 Harness policy gate，包含所有安全规则：
	//   PathTraversalRule      —— 阻止文件路径中的 ".."
	//   FileScopeRule          —— 把文件操作限制在 contract scope 内
	//   DangerousCommandRule   —— 阻止危险的 shell 命令 (Phase 5)
	//   ApprovalRule           —— 高风险操作需要前端审批 (Phase 5)
	//   TagPolicyRule          —— 通过 tool tag 强制 TaskContract 权限
	//   TokenBudgetRule        —— 超出 token 预算时阻止 tool call
	//   ToolWhitelistRule      —— 仅允许 contract 中列出的 tool
	//   CostBudgetRule         —— 超出 USD 成本预算时阻止 tool call (M2)
	//
	// 规则按顺序检查。第一个阻止的规则会停止链路。
	tokenBudgetRule := &harness.TokenBudgetRule{}
	costBudgetRule := harness.NewCostBudgetRule()
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		&harness.DangerousCommandRule{},
		harness.NewApprovalRule(approvalHandler),
		harness.NewTagPolicyRule(tools.ToolTags),
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
		costBudgetRule,
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	// 为任务建立进度追踪
	progressManager := harness.NewProgressManager()

	// Phase 6-D: 把 Engine 的 usage/cost 回调接到 CostTracker、Repository
	// 和 MetricsCollector。这是不感知成本的 Engine 把每次 LLM 调用的
	// usage 数据交出去做持久化与可观测性的唯一接入点。我们每个进程只
	// 创建一个 CostTracker（不是每个任务一个），让指标全局累计。
	costTracker := cost.NewCostTracker(cost.WithRegistry(modelRegistry))
	onUsage := func(model string, profile *llm.ModelProfile, usage llm.Usage) {
		observability.DefaultMetrics.RecordLLMCall(
			uint64(usage.PromptTokens),
			uint64(usage.CompletionTokens),
			uint64(usage.TotalTokens),
		)
		projectID := "default"
		if sessionID != "" {
			if sess, err := db.QuerySessionByID(sessionID); err == nil {
				projectID = sess.ProjectID
			}
		}
		// 若 Engine 未提供 profile（旧版回退），从 registry 解析一个，
		// 以便填充 pricing/tier 字段。
		if profile == nil || profile.Provider == "unknown" {
			if p := modelRegistry.Get(model); p != nil {
				profile = p
			}
		}
		record := costTracker.BuildRecordFromProfile(
			taskID, sessionID, projectID, agentID,
			0, // step_index 从 usage 聚合角度填充
			model, profile, usage,
		)
		// M2 修复：把本次调用成本累加进 CostBudgetRule，让 PolicyChain 在
		// 后续 tool call 中能根据累计成本阻断。此前 CostBudgetRule 已实现
		// 并有单测，但从未接入 main.go 的 PolicyChain，端到端不生效。
		// CostBudgetRule.currentCostUSD 本就是 float64 USD，直接传 CostUSD
		// 即可，无需经 CostCents 绕路（后者是 derived round 值，sub-cent 会丢精度）。
		if record.CostUSD > 0 {
			costBudgetRule.SetCost(record.CostUSD)
		}
		// 尽力而为的持久化；失败只记录日志，不中断任务。
		if costRepo != nil {
			if err := costRepo.Insert(record); err != nil {
				observability.DefaultLogger.Warn("cost", "failed to persist cost record", map[string]any{
					"task_id": taskID,
					"error":   err.Error(),
				})
			}
		}
		observability.DefaultMetrics.RecordCost(record.CostCents)
	}

	// Phase 7-H：leader 运行期间启用 dispatch_sub_agent 权限；运行结束后关闭。
	if role == runtime.AgentRoleLeader {
		leaderDispatchEnabled.Store(true)
	}

	engine := runtime.NewEngine(runtime.EngineConfig{
		AgentID:              agentID,
		SystemPrompt:         systemPrompt,
		Model:                cfg.LLMModel,
		Endpoint:             cfg.LLMEndpoint,
		APIKey:               cfg.LLMAPIKey,
		Provider:             provider, // 上方解析出的 mock 或真实 provider
		CaseID:               caseID,   // MockProvider 脚本匹配提示
		Temperature:          0.7,
		MaxTokens:            4096,
		MaxSteps:             contract.MaxSteps,
		Persistence:          persist,
		PolicyGate:           policyGate,
		Progress:             progressManager,
		Contract:             contract,
		SessionID:            sessionID,
		IsRoot:               isRoot,
		ParentTaskID:         parentTaskID,
		ApprovalHandler:      approvalHandler, // Phase 5: 审批处理器
		WorkingMemory:        workingMemory,   // Phase 6: 工作记忆注入
		AgentBus:             agentBus,        // Phase 5: 多Agent通信
		CheckpointManager:    checkpointMgr,   // Phase 5: 崩溃恢复
		TurnIndex:            turnIndex,       // 当前轮次
		WorkspaceDir:         workspaceDir,    // Session 级 workspace 目录（write_file/run_shell 的 CWD）
		OnLLMUsage:           onUsage,         // Phase 6-D: 成本/指标上报
		// Phase 6 Router: 把 model router 接入 Engine，让 chat 路径真正地
		// 分类 intent 并选择模型 tier。modelRouter 为 nil（classifier 不可用）
		// 时 Engine 透明地回退到 cfg.Model —— 保留旧行为。
		Router:    modelRouter,
		Registry:  modelRegistry,
		Providers: routerProviders,
		EvaluationRepository: func() runtime.EvaluationRepository {
			if caseService == nil {
				return nil
			}
			return caseService.Repository()
		}(),
		SessionMessageWriter: func(msg runtime.SessionMessageRecord) error {
			return db.InsertSessionMessage(db.SessionMessageRecord{
				ID:         "msg_" + uuid.New().String(),
				SessionID:  sessionID,
				TaskID:     msg.TaskID,
				TurnIndex:  msg.TurnIndex,
				Role:       msg.Role,
				Content:    msg.Content,
				ToolCallID: msg.ToolCallID,
				ToolCalls:  msg.ToolCalls,
				TokenCount: msg.TokenCount,
			})
		},
		SubTaskID:            taskID,
		// Phase 7-H: 角色与权限字段。
		Role:                 role,
		CanDispatchSubAgents: canDispatchSubAgents,
		CanDefineWorkflow:    canDefineWorkflow,
		SupervisorSubTaskID:  supervisorSubTaskID,
		ApproverMode:         approverMode,
		// Phase 7-I: worker 且 leader 审批模式时，把审批委托给 supervisor Engine。
		SupervisorDecisionHandler: func() runtime.ApprovalDelegationHandler {
			if role == runtime.AgentRoleWorker && approverMode == "leader" {
				return &leaderApprovalHandler{leaderSubTaskID: supervisorSubTaskID}
			}
			return nil
		}(),
		// Phase 7-C: 把 tracer、root context 与 latency recorder 接入 Engine。
		Tracer: tracer,
		RootTraceCtx: func() *observability.TraceContext {
			if len(rootTraceCtx) > 0 {
				return rootTraceCtx[0]
			}
			// 兜底：调用方未提供 root context 时新建一个（例如测试 / 恢复路径）。
			return tracer.StartRoot(taskID, "task")
		}(),
		LLMLatencyRecorder: func(latency time.Duration) {
			observability.DefaultMetrics.RecordLLMLatency(latency)
		},
		ToolLatencyRecorder: func(latency time.Duration) {
			observability.DefaultMetrics.RecordToolLatency(latency)
		},
		// Phase skill: 注入 Skill 子系统。ActiveSkills 取当前 registry 中所有
		// 处于 enabled 状态的 skill id；SkillVariables 暂留 nil，由后续 case/
		// session 上下文填充。
		SkillRegistry: globalSkillRegistry,
		ActiveSkills:  GetEnabledSkillIDs(globalSkillRegistry),
	}, tools, &hubAdapter{hub: hub}, taskID)

	hub.SendEvent(event.NewEvent("task_started", taskID, agentID, 0, map[string]any{
		"task_id":    taskID,
		"agent_id":   agentID,
		"session_id": sessionID,
		"input":      userInput,
		"turn_index": turnIndex,
	}))

	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	// 应用 contract 中按任务指定的 timeout。TimeoutSeconds > 0 创建带 deadline
	// 的 context；0（或负数）表示无限制 —— 不设 deadline。
	if contract.TimeoutSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(contract.TimeoutSeconds)*time.Second)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// 注册任务的 cancel 函数，以便 WebSocket 控制消息能取消本任务
	//（root 或 child）。goroutine 退出时必须移除，避免在 cancelRegistry
	// 中遗留条目。
	// Phase 7-A: 同时把 Engine 实例注册到 engineRegistry，使前端
	// pause/resume 消息能直接拿到引擎句柄。
	storeCancel(taskID, agentID, cancel)
	storeEngine(taskID, agentID, engine)
	defer removeCancel(taskID, agentID)
	defer removeEngine(taskID, agentID)
	// Phase 7-H：leader 运行结束（或失败退出）时，务必关闭 dispatch 权限，
	// 避免后续 worker 复用同一个 tool Registry 时误开启。
	defer func() {
		if role == runtime.AgentRoleLeader {
			leaderDispatchEnabled.Store(false)
		}
	}()

	observability.DefaultMetrics.IncrTasksStarted()

	result, totalTokens, err := engine.Run(ctx, userInput)
	if err != nil {
		observability.DefaultMetrics.IncrTasksFailed()
		log.Printf("[Task %s] Agent loop failed: %v", taskID, err)
		if sessionID != "" {
			// 失败后同样聚合所有任务 token 与 duration 并同步 session 状态，避免失败前
			// 的 token 消耗在第二次刷新 UI 时消失。
			aggregateTokens, _ := db.AggregateSessionTokens(sessionID)
			aggregateDuration, _ := db.AggregateSessionDuration(sessionID)
			db.UpdateSessionContextSize(sessionID, aggregateTokens, 0)
			newStatus := deriveSessionStatus(sessionID)
			db.UpdateSessionStatus(sessionID, newStatus)
			hub.SendEvent(event.NewEvent("session_status", taskID, agentID, 0, map[string]any{
				"session_id":   sessionID,
				"status":       newStatus,
				"total_tokens": aggregateTokens,
				"duration_ms":  aggregateDuration,
			}))
		}
		if result == "" {
			failureReason := err.Error()
			if errors.Is(err, context.DeadlineExceeded) {
				failureReason = "task_timeout"
			}
			hub.SendEvent(event.NewEvent("task_failed", taskID, agentID, 0, map[string]any{
				"reason": failureReason,
			}))
		}
		return
	}

	observability.DefaultMetrics.IncrTasksCompleted()

	// 完成后递增 session.turn_count（多轮对话）
	if sessionID != "" {
		db.UpdateSessionTurnCount(sessionID)
		// 聚合所有任务的累计 token 与 duration，同步回 sessions.total_tokens，保证
		// 侧边栏 token 显示和页面刷新后保持一致。
		aggregateTokens, _ := db.AggregateSessionTokens(sessionID)
		aggregateDuration, _ := db.AggregateSessionDuration(sessionID)
		db.UpdateSessionContextSize(sessionID, aggregateTokens, 0)
		newStatus := deriveSessionStatus(sessionID)
		db.UpdateSessionStatus(sessionID, newStatus)
		hub.SendEvent(event.NewEvent("session_status", taskID, agentID, 0, map[string]any{
			"session_id":   sessionID,
			"status":       newStatus,
			"total_tokens": aggregateTokens,
			"duration_ms":  aggregateDuration,
		}))
	}

	log.Printf("[Task %s] Completed successfully. Tokens: %d, Result: %s", taskID, totalTokens, truncate(result, 100))
}

// hubAdapter 把 ws.Hub 适配为 runtime.EventBus 接口。
type hubAdapter struct {
	hub *ws.Hub
}

func (a *hubAdapter) SendEvent(evt event.Event) {
	a.hub.SendEvent(evt)
}

// streamTask 发射一组演示事件序列，模拟多步 agent 任务。
func streamTask(hub *ws.Hub, taskID string) {
	agentID := "agent_test_001"

	sequence := []struct {
		eType   string
		data    map[string]any
		delayMs int
	}{
		{"agent_ready", nil, 100},
		{"task_started", map[string]any{"task_id": taskID}, 100},
		{"step_started", map[string]any{"step": 0, "type": "think"}, 100},
		{"llm_thinking", map[string]any{"content": "Starting analysis..."}, 200},
		{"llm_delta", map[string]any{"content": "I need to research the latest "}, 50},
		{"llm_delta", map[string]any{"content": "AI developments in 2026. "}, 50},
		// TODO: Phase 6 —— web_fetch + web_search tool 尚未注册。
		// 等这些 tool 实现并接入 tool registry 后，用真实注册的 tool
		// 替换本演示序列。
		{"llm_delta", map[string]any{"content": "Let me use the run_shell tool first."}, 100},
		{"llm_message_complete", nil, 200},
		{"step_complete", map[string]any{"step": 0}, 100},
		{"step_started", map[string]any{"step": 1, "type": "tool_call"}, 100},
		{"tool_call_started", map[string]any{"tool": "run_shell", "args": map[string]any{"command": "echo 'AI agents, multimodal models, safety research'"}}, 100},
		{"tool_call_output", map[string]any{"result": "AI agents, multimodal models, safety research"}, 200},
		{"tool_call_complete", map[string]any{"tool": "run_shell", "duration_ms": 1230}, 100},
		{"observation", map[string]any{"content": "Shell returned relevant topic keywords"}, 200},
		{"step_complete", map[string]any{"step": 1}, 100},
		{"step_started", map[string]any{"step": 2, "type": "think"}, 100},
		{"llm_delta", map[string]any{"content": "# AI in 2026\n\nBased on my research, "}, 50},
		{"llm_delta", map[string]any{"content": "here are the key developments:\n\n"}, 50},
		{"llm_delta", map[string]any{"content": "## 1. Multimodal AI Agents\n\n"}, 50},
		{"llm_delta", map[string]any{"content": "## 2. AI Safety frameworks\n\n"}, 50},
		{"llm_delta", map[string]any{"content": "## 3. Open Source Models\n\n"}, 50},
		{"llm_message_complete", nil, 200},
		{"step_complete", map[string]any{"step": 2}, 100},
		{"task_completed", map[string]any{"result": "Research completed successfully. Found 3 key insights."}, 100},
	}

	for _, item := range sequence {
		data := item.data
		if data == nil {
			data = make(map[string]any)
		}
		data["agent_id"] = agentID

		evt := event.NewEvent(item.eType, taskID, agentID, 0, data)
		hub.SendEvent(evt)

		if item.delayMs > 0 {
			time.Sleep(time.Duration(item.delayMs) * time.Millisecond)
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// handleListCheckpoints 返回所有可用 checkpoint task ID 的 JSON 数组。
// GET /api/checkpoints
func handleListCheckpoints(w http.ResponseWriter, _ *http.Request, cm *runtime.CheckpointManager) {
	taskIDs, err := cm.List()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list checkpoints: %v", err), http.StatusInternalServerError)
		return
	}
	if taskIDs == nil {
		taskIDs = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"checkpoints": taskIDs,
	})
}

// handleRecoverCheckpoint 从 checkpoint 恢复任务。
// POST /api/checkpoints/recover
// Body: {"task_id": "task_xxx"}
func handleRecoverCheckpoint(w http.ResponseWriter, r *http.Request, hub *ws.Hub, cfg *config.Config, tools *tool.Registry, persist runtime.Persistence, approvalHandler harness.ApprovalHandler, agentBus runtime.AgentBus, cm *runtime.CheckpointManager) {
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	// 从磁盘加载 checkpoint。
	cp, err := cm.Load(req.TaskID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load checkpoint: %v", err), http.StatusNotFound)
		return
	}

	// 从 checkpoint 的 agent ID 与恢复状态构建 engine config。
	// system prompt 用一个通用的恢复 prompt，因为原始 prompt 已在
	// 对话历史中。
	contract := harness.DefaultContract("resume")
	contract.MaxSteps = cp.StepIdx + 10 // 再允许 10 步

	// 若 engine config 中可用则从 checkpoint 恢复 case ID
	//（engine 自身的 caseID 未单独持久化，因此没有 case 元数据时
	// 会回退到关键词匹配）。
	caseID := ""

	// 为恢复路径从 mock/全局配置解析 LLM Provider。
	// 出错时记录日志并回退到 nil；Engine 会再用 Endpoint/APIKey/Model
	// 创建一个默认的 OpenAIProvider。
	provider, err := llm.CreateProviderFromConfig(cfg, cfg.LLMModel, caseID)
	if err != nil {
		log.Printf("[handleRecoverCheckpoint] Failed to create provider for case=%q (falling back to default): %v", caseID, err)
		provider = nil
	}

	progressManager := harness.NewProgressManager()
	tokenBudgetRule := &harness.TokenBudgetRule{}
	costBudgetRule := harness.NewCostBudgetRule()
	policyChain := harness.NewPolicyChain(
		&harness.PathTraversalRule{},
		&harness.FileScopeRule{},
		&harness.DangerousCommandRule{},
		harness.NewApprovalRule(approvalHandler),
		harness.NewTagPolicyRule(tools.ToolTags),
		tokenBudgetRule,
		&harness.ToolWhitelistRule{},
		costBudgetRule,
	)
	policyGate := harness.NewPolicyGate(policyChain, contract)

	cfg_ := runtime.EngineConfig{
		AgentID:           cp.AgentID,
		SystemPrompt:      "You are recovering from a checkpoint. Continue the task from where you left off.",
		Model:             cfg.LLMModel,
		Endpoint:          cfg.LLMEndpoint,
		APIKey:            cfg.LLMAPIKey,
		Provider:          provider, // 上方解析出的 mock 或真实 provider
		CaseID:            caseID,   // MockProvider 脚本匹配提示
		Temperature:       0.7,
		MaxTokens:         4096,
		MaxSteps:          contract.MaxSteps,
		Persistence:       persist,
		PolicyGate:        policyGate,
		Progress:          progressManager,
		Contract:          contract,
		ApprovalHandler:   approvalHandler,
		AgentBus:          agentBus,
		CheckpointManager: cm,
	}

	engine := runtime.RecoverFromCheckpoint(cp, cfg_, tools, &hubAdapter{hub: hub}, req.TaskID)

	// 发送恢复事件给前端。
	hub.SendEvent(event.NewEvent("task_started", req.TaskID, cp.AgentID, cp.StepIdx, map[string]any{
		"task_id":      req.TaskID,
		"agent_id":     cp.AgentID,
		"recovered":    true,
		"step_idx":     cp.StepIdx,
		"total_tokens": cp.TotalTokens,
	}))

	// 在 goroutine 中运行 engine。input 为空，因为对话历史里已有
	// 最后一条 user message。
	go func() {
		ctx := context.Background()
		cancel := context.CancelFunc(func() {})
		if contract.TimeoutSeconds > 0 {
			ctx, cancel = context.WithTimeout(ctx, time.Duration(contract.TimeoutSeconds)*time.Second)
		} else {
			ctx, cancel = context.WithCancel(ctx)
		}
		defer cancel()

		result, totalTokens, err := engine.Run(ctx, "")
		if err != nil {
			log.Printf("[Recovery %s] Agent loop failed: %v", req.TaskID, err)
			failureReason := err.Error()
			if errors.Is(err, context.DeadlineExceeded) {
				failureReason = "task_timeout"
			}
			hub.SendEvent(event.NewEvent("task_failed", req.TaskID, cp.AgentID, 0, map[string]any{
				"reason": failureReason,
			}))
			return
		}

		// 成功完成后删除 checkpoint。
		cm.Delete(req.TaskID)
		log.Printf("[Recovery %s] Completed. Tokens: %d, Result: %s", req.TaskID, totalTokens, truncate(result, 100))
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"task_id":  req.TaskID,
		"agent_id": cp.AgentID,
		"step_idx": cp.StepIdx,
		"status":   "recovering",
	})
}

// seedDefaultAdminIfNeeded 在数据库中不存在任何用户时创建一个默认 admin
// 用户与 API key。原始 API key 会一次性打印到控制台。
func seedDefaultAdminIfNeeded(store auth.APIKeyStore) {
	sqliteStore, ok := store.(*auth.SqliteAPIKeyStore)
	if !ok {
		return
	}
	count, err := sqliteStore.CountUsers()
	if err != nil || count > 0 {
		return
	}
	admin, err := sqliteStore.AddUser("Admin", auth.RoleAdmin)
	if err != nil {
		log.Printf("Auth: failed to create default admin user: %v", err)
		return
	}
	_, rawKey, err := sqliteStore.Create(admin.ID, "default")
	if err != nil {
		log.Printf("Auth: failed to create default API key: %v", err)
		return
	}
	log.Printf("========================================")
	log.Printf("DEFAULT ADMIN API KEY: %s", rawKey)
	log.Printf("  (save this key — it will not be shown again)")
	log.Printf("========================================")
}

// initDualLogging 以追加方式打开 logPath，并把结构化 logger 同时写到
// 文件与 os.Stdout。纯文本控制台 logger 故意保持不动，启动横幅仍可读。
func initDualLogging(logPath string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	// StructuredLogger 把 JSON 行同时写到 stdout 与文件。
	observability.DefaultLogger.SetOutput(io.MultiWriter(os.Stdout, logFile))
	// 非结构化的控制台 logger 保持不变；控制台仍会显示启动横幅与
	// 包级 log.Printf 调用。
	return nil
}

// handleSessionWorkspaceBrowse 返回 session workspace 目录的 JSON 元信息，
// 包含供前端一键跳转的 browse URL。
// GET /api/sessions/{id}/workspace-browse
func handleSessionWorkspaceBrowse(w http.ResponseWriter, r *http.Request, sessionID string) {
	sess, err := db.QuerySessionByID(sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id":    sessionID,
		"workspace_dir": sess.WorkspaceDir,
		"browse_url":    "/s/" + sessionID + "/",
	})
}

// workspaceFileNode 是 workspace-tree 响应里的单条文件/目录节点。
// relative_path 相对于 session workspace 根，前端可拼回 /s/{session_id}/{relative_path} 访问。
type workspaceFileNode struct {
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
	IsDir        bool   `json:"is_dir"`
	Size         int64  `json:"size"`
	ModTime      string `json:"mod_time"`
}

// handleSessionWorkspaceTree 列出当前 session workspace 下指定子目录（单层）。
// GET /api/sessions/{id}/workspace-tree?path=<relative-subdir>
//
// 设计取舍：
//   - 只列单层，目录由前端按需展开（再请求 path=<subdir>），避免一次性返回整棵树
//     在文件很多的工作目录下把响应撑爆。
//   - 路径校验复用 /s/ 静态服务的同款 prefix 校验，确保相对路径不会逃出 workspace。
//   - 排序：目录在前、文件在后，各自按名称升序，前端直接渲染即可。
func handleSessionWorkspaceTree(w http.ResponseWriter, r *http.Request, sessionID string) {
	sess, err := db.QuerySessionByID(sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if sess.WorkspaceDir == "" {
		// 没有 workspace 目录（例如尚未创建任务）直接返回空列表，而不是 404，
		// 让前端文件树显示"空目录"占位而不是错误态。
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"session_id":    sessionID,
			"workspace_dir": "",
			"path":          "",
			"entries":       []workspaceFileNode{},
		})
		return
	}

	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	// 相对路径统一用 / 分隔，避免 Windows 反斜杠绕过 Join 语义。
	rel = filepath.ToSlash(rel)
	// 拒绝绝对路径与 ..，防止 traversal。
	if rel == "." {
		rel = ""
	}
	if strings.HasPrefix(rel, "/") || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	root := filepath.Clean(sess.WorkspaceDir)
	target := filepath.Clean(filepath.Join(root, rel))
	// 再次确认解析后仍在 root 内。
	if target != root && !strings.HasPrefix(target+string(filepath.Separator), root+string(filepath.Separator)) {
		http.Error(w, "path traversal detected", http.StatusForbidden)
		return
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		// 目录不存在或不可读：返回空列表而非 500，前端可显示占位。
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"session_id":    sessionID,
			"workspace_dir": sess.WorkspaceDir,
			"path":          rel,
			"entries":       []workspaceFileNode{},
		})
		return
	}

	nodes := make([]workspaceFileNode, 0, len(entries))
	var dirNames, fileNames []string
	nameToEntry := make(map[string]os.DirEntry, len(entries))
	for _, e := range entries {
		// 跳过隐藏文件（以 . 开头），workspace 通常是产物目录，隐藏项没有浏览价值。
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		nameToEntry[e.Name()] = e
		if e.IsDir() {
			dirNames = append(dirNames, e.Name())
		} else {
			fileNames = append(fileNames, e.Name())
		}
	}
	sort.Strings(dirNames)
	sort.Strings(fileNames)
	emit := func(name string, isDir bool) {
		e := nameToEntry[name]
		info, err := e.Info()
		var size int64
		var mt string
		if err == nil {
			size = info.Size()
			mt = info.ModTime().UTC().Format(time.RFC3339)
		}
		// rel 是请求的相对子目录；节点相对路径 = rel + name（始终用 /）。
		rp := name
		if rel != "" {
			rp = rel + "/" + name
		}
		nodes = append(nodes, workspaceFileNode{
			Name:         name,
			RelativePath: rp,
			IsDir:        isDir,
			Size:         size,
			ModTime:      mt,
		})
	}
	for _, n := range dirNames {
		emit(n, true)
	}
	for _, n := range fileNames {
		emit(n, false)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"session_id":    sessionID,
		"workspace_dir": sess.WorkspaceDir,
		"path":          rel,
		"entries":       nodes,
	})
}

// isTruthyEnv 在环境变量值为 "1"、"true" 或 "yes" 时返回 true。
func isTruthyEnv(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes"
}

// fileExists 检查嵌入式文件系统中是否存在某个路径。
// 它会去掉前导 "/"，因为 fs.FS 的路径是相对的。
func fileExists(fsys fs.FS, path string) bool {
	// 去掉前导斜杠以兼容 fs.FS
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	if path == "" {
		path = "index.html"
	}
	f, err := fsys.Open(path)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
