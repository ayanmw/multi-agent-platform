## Why

Phase 6 已完成核心功能（多厂商 Provider、Router、Worker Pool、CostTracker、降级链），但技术债务清单中有 8 项未修复（4 项 P1、4 项 P2）。部分问题影响正确性和可观测性，需要在后续迭代中系统性修复。本次变更聚焦技术债务清理与 Phase 6 未完成的高级特性（RAG、Auth、可观测性）的基础设计，确保代码库达到可生产状态。

## What Changes

- **修复 P1-4**: Fallback context 传递 — 在 Provider 接口增加 context 注入机制，支持取消传播
- **修复 P1-5**: Fallback provider 选择 — 根据 Fallback model 名称查找 ProviderRegistry，避免硬编码端点
- **优化 P2-6**: ProviderRegistry.List 排序 — 返回稳定排序的列表
- **优化 P2-7**: CostTracker 精度 — 引入整数存储（分）避免浮点漂移
- **优化 P2-8**: 迁移版本对齐 — 确认并补齐 v8 迁移
- **设计 RAG**: 向量检索增强 — Embedding 接口 + 记忆召回管线升级
- **设计 Auth**: 身份认证 — User/Role 模型 + JWT + API Key 管理
- **设计 可观测性**: 结构化日志 + 指标导出 + 健康检查端点

## Capabilities

### New Capabilities

- `provider-context`: Provider 接口支持 context 注入，实现取消传播和超时控制
- `rag-core`: RAG 基础 — Embedding 接口、向量存储抽象、检索器
- `auth-basic`: 基础身份认证 — User/Role 模型 + API Key + JWT
- `observability`: 可观测性 — 结构化日志、Prometheus 指标、健康检查

### Modified Capabilities

- `llm-provider`: Provider 接口增加 Context 字段，ChatStream 接受 context 参数
- `cost-tracking`: CostRecord 存储单位从 USD 改为分（整数），避免浮点精度漂移

## Impact

- **修改文件**: `internal/llm/provider.go`, `internal/llm/openai_provider.go`, `internal/cost/cost_tracker.go`, `internal/cost/cost_http.go`, `pkg/db/migrate.go`, `internal/runtime/engine.go`
- **新增文件**: `internal/llm/embedding.go`, `internal/auth/`, `internal/observability/`
- **API 变更**: Cost API 返回值从 float64 USD 改为 int 分（向后兼容，新增字段）
- **依赖**: 无外部新依赖（向量检索 Phase 7+ 接入 LanceDB/ChromaDB）
