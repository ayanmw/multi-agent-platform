# verify: fix-agent-model-bright-karp

## 验证结果

### 后端单元测试

```bash
# 已执行
go test ./cmd/server/... ./internal/orchestrator/... ./internal/runtime/... ./pkg/db/...
```

结果：全部通过 ✅

统计：
- `cmd/server` PASS
- `internal/orchestrator` PASS（新增 `TestRunAgent_UsesDBAgentModel`）
- `internal/runtime` PASS
- `pkg/db` PASS

### 前端单元测试

```bash
cd web/v2
npm run build
npm run test
```

结果：TypeScript 编译通过，20 个测试文件 173 个测试全部通过 ✅
新增 `OptionsFlyout.test.ts` 3 个测试覆盖选择/默认值/外部 modelValue。

### mock cases 回归

```bash
bash scripts/cases-regression.sh
```

结果：21/21 PASS ✅

### 全量 Go 测试

```bash
go test ./...
```

结果：仅 `internal/tool` 的 `TestRealGeminiSearch` 因 Google Gemini API 免费配额耗尽（HTTP 429）失败；这是已知外部依赖问题，与本变更无关。其余全部通过。

### 代码审查修复

- runner subagent 初次实现中 `runAgentLoopWithTurn` 对同一 agent 查询两次 DB，已合并为单一 `agentRecord`。
- `Model` 字段注释已修正，明确 `spec.Model` 为最高优先级。
- `Recover` 路径已补充非 "no rows" 错误的 warning 日志。
- `resolveEffectiveModel` 后增加空行，避免与 `agentAllowedTools` 注释粘连。

## 尚未进行

- 真实 LLM 端到端冒烟验证（需要可用 LLM endpoint；当前 `.env` 的 Qwen 不可用，MiniMax 未配置）。
