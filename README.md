# Multi-Agent Platform

> Go + Vue 3 多 Agent 实时协作平台。从零构建，完全可观测的白盒 Agent。
> **当前版本：v0.7.0 Alpha**
> **Phase 状态：0–6 已完成，MCP 支持已落地，Phase 7 规划中**

## 快速开始

### 0. MCP（Model Context Protocol）支持（新增）

平台现在支持接入外部 MCP Server，作为内置工具的扩展：

```bash
# 方式一：静态配置（启动时加载）
export MCP_SERVERS='[
  {"name":"time","transport":"stdio","command":"node","args":["examples/mcp/time/mcp-time-server.js"],"enabled":true},
  {"name":"calc","transport":"stdio","command":"node","args":["examples/mcp/calc/mcp-calc-server.js"],"enabled":true}
]'
go run ./cmd/server

# 方式二：运行时动态 API 增删改查
# 列出
curl http://localhost:8080/api/mcp/servers

# 添加
curl -X POST http://localhost:8080/api/mcp/servers \
  -H 'Content-Type: application/json' \
  -d '{"id":"local-time","config":{"name":"local-time","transport":"stdio","command":"node","args":["examples/mcp/time/mcp-time-server.js"]},"enabled":true}'

# 方式三：从内置市场安装
# 启动后默认已注册 default static market，可通过 REST 或前端 "市场安装" 入口一键安装示例 MCP server
curl http://localhost:8080/api/mcp/markets
curl http://localhost:8080/api/mcp/markets/default/servers
curl -X POST http://localhost:8080/api/mcp/markets/default/servers/local-time/install

# 方式四：SSE transport 远程 MCP server
export MCP_SERVERS='[
  {"name":"remote-time","transport":"sse","endpoint":"http://localhost:3001/sse","enabled":true}
]'
go run ./cmd/server

# 运行时 API 添加 SSE server
curl -X POST http://localhost:8080/api/mcp/servers \
  -H 'Content-Type: application/json' \
  -d '{"id":"remote-time","config":{"name":"remote-time","transport":"sse","endpoint":"http://localhost:3001/sse"},"enabled":true}'

# 方式五：从远程 marketplace 安装
# 启动时通过 MCP_MARKETS 注册任意 JSON catalog URL，例如自建的 npm registry、GitHub releases、OpenCode 等
export MCP_MARKETS='[
  {"name":"opencode","url":"https://example.com/opencode-mcp-catalog.json"}
]'
go run ./cmd/server

# 列出所有已注册市场
curl http://localhost:8080/api/mcp/markets
# 查看远程市场的包
curl http://localhost:8080/api/mcp/markets/opencode/servers
# 安装远程包
curl -X POST http://localhost:8080/api/mcp/markets/opencode/servers/remote-time/install
```

接入的 MCP Server 及其工具在前端 **MCP Server 管理** 弹窗中可视化：🔄 刷新列表、🏪 从市场安装、➕ 手动添加，以及启用/禁用/删除动态 Server。安装自市场的 Server 会持久化到 `mcp_servers` 表，重启后仍保留。

接入的 MCP 工具在注册表中统一命名为 `mcp__<server>__<tool>`。例如 `time` Server 的 `get_current_time` 工具对 Agent 可见为 `mcp__time__get_current_time`。静态配置加载的 Server 不可通过 API 删除。

---

### 1. 配置

```bash
# 编辑 .env 文件（LLM endpoint / API key / 模型配置）
# 已有默认值，本地测试通常无需修改
cp .env.example .env
```

### 2. 编译运行

```bash
cd web
npm run build
cd ..
# 单文件部署：前端 web/dist/* 已嵌入 Go 二进制
go build -o server.exe ./cmd/server/
./server.exe --port 8080
```

### 3. 端到端测试（推荐）

```bash
# 运行后端冒烟测试
bash scripts/smoke-test.sh

# PowerShell 环境
.\scripts\smoke-test.ps1


# go的sample客户端测试(非最新)
# 编译
go build -o e2e-test.exe ./cmd/e2e-test/

# 启动 server 后运行测试
./e2e-test.exe --scenario all

# 仅测试简单对话
./e2e-test.exe --scenario simple

# 仅测试工具调用
./e2e-test.exe --scenario tool
```

### 4. 开发模式

```bash
# 前端独立热重载
cd web
npm install
npm run dev
```

### 5. curl 手动验证

```bash
# 健康检查
curl http://localhost:8080/healthz

# 指标
curl http://localhost:8080/metrics

# 创建任务
curl -X POST http://localhost:8080/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"action":"chat","input":"1+1=?"}'

# WebSocket 实时事件（运行任务后）
# wscat -c 'ws://localhost:8080/ws?session_id=<session_id>'
```

## 项目结构

```
cmd/
  server/                  # 服务入口：HTTP Server + API 路由 + WS Hub
  server/mcp_api.go        # MCP Server 管理 REST API（新增）
  e2e-test/                # 端到端测试工具（WebSocket 事件着色打印）
internal/
  agent/                   # Agent 类型定义
  auth/                    # API key / 用户 / 角色 / 认证中间件
  cases/                   # 预设 Task Cases
  config/                  # .env 加载 + 配置管理（含 MCP_SERVERS）
  cost/                    # CostTracker + CostBudgetRule
  harness/                 # PolicyChain / TaskContract / ApprovalRule
  llm/                     # LLM Provider 抽象 + OpenAI/Anthropic/DeepSeek 实现
  memory/                  # 记忆召回、作用域、上下文压缩
  observability/           # 结构化日志 + Prometheus metrics + /healthz
  orchestrator/            # 多 Agent 编排
  pool/                    # Worker Pool 并发调度
  runtime/                 # ReAct Loop 引擎 + Step 状态机 + 持久化
  tool/                    # Tool 注册表 + 内置工具 + 运行时注册
  tool/mcp/                # MCP Client / Manager / Repository / 示例（新增）
  version/                 # 版本信息 + go:embed
  ws/                      # WebSocket Hub
pkg/
  db/                      # SQLite Schema（17+ 表）、迁移、CRUD
  event/                   # 统一事件结构 + 序列化
web/                       # Vue 3 + Vite + TypeScript 前端
docs/                      # 历史/未来 Markdown 文档
roadmaps/                  # ROADMAP.md 路线图 + 版本史
doc/                       # HTML 格式项目文档（部分章节可能已过时）
openspec/                  # OpenSpec 变更产物
scripts/                   # 测试/发布脚本（smoke-test.sh、smoke-test.ps1）
data/                      # SQLite 数据库文件
storage/                   # 文件存储
examples/mcp/              # MCP Server 示例（time / calc）
```

## 架构概览

```
用户输入
  → POST /api/tasks 或 POST /api/sessions/:id/chat
  → Router 意图分类 / 模型选择
  → ReAct Engine
      Step 0: think (LLM ChatStream → SSE → llm_delta 事件)
      Step 1: tool_call → PolicyGate (Approval / Budget / Whitelist)
      Step 2: observe → loop
  → 超过 max_steps → task_failed (max_steps_exceeded)
  → 最终答案 → task_completed
  → WebSocket Hub 实时广播
```

## 当前状态

**v0.7.0 Alpha** — Phases 0–6 已完成，MCP 支持已落地，Phase 7 规划中。

| 功能 | 状态 | 说明 |
|------|------|------|
| WebSocket 实时通信 | ✅ | gorilla/websocket，事件驱动，多客户端广播 |
| ReAct Loop 引擎 | ✅ | think → tool_call → observe，支持 max_steps / timeout |
| 内置工具 | ✅ | run_shell、write_file、read_file + 运行时注册 |
| MCP 工具扩展 | ✅ | stdio transport + Manager 生命周期 + 动态 API（新增） |
| 工具沙箱 | ✅ | Docker 安全隔离 run_shell |
| DB 持久化 | ✅ | modernc.org/sqlite，17+ 表，迁移 v14+ |
| Vue 3 + Vite 前端 | ✅ | TypeScript、useTaskStore、useWebSocket |
| Session / Project | ✅ | multi-turn chat，Project 分组，Session 历史 |
| 多 Agent 并发 | ✅ | 并行派发，前端多树渲染 |
| Memory | ✅ | scope=session/project/global，向量召回，上下文压缩 |
| Auth | ✅ | API key + bcrypt，可配置 REQUIRE_AUTH |
| RAG | ✅ | LocalEmbeddingProvider + InMemoryVectorStore + `/api/memories/recall` |
| 成本 / 可观测性 | ✅ | CostTracker、/metrics、/healthz、结构化日志 |
| Checkpoint / Recovery | ✅ | 任务检查点 + 崩溃恢复 |
| 可配置 timeout | ✅ | TaskContract.TimeoutSeconds，0 表示无限制 |
| UI overlays | ✅ | Memory Browser overlay，独立关闭 |
| 展开 / 折叠全部 | ✅ | Expand All / Collapse All，最新 step 自动展开 |
| 智能滚动 | ✅ | 底部阈值 + 暂停提示 + Ctrl+End 恢复 |
| Step 索引 | ✅ | `#{{ index }}` 显示在每个 step 头部 |
| Provider Router | ✅ | 多厂商 Provider + fallback 降级 |

## 文档组织

- **根 `README.md`** — 当前项目状态摘要与快速开始。
- **`docs/`** — 历史实施与未来规划 Markdown：
  - `API_CHANGELOG.md` — API 契约与前端适配建议
  - `History.md` — 每次修复/优化批次的详细记录
  - `IMPLEMENTATION_PLAN.md` — 测试阶段实施计划（已归档）
  - `PHASE7_PLAN.md` — Phase 7 规划
  - `TEST_REPORT.md` / `TEST_COVERAGE_REPORT.md` — 测试报告
- **`roadmaps/ROADMAP.md`** — 完整路线图 + 版本历史。
- **`doc/`** — HTML 格式的项目文档。部分章节的内容可能已被后续 Markdown 文档覆盖或替代；遇到过时片段请参考 `docs/History.md`、`docs/API_CHANGELOG.md` 与 `roadmaps/ROADMAP.md` 中的最新记录。

## 设计文档

- `CLAUDE.md` — 项目设计哲学 + 编码约定 + 事件系统 + API 配置
- `roadmaps/ROADMAP.md` — 路线图 + 版本历史
- `openspec/changes/` — OpenSpec 变更产物（proposal / design / tasks）
- `doc/chapters/*.html` — 早期产品文档（HTML 格式，部分内容已逐步迁移至 `docs/`）