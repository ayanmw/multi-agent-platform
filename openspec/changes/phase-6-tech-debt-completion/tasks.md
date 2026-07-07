## 1. Provider Context 传递修复

- [x] 1.1 在 ChatRequest 结构体增加 Context context.Context 字段（provider.go）
- [x] 1.2 OpenAIProvider.ChatStream 使用 http.NewRequestWithContext(req.Context)（openai_provider.go）
- [ ] 1.3 AnthropicProvider.ChatStream 使用 http.NewRequestWithContext(req.Context)（anthropic_provider.go）
- [ ] 1.4 Engine fallback 逻辑移除多余的 context.WithCancel（engine.go）
- [ [ ] 1.5 验证编译通过（go build ./...）

## 2. Fallback Provider 选择优化

- [ ] 2.1 Engine fallback 时先查 ProviderRegistry.Get(modelName)（engine.go）
- [ ] 2.2 找不到时 fallback 到 llm.NewProvider(注册表配置) 而非硬编码 NewOpenAIProvider（engine.go）

## 3. CostTracker 精度修复

- [ ] 3.1 CostRecord 增加 CostCents int64 字段，计算时使用整数分（cost_tracker.go）
- [ ] 3.2 HTTP API 响应同时返回 CostCents 和 CostUSD（cost_http.go）
- [ ] 3.3 前端展示使用 CostCents 格式化显示（如 "$1.23"）

## 4. ProviderRegistry.List 排序

- [ ] 4.1 List() 方法返回排序后的 provider 名称切片（provider_registry.go）

## 5. 迁移版本对齐

- [ ] 5.1 确认 v8 迁移用途，补齐或注释说明（pkg/db/migrate.go）

## 6. RAG 基础设计（骨架）

- [ ] 6.1 定义 EmbeddingProvider 接口（internal/llm/embedding.go）
- [ ] 6.2 定义 VectorStore 接口（internal/memory/vector_store.go）
- [ ] 6.3 实现 InMemoryVectorStore（内部使用 sync.Map + 余弦相似度）

## 7. Auth 基础设计（骨架）

- [ ] 7.1 定义 User/Role 模型（internal/auth/model.go）
- [ ] 7.2 实现 API Key 生成/验证/哈希（internal/auth/apikey.go）
- [ ] 7.3 实现 /api/auth/api-keys CRUD 端点（cmd/server/api.go）
- [ ] 7.4 创建 api_keys 表和 users 表 DB 迁移

## 8. 可观测性基础

- [ ] 8.1 实现结构化日志包（internal/observability/logger.go）
- [ ] 8.2 实现 /healthz 端点（包含 DB/LLM 检查）
- [ ] 8.3 实现 /metrics 端点（Prometheus 格式：任务计数、成本总计）

## 9. 代码审查与验证

- [ ] 9.1 运行 go build ./... 确保编译通过
- [ ] 9.2 运行 go vet ./... 检查潜在问题
- [ ] 9.3 提交 Git（Commit: "Phase 6-C: 技术债务修复 + 高级特性设计"）
- [ ] 9.4 更新 ROADMAP.md 标记完成任务
