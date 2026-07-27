# Verification: 文件系统 Skill 扫描器

## 测试命令

```bash
cd D:\Claude-Code-MultiAgent
go test ./internal/skill ./cmd/server
```

## 编译

```bash
go build ./...
```

## 向后兼容

```bash
export PYTHONUTF8=1
export LLM_USE_MOCK=true
./scripts/cases-regression.sh
```

期望 21/21 PASS。

## 手动验收

1. 在临时项目目录 `<workdir>/.claude/skills/hello/SKILL.md` 写入：
   ```yaml
   ---
   name: Hello Skill
   description: A test filesystem skill
   tags: [test]
   ---
   You are a helpful assistant, and you always say "loaded from filesystem".
   ```
2. 启动 server，创建 session 指定 `workspace_dir=<workdir>`。
3. 打开 Manage Skills，应看到来源为 `local_file`、名称为 `Hello Skill` 的卡片。
4. 调用 `GET /api/skills?source=local_file` 应返回该 skill。
5. 发送 task，确认 system prompt 中包含 skill 内容。
6. 删除 skill 文件，调用 `POST /api/skills/scan`，Manage Skills 中 skill 消失，`skill_unloaded` 事件广播。
7. 调用 `POST /api/skills/scan-config` 关闭 `.agents/skills` 目录，确认该目录下 skill 不再被发现。

## 检查清单

- [ ] `LoadGlobal` 识别 server CWD 下 `.claude/skills`。
- [ ] `LoadForWorkdir` 识别 session workdir 下 skill。
- [ ] frontmatter 覆盖 name/description/tags/scope 生效。
- [ ] 关闭目录配置后该目录 skill 不加载。
- [ ] 删除文件后 `RefreshAll` 会卸载旧 skill。
- [ ] WS 广播 `skill_loaded` / `skill_unloaded`。
- [ ] mock regression 不破坏。
