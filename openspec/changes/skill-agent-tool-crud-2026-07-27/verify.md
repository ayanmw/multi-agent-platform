# Verification: Skill Agent Tool 完整 CRUD

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

## 手动验收

1. 用 Agent 会话调用 `skill/list`，确认只返回 summary。
2. 调用 `skill/get` 查看某个 built_in 的完整模板。
3. 调用 `skill/update_local` 更新 built_in：
   - 应自动 fork 为 local_db shadow。
   - 再次 `skill/get` 看到修改后内容。
   - registry/list 中来源显示 `local_db`，ID 相同。
4. 调用 `skill/delete_local` 删除该 shadow：
   - 删除成功。
   - `skill/get` 重新得到 built_in 原始内容。
5. 尝试删除 built_in 或 local_file skill，应返回 403。
6. 创建 project scope skill，从另一个 project 的 session 调用 `skill/update_local`，应被禁止。

## 检查清单

- [ ] LLM 通过 7 个 tool 完整 CRUD。
- [ ] built_in fork shadow 行为前后端一致。
- [ ] `skill/list` / `skill/search` 不泄露完整 prompt。
- [ ] project scope 越权修改被拒绝。
- [ ] 删除 shadow 后 built_in 恢复。
- [ ] mock regression 21/21 PASS。
