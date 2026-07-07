# 多轮对话 + 记忆作用域 + Project 管理

> **状态**: 设计讨论完成，待实现
> **日期**: 2026-07-06
> **关联**: Phase 6 多轮对话子模块

---

## 〇、Project 管理体系

### 0.1 核心概念

```
Project ──1:N──> Session ──1:N──> Task ──1:N──> Step
  │                 │
  │                 └── session_messages (多轮对话历史)
  │
  └── memories (scope=project 的记忆)
```

- **Project**: 一个独立的"工作空间"，有自己的工作目录、记忆、会话集合
- **Session → Project**: 1 个 Session 只属于 1 个 Project
- **Project → Session**: 1 个 Project 可以有多个 Session
- **Project → Memory**: 1 个 Project 有多条记忆（scope=project）

### 0.2 数据模型

**新增 `projects` 表**:
```sql
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    working_directory TEXT DEFAULT '',   -- 项目工作目录（绝对路径）
    config JSON DEFAULT '{}',            -- 项目级配置（运行时环境变量等）
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

**sessions 表新增 FK**:
```sql
ALTER TABLE sessions ADD COLUMN project_id TEXT DEFAULT 'default';
-- FK: FOREIGN KEY (project_id) REFERENCES projects(id)
```

**memories 表已有 `project_id`**（无需变更）

**Seed 默认 Project**:
```sql
INSERT INTO projects (id, name, description, working_directory)
VALUES ('default', 'Default Project', 'Auto-created default project', '');
```

### 0.3 Project CRUD API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/projects` | 列出所有项目 |
| POST | `/api/projects` | 创建项目 |
| GET | `/api/projects/:id` | 获取项目详情（含 sessions 列表、记忆统计） |
| PUT | `/api/projects/:id` | 更新项目（名称、工作目录、描述） |
| DELETE | `/api/projects/:id` | 删除项目（级联：sessions → tasks → steps → memories） |

### 0.4 Project 的职责边界

| 职责 | 说明 |
|------|------|
| **工作目录** | `write_file`/`read_file`/`run_shell` 的文件操作范围限定在此目录内 |
| **记忆容器** | `scope=project` 的记忆归属于此项目，跨 Session 共享 |
| **会话分组** | 前端按 Project 分组显示 Session 列表 |
| **配置隔离** | 不同项目可有不同的默认 Agent、不同的 max_steps、不同的模型偏好 |

### 0.5 前端侧边栏布局

```
┌─────────────────────┐
│  Projects     [+新] │
│  ┌─────────────────┐│
│  │ ▼ My Go Project ││  ← 当前选中的 Project
│  │   Sessions:      ││
│  │   ├─ 写斐波那契  ││
│  │   ├─ 重构 API   ││
│  │   └─ + New      ││
│  │                 ││
│  │ ▶ My Vue Project││  ← 折叠的其他 Project
│  │ ▶ Default       ││
│  └─────────────────┘│
│                     │
│  ⚙ Project Settings │  ← 点击进入 Project 编辑页
└─────────────────────┘
```

### 0.6 工作目录与安全性

- `run_shell` 的 `cwd` 默认设为 `project.working_directory`
- `write_file`/`read_file` 的路径校验：以 `project.working_directory` 为基准，拒绝 `../` 逃逸
- 用户可在 Project 设置中修改 `working_directory`
- 如果 `working_directory` 为空，则使用服务进程的工作目录

---

## 一、多轮对话架构（方案 C）

### 核心模型

```
Project: "my-go-app"
└── Session: "写一个 Go Web 服务"
    ├── session_messages (各轮共享的 LLM 对话历史)
    ├── Turn 1: Task A (root) ── parent_task_id = null, is_root = true
    │   └── 子任务 A1 ── parent_task_id = Task A
    ├── Turn 2: Task B ── parent_task_id = Task A (root), is_root = false
    │   └── 子任务 B1 ── parent_task_id = Task B
    ├── Turn 3: Task C ── parent_task_id = Task A (root), is_root = false
    └── ...
```

**关键规则**:
- `root_task_id` 指向第一轮任务（永远不变）
- 后续每一轮对话 = 一个独立 Task，`parent_task_id` 指向 root_task_id
- 如果某轮任务内部又派生了子 Agent 任务，子任务的 `parent_task_id` 指向该轮任务
- 任务层级: `root → turn_2, turn_3 (siblings) → child_of_turn_2 (children)`

### 上下文传递

新 Task 创建时，从 `session_messages` 表中加载所有历史消息，注入到新 Task 的初始 messages 中：

```
Turn 1 messages: [system, user:"写斐波那契", assistant, tool..., observation, 最终答案]
Turn 2 messages: [system, ...Turn 1 全部消息..., user:"加上缓存", assistant, ...]
Turn 3 messages: [system, ...Turn 1+2 全部消息..., user:"写测试", ...]
```

### 数据模型变更

**新增表: `session_messages`**
```sql
CREATE TABLE session_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    task_id TEXT NOT NULL,       -- 产生此消息的 Task
    turn_index INTEGER NOT NULL, -- 第几轮对话（0-based）
    role TEXT NOT NULL,          -- system / user / assistant / tool
    content TEXT NOT NULL,
    tool_call_id TEXT,           -- 工具调用 ID（tool role 时）
    tool_calls JSON,             -- assistant 消息中的 tool_calls
    token_count INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (session_id) REFERENCES sessions(id),
    FOREIGN KEY (task_id) REFERENCES tasks(id)
);
```

**sessions 表新增字段**:
```sql
ALTER TABLE sessions ADD COLUMN project_id TEXT DEFAULT 'default';  -- FK → projects
ALTER TABLE sessions ADD COLUMN turn_count INTEGER DEFAULT 0;       -- 当前轮次计数
ALTER TABLE sessions ADD COLUMN total_tokens INTEGER DEFAULT 0;     -- 累计 token
ALTER TABLE sessions ADD COLUMN context_size INTEGER DEFAULT 0;     -- 上下文大小（字节）
```

**tasks 表新增字段**:
```sql
ALTER TABLE tasks ADD COLUMN turn_index INTEGER;  -- 该 Task 属于第几轮对话
```

---

## 二、上下文压缩策略

### 触发条件

满足任一条件即触发压缩：
1. **轮次阈值**: `session.turn_count >= 20`
2. **Token 阈值**: `session.total_tokens >= 100KB`（约 25K tokens）

### 压缩流程

```
1. 检测到阈值触发（在新 Task 启动前同步执行）
2. 提取最近 N 轮的消息（保留最近 5 轮完整内容）
3. 对更早的轮次：
   a. 提取每轮的关键信息（用户输入、工具调用、最终结果）
   b. 生成结构化摘要
   c. 写入 memories 表（tier=consolidated, scope=session, type=session_summary）
4. 将摘要注入到 session_messages 中，替代原始消息
5. 更新 session.total_tokens 和 session.context_size
```

### 压缩后的消息结构

```
[system]
[压缩摘要: Turn 1-5 的关键信息]
[Turn 6 完整消息]
[Turn 7 完整消息]
... (最近 5 轮)
[user: 当前轮输入]
```

---

## 三、记忆作用域体系

### 3.1 四层记忆架构（当前已有）

| 层级 | 存储 | 生命周期 | 示例 |
|------|------|---------|------|
| Working Memory | 内存（不持久化） | 单次 Task | 当前任务上下文 |
| Raw Episodic | conversations 表 | 永久 | 用户说了什么、LLM 回了什么 |
| Consolidated Episodic | memories 表 (tier=consolidated) | 永久 | 任务摘要、经验教训 |
| Semantic/Policy | memories 表 (tier=semantic) | 永久 | 编码规范、偏好设置 |

### 3.2 作用域（Scope）三层体系

在 `memories` 表中新增 `scope` 字段：

```sql
ALTER TABLE memories ADD COLUMN scope TEXT DEFAULT 'project';
-- 取值: session | project | global
```

| Scope | 归属 | 可见范围 | 生命周期 | 示例 |
|-------|------|---------|---------|------|
| **session** | Session | 同一 Session 内 | Session 删除 → 标记 `session_ended` | "我们决定用 SQLite" |
| **project** | Project | 同一 Project 的所有 Session | Project 删除 → 级联删除 | "本项目使用 Tab 缩进" |
| **global** | 全局 | 所有项目 | 仅用户手动删除 | "用户偏好中文回复" |

### 3.3 各 Scope 的生成与晋升

```
   上下文压缩 ──→ session 级记忆
                    │
                    │ 跨 Session 重复（≥2 Session 出现相同模式）
                    ↓
   Heartbeat  ──→ project 级 consolidated
                    │
                    │ PromotionGate（跨项目重复 + explicit_user_instruction）
                    ↓
   用户标记   ──→ global 级 semantic
```

| Scope | 产生方式 | 晋升条件 |
|-------|---------|---------|
| session | 上下文压缩时自动生成 | **不自动晋升**，保持 session 级别 |
| project | Heartbeat 抽取 + 人工确认 | `repeated_across_sessions`（≥2 个 Session 中出现相同模式） |
| global | **仅用户显式标记** | 系统绝不自动晋升到 global，避免污染全局规则 |

### 3.4 召回优先级

新 Task 构建 Working Memory 时的召回顺序：

```
1. 加载 Session 级记忆（本 Session 的上下文压缩产物，scope=session）
2. 加载 Project 级 Semantic 规则（当前项目的稳定规则，scope=project, tier=semantic）
3. 加载 Project 级 Consolidated Episodes（关键词匹配 top N，scope=project, tier=consolidated）
4. 加载 Global 级 Semantic 规则（全局偏好，scope=global）
5. 加载 Session 的历史完整消息（最近 5 轮，从 session_messages 表）
```

### 3.5 确认结论

| # | 问题 | 结论 |
|---|------|------|
| 1 | Session/Project/Global 三层划分 | ✅ 合理 |
| 2 | Global 记忆创建权 | ✅ 仅用户显式标记，系统不自动晋升 |
| 3 | Session 结束后记忆处理 | ✅ 保留，标记 `status=session_ended`，可作为晋升证据 |
| 4 | 压缩时机 | ✅ 同步（新 Task 启动前压缩），保证上下文干净 |
| 5 | 实现优先级 | ✅ 先多轮对话（#1-3, #6-7），再做压缩（#4-5） |

---

## 四、完整数据模型总览

```
┌─────────────────────────────────────────────────────────────────┐
│                         projects                                 │
│  id | name | description | working_directory | config | ...      │
└──────────────────────┬──────────────────────────────────────────┘
                       │ 1:N
                       ↓
┌─────────────────────────────────────────────────────────────────┐
│                        sessions                                  │
│  id | project_id | root_task_id | turn_count | total_tokens | ...│
└──────┬──────────────────────┬────────────────────────────────────┘
       │ 1:N                  │ 1:N
       ↓                      ↓
┌──────────────┐    ┌──────────────────────────────────────┐
│    tasks      │    │         session_messages              │
│  turn_index  │    │  session_id | task_id | turn_index    │
│  parent_task │    │  role | content | tool_calls | ...    │
│  is_root     │    └──────────────────────────────────────┘
└──────┬───────┘
       │ 1:N
       ↓
┌──────────────┐
│    steps      │
│  type | ...  │
└──────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                        memories                                  │
│  project_id | scope | tier | type | content | confidence | ...   │
│  scope IN (session, project, global)                             │
│  tier IN (consolidated, semantic)                                │
└─────────────────────────────────────────────────────────────────┘
```

---

## 五、前端 UI 设计

### 5.1 侧边栏（Project + Session）

```
┌──────────────────────────┐
│  🏠 Projects      [+ 新] │
│  ┌──────────────────────┐│
│  │ ▼ 🗂 My Go Project   ││  ← 当前 Project
│  │   📁 /home/user/go/  ││
│  │   Sessions:           ││
│  │   ├─ 💬 写斐波那契     ││  ← Session
│  │   ├─ 💬 重构 API      ││
│  │   └─ + New Session   ││
│  │                      ││
│  │ ▶ 🗂 My Vue Project  ││  ← 折叠的 Project
│  │ ▶ 🗂 Default         ││
│  └──────────────────────┘│
│                          │
│  ⚙ Project Settings     │  ← 编辑当前 Project
└──────────────────────────┘
```

### 5.2 多轮时间线

```
┌─ Session: "写一个 Go Web 服务" ─────────────────────────────┐
│                                                              │
│  ▼ Turn 1: "写斐波那契函数"         ✅ completed  1.2k tokens │
│  │  ┌─ Agent: coder ──────────────────────────────────┐     │
│  │  │  🧠 think  →  🔧 run_shell  →  👁️ observation  │     │
│  │  │  🧠 think  →  Final Answer                      │     │
│  │  └─────────────────────────────────────────────────┘     │
│  │                                                           │
│  ▼ Turn 2: "加上缓存机制"           ✅ completed  2.1k tokens │
│  │  ┌─ Agent: coder ──────────────────────────────────┐     │
│  │  │  🧠 think  →  🔧 write_file  →  👁️ observation │     │
│  │  │  🧠 think  →  Final Answer                      │     │
│  │  └─────────────────────────────────────────────────┘     │
│  │                                                           │
│  ▶ Turn 3: "写单元测试"             🔄 running    0.5k tokens│
│                                                              │
│  ┌──────────────────────────────────────────────────────────┐│
│  │  💬 Type your message...                          [Send] ││
│  └──────────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────────┘
```

### 5.3 交互逻辑

- **默认状态**: 最新一轮展开，历史轮折叠
- **点击轮次标题**: 展开/折叠该轮的 AgentTree
- **折叠时显示**: 轮次标题 + 用户输入摘要 + 最终结果前 100 字
- **轮次状态指示器**: 同现有 StatusIndicator（running/completed/failed）
- **输入框始终可见**: 在最后一轮下方，复用现有 TaskInput 组件

### 5.4 组件层级

```
App.vue
├── Sidebar
│   ├── ProjectSelector (项目下拉 + 新建按钮)
│   ├── SessionList (按 Project 筛选的 Session 列表)
│   └── ProjectSettings (进入 Project 编辑页)
├── MainContent
│   ├── SessionView (整合组件)
│   │   ├── SessionHeader
│   │   ├── TurnList
│   │   │   ├── TurnItem (expandable)
│   │   │   │   ├── TurnHeader (turn number, input, status, tokens)
│   │   │   │   └── AgentTree (复用)
│   │   │   └── ...
│   │   └── TaskInput (复用)
│   └── ProjectConfig.vue (Project 编辑页)
└── AgentConfig.vue (已有，复用)
```

---

## 六、实现路线图

### Phase 6-A: Project 管理 + 多轮对话（核心）

| # | 任务 | 依赖 | 估时 |
|---|------|------|------|
| 1 | DB Schema: `projects` 表 + `session_messages` 表 + sessions/tasks/memories 新增字段 | 无 | 0.5h |
| 2 | 后端: Project CRUD API（`/api/projects`）+ 默认 Project 种子 | #1 | 1h |
| 3 | 后端: Session 级 messages 持久化（Engine 执行时同步写入 session_messages） | #1 | 2h |
| 4 | 后端: 多轮对话 API（同一 Session 内继续发送消息，创建新 Task 并注入历史上下文） | #3 | 2h |
| 5 | 前端: 侧边栏重构（Project 选择器 + Session 列表按 Project 分组） | 无 | 2h |
| 6 | 前端: Project 配置页（CRUD 表单） | #5 | 1.5h |
| 7 | 前端: TurnList + TurnItem 组件（多轮时间线 + 展开折叠） | 无 | 3h |
| 8 | 前端: SessionView 重构（整合时间线 + 输入框） | #7 | 2h |
| 9 | 集成测试 + 端到端验证 | #4, #8 | 2h |

### Phase 6-B: 上下文压缩 + 记忆作用域（优化）

| # | 任务 | 依赖 | 估时 |
|---|------|------|------|
| 10 | 后端: Memory 作用域扩展（scope 字段 + 迁移旧记录） | 6-A #1 | 1h |
| 11 | 后端: 上下文压缩引擎（阈值检测 + 摘要生成 + session_messages 更新） | #10 | 3h |
| 12 | 后端: 召回优先级实现（session → project → global） | #10 | 2h |
| 13 | 前端: Memory 浏览页面（按 scope/project 查看记忆） | #10 | 2h |
| 14 | 集成测试 | #11, #12 | 1.5h |

---

## 七、完整 API 总览

### Project API
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/projects` | 列出所有项目 |
| POST | `/api/projects` | 创建项目 |
| GET | `/api/projects/:id` | 项目详情 |
| PUT | `/api/projects/:id` | 更新项目 |
| DELETE | `/api/projects/:id` | 删除项目（级联） |

### Session API（已有，增强）
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/sessions?project_id=xxx` | 按项目筛选 Session 列表 |
| POST | `/api/sessions` | 创建 Session（绑定 project_id） |
| GET | `/api/sessions/:id` | Session 详情（含 turn 列表） |
| DELETE | `/api/sessions/:id` | 删除 Session（级联 session_messages） |

### 多轮对话 API（新增）
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/sessions/:id/chat` | 在 Session 内发起新轮对话 |
| GET | `/api/sessions/:id/messages` | 获取 Session 的完整消息历史 |
| POST | `/api/sessions/:id/compress` | 手动触发上下文压缩 |

### Memory API（增强）
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/memories?scope=project&project_id=xxx` | 按作用域和项目筛选 |
| PUT | `/api/memories/:id/scope` | 修改记忆作用域（project→global） |
| DELETE | `/api/memories/:id` | 删除记忆 |