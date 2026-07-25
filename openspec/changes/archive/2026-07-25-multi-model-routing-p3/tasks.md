# Tasks: Multi-Model Layered Routing P3

> 变更：`2026-07-25-multi-model-routing-p3`  
> 状态：进行中  
> 方法论：OpenSpec + superpowers，单个小任务完成后提交 Git。

- [x] **Task P3-0**: 创建 OpenSpec change（proposal / design / tasks）
- [x] **Task P3-1**: 启动时预注册所有 tier 模型的 RouterProviders
  - [x] 1-1 读取 `cfg.Models` + `DefaultProfiles()` 构建 `routerProviders` map
  - [x] 1-2 为 deepseek/openai/anthropic/gemini model 创建对应 provider
  - [x] 1-3 注入 `AgentDeps.RouterProviders` 并验证 `resolveProvider` 路径
  - [x] 1-4 `go test ./cmd/server/...` + `go build ./...` + 提交
- [x] **Task P3-2**: Agent 配置绑定到 RouteRequest
  - [x] 2-1 扩展 `AgentRunSpec` 携带 `PreferredModel` / `PreferredTier` / `AllowAutoRoute` / `MaxCostUSD` / `AgentRole`
  - [x] 2-2 在 `runner.go` 中通过 Agent DB 记录或 spec 字段填充 `EngineConfig.MaxCostUSD`
  - [x] 2-3 在 `Engine.think` 中把这些字段传给 `RouteRequest`
  - [x] 2-4 添加 `ParseTier` helper 解析 tier 字符串
  - [x] 2-5 运行测试 + 提交
- [x] **Task P3-3**: Router `intent_classified` 事件去重
  - [x] 3-1 在 `Router` 中新增 `emitted` map + `emitOnce` 方法
  - [x] 3-2 `intent_classified` 改用 `emitOnce`，`model_rate_limited` 保持每次广播
  - [x] 3-3 新增单测验证重复 `Select` 只产生一次 `intent_classified`
  - [x] 3-4 运行测试 + 提交
- [x] **Task P3-4**: RateLimiter 接入真实 LLM 调用
  - [x] 4-1 `EngineConfig` 新增 `RateLimiter` 字段
  - [x] 4-2 `Engine.think` 调用成功后 `RecordCall(selectedModel)`
  - [x] 4-3 `cmd/server` 把同一个 `RateLimiter` 传给 Router 和 EngineConfig
  - [x] 4-4 新增测试验证真实调用后限流生效
  - [x] 4-5 运行测试 + 提交
- [x] **Task P3-5**: 新增 Engine 路由事件测试
  - [x] 5-1 创建 `internal/runtime/engine_routing_test.go`
  - [x] 5-2 测试 `cost_budget_exceeded` 事件与任务失败
  - [x] 5-3 测试 `model_fallback_used` 事件与成功重试
  - [x] 5-4 运行测试 + 提交
- [x] **Task P3-6**: 前端 RoutingPanel 增强
  - [x] 6-1 `useRouteEvents.ts` 增加 `fallbacks` / `budgetExceeded` 聚合
  - [x] 6-2 `RoutingPanel.vue` 渲染 fallback / budget 状态徽章
  - [x] 6-3 `npm run build` 通过
  - [x] 6-4 运行测试 + 提交
- [x] **Task P3-7**: 全量验证与归档
  - [x] 7-1 `go test ./...` 全绿
  - [x] 7-2 `web/v2 npm run build` 通过
  - [x] 7-3 更新 `docs/superpowers/plans/2026-07-25-multi-model-layered-routing-plan.md` 标注 P3 完成
  - [x] 7-4 更新 `roadmaps/ROADMAP.md` 到 v0.14.1 Alpha
  - [x] 7-5 提交并归档 OpenSpec change 到 `openspec/changes/archive/`
