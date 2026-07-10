#!/usr/bin/env bash
# =============================================================================
# Multi-Agent Platform — 多 Agent 编排专项评测脚本 (维度 C)
# =============================================================================
# 评测 POST /api/multi-agent 的 orchestrator 拆分、子任务派发、child_tasks
# 持久化、多 agent 事件区分等能力。使用 mock LLM 确定性运行。
#
# 测试项：
#   1. multi_agent case — 2 agent 拆分 (researcher + writer)
#      a. 响应字段完整性 (agent_ids, agent_count, task_id, session_id, status)
#      b. root task 最终 status
#      c. child_tasks 数组
#      d. steps 数组多 agent_id 分布
#      e. 子任务 (taskID_agentID) 存在性与 status
#   2. code_gen case — 1 agent 拆分 (coder)
#   3. default case — 1 agent 拆分 (agent_default)
#
# 独立端口 18103 + 独立临时 DB，不污染仓库环境。
# =============================================================================
set -u

# ---- 配置 -------------------------------------------------------------------
PORT=18103
BASE="http://localhost:${PORT}"
DB_PATH="/tmp/ma-smoke-$$.db"
SERVER_BIN="/tmp/ma-smoke-server-$$.exe"
SERVER_LOG="/tmp/ma-smoke-server-$$.log"
SERVER_PID=""
PASS=0; FAIL=0; SKIP=0
RESULTS=()
FINDINGS=()

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null
    wait "${SERVER_PID}" 2>/dev/null
  fi
  rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" 2>/dev/null
  rm -f ./tmp/mock_gen.go 2>/dev/null
  rmdir ./tmp 2>/dev/null
  rm -f /tmp/ma-smoke-*.json 2>/dev/null
}
trap cleanup EXIT

# ---- 辅助函数 ---------------------------------------------------------------

# 解析 multi-agent 响应，输出 JSON 结构
parse_ma_response() {
  local json="$1"
  local escaped
  escaped=$(printf '%s' "$json" | sed "s/'/\\\\'/g")
  node -e "
const d = process.argv[1];
try {
  const o = JSON.parse(d);
  console.log(JSON.stringify({
    session_id: o.session_id || '',
    task_id: o.task_id || '',
    agent_count: o.agent_count || 0,
    agent_ids: o.agent_ids || [],
    max_steps: o.max_steps || 0,
    status: o.status || ''
  }));
} catch(e) { console.log(JSON.stringify({error: e.message})); }
" "$escaped"
}

# 解析 task detail，输出结构化 JSON
parse_task_detail() {
  local json="$1"
  local escaped
  escaped=$(printf '%s' "$json" | sed "s/'/\\\\'/g")
  node -e "
const d = process.argv[1];
try {
  const o = JSON.parse(d);
  const task = o.task || null;
  const steps = o.steps || [];
  const childTasks = o.child_tasks || [];
  const agentIdSet = {};
  const typeCount = {};
  steps.forEach(s => {
    agentIdSet[s.agent_id] = (agentIdSet[s.agent_id]||0)+1;
    typeCount[s.type] = (typeCount[s.type]||0)+1;
  });
  console.log(JSON.stringify({
    task_status: task ? task.status : 'not_found',
    task_agent_ids: task ? (task.agent_ids||[]) : [],
    task_is_root: task ? !!task.is_root : false,
    task_parent: task ? (task.parent_task_id||'') : '',
    steps_count: steps.length,
    steps_agent_ids: Object.keys(agentIdSet),
    steps_agent_id_counts: agentIdSet,
    steps_type_count: typeCount,
    child_tasks_count: childTasks.length,
    child_tasks: childTasks.map(c => ({id:c.id, status:c.status, agent_ids:c.agent_ids||[]}))
  }));
} catch(e) { console.log(JSON.stringify({error: e.message})); }
" "$escaped"
}

# 从 parsed JSON (单行) 中提取字段（node 版，支持数组/对象/数字/字符串）
pget() {
  local parsed="$1" field="$2"
  printf '%s' "$parsed" | node -e "
const d = JSON.parse(require('fs').readFileSync(0,'utf8'));
const v = d[process.argv[1]];
if (v === undefined) console.log('');
else if (typeof v === 'object') console.log(JSON.stringify(v));
else console.log(String(v));
" "$field" 2>/dev/null
}

# 获取 task detail body（仅 JSON body，不含状态码）
get_task_body() {
  local tid="$1"
  curl -s "${BASE}/api/tasks?id=${tid}" 2>/dev/null | tr -d '\r'
}

# 获取 task detail: 输出 "code<TAB>body"
get_task_detail() {
  local tid="$1"
  local body
  body=$(get_task_body "$tid")
  local code="200"
  if [[ -z "$body" ]]; then code="000"; fi
  printf '%s\t%s' "$code" "$body"
}

# 直接从 task detail 提取 task.status，避免 cut/printf 解析问题
get_task_status() {
  local tid="$1"
  curl -s "${BASE}/api/tasks?id=${tid}" 2>/dev/null | tr -d '\r' | node -e "
try {
  const d = JSON.parse(require('fs').readFileSync(0,'utf8'));
  console.log((d.task && d.task.status) || 'not_found');
} catch(e) { console.log('parse_error'); }
" 2>/dev/null
}

# 记录结果: record_result <name> <PASS|FAIL|SKIP> <evidence>
record_result() {
  local name="$1" result="$2" evidence="$3"
  RESULTS+=("[${result}] ${name}: ${evidence}")
  case "$result" in
    PASS) PASS=$((PASS+1)) ;;
    FAIL) FAIL=$((FAIL+1)) ;;
    SKIP) SKIP=$((SKIP+1)) ;;
  esac
  printf '%-6s %-35s %s\n' "[${result}]" "$name" "$evidence"
}

print_section() { echo; echo "===== $1 ====="; }

# 轮询 root task 直到 status 稳定非 running（连续 3 次相同），或超时 90s
poll_root_task() {
  local tid="$1"
  local deadline=$((SECONDS + 90))
  local prev_status=""
  local stable_count=0
  while [[ $SECONDS -lt $deadline ]]; do
    local status
    status=$(get_task_status "$tid")
    if [[ -n "$status" && "$status" != "running" && "$status" != "not_found" && "$status" != "parse_error" ]]; then
      if [[ "$status" == "$prev_status" ]]; then
        stable_count=$((stable_count+1))
        if [[ $stable_count -ge 2 ]]; then
          printf '%s' "$status"
          return 0
        fi
      else
        prev_status="$status"
        stable_count=1
      fi
    else
      prev_status=""
      stable_count=0
    fi
    sleep 1
  done
  printf 'timeout'
  return 1
}

# ---- 编译服务 ---------------------------------------------------------------
echo "[setup] 编译后端服务..."
if ! go build -o "${SERVER_BIN}" ./cmd/server 2>"${SERVER_LOG}"; then
  echo "[FATAL] 编译失败，日志见 ${SERVER_LOG}"
  cat "${SERVER_LOG}"
  exit 2
fi
echo "[setup] 编译成功 OK"

# ---- 启动服务 ---------------------------------------------------------------
echo "[setup] 启动服务 (port=${PORT}, DB=${DB_PATH}, LLM_USE_MOCK=true)..."
LLM_USE_MOCK=true \
REQUIRE_AUTH=false \
SERVER_PORT="${PORT}" \
DB_PATH="${DB_PATH}" \
  "${SERVER_BIN}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!

# ---- 等待健康 ---------------------------------------------------------------
echo "[setup] 等待 /healthz 就绪..."
ready=0
for i in $(seq 1 60); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/healthz" 2>/dev/null || true)
  if [[ "$code" == "200" ]]; then ready=1; break; fi
  sleep 0.5
done
if [[ "$ready" != "1" ]]; then
  echo "[FATAL] 服务 30s 内未就绪。服务日志："
  tail -30 "${SERVER_LOG}"
  exit 3
fi
echo "[setup] 服务就绪 OK"

# =============================================================================
# 测试 1: multi_agent case — 2 agent 拆分 (researcher + writer)
# =============================================================================
print_section "测试 1: multi_agent case (researcher + writer)"

echo "  [1.1] POST /api/multi-agent ..."
MA_RESP=$(curl -s -X POST "${BASE}/api/multi-agent" \
  -H 'Content-Type: application/json' \
  --data '{"input":"research and write a report","case_type":"multi_agent"}' 2>/dev/null | tr -d '\r')
echo "    响应: ${MA_RESP}"

MA_PARSED=$(parse_ma_response "$MA_RESP")
MA_SESSION=$(pget "$MA_PARSED" "session_id")
MA_TASK=$(pget "$MA_PARSED" "task_id")
MA_COUNT=$(pget "$MA_PARSED" "agent_count")
MA_IDS=$(pget "$MA_PARSED" "agent_ids")
MA_STATUS=$(pget "$MA_PARSED" "status")
echo "    session_id=${MA_SESSION}, task_id=${MA_TASK}"
echo "    agent_count=${MA_COUNT}, agent_ids=${MA_IDS}, status=${MA_STATUS}"

# 检查 a: 响应字段完整性
if [[ -n "$MA_TASK" && "$MA_COUNT" == "2" && "$MA_IDS" == '["agent_researcher","agent_writer"]' ]]; then
  record_result "1a 响应字段完整性" "PASS" \
    "agent_count=2, agent_ids=[agent_researcher,agent_writer]"
else
  record_result "1a 响应字段完整性" "FAIL" \
    "agent_count=${MA_COUNT}, agent_ids=${MA_IDS}, task_id=${MA_TASK} (期望 2 agent)"
fi

# 检查 status 字段
if [[ "$MA_STATUS" == "started" ]]; then
  record_result "1a 响应 status=started" "PASS" "status=started"
else
  record_result "1a 响应 status=started" "FAIL" "status=${MA_STATUS} (期望 started)"
fi

echo "  [1.2] 轮询 root task 直到完成..."
ROOT_FINAL=$(poll_root_task "$MA_TASK")
echo "    root task 最终 status=${ROOT_FINAL}"

# 检查 b: root task 最终 status
if [[ "$ROOT_FINAL" == "completed" || "$ROOT_FINAL" == "failed" ]]; then
  record_result "1b root task 最终 status" "PASS" \
    "status=${ROOT_FINAL} (engine.updateTask 用 rootTaskID 更新 root task)"
else
  record_result "1b root task 最终 status" "FAIL" \
    "status=${ROOT_FINAL} (期望 completed/failed)"
fi

# 获取 root task 最终详情
ROOT_BODY=$(get_task_body "$MA_TASK")
ROOT_PARSED=$(parse_task_detail "$ROOT_BODY")
ROOT_STATUS=$(pget "$ROOT_PARSED" "task_status")
ROOT_STEPS_COUNT=$(pget "$ROOT_PARSED" "steps_count")
ROOT_STEPS_AGENTS=$(pget "$ROOT_PARSED" "steps_agent_ids")
ROOT_STEPS_AGENT_COUNTS=$(pget "$ROOT_PARSED" "steps_agent_id_counts")
ROOT_STEPS_TYPE_COUNT=$(pget "$ROOT_PARSED" "steps_type_count")
ROOT_CHILD_COUNT=$(pget "$ROOT_PARSED" "child_tasks_count")
ROOT_CHILD=$(pget "$ROOT_PARSED" "child_tasks")
ROOT_STEPS_COUNT=${ROOT_STEPS_COUNT:-0}
ROOT_AGENT_IDS=$(pget "$ROOT_PARSED" "task_agent_ids")
echo "    root task agent_ids=${ROOT_AGENT_IDS}"
echo "    steps_agent_ids=${ROOT_STEPS_AGENTS}"
echo "    steps_agent_counts=${ROOT_STEPS_AGENT_COUNTS}"
echo "    steps_type_count=${ROOT_STEPS_TYPE_COUNT}"
echo "    child_tasks_count=${ROOT_CHILD_COUNT}, child_tasks=${ROOT_CHILD}"

# 检查 c: child_tasks 数组
if [[ "$ROOT_CHILD_COUNT" == "0" ]]; then
  record_result "1c child_tasks 数组" "FAIL" \
    "child_tasks 为空 (runAgent 未调 SaveTaskMeta 设置 parent_task_id)"
  FINDINGS+=("[严重] child_tasks 为空: orchestrator.runAction (orchestrator.go:256) 调用 SaveTask(taskID+\"_\"+agentID) 创建子任务，但未调用 SaveTaskMeta 设置 parent_task_id=rootTaskID，导致 QueryChildTasks(rootTaskID) 返回空数组。")
else
  record_result "1c child_tasks 数组" "PASS" \
    "child_tasks_count=${ROOT_CHILD_COUNT}"
fi

# 检查 c2: root task agent_ids 是否为空 (resolveSession 先用空 agentIDs 创建 task 的 bug)
if [[ "$ROOT_AGENT_IDS" == "[]" ]]; then
  record_result "1c2 root task agent_ids 非空" "FAIL" \
    "root task agent_ids=[] (resolveSession 先用空 agentIDs 创建 task，main.go:603 SaveTask 主键冲突未更新)"
  FINDINGS+=("[严重] root task agent_ids 为空: resolveSession (persistence.go:80) 先调 persist.SaveTask(taskID, userInput, []string{}) 用空 agentIDs 创建 task，main.go:603 再调 persist.SaveTask(taskID, req.Input, agentIDs) 因 PRIMARY KEY 冲突失败，root task 的 agent_ids 永远为空。")
elif [[ "$ROOT_AGENT_IDS" == '["agent_researcher","agent_writer"]' ]]; then
  record_result "1c2 root task agent_ids 非空" "PASS" \
    "root task agent_ids=${ROOT_AGENT_IDS}"
else
  record_result "1c2 root task agent_ids 非空" "FAIL" \
    "root task agent_ids=${ROOT_AGENT_IDS} (期望 [agent_researcher,agent_writer])"
fi

# 检查 d: steps 数组多 agent_id 分布
if [[ "$ROOT_STEPS_COUNT" -gt 0 ]] 2>/dev/null; then
  AGENT_COUNT_IN_STEPS=$(printf '%s' "$ROOT_STEPS_AGENTS" | node -e "
const d = JSON.parse(require('fs').readFileSync(0,'utf8'));
console.log(d.length);
" 2>/dev/null)
  AGENT_COUNT_IN_STEPS=${AGENT_COUNT_IN_STEPS:-0}
  if [[ "$AGENT_COUNT_IN_STEPS" -ge 2 ]] 2>/dev/null; then
    record_result "1d steps 多 agent_id 分布" "PASS" \
      "steps 中出现 ${AGENT_COUNT_IN_STEPS} 个 agent_id: ${ROOT_STEPS_AGENTS}"
  else
    record_result "1d steps 多 agent_id 分布" "FAIL" \
      "steps 中只有 ${AGENT_COUNT_IN_STEPS} 个 agent_id (${ROOT_STEPS_AGENTS})，期望 2 个 (step ID 碰撞导致另一 agent steps 丢失)"
    FINDINGS+=("[严重] step ID 碰撞: persistence.go SaveStep 用 step_{taskID}_{stepIdx}_{type} 作为主键，多 agent 并行时 stepIdx 从 0 开始且 type 序列相同，导致 INSERT PRIMARY KEY 冲突，部分 agent 的 steps 丢失。")
  fi
else
  record_result "1d steps 多 agent_id 分布" "FAIL" \
    "steps 数组为空 (steps_count=${ROOT_STEPS_COUNT})"
fi

# 检查 e: 子任务 (taskID_agentID) 存在性与 status
echo "  [1.3] 检查子任务存在性与 status..."
CHILD_TID_RESEARCHER="${MA_TASK}_agent_researcher"
CHILD_TID_WRITER="${MA_TASK}_agent_writer"

for child_tid in "$CHILD_TID_RESEARCHER" "$CHILD_TID_WRITER"; do
  child_short=$(printf '%s' "$child_tid" | sed 's/.*_agent_/agent_/')
  child_body=$(get_task_body "$child_tid")
  # 判断子任务是否存在：body 非空且不包含 "not found"/"no rows"
  if [[ -n "$child_body" && "$child_body" != *"not found"* && "$child_body" != *"no rows"* ]]; then
    child_parsed=$(parse_task_detail "$child_body")
    child_status=$(pget "$child_parsed" "task_status")
    child_steps=$(pget "$child_parsed" "steps_count")
    child_steps=${child_steps:-0}
    echo "    子任务 ${child_short}: status=${child_status}, steps=${child_steps}"
    if [[ "$child_status" == "running" ]]; then
      record_result "1e 子任务 ${child_short} status" "FAIL" \
        "子任务 status=running (engine.updateTask 更新 root task 而非子任务)"
      FINDINGS+=("[严重] 子任务 ${child_short} status 永远 running: engine.updateTask 用 e.taskID=rootTaskID 更新 root task，不更新子任务 (taskID_agentID)。子任务 steps 也挂在 rootTaskID 下，子任务查询 steps=${child_steps} 为空。")
    else
      record_result "1e 子任务 ${child_short} status" "PASS" \
        "子任务 status=${child_status}"
    fi
  else
    echo "    子任务 ${child_short}: 未持久化 (body: $(printf '%s' "$child_body" | head -c 80))"
    record_result "1e 子任务 ${child_short} 持久化" "FAIL" \
      "子任务记录未写入 DB (SaveTask 主键冲突失败，因 resolveSession 先用 rootTaskID 创建了 task)"
    FINDINGS+=("[严重] 子任务 ${child_short} 未持久化: orchestrator.runAgent 调 SaveTask(taskID+\"_\"+agentID)，但该 ID 不会与已有 task 冲突——实际查 DB 返回 no rows，说明 SaveTask 因 SQLITE_BUSY 或其他错误失败；或子任务 ID 格式 taskID_agentID 与查询不匹配。需查 server log 确认。")
  fi
done

# =============================================================================
# 测试 2: code_gen case — 1 agent 拆分 (coder)
# =============================================================================
print_section "测试 2: code_gen case (coder)"

echo "  [2.1] POST /api/multi-agent ..."
MA2_RESP=$(curl -s -X POST "${BASE}/api/multi-agent" \
  -H 'Content-Type: application/json' \
  --data '{"input":"generate a hello world program","case_type":"code_gen"}' 2>/dev/null | tr -d '\r')
echo "    响应: ${MA2_RESP}"

MA2_PARSED=$(parse_ma_response "$MA2_RESP")
MA2_TASK=$(pget "$MA2_PARSED" "task_id")
MA2_COUNT=$(pget "$MA2_PARSED" "agent_count")
MA2_IDS=$(pget "$MA2_PARSED" "agent_ids")
echo "    task_id=${MA2_TASK}, agent_count=${MA2_COUNT}, agent_ids=${MA2_IDS}"

if [[ "$MA2_COUNT" == "1" && "$MA2_IDS" == '["agent_coder"]' ]]; then
  record_result "2a code_gen 拆分" "PASS" \
    "agent_count=1, agent_ids=[agent_coder]"
else
  record_result "2a code_gen 拆分" "FAIL" \
    "agent_count=${MA2_COUNT}, agent_ids=${MA2_IDS} (期望 1 个 agent_coder)"
fi

echo "  [2.2] 轮询 root task 直到完成..."
MA2_ROOT=$(poll_root_task "$MA2_TASK")
echo "    root task 最终 status=${MA2_ROOT}"
if [[ "$MA2_ROOT" == "completed" || "$MA2_ROOT" == "failed" ]]; then
  record_result "2b code_gen root task status" "PASS" "status=${MA2_ROOT}"
else
  record_result "2b code_gen root task status" "FAIL" "status=${MA2_ROOT}"
fi

# =============================================================================
# 测试 3: default case — 1 agent 拆分 (agent_default)
# =============================================================================
print_section "测试 3: default case (agent_default)"

echo "  [3.1] POST /api/multi-agent (无 case_type) ..."
sleep 1  # 避免与测试 2 的 task_id 秒级碰撞
MA3_RESP=$(curl -s -X POST "${BASE}/api/multi-agent" \
  -H 'Content-Type: application/json' \
  --data '{"input":"hello world dialogue test"}' 2>/dev/null | tr -d '\r')
echo "    响应: ${MA3_RESP}"

MA3_PARSED=$(parse_ma_response "$MA3_RESP")
MA3_TASK=$(pget "$MA3_PARSED" "task_id")
MA3_COUNT=$(pget "$MA3_PARSED" "agent_count")
MA3_IDS=$(pget "$MA2_PARSED" "agent_ids")
MA3_IDS=$(pget "$MA3_PARSED" "agent_ids")
echo "    task_id=${MA3_TASK}, agent_count=${MA3_COUNT}, agent_ids=${MA3_IDS}"

if [[ "$MA3_COUNT" == "1" && "$MA3_IDS" == '["agent_default"]' ]]; then
  record_result "3a default 拆分" "PASS" \
    "agent_count=1, agent_ids=[agent_default]"
else
  record_result "3a default 拆分" "FAIL" \
    "agent_count=${MA3_COUNT}, agent_ids=${MA3_IDS} (期望 1 个 agent_default)"
fi

echo "  [3.2] 轮询 root task 直到完成..."
MA3_ROOT=$(poll_root_task "$MA3_TASK")
echo "    root task 最终 status=${MA3_ROOT}"
if [[ "$MA3_ROOT" == "completed" || "$MA3_ROOT" == "failed" ]]; then
  record_result "3b default root task status" "PASS" "status=${MA3_ROOT}"
else
  record_result "3b default root task status" "FAIL" "status=${MA3_ROOT}"
fi

# =============================================================================
# 汇总
# =============================================================================
print_section "汇总"
echo "----------------------------------------"
echo "  PASS : ${PASS}"
echo "  FAIL : ${FAIL}"
echo "  SKIP : ${SKIP}"
echo "----------------------------------------"
echo ""
echo "各项详细结果："
for r in "${RESULTS[@]}"; do
  echo "  ${r}"
done

# =============================================================================
# 后端 Orchestrator Bug / 缺口清单
# =============================================================================
print_section "后端 Orchestrator Bug / 缺口清单"
if [[ ${#FINDINGS[@]} -gt 0 ]]; then
  echo "  [运行时发现]"
  for f in "${FINDINGS[@]}"; do
    echo "  ${f}"
  done
fi

echo ""
echo "  [静态分析] 代码审查发现的 orchestrator 设计缺口（未改源码，仅记录）："
echo "  1. [严重] child_tasks 为空: orchestrator.runAgent (orchestrator.go:256) 调用"
echo "     SaveTask(taskID+\"_\"+agentID) 创建子任务，但未调用 SaveTaskMeta 设置"
echo "     parent_task_id=rootTaskID。QueryChildTasks(rootTaskID) 查 WHERE"
echo "     parent_task_id=rootTaskID 返回空。前端无法通过 child_tasks 看到子任务。"
echo "  2. [严重] 子任务记录因 SQLITE_BUSY 丢失: 多 agent 并行 goroutine 共享单一"
echo "     *sql.DB，modernc.org/sqlite 默认串行写入，并发 INSERT 触发 database is locked。"
echo "     runAgent 调 SaveTask 时 log 显示 'Failed to save step: database is locked'，"
echo "     子任务记录有时也因 SQLITE_BUSY 未写入。db.Init 未设置 PRAGMA busy_timeout"
echo "     和 WAL 模式，并发写入不安全。"
echo "  3. [严重] step ID 碰撞: persistence.go SaveStep 用 step_{taskID}_{stepIdx}_{type}"
echo "     作为主键。多 agent 并行时所有 agent 的 stepIdx 都从 0 开始且 type 序列相同，"
echo "     导致 INSERT PRIMARY KEY 冲突 (log: UNIQUE constraint failed: steps.id)。"
echo "     单 agent 内 tool observation 与 final observation 也碰撞 (同 stepIdx + type=observation)。"
echo "  4. [严重] root task agent_ids 为空: resolveSession (persistence.go:80) 先调"
echo "     persist.SaveTask(taskID, userInput, []string{}) 用空 agentIDs 创建 root task，"
echo "     main.go:603 再调 persist.SaveTask(taskID, req.Input, agentIDs) 因主键已存在"
echo "     (InsertTask 用 INSERT INTO，无 ON CONFLICT) 失败，root task 的 agent_ids 永远为 []。"
echo "  5. [设计问题] 子任务 status 永远 running: engine.updateTask 用 e.taskID=rootTaskID"
echo "     更新 root task 状态，不更新子任务 (taskID_agentID)。即使子任务记录写入成功，"
echo "     其 status 也永远停留在 running，子任务 steps 查询也为空（steps 挂在 rootTaskID 下）。"
echo "  6. [设计问题] steps 挂在 root taskID 下: orchestrator.runAgent 传 rootTaskID"
echo "     给 NewEngine，所有 agent 的 saveStep 用 TaskID=rootTaskID。子任务"
echo "     (taskID_agentID) 查询 steps 返回空，无法独立回放。"
echo "  7. [设计问题] strategy 字段无效: TaskDecomposer 返回 Strategy=\"pipeline\"/"
echo "     \"parallel\"，但 RunBlocking 无视 Strategy，总是并行启动所有 agent。"
echo "     pipeline 模式下 agent_writer 应等 agent_researcher 完成后再执行，实际并行。"
echo "  8. [设计问题] AgentBus 未实际使用: engine.sendAgentMessage 方法定义但无调用方，"
echo "     多 agent 间无消息传递。researcher 的研究成果不会传给 writer，writer 独立执行。"
echo "  9. [竞态] root task 状态由最后一个完成的 agent 决定: 多 agent 并行时，第一个"
echo "     agent 完成后 updateTask(\"completed\")，但其他 agent 仍在跑。轮询可能读到"
echo "     中间状态。最终状态由最后一个 agent 覆盖。"
echo " 10. [可观测性] 多 agent 事件通过 agent_id 区分，但 task_started 事件的"
echo "     agent_id=\"orchestrator\"，与子 agent 事件混合，前端需要特殊处理。"
echo " 11. [API 缺口] POST /api/multi-agent 不返回 session_id 与 agent_ids 的关联"
echo "     校验（如 agent_ids 是否都已在 DB agents 表注册），前端只能信任返回值。"

echo ""
if [[ $FAIL -gt 0 ]]; then
  echo "[multi-agent-smoke] 存在 FAIL 项，详见上方结果。服务日志：${SERVER_LOG}"
  exit 1
fi
echo "[multi-agent-smoke] 评测完成 (PASS=${PASS}, SKIP=${SKIP}, FAIL=${FAIL})"
exit 0
