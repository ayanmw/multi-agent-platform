# LEARNINGS — 项目约定、踩坑与企业级验收清单（no-2 白盒平台）

> 本文件供 LOOP 自动化每轮必读。包含：① 项目硬规则 ② 验证命令 ③ 已知上下文 ④ 企业级验收清单（Phase R 打分用）。

---

## ① 项目硬规则（白盒哲学 — 注释即规则）

- 每个导出类型/函数/接口必须有文档注释（职责与关系）；关键流程需行内注释；未完成工作用 `// TODO: Phase X — 描述`。
- **Token 统计只使用 API 返回的 `usage` 字段** —— 绝不做本地估算。
- **工具 workdir 安全**：永远不信任 LLM 传入的 `input["workdir"]`；一律经由 `ExecuteContext.Workdir` / `WorkdirHolder`。
- 启动入口：`AgentRunner.Run(ctx, AgentRunSpec{...})` 是唯一 run 入口（Phase 8-A 收敛）；`Run` 创建 `WorkdirHolder`，工具 CWD 唯一事实来源。
- 工具接口（自 Phase 8-A）：`Name()/Description()/Parameters()/Execute(input)` 外加 `Version()/Source()/CanonicalName()`；注册表键 `namespace/name@version`。
- **确定性测试优先**：回归/CI 用 `LLM_USE_MOCK=true` + `internal/llm/mock_builtin.go` 内置脚本，避免真实 LLM 调用。
- 配置从 `.env` 加载，优先级：系统环境变量 > `.env` > 默认值。不硬编码密钥/魔法值（已有 `engine.go:73` 90s 超时等可提为配置）。
- Git：每个里程碑完成后必须提交；提交信息 `feat(Nx-NN): 简要描述`；同步更新 ROADMAP/文档。

## ② 验证命令

```bash
# 后端
go build ./...
go vet ./...
go test -count=1 ./...

# 回归 / 冒烟（确定性，无需真实 LLM）
bash scripts/smoke-test.sh
bash scripts/cases-regression.sh          # 目标 21/21
bash scripts/multi-agent-smoke.sh

# 前端（仅在涉及前端时）
cd web && npm run build && cd ..
cd web/v2 && npm run build && cd ..
# 类型检查：vue-tsc --noEmit

# 手动 HTTP
curl http://localhost:8080/healthz
```

> Windows 回归注意：mock case 经 Python 从 stdin 读 JSON，必须 `export PYTHONUTF8=1`，否则中文（`skill/list` 的 DisplayName）按 GBK 解码致 `/api/tasks` JSON 解析失败、误判超时。

## ③ 已知上下文 / 踩坑

- ~~**N0-01 路由 bug**~~ **（已修复，轮次 1）**：原 `sendAgentMessageWithSubTask` 硬编码 `ToAgentID: ""`。现为 `sendAgentMessageTo(toAgentID, toSubTaskID, ...) bool`。**沉淀的硬规则**：
  - AgentBus 路由键是 `(ToAgentID, SubTaskID)`，回退键是 `ToAgentID`。**空 `ToAgentID` 不是广播语义**，只会滞留队列并按「丢最旧」挤掉真实消息。需要广播必须由上层遍历目标显式逐个发送。
  - root/leader Engine 统一以 `AgentID="leader"` + `SubTaskID=rootTaskID` 注册 handler（`cmd/server/tasks_api.go` 与 orchestrator OutputTo 转发均按此约定）。worker → supervisor 必须以这对键为目标；常量为 `runtime.DefaultSupervisorAgentID`。
  - 新增消息通道时，**发送函数应返回是否真正投递**，供上层决定是否走兜底通道；否则「修好主通道 + 保留兜底」= 重复投递。审批委托即以 `DelegatedApprovalRequest.BusNotified` 保证恰好一次。
- **AgentRunSpec 的角色字段是死配置**：`cmd/server/runner.go` 的 `Role/CanDispatchSubAgents/CanDefineWorkflow/ApproverMode/SupervisorSubTaskID` 注释称「可显式覆盖」，但 runner 实际只按 `isRoot` 推导，从不读 `spec.*`。改这块前先确认；建议在 N2 阶段清理（要么实现覆盖，要么删字段+改注释）。
- **N0-02 历史自复制**：`internal/runtime/engine.go:823` 把含上一轮 history 的 system prompt 写回 `session_messages` → 下一轮再读出二次膨胀。回归断言见 N0-02/N1-01。
- **多轮历史接错层**：`cmd/server/api.go:1838` `handleSessionChat` 把历史压扁成 system prompt 文本塞入（而非原生 message 数组）。N1-01 应下沉到 Engine 用原生数组。
- **Agent CRUD 页面已存在但被低估**：v1/v2 已有 `AgentConfig.vue`（v2 是 Manage 面板一级 tab），ROADMAP 称「缺管理页面」不准确；真实缺口是分页/搜索/role 列等增量（N1-05）。
- **三子系统 ROADMAP 描述与代码不符**：Agent CRUD 页面其实存在；多轮历史已「能跑」但接错层且有自复制 bug；AgentBus 收已闭环、发是半双工。校正见 N2-03。
- **文档版本不一致**：README 写 v0.13.0、ROADMAP 写 v0.15.1、git 最新提交可能更晚。统一见 N2-03。

## ④ 企业级多 Agent 协作平台验收清单（Phase R 打分用）

> 每维度评分：Pass / Partial / Fail。质量门要求全部 = Pass。

| # | 维度 | Pass 标准 |
|---|------|-----------|
| E1 | **认证与鉴权 (AuthN/AuthZ)** | API key 鉴权 + RBAC 覆盖所有敏感路由；角色 viewer/developer/admin 权限隔离正确；无特权默认 |
| E2 | **审计与合规 (Audit)** | 所有 mutation 记录 actor+timestamp+scope，可查询；敏感操作有审计轨迹 |
| E3 | **多租户与隔离 (Isolation)** | workspace/session 隔离；worktree 隔离；无跨租户/跨 session 数据泄漏；workdir 不可越界 |
| E4 | **安全 (Security)** | 密钥不进 VCS（.gitignore 覆盖）；Shell 执行有沙箱/默认 deny + 审计；输入校验；无明显 prompt-injection 敞口 |
| E5 | **可观测性 (Observability)** | 结构化日志 + /metrics + /healthz + 事件流式 + tracing；「白盒」闭合，状态变更全经 EventBus |
| E6 | **可靠性 (Reliability)** | checkpoint/recovery；幂等；优雅降级（mock provider）；max-steps/timeout 护栏；崩溃可恢复 |
| E7 | **可扩展性 (Scalability)** | worker pool；并发安全（无 data race）；背压；横向扩展路径清晰 |
| E8 | **可测试性 (Testability)** | mock 回归 21/21 稳定；smoke 绿；关键流程 E2E；`go vet` 干净；CI 可跑 |
| E9 | **API/配置稳定性 (Stability)** | 配置经 .env 且优先级正确；无硬编码密钥/魔法值；合理默认值；API 契约稳定 |
| E10 | **文档准确性 (Docs)** | CLAUDE.md/AGENTS.md/README/ROADMAP 与代码一致；版本号对齐；三子系统描述真实 |

> 任一维度 = Fail 或 Partial → Phase R 生成对应改进里程碑（新 section，○ 任务），下一轮继续。
