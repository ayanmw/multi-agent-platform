#!/usr/bin/env bash
# =============================================================================
# 维度 E — 6 预设 Case mock 回归评测
# =============================================================================
# 对 6 个内置 mock case (code-gen, dialogue, research, multi-agent, long-task,
# tool-error) 逐个串行执行，验证：
#   a. 最终 task.status (completed / failed / timeout)
#   b. steps 数量、是否含 tool_call 步骤、tool_name
#   c. final_result 是否非空
#   d. total_tokens > 0 (验证 usage 写入)
#   e. cost_records >= 1 (GET /api/costs?task_id=<tid>)
#
# 约束：独立端口 18105 + 独立 DB；LLM_USE_MOCK=true 无需真实 API Key。
#       不改后端源码，只创建/修改本脚本。
# =============================================================================
set -u

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

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null
    wait "${SERVER_PID}" 2>/dev/null
  fi
  rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" 2>/dev/null
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
#   jstr <json> <code>  — code 中 d 为已解析 JSON dict
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

# ---- 单个 Case 评测函数 ------------------------------------------------------
# run_case <case_id> <input> <expect_status> <expect_tool> <expect_final> <desc>
#   expect_status: completed | failed
#   expect_tool:   yes | no
#   expect_final:  nonempty | empty
run_case() {
  local case_id="$1" input="$2" exp_status="$3" exp_tool="$4" exp_final="$5" desc="$6"

  echo ""
  echo "===== Case: ${case_id} (${desc}) ====="

  # POST 创建任务
  local post_body post_code tid
  post_body=$(curl -s -o /tmp/cases-post-$$ -w '%{http_code}' \
    -X POST "${BASE}/api/tasks?case=${case_id}" \
    -H 'Content-Type: application/json' \
    --data "{\"action\":\"chat\",\"input\":\"${input}\",\"agent_id\":\"agent_cases\",\"max_steps\":8}" \
    2>/dev/null)
  post_code=$(cat /tmp/cases-post-$$ 2>/dev/null)
  rm -f /tmp/cases-post-$$

  if [[ "${post_body}" != "200" ]]; then
    echo "  [FAIL] POST 返回 HTTP ${post_body}, body=${post_code:0:120}"
    FAIL=$((FAIL+1))
    RESULTS+=("${case_id}|FAIL|http_${post_body}|0|no|0|0|")
    PROBLEMS+=("[${case_id}] POST HTTP ${post_body}")
    return
  fi

  tid=$(printf '%s' "$post_code" | python -c "import sys,json; print(json.load(sys.stdin).get('task_id',''))" 2>/dev/null)
  if [[ -z "${tid}" ]]; then
    echo "  [FAIL] 未获取到 task_id, body=${post_code:0:120}"
    FAIL=$((FAIL+1))
    RESULTS+=("${case_id}|FAIL|no_task_id|0|no|0|0|")
    PROBLEMS+=("[${case_id}] 无 task_id")
    return
  fi
  echo "  task_id=${tid}"

  # 轮询等待任务完成 (最多 30s)
  local status="" resp="" elapsed=0
  for i in $(seq 1 60); do
    resp=$(curl -s "${BASE}/api/tasks?id=${tid}" 2>/dev/null)
    status=$(printf '%s' "$resp" | python -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null)
    if [[ "${status}" == "completed" || "${status}" == "failed" ]]; then
      break
    fi
    sleep 0.5
    elapsed=$((i))
  done

  if [[ "${status}" != "completed" && "${status}" != "failed" ]]; then
    echo "  [FAIL] 轮询超时 (30s), 最后 status=${status}"
    FAIL=$((FAIL+1))
    RESULTS+=("${case_id}|FAIL|timeout|0|no|0|0|")
    PROBLEMS+=("[${case_id}] 轮询超时")
    return
  fi
  echo "  轮询耗时 ~$((elapsed * 5 / 10))s"

  # 提取指标
  local final_result total_tokens step_count has_tool tool_names
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

  # 查询 cost records
  local cost_resp cost_count
  cost_resp=$(curl -s "${BASE}/api/costs?task_id=${tid}" 2>/dev/null)
  cost_count=$(printf '%s' "$cost_resp" | python -c "import sys,json; print(json.load(sys.stdin).get('record_count',0) or 0)" 2>/dev/null)

  # 安全默认值
  total_tokens=${total_tokens:-0}
  step_count=${step_count:-0}
  cost_count=${cost_count:-0}

  # 打印详情
  echo "  status=${status} (expect ${exp_status})"
  echo "  steps=${step_count}, has_tool_call=${has_tool} (expect ${exp_tool})"
  echo "  tool_names=${tool_names}"
  echo "  final_result=${final_result:0:80}"
  echo "  total_tokens=${total_tokens}"
  echo "  cost_records=${cost_count}"

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
    # 去除空白后检查是否非空
    local stripped
    stripped=$(echo "${final_result}" | tr -d '[:space:]')
    if [[ -z "${stripped}" ]]; then
      verdict="FAIL"
      reasons="${reasons} final_result_empty"
    fi
  fi
  if [[ "${exp_final}" == "empty" && "${status}" == "completed" ]]; then
    # 期望空结果但任务却成功了
    local stripped
    stripped=$(echo "${final_result}" | tr -d '[:space:]')
    if [[ -n "${stripped}" ]]; then
      : # 完成时非空结果是可以的
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

  if [[ "${verdict}" == "PASS" ]]; then
    PASS=$((PASS+1))
    echo "  [PASS] 符合预期"
  else
    FAIL=$((FAIL+1))
    echo "  [FAIL]${reasons}"
    PROBLEMS+=("[${case_id}]${reasons}")
  fi

  RESULTS+=("${case_id}|${verdict}|${status}|${step_count}|${has_tool}|${total_tokens}|${cost_count}|${final_result:0:60}")

  sleep 1  # 避免秒级 task_id 冲突 (task_id 格式 task_YYYYMMDDHHMMSS)
}

# ---- 执行 6 个 Case (串行) ---------------------------------------------------
echo ""
echo "========== 6 Case Mock 回归评测 =========="

run_case "code-gen" \
  "Please generate a simple Go program with a main function" \
  "completed" "yes" "nonempty" \
  "代码生成 + write_file"

run_case "dialogue" \
  "Hello! Let us have a chat about AI" \
  "completed" "no" "nonempty" \
  "交互式对话"

run_case "research" \
  "Please research the topic and find relevant information" \
  "completed" "yes" "nonempty" \
  "研究任务 + run_shell"

run_case "multi-agent" \
  "Delegate this task to multiple agents in a team" \
  "completed" "no" "nonempty" \
  "多 Agent 协作模拟"

run_case "long-task" \
  "Execute a long task with multiple steps" \
  "completed" "yes" "nonempty" \
  "长任务多步循环"

run_case "tool-error" \
  "This should trigger an error scenario" \
  "failed" "yes" "empty" \
  "错误处理验证"

# ---- 汇总 --------------------------------------------------------------------
echo ""
echo "========== 汇总 =========="
printf '%-14s %-7s %-11s %-6s %-10s %-10s %-10s\n' "Case" "Result" "Status" "Steps" "ToolCall" "Tokens" "CostRecs"
printf '%-14s %-7s %-11s %-6s %-10s %-10s %-10s\n' "------------" "-------" "-----------" "------" "----------" "----------" "----------"
for r in "${RESULTS[@]}"; do
  IFS='|' read -r cid verdict status steps has_tool tokens costs _ <<< "$r"
  printf '%-14s %-7s %-11s %-6s %-10s %-10s %-10s\n' \
    "$cid" "$verdict" "$status" "$steps" "$has_tool" "$tokens" "$costs"
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
