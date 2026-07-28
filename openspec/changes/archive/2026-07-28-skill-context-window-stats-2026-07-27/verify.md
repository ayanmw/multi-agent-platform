# Verification: Context Window Skill 统计与事件同步

## 测试命令

```bash
cd D:\Claude-Code-MultiAgent
go test ./internal/runtime ./internal/llm ./internal/skill ./cmd/server
```

## 前端测试

```bash
cd D:\Claude-Code-MultiAgent\web\v2
npm run test:unit
```

## 编译

```bash
go build ./...
cd web/v2 && npm run build
```

## 向后兼容

```bash
export PYTHONUTF8=1
export LLM_USE_MOCK=true
./scripts/cases-regression.sh
```

## 手动验收

1. 启动 server，启用一个包含大模板的 skill。
2. 发送 task，打开 ContextWindowPanel。
3. 确认 "Skill Injection" 区显示该 skill 名称、模板名、`estimated_tokens`、占比。
4. 在 messages timeline 中 system message 旁看到 `InjectedSkillBadge`。
5. 禁用 skill 后重新发送 task，确认 skill 区为空。
6. 通过 WS 监听 `skill_rendered` 事件，确认每次 run 启动时收到 data.blocks。

## 检查清单

- [ ] `context_window_snapshot` 包含 `skill_blocks`。
- [ ] Skill blocks 不重复计入 total tokens（与 messages[0] 一致）。
- [ ] `skill_rendered` 事件广播。
- [ ] 前端事件类型补齐。
- [ ] ContextWindowPanel 渲染正常，空态不报错。
- [ ] mock regression 21/21 PASS。
