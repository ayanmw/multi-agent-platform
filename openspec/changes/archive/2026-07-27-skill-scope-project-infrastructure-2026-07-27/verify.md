# Verification: Skill Scope / Project / Workdir 基础设施

## 单元/集成测试

```bash
cd D:\Claude-Code-MultiAgent
go test ./internal/skill ./internal/runtime ./cmd/server
```

结果：全部 PASS，无 race。

```
ok  	github.com/anmingwei/multi-agent-platform/internal/skill	0.251s
ok  	github.com/anmingwei/multi-agent-platform/internal/runtime	0.603s
ok  	github.com/anmingwei/multi-agent-platform/cmd/server	12.988s
```

## 编译检查

```bash
go build ./...
```

结果：exit 0，全部编译通过。

## 向后兼容回归

```bash
# Windows Git Bash
export PYTHONUTF8=1
export LLM_USE_MOCK=true
./scripts/cases-regression.sh
```

结果：21/21 PASS（100%）。

汇总：

```
通过率: 21/21 (100%)
```

所有 L1-L5 case（含 skill-code-helper、multi-agent 静态/动态编排）均通过；
L4/L5 编排事件 `decompose_done` / `agent_dispatched` / `agent_completed` 与 `child_tasks[].steps` 回填正常。

## 手动/运行期验证

- `GET /api/skills?project_id=proj-go-v2&scope=project` 等过滤已在 `cmd/server/api_skill_project_test.go` 中覆盖。
- `ResolveActiveSkills` 的去重、project 覆盖 global、workdir 子目录匹配已在 `internal/skill/registry_test.go` 中覆盖。
- `EngineConfig.SkillVariables` 在 `cmd/server/runner.go` 注入，renderer nil-safe 已在 `internal/skill/renderer.go` 兜底。

## 附加发现与修复

回归脚本在 `.env` 设置 `SERVER_PORT=30080` 后无法启动，因为脚本只能覆盖 `SERVER_PORT` 环境变量，
但 dotenv 默认 `.env 优先`，导致脚本设置被 `.env` 覆盖。已在 `cmd/server/main.go` 添加 `config.SetOSFirst()`
并在 `-port` 非空时直接采用命令行端口，保证脚本与环境变量的优先级正确。
修复后回归脚本在 `SERVER_PORT=30080` 的 `.env` 下仍可正常于端口 18105 运行。

## 验收清单

- [x] `GET /api/skills?project_id=x&scope=project` 返回正确子集。
- [x] `ResolveActiveSkills` 单元测试覆盖去重与过滤。
- [x] `EngineConfig.SkillVariables` 非空，`{{workspace_dir}}` 被正确渲染。
- [x] Mock 回归 21/21 PASS。
