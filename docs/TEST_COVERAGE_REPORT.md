# Multi-Agent Platform — 测试覆盖率审计报告

> 生成日期：2026-07-12
> 审计范围：Go 后端全量包 + 前端 Vue 3 + 脚本端到端冒烟
> 审计方式：`go test -cover ./...` + 逐函数 `go tool cover -func` + 源码 LOC 统计 + 已有评测报告交叉核对
> 后端版本：commit `231e403`（feat: Phase 2.5 UI 修复批次 — F5/F6/F10/F11）

---

## 0. 摘要 (TL;DR)

| 指标 | 数值 |
|------|------|
| Go 源码总量（非测试） | ~18,846 LOC |
| 单元测试文件 | 10 个（~4,972 行） |
| 已测试包（有 `_test.go`） | 10 / 21 |
| 单测覆盖率 ≥ 80% 的包 | 3（memory, config, fib demo） |
| 单测覆盖率 0% 的包 | 11（含 runtime/orchestrator/ws 等核心） |
| 端到端冒烟脚本 | 6 个（共 ~119K 字节，46+34 PASS） |
| 前端运行时测试 | 0（仅 vue-tsc 类型检查） |

**结论**：项目已建立完整端到端冒烟网（HTTP/WS/Policy/MultiAgent/Auth/Cases 6 维度），核心单测覆盖在 **辅助层**（memory/config/router/policy）较扎实，但 **核心运行时层**（runtime/orchestrator/pool/ws/harness 大部）单测覆盖几乎为零，仅靠端到端脚本兜底。前端零运行时测试。

---

## 1. 测试矩阵总览

### 1.1 单元测试 — 已有测试的包

| 包 | 单测文件 | 测试 LOC | 覆盖率 | 等级 | 备注 |
|----|---------|---------|--------|------|------|
| `internal/memory` | memory_test.go | 368 | **100.0%** | ✅ 优 | 向量存储 + scope 全覆盖 |
| `internal/config` | config_test.go | 448 | **83.5%** | ✅ 优 | .env / 环境变量 / 默认值链路 |
| `fib` + `project/fib` | fib_test.go | ~60 | **80.0%** | ✅ 优 | demo，非生产代码 |
| `internal/cost` | cost_test.go | 421 | **44.5%** | 🟡 中 | tracker/fallback 充分，**repository 全 0%** |
| `internal/tool` | registry_test.go | 548 | **32.7%** | 🟡 中 | registry 100%，**sandbox/dynamic/builtin 大部分 0%** |
| `internal/llm` | router_test.go + mock_provider_test.go | 1,420 | **30.0%** | 🟡 中 | router/model_profile 100%，**真实 provider 全 0%** |
| `pkg/db` | database_test.go | 670 | **25.6%** | 🟡 中 | database.Init/schema 充分，**persistence 大部分 0%** |
| `internal/harness` | policy_test.go | 736 | **19.0%** | 🔴 低 | policy_chain 充分，**harness/approval/recall/promotion/heartbeat 几乎全 0%** |
| `internal/auth` | auth_test.go | 361 | **8.7%** | 🔴 低 | auth.go 业务逻辑 100%，**auth_http.go + sqlite_store.go 全 0%** |

### 1.2 单元测试 — 无测试文件的包（覆盖率 0%）

| 包 | 职责 | 源码 LOC | 优先级 | 影响面 |
|----|------|---------|--------|--------|
| `internal/runtime` | **ReAct Loop 引擎 + Step 状态机** | 1,954 | 🔴 P0 | 系统核心，bug 直达任务执行 |
| `internal/orchestrator` | 多 Agent 编排器 + AgentBus 适配 | 708 | 🔴 P0 | 多 Agent 拆分/子任务派发 |
| `cmd/server` | HTTP + WS 入口 + API handler | 2,951 | 🔴 P0 | 路由/鉴权/持久化拼装 |
| `internal/ws` | WebSocket Hub（connect/broadcast/disconnect） | 197 | 🟠 P1 | 事件流广播正确性 |
| `internal/pool` | Worker Pool 并发调度 | 187 | 🟠 P1 | 并发上限/退避 |
| `internal/cases` | 6 预设 Case 管理 | 266 | 🟡 P2 | case_id 匹配，已由端到端兜底 |
| `internal/observability` | 结构化日志 + Metrics | 238 | 🟡 P2 | 辅出层，bug 不影响主链路 |
| `pkg/event` | 事件结构体 + 工厂函数 | 42 | 🟡 P2 | 仅 1 个 `NewEvent`，POJO |
| `internal/agent` | Agent 类型定义 | 51 | 🟢 P3 | 纯数据结构 |
| `internal/version` | 版本字符串（go:embed） | 26 | 🟢 P3 | 编译期常量 |
| `cmd/e2e-test` | e2e 测试工具 | 359 | 🟢 P3 | 测试工具本身 |
| `scripts` | ws-smoke.go 归属包 | — | 🟢 P3 | 测试脚本 |
| `web` | Vue 3 前端 | ~3,000 | 🔴 见 §7 | 前端零运行时测试 |

---

## 2. 已充分测试的模块

### 2.1 `internal/memory` — 100% ✅

- **覆盖**：`memory_vector.go` / `vector_store.go` 全函数
- **为什么充分**：纯数据层，无外部依赖（DB/网络），测试可隔离；scope 过滤、向量召回、元数据操作全函数级覆盖。

### 2.2 `internal/config` — 83.5% ✅

- **覆盖**：.env 加载、环境变量优先级、默认值回退、类型转换
- **缺口**：少量 Windows 注册表/系统调用分支未覆盖（仅 ~16%）

### 2.3 `internal/llm/router.go` + `model_profile.go` — 88~100% ✅

- **覆盖**：意图分类（keywordClassify）、tier 映射、候选过滤、fallback 选择全路径
- **价值**：这是 LLM 路由决策的核心，单测保证了"输入意图 → 模型选择"的确定性。

### 2.4 `internal/harness/policy_test.go` — Policy 规则链 ✅

- **覆盖**：736 行测试覆盖 `PolicyChain` / `PolicyGate` / 各 `Rule.Evaluate()`
- **价值**：安全门单测扎实，但端到端集成见 §5 已暴露跨平台 bug（FileScopeRule Windows 路径放行）。

### 2.5 `internal/auth/auth.go` — 业务逻辑 100% ✅

- `IsValid` / `IsRevoked` / `GenerateAPIKey` / `VerifyPassword` / `MatchPrefix` 全覆盖
- **缺口**：`auth_http.go`（中间件）+ `sqlite_store.go`（持久化）全 0%，靠 auth-smoke.sh 端到端兜底（16 PASS）。

---

## 3. 部分测试的模块（覆盖率 < 60%）

### 3.1 `internal/cost` — 44.5% 🟡

| 子模块 | 覆盖率 | 缺口 |
|--------|--------|------|
| `cost_tracker.go`（核心累加 + 计算） | ~80%+ | `DailyReport` 未测 |
| `fallback.go`（退避链 + 重试判定） | ~95% | 几乎无缺口 |
| `repository.go`（SQLite + 内存仓储） | **0%** | 全部 CRUD 未测，仅端到端冒烟间接验证 |

**风险**：cost_repository 是成本数据持久化的唯一路径，端到端只验证"有记录"，不验证并发写、空字段、聚合查询正确性。

### 3.2 `internal/tool` — 32.7% 🟡

| 子模块 | 覆盖率 | 缺口 |
|--------|--------|------|
| `registry.go` | 100% | 无 |
| `builtin.go`（run_shell/write_file/read_file） | ~30% | 错误路径、超长输出、非零退出码未测 |
| `sandbox.go`（Docker 沙箱执行器） | **0%** | 全部未测，Phase 5 才启用 |
| `dynamic.go`（运行时 Tool 注册） | ~10% | `sanitizeInput` 未测 |

**风险**：`builtin.go:175` `executeShell` 非零退出码返回 `(result, nil)` 而非 error（已在 TEST_REPORT.md 高危 M8 记录），单测本可捕获但未覆盖该分支。

### 3.3 `internal/llm` — 30.0% 🟡

| 子模块 | 覆盖率 | 缺口 |
|--------|--------|------|
| `router.go` + `model_profile.go` | 88~100% | 几乎无 |
| `mock_provider.go` + `mock_store*.go` | ~50% | mock 工具自身，影响测试可信度 |
| `openai_provider.go` | **0%** | 真实 OpenAI Chat / ChatStream 未测 |
| `anthropic_provider.go` | **0%** | 737 LOC 全未测 |
| `provider_factory.go` / `provider_registry.go` | **0%** | 工厂注册未测 |

**风险**：真实 LLM provider（737+254=991 LOC）完全靠手测，SSE 解析、tool_call 字段映射、usage 解析的回归风险高。

### 3.4 `pkg/db` — 25.6% 🟡

| 子模块 | 覆盖率 | 缺口 |
|--------|--------|------|
| `database.go`（Init/schema） | ~70% | `Init` 的并发配置（busy_timeout/WAL）未测 → 已暴露 S3 bug |
| `migrate.go` | ~30% | 迁移链路部分 |
| `memory.go` / `memory_scope.go` | ~40% | scope 查询部分 |
| `persistence.go` | **~10%** | 906 LOC，仅 `QueryTaskByID` 81.8%，其余 CRUD 全 0% |

**风险**：这是已知重灾区。`persistence.go` 的 27 个函数中 26 个未单测，端到端已暴露 S4（step ID 碰撞）、S5（SaveTask 主键冲突）严重 bug。

### 3.5 `internal/harness` — 19.0% 🟡

| 子模块 | 覆盖率 | 缺口 |
|--------|--------|------|
| `policy*.go` | 充分 | — |
| `cost_budget_rule.go` | 充分 | 端到端未加入 PolicyChain（M2） |
| `harness.go`（891 LOC 主引擎装配） | **~5%** | 几乎全未测 |
| `approval.go`（576 LOC 审批流） | **0%** | 全未测 |
| `recall.go`（629 LOC 记忆召回） | **0%** | 26 个函数全 0% |
| `heartbeat.go`（473 LOC 心跳） | **0%** | 全未测 |
| `promotion.go`（234 LOC） | **0%** | 全未测 |
| `compressor.go`（302 LOC） | **0%** | 全未测 |

**风险**：harness 是"装配层"，把 policy/approval/recall/heartbeat 串起来，单测只测了 policy 子集，整体装配正确性靠端到端。

### 3.6 `internal/auth` — 8.7% 🔴

| 子模块 | 覆盖率 | 缺口 |
|--------|--------|------|
| `auth.go`（210 LOC 业务逻辑） | ~90% | 几乎无 |
| `auth_http.go`（332 LOC 中间件 + handler） | **0%** | 鉴权链路全靠端到端 |
| `sqlite_store.go`（332 LOC 持久化） | **0%** | 全部 CRUD 未测 |

**风险**：中间件 16 个函数全 0%，端到端已暴露 M4（GET 一律豁免）、M5（无 RBAC）、M6（key 枚举）等设计缺口。

---

## 4. 未测试的模块（按优先级排序）

### 🔴 P0 — 阻塞核心功能正确性

| # | 包 | LOC | 为什么重要 | 潜在 bug 类型 |
|---|----|-----|-----------|--------------|
| 1 | `internal/runtime` | 1,954 | ReAct Loop 引擎 + Step 状态机 + checkpoint + AgentBus | 死循环、状态机错误转移、并发竞态、context 取消不生效、max_steps 误判 |
| 2 | `internal/orchestrator` | 708 | 多 Agent 拆分 + 子任务派发 + AgentBus 路由 | child_tasks 丢失（S2）、Strategy 无效（M7）、agent_ids 为空（S5） |
| 3 | `cmd/server` | 2,951 | HTTP 路由 + WS handler + 鉴权拼装 + 持久化装配 | 路由漏注册、handler 参数校验缺失、TaskContract 字段透传断（M3） |

### 🟠 P1 — 影响可靠性

| # | 包 | LOC | 为什么重要 | 潜在 bug 类型 |
|---|----|-----|-----------|--------------|
| 4 | `internal/ws` | 197 | WebSocket Hub 广播 + 连接管理 | 广播漏消息、goroutine 泄漏、断线重连状态丢失 |
| 5 | `internal/pool` | 187 | Worker Pool 并发上限 + 退避 | 并发超限（API 限流）、退避失效、goroutine 阻塞 |

### 🟡 P2 — 辅助层 / 可延后

| # | 包 | LOC | 为什么重要 | 备注 |
|---|----|-----|-----------|------|
| 6 | `internal/cases` | 266 | case_id 匹配 + 关键词回退 | 已由 cases-regression.sh 6 PASS 兜底 |
| 7 | `internal/observability` | 238 | 结构化日志 + Metrics | bug 不影响主链路 |
| 8 | `pkg/event` | 42 | 事件工厂函数 | 仅 1 个 `NewEvent`，POJO |

### 🟢 P3 — 低风险

| # | 包 | LOC | 备注 |
|---|----|-----|------|
| 9 | `internal/agent` | 51 | 纯数据结构，编译器保证 |
| 10 | `internal/version` | 26 | 编译期 go:embed 常量 |
| 11 | `cmd/e2e-test` | 359 | 测试工具自身 |

---

## 5. 端到端冒烟测试覆盖范围

### 5.1 脚本总览

项目建立了 **6 个独立冒烟脚本**，每个独立端口 + 独立临时 DB，互不污染。最新一次端到端评测结果见 `docs/TEST_REPORT.md`（34 PASS / 8 FAIL / 3 SKIP）。

| 脚本 | 语言 | 端口 | 大小 | 覆盖维度 | 场景数 | 最近结果 |
|------|------|------|------|---------|--------|---------|
| `scripts/smoke-test.sh` | bash | 18080 | 17.7 KB | HTTP REST 全端点冒烟 | 30+ 端点 | 46 PASS / 0 FAIL / 1 SKIP |
| `scripts/smoke-test-auth.sh` | bash | 18092 | 19.1 KB | HTTP REST 全端点冒烟（REQUIRE_AUTH=true） | 30+ 端点 | 39 PASS / 0 FAIL / 0 SKIP |
| `scripts/ws-smoke.go` | Go | 18101 | 28.5 KB | WebSocket 事件流 | 3 场景 | 2 PASS / 1 FAIL（cancel 未实现） |
| `scripts/policy-smoke.sh` | bash | 18102 | 26.2 KB | Policy 安全门端到端 | 5 规则 + 2 SKIP | 3 PASS / 2 FAIL / 2 SKIP |
| `scripts/multi-agent-smoke.sh` | bash | 18103 | 22.2 KB | 多 Agent 编排（orchestrator） | 3 拆分场景 | 7 PASS / 5 FAIL（持久化 bug） |
| `scripts/auth-smoke.sh` | bash | 18104 | 21.5 KB | Auth 开启模式全链路（旧脚本，已弃用） | 16 检查点 | FATAL（log cleanup 时序 bug，被 smoke-test-auth.sh 替代） |
| `scripts/cases-regression.sh` | bash | 18105 | 10.6 KB | 6 预设 Case Mock 回归 | 6 Case | 6 PASS / 0 FAIL（long-task 预期已修正） |

### 5.2 API 端点冒烟覆盖矩阵

下表评估每个 API 端点在冒出脚本中的覆盖深度。覆盖等级：
- ✅ **5 脚本全过** — 多维度验证，行为确定
- 🟢 **3-4 脚本覆盖** — 主要路径有保障，少量边界未测
- 🟡 **1-2 脚本覆盖** — happy path 有保障，错误/边界路径薄弱
- 🔴 **仅 1 个脚本浅覆盖** — 仅端点存在性验证，无业务逻辑验证
- ❌ **无覆盖** — 未出现在任何冒出脚本中

| 端点 | 覆盖脚本数 | 等级 | 覆盖要点 | 盲区 |
|------|:---------:|:----:|---------|------|
| `GET /healthz` | 5 | ✅ | auth on/off 均通过，启动就绪检查 | 无 |
| `GET /metrics` | 2 | 🟡 | smoke-test + smoke-test-auth 均验证返回 200 | 字段完整性未断言 |
| `GET /api/version` | 2 | 🟢 | smoke-test + auth-smoke 均通过 | 无 |
| `GET /api/health` | 2 | 🟡 | smoke-test + smoke-test-auth 均验证返回 200 | 与 /healthz 功能重合，无差异化验证 |
| `POST /api/auth/api-keys` | 2 | ✅ | smoke-test（REQUIRE_AUTH=false）+ auth-smoke（REQUIRE_AUTH=true）均验证 201 + key 返回 | 无 |
| `GET /api/auth/api-keys` | 2 | 🟡 | 两个脚本都测了，但 auth-smoke 发现 **M6: 无 token 可枚举** | auth on 下应只返回当前用户的 keys |
| `DELETE /api/auth/api-keys/:id` | 2 | ✅ | 创建→吊销→已吊销再访问 全链路 | 不存在的 id → 404（auth-smoke 测了） |
| `GET /api/projects` | 1 | 🔴 | 仅 smoke-test 验证返回 200 | **auth on / 空列表 / 分页** 均未测 |
| `POST /api/projects` | 1 | 🔴 | 仅 smoke-test 验证 201 | **重复名称 / 空 body / 非法字段** 未测 |
| `GET /api/projects/:id` | 1 | 🔴 | 仅 smoke-test 依赖创建后查 | **不存在的 id / 其他用户的 id（RBAC）** 未测 |
| `PUT /api/projects/:id` | 1 | 🔴 | 仅 smoke-test 创建后改 | **并发更新 / 不存在的 id** 未测 |
| `DELETE /api/projects/:id` | 1 | 🔴 | 仅 smoke-test 创建后删 | **不存在的 id / 级联删除 session/task** 未测 |
| `GET /api/sessions` | 1 | 🔴 | 仅 smoke-test 验证返回 200 | **auth on / 空列表 / 按 user_id 过滤** 均未测 |
| `POST /api/sessions` | 1 | 🔴 | 仅 smoke-test 创建 | **project_id 不存在 / 空 user_input** 未测 |
| `GET /api/sessions/:id` | 1 | 🔴 | 仅 smoke-test 依赖创建后查 | **不存在的 id / 其他用户的 session（RBAC）** 未测 |
| `GET /api/sessions/:id/messages` | 1 | 🔴 | 仅 smoke-test 创建 session + chat 后查 | **空 messages / 多 turn 顺序 / 分页** 均未测 |
| `POST /api/sessions/:id/chat` | 1 | 🔴 | 仅 smoke-test 触发一次 chat | **多 turn 上下文连续性 / session 已完成后再 chat** 未测 |
| `DELETE /api/sessions/:id` | 1 | 🔴 | 仅 smoke-test 创建后删 | **级联清理 task/step/message / 不存在的 id** 未测 |
| `GET /api/agents` | 1 | 🔴 | 仅 smoke-test 验证返回 200 | **auth on / 空列表 / 分页** 均未测 |
| `POST /api/agents` | 1 | 🔴 | 仅 smoke-test 创建 | **重复 id / 空 system_prompt / 非法 id** 未测 |
| `GET /api/agents/:id` | 1 | 🔴 | 仅 smoke-test 创建后查 | **不存在的 id** 未测 |
| `PUT /api/agents/:id` | 1 | 🔴 | 仅 smoke-test 创建后改 | **不存在的 id / 并发更新** 未测 |
| `DELETE /api/agents/:id` | 1 | 🔴 | 仅 smoke-test 创建后删 | **被 session 引用的 agent 能否删除** 未测 |
| `GET /api/tasks` | 3 | 🟡 | smoke-test + auth-smoke（GET 豁免）+ cases-regression | **auth on 下应受限（M4）**；分页 / 过滤未测 |
| `POST /api/tasks` | 5 | 🟡 | 全部 5 脚本均有 POST task 操作 | **空必填字段 / max_steps 上限 / contract 字段透传** 验证深度不足 |
| `GET /api/tasks?id=` | 3 | 🟡 | smoke-test + cases-regression + multi-agent | **不存在的 task_id / 其他用户的 task（RBAC）** 未测 |
| `POST /api/multi-agent` | 1 | 🔴 | 仅 multi-agent-smoke 测 3 场景 | **auth on / 空 input / case_type 不存在 / 超时** 未测 |
| `GET /api/tools` | 1 | 🔴 | 仅 smoke-test 验证返回 200 | **auth on / 空列表 / 按 type 过滤** 未测 |
| `POST /api/tools` | 1 | 🔴 | 仅 smoke-test 创建 shell type | **重复名称 / 非法 type / 不支持的 type** 未测 |
| `DELETE /api/tools?name=` | 1 | 🔴 | 仅 smoke-test 创建后删 | **不存在的 name / 被 task 引用的 tool** 未测 |
| `GET /api/cases` | 1 | 🔴 | 仅 smoke-test 验证返回 200 | 字段结构 / case 详情未断言 |
| `GET /api/costs` | 2 | 🟡 | smoke-test + auth-smoke 均通过 | **聚合字段语义（cost_cents / record_count / total_tokens）未断言** |
| `GET /api/costs?task_id=` | 1 | 🔴 | smoke-test 传了 task_id 但只验 200 | **不存在的 task_id / cost 为 0 时** 未测 |
| `GET /api/costs?session_id=` | 1 | 🔴 | 同上 | **空 session / 不关联任何 task** 未测 |
| `GET /api/costs?project_id=` | 1 | 🔴 | 同上 | **不存在的 project** 未测 |
| `GET /api/checkpoints` | 1 | 🔴 | 仅 smoke-test 验证返回 200 | **空列表 / 分页** 未测 |
| `POST /api/checkpoints/recover` | 1 | 🔴 | smoke-test 传 fake task_id 验 404 | **真实 checkpoint 写入后 recover 链路** 未测 |
| `POST /api/checkpoints` | 0 | ❌ | **完全未覆盖** | 手动创建 checkpoint 流程 |
| `GET /api/memories` | 1 | 🔴 | 仅 smoke-test 验证返回 200 | **auth on / 空列表 / 分页 / scope 过滤** 未测 |
| `GET /api/memories/recall` | 1 | 🔴 | smoke-test 传参验 200 | **空库不 crash / 无结果返回空数组 / 参数校验** 未测 |
| `POST /api/memories/promote` | 1 | 🔴 | smoke-test 传 `{"task_id":"smoke"}` 验 200 | **promote 后列表是否真的增加 / 不存在的 task_id** 未测 |
| `PUT /api/memories/:id/scope` | 1 | 🔴 | 用 fake_id 验端点命中（返回 200） | **不存在的 id 应 404 / 非法 scope 值** 未测 |
| `DELETE /api/memories/:id` | 1 | 🔴 | 同上 | **不存在的 id / 级联清理 links** 未测 |
| `POST /api/memories` | 0 | ❌ | **完全未覆盖** | 手动写记忆 |
| `POST /api/approve` | 0 | ❌ | **完全未覆盖** | policy-smoke 只测了超时路径，批准分支未测 |
| `POST /api/deny` | 0 | ❌ | **完全未覆盖** | 同上 |
| `WS /ws` | 1 | ✅ | ws-smoke 专项验证：事件序列 + 字段 + 控制消息 | 重连 / 多 client / 大消息 未测 |
| `WS /ws 控制消息` | 1 | 🟡 | ws-smoke 测了 cancel + pause | **approve / deny / resume** 控制消息未测 |
| `POST /api/mock/scripts` | 2 | 🟡 | smoke-test + policy-smoke 均注入过 | **重复 id / 非法 JSON / 大小限制** 未测 |
| `GET /api/mock/scripts` | 2 | 🟡 | 两个脚本均验证 | 无 |
| `GET /api/mock/scripts/:id` | 1 | 🔴 | smoke-test 验存在性 | **不存在的 id** 未测 |
| `DELETE /api/mock/scripts/:id` | 1 | 🔴 | smoke-test 创建后删 | **不存在的 id** 未测 |
| `POST /api/mock/reset` | 1 | 🔴 | smoke-test 召唤重置 | **无 mock 脚本时调用** 未测 |

### 5.3 覆盖统计（按端点级别）

```
总 API 端点（去重）：43 个
  ✅ 充分覆盖（3+ 脚本，含业务断言）： 8  个 (19%)
  🟡 部分覆盖（有测但断言浅）：         8  个 (19%)
  🔴 浅覆盖（仅存在性 / 单场景）：     20  个 (46%)
  ❌ 完全未覆盖：                        7  个 (16%)
```

### 5.4 冒烟测试的固有局限

端到端冒出脚本是 **"行为存在性"** 检查，不是 **"语义正确性"** 检查：

| 冒出能验证的 | 冒出不能验证的 |
|------------|--------------|
| 端点不 crash（2xx/4xx，不 5xx） | 返回字段的数值是否正确（如 cost_cents 精度） |
| 安全门 E2E 拦截（PolicyRule 端到端触发） | 并发写时的数据一致性（SQLite busy_timeout） |
| 事件流序列合规性（ws-smoke 设计序列对比） | 多 Agent 并行时 step 不丢失（冒烟是串行发布） |
| Auth 中间件 on/off 双模式 | RBAC 权限隔离（role 从未被校验） |
| Case 回归（status/steps/tool_call/tokens） | 上下文压缩后多轮对话的正确性 |

**结论**：冒出脚本是回归安全网（"不回归到更差"），不是正确性验证工具。发现 bug 的能力取决于：
1. 是否有针对性的断言（不只是 200）
2. 是否覆盖了 auth on 模式
3. 是否覆盖了错误/边界路径

### 5.5 推荐的冒烟测试演进策略

当前冒出脚本的优先级补全方向：

| 优先级 | 目标 | 说明 |
|--------|------|------|
| **P0** | auth on 模式全覆盖 | M4/M5/M6 安全缺口；现有 5 个脚本全用 `REQUIRE_AUTH=false` |
| **P1** | 错误/边界路径加深断言 | 空 body、不存在的 id、非法字段——目前仅有存在性检查 |
| **P2** | 缺失端点补覆盖 | POST /api/approve、POST /api/checkpoints、POST /api/memories 等 |
| **P3** | 聚合查询语义验证 | costs 的 cost_cents 精度、memories recall 的排序正确性 |

---

## 6. 未测试的风险点（按潜在 bug 类型）

### 6.1 并发与持久化层（最高风险）

| 风险点 | 藏在哪 | 已知/潜在 bug |
|--------|--------|--------------|
| SQLite 并发写 | `pkg/db/database.go` Init 未设 busy_timeout/WAL | **已暴露 S3**：多 agent 并行 INSERT 报 `database is locked` |
| Step ID 生成 | `pkg/db/persistence.go:36` 用 `step_{taskID}_{stepIdx}_{type}` | **已暴露 S4**：多 agent 并行 stepIdx 从 0 开始 → 主键碰撞 |
| Task Save 主键策略 | `pkg/db/persistence.go:80` InsertTask 无 ON CONFLICT | **已暴露 S5**：root task agent_ids 永远空 |
| 子任务 parent 关联 | `internal/orchestrator/orchestrator.go:256` 未调 SaveTaskMeta | **已暴露 S2**：child_tasks 永远空 |
| Worker Pool 并发上限 | `internal/pool/pool.go` 全未测 | 潜在：API 并发超限（CLAUDE.md memory 记录上限 5） |
| Task ID 生成 | 按秒级时间戳 | 潜在：1 秒内并发碰撞（TEST_REPORT 低危已记录） |

### 6.2 ReAct Loop 引擎层

| 风险点 | 藏在哪 | 潜在 bug |
|--------|--------|---------|
| Step 状态机转移 | `internal/runtime/engine.go` 1,548 LOC | 死循环、状态卡死、max_steps 误判 |
| Context 取消 | `cmd/server/main.go:84` TODO 未实现 | **已暴露 S6**：cancel/pause/resume 静默忽略 |
| Checkpoint 恢复 | `internal/runtime/checkpoint.go` 273 LOC | 恢复后状态不一致 |
| AgentBus 消息传递 | `internal/runtime/agentbus.go` + `orchestrator/agentbus_adapter.go` | **已暴露 M7**：sendAgentMessage 无调用方，agent 间无通信 |
| Tool 错误处理 | `internal/tool/builtin.go:175` 非零退出不报 error | **已暴露 M8**：tool-error case 名实不符 |

### 6.3 LLM Provider 层

| 风险点 | 藏在哪 | 潜在 bug |
|--------|--------|---------|
| SSE 流式解析 | `internal/llm/openai_provider.go` + `anthropic_provider.go` 991 LOC 全 0% | delta 拼接错位、tool_call 字段映射错、usage 解析错 |
| Provider 工厂 | `internal/llm/provider_factory.go` 全 0% | 配置 → provider 实例化链路断 |
| Embedding 生成 | `internal/llm/embedding.go` 220 LOC 全 0% | 向量维度不匹配、批量截断 |

### 6.4 安全层

| 风险点 | 藏在哪 | 已知/潜在 bug |
|--------|--------|--------------|
| FileScopeRule 跨平台 | `internal/harness/harness.go:541` | **已暴露 S1**：Windows 对 Unix 绝对路径放行 |
| 审批 vs 硬拦截 | `internal/runtime/engine.go:1213-1222` | **已暴露 S7**：硬性拦截走 30s 审批 |
| isHighRiskFilePath 子串匹配 | `internal/harness/approval.go:530` | **已暴露低危**：`./etc/x` 可绕过 |
| Auth GET 豁免 | `internal/auth/auth_http.go:91` | **已暴露 M4/M6**：敏感读端点无 token 可访问 |
| RBAC 未启用 | `internal/auth/auth_http.go` | **已暴露 M5**：role 定义了但从不校验 |

### 6.5 前端（见 §7）

---

## 7. 前端测试状态

### 7.1 当前状态

| 维度 | 状态 | 说明 |
|------|------|------|
| 类型检查（vue-tsc） | ✅ 有 | `npm run build` 跑 `vue-tsc -b && vite build`，`tsconfig.tsbuildinfo` 存在（2026-07-11 最新） |
| 单元测试（Vitest/Vue Test Utils） | ❌ 无 | `package.json` 无 `vitest` / `@vue/test-utils` 依赖 |
| 组件测试 | ❌ 无 | 15 个 `.vue` 组件零运行时测试 |
| Composables 测试 | ❌ 无 | 8 个 `use*.ts` 零测试 |
| E2E 测试（Cypress/Playwright） | ❌ 无 | 无浏览器自动化 |
| 构建产物 | ✅ 有 | `web/dist/` 存在（2026-07-11 最新） |

### 7.2 前端代码规模

```
web/src/
├── App.vue                    33,196 bytes（主入口）
├── components/                15 个 .vue 组件
│   ├── AgentConfig.vue        AgentTree.vue      ApprovalDialog.vue
│   ├── CaseCard.vue           CaseDetailModal.vue KeyboardTips.vue
│   ├── MemoryBrowser.vue      MetricsPanel.vue   ProjectConfig.vue
│   ├── StatusIndicator.vue    TaskInput.vue      Toast.vue
│   └── TurnItem.vue           TurnList.vue       TypeWriter.vue
├── composables/               8 个状态管理
│   ├── useAgentStore.ts       useKeyboard.ts     useMemoryStore.ts
│   ├── useProjectStore.ts     useSessionStore.ts useTaskStore.ts
│   ├── useToast.ts            useWebSocket.ts
└── types/events.ts            前端事件类型定义
```

### 7.3 风险

- **类型安全有保障**（vue-tsc strict 模式），编译期错误可捕获
- **运行时逻辑零保护**：
  - `useWebSocket.ts` 事件路由逻辑（事件 → store 映射）
  - `useTaskStore.ts` 任务树构建（多 agent 并行状态合并）
  - `AgentTree.vue` 递归渲染
  - `TypeWriter.vue` 流式 delta 拼接
- 后端事件结构变更（如新增 `agent_ready` / `agent_status` / `session_status` 扩展事件）前端无回归保护

---

## 8. 建议的下一步测试工作

### P0 — 阻塞 Phase 7（必须先做）

> 依据：TEST_REPORT.md 已暴露的 S1~S7 严重 bug 的回归保护

| # | 任务 | 目标包 | 预期覆盖率 | 工作量 |
|---|------|--------|-----------|--------|
| P0.1 | **`pkg/db/persistence.go` CRUD 单测** | pkg/db | 25.6% → 70% | 1-2 天 |
| P0.2 | **`pkg/db/database.go` 并发配置单测**（busy_timeout + WAL + 并发 INSERT） | pkg/db | 验证 S3 修复 | 0.5 天 |
| P0.3 | **`internal/orchestrator` 编排单测**（子任务 parent 关联、agent_ids 持久化） | orchestrator | 0% → 60% | 1 天 |
| P0.4 | **`internal/runtime/engine.go` ReAct Loop 单测**（mock LLM，验证状态机转移） | runtime | 0% → 50% | 2-3 天 |
| P0.5 | **`internal/runtime/checkpoint.go` 单测**（保存/恢复一致性） | runtime | 0% → 80% | 0.5 天 |

### P1 — 核心可靠性

| # | 任务 | 目标包 | 预期覆盖率 | 工作量 |
|---|------|--------|-----------|--------|
| P1.1 | **`internal/llm/openai_provider.go` SSE 解析单测**（用 httptest.Server mock） | llm | 30% → 55% | 1 天 |
| P1.2 | **`internal/llm/anthropic_provider.go` 单测** | llm | 30% → 65% | 1 天 |
| P1.3 | **`internal/auth/auth_http.go` + `sqlite_store.go` 单测** | auth | 8.7% → 70% | 1 天 |
| P1.4 | **`internal/harness/approval.go` 审批流单测**（批准 + 拒绝 + 超时三分支） | harness | 19% → 40% | 1 天 |
| P1.5 | **`internal/harness/recall.go` 记忆召回单测** | harness | 19% → 50% | 1 天 |
| P1.6 | **`internal/ws/hub.go` 并发广播单测**（多连接、断线、广播漏消息） | ws | 0% → 80% | 0.5 天 |
| P1.7 | **`internal/pool/pool.go` Worker Pool 单测**（并发上限、退避、关闭） | pool | 0% → 80% | 0.5 天 |
| P1.8 | **`internal/tool/builtin.go` 错误路径单测**（非零退出码、超长输出、不存在的文件） | tool | 32.7% → 60% | 0.5 天 |

### P2 — 补强与前端

| # | 任务 | 目标 | 工作量 |
|---|------|------|--------|
| P2.1 | 引入 **Vitest + @vue/test-utils**，覆盖 `useWebSocket.ts` 事件路由 | web | 1 天 |
| P2.2 | 前端组件冒烟测试（15 个组件 mount 不报错 + props 渲染） | web | 1 天 |
| P2.3 | `internal/cases` 单测（case_id 精确匹配 + 关键词回退） | cases | 0.5 天 |
| P2.4 | `internal/observability` 单测（日志格式 + metrics 累加） | observability | 0.5 天 |
| P2.5 | 补全端到端冒烟缺口：`POST /api/auth/api-keys`、`POST /api/tools`、`POST /api/approve`（批准分支） | scripts | 0.5 天 |
| P2.6 | 引入 **`go test -race`** 到 CI，捕获并发竞态 | CI | 0.5 天 |
| P2.7 | 引入 **覆盖率门槛**（如 `min=50%`），低于则 CI 失败 | CI | 0.5 天 |

### P3 — 长期

| # | 任务 | 目标 | 工作量 |
|---|------|------|--------|
| P3.1 | 引入 **Playwright** 前端 E2E（任务创建 → WS 事件 → 树渲染 → 完成） | web | 2-3 天 |
| P3.2 | **`internal/llm/embedding.go` 单测** | llm | 0.5 天 |
| P3.3 | 真实 LLM 集成测试（mock 模式 + 真实 API 双跑） | scripts | 1 天 |
| P3.4 | 性能/负载测试（多 agent 并发 10+ 任务） | scripts | 2 天 |

---

## 9. 附：覆盖率方法论说明

- **单元测试覆盖率**：`go test -cover ./...` + `go tool cover -func`，按包统计
- **端到端覆盖**：6 个冒烟脚本，独立端口 + 独立临时 DB，不污染 `data/`
- **前端覆盖率**：暂无（无 Vitest），仅 `vue-tsc -b` 类型检查
- **已知未纳入覆盖率的脚本**：`scripts/smoke-test.ps1`（PowerShell 版本，与 `.sh` 等价）

---

*报告生成：2026-07-12*
*审计工具：`go test -cover` + `go tool cover -func` + 源码 LOC 统计*
*交叉参考：`docs/TEST_REPORT.md`（端到端评测，commit 231e403）*
