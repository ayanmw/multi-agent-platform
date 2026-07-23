# Agent 进程化探索

> 日期：2026-07-22  
> 性质：Phase 8-A 关联方向探索，不写实施计划  

---

## 1. 为什么考虑进程化

当前 `cmd/server` 中的所有 Agent 都在一个 Go 进程内运行，通过 goroutine 并发。这种模式简单、低开销、事件统一，但长期会面临以下挑战：

- **故障隔离弱**：一个 Agent 的 panic 或死循环可能拖垮整个 server；`run_shell` 等工具即使加沙箱也仍共享同一内核。
- **资源预算困难**：CPU/内存/GPU 预算按进程全局计算，无法给单 Agent 精确限流。
- **语言与生态锁定**：新 Agent 必须用 Go 实现，不能复用 Python 数据分析、Node 脚本、专用推理服务或容器化 workload。
- **水平扩展瓶颈**：单机 goroutine 受限于单进程，未来多机调度需要更粗粒度的"任务单元"。

进程化目标不是立即替换现有 runtime，而是为"未来可把单个 Agent 放到独立进程/sidecar/容器中"保留架构空间。

---

## 2. 可能的形态

| 形态 | 说明 | 优点 | 缺点 |
|---|---|---|---|
| A. 进程内 goroutine（当前） | 一个 Go 进程跑所有 Agent | 简单、低延迟、易调试 | 隔离差、跨语言难 |
| B. 子进程 fork（轻量分离） | 用 `os/exec` 启动子进程跑单个 Agent | 崩溃隔离、语言无关 | IPC/事件转发复杂、启动开销 |
| C. sidecar 容器 | 每个 Agent 一个容器 | 强隔离、独立资源限制 | 调度重、成本高 |
| D. Worker pool 远程 Agent | 独立 Agent worker 服务，server 派发任务 | 可水平扩展、语言无关 | 网络延迟、需要注册发现 |
| E. WASM micro-agent | 用 WASM 沙箱跑轻量 agent | 启动快、安全、跨语言 | 能力受限、生态成熟度 |

当前项目更适合走 **B → D** 的渐进路线：先把入口收口为可序列化的 `AgentRunSpec`，再考虑子进程或 sidecar；不直接跳到 sidecar，避免过度工程。

---

## 3. 实现进程化需要解决哪些问题

### 3.1 Agent 运行描述的序列化

必须把一次 Agent 运行的全部输入变成可 JSON/Protobuf 化的纯数据。本轮 Phase 8-A 的 `AgentRunSpec` 就是为此奠基。

### 3.2 事件通道跨进程转发

当前 `runtime.EventBus` 只有 `SendEvent(event.Event)`。跨进程方案可选：

- **stdio JSON streaming**：子进程把 stdout 当事件流；简单但难处理双工。
- **Unix domain socket / named pipe**：父子进程间建立 stream，发送 `event.Event` protobuf/JSON。
- **WebSocket 反向连接**：子进程启动后连回 server 的 WS 点；适合 sidecar/远程 worker。
- **消息队列**：Redis/NATS；适合多机 worker pool。

推荐未来先尝试 Unix domain socket + JSON 事件包，因为与当前事件模型最贴近。

### 3.3 Tool Registry 跨进程共享

Tool 调用目前在进程内完成。子进程化后，需要：

- 把 `ToolDescriptor` 传给子进程。
- 子进程本地实现 builtin tool，或通过 RPC/IPC 把 tool call 回调到父进程执行。

本轮 `ToolDescriptor + ToolExecutor` 拆分让"序列化工具描述"和"在目标进程里构造 executor"成为可能。

### 3.4 Session/Checkpoint 共享

- Session workspace 目录天然可共享（父子进程同一 FS）。
- Checkpoint 可以写到共享文件，父进程触发恢复时读回。
- AgentBus 消息若跨进程，需要按 subTaskID 路由。

### 3.5 生命周期与控制

暂停、恢复、取消操作需要从父进程投递到子进程。未来可复用 cancel context + IPC signal。

---

## 4. 与本项目设计哲学的冲突与调和

白盒 Agent 要求每个 token/tool call/step 事件都实时广播。进程化后：

- 事件必须由子进程发出并通过 IPC 回到父进程再广播；
- 不能用"只返回最终结果"的黑盒封装；
- 需要最小化事件序列化/反序列化延迟。

因此进程间协议必须严格保持事件粒度，不能批量压缩到只剩最终结果。

---

## 5. 暂不实施的判断

- 当前代码规模与团队节奏还不需要真正的进程隔离；
- 先把 `AgentRunSpec`、事件接口、ToolDescriptor 抽象做好，未来切换成本最低；
- 过早 sidecar 会显著增加部署复杂度与延迟，与本项目"从零构建、完全可观测"的渐进式目标不一致。

---

*本文件为方向性研究，未给出实施计划。具体架构整理见 `docs/superpowers/specs/2026-07-22-phase-8a-architecture-evolution-design.md`。*
