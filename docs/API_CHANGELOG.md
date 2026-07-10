# API CHANGELOG

> 文档位置：`docs/API_CHANGELOG.md`  
> 生成日期：2026-07-10  
> 对应后端状态：**backend 全方位测试完成**  
> 范围：本次 API 全量测试（curl 冒烟 + Go 单测）期间发现的文档/实现差异、确认一致的契约，以及给 frontend 的适配建议。

---

## 变更分类说明

| 类型 | 含义 |
|------|------|
| `fix` | 文档与实现不一致，需要前端按实现修正 |
| `confirm` | 文档与实现一致，前端可直接依赖 |
| `risk` | 实现存在已知问题，前端需降级/容错 |
| `future` | 当前未实现，待后续 Phase 再评估 |

---

## 1. 已确认契约（confirm）

### 1.1 `POST /api/tasks`
- **端点**: `POST /api/tasks?case=<caseID>`
- **参数**:
  - Body: `{ "action": "chat", "input": "...", "agent_id": "...", "max_steps": 10 }`
  - Query `case`: 透传给 MockProvider 做 `case_id` 精确匹配。真实 LLM 场景下 `case` 仅用于 `LLM_REAL_CASES` 开关判定。
- **返回**: `201`
  ```json
  {
    "session_id": "...",
    "task_id": "...",
    "agent_id": "...",
    "action": "chat"
  }
  ```
- **前端适配**: 无。CaseCard 触发任务时带 `?case=<caseID>`。

### 1.2 `GET /api/tasks?id=<taskID>`
- **端点**: `GET /api/tasks?id=<taskID>`
- **返回**: `200`
  ```json
  {
    "steps": [...],
    "child_tasks": [...]
  }
  ```
- **前端适配**: 任务详情 / 回放页直接依赖此结构。

### 1.3 `/api/costs`
- **端点**:
  - `GET /api/costs?task_id=...`
  - `GET /api/costs?session_id=...`
  - `GET /api/costs?project_id=...`
- **返回**: `200`
  ```json
  {
    "record_count": 1,
    "total_cost_cents": 50,
    "total_cost_usd": 0.50,
    "total_tokens": 150,
    "input_tokens": 100,
    "output_tokens": 50,
    "by_model": { "deepseek-v4-flash": 50 },
    "by_agent": { "agent_1": 50 },
    "by_tier": { "standard": 50 },
    "records": [...]
  }
  ```
- **前端适配**: 成本面板按此结构渲染。

### 1.4 `/api/multi-agent`
- **端点**: `POST /api/multi-agent`
- **返回**: `201`
  ```json
  {
    "session_id": "...",
    "task_id": "...",
    "agent_ids": ["a1", "a2"],
    "agent_count": 2,
    "status": "created"
  }
  ```
- **前端适配**: 多树渲染依据 `agent_ids`。

### 1.5 `POST /api/projects`
- **返回**: `201`，body `{ "id": "..." }`。
- **前端适配**: 注意状态码是 201 不是 200，其余无差异。

### 1.6 `POST /api/sessions`
- **返回**: `201`，body `{ "session_id": "..." }`。
- **前端适配**: 字段名是 `session_id` 不是 `id`。

### 1.7 Auth 默认关闭
- **行为**: `REQUIRE_AUTH=false` 时所有 `/api/*` 无需 token。
- **风险**: 切到 `REQUIRE_AUTH=true` 后所有写操作（以及部分敏感读）需要 `Authorization: Bearer <api_key>`。
- **前端适配**: 必须支持 Bearer token 输入框 / 环境变量注入。

### 1.8 `/api/checkpoints/recover`
- **端点**: `POST /api/checkpoints/recover`
- **行为**: 无 checkpoint 时返回 `404`（合理）。
- **前端适配**: recover 按钮需处理 404。

### 1.9 Mock 管理端点
- **端点**:
  - `GET /api/mock/scripts`
  - `GET /api/mock/scripts/:id`
  - `POST /api/mock/scripts`
  - `DELETE /api/mock/scripts/:id`
  - `POST /api/mock/reset`
- **前端适配**: 仅测试环境使用，不暴露给生产前端。

---

## 2. 需要修正文档或前端的差异（fix）

### 2.1 `POST /api/run-case` 不存在
- **文档位置**: `IMPLEMENTATION_PLAN.md` 第 4.5 节
- **当前状态**: 源码未实现 `/api/run-case`
- **正确的调用方式**: `POST /api/tasks?case=<caseID>`
- **影响**: 中
- **行动**:
  - 方案 A：前端 CaseCard 改为调用 `/api/tasks?case=<id>`（推荐）。
  - 方案 B：后端补一个薄代理端点 `/api/run-case` 转发到 `/api/tasks?case=`。

### 2.2 Memory 路由与文档不符
- **文档位置**: `IMPLEMENTATION_PLAN.md` 第 4.5 节
- **文档列出的端点**（不存在或路径不对）:
  - `POST /api/memories`（顶层创建）—— 实际不存在，创建记忆通过 `POST /api/memories/promote` 从 task 提升。
  - `PUT /api/memories/{id}` —— 实际为 `PUT /api/memories/{id}/scope`，只改 scope。
- **实际存在的端点**:
  ```
  GET    /api/memories
  POST   /api/memories/promote
  GET    /api/memories/recall?query=...
  PUT    /api/memories/{id}/scope
  DELETE /api/memories/{id}
  ```
- **影响**: 中
- **前端适配**: Memory 浏览页按实际路由对接；不存在“直接新建记忆”功能，必须从 task 提升。

### 2.3 `POST /api/tools` 必填字段
- **文档位置**: `IMPLEMENTATION_PLAN.md` 第 4.5 节
- **当前状态**: Body 必填 `type`（`shell` / `http` / `inline`），且各 type 有必填子字段：
  - `shell`: `command`
  - `http`: `url`
  - `inline`: `code`
- **影响**: 中
- **前端适配**: 工具注册表单需按 type 动态显示对应字段。

---

## 3. 已知实现风险（risk）

### 3.1 `memory.CosineSimilarity` 分母缺 `sqrt`
- **位置**: `internal/memory/vector_store.go:64`
- **问题**: 分母使用 `magA * magB`（平方和乘积），标准余弦相似度应为 `sqrt(magA) * sqrt(magB)`（模的乘积）。
- **影响**: 非单位向量的相似度被系统性低估，`Search` 的 topK 排序可能扭曲。
- **前端适配**: 当前影响限于后端召回排序；前端若展示相似度分数，需知道该分数不是标准 cosine。
- **修复后验证**: `internal/memory/memory_test.go` 中已有 3 个 `t.Skip` 用例，修复后去掉 skip 即自动验证。

### 3.2 Tool Registry 无内置工具保护 + 无 mutex
- **位置**: `internal/tool/registry.go`
- **问题**: 内置工具删除保护只在 HTTP handler 层（`IsBuiltin` 检查）；`Registry.Unregister` 可直接删除 `run_shell` 等内置工具。`tools` map 无同步原语，并发写会 panic。
- **影响**: 中
- **前端适配**: 前端调用正常，但运维/测试阶段不要直接 unregister 内置工具。

### 3.3 SQLite 连接池未做并发控制
- **位置**: `pkg/db/database.go`
- **问题**: 未设置 `SetMaxOpenConns(1)` 和 busy_timeout，多 goroutine 并发写 modernc.org/sqlite 可能 `SQLITE_BUSY`。
- **影响**: 中
- **前端适配**: 前端无感知，但高并发场景后端可能 500。

### 3.4 Router 忽略 `BudgetUSD` / `LatencyReq`
- **位置**: `internal/llm/router.go`
- **问题**: `RouteRequest` 虽然定义了这两个字段，但 `filterCandidates` / `meetsRequirements` 未读取。
- **影响**: 低
- **前端适配**: 当前前端若传预算/延迟要求，后端不会据此过滤模型。

### 3.5 Memory 对不存在 id 返回 200
- **位置**: `cmd/server/api.go`（memory 相关 handler）
- **问题**: `PUT /api/memories/{id}/scope` 和 `DELETE /api/memories/{id}` 对不存在的 id 返回 `200`，语义上应是 `404`。
- **影响**: 低
- **前端适配**: 删除/改 scope 后若需确认成功，应再 GET 列表校验。

---

## 4. 当前未实现 / 待后续 Phase（future）

### 4.1 WebSocket 事件流专项测试
- **位置**: `/ws`
- **状态**: curl 只能做握手，完整的事件序列（`task_started` → `llm_delta` → `tool_call_started` → ... → `task_completed`）需要 wscat/Go 客户端专项测试。
- **影响**: 中
- **前端适配**: Phase 2 UI 必须以 WS 事件流为真实数据源，不能仅轮询 HTTP。

### 4.2 `handleSessionChat` 未透传 `case` query
- **位置**: `cmd/server/api.go:889`
- **状态**: `/api/sessions/:id/chat` 向 `runAgentLoopWithTurn` 传入空 `caseID`，session-chat 只能走关键词匹配，无法触发 case_id 精确匹配。
- **影响**: 低
- **行动**: 如需支持，可在 `/api/sessions/:id/chat` 增加 `?case=<id>` 透传。不阻塞当前阶段。

---

## 5. Mock / 真实 LLM 开关（confirmed）

三层优先级已验证：

| 变量 | 默认值 | 含义 |
|------|--------|------|
| `LLM_USE_MOCK` | `true` | 总开关，`true` 时默认走 MockProvider。 |
| `LLM_REAL_CASES` | `` | 即使 `LLM_USE_MOCK=true`，这些 case 仍走真实 LLM。 |
| `LLM_MOCK_ENDPOINTS` | `` | 即使 `LLM_USE_MOCK=false`，这些端点/case 仍走 mock。 |

**优先级**:
1. `LLM_MOCK_ENDPOINTS` 命中 → 强制 mock。
2. `LLM_REAL_CASES` 命中 → 强制真实。
3. `LLM_USE_MOCK=true` → mock。
4. 否则 → 真实。

---

## 6. Frontend 适配检查清单

- [ ] CaseCard 调用：从 `/api/run-case` 改为 `POST /api/tasks?case=<caseID>`。
- [ ] 新建会话后读取 `session_id` 字段。
- [ ] 新建项目后按 201 + `id` 处理。
- [ ] 成本面板按 `/api/costs` 的聚合结构渲染。
- [ ] Memory 页面只使用实际存在的 5 个端点，不支持直接 `POST /api/memories`。
- [ ] 工具注册表单按 `type` 动态校验必填子字段。
- [ ] Auth 开关为 true 时，所有请求带 `Authorization: Bearer <key>`。
- [ ] 任务详情/回放依赖 `GET /api/tasks?id=`。
- [ ] 多 Agent 页面依据 `/api/multi-agent` 返回的 `agent_ids`。
- [ ] WebSocket `/ws?session_id=...` 为事件流主数据源。

---

## 7. 附录：测试覆盖文件清单

| 模块 | 测试文件 | 顶层用例数 | 关键覆盖 |
|------|---------|-----------|---------|
| MockProvider | `internal/llm/mock_provider_test.go` | 16 | case_id 匹配 / 关键词回退 / 动态覆盖 / usage |
| Config | `internal/config/config_test.go` | 6 (38 子) | ShouldMock 三层优先级 / Load / splitAndTrim |
| Harness Policy | `internal/harness/policy_test.go` | 11 | 7 Rule + Chain 短路 + Gate 注入 |
| Auth | `internal/auth/auth_test.go` | 16 | bcrypt / GenerateKey / MatchPrefix / Role |
| DB | `pkg/db/database_test.go` | 18 | 迁移幂等 / 16 表 / CRUD / 并发 |
| Router | `internal/llm/router_test.go` | 32 (53 子) | 意图分类 / 模型选择 / fallback 链 |
| Tool Registry | `internal/tool/registry_test.go` | 20 | Register / Execute / Unregister / IsBuiltin |
| Cost | `internal/cost/cost_test.go` | 10 | 整数精度 / 聚合 / 回调 / fallback 链 |
| Memory | `internal/memory/memory_test.go` | 11 | CosineSimilarity / Normalize / VectorStore |
| curl 冒烟 | `scripts/smoke-test.sh` | 46 PASS / 1 SKIP | 全部 REST 端点基础可用性 |
| curl 冒烟 | `scripts/smoke-test.ps1` | 核心端点 | Windows PowerShell 最小可用版 |

---

*最后更新：2026-07-10*
