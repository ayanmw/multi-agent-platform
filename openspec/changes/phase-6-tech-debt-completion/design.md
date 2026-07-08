## Context

Phase 6 已完成多厂商 Provider、Router、Worker Pool、CostTracker、降级链等核心功能，但代码库存在技术债务未清理，且 RAG、Auth、可观测性等高级特性仅有设计文档未实现。当前代码库处于 v0.6 Alpha，需要系统性提升生产就绪度。

**当前状态**:

- Provider 接口缺乏 context 传递，导致 fallback 无法响应取消信号
- Fallback provider 选择逻辑硬编码 OpenAIProvider，不支持 Anthropic 等非 OpenAI 兼容 Provider
- CostTracker 使用 float64 存储 USD，长期累积存在精度漂移
- 迁移版本跳跃（v5→v6→v7→v9→v10），缺少 v8
- RAG/Auth/可观测性仅有设计文档，无实现

**约束**:

- 保持向后兼容（现有 API、事件格式不变）
- 无外部新依赖（向量检索 Phase 7+ 接入）
- 遵循白盒 Agent 设计哲学（事件驱动、可观测）

## Goals / Non-Goals

**Goals**:

1. 清理 Phase 6 技术债务（P1/P2 问题）
2. 设计 RAG、Auth、可观测性的基础架构
3. 提升代码可维护性和生产就绪度

**Non-Goals**:

- 不实现完整的 RAG 检索管线（Phase 7+）
- 不实现完整的 Auth 前端页面（Phase 7+）
- 不引入新外部依赖（LanceDB/ChromaDB、OpenTelemetry SDK 等）

## Decisions

### D1: Provider 接口增加 Context 字段

**决策**: 在 `ChatRequest` 中增加 `Context context.Context` 字段，`ChatStream` 实现使用 `http.NewRequestWithContext` 绑定 context。

** rationale**: 取消传播是 goroutine 安全的基础。当前 fallback 请求创建新 goroutine 但不受主 context 控制，可能导致 goroutine 泄漏。

**Alternatives Considered**:

- 在 `ChatStream` 签名中增加 context 参数（破坏性变更）— 需要修改所有 Provider 实现和调用方
- 使用全局 cancel 变量（反模式）— 不推荐

**选择**: 增加 `ChatRequest.Context` 字段（非破坏性变更，可选使用）

### D2: Fallback Provider 查找优化

**决策**: Fallback 时先查询 `ProviderRegistry`，按 model name 查找 Provider，找不到时再按 provider name 查找，最后 fallback 到 `NewProvider` 工厂。

**Rationale**: 当前硬编码 `NewOpenAIProvider` 会导致 Anthropic fallback 错误使用 OpenAI endpoint。

**Alternatives Considered**:

- 在 RouteDecision 中预解析 Provider（增加复杂度）— 当前设计已足够
- 强制所有 Provider 实现 Name() 返回 provider name（已实现）

### D3: CostTracker 整数存储

**决策**: CostRecord 增加 `CostCents int64` 字段（存储分），保留 `CostUSD float64` 用于显示。计算时使用整数运算（分 = input_tokens * input_price_cents / 1M）。

**Rationale**: 浮点精度漂移在大额累积时显现（如 10000 次调用后误差可达 $0.01+）。

**Alternatives Considered**:

- 使用 decimal 包（增加依赖）— 过度设计
- 使用定点数（int64 存储微秒级）— 可扩展但当前需求用分足够

### D4: RAG 抽象接口

**决策**: 定义 `EmbeddingProvider` 接口 + `VectorStore` 接口，不绑定具体实现。

**Rationale**: 为 Phase 7+ 接入 LanceDB/ChromaDB 预留接口，当前实现内存版 VectorStore。

### D5: Auth 基础模型

**决策**: 使用 API Key 作为主要认证方式（JWT Phase 7+），User/Role 模型存储在 SQLite。

**Rationale**: 当前系统为单用户/多 Agent 场景，API Key 足够；JWT 为多用户场景预留。

### D6: 可观测性基础

**决策**: 集成结构化日志（已有 log 包增强）、/healthz 端点、/metrics 端点（Prometheus 格式）。

**Rationale**: 生产环境需要健康检查和指标监控，但不引入 OpenTelemetry SDK（Phase 7+）。

## Risks / Trade-offs

| Risk | Mitigation |
|------|-----------|
| Context 增加导致 Provider 实现复杂度上升 | 默认 nil context，向后兼容；仅在需要时使用 |
| 整数存储分需要前端适配 | 后端 API 同时返回 CostCents 和 CostUSD，前端按需显示 |
| Auth 增加认证开销 | API Key 认证为 O(1) 查找，开销极小 |
| 可观测性增加日志量 | 结构化日志按级别过滤，生产环境只记录 warn+ |

## Migration Plan

1. **Phase 6-C** ✅ : 修复 P1/P2 技术债务 + RAG/Auth/Observability 基础接口
2. **Phase 6-D** (next): 可观测性落地（结构化日志接入业务流、/healthz、/metrics） + 成本持久化到 cost_records 表
3. **Phase 6-E** (after): Auth 实际生效（users/api_keys 表、API Key 验证中间件） + RAG 向量召回接入 MemoryRecall
4. **Phase 7**: 引入外部依赖 — 向量数据库 + 外部 Embedding API + JWT/OAuth + OpenTelemetry

**Scope clarification**: Phase 6 must not be "skeleton only". 6-D and 6-E are real feature iterations that run in production without new external dependencies. Skeleton placeholders without business integration must not be accepted for Phase 6.

**Rollback**: 所有变更为向后兼容，可通过 Git revert 回退。

## Open Questions

- [x] RAG 检索策略：向量相似度阈值？混合检索（关键词 + 向量）？ → Phase 6-E 先用关键词粗筛 + 向量精排；Phase 7 升级为混合检索
- [x] Auth 多用户支持需求优先级？ → Phase 6-E 实现单用户多 API Key；JWT/OAuth 推迟到 Phase 7
- [x] 可观测性指标格式：Prometheus vs OpenTelemetry？ → Phase 6-D 使用 Prometheus 文本格式；OpenTelemetry 推迟到 Phase 7
