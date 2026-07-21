# 工作流编排方案深度调研参考文档

> **文档定位**：未来研究方向备忘录。暂不写入 `roadmaps/ROADMAP.md`，待团队决定实施时再据此拆分为正式 Phase 任务。
> **最后更新**：2026-07-21
> **调研范围**：面向“多 Agent 协作”场景的流程编排框架，重点对比与本项目当前 DAG 实现的关系。

---

## 一句话结论

**LangGraph / Temporal / Airflow / Prefect / Dify / FastGPT / coze** 的本质都是“状态驱动的工作流引擎”：它们把任务的执行抽象成图，节点可以是任意函数或 Agent，边表达控制流（顺序、分支、循环、等待、重试）。

我们当前实现的 `RunBlockingDAG` 是这套能力的**子集**：只编排 Agent 节点，只支持 DAG（无环），只支持简单的布尔条件表达式，Agent 之间的数据流靠 `AgentBus` / `OutputTo` 传递，而不是共享的 workflow state。

如果后续要像 LangGraph 一样支持“循环、等待输入、子图嵌套、函数节点、内存共享”，需要做一次从“调度器”到“状态机引擎”的升级。

---

## 1. 为什么要写这篇文档

### 1.1 当前的能力边界

截至 Phase 7-H2 阶段 5，我们实现了 DAG 调度：

- `WorkflowNode`：一个 Agent + 依赖 + 触发条件。
- `WorkflowEdge`：显式边（from/to/condition）。
- `RunBlockingDAG`：基于 Kahn 算法，满足依赖和条件时启动 Agent，不满足则标记为 `skipped`。
- 条件表达式：手写 tokenizer + shunting-yard 求值，支持 `<agent_id>.completed/failed` 与 `&& || ()`。

能表达：

```text
researcher ──► writer
            (条件：researcher.completed)

┌─────────┐         ┌─────────┐
│ agent_a │ ─────── │ agent_c │
└─────────┘         └─────────┘
       │                            (条件：a.completed || b.completed)
┌─────────┐         ┌─────────┐
│ agent_b │ ───────┘
└─────────┘
```

不能表达：

- **循环 / retry**：某个 Agent 失败后重试 3 次，或回退到上一步。
- **等待外部事件**：等待用户审批、等待 webhook、等待定时器。
- **非 Agent 节点**：纯计算节点、工具节点、人工节点、LLM-as-judge 节点。
- **共享内存**：多个 Agent 共同读写同一个上下文对象。
- **动态扩图**：运行时根据中间结果追加新节点。

### 1.2 研究目标

梳理主流方案的设计理念与工程取舍，为后续决策提供输入：

1. 我们的 DAG 子集在行业中处于什么位置？
2. 如果要扩展，应该优先补哪些能力？哪些是“看起来很酷但成本高”的能力？
3. 有哪些迁移路径可以让当前实现平滑演进，而不是推倒重来？

---

## 2. 主流编排框架分类

### 2.1 面向 LLM Agent 的编排框架

#### LangGraph（LangChain 生态）

- **核心模型**：`StateGraph`，节点是函数或 Agent，边表达状态转移。
- **状态**：所有节点共享一个 `State` 对象（TypedDict / Pydantic），节点可以读写 state。
- **循环**：支持 `add_conditional_edges` 实现循环、重试、人工介入。
- **Human-in-the-loop**：内置 `interrupt` / `resume` 语义。
- **检查点**：自动持久化 state，支持从任意节点恢复。
- **与本项目的对比**：
  - 相似：都用“边条件”决定下一步。
  - 差异：LangGraph 的节点是任意函数，节点之间靠共享 state 通信；我们的节点目前**只能**是 Agent，通信靠 AgentBus 消息。

**示例**（LangGraph 风格）：

```python
class State(TypedDict):
    query: str
    research: str
    draft: str
    approved: bool

graph = StateGraph(State)
graph.add_node("researcher", research_agent)
graph.add_node("writer", write_agent)
graph.add_node("reviewer", review_agent)
graph.add_node("human_approve", human_approve)  # 非 Agent 节点

graph.set_entry_point("researcher")
graph.add_edge("researcher", "writer")
graph.add_conditional_edges(
    "writer",
    lambda state: "approve" if state["draft"] else "rewrite",
    {"approve": "reviewer", "rewrite": "writer"}
)
graph.add_edge("reviewer", "human_approve")
```

#### Dify / FastGPT / Coze

- **定位**：低代码 / 无代码 Agent 应用搭建平台。
- **核心模型**：工作流画布，节点包括 LLM、知识库检索、条件分支、变量赋值、工具调用、人工输入等。
- **状态**：通常是隐式的“上下文变量”，用户可以在画布上显式传递。
- **循环**：多数支持简单的循环和条件分支；人工审批节点是付费/企业功能。
- **与本项目的对比**：
  - 我们不提供可视化画布，但 backend 的 DAG 调度可以被视为“这些平台 backend 的简化版”。
  - 它们更适合非技术用户快速搭建；我们更适合需要代码级可控性和白盒观测的场景。

### 2.2 通用工作流引擎

#### Temporal

- **核心模型**：Workflow-as-code，用代码定义工作流状态机，Worker 执行 Activity。
- **状态**：Workflow 状态由 Temporal 服务端持久化，支持长时间运行、故障恢复。
- **循环 / 等待**：原生支持 sleep、定时器、等待外部 signal、子 workflow。
- **与本项目的对比**：
  - Temporal 是“重引擎”，需要独立服务，适合企业级长流程。
  - 我们的 Agent 执行时间通常以秒/分钟计，不需要 Temporal 级别的持久化和重放能力。
  - 但 Temporal 的“工作流代码即状态机”思想值得借鉴。

#### Apache Airflow / Prefect

- **核心模型**：DAG，节点是任务（通常是函数/脚本），边是依赖关系。
- **状态**：任务的运行状态由调度器管理，数据通常通过 XCom（Airflow）或 Prefect 的 result 传递。
- **循环**：原生 DAG 不支持循环；Airflow 需要借助 TriggerDagRunSensor 或动态 DAG。
- **与本项目的对比**：
  - 传统数据流水线编排，节点粒度大，不适合 LLM Agent 的细粒度 ReAct loop。
  - 我们当前实现的 DAG 与 Airflow DAG 在“拓扑调度”层面最像，但我们更面向 Agent 节点和 LLM 驱动的动态拆分。

---

## 3. 关键能力对比矩阵

| 能力 | LangGraph | Temporal | Airflow | Dify/FastGPT/Coze | 我们当前 |
|------|-----------|----------|---------|-------------------|----------|
| 节点类型 | 任意函数/Agent | Activity（任意函数） | Task（任意函数） | LLM/工具/条件/人工 | 仅 Agent |
| 循环/重试 | ✅ 原生 | ✅ 原生 | ❌ 需技巧 | ✅ 有限 | ❌ |
| 条件分支 | ✅ 任意函数 | ✅ 任意函数 | ✅ 分支节点 | ✅ 画布分支 | ✅ 布尔表达式 |
| 共享状态 | ✅ State 对象 | ✅ Workflow state | ✅ XCom | ✅ 上下文变量 | ❌ 靠 AgentBus |
| 人工介入 | ✅ interrupt | ✅ signal | ❌ | ✅ 企业版 | ❌ |
| 子图嵌套 | ✅ subgraph | ✅ child workflow | ✅ subdag | ✅ 子工作流 | ❌ |
| 持久化/恢复 | ✅ checkpoint | ✅ 历史重放 | ✅ | ✅ | ❌ 仅任务级 |
| 动态扩图 | ✅ 运行时加边 | 有限 | 有限 | 有限 | ❌ 单次派发 |
| 观测性 | trace/state | 完整 event history | task log | 画布执行记录 | 事件驱动（强） |
| 部署复杂度 | 低（库） | 高（独立服务） | 中（调度器+worker） | SaaS/私有化 | 低（进程内） |

---

## 4. 当前实现的详细设计回顾

### 4.1 数据结构

```go
type WorkflowNode struct {
    Agent        AgentSpec // 节点运行的 Agent
    Dependencies []string  // 依赖的 agent_id 列表
    Condition    string    // 触发本节点的布尔表达式
}

type WorkflowEdge struct {
    From      string // 上游 agent_id
    To        string // 下游 agent_id
    Condition string // 边上触发的条件
}

type AgentWorkflow struct {
    Nodes []WorkflowNode
    Edges []WorkflowEdge
}
```

### 4.2 调度算法

使用 **Kahn 算法**：

1. 从 `Dependencies` 和 `Edges` 构建入度表和邻接表。
2. 入度为 0 的节点立即启动 goroutine 执行。
3. 节点完成后，遍历其出边：
   - 如果上游节点状态不是 `completed`，跳过该边。
   - 评估节点自身 condition 与边 condition。
   - 都满足时，下游节点入度减 1；入度为 0 时启动。
   - 任一条件不满足时，也减入度；入度为 0 时标记该节点为 `skipped`。
4. `ctx.Done()` 时停止产生新的 Agent，已有结果保留。

### 4.3 条件表达式

```text
agent_a.completed && agent_b.succeeded
agent_x.failed || (agent_y.completed && agent_z.completed)
```

求值方式：手写 tokenizer → shunting-yard 转后缀 → stack 求值。

**优点**：零外部依赖、编译产物小、LLM 输出可控。

**缺点**：表达能力有限，不支持比较运算符、算术、函数调用。

---

## 5. 如果要演进，有哪些路径

### 路径 A：在现有 DAG 基础上扩展（推荐作为下一小步）

保持当前的 `AgentWorkflow` 模型，逐步增加以下能力：

1. **retry 语义**
   - 在 `WorkflowNode` 增加 `RetryPolicy`：最大重试次数、退避策略、按错误类型重试。
   - `runAgent` 失败时按策略重试，重试成功不触发下游 skipped。

2. **等待 / 异步事件**
   - 在 `AgentWorkflow` 增加 `WaitNode` 或边条件里的 `external_event` 关键字。
   - Orchestrator 等待一个 `external_signal`（如用户审批、webhook）再继续。
   - 需要持久化等待状态（checkpoint 扩展）。

3. **工具节点 / LLM-as-judge 节点**
   - 扩展 `WorkflowNode.Agent` 使其不强制要求 `SystemPrompt`：节点类型可以是 `type: agent|tool|judge|wait`。
   - 工具节点直接调用 Registry 执行，不走完整 ReAct engine。

4. **共享 state**
   - 引入 `WorkflowState map[string]any`，节点可以把结果写入 key，下游按 key 读取。
   - 在 UI 上体现为“工作流变量”。

### 路径 B：引入通用工作流引擎（LangGraph / Temporal 风格）

把 Agent 执行抽象成更通用的节点模型：

```go
type WorkflowNode struct {
    ID       string
    Type     NodeType // agent / tool / human / condition / map / reduce
    Config   map[string]any
    Execute  func(ctx context.Context, state WorkflowState) (WorkflowState, error)
}
```

配套：
- `WorkflowState`：节点间共享的可序列化状态。
- `WorkflowEngine`：状态机驱动的事件循环，支持循环、等待、子图。
- `CheckpointStore`：持久化当前激活节点和 state，支持崩溃恢复。

**风险**：改动面大，当前的 `RunBlocking` / `RunBlockingDAG` 会退化为 legacy API。

### 路径 C：保持现状，只优化条件表达和数据流

如果当前 DAG 已经覆盖 80% 的使用场景，可以继续维持现状，只补：

1. 更友好的条件表达式（支持 `==`、`!=`、正则）。
2. Agent 输出通过 `OutputTo` 自动写入下游 Agent 的输入模板（如 `{{researcher.result}}`）。
3. 前端 DAG 可视化（只读展示）。

---

## 6. 决策建议（按优先级）

| 优先级 | 方向 | 理由 | 成本 |
|--------|------|------|------|
| P1 | 路径 A：retry + 等待外部事件 | 解决真实场景的健壮性问题，改动可控 | 中 |
| P2 | 路径 A：工具节点 / judge 节点 | 提升编排表达力，无需完整状态机 | 中 |
| P3 | 路径 C：条件表达式 + 数据流模板 | 快速提升易用性，改动小 | 低 |
| P4 | 路径 B：通用工作流引擎 | 能力最强但改动最大，适合长期重构 | 高 |
| P5 | 可视化 DAG 编辑器 | 前端工作量大，对 backend 是 read-only | 高（前端） |

---

## 7. 与白盒 Agent 设计哲学的兼容性

本项目强调“白盒 Agent”：每个 token、每次 tool call、每个 step 状态转换都生成事件。

任何向通用工作流引擎的演进都必须保留这一点：

- **状态变更必须发事件**：节点启动、完成、失败、等待、跳过都要有对应事件。
- **状态机必须可观测**：当前激活节点、等待原因、workflow state 快照可被查询。
- **LLM 驱动调度必须可见**：如果 Agent 决定动态扩图，这个决定本身要作为 step/tool_call 事件被记录。

Temporal / LangGraph 的 checkpoint 机制值得借鉴，但事件流需要我们自己补充。

---

## 8. 待研究但未展开的课题

1. **长运行 workflow 的持久化**：当前 `RunBlockingDAG` 只把终态写入 DB，中间状态（哪些节点在等待、哪些在重试）是内存中的。是否需要 checkpoint 级别的持久化？
2. **循环与无限步数控制**：支持循环后必须设置总步数/时间上限，避免 LLM 或条件错误导致死循环。
3. **AgentBus vs shared state 的取舍**：AgentBus 消息天然可观测但粗粒度；shared state 灵活但容易变成黑盒。有没有混合方案？
4. **与 MCP / function tools 的整合**：DAG 节点是否可以直接调用 MCP server 作为工具节点？

---

## 9. 参考链接

- LangGraph docs: https://langchain-ai.github.io/langgraph/
- Temporal docs: https://docs.temporal.io/
- Airflow DAG concepts: https://airflow.apache.org/docs/apache-airflow/stable/core-concepts/dags.html
- Prefect: https://www.prefect.io/
- Dify workflow: https://docs.dify.ai/guides/workflow
- FastGPT workflow: https://fastgpt.in/docs/workflow/
