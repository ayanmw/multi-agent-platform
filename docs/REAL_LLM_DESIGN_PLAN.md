# 真实 LLM 测试 — 设计优化计划

> 生成日期：2026-07-14
> 来源：真实 LLM 第一轮冒烟测试（`scripts/real-llm-smoke.sh`，`deepseek-v4-flash-local`）发现的设计类/优化类问题
> 性质：非阻断 bug，是架构/精度/鲁棒性层面的优化项，供后续 Phase 规划参考
> 配套：详见 `docs/TEST_REPORT.md` 第 10 章（真实 LLM 第一轮冒烟）

---

## 0. 背景与原则

真实 LLM 测试暴露了 mock 测不到的代码路径（SSE 分块、reasoning 字段、真实 usage、Router 意图分类、错误指纹、WS 背压）。本计划收录的设计类问题分三类：

- **配置/映射类**（D1-D2）：模型权限映射、fallback chain 与实际可用模型不匹配
- **精度/语义类**（D3-D4）：cost 精度截断、reasoning 模型 MaxTokens 语义
- **鲁棒性类**（D5-D8）：取消传播、并发节流、重试、WS 背压、逻辑冗余

优先级标注：🔴 高（影响功能正确性）/ 🟡 中（影响体验或可观测性）/ 🟢 低（代码质量）

---

## D1 🔴 Router fallback chain 与实际可用模型不匹配

**现象**：multi-agent 路径（intent=multi_step → tier=standard）Router 选 `deepseek-v4-pro`，token 无权访问 403；fallback `deepseek-v4-flash`（无 `-local`）也 403；两 agent 反复重试 "repeated LLM error" failed。单 agent 路径用 `deepseek-v4-flash-local` 全成功。

**根因**：
- `internal/llm/router.go:179` `r.registry.GetFallback(primary.Name)` 用 profile 的 `FallbackModel` 字段
- `internal/llm/model_profile.go:308` `deepseek-v4-pro` 的 `FallbackModel="deepseek-v4-flash"`（标准名，非 `-local`）
- DefaultProfiles 注册的是标准云端模型名，但实际部署 token 只能访问 `cfg.LLMModel`（`-local` 变体）
- task #1 已为 primary 路径修了（先注册 `cfg.LLMModel` 克隆 profile），但 **fallback chain 没修**——pro 的 fallback 仍指向标准名

**优化方案**（二选一或结合）：
1. **fallback 指向 cfg.LLMModel**：main.go 注册克隆 profile 时，把所有 standard/premium tier profile 的 `FallbackModel` 改指向 `cfg.LLMModel`（本地兜底模型），而非标准名。这样任何 tier 失败都回退到 token 确定可访问的 `-local`。
2. **Router 选中后校验可达性**：engine.go 在 Router 选中 model 后，若该 model 不在 `routerProviders` map 里（即没构造对应 provider），直接降级到 `cfg.LLMModel`。当前 `routerProviders` 只注册了 `cfg.LLMModel` 和 `"deepseek"` 两个 key，选 `deepseek-v4-pro` 时 `providers[profile.Name]` 查不到 → 走兜底分支，但兜底分支用 `selectedModel` 建新 provider 仍指向 pro → 403。应改成兜底时强制用 `cfg.LLMModel`。

**推荐**：方案 1（改 FallbackModel 指向 cfg.LLMModel），治本且改动小（main.go 注册 profile 时一行）。

**工作量**：小（~10 行 main.go）

---

## D2 🟡 multi-agent 默认 max_steps 与真实 LLM 不匹配

**现象**：multi-agent case 默认 max_steps=3，真实 LLM 下 researcher/writer 系统提示复杂，3 步必超限 failed。

**根因**：环境配置不合理，非代码 bug。预设 case 的 max_steps 按 mock 行为调优（mock 几步就返回 final），真实 LLM 需要更多步。

**优化方案**：
- 预设 case 的 max_steps 区分 mock/real：mock 模式保持小值（3-5，快），real 模式用大值（≥20）
- 或统一调大默认 max_steps 到 20-30，mock 下 LLM 早返回不影响
- 已临时修正：`scripts/real-llm-smoke.sh` 场景3 max_steps 3→100、场景4 2→20

**工作量**：小（调 case 定义或脚本）

---

## D3 🟡 cost 整数 cents 精度截断，小对话显示 $0

**现象**：`/api/costs` 对单次小对话返回 `total_cost_usd=0`，虽然 tokens 记录正确（非零）。场景4 两步分别算出 0.112 cents、0.072 cents，被 `/ 1_000_000` 整数除截断到 0。

**根因**：
- `internal/cost/cost_tracker.go:307-311` `CalculateCost` 用整数 cents：`(tokens * price_cents) / 1_000_000`
- price_cents = `InputPrice * 100`（如 0.14 → 14 cents/1M）
- 单次小对话（几千 token）算出的 cents < 1，整数除法截断到 0
- 这是**既定设计**，有 `TestCalculateCost "small token count truncates toward zero"` 单测保护

**优化方案**（二选一）：
1. **提高精度单位到 micro-cents**（1/1,000,000 USD）：CostCents 改 CostMicroCents，`(tokens * price * 1_000_000) / 1_000_000`。改动：CostRecord schema + DB 列 + 前端显示 + 单测。工作量中等。
2. **改浮点 USD**：CostCents 改 CostUSD float64，`tokens * price / 1_000_000`。精度有浮点误差但小对话非 0。改动同上但更简单。

**决策点**：用户要求"costs 仅供参考但必须得有（非 0）"。当前整数 cents 对小对话天然 0。若接受"小对话 cost 显示 0、tokens 仍正确"则不用改；若要求小对话也非 0 则需 D3。

**工作量**：中（方案 2 约 30 行 + 单测调整）

---

## D4 🟡 reasoning 模型 MaxTokens 语义，Result 空但标 completed

**现象**：`deepseek-v4-flash-local` 后端是 Step-3.7-Flash（reasoning 模型），4096 MaxTokens 全用在思维链（reasoning），正文 content 为空、finish_reason=length，但任务被标记 completed。用户看到"任务完成"却无输出。

**根因**：
- `EngineConfig.MaxTokens=4096` 对 reasoning 模型不够（思维链吃满预算，正文没开始）
- engine 判 completed 的条件不区分"有 content"和"流结束但 content 空"

**优化方案**：
1. **reasoning 模型单独配 MaxTokens**：ModelProfile 加 `MaxOutputTokens`（已有字段，deepseek-v4-flash=4096），reasoning 模型设大（如 8192-16384）。engine 用 profile.MaxOutputTokens 覆盖 EngineConfig.MaxTokens。
2. **completed 判定加 content 检查**：若 finish_reason=length 且 content 空，标记 failed/needs_more_tokens 而非 completed，提示用户"输出被截断"。
3. **前端展示 reasoning**：task #1 已加 `reasoning` 字段转发为 llm_delta，前端可折叠展示思维链（即使 content 空也有 reasoning 可看）。

**推荐**：方案 1 + 3 结合。方案 1 治本（给够预算），方案 3 兜底（预算仍不够时用户能看到 reasoning）。

**工作量**：小-中（方案 1 约 20 行 engine.go；方案 3 前端组件）

---

## D5 🟡 openai_provider chunk 循环无 ctx.Done()，取消不生效

**现象**：`internal/llm/openai_provider.go:120-254` ChatStream 的 chunk 循环没有 `ctx.Done()` 检查（`client.go` 版有）。客户端取消时这一路继续跑到 120s HTTP 超时或流结束。

**根因**：OpenAIProvider 的 ChatStream 实现遗漏了 context 取消检查。

**优化方案**：在 chunk 循环里加 `select { case <-ctx.Done(): return ctx.Err(); default: }`，或在 reader 读取时用 `ctx.Done()` 监听。

**工作量**：小（~5 行）

---

## D6 🟡 多 Agent 无并发节流，易触发 429

**现象**：`internal/orchestrator/orchestrator.go` RunBlocking 用 goroutine + WaitGroup 并发拉起所有 agent，无并发节流。N 个 agent 同时打真实 API，易触发服务端 429。

**根因**：`internal/pool/pool.go` 有 WorkerPool（信号量 `make(chan struct{}, workers)`）但 `dispatch()` 是占位实现（`pool.go:167-168` `_ = ctx; _ = cancel`），未接入引擎。

**优化方案**：
1. 短期：orchestrator 用 `semaphore.Weighted` 或带缓冲 channel 限制并发 agent 数（如 ≤2）
2. 中期：完善 pool.WorkerPool.dispatch 接入引擎，按 Router 配置的 RateLimitRPM 节流

**工作量**：小（方案 1 ~15 行）/ 中（方案 2）

---

## D7 🟢 无 HTTP 错误重试逻辑

**现象**：`openai_provider.go` 失败直接返回错；`engine.go:1246` fallback provider 只在 Router 选模型失败时回退，不是 HTTP 错误重试。瞬时网络错误/429 也直接失败。

**优化方案**：对可重试错误（429、5xx、网络超时）加指数退避重试（如 3 次）。区分"可重试错误"与"重复错误"（D8 的 isRepeatingError 归一化已铺路）。

**工作量**：中（~50 行 + 重试策略配置）

---

## D8 🟢 engine 循环内重复保存 think（逻辑冗余）

**现象**：`internal/runtime/engine.go:834` `for _, tc := range toolCalls` 循环内每个 tool call 执行前都 `saveStep(think, stepIdx)`，stepIdx 不自增 → 一次 LLM 返回 N 个 tool_calls 时同一 think 保存 N 次。

**根因**：task #2 已用 uuid 后缀修了主键碰撞（1555 消失），但**重复保存本身是逻辑冗余**——think step 应在循环外保存一次，而非循环内 N 次。

**优化方案**：把 `saveStep(think)` 提到 `for _, tc := range toolCalls` 循环之前，只保存一次。tool_call/observation 仍按 per-tool 保存。

**工作量**：小（~5 行调整循环结构）+ 需验证历史回放顺序不变

---

## D9 🟢 WS broadcast 背压，慢客户端丢事件

**现象**：`internal/ws/hub.go:79-93` broadcast 是无缓冲 channel，client Send 缓冲 256，满了 `default` 丢事件。真实 LLM delta 频率高，前端慢或断连时事件可能被丢。

**优化方案**：
1. 慢客户端检测 + 主动断开（背压时关闭该 client 而非静默丢）
2. 关键事件（task_completed/failed）不走 default 丢弃，保证终态必达
3. 事件队列按优先级（delta 可丢，终态不可丢）

**工作量**：中（~40 行 hub.go）

---

## 优先级排序与建议批次

| 批次 | 项 | 理由 |
|------|-----|------|
| 第 1 批（功能正确性） | D1 Router fallback、D4 reasoning MaxTokens | 直接导致 multi-agent failed / 用户看不到输出 |
| 第 2 批（鲁棒性） | D5 ctx.Done、D6 并发节流 | 真实 LLM 下取消/限流场景必备 |
| 第 3 批（精度/体验） | D3 cost 精度、D8 重复保存 | 可观测性 + 数据干净 |
| 第 4 批（长期） | D2 max_steps 策略、D7 重试、D9 WS 背压 | 优化项，非阻断 |

---

## 已修复项（不在本计划，记录对照）

- ✅ R1 Router 死代码激活（task #1）
- ✅ R1-回归 Router 激活后 403 死循环（task #1，注册 cfg.LLMModel 克隆 profile）
- ✅ R2 isRepeatingError 对 403 永不触发（task #1，错误指纹归一化）
- ✅ R3 steps.id UNIQUE 碰撞（task #2，uuid 后缀）
- ✅ R4 cost model 名对齐 selectedModel（task #1）
- ✅ R6 迁移日志刷屏（task #4）
- ✅ DefaultProfiles 价格笔误修正（task #3，flash 0.28、pro 0.435/0.87）
- ✅ 模型价格查看/修改 API + 前端 UI（task #3）

---

*计划制定：2026-07-14，leader 基于 5 个子 agent 的真实 LLM 测试与修复报告整理*
