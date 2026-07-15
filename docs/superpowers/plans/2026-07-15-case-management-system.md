# Case 管理系统实现方案

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个可自定义 Case 的前后端完整方案：支持 Case CRUD、分类/Tag 筛选、Goal 目标定义，并在任务完成后用 LLM 判定结果是否符合目标；默认 Case 在 Case 库为空时自动插入；前端可按 Tag 筛选 Case 列表。

**Architecture:** 后端新增 `cases` 与 `case_evaluations` 数据表，`internal/cases` 层统一合并内置 Case 与 DB 自定义 Case；`internal/harness` 增加 `llm_judge` 评估器并在 `runtime/engine` 任务完成后自动触发；前端使用 Pinia 风格的 `useCaseStore` 管理状态，提供过滤栏、Case 卡片和编辑表单。

**Tech Stack:** Go 1.25 + SQLite (modernc.org/sqlite) + Vue 3 + Vite + TypeScript + marked。

---

## 1. 实现范围

### 新增功能
1. `cases` 表与 `case_evaluations` 表的数据库迁移。
2. 内置 Case 与自定义 Case 的统一模型和 Repository / Service 层。
3. Case CRUD HTTP API：`GET /api/cases`、`GET /api/cases/:id`、`POST /api/cases`、`PUT /api/cases/:id`、`DELETE /api/cases/:id`。
4. `/api/cases` 支持 `?tag=`、`?category=` 筛选（可多选，逗号分隔）。
5. 启动时若 `cases` 表为空，自动插入内置默认 Case（`is_builtin = 1`）。
6. 扩展 `AcceptanceEvaluator`，新增 `llm_judge` 判定器：把 task 结果、messages、Goal 与 rubric 发给 LLM，返回 `passed/score/reason`。
7. `runtime/engine.go` 在 `task_completed` 后自动调用评估器，并广播 `task_evaluated` 事件。
8. 前端新建类型文件 `types/case.ts`、状态管理 `stores/useCaseStore.ts`、组件 `CaseFilter.vue`、`CaseForm.vue`、升级 `CaseCard.vue` 与 `App.vue` 实现筛选与 CRUD。

### 本次不实现
- Case 导入/导出（JSON/CSV）——后续迭代。
- 评估器异步队列/重试——简单同步调用即可。
- Case 权限与共享——假设当前单用户本地使用。

---

## 2. 文件结构

### 后端
| 文件 | 职责 |
|------|------|
| `pkg/db/migrate.go` | 新增 `cases`、`case_evaluations` 表迁移 |
| `internal/cases/case.go` | Case/Goal/AcceptanceCriterion 领域模型（替代旧的 `cases.go`） |
| `internal/cases/builtin.go` | 五个内置默认 Case 的定义 |
| `internal/cases/repository.go` | SQLite CRUD + 查询 tag/category |
| `internal/cases/service.go` | 业务逻辑：内置与自定义 Case 合并、启动时 seed |
| `internal/harness/evaluator.go` | 扩展 AcceptanceEvaluator，增加 `llm_judge` |
| `internal/harness/llm_judge.go` | LLM Judge 实现 |
| `cmd/server/api.go` | 新增/修改 Case API handler |
| `cmd/server/main.go` | 初始化 `cases.Service` 并注册路由 |
| `internal/runtime/engine.go` | 任务完成后调用 evaluator 并广播事件 |
| `pkg/event/event.go` | 新增 `task_evaluated` 事件类型 |

### 前端
| 文件 | 职责 |
|------|------|
| `web/src/types/case.ts` | Case、TaskContract、EvaluationResult 类型 |
| `web/src/stores/useCaseStore.ts` | Case 列表、筛选条件、CRUD 操作 |
| `web/src/components/CaseFilter.vue` | 按 Tag、Category 筛选 |
| `web/src/components/CaseForm.vue` | 新建/编辑 Case 表单 |
| `web/src/components/CaseCard.vue` | 展示 Case 信息、可点击 Tag、编辑/删除入口 |
| `web/src/App.vue` | 集成筛选栏、Case 网格、表单弹窗 |
| `web/src/stores/useTaskStore.ts` | （可能修改）启动任务时携带 caseId/goal |

---

## 3. 数据模型

### Case（领域模型）
```go
type Case struct {
    ID            string            `json:"id"`
    Name          string            `json:"name"`
    Description   string            `json:"description"`
    Icon          string            `json:"icon"`
    Category      string            `json:"category"`
    SystemPrompt  string            `json:"system_prompt"`
    DefaultInput  string            `json:"default_input"`
    Contract      TaskContract      `json:"contract"`
    Tags          []string          `json:"tags"`
    IsBuiltin     bool              `json:"is_builtin"`
    CreatedAt     time.Time         `json:"created_at"`
    UpdatedAt     time.Time         `json:"updated_at"`
}

type TaskContract struct {
    Goal               string                `json:"goal"`
    Scope              string                `json:"scope"`
    MaxSteps           int                   `json:"max_steps"`
    Permissions        []string              `json:"permissions"`
    AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria"`
    Tags               []string              `json:"tags"`
}

type AcceptanceCriterion struct {
    Type   string `json:"type"`   // file_exists | content_contains | shell_exit_zero | test_pass | llm_judge
    Target string `json:"target"` // 文件名、关键字、shell 命令、或 LLM rubric
}
```

### 数据库表
```sql
CREATE TABLE cases (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    icon TEXT,
    category TEXT,
    system_prompt TEXT,
    default_input TEXT,
    contract_json TEXT NOT NULL,
    tags_json TEXT,
    is_builtin INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX idx_cases_category ON cases(category);
CREATE INDEX idx_cases_is_builtin ON cases(is_builtin);

CREATE TABLE case_evaluations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,
    case_id TEXT NOT NULL,
    passed INTEGER NOT NULL,
    score REAL,
    reason TEXT,
    evaluated_at DATETIME NOT NULL
);
CREATE INDEX idx_eval_task ON case_evaluations(task_id);
```

Tag 在 SQL 中以 JSON 数组字符串存储；查询时加载所有 case 后在内存按 tag 过滤，性能足够当前规模。

---

## 4. API 设计

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/cases?tag=code,coding&category=generation` | 返回内置+自定义 Case 列表，可筛选 |
| GET | `/api/cases/:id` | 获取单个 Case |
| POST | `/api/cases` | 创建自定义 Case |
| PUT | `/api/cases/:id` | 更新自定义 Case（内置不可改） |
| DELETE | `/api/cases/:id` | 删除自定义 Case（内置不可删） |
| GET | `/api/cases/:id/evaluations/:task_id` | 查看某任务对某 Case 的评估结果 |

`/api/cases` 返回示例：
```json
[
  {
    "id": "code-gen",
    "name": "代码生成",
    "category": "generation",
    "tags": ["code", "coding"],
    "is_builtin": true,
    ...
  }
]
```

---

## 5. LLM Judge 规则

新增 `AcceptanceCriterion.Type = "llm_judge"`，`Target` 为评估 prompt/rubric。

评估流程：
1. 引擎收集 task 运行结果：
   - 用户原始输入
   - Agent 最终回答
   - 关键 tool result（最后 N 条）
2. 拼装 Judge Prompt：
   ```
   Goal: <case.contract.goal>
   Rubric: <criterion.target>
   User Input: ...
   Final Answer: ...
   Tool Outputs: ...

   请判断结果是否符合目标。返回 JSON：{"passed": bool, "score": 0-1, "reason": "..."}
   ```
3. 调用 `llm.Router`，解析 JSON。
4. 所有 criterion 都通过则 task 标记为 `passed`，否则 `failed`。

---

## 6. 前端设计

### 状态管理 useCaseStore
```ts
interface CaseState {
  cases: Case[]
  loading: boolean
  selectedTags: string[]
  selectedCategory: string | null
}

const actions = {
  async loadCases(): Promise<void>
  async createCase(c: Omit<Case, 'id' | 'created_at' | 'updated_at'>): Promise<void>
  async updateCase(id: string, c: Partial<Case>): Promise<void>
  async deleteCase(id: string): Promise<void>
  toggleTag(tag: string): void
  setCategory(category: string | null): void
}

const getters = {
  filteredCases: ComputedRef<Case[]>
  allTags: ComputedRef<string[]>
  allCategories: ComputedRef<string[]>
}
```

### UI 布局
- 顶部：标题 + 新建 Case 按钮。
- 筛选栏：Category 下拉、Tag 胶囊（可多选）、清除筛选按钮。
- 主体：Case 卡片网格，每个卡片展示 Icon、Name、Category、Tags、Goal 摘要。
- 点击卡片打开 `CaseDetailModal`（已有），编辑进入 `CaseForm` 弹窗。
- 运行 Case 后，在任务详情区域显示 `EvaluationResult` 标签（pass/fail + reason）。

---

## 7. 关键注意事项

1. **内置 Case 保护**：`is_builtin=1` 的 Case 不可通过 API 修改/删除。
2. **ID 生成**：自定义 Case ID 使用 `case-<nanoid>` 或 UUID；更新时不允许改 ID。
3. **错误处理**：每个 API handler 返回统一错误格式；前端 Toast 提示。
4. **事件广播**：engine 评估完成后广播 `task_evaluated` 事件，前端订阅更新任务状态。
5. **默认 Case Seed**：放在 `cases.Service` 的 `Init()` 中，应用启动时检查并插入。
6. **类型一致性**：Go 的 `Case` 字段名与前端 `Case` 接口必须完全一致。
7. **注释要求**：每个导出类型、函数、Vue props/emits 都需要中文注释（遵循项目注释铁律）。

---

## 8. 验收标准

- [ ] 启动时 SQLite `cases` 表为空则自动插入 5 个默认 Case。
- [ ] 前端首次加载可看到默认 Case，且 Tag 可点击筛选。
- [ ] 可创建自定义 Case，保存到 DB，刷新后仍存在。
- [ ] 可编辑/删除自定义 Case，内置 Case 不能删改。
- [ ] 运行一个带 `llm_judge` 标准的 Case，任务结束后收到 `task_evaluated` 事件，显示 passed/score/reason。
- [ ] `/api/cases?tag=code` 只返回包含 `code` tag 的 Case。
- [ ] `go test ./...` 通过，前端 `npm run type-check` 通过。

---

## 9. 建议拆分实施顺序

1. 数据库迁移 + SQL 测试
2. 后端 `internal/cases` models / repository / service（含 seed）
3. 后端 Case CRUD API + tag/category 筛选
4. 后端 LLM Judge evaluator + engine 集成
5. 前端类型 + useCaseStore
6. 前端 CaseFilter + CaseForm + CaseCard 改造
7. App.vue 集成 + 评估结果展示
8. 联调测试与提交
