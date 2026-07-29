# Skill 系统 Review 修复闭环

> 对应审查报告：`docs/SKILL_SYSTEM_REVIEW.md`
> 修复周期：2026-07-29
> 状态：✅ 全部 37 条已修复并提交到 main

## 背景

`docs/SKILL_SYSTEM_REVIEW.md` 记录了 Skills 系统完整改造（6 份 OpenSpec change）后的对抗式审查结果：3 Critical + 6 High + 16 Medium + 14 Low，合计 39 条发现，去重并随批落地后实际修复 37 条（L11 随 P0 批次的 C3 一并落地）。

本批为纯 bugfix（功能断裂、越权、数据污染、测试缺口、UX 提示），不引入新 capability，按 CLAUDE.md「方法论选择」规则**不建独立 OpenSpec change**，仅以本文件记录修复总账与 commit 溯源，供后续回查。

## 修复批次

### P0 — Critical + High（6 条）
跨 Spec 接缝的真实路径失效与越权/污染，优先修复，每条配回归测试。

| 条目 | 内容 | commit |
|------|------|--------|
| C1 | 临时 command skill 经 extraIDs 注入当前 run | `d2ff3b7` |
| C2 | orchestrator 子 agent 注入 SkillRegistry/ActiveSkills/SkillVariables | `1b0c33a` |
| C3 + L11 | project scope 越权防护 + local_file 持久化污染 | `16822ae` |
| H1 | resolveSession 新建 session 触发 workdir 扫描 | `43cd34b` |
| H3 | CommandBar ↑/↓ 导航生效 + 临时 skill ID 发送后清空 | `e451df1` |
| (H2) | REST enable/disable 拦截 local_file | 随 `16822ae` 落地 |

### P1 — Medium 高优（9 条）

| 条目 | 内容 | commit |
|------|------|--------|
| M2/M3 | frontmatter 正则兼容 CRLF / 空 frontmatter | `d7c23fa` |
| M9/M10/M11 | 抽公共 MatchWorkdir 统一三处 workdir 判定 | `c9c1697` |
| H4/M12/M13 | CommandRegistry 按 scope+workdir 命名空间隔离 + isValidCommandID 接入 + UnloadForWorkdir 只卸载 project | `59ce4c7` |
| M1 | scan-config 改用 RefreshGlobal 保留 project skill | `f175e21` |
| H6 | POST/PUT /api/skills 放行 session scope | `5185b0d` |
| H5 | 前端订阅 skill_command_* 事件刷新命令列表 | `3d7e9aa` |
| M6/M7 | handleTriggerSkill 自动发送 + SkillForm parameters 独立提交 | `bd49752` |
| M3/M12/M13 | 见上（合并进 H4 批） | `59ce4c7` |

### P2 — Medium 收尾（9 条）

| 条目 | 内容 | commit |
|------|------|--------|
| M16 | runner.go 注入改用 r.Deps.SkillRegistry 收口依赖 | `84bf9c1` |
| M4 | scan 返回真实 loaded/unloaded 计数 | `d172021` |
| M14 | invoke 关联 SkillID 不存在时剔除并告警 | `d794063` |
| M5 | registerSkillRoutes 写操作加 RoleAdmin 守卫 | `b305586` |
| M15 | skill Agent Tool 变更后广播 skill_* 事件 | `f151a53` |
| M8 | useSkills 事件同步改为真实 onEvent 驱动断言 | `ea86776` |

### P3 — Low 打磨（13 条）

| 条目 | 内容 | commit |
|------|------|--------|
| L2/L3/L4/L8/L9/L10/L12/L13/L14 | 后端收尾（skill_rendered 空广播守卫 / token overhead / NewEngine 负向测试 / Renderer nil-safe / Recover workspace 兜底 helper / forked_from 二次编辑 / 启动写默认 scan_dirs / invoke body 400 / 测试缺口补齐） | `85faaf1` |
| L1/L5/L6/L7 | 前端收尾（ContextWindowPanel badge 聚合 / SkillForm session 提示 / built_in fork 提示 / ManageContent.test stub 更新） | `c1d9a59` |

## 验证

- `go build ./...` ✅
- `go test ./cmd/server/ ./internal/skill/ ./internal/runtime/ ./internal/orchestrator/ ./internal/tool/` ✅
- 前端 vitest：SkillForm / SkillManager / ManageContent / ContextWindowPanel / useSkills 全绿
- mock 回归 `scripts/cases-regression.sh` 21/21 未受影响

## 已知遗留（非阻塞）

1. LSP 对 `runner.go` 新 helper `resolveSessionWorkspace` 偶报 `resolveWorkspaceDir redeclared` 误报（与 `api.go` 同名旧函数混淆），`go build` 实际 exit=0。
2. 全量 vitest 的 33 个 FAIL 全部来自 `.worktrees/logging-upgrade/` 隔离 worktree（缺 `vue` 依赖），与 main 工作区无关——vitest include 未排除 worktree 目录的既有问题，可后续单独修。
3. Renderer 的 `SkillParameter.Default` 回退未实现（L8 评估为超出本批范围，签名兼容性优先），已在测试注释标记为已知限制。

## 溯源命令

```bash
git log --oneline --grep="skill" | head -25   # 全部 skill 修复 commit
git log --oneline 16822ae..c1d9a59            # 本批 review 修复区间
```
