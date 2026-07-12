# Phase 7 规划文档：生产化与安全加固

> **生成日期**: 2026-07-11
> **对应版本**: v0.6.2 Alpha（Phase 6 收尾修复批次后）
> **状态**: 规划中，待 Phase 7 开始时实施

---

## 背景

Phase 6 完成核心功能（Auth 中间件 + RAG 向量召回 + CostTracker + 可观测性）。Phase 6 收尾期间通过 5 维度端到端评测（`docs/TEST_REPORT.md`），发现并修复了 14 项 P0/P1 级别问题。

本文件记录 Phase 7 待实施的工作项，分为：

1. **安全加固**（阻塞生产部署，需优先实施）
2. **UI/UX 改进**（提升用户体验）
3. **Phase 7 远期规划**（7-A ~ 7-E 大项）

---

## 1. 安全加固（阻塞生产部署）

### 1.1 M4 — Auth GET 请求豁免敏感端点

| 项 | 内容 |
|----|------|
| **问题** | `internal/auth/auth_http.go:91` 判断 `!requiresAuth \|\| r.Method == http.MethodGet`，导致所有 GET 请求（包括 `GET /api/tasks`、`GET /api/costs`、`GET /api/memories`）无 token 可访问 |
| **影响** | `REQUIRE_AUTH=true` 下，攻击者可枚举所有任务、成本、记忆 |
| **建议方案** | 缩小豁免范围：只豁免 `/healthz`、`/metrics` 等公开健康检查端点；敏感读端点（`/api/tasks`、`/api/costs`、`/api/memories`）需要有效 token |
| **实施复杂度** | 低（改 `isProtectedRoute` 或删除 `r.Method == http.MethodGet` 短路） |

### 1.2 M5 — 无 RBAC/role 校验

| 项 | 内容 |
|----|------|
| **问题** | `internal/auth/auth.go` 已定义 `RoleAdmin/RoleUser/RoleViewer` 三角色，且单测覆盖，但中间件与 handler 从未读取或校验 role |
| **影响** | 所有有效 token 权限等同，admin 专属操作（如删除 project、吊销他人 key）无法限制 |
| **建议方案** | 1）在 `authenticateRequest` 成功后查 DB 取用户 role；2）中间件注入 role 到 context；3）handler 按需校验 `admin-only` 路由 |
| **实施复杂度** | 中（需 DB 查询 + context 传播 + 路由规则映射） |

### 1.3 M6 — GET /api/auth/api-keys 无 token 可枚举

| 项 | 内容 |
|----|------|
| **问题** | `GET /api/auth/api-keys` 在 `REQUIRE_AUTH=true` 下仍可无 token 访问，返回所有 key 元数据（含 prefix 前 12 字符） |
| **影响** | 离线碰撞成本下降：12 字符 base64url ≈ 72 bit 熵，暴力碰撞可行性提升 |
| **建议方案** | 将 `/api/auth/api-keys` 纳入 protectedRoutes，或按 `user_id` 过滤（只返回当前用户 key），或返回时脱敏 prefix显示为 `sk_****...xxxx` |
| **依赖** | M5 RBAC 实现后，可按 role 决定枚举粒度 |
| **实施复杂度** | 低 |

### 1.4 Phase 0 — task_id 碰撞风险

| 项 | 内容 |
|----|------|
| **问题** | `cmd/server/persistence.go:90` 用 `task_` + 秒级时间戳生成 ID，同秒并发请求会碰撞 |
| **影响** | 极低（现有 tests 是顺序的，实测无碰撞；但理论上 1 秒内两次 POST 会重叠） |
| **建议方案** | 改用 `task_` + `uuid.New().String()[:8]`，或直接 `task_` + `time.Now().UnixNano()` |
| **实施复杂度** | 极低 |

### 1.5 Phase 0 — `isHighRiskFilePath` 子串匹配（已部分修复）

| 项 | 内容 |
|----|------|
| **现状** | `internal/harness/approval.go` 已改为路径前缀匹配（`strings.HasPrefix(filepath.ToSlash(path), r)`），`./etc/x` 不再触发误判 |
| **残留** | 仍使用子串逻辑检测某些路径模式，建议改为完整路径或正则匹配 |
| **实施复杂度** | 低 |

---

## 2. UI/UX 改进（Phase 2.5 / Phase 3 完成项提升）

> 2026-07-11 批次已处理：可配置任务超时、Memory Browser overlay、Expand All/Collapse All、智能自动滚动、max_steps 失败后 Continue 保留上下文、Step 索引显示、默认 MaxSteps 30、首次错误反馈给 AI。以下 F5 / F6 / F8 / F9 / F10 / F11 中，F5/F6/F10/F11 已修复，F8/F9 仍待 Phase 7。

### 2.1 F5 — idle 状态样式

| 项 | 内容 |
|----|------|
| **问题** | `web/src/types/events.ts:112` `TaskStatus` 有 `running`/`completed`/`failed`，但 DB 可能返回 `empty`/`idle` 状态；`App.vue` 的 `v-if` 不匹配这些状态，导致任务列表显示异常 |
| **建议** | 1）扩展 `TaskStatus` 类型；2）App.vue 和 AgentTree 的 `v-if` 增加 `idle`/`empty` 分支；3）`StatusIndicator` 增加灰色 idle 样式 |
| **实施复杂度** | 低 |

### 2.2 F6 — ApprovalDialog 无错误状态

| 项 | 内容 |
|----|------|
| **问题** | 审批请求如果被 30s 超时拒绝，前端 `ApprovalDialog` 无任何通知，`pendingApproval` 只是变 null |
| **建议** | `handleEvent('system_info')` 增加超时拒绝分支，emit 一个 toast 通知用户"审批超时，操作已被拒绝" |
| **实施复杂度** | 低 |

### 2.3 F8 — WS 重连后不补事件

| 项 | 内容 |
|----|------|
| **问题** | `useWebSocket.ts` 重连后只清空状态，`taskCache` 保持原样（可能有脏数据），不调用 `loadTask` 补齐 |
| **建议** | `ws.onopen` 时如果有 `activeTaskId`，发一条心跳或重拉 `GET /api/tasks?id=`；更简单的方案：onopen 时 emit 一个事件让 store 自行决定是否 reload |
| **实施复杂度** | 中 |

### 2.4 F9 — maxSteps 滑块与后端脱节

| 项 | 内容 |
|----|------|
| **问题** | `TaskInput.vue` 的 `maxSteps` slider 右滑到 50，但后端 `DefaultContract` 默认 max_steps=10；前端未读取后端限制，用户可能设一个远超模型上下文的值 |
| **建议** | 前端加载时 `GET /api/contract`（新端点）或从已有的 case 数据中读取合理范围；或让 TaskInput 的上限通过 props 传入 |
| **实施复杂度** | 低 |

### 2.5 F10 — Step key 多 agent 冲突

| 项 | 内容 |
|----|------|
| **问题** | `AgentTree.vue` 的 `v-for="step in agent.steps"` 用 `:key="step.index"`，多 agent 并行时不同 agent 的 step 索引都是从 0 开始 |
| **建议** | 改为 `:key="step.index + '_' + step.type"` 或在前端生成唯一 ID（含 agentId） |
| **实施复杂度** | 极低 |

### 2.6 F11 — TypeWriter 频繁 DOM 操作

| 项 | 内容 |
|----|------|
| **问题** | 流式更新时每次 text change 都重置 `copyButtonsInjected=false` 并 `querySelectorAll('pre')` + `insertBefore`，可能造成卡顿 |
| **建议** | 加防抖（debounce）或只在 step 完成时注入 copy 按钮；或检查 `pre` 是否已有 `.code-toolbar` 再跳过 |
| **实施复杂度** | 低 |

---

## 3. Phase 7 远期规划（7-A ~ 7-E）

> 与 `roadmaps/ROADMAP.md` Phase 7 章节保持一致。每个子阶段实施前需新建 OpenSpec change。

### 3.1 7-A 身份与多用户体系

- [ ] JWT access/refresh token（与现有 API key 并存）
- [ ] OAuth2 第三方登录（GitHub / Google）
- [ ] Web UI 登录页 + 用户管理界面（Vue 路由守卫）
- [ ] 数据隔离：session / project / memory 按 `user_id` 隔离
- [ ] 配额管理：每用户 token / 成本 / 并发任务上限
- [ ] RBAC 细化：角色权限下沉到 Tool / 端点级

### 3.2 7-B 外部向量与 Embedding 集成

- [ ] `EmbeddingProvider` 远程实现（OpenAI text-embedding-3 / Cohere）
- [ ] `VectorStore` 持久化后端（pgvector 或 ChromaDB）
- [ ] 混合检索：向量召回 + BM25 关键词 + 重排
- [ ] 增量索引：memory 写入时实时 upsert
- [ ] 语义去重：相似度阈值合并

### 3.3 7-C 深度可观测

- [ ] OpenTelemetry trace 跨 Agent / Tool / LLM 调用链路
- [ ] Prometheus SDK 替换手写 metrics
- [ ] 审计日志：actor / target / before / after
- [ ] 多 Agent 协作 trace 树可视化
- [ ] 事件回放：基于 SQLite 事件流重建任务执行

### 3.4 7-D Harness 治理与合规

- [ ] 成本预算硬限制：阈值自动暂停 + 告警
- [ ] 审批工作流增强：多级审批 / 超时升级
- [ ] 合规快照：tool call 输入输出录制、文件变更 diff
- [ ] 数据保留策略：episodic memory TTL + 自动归档
- [ ] PII 脱敏：memory / 日志敏感信息自动打码

### 3.5 7-E 生产部署

- [ ] Docker Compose / K8s 部署清单
- [ ] Postgres 替换 SQLite
- [ ] CI/CD: GitHub Actions build / test / vet / lint
- [ ] 备份恢复 + HA

---

## 4. 实施建议顺序

```
Phase 2.5 (UI 改进, 已完成)
  ├── F5  idle 状态样式          ✅
  ├── F6  ApprovalDialog 错误状态  ✅
  ├── F10 Step key 冲突修复        ✅
  ├── F11 TypeWriter 防抖          ✅
  ├── 可配置任务超时                ✅
  ├── 展开/折叠全部 + 智能滚动       ✅
  ├── max_steps Continue 保上下文    ✅
  ├── Step 索引显示                ✅
  ├── 默认 MaxSteps 30             ✅
  ├── 错误反馈优先策略              ✅
  └── F9  maxSteps 后端范围限制    ⏳ Phase 7

Phase 7 (安全加固, P0 阻塞生产)
  ├── M4  Auth GET 豁免收紧       (~1h)
  ├── M5  RBAC enforcement        (~3-4h)
  ├── M6  API keys 枚举脱敏       (~1h, 依赖 M5)
  └── Phase 0 task_id 碰撞修复    (~10min)

Phase 7 (远期, 各子阶段)
  ├── 7-B 外部向量 (独立, 可先行)
  ├── 7-C 深度可观测 (独立, 可并行)
  ├── 7-A 多用户身份 (依赖 DB schema)
  ├── 7-D 治理合规 (依赖 7-A)
  └── 7-E 生产部署 (最后)
```

---

## 5. 关联文档

| 文档 | 路径 | 说明 |
|------|------|------|
| 端到端测试报告 | `docs/TEST_REPORT.md` | 5 维度 34 PASS/8 FAIL/3 SKIP |
| API 调整清单 | `docs/API_CHANGELOG.md` | 16 项前端适配建议 |
| 产品路线图 | `roadmaps/ROADMAP.md` | 全 Phase 规划 |
| 实施计划 | `IMPLEMENTATION_PLAN.md` | 测试阶段实施计划（已归档） |
