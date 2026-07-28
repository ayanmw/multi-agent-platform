# Verification: 前端 SkillManager UI 改造

## 前端测试

```bash
cd /d/Claude-Code-MultiAgent/.claude/worktrees/skill-manager-ui-2026-07-27/web/v2
npm run test:unit
```

结果：19 文件 / 160 用例全绿。

## 前端构建

```bash
cd /d/Claude-Code-MultiAgent/.claude/worktrees/skill-manager-ui-2026-07-27/web/v2
npm run build
```

结果：vite build 成功，无 TS 错误。

## 后端编译

```bash
cd /d/Claude-Code-MultiAgent/.claude/worktrees/skill-manager-ui-2026-07-27
go build ./...
```

结果：无编译错误。

## 向后兼容

```bash
export PYTHONUTF8=1
export LLM_USE_MOCK=true
./scripts/cases-regression.sh
```

结果：21/21 PASS（100%）。

## 手动验收

1. 打开 v2 前端，进入 Manage → Skills。
2. 确认能看到 built_in、local_db、local_file（若已创建）skill。
3. 搜索框输入关键词，列表过滤。
4. 点击 built_in skill，详情弹窗显示"内置不可编辑"；点击 local_file skill 显示"只读"。
5. 点击 New Skill，创建 local_db skill；成功后出现在列表。
6. 点击 Edit，修改 description，保存。
7. 点击 enable switch，确认 WS 广播 `skill_enabled`。
8. 在 CommandBar 输入 `/`，弹出 picker；选择命令或 skill，确认发送后 skill 被启用。
9. 发送 task，ContextWindowPanel 展示 skill 注入区。
10. 删除 local_db skill，列表移除，`skill_unloaded` 广播。

## 检查清单

- [x] SkillManager 替换 SkillPanel。
- [x] useSkills 支持完整 CRUD 与事件同步。
- [x] local_file 只读、built_in 不可编辑。
- [x] CommandBar `/` picker 可用且支持 skill/command 双分组。
- [x] ContextWindowPanel 展示 skill 注入占位区。
- [x] 移动端 manage tab 正常显示。
- [x] mock regression 21/21 PASS。
