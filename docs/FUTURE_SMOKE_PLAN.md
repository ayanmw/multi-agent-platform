# Multi-Agent Platform — 冒烟测试覆盖度盘点与后续计划

> 编排者：leader（基于 mock / real-llm 两轮冒烟结果 + 代码走读）
> 生成日期：2026-07-15
> 基准代码：commit `a2475e7`

---

## 1. 为什么做这个盘点

真实 LLM 第一轮冒烟（`scripts/real-llm-smoke.sh`）把核心链路跑通了，但 5 个场景集中在"单 agent + 基础 tool"和"mock 化的多 agent 入口"。多 Agent 协调、复杂 Policy、Auth 细粒度、Memory、可观测性等模块要么只做了 mock 表面验证，要么压根没端到端跑过。

本文档给出：
- 当前冒烟测试已覆盖的能力与缺口
- 需要更复杂 case 才能覆盖的场景
- 因功能尚未完善而暂时无法测试的阻塞项
- 下一轮冒烟测试的优先级与建议脚本

为减少后续重复探索成本，所有判断尽量给出**对应文件:行号**或**已有脚本**。

---

## 2. 当前冒烟测试脚本全景

| 脚本 | 主要维度 | mock/real | 备注 |
|------|----------|-----------|------|
| `scripts/smoke-test.sh` | 基础服务启动 + /healthz + 简单 HTTP | mock | 最早的基础冒烟 |
| `scripts/ws-smoke.go` | WebSocket 事件流顺序 | mock | 维度 A |
| `scripts/policy-smoke.sh` | PolicyGate 拦截 | mock | 维度 B，有 Windows 路径缺陷 |
| `scripts/multi-agent-smoke.sh` | 多 Agent 编排入口、子任务树 | mock | 维度 C，子任务树仍然不准 |
| `scripts/auth-smoke.sh` | API key 创建/使用/吊销 | mock | 维度 D |
| `scripts/cases-regression.sh` | 6 内置 mock case 回归 | mock | 维度 E |
| `scripts/real-llm-smoke.sh` | 真实 LLM 5 场景 | real | 第 10 章 |
| `scripts/smoke-test-auth.sh` | Auth 开启下的基础冒烟 | mock | 补充 auth |
| `scripts/ws-auth-smoke.sh` / `.go` | Auth 开启下的 WS 连接 | mock | 新增，auth 维度 |
| `scripts/multi-agent-auth.sh` | Auth 开启下的多 agent | mock | 新增，auth+编排 |
| `scripts/policy-auth.sh` | Auth 开启下的 policy | mock | 新增，auth+policy |

---

## 3. 已覆盖能力清单

以下标注 **mock** / **real** / **单测** / **未覆盖**。"已覆盖"指至少有一次端到端冒烟跑通。

### 3.1 HTTP 基础与 Session 管理

| 能力 | 状态 | 证据 |
|------|------|------|
| `GET /api/sessions` 列表 | mock | `api.go:127` `handleSessions` |
| `POST /api/sessions` 创建 | mock | `api.go:169` |
| `GET/PUT/DELETE /api/sessions/{id}` | mock | `api.go:218` |
| workspace 目录自动创建 | mock/real | `resolveWorkspaceDir`/`real-llm-smoke.sh` 场景1 |
| `/s/{session_id}/...` 静态文件 | mock | `main.go:736`（未在冒烟中显式测） |
| `/healthz` `/health` `/metrics` | mock | `main.go:664-702` |

### 3.2 单 Agent ReAct Loop

| 能力 | 状态 | 证据 |
|------|------|------|
| `POST /api/tasks` 普通 chat | mock/real | `main.go:387-505` |
| `write_file` tool_call | mock/real | `builtin.go`、real 场景1 |
| `read_file` tool_call | real | real 场景1 间接触发 |
| `run_shell` tool_call | mock/real | real 场景2 |
| 事件序列 think→tool_call→observation→final | mock/real | TEST_REPORT 维度 A/E |
| `max_steps_exceeded` 失败路径 | mock | tool-error case |
| `task_failed` 事件 | mock | policy-smoke |
| SSE 流式 delta | real | real-llm-smoke |
| `reasoning_content` / `reasoning` 字段 | real | commit a2475e7, `client.go` |

### 3.3 Router / 模型分层（Phase 6）

| 能力 | 状态 | 证据 |
|------|------|------|
| Router 注入 Engine / Orchestrator | real | `main.go:382`, `orchestrator.go:335` |
| intent classification + tier 选择 | real | `router.go:145` |
| `model_routed` 事件 | real | `engine.go:1227` |
| primary 命中 cfg.LLMModel | real | Router 日志选 deepseek-v4-flash-local |
| fallback chain pro→cfg.LLMModel | **已代码修复，未真实验证** | D1, commit a2475e7 |
| Router classifier 在 mock 下走 mock script | mock | `builtin:router-classifier` |

### 3.4 Cost / 可观测性

| 能力 | 状态 | 证据 |
|------|------|------|
| CostRecord 写入 + 持久化 | mock/real | `cost_tracker.go`, `cost/repository.go` |
| `/api/costs` 查询 | mock/real | `api.go:659` |
| cost_cents 整数列 round-trip | 单测 | `pkg/db/database_test.go` |
| float64 USD 精度（小对话非 0） | **已代码修复，未真实验证** | D3, commit a2475e7 |
| `/api/models/prices` 查看/修改价格 | 单测 | `cmd/server/model_price_api.go`（UI 有 Dialog，未端到端跑） |
| Prometheus `/metrics` | mock | `main.go:693` |

### 3.5 Policy / 安全门

| 能力 | 状态 | 证据 |
|------|------|------|
| PathTraversalRule（`..`） | mock | policy-smoke |
| DangerousCommandRule（rm -rf） | mock | 但走审批超时、非硬拦截 |
| FileScopeRule 相对路径 | mock | Windows 对 Unix 绝对路径有缺陷 |
| ApprovalRule 审批超时拒绝 | mock | policy-smoke |
| CostBudgetRule 加入 PolicyChain | **单测有，冒烟未触发** | `main.go:1142` 已接入 |
| TokenBudgetRule / ToolWhitelistRule | **未覆盖** | API body 已支持但未在冒烟中传入 |
| 审批 approved（放行）路径 | **未覆盖** | mock 下被转成审批超时 |

### 3.6 多 Agent 编排

| 能力 | 状态 | 证据 |
|------|------|------|
| `POST /api/tasks action=multi-agent` | mock | `main.go:428` |
| TaskDecomposer 拆分 agent specs | mock | `orchestrator.go` |
| 并发拉起多个 agent goroutine | mock/real | `RunBlocking` |
| root task 状态聚合 | mock | `orchestrator.go:182` |
| child_tasks 关联（parent_task_id） | **单测/部分修复，真实未验证** | `SaveTaskMeta` 已设，真实多 agent 未 green |
| AgentBus agent 间消息传递 | **未覆盖** | `agentBus.go` 有实现但无调用方 |
| Strategy pipeline/parallel | **未覆盖** | 字段存在但 `RunBlocking` 总是并行 |
| multi-agent 真实 LLM 端到端 green | **未验证** | real 场景3 首轮 failed，D1 后待第三轮 |

### 3.7 Auth

| 能力 | 状态 | 证据 |
|------|------|------|
| Bearer 校验 | mock | `auth-smoke.sh` 16/16 |
| API key 创建/列出/吊销 | mock | `auth-smoke.sh` |
| 受保护端点写操作拦截 | mock | `DefaultProtectedRoutes` |
| Auth 开启下的 smoke 变体 | mock | `ws-auth-smoke`, `multi-agent-auth`, `policy-auth` |
| RBAC role 区分 admin/user/viewer | **未覆盖** | `auth.Role` 定义未接入中间件 |
| GET 敏感读端点保护 | **未覆盖** | `auth_http.go:94` 一律 exempt GET |
| `PUT /api/models/prices` 写保护 | **已加 protectedRoute，未测** | `auth_http.go:71` |

### 3.8 Memory / RAG（Phase 6）

| 能力 | 状态 | 证据 |
|------|------|------|
| CRUD `/api/memories` | **未覆盖** | `api.go:469-535` |
| MemoryRecall 注入系统提示 | mock | `main.go:438` 调用了 BuildWorkingMemory |
| Embed `/api/memories/{id}/embed` | **未覆盖** | `api.go:636` |
| Vector search | **未覆盖** | 依赖 embed provider |
| MemoryUpdated/MemoryDeleted 事件 | **未覆盖** | `api.go:600` |

### 3.9 Tools / Sandbox

| 能力 | 状态 | 证据 |
|------|------|------|
| 内置 3 个 tools（run_shell/write_file/read_file） | mock/real | real 场景1/2 |
| Docker sandbox shell | **未覆盖（环境通常无 Docker）** | `main.go:355-367` |
| 运行时 tool 注册（Phase 5） | **未覆盖** | `/api/tools` 已注册但冒烟未改 |

### 3.10 控制与生命周期

| 能力 | 状态 | 证据 |
|------|------|------|
| cancel task via WS control | **部分实现，未测试** | `main.go:96` 有 cancel 分支但无 handler 逻辑 |
| pause/resume | **未实现** | `main.go:84` TODO |
| checkpoint save/load | **未测试** | `checkpointMgr` 已注入 but 未验证崩溃恢复 |

---

## 4. 未覆盖的复杂 case（按优先级）

### 🔴 高优先级（影响功能正确性）

| # | case | 当前问题/风险 | 目标脚本/扩展 |
|---|------|-------------|--------------|
| 1 | **✅ 修复 multi-agent 真实 LLM green** | researcher 不再依赖未注册 web_search；tool_call Arguments 回写 history 前统一 sanitize，场景 3 completed（commit `0d19eb7`） | `scripts/real-llm-smoke.sh` 场景3 |
| 2 | **AgentBus 真实 agent 间通信** | `sendAgentMessage` 无调用方，researcher→writer 数据不传递；需构造 case 让 writer 真正消费 researcher 产出 | 新增 `scripts/agentbus-smoke.sh` 或扩展 multi-agent case |
| 3 | **子任务树独立回放** | child_tasks 已非空，但子任务 status/steps 仍挂在 root taskID 下；前端无法点开单个 agent 历史 | 扩展 `multi-agent-smoke.sh` 断言每个 agent 子任务有独立 steps、parent_task_id 正确、子任务 status=completed |
| 4 | **CostBudgetRule 端到端触发** | 已接入但在 smoke 中未设置 `cost_budget_usd` | 扩展 `policy-smoke.sh` 或新增 `cost-budget-smoke.sh` |
| 5 | **TokenBudgetRule / ToolWhitelistRule 触发** | API body 已支持 but 未在冒烟中传入 | 扩展 `policy-smoke.sh` body 传 `token_budget` / `allowed_tools` |
| 6 | **审批 approved 路径** | mock 下 DangerousCommand 走审批超时拒绝，approved 放行分支未测 | 扩展 `policy-smoke.sh` 加 approve decision WS 消息 |
| 7 | **真实 LLM multi-agent 产物断言** | 场景 3 只断言 status=completed；未验证 writer 是否真正使用了 researcher 的结果 | 扩展 `real-llm-smoke.sh` 场景3：断言最终答案包含 research 关键词或 writer 产物文件存在 |

### 🟡 中优先级（鲁棒性 / 可观测性）

| # | case | 当前问题/风险 | 目标脚本/扩展 |
|---|------|-------------|--------------|
| 8 | **cancel/pause/resume WS 控制** | cancel 只有 registry 无 handler；pause/resume 未实现 | 新增 `scripts/control-smoke.sh`；需先实现 engine 控制 |
| 9 | **多 Agent 并发节流（≥3 agent）** | 无并发限制，真实 LLM 下易 429 | `scripts/real-llm-smoke.sh` 加 3 agent 并发场景或独立脚本 |
| 10 | **HTTP 429/5xx 重试** | 当前失败直接返回，没有重试 | 需先实现 D7，再新增 `scripts/retry-smoke.sh` |
| 11 | **WS 背压 / 慢客户端** | hub.go 丢 delta，终态事件可能被丢 | 新增 `scripts/ws-backpressure-smoke.go` |
| 12 | **Memory CRUD + embed + search** | API 存在但冒烟未覆盖 | 新增 `scripts/memory-smoke.sh` |
| 13 | **模型价格编辑 API 鉴权与生效** | UI 已做 but 端到端未跑；鉴权写保护已加 | 扩展 `auth-smoke.sh` 或新增 `model-price-smoke.sh` |
| 14 | **Docker sandbox shell** | 功能检票在启动 but 未验证隔离 | 需环境有 Docker；新增 `scripts/docker-sandbox-smoke.sh` |

### 🟢 低优先级（代码质量 / 长期）

| # | case | 当前问题/风险 | 目标脚本/扩展 |
|---|------|-------------|--------------|
| 15 | **RBAC role 区分 admin/user** | Role 定义未接入中间件 | 需先实现；新增 `scripts/rbac-smoke.sh` |
| 16 | **GET 请求 Auth 保护** | `auth_http.go:94` 一律放行 GET | 需先决策；扩展 `auth-smoke.sh` |
| 17 | **checkpoint 崩溃恢复** | CheckpointManager 注入 but 未验证 | 需模拟崩溃；新增 `scripts/checkpoint-smoke.sh` |
| 18 | **Strategy pipeline 顺序执行** | 字段存在但总是并行 | 需先实现；新增 `scripts/strategy-smoke.sh` |

---

## 5. 功能未完善导致暂时无法测试的阻塞项

| 阻塞项 | 位置 | 状态 | 何时可测 |
|--------|------|------|----------|
| Engine pause/resume 未实现 | `main.go:84` TODO | 代码占位 | 实现后 |
| AgentBus 无真实路由调用方 | `orchestrator.go` / `agentBus.go` | 包装好但未在 runAgent 中消费 | 接入后 |
| Strategy 字段无效（总是并行） | `orchestrator.go:158` | 字段忽略 | 实现 pipeline/parallel 调度后 |
| WorkerPool.dispatch 占位 | `internal/pool/pool.go:167-168` | `_ = ctx; _ = cancel` | 实现后 |
| RBAC 中间件未接入 | `auth_http.go:94` | Role 只定义未用 | 实现后 |
| GET 请求一律 exempt | `auth_http.go:94` | 设计选择 but 中危 | 如果需要改设计后 |
| Docker sandbox 环境依赖 | `main.go:355` | 环境通常无 Docker | CI 有 Docker 后 |

---

## 6. 下一轮真实 LLM 冒烟测试建议

### 6.1 立即执行（本轮无新增开发）

目标：**验证 R7/R8 修复后 multi-agent 真实 LLM green**

```bash
bash scripts/real-llm-smoke.sh
```

预期断言：
- 场景3 multi-agent：`status=completed`，无 `web_search` / `tool not found` 错误，无 `400 Unterminated string`
- 场景4 dialogue：`GET /api/costs?task_id=...` 返回 `total_cost_usd > 0`（浮点数）

### 6.2 下一轮 smoke 开发优先级（建议顺序）

1. **扩展 `policy-smoke.sh`**：
   - body 传 `cost_budget_usd` / `token_budget` / `allowed_tools`
   - 通过 WS 发送 approve decision，验证 approved 路径
2. **扩展 `multi-agent-smoke.sh`**：
   - 断言每个 child task 有独立 task_id、子任务 status=completed、parent_task_id 正确
   - 断言子任务 steps 不合并到 root task
3. **新增 `agentbus-smoke.sh`**（mock / real）：
   - 构造两个 agent，agentA finish 后通过 AgentBus 给 agentB 发消息
   - 断言 agentB 的 input 包含 agentA 结果
4. **新增 `memory-smoke.sh`**（mock，可选 embed provider mock）：
   - POST /api/memories → GET /api/memories → PUT → DELETE → embed
5. **新增 `control-smoke.sh`**：需先实现 cancel handler / pause/resume
6. **新增 `ws-backpressure-smoke.go`**：模拟慢客户端，断言 task_completed 必达

---

## 7. 对"覆盖当前系统设计"的综合判断

| 系统模块 | 当前覆盖度 | 说明 |
|----------|-----------|------|
| 单 Agent ReAct Loop | **80%** | 基础路径 mock+real 全绿；pause/resume/cancel、tool-error 真实事件仍可补 |
| Router 分层模型 | **85%** | primary + fallback chain 真实验证，multi-agent pro→efficient 回退路径真实跑通 |
| 多 Agent 编排 | **60%** | 入口真实 LLM green；child tree 结构基本正确，但子任务独立 steps/status、AgentBus、strategy、并发控制仍未验证 |
| Policy 安全门 | **50%** | 主要规则 mock 测试；budget/whitelist/approved 路径未测；Windows 路径缺陷未修 |
| Auth | **70%** | Bearer + key 生命周期完整；RBAC、GET 保护、细粒度 route 未测 |
| Cost / Metrics | **70%** | 真实 LLM 下 cost 链路完整；价格编辑 API 真实验证未做 |
| Memory / RAG | **20%** | API 存在，冒烟几乎未碰 |
| Tools / Sandbox | **30%** | 3 内置工具 ok；Docker sandbox 未验证；动态注册未测 |
| WS 事件与背压 | **60%** | 顺序字段 ok；背压/慢客户端未测 |

**结论**：当前冒烟测试对"入口 API 能跑、单 agent 能完成、基础安全能拦截、Router fallback chain 能工作、真实 LLM multi-agent 能完成"覆盖较好。接下来应优先补齐 **AgentBus 真实通信、子任务树独立回放、复杂 Policy 组合（budget/whitelist/approved）、Memory CRUD**，并提升 **WS 背压 / 并发节流**等鲁棒性 case。
