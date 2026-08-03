# PROGRESS — 白盒多 Agent 协作平台 LOOP 执行日志（no-2 版本）

> 每轮 LOOP 在末尾「执行日志」追加一条；末尾 `[LOOP STATE]` 区块记录循环轮次、质量门、DONE 标志。
> 格式：`### YYYY-MM-DD HH:MM | 轮次 | Nx-NN | 状态 + 内容 + Commit + 验证 + 下一步`

---

## 初始化

- 2026-08-02 22:31 | 初始化 | LOOP 协议建立。初始里程碑：N0 缺陷修复（N0-01 路由 bug、N0-02 历史自复制）、N1 企业级核心（多轮历史/ AgentBus 闭环 / RBAC / Shell 沙箱 / Agent CRUD 前端 / 审计）、N2 质量加固。Phase R（评审-重规划）在任务清零时触发，目标：评审完美 + mock 21/21 + 设计符合企业级多 Agent 协作平台（见 LEARNINGS 10 维度）。预算 24h（validUntil 2026-08-03T22:31:06+08:00）。

---

## 执行日志

### 2026-08-03 08:50 | 轮次 1 | N0-01 | ✅ 修复 AgentBus 路由 bug（P0）

**根因**：`internal/runtime/engine.go` 的 `sendAgentMessageWithSubTask` 硬编码 `ToAgentID: ""`。AgentBus 的 handler 以 `(agentID, subTaskID)` / `agentID` 为键注册，空目标匹配不到任何 handler，消息只会进入 `maxQueue=100` 待投递队列滞留，并按「丢最旧」把真实待投递消息挤出队列。唯一调用方是 `approval_delegation.go` 的审批委托，实际能送达全靠 `cmd/server/runner.go` 的自投递兜底重发。

**改动**：
- `internal/runtime/engine.go`：新增常量 `DefaultSupervisorAgentID = "leader"`、配置字段 `EngineConfig.SupervisorAgentID`、辅助方法 `supervisorAgentID()`；把 `sendAgentMessageWithSubTask` 重构为 `sendAgentMessageTo(toAgentID, toSubTaskID, msgType, content) bool`，写入真实 `ToAgentID`，并对空目标**防御性拒绝 + Warn 日志**（宁可不发也不污染队列）；`system_info.to_agent` 由空串改为真实目标；`sendAgentMessage` 语义修正（此前把 toAgentID 误当 subTaskID 传）；`SendAgentMessage` 补注释明确其为「自投递」语义。
- `internal/runtime/approval_delegation.go`：`DelegatedApprovalRequest` 新增 `SupervisorAgentID` / `BusNotified` 字段；委托时按 `(supervisorAgentID, SupervisorSubTaskID)` 精确路由，并回写投递结果。
- `cmd/server/runner.go`：兜底自投递改为仅在 `!req.BusNotified` 时执行，消除路由修复后的**重复投递**（保证恰好一次）。
- `internal/orchestrator/orchestrator.go`：worker EngineConfig 显式设置 `SupervisorAgentID: runtime.DefaultSupervisorAgentID`。

**测试**：新增 `internal/runtime/agentbus_routing_test.go`（4 例：默认/自定义 supervisor ID、路由目标与事件断言、空目标拒绝、无 bus no-op）＋ `internal/orchestrator/orchestrator_test.go::TestAgentBus_WorkerToLeaderRouting`（投递侧：命中 leader handler；空目标不投递且滞留队列=1）。

**验证**：`go build ./...` ✅ / `go vet ./...` ✅ / `go test -count=1 ./...` ✅ 全绿（无失败包）；`go test ./internal/runtime/... ./internal/orchestrator/...` ✅。

**Commit**：`fabc678`（前置：网络测试确定性守卫归档）、`0c4ce7e`（N0-01 本体）

**下一步**：N0-02 修复多轮历史自复制（`engine.go:823` system prompt 回写 session_messages）。

---

### 2026-08-03 09:15 | 轮次 2 | N0-02 | ✅ 修复多轮历史自复制（P1）

**根因**：链路是「读侧回灌 + 写侧回写」共同构成的递归套娃。
1. `cmd/server/api.go handleSessionChat` 读出全部 `session_messages`，用 `buildHistoryContext` 压成文本前置进 system prompt；
2. `internal/runtime/engine.go Run()` 又把这份**带历史的运行时 prompt** 原样写回 `session_messages`（role="system"）；
3. 下一轮第 1 步再把它当历史读出来 → 第 N 轮的 prompt 里嵌着第 N-1 轮的 prompt，后者又嵌着第 N-2 轮……上下文随轮次膨胀且语义失真。

**改动**：
- `internal/runtime/engine.go`：新增 `EngineConfig.BaseSystemPrompt`（不含历史回灌的干净内核）与 `Engine.baseSystemPrompt`；把 `NewEngine` 中散落的 prompt 拼接重构为 `promptPrefix`/`promptSuffix` + `buildSystemPrompt(core)` 闭包，使**运行时 prompt 与持久化基线共享同一套增强**（WorkingMemory 前缀 / WorkspaceDir / Skill / Todo 后缀，skill 事件仍只广播一次）；新增 `persistedSystemPrompt()`，`Run()` 改为持久化基线而非 `e.messages[0]`。
- `cmd/server/api.go`：`buildHistoryContext` 过滤 `Role=="system" && TurnIndex>=0`（历史轮次的指令基线），保留 `TurnIndex==-1` 的压缩摘要；过滤后为空返回空串。`handleSessionChat` 传 `BaseSystemPrompt: systemPrompt`，并**移除 workingMemory 的重复拼接**（NewEngine 已前置 `cfg.WorkingMemory`，此前一段 Working Memory 会在 prompt 中出现两遍）。
- `cmd/server/runner.go`：`AgentRunSpec.BaseSystemPrompt` 透传到 `EngineConfig`。

**测试**：新增 `internal/runtime/session_history_test.go`（3 例：基线优先级 / Run 持久化不含历史且运行时仍含历史 / 3 轮持久化长度恒定）＋ `cmd/server/session_history_test.go`（4 例：跳过历史 system 行含污染遗留行 / 保留压缩摘要且不误标 Turn 0 / 全 system 行返回空串 / 5 轮无套娃）。

**过程发现**：首版把「未增强的裸基线」写库，导致 `TestSkillPromptInjectedE2E` 变红——持久化记录丢了 skill 注入段。据此改为两份 prompt 共享增强，既修 bug 又不损可观测性。已沉淀为 LEARNINGS 硬规则。

**验证**：`go build ./...` ✅ / `go vet ./...` ✅ / `go test -count=1 ./...` ✅ 全绿（无失败包）；N0-01 回归 `go test ./internal/runtime/... ./internal/orchestrator/...` ✅；`gofmt` 经 LF 归一后干净（CRLF 伪影已记入 LEARNINGS）。

**Commit**：`e6f530e`

**下一步**：N0-03 —— N0 回归验证与结项（cases-regression 21/21 + smoke-test + 结项报告）。

---

### 2026-08-03 10:12 | 轮次 3 | N0-03 | ✅ N0 回归验证与结项

**目标**：对 N0-01 / N0-02 两项缺陷修复做全量回归，确认无回归、无带红提交，并出具 N0 结项报告，开放 N1 门槛。

#### 一、验证矩阵（全部实测，非推断）

| # | 验证项 | 命令 | 结果 | 证据 |
|---|--------|------|------|------|
| V1 | 编译 | `go build ./...` | ✅ PASS | exit 0，无输出 |
| V2 | 静态检查 | `go vet ./...` | ✅ PASS | exit 0，无告警 |
| V3 | 全量单测 | `go test -count=1 ./...` | ✅ PASS | 24 个有测试的包全部 `ok`，0 个 FAIL（含 `cmd/server` 10.3s、`internal/runtime` 0.53s、`internal/orchestrator` 0.69s、`internal/tool` 17.8s） |
| V4 | Case mock 回归 | `bash scripts/cases-regression.sh` | ✅ **21/21 (100%)** | exit 0；L1 4/4、L2 5/5、L3 5/5、L4 4/4、L5 3/3 |
| V5 | REST 冒烟 | `bash scripts/smoke-test.sh` | ✅ PASS | **63 PASS / 0 FAIL / 1 SKIP**（SKIP = `/ws` 握手，curl 能力限制，非缺陷） |

#### 二、N0-01（AgentBus 路由）回归证据

`cases-regression.sh` 的 L4/L5 共 7 个多 Agent case 全部 PASS，且编排事件三元组（`decompose_done` / `agent_dispatched` / `agent_completed`）**全部 ≥1 且单调递增累计**（1/1/1 → 7/14/14），`child_tasks[].steps` 回填 `child_steps=yes` 全绿。说明 leader↔worker 的消息路由与事件广播链路真实闭合，未出现空目标滞留。

`multi-agent-fault-tolerance`（L5 容错场景）completed，证明修复后的「恰好一次」投递语义（`BusNotified` 抑制兜底重发）在故障回退路径下仍成立。

#### 三、N0-02（多轮历史自复制）回归证据

- `context-compression` case PASS（`TurnIndex==-1` 压缩摘要保留逻辑未被过滤规则误伤）。
- `skill-code-helper` case PASS + `cmd/server` 包全绿（含 `TestSkillPromptInjectedE2E`），证明「运行时 prompt / 持久化基线共享同一套增强」的设计既修了自复制，又未丢失 skill 注入的可观测性。
- `checkpoint-resume` PASS，说明 prompt 分离改动未破坏 checkpoint 恢复路径。

#### 四、token/成本口径校验（顺带体检）

21 个 case 的 `total_tokens` 全部 > 0（302 ~ 1099），`cost_records` 全部 ≥ 1，符合 LEARNINGS 硬规则「Token 统计只使用 API 返回的 usage 字段」——mock provider 同样走 usage 通道，未做本地估算。

#### 五、环境适配说明（不改生产脚本）

WorkBuddy 沙箱中 Git Bash 的 `/tmp` = `AppData\Local\Temp`，而原生 Windows 二进制（`go build -o`、`curl -o`、`node`）解析 `/tmp` 为**当前盘根 `C:\tmp`**，两者视图不一致，导致 `SERVER_BIN="/tmp/..."` 的脚本在沙箱内 exec 失败。本轮采用「**只读副本 + 路径重写**」策略：`sed 's#/tmp/#C:/Users/Joker/.workbuddy/loop-tmp/#g'` 生成仓库外副本执行，**未修改 `scripts/` 下任何生产脚本**（它们在正规 Git Bash / Linux CI 下本就正确）。该方法已沉淀入 LEARNINGS。

#### 六、结项结论

> **N0 缺陷修复里程碑正式结项。** N0-01（P0 路由）、N0-02（P1 历史自复制）、N0-03（回归结项）全部 ✅。后端零编译错误、零 vet 告警、零测试失败；确定性回归 21/21；REST 冒烟 63/63。**N1 门槛开放**，下一轮起可开始 N1 企业级核心能力。

**遗留（非阻塞，已在 LEARNINGS 记录，归属后续里程碑）**：
1. 多轮历史仍压扁为 system prompt 文本（接错层）→ N1-01 下沉为原生 `[]llm.Message`。
2. AgentBus 仍是「收闭环、发半双工」，LLM 无法主动发消息 → N1-02。
3. `AgentRunSpec` 的 `Role/CanDispatchSubAgents/...` 是死配置（runner 只按 `isRoot` 推导，从不读 `spec.*`）→ 建议 N2 清理。
4. smoke 记录的 4 处「API 与文档差异」（`POST /api/projects` 返 201、`POST /api/tools` 必填 type 子字段、Memory 路由与文档不符、`/ws` 需专项测）→ 归入 N2-03 文档一致性。

**Commit**：见下方回填

**下一步**：N1-01 —— 多轮对话历史回读（原生 message 数组下沉到 Engine ReAct Loop）。

---

## [LOOP STATE]

```
loop_round:        3
phase:             N1 (企业级核心能力)
quality_gate_pass: false
done:              false
last_review:       (未执行 — Phase R 待 PLAN 无 ○ 时触发)
next_milestone:    N1
budget_validuntil: 2026-08-03T22:31:06+08:00
```
