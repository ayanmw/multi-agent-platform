# Phase 6-C 实施计划
> 创建时间: 2026-07-07
> 变更: phase-6-tech-debt-completion
> 当前进度: 9/9 组任务完成 (Task 1-9)

---

## 已完成

| 任务 | 文件 | 变更 |
|------|------|------|
| 1.1 | internal/llm/client.go | ChatRequest 增加 Context context.Context 字段 + import "context" |
| 1.2 | internal/llm/openai_provider.go | Chat + ChatStream 均改为 http.NewRequestWithContext(req.Context) |
| 1.3 | internal/llm/anthropic_provider.go | Chat 方法改为 http.NewRequestWithContext(req.Context)，import 加 "context" |
| 2.1+2.2 | internal/runtime/engine.go | fallback 时不再硬编码 NewOpenAIProvider，改为复用 e.providers 查找逻辑 |

---

## 待实施（按顺序）

### Task 3: CostTracker 精度修复

**文件**: `internal/cost/cost_tracker.go`

**改前** (约第 39-81 行):
```go
type CostRecord struct {
	ID           string
	TaskID       string
	...
	CostUSD      float64    // ← 删除或保留用于显示
	CreatedAt    time.Time
}
```

**改后**:
```go
type CostRecord struct {
	ID           string
	TaskID       string
	...
	CostCents    int64      // 新增：整数存储（分）
	CostUSD      float64    // 保留用于向后兼容显示
	CreatedAt    time.Time
}
```

**改前** (CalculateCost 约第 288-300 行):
```go
func (ct *CostTracker) CalculateCost(profile *llm.ModelProfile, usage llm.Usage) float64 {
	...
	inputCost := float64(usage.PromptTokens) * profile.InputPrice
	outputCost := float64(usage.CompletionTokens) * profile.OutputPrice
	return (inputCost + outputCost) / 1_000_000
}
```

**改后**:
```go
func (ct *CostTracker) CalculateCost(profile *llm.ModelProfile, usage llm.Usage) int64 {
	if profile == nil {
		return 0
	}
	if usage.TotalTokens == 0 {
		return 0
	}
	inputCost := int64(usage.PromptTokens) * int64(profile.InputPrice * 100)
	outputCost := int64(usage.CompletionTokens) * int64(profile.OutputPrice * 100)
	return (inputCost + outputCost) / 1_000_000
}
```

**改前** (BuildRecordFromProfile 约第 308-340 行):
```go
cost := ct.CalculateCost(profile, usage)

return CostRecord{
	...
	CostUSD: cost,
	...
}
```

**改后**:
```go
costCents := ct.CalculateCost(profile, usage)

return CostRecord{
	...
	CostCents: costCents,
	CostUSD:   float64(costCents) / 100.0,  // 用于显示，从分转换
	...
}
```

**文件**: `internal/cost/cost_http.go`

无需修改，JSON 序列化自动包含新字段 CostCents，前端按需展示。

---

### Task 4: ProviderRegistry.List 排序

**文件**: `internal/llm/provider_registry.go`

**改前** (约第 70-78 行):
```go
func (r *ProviderRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	return names
}
```

**改后**:
```go
func (r *ProviderRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
```

需要在文件头部 import 加 `"sort"`。

---

### Task 5: 迁移版本对齐

**文件**: `pkg/db/migrate.go`

当前迁移列表: v1, v2, v3, v4, v5, v6, v7, v9, v10 — 缺少 v8。

**操作**: 在 v7 和 v9 之间插入 v8 迁移条目：
```go
{
    Version: 8,
    Description: "Placeholder migration (no-op) — v8 reserved for future schema change",
    SQL: `SELECT 1`,
},
```

---

### Task 6: RAG 基础骨架

**新文件**: `internal/llm/embedding.go`
```go
package llm

// EmbeddingProvider 接口定义
type EmbeddingProvider interface {
    Embed(text string) ([]float32, error)
    EmbedBatch(texts []string) ([][]float32, error)
    Dimensions() int
}
```

**新文件**: `internal/memory/vector_store.go` (需 mkdir internal/memory)
```go
package memory

// VectorStore 接口定义
type VectorStore interface {
    Upsert(id string, vector []float32, metadata map[string]any) error
    Search(query []float32, topK int) ([]SearchResult, error)
    Delete(id string) error
}

type SearchResult struct {
    ID       string
    Score    float64
    Metadata map[string]any
}
```

**新文件**: `internal/memory/memory_vector.go` (InMemoryVectorStore 实现)

---

### Task 7: Auth 基础骨架

**新目录**: `internal/auth/`

**新文件**: `internal/auth/model.go`
```go
package auth

type User struct {
    ID       string
    Name     string
    Role     string  // "admin", "user", "viewer"
    APIKeyHashed string
    CreatedAt time.Time
}
```

**新文件**: `internal/auth/apikey.go` — GenerateAPIKey, HashAPIKey, VerifyAPIKey

**新文件**: `cmd/server/auth_api.go` — /api/auth/api-keys CRUD 端点

**DB 迁移**: 在 migrate.go 加 v11 创建 users 表和 api_keys 表

---

### Task 8: 可观测性基础

**新文件**: `internal/observability/logger.go` — 结构化日志包装

**修改**: `cmd/server/main.go` 加 /healthz 和 /metrics 端点

---

### Task 9: 验证与提交

- go build ./...
- go vet ./...
- git commit
- 更新 ROADMAP.md

---

## 注意事项

1. **所有 Edit 前必须 Read 目标文件**，确认精确内容（Tab 数量、空格）
2. **engine.go 不要用 Write 整体覆盖**，只用 Edit 小范围替换
3. **build 通过后再继续下一个任务**
4. **tasks.md 每完成一个任务标记 [x]**
