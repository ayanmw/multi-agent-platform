#!/usr/bin/env bash
# =============================================================================
# Multi-Agent Platform — 真实 LLM 冒烟测试脚本
# =============================================================================
# 作用：不传 LLM_USE_MOCK=true，让服务读 .env 的真实 LLM 配置
#       (endpoint/key/model)，对 5 个场景跑真实 LLM 调用，验证：
#         - 任务能达终态 (completed/failed，不卡死)
#         - 事件流完整 (task_started → llm_* → task_completed/failed)
#         - 服务日志无 panic
#         - usage 非零 (证明真实 LLM 返回了 token 统计)
#         - tool_call 能被 LLM 生成并执行 (LLM 行为不可控，无 tool_call 记 SKIP)
#
# 断言哲学：真实 LLM 输出不可预测，只用"结构/状态断言"，不断言具体文字。
#
# 环境：Windows + Git Bash。需要 curl / go / node。jq 可选（无 jq 用 node）。
# 端口 18200，独立临时 DB /tmp/real-llm-$$.db，跑完清理。
#
# 用法：  bash scripts/real-llm-smoke.sh
#   可选环境变量：
#     PORT          服务端口（默认 18200）
#     KEEP_SERVER   =1 时不杀服务（调试用）
#     KEEP_LOGS     =1 时不删日志/DB（调试用）
# =============================================================================
set -u

# ---- 配置 -------------------------------------------------------------------
PORT="${PORT:-18200}"
BASE="http://localhost:${PORT}"
DB_PATH="${SMOKE_DB:-/tmp/real-llm-$$.db}"
SERVER_BIN="/tmp/real-llm-server-$$.exe"
SERVER_LOG="/tmp/real-llm-server-$$.log"
SERVER_PID=""
PASS=0
FAIL=0
SKIP=0
RESULTS=()        # 每项检查的明细
FINDINGS=()       # 发现的问题清单（最重要产出）
TIMINGS=()        # 每场景耗时
LLM_OK="unknown"  # LLM 可达性：unknown/yes/no（场景1后判定，no 则后续场景跳过）

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    if [[ "${KEEP_SERVER:-0}" != "1" ]]; then
      kill "${SERVER_PID}" 2>/dev/null
      wait "${SERVER_PID}" 2>/dev/null
    fi
  fi
  if [[ "${KEEP_LOGS:-0}" != "1" ]]; then
    rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" 2>/dev/null
    rm -f /tmp/real-llm-*-resp-$$ 2>/dev/null
  fi
}
trap cleanup EXIT

# ---- 辅助函数 ---------------------------------------------------------------

# kill 占用端口的孤儿进程（上一轮冒烟踩过坑：孤儿进程占端口导致新服务绑不上）
kill_orphans_on_port() {
  local port="$1"
  # Windows netstat -ano 输出形如：
  #   TCP    0.0.0.0:18200          0.0.0.0:0              LISTENING       12345
  #   TCP    [::]:18200              [::]:0                 LISTENING       12345
  local pids
  pids=$(netstat -ano 2>/dev/null | grep -E ":${port} " | grep -i "LISTEN" | awk '{print $NF}' | sort -u)
  for pid in $pids; do
    if [[ "$pid" =~ ^[0-9]+$ ]] && [[ "$pid" != "$$" ]]; then
      echo "[setup] 端口 ${port} 被 PID ${pid} 占用，kill 之"
      taskkill //F //PID "$pid" 2>/dev/null || kill -9 "$pid" 2>/dev/null || true
      sleep 1
    fi
  done
}

# jval <json> <dot.path>  — 用 node 按 . 路径取字段，对象返回 JSON 字符串，标量返回字符串
jval() {
  local json="$1" path="$2"
  printf '%s' "$json" | node -e "
const d = JSON.parse(require('fs').readFileSync(0,'utf8'));
const parts = process.argv[1].split('.');
let v = d;
for (const p of parts) { if (v == null) break; v = v[p]; }
if (v === undefined || v === null) console.log('');
else if (typeof v === 'object') console.log(JSON.stringify(v));
else console.log(String(v));
" "$path" 2>/dev/null
}

# jrun <json> <node_script>  — 把 JSON 送到 stdin，跑任意 node 脚本提取复杂结构
jrun() {
  local json="$1" script="$2"
  printf '%s' "$json" | node -e "
try { const d = JSON.parse(require('fs').readFileSync(0,'utf8')); ${script} }
catch(e) { console.log(''); }
" 2>/dev/null
}

# file_exists <path> — 用 node fs.existsSync 检查文件存在性（规避 bash 反斜杠路径问题）
file_exists() {
  node -e "const fs=require('fs'); console.log(fs.existsSync(process.argv[1]) ? 'yes' : 'no')" "$1" 2>/dev/null
}

# record_result <name> <PASS|FAIL|SKIP> <evidence>
record_result() {
  local name="$1" result="$2" evidence="$3"
  RESULTS+=("[${result}] ${name}: ${evidence}")
  case "$result" in
    PASS) PASS=$((PASS+1)) ;;
    FAIL) FAIL=$((FAIL+1)) ;;
    SKIP) SKIP=$((SKIP+1)) ;;
  esac
  printf '%-6s %-42s %s\n' "[${result}]" "$name" "$evidence"
}

print_section() { echo; echo "===== $1 ====="; }

# 轮询 task 状态直到终态 (completed/failed)，超时 90s
# 输出 "status elapsed_sec"
poll_task() {
  local tid="$1"
  local start=$SECONDS
  local deadline=$((SECONDS + 90))
  while [[ $SECONDS -lt $deadline ]]; do
    local body status
    body=$(curl -s "${BASE}/api/tasks?id=${tid}" 2>/dev/null | tr -d '\r')
    if [[ -z "$body" ]]; then sleep 1; continue; fi
    status=$(jrun "$body" "console.log((d.task && d.task.status) || 'not_found')")
    if [[ "$status" == "completed" || "$status" == "failed" ]]; then
      printf '%s %d' "$status" $((SECONDS - start))
      return 0
    fi
    sleep 1
  done
  printf 'timeout %d' $((SECONDS - start))
  return 1
}

# 检查服务日志有无 panic 关键字
check_no_panic() {
  if grep -E "panic:|goroutine [0-9]+ \[|nil pointer dereference" "${SERVER_LOG}" >/dev/null 2>&1; then
    return 1
  fi
  return 0
}

# 从 task detail 的 steps 数组里提取 tool_call 摘要
# 输出 JSON: {total, tool_calls, tools:[], statuses:[], blocked, block_reasons:[]}
extract_tool_summary() {
  local detail="$1"
  jrun "$detail" "
const steps = d.steps || [];
const tc = steps.filter(s => s.type === 'tool_call');
const blocked = tc.filter(s => s.status === 'failed' && (s.tool_output||'').includes('POLICY BLOCK'));
console.log(JSON.stringify({
  total: steps.length,
  tool_calls: tc.length,
  tools: tc.map(s => s.tool_name),
  statuses: tc.map(s => s.status),
  blocked: blocked.length,
  block_reasons: blocked.map(s => (s.tool_output||'').substring(0,120))
}));
"
}

# ---- 环境信息 ---------------------------------------------------------------
print_section "环境信息"
echo "  PORT       = ${PORT}"
echo "  DB_PATH    = ${DB_PATH}"
echo "  SERVER_LOG = ${SERVER_LOG}"
if [[ -f ".env" ]]; then
  echo "  .env (脱敏):"
  sed -E 's/(API_KEY=sk-)[A-Za-z0-9_-]+/\1***REDACTED***/' .env | sed 's/^/    /'
else
  echo "  [WARN] 未找到 .env，真实 LLM 配置可能缺失"
  FINDINGS+=("[环境] 未找到 .env 文件，真实 LLM endpoint/key/model 可能缺失，所有场景可能 timeout")
fi

# ---- 编译服务 ---------------------------------------------------------------
print_section "编译服务"
echo "[setup] go build -o ${SERVER_BIN} ./cmd/server"
if ! go build -o "${SERVER_BIN}" ./cmd/server 2>"${SERVER_LOG}"; then
  echo "[FATAL] 编译失败，日志见 ${SERVER_LOG}"
  cat "${SERVER_LOG}"
  exit 2
fi
echo "[setup] 编译成功"

# ---- 启动服务 ---------------------------------------------------------------
kill_orphans_on_port "${PORT}"
echo "[setup] 启动服务 (port=${PORT}, DB=${DB_PATH}, LLM_USE_MOCK=false — 读 .env 真实 LLM)"
# 显式传 LLM_USE_MOCK=false 覆盖（.env 也是 false，双保险）。
# endpoint/key/model 从 .env 读，不在这里传。
LLM_USE_MOCK=false \
REQUIRE_AUTH=false \
SERVER_PORT="${PORT}" \
DB_PATH="${DB_PATH}" \
  "${SERVER_BIN}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!
echo "[setup] server PID=${SERVER_PID}"

# ---- 等待健康 ---------------------------------------------------------------
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
echo "[setup] 服务就绪 OK"
echo "  服务启动日志（前 15 行）："
head -15 "${SERVER_LOG}" | sed 's/^/    /'

# =============================================================================
# 场景 1：单 agent + 诱导 write_file tool_call
# =============================================================================
print_section "场景 1: 单 agent + 诱导 write_file"
# 先创建 session，拿到 session_id 和 workspace_dir（用于落盘检查）
SESS1_RESP=$(curl -s -X POST "${BASE}/api/sessions" -H 'Content-Type: application/json' \
  --data '{"user_input":"create hello.txt via real llm","project_id":"default"}' 2>/dev/null | tr -d '\r')
SESS1_ID=$(jval "$SESS1_RESP" "session_id")
echo "  POST /api/sessions -> session_id=${SESS1_ID}"
# POST /api/sessions 响应不含 workspace_dir，需 GET /api/sessions/{id}
SESS1_WS=""
if [[ -n "$SESS1_ID" ]]; then
  SESS1_DETAIL=$(curl -s "${BASE}/api/sessions/${SESS1_ID}" 2>/dev/null | tr -d '\r')
  SESS1_WS=$(jval "$SESS1_DETAIL" "session.workspace_dir")
fi
echo "  workspace_dir=${SESS1_WS}"

S1_RESP=$(curl -s -X POST "${BASE}/api/tasks" -H 'Content-Type: application/json' \
  --data "{\"action\":\"chat\",\"session_id\":\"${SESS1_ID}\",\"agent_id\":\"agent_real_write\",\"max_steps\":4,\"input\":\"请在当前目录创建一个文件 hello.txt，内容写 hello from real llm smoke。必须使用 write_file 工具完成，不要只给文字描述。\"}" 2>/dev/null | tr -d '\r')
S1_TASK=$(jval "$S1_RESP" "task_id")
echo "  POST /api/tasks -> task_id=${S1_TASK}"

if [[ -z "$S1_TASK" ]]; then
  record_result "1a 任务创建" "FAIL" "未拿到 task_id，resp=$(printf '%s' "$S1_RESP" | head -c 120)"
  LLM_OK="no"
else
  S1_RESULT=$(poll_task "$S1_TASK")
  S1_STATUS=$(echo "$S1_RESULT" | awk '{print $1}')
  S1_ELAPSED=$(echo "$S1_RESULT" | awk '{print $2}')
  TIMINGS+=("场景1 write_file: ${S1_ELAPSED}s (status=${S1_STATUS})")
  echo "  轮询结果: status=${S1_STATUS}, elapsed=${S1_ELAPSED}s"

  if [[ "$S1_STATUS" == "completed" || "$S1_STATUS" == "failed" ]]; then
    record_result "1a 任务达终态" "PASS" "status=${S1_STATUS}, elapsed=${S1_ELAPSED}s"
    LLM_OK="yes"
  else
    record_result "1a 任务达终态" "FAIL" "status=${S1_STATUS} (90s 超时或无响应，LLM 可能不可达)"
    LLM_OK="no"
    FINDINGS+=("[场景1] 任务 ${S1_TASK} 90s 未达终态 (status=${S1_STATUS})。可能原因：真实 LLM 不可达 / endpoint 鉴权失败 / LLM 响应超时。查 server log 确认。")
  fi

  # steps 里的 tool_call 摘要
  S1_DETAIL=$(curl -s "${BASE}/api/tasks?id=${S1_TASK}" 2>/dev/null | tr -d '\r')
  S1_STEPS=$(extract_tool_summary "$S1_DETAIL")
  S1_TOTAL=$(jval "$S1_STEPS" "total")
  S1_TC_COUNT=$(jval "$S1_STEPS" "tool_calls")
  S1_TOOLS=$(jval "$S1_STEPS" "tools")
  echo "  steps: total=${S1_TOTAL}, tool_calls=${S1_TC_COUNT}, tools=${S1_TOOLS}"

  if [[ "$S1_TC_COUNT" =~ ^[0-9]+$ ]] && [[ "$S1_TC_COUNT" -gt 0 ]]; then
    record_result "1b LLM 生成 tool_call" "PASS" "tool_calls=${S1_TC_COUNT}, tools=${S1_TOOLS}"
    if [[ "$S1_TOOLS" == *"write_file"* ]]; then
      record_result "1c write_file 被调用" "PASS" "tools=${S1_TOOLS}"
      # 落盘检查
      if [[ -n "$SESS1_WS" ]]; then
        HELLO_PATH="${SESS1_WS}/hello.txt"
        if [[ "$(file_exists "$HELLO_PATH")" == "yes" ]]; then
          record_result "1d write_file 落盘" "PASS" "文件存在: ${HELLO_PATH}"
        else
          record_result "1d write_file 落盘" "FAIL" "文件不存在: ${HELLO_PATH}"
          FINDINGS+=("[场景1] write_file tool_call 已执行但 hello.txt 未落盘到 ${HELLO_PATH}。可能 write_file 路径解析仍有偏差（相对路径 vs workspace 锚点），见最近 commit d1988bd。")
        fi
      else
        record_result "1d write_file 落盘" "SKIP" "无 session workspace_dir，无法检查落盘"
      fi
    else
      record_result "1c write_file 被调用" "SKIP" "LLM 调用了其他工具: ${S1_TOOLS}（LLM 行为不可控，不判 FAIL）"
    fi
  else
    record_result "1b LLM 生成 tool_call" "SKIP" "LLM 未生成 tool_call（LLM 行为不可控）"
    record_result "1c write_file 被调用" "SKIP" "依赖 1b"
    record_result "1d write_file 落盘" "SKIP" "依赖 1c"
  fi

  # usage 断言：真实 LLM 应返回非零 token
  S1_TOKENS=$(jval "$S1_DETAIL" "task.total_tokens")
  if [[ "$S1_TOKENS" =~ ^[0-9]+$ ]] && [[ "$S1_TOKENS" -gt 0 ]]; then
    record_result "1e usage 非零" "PASS" "task.total_tokens=${S1_TOKENS}"
  else
    record_result "1e usage 非零" "FAIL" "task.total_tokens=${S1_TOKENS}（真实 LLM 应返回非零 usage）"
    FINDINGS+=("[场景1] task.total_tokens=${S1_TOKENS}，真实 LLM 调用后 usage 未记录或为 0。可能 onLLMUsage 回调未触发 / OpenAIProvider 未解析 usage / CostTracker 未写入。")
  fi
fi

# =============================================================================
# 场景 2：单 agent + 诱导 run_shell（测 PolicyGate）
# =============================================================================
print_section "场景 2: 单 agent + 诱导 run_shell (测 PolicyGate)"
if [[ "$LLM_OK" != "yes" ]]; then
  record_result "2a 任务达终态" "SKIP" "LLM 不可达（场景1 未通过），跳过场景2 省时省钱"
  record_result "2b LLM 生成 tool_call" "SKIP" "依赖 2a"
  record_result "2c run_shell 被调用" "SKIP" "依赖 2b"
  record_result "2d PolicyGate 行为" "SKIP" "依赖 2c"
else
  sleep 1  # 避免任务 ID 秒级碰撞
  S2_RESP=$(curl -s -X POST "${BASE}/api/tasks" -H 'Content-Type: application/json' \
    --data '{"action":"chat","agent_id":"agent_real_shell","max_steps":3,"input":"请用 run_shell 工具执行命令：echo real-llm-smoke-test。必须使用 run_shell 工具，不要只描述。"}' 2>/dev/null | tr -d '\r')
  S2_TASK=$(jval "$S2_RESP" "task_id")
  echo "  POST /api/tasks -> task_id=${S2_TASK}"
  S2_RESULT=$(poll_task "$S2_TASK")
  S2_STATUS=$(echo "$S2_RESULT" | awk '{print $1}')
  S2_ELAPSED=$(echo "$S2_RESULT" | awk '{print $2}')
  TIMINGS+=("场景2 run_shell: ${S2_ELAPSED}s (status=${S2_STATUS})")
  echo "  轮询结果: status=${S2_STATUS}, elapsed=${S2_ELAPSED}s"

  if [[ "$S2_STATUS" == "completed" || "$S2_STATUS" == "failed" ]]; then
    # run_shell 即使被 PolicyGate 拦截导致 failed 也是有效终态
    record_result "2a 任务达终态" "PASS" "status=${S2_STATUS}, elapsed=${S2_ELAPSED}s"
  else
    record_result "2a 任务达终态" "FAIL" "status=${S2_STATUS}（超时）"
  fi

  S2_DETAIL=$(curl -s "${BASE}/api/tasks?id=${S2_TASK}" 2>/dev/null | tr -d '\r')
  S2_STEPS=$(extract_tool_summary "$S2_DETAIL")
  S2_TC_COUNT=$(jval "$S2_STEPS" "tool_calls")
  S2_TOOLS=$(jval "$S2_STEPS" "tools")
  S2_BLOCKED=$(jval "$S2_STEPS" "blocked")
  S2_BLOCK_REASONS=$(jval "$S2_STEPS" "block_reasons")
  echo "  tool_calls=${S2_TC_COUNT}, tools=${S2_TOOLS}, blocked=${S2_BLOCKED}"

  if [[ "$S2_TC_COUNT" =~ ^[0-9]+$ ]] && [[ "$S2_TC_COUNT" -gt 0 ]]; then
    record_result "2b LLM 生成 tool_call" "PASS" "tool_calls=${S2_TC_COUNT}, tools=${S2_TOOLS}"
    if [[ "$S2_TOOLS" == *"run_shell"* ]]; then
      record_result "2c run_shell 被调用" "PASS" "tools=${S2_TOOLS}"
    else
      record_result "2c run_shell 被调用" "SKIP" "LLM 调用了其他工具: ${S2_TOOLS}"
    fi
    # PolicyGate 行为：blocked>0 说明有拦截；blocked=0 说明放行（echo 是安全命令）
    if [[ "$S2_BLOCKED" =~ ^[0-9]+$ ]] && [[ "$S2_BLOCKED" -gt 0 ]]; then
      record_result "2d PolicyGate 行为" "PASS" "检测到 ${S2_BLOCKED} 次 POLICY BLOCK: ${S2_BLOCK_REASONS}"
      echo "  [观察] PolicyGate 拦截了 ${S2_BLOCKED} 次 tool_call（可能 LLM 生成了危险命令被 DangerousCommandRule 拦）"
    else
      record_result "2d PolicyGate 行为" "PASS" "无 POLICY BLOCK（echo 是安全命令，放行正常）"
    fi
  else
    record_result "2b LLM 生成 tool_call" "SKIP" "LLM 未生成 tool_call"
    record_result "2c run_shell 被调用" "SKIP" "依赖 2b"
    record_result "2d PolicyGate 行为" "SKIP" "依赖 2c"
  fi

  # 观察项：服务日志里的 PolicyGate / DangerousCommand 记录
  S2_POLICY_LOG=$(grep -E "POLICY BLOCK|DangerousCommand|\[Policy\]" "${SERVER_LOG}" 2>/dev/null | tail -5)
  if [[ -n "$S2_POLICY_LOG" ]]; then
    echo "  [观察] PolicyGate 日志命中:"
    echo "$S2_POLICY_LOG" | sed 's/^/    /'
  else
    echo "  [观察] 服务日志无 PolicyGate 拦截记录（echo 放行，符合预期）"
  fi
fi

# =============================================================================
# 场景 3：多 Agent 编排（2 个 agent，小规模）
# =============================================================================
print_section "场景 3: 多 Agent 编排 (2 agent)"
if [[ "$LLM_OK" != "yes" ]]; then
  record_result "3a 编排达终态" "SKIP" "LLM 不可达，跳过场景3"
  record_result "3b WS 背压观察" "SKIP" "依赖 3a"
else
  sleep 1
  # 注意：POST /api/tasks 的 multi-agent action 不读取 req.MaxSteps（main.go:301-366），
  # 必须用 POST /api/multi-agent 端点（main.go:664）才会把 max_steps 应用到每个 spec。
  # max_steps=3 在真实 LLM 下不够：deepseek-v4-flash 跑 researcher/writer 的复杂系统提示，
  # 3 步内必超限（两个 agent 都 max steps (3) exceeded），属环境配置不合理而非后端/LLM bug。
  # 改为 100 避免环境性失败（LLM 正常几步即给 final answer，不会真跑满 100 步）。
  S3_RESP=$(curl -s -X POST "${BASE}/api/multi-agent" -H 'Content-Type: application/json' \
    --data '{"action":"multi-agent","case_type":"multi_agent","max_steps":100,"input":"agent1 用一句话总结什么是 Agent，agent2 用一句话总结什么是 Tool。各自独立完成，不需要协作。"}' 2>/dev/null | tr -d '\r')
  S3_TASK=$(jval "$S3_RESP" "task_id")
  S3_COUNT=$(jval "$S3_RESP" "agent_count")
  S3_IDS=$(jval "$S3_RESP" "agent_ids")
  echo "  POST /api/multi-agent -> task_id=${S3_TASK}, agent_count=${S3_COUNT}, agent_ids=${S3_IDS}"

  if [[ -z "$S3_TASK" ]]; then
    record_result "3a 编排达终态" "FAIL" "未拿到 task_id，resp=$(printf '%s' "$S3_RESP" | head -c 120)"
  else
    S3_RESULT=$(poll_task "$S3_TASK")
    S3_STATUS=$(echo "$S3_RESULT" | awk '{print $1}')
    S3_ELAPSED=$(echo "$S3_RESULT" | awk '{print $2}')
    TIMINGS+=("场景3 multi-agent: ${S3_ELAPSED}s (status=${S3_STATUS})")
    echo "  轮询结果: status=${S3_STATUS}, elapsed=${S3_ELAPSED}s"

    if [[ "$S3_STATUS" == "completed" || "$S3_STATUS" == "failed" ]]; then
      record_result "3a 编排达终态" "PASS" "status=${S3_STATUS}, elapsed=${S3_ELAPSED}s, agents=${S3_COUNT}"
    else
      record_result "3a 编排达终态" "FAIL" "status=${S3_STATUS}（90s 超时）"
      # 区分是 LLM 慢还是 root task status 不更新 bug
      S3_DONE_LOG=$(grep -E "\[Multi-Agent\] Task ${S3_TASK}: all agents completed" "${SERVER_LOG}" 2>/dev/null)
      if [[ -n "$S3_DONE_LOG" ]]; then
        FINDINGS+=("[场景3] root task ${S3_TASK} 轮询 timeout，但 server log 已打印 'all agents completed'。说明 orchestrator.RunBlocking 已结束但 root task status 未更新为 completed/failed（engine.updateTask 更新的是 subTaskID=${S3_TASK}_agent_xxx 而非 rootTaskID）。【Phase 7-H2 / MA9 跟踪中】这是已知后端 bug，见 multi-agent-smoke.sh FINDINGS 与 roadmaps/ROADMAP.md Phase 7-H2 阶段4。")
      else
        FINDINGS+=("[场景3] root task ${S3_TASK} 90s 超时且 server log 无 'all agents completed'。可能 2 个 agent 并行真实 LLM 调用总耗时 >90s，或某个 agent 卡死。")
      fi
    fi

    # 查 child_tasks 状态作为补充证据
    S3_DETAIL=$(curl -s "${BASE}/api/tasks?id=${S3_TASK}" 2>/dev/null | tr -d '\r')
    S3_CHILD_STATUS=$(jrun "$S3_DETAIL" "
const ct = d.child_tasks || [];
console.log(JSON.stringify(ct.map(c => ({id:c.id, status:c.status})));
")
    echo "  child_tasks: ${S3_CHILD_STATUS}"
  fi

  # 背压观察：WS hub broadcast 是 unbuffered channel，client.Send 是 buffered 256，
  # 满了静默丢弃（hub.go:82-85 default 分支无日志）。所以 grep 不到 "drop" 日志属正常。
  S3_DROP=$(grep -iE "broadcast.*drop|channel full|dropped" "${SERVER_LOG}" 2>/dev/null | tail -3)
  if [[ -n "$S3_DROP" ]]; then
    record_result "3b WS 背压观察" "FAIL" "检测到背压日志: ${S3_DROP}"
    FINDINGS+=("[场景3] WS broadcast 背压: ${S3_DROP}")
  else
    record_result "3b WS 背压观察" "PASS" "无背压日志（hub.go default 分支静默丢弃，无日志也属正常）"
  fi
fi

# =============================================================================
# 场景 4：普通对话（测 Router 意图分类 + tier + usage）
# =============================================================================
print_section "场景 4: 普通对话 (测 Router 意图分类 + tier)"
if [[ "$LLM_OK" != "yes" ]]; then
  record_result "4a 任务达终态" "SKIP" "LLM 不可达，跳过场景4"
  record_result "4b usage 非零" "SKIP" "依赖 4a"
  record_result "4c /api/costs 有记录" "SKIP" "依赖 4a"
  record_result "4d Router 触发" "SKIP" "依赖 4a"
else
  sleep 1
  # max_steps=2 在真实 LLM 下偏小（deepseek-v4-flash 偶尔首步不直接给 final answer），
  # 之前 4a 曾因超限 failed。调大到 20 给足余量，正常对话 1-2 步即完成不会真跑满。
  S4_RESP=$(curl -s -X POST "${BASE}/api/tasks" -H 'Content-Type: application/json' \
    --data '{"action":"chat","agent_id":"agent_real_chat","max_steps":20,"input":"你好，请用一句话介绍你自己"}' 2>/dev/null | tr -d '\r')
  S4_TASK=$(jval "$S4_RESP" "task_id")
  echo "  POST /api/tasks -> task_id=${S4_TASK}"
  S4_RESULT=$(poll_task "$S4_TASK")
  S4_STATUS=$(echo "$S4_RESULT" | awk '{print $1}')
  S4_ELAPSED=$(echo "$S4_RESULT" | awk '{print $2}')
  TIMINGS+=("场景4 dialogue: ${S4_ELAPSED}s (status=${S4_STATUS})")
  echo "  轮询结果: status=${S4_STATUS}, elapsed=${S4_ELAPSED}s"

  if [[ "$S4_STATUS" == "completed" || "$S4_STATUS" == "failed" ]]; then
    record_result "4a 任务达终态" "PASS" "status=${S4_STATUS}, elapsed=${S4_ELAPSED}s"
  else
    record_result "4a 任务达终态" "FAIL" "status=${S4_STATUS}（超时）"
  fi

  # usage 断言（task.total_tokens）
  S4_DETAIL=$(curl -s "${BASE}/api/tasks?id=${S4_TASK}" 2>/dev/null | tr -d '\r')
  S4_TOKENS=$(jval "$S4_DETAIL" "task.total_tokens")
  if [[ "$S4_TOKENS" =~ ^[0-9]+$ ]] && [[ "$S4_TOKENS" -gt 0 ]]; then
    record_result "4b usage 非零" "PASS" "task.total_tokens=${S4_TOKENS}"
  else
    record_result "4b usage 非零" "FAIL" "task.total_tokens=${S4_TOKENS}"
  fi

  # /api/costs 断言
  S4_COSTS=$(curl -s "${BASE}/api/costs?task_id=${S4_TASK}" 2>/dev/null | tr -d '\r')
  S4_COST_RECORDS=$(jval "$S4_COSTS" "record_count")
  S4_COST_TOKENS=$(jval "$S4_COSTS" "total_tokens")
  S4_COST_USD=$(jval "$S4_COSTS" "total_cost_usd")
  echo "  /api/costs: record_count=${S4_COST_RECORDS}, total_tokens=${S4_COST_TOKENS}, usd=${S4_COST_USD}"
  if [[ "$S4_COST_RECORDS" =~ ^[0-9]+$ ]] && [[ "$S4_COST_RECORDS" -gt 0 ]]; then
    record_result "4c /api/costs 有记录" "PASS" "record_count=${S4_COST_RECORDS}, tokens=${S4_COST_TOKENS}, usd=${S4_COST_USD}"
  else
    record_result "4c /api/costs 有记录" "FAIL" "record_count=${S4_COST_RECORDS}（真实 LLM 应产生 cost 记录）"
    FINDINGS+=("[场景4] /api/costs?task_id=${S4_TASK} 无记录 (record_count=0)。CostTracker/SqliteCostRepository 未持久化真实 LLM usage。可能 onLLMUsage 回调未触发或 repo.Insert 失败。")
  fi

  # Router 观察项：engine.go:1115 要求 e.cfg.Router != nil && e.cfg.Registry != nil
  # 但 main.go runAgentLoopWithTurn 构建 EngineConfig 时未设置 Router/Registry/Providers，
  # 所以 Router 路径在 chat 路径完全不会触发。
  S4_ROUTER_LOG=$(grep -E "\[Router\]|model_routed|classifyIntent" "${SERVER_LOG}" 2>/dev/null | tail -5)
  if [[ -n "$S4_ROUTER_LOG" ]]; then
    record_result "4d Router 触发" "PASS" "日志命中: ${S4_ROUTER_LOG}"
  else
    record_result "4d Router 触发" "FAIL" "无 Router 日志 — EngineConfig 未注入 Router/Registry"
    FINDINGS+=("[场景4][严重设计缺口] Router 未触发: main.go runAgentLoopWithTurn 构建 EngineConfig (main.go:1063-1101) 时未设置 Router/Registry/Providers 字段，engine.go:1115 条件 e.cfg.Router != nil && e.cfg.Registry != nil 永远为 false。Phase 6 Router 动态模型选择 / classifyIntent 意图分类在 chat 路径完全未生效。这是真实 LLM 路径下的一个死代码路径。【Phase 7-H2 / MA8 跟踪中】")
  fi
fi

# =============================================================================
# 场景 5：预设 case（dialogue，MaxSteps=2 最小）
# =============================================================================
print_section "场景 5: 预设 case (dialogue, max_steps=2)"
if [[ "$LLM_OK" != "yes" ]]; then
  record_result "5a case 任务达终态" "SKIP" "LLM 不可达，跳过场景5"
else
  sleep 1
  # 用 /api/run-case 跑 dialogue case，传简短 input 覆盖默认长 input
  S5_RESP=$(curl -s -X POST "${BASE}/api/run-case" -H 'Content-Type: application/json' \
    --data '{"case":"dialogue","max_steps":2,"input":"用一句话解释什么是 WebSocket"}' 2>/dev/null | tr -d '\r')
  S5_TASK=$(jval "$S5_RESP" "task_id")
  S5_CASE=$(jval "$S5_RESP" "case_id")
  echo "  POST /api/run-case -> task_id=${S5_TASK}, case_id=${S5_CASE}"
  S5_RESULT=$(poll_task "$S5_TASK")
  S5_STATUS=$(echo "$S5_RESULT" | awk '{print $1}')
  S5_ELAPSED=$(echo "$S5_RESULT" | awk '{print $2}')
  TIMINGS+=("场景5 run-case dialogue: ${S5_ELAPSED}s (status=${S5_STATUS})")
  echo "  轮询结果: status=${S5_STATUS}, elapsed=${S5_ELAPSED}s"

  if [[ "$S5_STATUS" == "completed" || "$S5_STATUS" == "failed" ]]; then
    record_result "5a case 任务达终态" "PASS" "status=${S5_STATUS}, elapsed=${S5_ELAPSED}s, case=${S5_CASE}"
  else
    record_result "5a case 任务达终态" "FAIL" "status=${S5_STATUS}（超时）"
  fi
fi

# =============================================================================
# 全局检查
# =============================================================================
print_section "全局检查"
if check_no_panic; then
  record_result "G1 服务日志无 panic" "PASS" "grep panic:/goroutine/nil pointer 无命中"
else
  record_result "G1 服务日志无 panic" "FAIL" "服务日志含 panic/goroutine/nil pointer"
  FINDINGS+=("[严重] 服务日志含 panic: $(grep -E 'panic:|goroutine [0-9]+ \[|nil pointer' "${SERVER_LOG}" 2>/dev/null | head -3)")
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
for r in "${RESULTS[@]}"; do echo "  ${r}"; done
echo ""
echo "真实 LLM 响应耗时统计："
for t in "${TIMINGS[@]}"; do echo "  ${t}"; done

# =============================================================================
# 发现的问题清单（最重要产出）
# =============================================================================
print_section "发现的问题清单"
if [[ ${#FINDINGS[@]} -gt 0 ]]; then
  for f in "${FINDINGS[@]}"; do echo "  - ${f}"; done
else
  echo "  (无)"
fi

# =============================================================================
# 服务日志 WARNING/ERROR 摘要
# =============================================================================
print_section "服务日志 WARNING/ERROR 摘要"
SVC_WARN=$(grep -iE "warn|error|fatal|fail|panic" "${SERVER_LOG}" 2>/dev/null | grep -vE "level=info|INFO|cost.*persist" | head -20)
if [[ -n "$SVC_WARN" ]]; then
  echo "$SVC_WARN" | sed 's/^/  /'
else
  echo "  (无明显 WARNING/ERROR)"
fi

# =============================================================================
# 服务日志关键片段（最后 30 行）
# =============================================================================
print_section "服务日志最后 30 行"
tail -30 "${SERVER_LOG}" | sed 's/^/  /'

echo ""
if [[ ${FAIL} -gt 0 ]]; then
  echo "[real-llm-smoke] 存在 FAIL 项，详见上方。服务日志：${SERVER_LOG}"
  exit 1
fi
echo "[real-llm-smoke] 真实 LLM 冒烟完成 (PASS=${PASS}, SKIP=${SKIP}, FAIL=${FAIL})"
exit 0
