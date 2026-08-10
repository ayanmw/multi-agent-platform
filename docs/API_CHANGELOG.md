# API 变更日志

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
  - Body: `{ "action": "chat", "input": "...", "agent_id": "...", "max_steps": 30, "timeout_seconds": 0 }`
  - `max_steps`: 覆盖本次任务的 ReAct 循环上限（默认 30）。
  - `timeout_seconds`: 覆盖本次任务的运行超时，0 表示不限制。
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
- **参数**:
  - Body: `{ "input": "...", "max_steps": 30, "timeout_seconds": 0, "agents": [...] }`
  - `max_steps` / `timeout_seconds` 为全局覆盖，会分别写入每个 agent spec 的 contract。
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
- **端点**: `POST /api/projects`
- **Body**: `{ "name": "...", "description": "...", "working_directory": "...", "rules": [...] }`；`name` 必填，缺失返回 `400 name is required`。
- **返回**: `201 Created`，body 是**完整的 project 记录**（不是仅 `{id}`）：
  ```json
  {
    "id": "uuid",
    "name": "...",
    "description": "...",
    "working_directory": "...",
    "config": { ... },
    "created_at": "2026-08-10T00:00:00Z",
    "updated_at": "2026-08-10T00:00:00Z"
  }
  ```
  字段名以 `pkg/db.ProjectRecord` 的 JSON tag 为准（`cmd/server/api.go::handleProjects`）。
- **前端适配**: 状态码是 **201 不是 200**（`scripts/smoke-test.sh` 已按 201 断言）；创建后可直接用返回体填充列表，无需二次 GET。

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

### 1.10 `GET /api/cases/:id/evaluations/:task_id`
- **端点**: `GET /api/cases/:id/evaluations/:task_id`
- **参数**:
  - 路径 `:id`: Case ID。
  - 路径 `:task_id`: Task ID。
- **返回**: `200`
  - 找到评估记录：
    ```json
    {
      "evaluation": {
        "id": 1,
        "task_id": "task_xxx",
        "case_id": "code-gen",
        "passed": true,
        "score": 0.95,
        "reason": "结果符合目标：生成了可运行的 Go 程序。",
        "evaluated_at": "2026-07-17T10:00:00Z"
      }
    }
    ```
  - 无评估记录：
    ```json
    {
      "evaluation": null
    }
    ```
- **前端适配**: 任务详情/回放页可在已有 `GET /api/tasks?id=xxx` 回填之外，按 case+task 二次确认评估结果。

### 1.11 `GET /api/tasks?id=<taskID>` 新增 `evaluation` 字段
- **端点**: `GET /api/tasks?id=<taskID>`
- **返回**: `200`
  ```json
  {
    "task": { ... },
    "steps": [...],
    "child_tasks": [...],
    "evaluation": {
      "case_id": "code-gen",
      "passed": true,
      "score": 0.95,
      "reason": "结果符合目标：生成了可运行的 Go 程序。",
      "evaluated_at": "2026-07-17T10:00:00Z"
    }
  }
  ```
  - 若任务无评估记录，`evaluation` 为 `null`。
- **前端适配**: 历史任务（historical task replay）详情页可直接读取 `evaluation` 展示 case 通过/失败状态，无需额外请求。前端需按 `null` 做降级，避免无 case 评估时显示错误。

---

## 2. 文档说明 / 设计确认（confirm）

> 本节内容用于澄清文档与实现的关系，无修正缺口。

### 2.1 `POST /api/run-case` 已实现

- **状态**: 后端已以薄代理端点实现，前端可直接使用。
- **端点**: `POST /api/run-case`
- **实际行为**: 转发至 `POST /api/tasks?case=<caseID>`，透传 body 和查询参数。
- **文档来源**: `IMPLEMENTATION_PLAN.md` 第 4.5 节最初标记为"待补实现"，现已交付。
- **前端适配**: 已在 §6 清单中标记为已完成（§6.1 CaseCard 调用）。

### 2.2 Memory 路由全表（**2026-08-10 校正 / N3-05**）

- **状态**: 本节旧版本称「不存在顶层 `POST /api/memories`、不存在 `PUT /api/memories/{id}`，记忆必须从 task 提升」——**该结论已随实现演进失效**，属典型契约漂移。当前实现（`cmd/server/server.go::registerRoutes` + `cmd/server/api.go`）两者**均已存在**。
- **实际路由全表**（以代码为准）:

  | 方法 | 路径 | 说明 | 成功返回 |
  |------|------|------|----------|
  | GET | `/api/memories` | 列表；支持 `scope/tier/type/status/project/limit/offset` | `200` `{items,total,limit,offset}` |
  | POST | `/api/memories` | **顶层直接创建**；`content` 必填，其余有默认值（`scope=project`、`type=fact`、`tier=consolidated`、`project_id=default`、`status=active`、`confidence=1.0`）；尽力而为写入向量库 | `201` 完整 memory 记录 |
  | GET | `/api/memories/{id}` | 详情 | `200` / `404` |
  | PUT | `/api/memories/{id}` | **更新 content / confidence / status**；三者全空返 `400`；写审计 + 广播 `memory_updated` | `200` 更新后记录 |
  | DELETE | `/api/memories/{id}` | 删除；写审计 + 广播 `memory_deleted` | `200` / `404` |
  | PUT | `/api/memories/{id}/scope` | 单独调整 scope（session/project/global） | `200` |
  | POST | `/api/memories/{id}/embed` | 为指定 memory 生成/刷新 embedding | `200` |
  | POST | `/api/memories/promote` | 从 task 提升记忆（PromotionGate） | `200` |
  | GET | `/api/memories/recall` | `?task=` 上下文召回 或 `?query=` 纯向量检索，`&project=&max=` | `200` |
  | GET | `/api/memories/stats` | `?project=` 统计 | `200` |

- **前端适配**: Memory 页面**可以**提供「直接新建记忆」入口（POST 顶层）与「编辑内容/状态/置信度」入口（PUT `{id}`）；「从 task 提升」是并列能力而非唯一路径。

### 2.3 `POST /api/tools` 契约（**2026-08-10 补全 / N3-05**）

- **状态**: 后端要求 `type` 字段及其子字段，旧文档（`IMPLEMENTATION_PLAN.md` 第 4.5 节）未说明，本节即为权威描述。
- **Body 结构**（`cmd/server/tool_api.go::handleRegisterTool`）:
  - `type`（**必填**）: `shell` / `http` / `inline`；缺失或非法 → `400 type must be 'shell', 'http', or 'inline', got: <v>`。
  - 依 type 的必填子字段（缺失 → `400 <field> is required for <type>-type tools`）:
    - `shell` → `command`
    - `http` → `url`（`method` 可选，默认 `GET`）
    - `inline` → `code`
  - 可选字段与默认值: `name`（缺省自动生成 `dynamic_tool_%03d`）、`description`（缺省 `Dynamic tool: <name> (<type>)`）、`parameters`（缺省空 object schema）。
- **返回**:
  - `201 Created`，body `{name, description, parameters, type}` + 依 type 的 `command` / `url`+`method` / `code`。
  - `409 Conflict`：CanonicalName（`name@1.0.0`）已存在。
  - `500`：持久化失败（此时注册会回滚，不留半注册状态）。
- **鉴权**: `POST` / `DELETE /api/tools` 受 RBAC `tools:write` 守卫（N1-03）；`REQUIRE_AUTH=false` 时仍属特权写路由，需 API key（N3-01）。
- **前端适配**: 工具注册表单按 type 动态显示对应字段；按 201 而非 200 判成功；409 需提示改名或升版本。

---

## 3. 已知实现风险（risk）

### 3.1 SQLite 连接池未做并发控制 —— **已解决（N3-04c 复核）**
- **位置**: 原 `pkg/db/database.go`，现 `pkg/db/backend_sqlite.go`
- **原问题**: 未设置 `SetMaxOpenConns(1)` 和 busy_timeout，多 goroutine 并发写 modernc.org/sqlite 可能 `SQLITE_BUSY`。
- **现状**: SQLite 后端在 `Configure` 阶段已固定 `SetMaxOpenConns(1)` + WAL + `busy_timeout`；单写语义在启动期以告警形式显式暴露（见 `docs/DB_BACKEND_ABSTRACTION.md`）。本条保留为历史记录，前端无需再做 500 降级。
- **影响**: 中
- **前端适配**: 前端无感知，但高并发场景后端可能 500。

### 3.2 Router 忽略 `BudgetUSD` / `LatencyReq`
- **位置**: `internal/llm/router.go`
- **问题**: `RouteRequest` 虽然定义了这两个字段，但 `filterCandidates` / `meetsRequirements` 未读取。
- **影响**: 低
- **前端适配**: 当前前端若传预算/延迟要求，后端不会据此过滤模型。

---

## 4. 当前未实现 / 待后续 Phase（future）

### 4.1 WebSocket 事件流专项测试 —— **已交付（N3-05）**
- **位置**: `/ws`
- **原状态**: curl 无法完成 Upgrade 握手，`scripts/smoke-test.sh` 对 `/ws` 恒为 `[SKIP]`，事件流契约无自动化覆盖。
- **现状**: `internal/ws/hub_handshake_test.go` 用真实 gorilla/websocket 客户端 + `httptest.Server` 覆盖三条契约：
  - `TestServeWSHandshakeAndEventStream`：握手返回 **101**，广播事件以 JSON 文本帧原样抵达，字段名与 `pkg/event.Event` 的 JSON tag 一致（`event_id/task_id/sub_task_id/agent_id/step_index/type/timestamp/data`）。
  - `TestServeWSSessionSubscriptionOverWire`：`?session_id=` 订阅在真实连接上生效，跨 session 事件不泄漏（N3-02 隔离语义的线上验证）。
  - `TestServeWSRejectsPlainHTTPRequest`：非 WS 的普通 GET 被 4xx 拒绝且不注册 client。
- **仍属 SKIP 的部分**: smoke 脚本里的 `/ws` 行仍是 curl 能力限制导致的 `[SKIP]`（非缺陷）；端到端「完整事件序列」（`task_started → llm_delta → tool_call_started → … → task_completed`）由 `scripts/cases-regression.sh` 经 WS 订阅校验（21 case）。
- **前端适配**: web/v2 以 WS 事件流为主数据源；连接建议携带 `?session_id=` 以获得服务端级隔离。

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

- [x] CaseCard 调用：已实现 `/api/run-case` 薄代理，转发至 `POST /api/tasks?case=<caseID>`。前端可继续使用此端点。
- [ ] 新建会话后读取 `session_id` 字段。
- [ ] 新建项目后按 201 + `id` 处理。
- [ ] 成本面板按 `/api/costs` 的聚合结构渲染。
- [x] Memory 页面按 §2.2 路由全表对接（顶层 `POST` 创建与 `PUT /{id}` 编辑**均已可用**，「从 task 提升」为并列能力）。
- [x] 工具注册表单按 `type` 动态校验必填子字段：`shell`→`command`、`http`→`url`、`inline`→`code`；按 201 判成功、409 提示重名。
- [ ] Auth 开关为 true 时，所有请求带 `Authorization: Bearer <key>`。
- [ ] 任务详情/回放依赖 `GET /api/tasks?id=`，并展示返回的 `evaluation` 字段（若无则降级）。
- [ ] 多 Agent 页面依据 `/api/multi-agent` 返回的 `agent_ids`。
- [ ] WebSocket `/ws?session_id=...` 为事件流主数据源。

---

## 7. API 契约漂移自检清单（N3-05 新增）

> **为什么需要它**：本文件历史上出现过三类漂移——① 状态码写错（`POST /api/projects` 曾记为 body `{id}`）；② 能力已实现但文档仍写「不支持」（Memory 顶层 `POST` / `PUT {id}`）；③ 缺陷已修复但风险条目未撤（SQLite 并发控制）。漂移的文档比没有文档更危险：前端会据此写死降级逻辑。
>
> **执行时机**：任何 PR 只要改动了 `cmd/server/**`、`internal/ws/hub.go`（路由/握手）或 `pkg/db` 的对外记录结构，合并前逐条过一遍；Phase R 评审时整表复核一次（对应 E10 维度）。

| # | 检查项 | 判定方法 | 漂移信号 |
|---|--------|----------|----------|
| C1 | **路由存在性**：文档列出的每个端点在 `registerRoutes` 中都能找到 | `grep -n "api/<资源>" cmd/server/server.go` | 文档有、代码无（或反之） |
| C2 | **方法集完整**：每个路径支持的 HTTP 方法与 handler 的 `switch r.Method` 一致 | 读 handler 的 method 分支 + `default` 分支的 405 文案 | 文档漏写某个方法（Memory 漂移即此类） |
| C3 | **状态码精确**：创建类返回 `201`、更新/查询 `200`、缺参 `400`、重名 `409`、不存在 `404` | 搜 handler 内的 `w.WriteHeader(` 与 `http.Error(` | 文档写 200 实际 201（Projects 漂移即此类） |
| C4 | **响应体形状**：文档示例字段名 == 结构体 JSON tag | 定位返回的 struct，比对 tag | 手写示例与 tag 不符 |
| C5 | **必填字段与默认值**：必填项、缺省填充值在文档中显式列出 | 读 handler 开头的校验段与 `if x == "" { x = ... }` | 前端漏传导致 400（Tools `type` 漂移即此类） |
| C6 | **鉴权姿态**：写路由是否受 RBAC / 特权路由保护，`REQUIRE_AUTH=false` 下的行为 | 查 `auth.RequirePermissionFunc` 与 `DefaultPrivilegedWriteRoutes()` | 文档未标注鉴权要求 |
| C7 | **事件契约**：WS 广播字段与 `pkg/event.Event` tag、前端 `web/v2/src/types/events.ts` 三方一致 | `go test ./internal/ws/ -run TestServeWS` | 三方任一不同步 |
| C8 | **风险条目时效**：§3 的每条 `risk` 是否仍然成立 | 按「位置」字段回代码复核 | 缺陷已修但条目仍在（SQLite 漂移即此类） |
| C9 | **冒烟脚本自述**：`scripts/smoke-test.sh` 的 `PROBLEMS` 文案是否仍属实 | 跑一次 smoke，逐条比对本文件 | 脚本输出的「问题」已被修复 |
| C10 | **版本与日期**：文末「最后更新」与本轮改动同步 | 文件末尾 | 长期不动 = 无人复核 |

**自检最小命令组**（全绿方可认为契约无漂移）：

```bash
go vet ./...
go test -short -count=1 ./cmd/server/... ./internal/ws/...
bash scripts/smoke-test.sh          # 关注末尾 PROBLEMS 列表是否与本文件一致
bash scripts/cases-regression.sh    # 21/21，覆盖 WS 事件序列
```

---

## 8. 附录：测试覆盖文件清单

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

*最后更新：2026-08-10（N3-05 API 契约文档校正：Projects 201 响应体 / Tools type 契约 / Memory 路由全表 / WS 握手专项测试 / 新增 §7 漂移自检清单）*
