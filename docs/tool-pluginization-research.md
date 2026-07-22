# Tool 插件化探索

> 日期：2026-07-22  
> 性质：Phase 8-A 关联方向探索，不写实施计划  

---

## 1. 为什么考虑插件化

当前工具分两类：

1. **BuiltinTool**：在 `internal/tool/builtin.go` 用 Go 函数实现，编译进二进制；
2. **DynamicTool**：通过 REST API 注册，数据驱动执行 shell/http/inline 脚本。

但系统会逐渐需要：

- 第三方贡献者无需重新编译就能添加工具；
- Python/Node 写的现有脚本或 ML pipeline 直接注册为工具；
- 工具版本管理（同一工具多版本并存、灰度发布）；
- 不同 MCP server 的工具能统一以"可序列化描述"接入，而非仅作为内存对象。

插件化目标不是立即实现外部加载，而是先把**"工具描述"与"工具执行体"分离**，让未来多种加载方式（DB、本地文件、MCP、WASM、gRPC plugin）共享同一抽象。

---

## 2. 可能的形态

| 形态 | 说明 | 优点 | 缺点 |
|---|---|---|---|
| A. 内置 Go 函数（当前） | 写 Go 代码 + 编译 | 高性能、类型安全 | 必须重新编译 |
| B. DB 动态 tool（当前） | shell/http/inline 模板 | 运行时注册、简单 | 能力有限、安全难控 |
| C. WASM plugin | WASM 模块作为执行体 | 沙箱、跨语言、启动快 | 接口限制、调试复杂 |
| D. gRPC plugin（HashiCorp go-plugin 模式） | 独立进程实现工具，通过 gRPC 调用 | 语言无关、隔离好 | 网络/序列化开销 |
| E. MCP server | 外部 server 按 MCP 协议暴露工具 | 生态正在形成、标准化 | 协议引入依赖 |
| F. 本地脚本/可执行文件 | 把任意可执行文件注册为 tool | 最简单、广覆盖 | 安全差、输入输出格式难统一 |

当前项目已引入 MCP，可作为 E 的过渡；但 MCP 只是协议，不能解决"工具描述序列化 + 执行体抽象"问题。本轮先做这层抽象。

---

## 3. 关键抽象

### 3.1 ToolDescriptor

纯数据对象，描述一个工具"是什么"。它应该能：

- 从 DB、JSON、MCP server capability、WASM manifest 中构造；
- 序列化为 JSON 后跨进程/网络传输；
- 在 Registry 中作为主键的一部分。

与当前 `Tool` 接口中元数据方法（`Name/Description/Parameters/Tags/Aliases`）对应，但不含闭包。

### 3.2 ToolExecutor

执行体接口：

```go
type ToolExecutor interface {
    Execute(ctx ExecuteContext, input map[string]any) (any, error)
}
```

不同来源对应不同实现：

- `BuiltinExecutor`：Go 函数。
- `DynamicExecutor`：shell/http/inline 模板。
- `WASMExecutor`：加载 WASM 模块并调用导出函数。
- `GRPCExecutor`：连接 gRPC plugin 进程。
- `MCPExecutor`：通过 MCP 协议调用外部 server。

### 3.3 ToolLoader

```go
type ToolLoader interface {
    Load(ctx context.Context) ([]tool.Tool, error)
}
```

- `BuiltInToolLoader`：加载编译期 builtin tool。
- `DBToolLoader`：从 SQLite 加载动态 tool。
- `MCPDiscoveryLoader`：从 MCP manager 同步 tool（或未来事件驱动增量同步）。
- `PluginDirLoader`：扫描本地目录，未来加载 WASM / gRPC config。

---

## 4. 插件化对当前系统的影响

### 4.1 Registry

需要支持同一 `namespace/name` 下多个 version。当前 `FullName` 作为键会覆盖，未来键应改为 `CanonicalName()` = `namespace/name@version`。

### 4.2 Tool 身份判定

硬编码 `IsBuiltin` 必须改为按 `Source() == "builtin"` 判断，因为工具来源会越来越多。

### 4.3 持久化表

`tools` 表需要 namespace/version/source/execution_config，否则无法描述插件来源与执行参数。

### 4.4 安全与审批

外部插件默认应走审批/沙箱；TagPolicyRule 应能按 source 或 tag 强制策略。这是 Phase I 后的扩展点。

### 4.5 事件

工具调用事件仍需保持白盒粒度：tool_call_started、tool_call_output、tool_call_complete。无论执行体是本地函数还是远程 plugin，事件流不变。

---

## 5. 暂不实施的判断

- WASM/gRPC plugin 生态与调试成本较高，当前阶段收益不如先把架构接口理清楚；
- 现有 DynamicTool 已覆盖 shell/http 场景，可平滑迁移到新的 `ToolDescriptor + DynamicExecutor`；
- MCP 工具的加载可继续走现有 mcp manager，未来再统一到 `ToolLoader` 接口；
- 先把核心抽象落地，插件加载器可在后续 phase 逐项实现，避免一次引入过多新概念。

---

## 6. 与本项目其他子系统的关系

- **Skill**：Skill 是 prompt 包，不是 tool，不进入 `Tool` 抽象。
- **Cron**：Cron 的 script/webhook action 可直接复用 `ToolDescriptor` 中 `execution_config` 描述。
- **MCP**：MCP server 的工具未来可作为 `ToolLoader` 的一种来源。
- **Agent 进程化**：跨进程 Agent 必须携带其可用工具的 `ToolDescriptor` 列表，目标进程据此构造 executor。

---

*本文件为方向性研究，未给出实施计划。具体架构整理见 `docs/superpowers/specs/2026-07-22-phase-8a-architecture-evolution-design.md`。*
