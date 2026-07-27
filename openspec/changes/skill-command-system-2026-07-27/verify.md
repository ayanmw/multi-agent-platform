# Verification: Skill Command 系统

## 后端测试

```bash
cd D:\Claude-Code-MultiAgent
go test ./internal/skill ./cmd/server
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

1. 在 `<workdir>/.claude/commands/ops/new.md` 写入：
   ```yaml
   ---
   name: New OpenSpec Change
   description: Create a new OpenSpec change
   skill: openspec-new-change
   ---
   You are creating a new OpenSpec change...
   ```
2. 启动 server，创建 session 绑定 workdir。
3. CommandBar 输入 `/`，应看到 `ops:new` 命令。
4. 选中 `/ops:new`，输入框预填充，发送剩余文本。
5. 确认关联 skill `openspec-new-change` 被启用，LLM 收到 skill prompt。
6. 创建无 `skill` 的命令 `.claude/commands/hello.md`，仅含 prompt；invoke 后确认 prompt 作为临时 skill 注入。
7. 不同 workdir 下的同名命令不冲突。

## 检查清单

- [ ] 命令 ID 冒号分层正确。
- [ ] 前端 `/` 触发 SkillPicker。
- [ ] invoke 接口启用关联 skill。
- [ ] 无关联 skill 时临时 skill 注入。
- [ ] project scope 命令有权限隔离。
- [ ] 事件 `skill_command_loaded` 广播。
- [ ] mock regression 21/21 PASS。
