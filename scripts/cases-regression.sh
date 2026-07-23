#!/usr/bin/env bash
# =============================================================================
# Case 矩阵 mock 回归评测（L1-L5 全量）
# =============================================================================
# 对 cases.All() 返回的全部内置 case 逐个串行执行，验证：
#   a. 最终 task.status (completed / failed / timeout)
#   b. steps 数量、是否含 tool_call 步骤、tool_name
#   c. final_result 是否非空
#   d. total_tokens > 0 (验证 usage 写入)
#   e. cost_records >= 1 (GET /api/costs?task_id=<tid>)
#   f. L4/L5 多 Agent case 的编排事件流：decompose_done / agent_dispatched / agent_completed
#   g. L4/L5 case 的 child_tasks[].steps 回填（Phase 7-H2 MA5）
#
# 约束：独立端口 18105 + 独立 DB；LLM_USE_MOCK=true 无需真实 API Key。
#       不改后端源码，只创建/修改本脚本。
# =============================================================================
set -u

# Windows 下 python 默认 stdin 编码为 GBK（cp936），而 /api/tasks 响应可能含
# UTF-8 中文（如 skill/list 返回的 Skill DisplayName）。GBK 解码 UTF-8 字节会
# 失败导致 json.load 抛异常、轮询 status 恒空 → 超时。强制 UTF-8 模式修复。
export PYTHONUTF8=1

# ---- 配置 --------------------------------------------------------------------
PORT=18105
BASE="http://localhost:${PORT}"
RAND=$$
DB_PATH="/tmp/cases-${RAND}.db"
SERVER_BIN="/tmp/cases-server-${RAND}.exe"
SERVER_LOG="/tmp/cases-server-${RAND}.log"
SERVER_PID=""
PASS=0
FAIL=0
PROBLEMS=()
RESULTS=()
WS_EVENTS="/tmp/cases-ws-events-${RAND}.ndjson"

# 每个 case 的期望终态、expect_tool、expect_final。
# 大部分 case 在 mock 下应 completed；明确测试失败路径的 case 期望 failed。
declare -A EXP_STATUS
declare -A EXP_TOOL
declare -A EXP_FINAL
EXP_STATUS=(
  ["code-gen"]="completed"
  ["dialogue"]="completed"
  ["research"]="completed"
  ["long-task"]="completed"
  ["todo-driven"]="completed"
  ["web-research"]="completed"
  ["skill-code-helper"]="completed"
  ["cron-notify"]="completed"
  ["llm-judge-qa"]="completed"
  ["policy-enforcement"]="failed"
  ["approval-flow"]="completed"
  ["max-steps-exhaustion"]="failed"
  ["context-compression"]="completed"
  ["checkpoint-resume"]="completed"
  ["multi-agent"]="completed"
  ["multi-agent-parallel"]="completed"
  ["multi-agent-sequential"]="completed"
  ["multi-agent-dag"]="completed"
  ["multi-agent-leader-dispatch"]="completed"
  ["multi-agent-review"]="completed"
  ["multi-agent-fault-tolerance"]="completed"
)
EXP_TOOL=(
  ["code-gen"]="yes"
  ["dialogue"]="no"
  ["research"]="yes"
  ["long-task"]="no"
  ["todo-driven"]="yes"
  ["web-research"]="yes"
  ["skill-code-helper"]="yes"
  ["cron-notify"]="yes"
  ["llm-judge-qa"]="no"
  ["policy-enforcement"]="yes"
  ["approval-flow"]="yes"
  ["max-steps-exhaustion"]="yes"
  ["context-compression"]="no"
  ["checkpoint-resume"]="yes"
  ["multi-agent"]="yes"
  ["multi-agent-parallel"]="yes"
  ["multi-agent-sequential"]="yes"
  ["multi-agent-dag"]="yes"
  ["multi-agent-leader-dispatch"]="yes"
  ["multi-agent-review"]="yes"
  ["multi-agent-fault-tolerance"]="yes"
)
EXP_FINAL=(
  ["code-gen"]="nonempty"
  ["dialogue"]="nonempty"
  ["research"]="nonempty"
  ["long-task"]="nonempty"
  ["todo-driven"]="nonempty"
  ["web-research"]="nonempty"
  ["skill-code-helper"]="nonempty"
  ["cron-notify"]="nonempty"
  ["llm-judge-qa"]="nonempty"
  ["policy-enforcement"]="empty"
  ["approval-flow"]="nonempty"
  ["max-steps-exhaustion"]="empty"
  ["context-compression"]="nonempty"
  ["checkpoint-resume"]="nonempty"
  ["multi-agent"]="nonempty"
  ["multi-agent-parallel"]="nonempty"
  ["multi-agent-sequential"]="nonempty"
  ["multi-agent-dag"]="nonempty"
  ["multi-agent-leader-dispatch"]="nonempty"
  ["multi-agent-review"]="nonempty"
  ["multi-agent-fault-tolerance"]="nonempty"
)

# 阶梯分组（按 case ID 前缀/已知 ID 归类）
L1_CASES=(code-gen dialogue research long-task)
L2_CASES=(todo-driven web-research skill-code-helper cron-notify llm-judge-qa)
L3_CASES=(policy-enforcement approval-flow max-steps-exhaustion context-compression checkpoint-resume)
L4_CASES=(multi-agent multi-agent-parallel multi-agent-sequential multi-agent-dag)
L5_CASES=(multi-agent-leader-dispatch multi-agent-review multi-agent-fault-tolerance)

MULTI_AGENT_CASES=(multi-agent multi-agent-parallel multi-agent-sequential multi-agent-dag multi-agent-leader-dispatch multi-agent-review multi-agent-fault-tolerance)

cleanup() {
  if [[ -n "${WS_PID:-}" ]] && kill -0 "${WS_PID}" 2>/dev/null; then
    kill "${WS_PID}" 2>/dev/null || true
  fi
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null
    wait "${SERVER_PID}" 2>/dev/null
  fi
  rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" "${WS_EVENTS}" "${WS_EVENTS}.err" 2>/dev/null
  # 清理 mock 可能创建的文件
  rm -f /tmp/mock_gen.go 2>/dev/null
  rm -rf /tmp/tmp 2>/dev/null
}
trap cleanup EXIT

# ---- Python 可用性检查 -------------------------------------------------------
if ! command -v python >/dev/null 2>&1; then
  echo "[FATAL] 需要 python（用于 JSON 解析）"; exit 1
fi

# ---- JSON 辅助函数 -----------------------------------------------------------
# 从 JSON 字符串中提取字段。使用 python 保证健壮性。
jstr() {
  local json="$1"; shift
  printf '%s' "$json" | python -c "
import sys, json
try:
    d = json.load(sys.stdin)
except:
    d = {}
try:
    $1
except:
    print('')
" 2>/dev/null
}

# jrun: 执行任意 python 表达式，d 为解析后的 JSON
jrun() {
  local json="$1"; shift
  printf '%s' "$json" | python -c "
import sys, json
try:
    d = json.load(sys.stdin)
except:
    d = {}
try:
    $1
except Exception as e:
    print('')
" 2>/dev/null
}

# ---- 编译后端 ----------------------------------------------------------------
echo "[setup] 编译后端服务..."
if ! go build -o "${SERVER_BIN}" ./cmd/server 2>"${SERVER_LOG}"; then
  echo "[FATAL] 编译失败，日志见 ${SERVER_LOG}"
  cat "${SERVER_LOG}"
  exit 2
fi
echo "[setup] 编译成功 → ${SERVER_BIN}"

# ---- 启动服务 ----------------------------------------------------------------
echo "[setup] 启动服务 (port=${PORT}, DB=${DB_PATH}, LLM_USE_MOCK=true)..."
LLM_USE_MOCK=true \
REQUIRE_AUTH=false \
SERVER_PORT="${PORT}" \
DB_PATH="${DB_PATH}" \
  "${SERVER_BIN}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!

# ---- 等待健康检查 ------------------------------------------------------------
echo "[setup] 等待 /healthz 就绪..."
ready=0
for i in $(seq 1 60); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/healthz" 2>/dev/null || true)
  if [[ "${code}" == "200" ]]; then ready=1; break; fi
  sleep 0.5
done
if [[ "${ready}" != "1" ]]; then
  echo "[FATAL] 服务 30s 内未就绪。服务日志："
  tail -30 "${SERVER_LOG}"
  exit 3
fi
echo "[setup] 服务就绪 ✓"

# ---- WS 事件订阅（用于 L4/L5 编排事件断言）------------------------------------
# 必须在服务就绪后启动：orchestrator 的 decompose_done / agent_dispatched /
# agent_completed 事件只经 hub.SendEvent 做 WS 广播，不写 task steps，因此
# HTTP fallback 拿不到，只能靠 WS 订阅捕获。脚本带重连，避免单次握手失败。
: > "${WS_EVENTS}"
node -e "
  const out = process.argv[1], base = process.argv[2], fs = require('fs');
  let fd, ws, connectedOnce = false;
  function connect() {
    ws = new WebSocket(base + '/ws?session_id=cases-regression');
    ws.addEventListener('open',  () => {
      if (fd === undefined) fd = fs.openSync(out, 'a');
      fs.writeSync(fd, JSON.stringify({type:'__ws_open__'}) + '\n');
      connectedOnce = true;
    });
    ws.addEventListener('message', m => {
      try { const e = JSON.parse(m.data); if (fd === undefined) fd = fs.openSync(out, 'a'); fs.writeSync(fd, JSON.stringify(e) + '\n'); } catch (x) {}
    });
    ws.addEventListener('error', e => {
      fs.writeFileSync(out + '.err', String(e.message || e) + '\n', { flag: 'a' });
    });
    ws.addEventListener('close', () => {
      // 服务可能还在重启或握手失败；带退避重连，直到 cleanup kill 本进程。
      setTimeout(connect, 300);
    });
  }
  connect();
  setInterval(() => {}, 1000);
" "${WS_EVENTS}" "${BASE}" >/dev/null 2>&1 &
WS_PID=$!

# 等 WS 连上（最多 5s）
ws_ready=0
for i in $(seq 1 10); do
  if grep -q "__ws_open__" "${WS_EVENTS}" 2>/dev/null; then ws_ready=1; break; fi
  sleep 0.5
done
if [[ "${ws_ready}" == "1" ]]; then
  echo "[setup] WS 订阅已连接 ✓"
else
  echo "[setup] WS 订阅未连上（后续编排事件断言可能依赖 HTTP fallback）"
fi

# ---- 从 /api/cases 拉取全部 case ID ------------------------------------------
# 这样脚本无需硬编码新增 case，自动跟随 cases.All() 扩展。
echo "[setup] 拉取内置 case 列表..."
CASES_JSON=$(curl -s "${BASE}/api/cases" 2>/dev/null)
mapfile -t ALL_CASES < <(printf '%s' "$CASES_JSON" | python -c "
import sys, json
try:
    cases = json.load(sys.stdin)
    for c in cases:
        print(c.get('id',''))
except Exception as e:
    print('')
" 2>/dev/null | grep -v '^$')

if [[ ${#ALL_CASES[@]} -eq 0 ]]; then
  echo "[FATAL] 未从 /api/cases 获取到 case 列表"
  exit 4
fi
echo "[setup] 发现 ${#ALL_CASES[@]} 个内置 case"

# ---- 单个 Case 评测函数 ------------------------------------------------------
# run_case <case_id>
run_case() {
  local case_id="$1"
  local exp_status="${EXP_STATUS[$case_id]:-completed}"
  local exp_tool="${EXP_TOOL[$case_id]:-no}"
  local exp_final="${EXP_FINAL[$case_id]:-nonempty}"

  echo ""
  echo "===== Case: ${case_id} ====="

  # POST /api/run-case（复用 case 默认 input / system prompt / contract）
  local post_body post_code
  post_body=$(curl -s -o /tmp/cases-post-$$ -w '%{http_code}' \
    -X POST "${BASE}/api/run-case" \
    -H 'Content-Type: application/json' \
    --data "{\"case\":\"${case_id}\",\"agent_id\":\"agent_cases\"}" \
    2>/dev/null)
  post_code=$(cat /tmp/cases-post-$$ 2>/dev/null)
  rm -f /tmp/cases-post-$$

  if [[ "${post_body}" != "200" ]]; then
    echo "  [FAIL] POST 返回 HTTP ${post_body}, body=${post_code:0:120}"
    FAIL=$((FAIL+1))
    RESULTS+=("${case_id}|FAIL|http_${post_body}|0|no|0|0||||")
    PROBLEMS+=("[${case_id}] POST HTTP ${post_body}")
    return
  fi

  tid=$(printf '%s' "$post_code" | python -c "import sys,json; print(json.load(sys.stdin).get('task_id',''))" 2>/dev/null)
  if [[ -z "${tid}" ]]; then
    echo "  [FAIL] 未获取到 task_id, body=${post_code:0:120}"
    FAIL=$((FAIL+1))
    RESULTS+=("${case_id}|FAIL|no_task_id|0|no|0|0||||")
    PROBLEMS+=("[${case_id}] 无 task_id")
    return
  fi
  echo "  task_id=${tid}"

  # 轮询等待任务完成 (最多 60s)
  local status="" resp="" elapsed=0
  for i in $(seq 1 120); do
    resp=$(curl -s "${BASE}/api/tasks?id=${tid}" 2>/dev/null)
    status=$(printf '%s' "$resp" | python -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null)
    if [[ "${status}" == "completed" || "${status}" == "failed" ]]; then
      break
    fi
    sleep 0.5
    elapsed=$((i))
  done

  if [[ "${status}" != "completed" && "${status}" != "failed" ]]; then
    echo "  [FAIL] 轮询超时 (60s), 最后 status=${status}"
    FAIL=$((FAIL+1))
    RESULTS+=("${case_id}|FAIL|timeout|0|no|0|0||||")
    PROBLEMS+=("[${case_id}] 轮询超时")
    return
  fi
  echo "  轮询耗时 ~$((elapsed / 2))s"

  # 提取指标
  local final_result total_tokens step_count has_tool tool_names child_count
  final_result=$(printf '%s' "$resp" | python -c "
import sys,json
d=json.load(sys.stdin)
r=d.get('task',{}).get('final_result','') or ''
print(r[:120].replace(chr(10),' ').replace(chr(13),' '))
" 2>/dev/null)
  total_tokens=$(printf '%s' "$resp" | python -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('total_tokens',0) or 0)" 2>/dev/null)
  step_count=$(printf '%s' "$resp" | python -c "import sys,json; print(len(json.load(sys.stdin).get('steps',[])))" 2>/dev/null)
  has_tool=$(printf '%s' "$resp" | python -c "
import sys,json
d=json.load(sys.stdin)
print('yes' if any(s.get('type')=='tool_call' for s in d.get('steps',[])) else 'no')
" 2>/dev/null)
  tool_names=$(printf '%s' "$resp" | python -c "
import sys,json
d=json.load(sys.stdin)
tools=[s.get('tool_name','') for s in d.get('steps',[]) if s.get('type')=='tool_call']
print(','.join(sorted(set(tools))) if tools else 'none')
" 2>/dev/null)
  child_count=$(printf '%s' "$resp" | python -c "import sys,json; print(len(json.load(sys.stdin).get('child_tasks',[])))" 2>/dev/null)

  # 查询 cost records
  local cost_resp cost_count
  cost_resp=$(curl -s "${BASE}/api/costs?task_id=${tid}" 2>/dev/null)
  cost_count=$(printf '%s' "$cost_resp" | python -c "import sys,json; print(json.load(sys.stdin).get('record_count',0) or 0)" 2>/dev/null)

  # 安全默认值
  total_tokens=${total_tokens:-0}
  step_count=${step_count:-0}
  cost_count=${cost_count:-0}
  child_count=${child_count:-0}

  # 多 Agent 编排事件断言（仅 L4/L5）
  local orch_events=""
  local decompose_count=0 dispatched_count=0 completed_count=0
  local child_steps_ok="na"
  if is_multi_agent "${case_id}"; then
    # 优先从 WS 事件文件统计
    if [[ -f "${WS_EVENTS}" ]]; then
      decompose_count=$(grep -c '"type":"decompose_done"' "${WS_EVENTS}" 2>/dev/null) || decompose_count=0
      dispatched_count=$(grep -c '"type":"agent_dispatched"' "${WS_EVENTS}" 2>/dev/null) || dispatched_count=0
      completed_count=$(grep -c '"type":"agent_completed"' "${WS_EVENTS}" 2>/dev/null) || completed_count=0
    fi
    # 兜底：从任务 steps 里找 orchestrator 事件（老版本可能不发 WS 但会写 steps）
    if [[ "${decompose_count}" -lt 1 ]]; then
      decompose_count=$(printf '%s' "$resp" | python -c "
import sys,json
d=json.load(sys.stdin)
print(len([s for s in d.get('steps',[]) if s.get('type')=='orchestrator' and s.get('tool_name')=='decompose_done']))
" 2>/dev/null)
    fi
    if [[ "${dispatched_count}" -lt 1 ]]; then
      dispatched_count=$(printf '%s' "$resp" | python -c "
import sys,json
d=json.load(sys.stdin)
print(len([s for s in d.get('steps',[]) if s.get('type')=='orchestrator' and s.get('tool_name')=='agent_dispatched']))
" 2>/dev/null)
    fi
    if [[ "${completed_count}" -lt 1 ]]; then
      completed_count=$(printf '%s' "$resp" | python -c "
import sys,json
d=json.load(sys.stdin)
print(len([s for s in d.get('steps',[]) if s.get('type')=='orchestrator' and s.get('tool_name')=='agent_completed']))
" 2>/dev/null)
    fi

    # child_tasks[].steps 回填断言
    child_steps_ok=$(printf '%s' "$resp" | python -c "
import sys,json
d=json.load(sys.stdin)
cts=d.get('child_tasks',[])
if not cts:
    print('no_child')
else:
    ok=all(len(c.get('steps',[]))>0 for c in cts)
    print('yes' if ok else 'no')
" 2>/dev/null)

    orch_events="decompose=${decompose_count} dispatched=${dispatched_count} completed=${completed_count} child_steps=${child_steps_ok}"
  fi

  # 打印详情
  echo "  status=${status} (expect ${exp_status})"
  echo "  steps=${step_count}, has_tool_call=${has_tool} (expect ${exp_tool})"
  echo "  tool_names=${tool_names}"
  echo "  final_result=${final_result:0:80}"
  echo "  total_tokens=${total_tokens}"
  echo "  cost_records=${cost_count}"
  if [[ -n "${orch_events}" ]]; then
    echo "  orchestrator: ${orch_events}"
  fi

  # ---- 判定 ----
  local verdict="PASS" reasons=""

  if [[ "${status}" != "${exp_status}" ]]; then
    verdict="FAIL"
    reasons="${reasons} status(${status}!=${exp_status})"
  fi
  if [[ "${has_tool}" != "${exp_tool}" ]]; then
    verdict="FAIL"
    reasons="${reasons} tool_call(${has_tool}!=${exp_tool})"
  fi
  if [[ "${exp_final}" == "nonempty" ]]; then
    local stripped
    stripped=$(echo "${final_result}" | tr -d '[:space:]')
    if [[ -z "${stripped}" ]]; then
      verdict="FAIL"
      reasons="${reasons} final_result_empty"
    fi
  fi
  if [[ "${total_tokens}" -le 0 ]]; then
    verdict="FAIL"
    reasons="${reasons} tokens<=0"
  fi
  if [[ "${cost_count}" -lt 1 ]]; then
    verdict="FAIL"
    reasons="${reasons} no_cost_records"
  fi
  if is_multi_agent "${case_id}"; then
    if [[ "${decompose_count}" -lt 1 ]]; then
      verdict="FAIL"
      reasons="${reasons} no_decompose_done"
    fi
    if [[ "${dispatched_count}" -lt 1 ]]; then
      verdict="FAIL"
      reasons="${reasons} no_agent_dispatched"
    fi
    if [[ "${completed_count}" -lt 1 ]]; then
      verdict="FAIL"
      reasons="${reasons} no_agent_completed"
    fi
    # 对真多 Agent case（不含 legacy），要求 child_steps_ok=yes
    if [[ "${case_id}" != "multi-agent" && "${child_steps_ok}" != "yes" ]]; then
      verdict="FAIL"
      reasons="${reasons} child_steps(${child_steps_ok})"
    fi
  fi

  if [[ "${verdict}" == "PASS" ]]; then
    PASS=$((PASS+1))
    echo "  [PASS] 符合预期"
  else
    FAIL=$((FAIL+1))
    echo "  [FAIL]${reasons}"
    PROBLEMS+=("[${case_id}]${reasons}")
  fi

  RESULTS+=("${case_id}|${verdict}|${status}|${step_count}|${has_tool}|${total_tokens}|${cost_count}|${decompose_count}|${dispatched_count}|${completed_count}|${child_steps_ok}|${final_result:0:60}")

  sleep 1  # 避免秒级 task_id 冲突
}

# is_multi_agent <case_id> 判断是否为 L4/L5 多 Agent case
is_multi_agent() {
  local cid="$1"
  for m in "${MULTI_AGENT_CASES[@]}"; do
    if [[ "${m}" == "${cid}" ]]; then return 0; fi
  done
  return 1
}

# run_level <label> <case_id>...
run_level() {
  local label="$1"; shift
  local ids=("$@")
  echo ""
  echo "========== ${label} =========="
  for cid in "${ids[@]}"; do
    run_case "${cid}"
  done
}

# ---- 执行 L1-L5 全部 Case ----------------------------------------------------
echo ""
echo "========== Case Mock 回归评测（全 ${#ALL_CASES[@]} 个） =========="

run_level "L1 单 Agent 基线" "${L1_CASES[@]}"
run_level "L2 单 Agent + 子系统" "${L2_CASES[@]}"
run_level "L3 Harness 治理" "${L3_CASES[@]}"
run_level "L4 多 Agent 静态编排" "${L4_CASES[@]}"
run_level "L5 多 Agent 动态编排" "${L5_CASES[@]}"

# ---- 汇总 --------------------------------------------------------------------
echo ""
echo "========== 汇总 =========="
printf '%-28s %-7s %-11s %-6s %-10s %-10s %-10s %-12s %-12s %-12s %-12s\n' \
  "Case" "Result" "Status" "Steps" "ToolCall" "Tokens" "CostRecs" "Decompose" "Dispatched" "Completed" "ChildSteps"
printf '%-28s %-7s %-11s %-6s %-10s %-10s %-10s %-12s %-12s %-12s %-12s\n' \
  "----------------------------" "-------" "-----------" "------" "----------" "----------" "----------" "------------" "------------" "------------" "------------"
for r in "${RESULTS[@]}"; do
  IFS='|' read -r cid verdict status steps has_tool tokens costs decomp disp comp child_steps _ <<< "$r"
  printf '%-28s %-7s %-11s %-6s %-10s %-10s %-10s %-12s %-12s %-12s %-12s\n' \
    "$cid" "$verdict" "$status" "$steps" "$has_tool" "$tokens" "$costs" "${decomp:-na}" "${disp:-na}" "${comp:-na}" "${child_steps:-na}"
done

echo ""
total=$((PASS + FAIL))
if [[ ${total} -gt 0 ]]; then
  pct=$((PASS * 100 / total))
else
  pct=0
fi
echo "通过率: ${PASS}/${total} (${pct}%)"

if [[ ${#PROBLEMS[@]} -gt 0 ]]; then
  echo ""
  echo "---- 问题清单 ----"
  for p in "${PROBLEMS[@]}"; do
    echo "  ${p}"
  done
fi

echo ""
echo "[done] 服务已关闭，临时 DB 已清理"

if [[ ${FAIL} -gt 0 ]]; then
  exit 1
fi
exit 0
