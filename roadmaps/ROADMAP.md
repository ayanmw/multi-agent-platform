# 多 Agent 平台 — 产品路线图

> **最近更新**: 2026-07-26
> **当前版本**: v0.15.0 Alpha
> **更新规则**: 每个 Phase 任务完成后，更新本文件并提交 Git。

---

## 路线图总览

```
Phase 0 ✅ → Phase 1 ✅ → Phase 2 ✅ → Phase 3 ✅ → Phase 4 ✅ → Phase 5 ✅ → Phase 6 ✅
  (骨架)      (Agent)     (UI)       (Cases)    (并发)      (注册)      (高级)

Phase skill ✅ → Phase TODO ✅ → Phase 7-cron ✅ → Phase UI-v2 ✅ → Phase 7-H2 ✅
  (Skill 系统)    (TODO 子系统)   (定时器)          (控制室 UI)      (编排闭环)

Phase 8-A ✅ → Phase 8-B ✅ → Phase worktree ✅ → web-search-china ✅ → smoke-fix ✅
  (架构演进)    (架构收尾)      (worktree 隔离)   (国内搜索+深度研究)  (冒烟测试修复)

multi-model-routing ✅ → llm-provider-model-management ✅
  (多模型分层路由 P1-P3)   (LLM Provider 与模型持久化)
```

---

## 已完成 Phase

| Phase | 日期 | 一句话说明 |
|-------|------|-----------|
| Phase 0: 项目骨架 + 通信验证 | 2026-07-03 | Go 模块、WS Hub、AgentEvent、SQLite 6 表、Vue CDN demo |
| Phase 1: Agent Loop 核心引擎 | 2026-07-03 | 真实 LLM SSE Client、ReAct Engine、3 个内置工具、DB 持久化 |
| Phase 1.5: 扩展工具注册表 | 2026-07-18 | Tool Namespace/Tags、`core/*` 系列工具、风险标签 |
| Phase 2: 前端可视化 | 2026-07-03 | Vite+Vue3+TS 迁移、AgentTree、TypeWriter、Markdown 高亮 |
| Phase 3: 预设 Cases + Harness 基础 | 2026-07-15 | Case CRUD、TaskContract、FileScopeRule、PolicyGate、LLM Judge |
| Phase 4: 多 Agent 并发 + 记忆基础 | 2026-07-05 | 多 Agent 并行分派、多树渲染、Policy Gate、Memory 基础 |
| Phase 5: 工具注册生态 + 执行沙箱 | 2026-07-08 | 版本化 Registry、动态/JSON/Docker 工具加载、Docker 沙箱 |
| Phase 6: 高级能力 + 可观测性 + gRPC | 2026-07-10 | RAG、Model Router、gRPC、Cost Tracker |
| Phase skill: Skill 可复用 Prompt 包 | 2026-07-10 | Skill Registry/Renderer/REST、前端 `/` 触发 SkillPicker |
| Phase TODO: Session 级 TODO 子系统 | 2026-07-16 | TODO Service、6 个 Agent Tools、拖拽嵌套 |
| Phase 7-cron: 定时器子系统 | 2026-07-21 | cron/interval/once 调度、4 种 action、14 个 cron 事件 |
| Phase UI-v2: Observable Control Room | 2026-07-24 | Dock 三栏 + 移动 3-tab、CommandBar、Inspector、可访问性 |
| Phase 7-H2: Multi-Agent 编排闭环 | 2026-07-21 | parallel/sequential/DAG、AgentBus 隔离、编排事件可观测 |
| Phase 8-A: 架构演进 | 2026-07-23 | AgentRunner 收口、ToolDescriptor/Executor/Loader、cmd/server 拆分 |
| Phase 8-B: 架构收尾 | 2026-07-24 | 动态工具持久化、handler 方法化、闭包退场、recovery 收口 |
| Phase worktree: Session 级 git worktree 隔离 | 2026-07-24 | Manager 原语、WorkdirHolder、启动孤儿扫描 |
| Phase web-search-china: 国内搜索与深度研究 | 2026-07-24 | Baidu/Sogou/Bing China HTML、`core/web_research`、usage 回传 |
| Phase smoke-fix: 冒烟测试失败修复 | 2026-07-25 | 工具 Unregister fallback、policy 测试路径修复 |
| Phase multi-model-routing: 多模型分层路由 | 2026-07-25 | 5-tier Router、RateLimiter、预算治理、fallback、前端路由面板 |
| Phase llm-provider-model-management: Provider 与模型持久化 | 2026-07-26 | Provider `ListModels`、DB 持久化、ProfileResolver、前端 Model Manager |

---

## 历史版本（已归档）

| 版本 | 日期 | 说明 |
|------|------|------|
| v0.9.0–v0.9.7 Alpha | 2026-07-19~21 | multi-agent 动态编排从 orchestrator 到 DAG + AgentBus 隔离 |
| v0.10.0 Alpha | 2026-07-21 | Session 级 TODO 子系统 |
| v0.11.0–v0.11.3 Alpha | 2026-07-21~22 | Cron 子系统前后端 + 内置 Case 矩阵 5→21 |
| v0.12.0–v0.12.2 Alpha | 2026-07-23 | Phase 8-A 架构演进 + worktree 隔离 + real-llm-smoke 143/20/0 |
| v0.13.0–v0.13.7 Alpha | 2026-07-24~25 | Phase 8-B 收尾 + UI-v2 体验 + 权限/自动审批 |
| v0.14.1 Alpha | 2026-07-25 | multi-model-routing P1-P3 完整落地 |
| v0.15.0 Alpha | 2026-07-26 | LLM Provider & Model Management 多 Provider 配置与模型持久化 |

---

## 下一步 / 未做清单

**核心未做能力**

- [ ] Shell 沙箱：当前 `run_shell`/`execute_program` 可选 Docker；长期需完善 Firecracker/gVisor 或无 Docker 环境的安全降级方案。
- [ ] Agent CRUD 前端页面（v1/v2）：后端 API 已完整，缺独立管理页面（目前仅 AgentConfig 在选择 Agent 时使用）。
- [ ] Conversation 历史回读用于多轮对话：当前每个 task 独立上下文，session 级多轮记忆尚未接入 Engine。
- [ ] AgentBus 接入 Engine ReAct Loop：AgentBus listener 已存在，但 LLM 主动收发 agent message 的协议未完全闭环。

**模型与 Provider**

- [ ] Anthropic/Gemini Provider 真实 `ListModels` 与 Chat 接入（当前为 stub，仅有 OpenAI-compatible 通道可用）。
- [ ] 动态模型配置热加载（当前依赖 `.env` + 重启）。
- [ ] 跨模型 tokenizer 成本校准（当前按 `InputPrice`/`OutputPrice` 与 usage 直接计算）。
- [ ] 后端全量真实 Provider 接入与 E2E 验证。

**治理与基础设施**

- [ ] Token 治理与 context 压缩：长任务上下文截断/摘要策略。
- [ ] RBAC：用户/角色/权限体系（当前仅靠 API key 粗粒度鉴权）。
- [ ] 部署文档与 K8s/容器化交付物。
- [ ] Baidu 移动搜索反爬适配：验证码页 fallback（headless/API/cookie 池）。

**真实 LLM 已知限制（已记录于 memory）**

- L5 `leader-dispatch` / `fault-tolerance` 在真实 LLM 下可靠性不稳定，mock 回归 21/21 PASS，但 real-LLM 不作为 FAIL 处理。

---

## 已完成归档的 OpenSpec 变更

- `2026-07-23-extend-task-cases`
- `2026-07-24-web-search-china-providers`
- `2026-07-24-v2-mobile-usability-fix`
- `2026-07-25-agent-config-permissions-and-v2-auto-approval`
- `2026-07-25-add-configurable-auto-approval`
- `2026-07-25-web-v2-ui-ux-optimization`
- `2026-07-25-multi-model-routing`
- `2026-07-26-llm-provider-model-management`
- `provider-model-management-frontend-transparency`
- `2026-07-26-backend-security-and-concurrency-hardening`
- `2026-07-26-server-graceful-shutdown-and-main-refactor`

---

## 更新日志

| 日期 | 变更 |
|------|------|
| 2026-07-26 | 归档 security + graceful shutdown；整理 ROADMAP 历史细节，集中展示「下一步 / 未做清单」 |
