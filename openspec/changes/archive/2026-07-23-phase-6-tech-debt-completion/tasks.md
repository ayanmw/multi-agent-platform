## Phase 6-C
- [x] Provider Context/Fallback 修复
- [x] CostTracker 整数精度
- [x] ProviderRegistry 排序
- [x] migrate v8 对齐
- [x] RAG/Auth/Observability 骨架

## Phase 6-D 目标：可观测性与成本持久化落地

> **非空壳、真实运行**。不引入外部依赖（Prometheus/OTel/LanceDB 等）。

### 6-D.1 结构化日志接入业务流
- [x] 在 `cmd/server/main.go` 初始化 `observability.DefaultLogger`，按 `LOG_LEVEL` env 配置级别
- [x] 替换关键路径的 `log.Printf` 为结构化日志（server 启动、DB 初始化、任务启动/完成/失败）
- [x] 新增 `/healthz` 端点：检查 DB ping、WS hub 状态，返回 JSON
- [x] 新增 `/metrics` 端点：Prometheus 文本格式，暴露 `agent_tasks_total`, `llm_calls_total`, `llm_tokens_total`, `cost_cents_total`
- [x] 新增 `/api/costs` 查询端点：按 task_id/session_id/project_id 聚合成本
- [x] 验证：curl `/healthz` 和 `/metrics` 返回正确

### 6-D.2 成本持久化到 cost_records 表
- [x] 新增 migration v11：在 `cost_records` 表加 `cost_cents` 列；若已存在数据则按 `cost_usd*100` 回填
- [x] 在 `internal/cost` 新增 `CostRepository` 接口（内存 store + SQLite store）
- [x] 实现 `SqliteCostRepository.Insert(record)` 写入 SQLite
- [x] 在 Engine LLM 调用完成后调用 `OnLLMUsage` callback → CostTracker → Repository → MetricsCollector
- [x] HTTP `/api/costs` 端点从 repository 查询（内存缓存做 fallback）
- [x] 在 `cmd/server/main.go` 初始化 `modelRegistry` 并注入 CostTracker，使 tier/provider/pricing 字段正确填充
- [x] 验证：运行一次任务后 `cost_records` 表有真实记录；curl cost API 返回数据

### 6-D.3 收尾
- [x] `go build ./...`, `go vet ./...` 通过
- [x] 更新 `roadmaps/ROADMAP.md` 标记 6-D 完成
- [x] Git commit: `Phase 6-D: observability endpoints + cost persistence`

## Phase 6-E 目标：Auth 与 RAG 实际集成

> 占坑项在 6-E 实现，6-D 不引入。

### 6-E.1 认证实际生效
- [x] DB migration v12：创建 `users` 表和 `api_keys` 表
- [x] 实现 DB-backed `auth.APIKeyStore`
- [x] `cmd/server/main.go` 启动时创建默认 admin 用户 + 默认 API key（首次启动打印到日志）
- [x] 在 `main.go` 注册 `/api/auth/api-keys` 端点（create/list/revoke）
- [x] 新增可配置 Auth 中间件：默认关闭，`REQUIRE_AUTH=true` 时检查 `Authorization: Bearer <key>`
- [x] 受保护操作：删除 session/project、run_shell、创建/删除 agent、工具注册

### 6-E.2 RAG 记忆向量召回
- [x] 实现本地 EmbeddingProvider（TF-IDF / 关键词 one-hot，无外部模型依赖）作为 v0
- [x] 在 `MemoryRecall` 启动时把 consolidated/semantic memories 加载到 `InMemoryVectorStore`
- [x] 召回逻辑增加向量相似度排序：先关键词粗筛，再用向量精排 topK
- [x] `/api/memories/recall` 增加 `query` 参数，返回按相似度排序的记忆列表
- [x] 在 working memory 注入中优先使用向量召回结果

### 6-E.3 收尾
- [x] 编译、vet、集成测试
- [x] 更新 ROADMAP 标记 6-E 完成
- [x] Git commit: `Phase 6-E: auth middleware + RAG memory recall`

## Phase 6-F: Memory 类型体系 + CRUD API + LLM 摘要 + 向量持久化 + 前端可观测性

> 作为 `phase-6-tech-debt-completion` 的补充任务，收尾 Phase 6+ 所有剩余技术债务。
> 设计约束：无 embedding 模型可用，但 LLM (deepseek-v4-flash) 可用；所有 RAG/memory 操作必须前端可观测（白盒 Agent 哲学）。

### 6-F.1 Embedding / CosineSimilarity BUG 修复
- [x] 复核 `internal/memory/vector_store.go` 的 `CosineSimilarity` 实现：已使用 `math.Sqrt(magA) * math.Sqrt(magB)`，无需再修
- [x] 清理 `internal/memory/memory_test.go` 中过时的 "BUG" 注释，补充非单位向量回归测试

### 6-F.2 向量库 SQLite 持久化
- [x] migration v16: 创建 `memory_embeddings` 表（`memory_id`, `model`, `dimensions`, `embedding` BLOB, `updated_at`）
- [x] `pkg/db/memory_embedding.go`: `MemoryEmbeddingRow` + CRUD（insert/replace, load all/by model, delete）
- [x] `internal/memory/sqlite_vector_store.go`: `SqliteVectorStore` 实现 `VectorStore` 接口，启动时从 SQLite 加载到内存镜像，写入时同步落盘
- [x] 单元测试覆盖 `sqlite_vector_store.go`

### 6-F.3 真实 LLM 摘要替换 keyword-based
- [x] `internal/harness/summarizer.go`: 新增 `LLMSummarizer` / `KeywordSummarizer` / `LLMSummarizerImpl` + `HubEmitter`
- [x] `ContextCompressor` 接入 `LLMSummarizer`，LLM 失败时回退 keyword 摘要
- [x] `Heartbeat` 接入 `LLMSummarizer`，LLM 失败时回退 keyword 摘要
- [x] `cmd/server/main.go`: 用 `CreateProviderFromConfig` 创建 summarizer provider，构造 keyword adapter 并注入 Heartbeat
- [x] 单元测试覆盖正常路径 / fallback / 超时 / keyword adapter / HubEmitter

### 6-F.4 Memory 类型体系 & CRUD API
- [x] `pkg/db/memory.go`: 扩展 memory type 校验函数 + stats/pagination 查询
- [x] `pkg/event/event.go`: 新增 memory 相关事件常量
- [x] `cmd/server/api.go`: 完整 memory CRUD + `/embed` + `/stats`，支持 filter/pagination

### 6-F.5 前端可观测性
- [x] `web/src/types/events.ts`: 扩展 `EventType` 与 memory/RAG 事件
- [x] `web/src/composables/useMemoryEvents.ts`: module-level memory 事件订阅器 + 统计 + 自动刷新 memory 列表
- [x] `web/src/composables/useMemoryStore.ts`: 补充 create/update/embed/stats/pagination
- [x] `web/src/components/MemoryCreateDialog.vue`: 创建 memory 弹窗
- [x] `web/src/components/RAGPreviewPanel.vue`: RAG recall 预览面板
- [x] `web/src/components/MemoryEventsTimeline.vue`: memory 事件时间线
- [x] `web/src/components/MemoryBrowser.vue`: filters / create / inline edit / embed / pagination
- [x] `web/src/App.vue`: tabbed Memory overlay（Browser / RAG / Events），header 入口按钮

### 6-F.6 收尾
- [x] `go build ./...`, `go vet ./...` 通过
- [x] `go test ./...` 通过
- [x] `vue-tsc --noEmit` 通过
- [x] `vite build` 通过
- [x] 更新 `roadmaps/ROADMAP.md` 标记 6-F 完成
- [x] Git commit: `Phase 6-F: memory type system + CRUD + LLM summarizer + vector persistence + frontend observability`

## Phase 7（远期，仅规划）
- 接入外部向量数据库（LanceDB / ChromaDB / pgvector）
- 接入外部 Embedding API（OpenAI/Cohere）
- JWT / OAuth 多用户支持
- OpenTelemetry / Prometheus SDK 深度可观测
