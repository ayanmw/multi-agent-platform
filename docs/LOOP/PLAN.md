# 白盒多 Agent 协作平台 — 自主推进任务清单（LOOP 协议 / no-2 版本）

> 状态：○ 待做 | ⏳ 进行中 | ✅ 已完成 | ❌ 阻塞
> 自动化每轮读取本文件，选第一个 ○ 任务实现 → 验证 → commit → 标记 ✅ → STOP。
> **评审-重规划（Phase R）**：当本文件无任何 ○ 任务时，每轮执行一次全项目评审 + 企业级验收 + 重新规划，追加新一轮里程碑（见文末）。
> **里程碑门槛**：N0（缺陷）全部 ✅ 后，才允许开始 N1；N1 全部 ✅ 后，才允许开始 N2。

本项目 = `github.com/ayanmw/multi-agent-platform`，Go 1.25 后端 + Vue3/Vite/TS 前端，白盒可观测哲学。
版本 v0.15.1 Alpha（ROADMAP 标记）/ v0.13.0 Alpha（README 滞后，待校正）。

---

## N0 缺陷修复（当前阶段 — N1 的硬前置门槛）

> 来源：代码审阅发现的真实 bug（已在 LEARNINGS.md 记录 file:line）。规则：本阶段未全部 ✅ 前，禁止开始任何 N1 任务。

| # | 严重度 | 任务 | 状态 | 验证标准 | 依赖 |
|---|--------|------|------|----------|------|
| N0-01 | **P0** | **修复 AgentBus 路由 bug**：`internal/runtime/engine.go:2628` 处 `ToAgentID: ""` 导致 LLM 回复经 AgentBus 投递时目标为空，消息永远无法送达，在 `maxQueue=100` 队列中滞留并污染共享空间（当前靠 `runner.go:445` 重复发同样内容兜底）。改为正确设置目标 agent 或显式广播语义 | ✅ | 多 Agent 编排场景下子 agent 能收到 leader 的消息；AgentBus 队列不再出现目标为空的滞留消息；`go test ./internal/runtime/... ./internal/orchestrator/...` 全绿 | 无 |
| N0-02 | **P1** | **修复多轮历史自复制**：`internal/runtime/engine.go:823` 把含上一轮 history 的 system prompt 文本写回 `session_messages`，下一轮再读出 → 上下文二次膨胀、语义失真。改为：历史以原生 message 数组存储/读取，system prompt 不携带历史文本 | ✅ | 连续两轮对话，第二轮上下文长度稳定（不随轮次线性膨胀）；历史实体引用正确；`go test ./internal/runtime/...` 全绿 | 无 |
| N0-03 | — | **N0 回归验证与结项**：`go build ./...` + `go vet ./...` + `go test -count=1 ./...` 全绿；`bash scripts/cases-regression.sh`（目标 21/21）+ `bash scripts/smoke-test.sh` 通过；在 PROGRESS.md 写「N0 结项报告」 | ○ | 全部验证绿；结项报告落盘；此后方可进入 N1 | N0-01, N0-02 |

---

## N1 企业级核心能力（门槛：N0 全部 ✅ 后才可开始）

> 来源：ROADMAP「未做清单」4 项 + 企业级多 Agent 平台必备能力（RBAC / 审计）。

| # | 任务 | 状态 | 验证标准 | 依赖 |
|---|------|------|----------|------|
| N1-01 | **多轮对话历史回读（原生 message 数组）**：将历史正确接入 Engine ReAct Loop，而非压扁成 system prompt 文本。构造 `[]model.Message` 多轮传入，session 级多轮记忆生效 | ○ | 连续两轮对话，第二轮正确引用第一轮实体；上下文不二次膨胀（回归 N0-02 断言）；新增/扩展多轮 E2E 测试 | N0 |
| N1-02 | **AgentBus ↔ ReAct Loop 闭环**：新增 `send_agent_message` 工具，使 LLM 能主动向其它 agent 收发消息；补全 Engine 可被 AgentBus 驱动/回调的接口（注入消息、恢复），形成双向协议 | ○ | LLM 经工具主动发送 agent message 被目标收到；编排事件经 WS 可达；`go test ./internal/orchestrator/... ./internal/runtime/...` 全绿 | N1-01 |
| N1-03 | **RBAC 落地**：`middleware.RequirePermission(resource, action)` 接入所有敏感路由——Provider/Model 创建更新删除、Session 删除、APIKey 管理、Agent 配置写；角色 viewer/developer/admin；`main.go` 路由注册成链 | ○ | viewer 调 `DELETE /api/providers/:id` → 403；developer/admin 正常；新增权限测试 | N0 |
| N1-04 | **Shell 沙箱安全降级**：无 Docker 环境下 `run_shell`/`execute_program` 的安全策略——危险命令前缀黑名单（rm -rf /、git push --force 等）+ 策略枚举 allow/ask/deny，无人值守默认 deny 并写审计；保留 Docker 路径 | ○ | 命中黑名单命令被拒并写审计；正常命令在约束 cwd 内执行；单测覆盖 | N0 |
| N1-05 | **Agent CRUD 前端管理页面**：在 web/v2 Manage 面板新增独立 Agent 管理 tab（v1/v2 后端 API 已完整，目前仅 AgentConfig 在选择 Agent 时使用），支持分页 / 搜索 / role 列 / 启停 | ○ | npm run build + vue-tsc --noEmit 全绿；前端能列/建/改/删 Agent 配置 | N0 |
| N1-06 | **审计日志**：所有 mutation（Provider/Model/Session/Agent/APIKey/Todo/Cron 写操作）记录 actor + timestamp + scope，落审计表，提供查询接口 | ○ | 一次写操作后审计表出现对应记录；提供 `GET /api/audit` 或等价查询；单测覆盖 | N1-03 |

---

## N2 质量与可观测加固（门槛：N1 全部 ✅ 后才可开始）

| # | 任务 | 状态 | 验证标准 | 依赖 |
|---|------|------|----------|------|
| N2-01 | **可观测性补全**：结构化 tracing、事件完整性校验、/metrics 维度补全（按 agent/session/step 维度），确保「白盒」闭合 | ○ | /metrics 暴露关键维度；tracing 串联一次 run；`go vet/test` 绿 | N1 |
| N2-02 | **测试覆盖**：扩展 E2E 覆盖多轮记忆 / RBAC 403 / 审计；`bash scripts/cases-regression.sh` 稳定 21/21；`bash scripts/multi-agent-smoke.sh` 通过 | ○ | 全部测试绿且稳定（可重复运行不抖）；新增 E2E 落盘 | N1 |
| N2-03 | **文档一致性**：校正 README（v0.13→v0.15.1）、ROADMAP、AGENTS.md 版本号对齐；修正三子系统（Agent CRUD / 多轮历史 / AgentBus）描述与代码真实现状的偏差 | ○ | README/ROADMAP/AGENTS 版本与 git 最新一致；三子系统描述准确 | N1 |
| N2-04 | **真实 Provider 通道**：Anthropic / Gemini 的 Chat 通道从 stub 落地为真实 SSE 流式实现（复用 `internal/llm/client.go`），非 OpenAI-compatible 也可跑 | ○ | 配置 Anthropic/Gemini endpoint 后能真实流式对话；`go test ./internal/llm/...` 绿 | N1 |

---

## 阻塞与依赖

- **阶段门槛**：N0-01..03 全部 ✅ 前，任何 N1 任务不得开始；N1 全部 ✅ 前，任何 N2 任务不得开始（循环按「第一个 ○」自然保证 + 硬规则）。
- N0-01 / N0-02 相互独立，可顺序逐轮完成；N0-03 依赖前两项。
- N1-01 → N1-02（多轮历史是 AgentBus 闭环的 message 契约前置）；N1-03 独立；N1-04 独立；N1-05 前端独立；N1-06 依赖 N1-03。
- **并发风险（重要）**：N1-01 与 N1-02 都改 `internal/runtime/engine.go` 的 `Run()`（共用 `messagesMu`）。虽分两轮实现，但每轮结束后必须重跑对方已写的测试，避免回归。
- N1 全部 → N2 各项。

---

## Phase R — 评审与重规划（当本文件无 ○ 时触发）

当所有 ○ 任务清零（N0/N1/N2 全 ✅ 或 ❌），LOOP 每轮进入 Phase R：

1. **全量验证**：`go build/vet/test ./...` + `bash scripts/cases-regression.sh`（21/21）+ `bash scripts/smoke-test.sh` + 前端 build。
2. **企业级评审**：按 LEARNINGS.md「企业级多 Agent 协作平台验收清单」10 维度逐项打分 Pass/Partial/Fail，输出评审报告。
3. **质量门判定**：
   - 全部 10 维度 = Pass **且** mock 回归 21/21 **且** go 全绿 **且** 评审结论 = 「完美」→ 在 PROGRESS.md `[LOOP STATE]` 设 `DONE=true`，输出验收结论，LOOP 永久终止。
   - 否则 → 基于评审缺口生成**新一轮里程碑**（如 N3 弹性/多租户/国际化/性能），追加为新 section（任务标 ○），记录本轮评审要点到 LEARNINGS；下一轮 LOOP 继续实现。
4. STOP。

> 设计哲学（loop engineering）：每一轮闭环都让项目更逼近「企业级多 Agent 协作平台」标准；重规划由评审发现驱动，而非固定路线图。
