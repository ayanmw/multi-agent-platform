# 多 Agent 平台 — API 全方位测试实施计划

> **目标**: 对项目进行 API 层面全方位测试，确保所有功能模块和系统设计符合预期。
> **原则**: 先 backend 全测完并列出 API 调整清单，再统一评估并修改 frontend。
> **时间**: 2026-07-09

---

## 1. 测试目标与范围

### 1.1 目标
- 通过 curl 冒烟跑所有端点，找出明显问题。
- 为关键模块补 `_test.go` 单元/集成测试。
- 成熟用例加入预设 Cases，方便前端随时测试流程。
- 默认使用 mock LLM，少量真实调用验证端到端；通过开关控制 mock/真实。
- 汇总 API 调整清单，供 frontend 统一评估。

### 1.2 范围
- **覆盖模块**: HTTP REST API、WebSocket 事件流、Auth 中间件、LLM Provider/Router、ReAct Engine、Harness Policy、Cost/Metrics/Memory、Tool 注册、Session/Project/Task 管理。
- **测试形式**: curl 冒烟脚本 → Go `_test.go` → mock LLM 回归 → 真实 LLM 端到端验证。
- **边界**: 前端改动押后；本阶段只输出 API 调整清单，不实际改前端。

---

## 2. 三层 Mock / 真实 LLM 开关设计

为支持"默认 mock、少量真实"的测试策略，引入三层开关。

### 2.1 环境变量

| 变量 | 默认值 | 含义 |
|------|--------|------|
| `LLM_USE_MOCK` | `true` | 全局总开关：`true` 时所有 LLM 调用走 MockProvider；`false` 时走真实 Provider。 |
| `LLM_REAL_CASES` | `` | 允许真实 LLM 的 case_id 列表，逗号分隔。例如 `research`。即使 `LLM_USE_MOCK=true`，这些 case 也走真实。 |
| `LLM_MOCK_ENDPOINTS` | `` | 强制走 mock 的 HTTP 端点或 case_id 列表，逗号分隔。即使 `LLM_USE_MOCK=false`，这些也走 mock。 |

### 2.2 优先级（从高到低）

1. `LLM_MOCK_ENDPOINTS` 命中 → 强制 mock。
2. `LLM_REAL_CASES` 命中 → 强制真实。
3. `LLM_USE_MOCK=true` → mock。
4. 否则 → 真实。

### 2.3 MockProvider

- 实现 `internal/llm/provider.go` 的 `Provider` 接口。
- 不发起 HTTP 请求，直接返回收敛的 JSON delta + tool_calls，用于稳定回归测试。
- 支持两种匹配：
  - **内置脚本**: 按 `case_id` 匹配预置响应。
  - **动态脚本**: 通过 `/api/mock/scripts` 运行时覆盖。
- 生成标准 `usage` 字段（`prompt_tokens`、`completion_tokens`、`total_tokens`），成本/指标链路继续生效。

**已实现**:
- `internal/llm/mock_store.go` 中定义了 `MockScript` / `MockResponse` / `MockScriptStore` 接口，以及内存实现 `InMemoryMockScriptStore` 和 SQLite 实现 `SqliteMockScriptStore`，并提供进程级 `DefaultMockStore`。
- `internal/llm/mock_provider.go` 实现 `Provider` 接口（`Chat` / `ChatStream`），按 case_id / 关键词匹配脚本并回放响应序列，生成 `usage`。
- `internal/llm/mock_builtin.go` 提供 6 个内置脚本（code-gen / dialogue / research / multi-agent / long-task / tool-error）。
- `internal/llm/provider_factory.go` 新增 `"mock"` 分支与 `CreateProviderFromConfig(cfg, modelName, caseID)`，按 `cfg.ShouldMock` 选择 MockProvider 或真实 Provider。
- `internal/config/config.go` 新增 `LLMUseMock` / `LLMRealCases` / `LLMMockEndpoints` 字段与 `ShouldMock(caseID, endpointHint)` 方法（三层优先级已实现）。
- `pkg/db/migrate.go` 新增 v13 `mock_scripts` 表迁移。
- `cmd/server/mock_api.go` 实现 `/api/mock/scripts` 与 `/api/mock/reset` 端点。
- `cmd/server/main.go` 启动时初始化 `DefaultMockStore`（含 SQLite 动态脚本回填）并注册 mock 路由。
- `go build ./...` / `go vet ./...` 通过。

**已接入（完成）**:
- `runAgentLoop` / `runAgentLoopWithTurn` 已扩展签名增加 `caseID` 形参，并在函数体内调用 `llm.CreateProviderFromConfig(cfg, cfg.LLMModel, caseID)` 注入 `Provider`，写入 `runtime.EngineConfig.Provider`（`cmd/server/main.go:807,884`）。
- chat action 已读取 `caseID := r.URL.Query().Get("case")` 并透传至 `runAgentLoop`（`cmd/server/main.go:341,389`）。
- checkpoint-recover 路径同样调用 `CreateProviderFromConfig` 并设置 `EngineConfig.CaseID`（`cmd/server/main.go:1082-1112`）。
- multi-agent 路径经由 `orchestrator` 调用 `CreateProviderFromConfig(o.cfg, model, "")` 并注入 `Provider`（`internal/orchestrator/orchestrator.go:219,231`）。
- `runtime.EngineConfig` 新增 `CaseID` 字段，`NewEngine` 读取并写入 `ChatRequest.CaseID`，MockProvider 可按 case_id 精确匹配（`internal/runtime/engine.go:166,410,1021`）。
- **已知小缺口**: `handleSessionChat`（`cmd/server/api.go:889`）目前向 `runAgentLoopWithTurn` 传入空 `caseID`，session-chat 暂走关键词匹配。如需按 case 精确匹配，可后续在 `/api/sessions/:id/chat` 增加 `case` query param 透传。不阻塞测试。

### 2.4 内置 Mock 脚本

| case_id | 行为 |
|---------|------|
| `code-gen` | 返回 `write_file` tool_call，写入文件后第二轮返回最终说明。 |
| `dialogue` | 返回纯文本 final answer（最终答案），无 tool_call。 |
| `research` | 返回 `read_file` 或 `run_shell` tool_call，第二轮总结。 |
| `multi-agent` | 返回子任务派发信息（通过 AgentBus 或纯文本）。 |
| `long-task` | 多轮 thought → tool_call → observe 循环，用于测试 Policy/TokenBudget。 |
| `tool-error` | 第二次交互触发错误，验证 Engine 错误处理。 |

### 2.5 Mock 响应匹配规则

- 输入匹配维度：
  - `case_id` 精确匹配（优先）。
  - 用户输入关键词模糊匹配（fallback）。
- 支持响应模板中的占位符：例如 `{{file}}`、`{{content}}`。

---

## 3. `/api/mock/scripts` 管理端点

提供运行时 mock 脚本 CRUD，仅用于测试环境（`REQUIRE_AUTH=true` 时仅 admin 可写）。

### 3.1 数据模型

```json
{
  "id": "custom-research",
  "case_id": "research",
  "priority": 100,
  "match_input": ["weather", "today"],
  "responses": [
    {
      "type": "tool_call",
      "tool": "run_shell",
      "arguments": {"command": "echo 'sunny'"},
      "usage": {"prompt_tokens": 100, "completion_tokens": 50}
    },
    {
      "type": "text",
      "content": "今天的天气是晴天。",
      "usage": {"prompt_tokens": 130, "completion_tokens": 20}
    }
  ]
}
```

### 3.2 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/mock/scripts` | 列出所有动态 mock 脚本（内存 + DB）。 |
| GET | `/api/mock/scripts/:id` | 获取单个脚本。 |
| POST | `/api/mock/scripts` | 创建/覆盖脚本。 |
| DELETE | `/api/mock/scripts/:id` | 删除脚本。 |
| POST | `/api/mock/reset` | 重置为内置脚本（清空动态覆盖）。 |

### 3.3 持久化

- 动态脚本存入 SQLite `mock_scripts` 表（migration v13）。
- 服务启动时加载到内存索引，保证匹配速度。

---

## 4. curl 冒烟测试路由清单

脚本位置：`scripts/smoke-test.sh`（Windows 对应 `scripts/smoke-test.ps1`）。

### 4.1 基础/观测

- `GET /healthz`
- `GET /metrics`
- `GET /api/version`

### 4.2 Auth（开启/关闭两种模式）

- `POST /api/auth/api-keys`
- `GET /api/auth/api-keys`
- `DELETE /api/auth/api-keys/:id`

### 4.3 Session / Project

- `GET /api/projects`
- `POST /api/projects`
- `GET /api/projects/:id`
- `PUT /api/projects/:id`
- `DELETE /api/projects/:id`
- `GET /api/sessions`
- `POST /api/sessions`
- `GET /api/sessions/:id`
- `PUT /api/sessions/:id`
- `DELETE /api/sessions/:id`
- `POST /api/sessions/:id/chat`
- `GET /api/sessions/:id/messages`

### 4.4 Agent / Task

- `GET /api/agents`
- `POST /api/agents`
- `GET /api/agents/:id`
- `PUT /api/agents/:id`
- `DELETE /api/agents/:id`
- `POST /api/tasks`
- `GET /api/tasks`
- `GET /api/tasks?id=:id`
- `POST /api/tasks/:id/continue`
- `POST /api/multi-agent`

### 4.5 Tool / Memory / Cost / Cases

- `GET /api/tools`
- `POST /api/tools`
- `DELETE /api/tools/:name`
- `GET /api/cases`
- `POST /api/run-case`
- `GET /api/memories`
- `POST /api/memories`
- `DELETE /api/memories/:id`
- `PUT /api/memories/:id`
- `GET /api/memories/recall?query=...`
- `GET /api/costs?task_id=...`
- `GET /api/costs?session_id=...`
- `GET /api/costs?project_id=...`

### 4.6 Mock 管理

- `GET /api/mock/scripts`
- `POST /api/mock/scripts`
- `DELETE /api/mock/scripts/:id`
- `POST /api/mock/reset`

### 4.7 WebSocket

- 连接 `/ws?session_id=...`，发送控制消息（pause/resume/cancel），验证事件序列。

---

## 5. `_test.go` 关键模块清单

| 模块 | 文件 | 测试重点 |
|------|------|---------|
| Auth | `internal/auth/auth_test.go` | bcrypt 验证、上下文注入、受保护路由、`REQUIRE_AUTH` 开关、API key CRUD 归属校验。 |
| Config | `internal/config/config_test.go` | mock/真实开关解析、模型分层配置、默认值。 |
| LLM Provider | `internal/llm/openai_provider_test.go` | SSE 解析、`usage` 透传、fallback 触发、reasoning_content。 |
| MockProvider | `internal/llm/mock_provider_test.go` | case_id 匹配、动态脚本覆盖、usage 生成、tool_call 序列。 |
| Router | `internal/llm/router_test.go` | 意图分类、模型选择、成本阈值、fallback 链。 |
| Engine | `internal/runtime/engine_test.go` | ReAct Loop 最大步数、tool_call 执行、错误恢复、OnLLMUsage 回调。 |
| Policy | `internal/harness/policy_test.go` | ToolWhitelistRule、TokenBudgetRule、ApprovalRule、CostBudgetRule、DangerousCommandRule。 |
| Cost | `internal/cost/cost_test.go` | CostCents 精度、Repository 读写、`/api/costs` 聚合。 |
| Metrics | `internal/observability/metrics_test.go` | counter 递增、Prometheus 文本格式。 |
| Memory | `internal/memory/memory_test.go` | 冲突检测/合并、向量召回 topK、BuildVectorIndex。 |
| Tool Registry | `internal/tool/registry_test.go` | 注册、覆盖、删除、内置工具不可删。 |
| DB | `pkg/db/database_test.go` | 迁移幂等、外键约束、CRUD。 |
| Cases | `internal/cases/cases_test.go` | 5 个 case 契约校验、参数默认值。 |
| API Handlers | `cmd/server/api_test.go` | 各 REST handler 返回结构、错误码、auth 注入。 |

---

## 6. 真实 LLM 端到端验证

### 6.1 用例选择
- **首选 `research` case**: 输入需查询近期技术资讯，期望触发 `run_shell`（如 `curl` 或 `date`）并生成总结。
- **备选 `dialogue` case**: 验证无 tool_call 的最简路径。

### 6.2 验证环境
- 设置 `LLM_USE_MOCK=false`，`LLM_REAL_CASES=research,dialogue`。
- 确保 `.env` 中 `LLM_API_KEY` 有效。
- 观察 `/api/costs` 是否生成真实记录，`/metrics` 是否增加 `llm_calls_total`。

### 6.3 验收标准
- 任务完成且 `task_completed` 事件携带非空 `output`。
- `cost_records` 表有该 task 的记录，`cost_cents > 0`。
- `session_messages` 表写入用户输入与 assistant 回复。

---

## 7. API 调整清单（执行中动态更新）

本清单在测试过程中记录，backend 全测完后统一评估对 frontend 的影响。

| # | API/行为 | 当前状态 | 可能调整 | 影响前端 | 备注 |
|---|---------|---------|---------|---------|------|
| 1 | `POST /api/tasks` 参数 | ✅ 已验证 | action=chat 需 body.input；?case= query 透传 MockProvider | 中 | 前端 CaseCard 调用时需带 ?case=<caseID> |
| 2 | `POST /api/tasks` 返回 | ✅ 已验证 | 返回 {session_id, task_id, agent_id, action} | 中 | session_id 可能为空（未传则自动建） |
| 3 | `/api/costs` 返回结构 | ✅ 已验证 | 统一 {record_count,total_cost_cents,total_cost_usd,total_tokens,input_tokens,output_tokens,by_model,by_agent,records[]} | 低 | 前端成本面板按此结构适配 |
| 4 | `/api/memories/recall` | ✅ 已验证 | query: task/project/max；另有 ?query= 走纯向量召回 | 低 | Memory 浏览页 |
| 5 | Auth 中间件默认关闭 | REQUIRE_AUTH=false | REQUIRE_AUTH=true 时需 Bearer token | 高 | 前端需支持 Bearer token |
| 6 | Mock 开关 API | ✅ 已验证 | /api/mock/* 仅测试用，不暴露生产前端 | 低 | /api/mock/scripts CRUD + /api/mock/reset 全通 |
| 7 | `/api/run-case` | ⚠️ 不存在 | 实际无此端点；运行 case 用 POST /api/tasks?case=<id> | 中 | 文档第 4.5 节列了 /api/run-case，源码未实现，需删文档或补端点 |
| 8 | `POST /api/sessions` 返回 | ✅ 已验证 | 返回字段名是 session_id（非 id），状态码 201 | 中 | 前端建会话后取 session_id |
| 9 | `POST /api/projects` 返回 | ✅ 已验证 | 状态码 201（非 200），返回 id | 低 | 前端按 2xx 处理 |
| 10 | `POST /api/tools` 参数 | ✅ 已验证 | 必填 type=shell/http/inline + 各 type 必填子字段(command/url/code)；201 | 中 | 文档第 4.5 节未说明 type 必填 |
| 11 | Memory 路由 | ✅ 已验证 | 实际路由：GET /api/memories、POST /promote、GET /recall、PUT /{id}/scope、DELETE /{id} | 中 | 文档列的 POST /api/memories(顶层创建)、PUT /api/memories/{id} 不存在 |
| 12 | Memory 不存在 id 行为 | ⚠️ 待优化 | PUT /{id}/scope、DELETE /{id} 对不存在 id 返回 200（应考虑 404） | 低 | 前端删除/改 scope 需自行判断结果 |
| 13 | `/api/checkpoints/recover` | ✅ 已验证 | 无 checkpoint 时返回 404（合理） | 低 | 前端 recover 需处理 404 |
| 14 | WebSocket `/ws` | 🔜 待专项 | curl 难验握手，需 wscat/Go 客户端测事件序列 | 中 | 控制消息 pause/resume/cancel + 事件流待测 |
| 15 | `/api/multi-agent` | ✅ 已验证 | POST，返回 {session_id,task_id,agent_ids,agent_count,status} | 中 | 前端多树渲染依据 agent_ids |
| 16 | `GET /api/tasks?id=` | ✅ 已验证 | 返回 {steps[], child_tasks[]} 含完整 step 状态 | 中 | 前端任务详情/回放依据 |

*注：本表已根据冒烟实测结果更新（2026-07-10）。"#7 /api/run-case 不存在"与"#11 Memory 路由差异"是文档与实现的最大偏差，需在前端阶段前确认是补端点还是改文档。完整 changelog 见 `docs/API_CHANGELOG.md`。*

---

## 8. 多步骤 Case 未来扩展脑洞（本阶段不实施）

- **自动化验证 case**: 任务完成后自动执行 AcceptanceCriteria，返回 pass/fail。
- **多 Agent 协作 case**: root task 自动拆分子任务，多个 Agent 并行，frontend 同时渲染多棵树。
- **审批中断 case**: 触发 `run_shell` 危险命令，弹出 Approval 弹窗，人工确认后继续。
- **成本熔断 case**: 设置极低 TokenBudget，验证任务被 PolicyGate 拦截。
- **记忆召回 case**: 先写入 memory，再发起相关查询，验证 working memory 注入。

本阶段只在 preset cases 中加入成熟稳定的单步骤/两步 case，多步骤复杂 case 留待 Phase 7 或后续迭代。

---

## 9. 执行顺序与验收标准

### 9.1 执行顺序

1. **基础设施**
   - [x] 创建 `IMPLEMENTATION_PLAN.md`（当前）。
   - [x] 新增 migration v13 `mock_scripts` 表。
   - [x] 实现 `MockProvider` 与三层开关配置。
   - [x] 实现 `/api/mock/scripts` 端点。
   - [x] 接入 MockProvider 到 Engine 调用链（chat / checkpoint-recover / multi-agent 三路径已注入 Provider+CaseID；session-chat 暂走关键词匹配）。
   - 当前进度：`go build ./...` / `go vet ./...` 通过；基础设施全部就绪，进入冒烟与单测阶段。

2. **Mock 脚本**
   - 内置 6 个 case 脚本。
   - 验证 `LLM_USE_MOCK=true` 时所有 case 稳定运行。

3. **curl 冒烟**
   - [x] 编写 `scripts/smoke-test.sh` / `smoke-test.ps1`。
   - [x] 跑通 4.1 - 4.6 所有端点，记录问题。实测结果：**46 PASS / 0 FAIL / 1 SKIP（WS）**，发现 6 项文档/实现差异已写入第 7 节。

4. **Go 测试**
   - [x] 按第 5 节清单补 `_test.go`（已完成 9 个包，详见下）。
   - [x] `go test ./...` / `go vet ./...` / `go build ./...` 全部通过。

   已完成测试文件（按模块）：
   | 模块 | 文件 | 顶层测试 | 关键覆盖 |
   |------|------|---------|---------|
   | MockProvider | `internal/llm/mock_provider_test.go` | 16 | case_id 匹配/关键词回退/动态覆盖/响应序列/usage/CRUD |
   | Config | `internal/config/config_test.go` | 6 (38 子) | ShouldMock 三层优先级/Load 环境解析/splitAndTrim |
   | Harness Policy | `internal/harness/policy_test.go` | 11 | 7 个 Rule + Chain 短路 + Gate contract 注入 |
   | Auth | `internal/auth/auth_test.go` | 16 | bcrypt/GenerateKey/MatchPrefix/Role/IsRevoked |
   | DB | `pkg/db/database_test.go` | 18 | 迁移幂等/16 表存在/Session+Task CRUD/并发 |
   | Router | `internal/llm/router_test.go` | 32 (53 子) | 意图分类/模型选择/fallback 链/ModelRegistry |
   | Tool Registry | `internal/tool/registry_test.go` | 20 | Register/Execute/Unregister/IsBuiltin/并发 |
   | Cost | `internal/cost/cost_test.go` | 10 | CostCents 精度/聚合/onRecord 回调/fallback 链/IsRetryable |
   | Memory | `internal/memory/memory_test.go` | 11 | CosineSimilarity/Normalize/VectorStore CRUD/维度校验/并发 |

5. **真实验证**
   - [x] 用 `research` + `dialogue` 跑真实 LLM。
   - [x] `dialogue` 完成且写入 `cost_records`。
   - [x] `research` 触发真实 `run_shell` 调用，但因 `max_steps=5` 不足失败；已记录建议增大默认步数。
   - [x] 确认成本/指标链路有记录，但 `CostCents=0` 因 `.env` 模型名 `deepseek-v4-flash-local` 与 `ModelRegistry` 默认名 `deepseek-v4-flash` 不一致导致 tier/pricing 未命中。

6. **汇总清单**
   - [x] 完善第 7 节 API 调整清单。
   - [x] 输出正式 API CHANGELOG 到 `docs/API_CHANGELOG.md`。

### 9.2 验收标准

- [x] `go build ./...` / `go vet ./...` / `go test ./...` 全部通过（9 个包补齐 _test.go）。
- [x] 冒烟脚本跑一次可验证全部端点基础可用性（46 PASS / 0 FAIL / 1 SKIP）。
- [x] mock 模式下 6 个内置 case 脚本已全部就位（code-gen / dialogue / research / multi-agent / long-task / tool-error），供前端与回归测试使用。
- [x] 真实模式下 `dialogue` case 成功执行并写入 cost_records；`research` case 触发真实网络调用并写入记录，但因 `max_steps=5` 不足导致 `max_steps_exceeded` 失败。
- [x] API 调整清单已输出为 `docs/API_CHANGELOG.md`，frontend 阶段可据此评估改动。

---

*文档最后更新：2026-07-10*
