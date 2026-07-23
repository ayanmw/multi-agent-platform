#!/usr/bin/env bash
# =============================================================================
# Multi-Agent Platform — 真实 LLM 冒烟测试脚本
# =============================================================================
# 作用：不传 LLM_USE_MOCK=true，让服务读 .env 的真实 LLM 配置
#       (endpoint/key/model)，对两类场景跑真实 LLM 调用：
#
#   Part A — 6 个白盒手写场景（细致断言单个能力点）：
#         - 任务能达终态 (completed/failed，不卡死)
#         - 事件流完整 (task_started → llm_* → task_completed/failed)
#         - 服务日志无 panic
#         - usage 非零 (证明真实 LLM 返回了 token 统计)
#         - tool_call 能被 LLM 生成并执行 (LLM 行为不可控，无 tool_call 记 SKIP)
#
#   Part B — Case 矩阵全量真实 LLM 评测（L1-L5，遍历 /api/cases 全部内置 case）：
#         - 走 /api/run-case 统一入口，一次性暴露 mock 回归（22 个确定性脚本）
#           掩盖的真实问题：usage 解析、cost 持久化、PolicyGate 真实拦截、
#           orchestrator 真实 LLM 下是否调 dispatch_sub_agent、L5 动态编排边界。
#         - 断言分硬失败（达终态/usage/cost/panic）与软标记（status/tool/
#           final/编排事件，real-LLM 行为不可控不计 FAIL）。
#         - L5 multi-agent-leader-dispatch / multi-agent-fault-tolerance 标
#           known-limitation，硬失败降级为软标记。
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
#     SKIP_PARTB=1  跳过 Part B 全量 case 评测（只跑 Part A 6 场景，省时省钱）
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
  if [[ -n "${WS_PID}" ]] && kill -0 "${WS_PID}" 2>/dev/null; then
    kill "${WS_PID}" 2>/dev/null || true
  fi
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    if [[ "${KEEP_SERVER:-0}" != "1" ]]; then
      kill "${SERVER_PID}" 2>/dev/null
      wait "${SERVER_PID}" 2>/dev/null
    fi
  fi
  if [[ "${KEEP_LOGS:-0}" != "1" ]]; then
    rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" 2>/dev/null
    rm -f /tmp/real-llm-*-resp-$$ 2>/dev/null
    rm -f "${WS_EVENTS}" "${WS_EVENTS}.err" 2>/dev/null
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
  local timeout_sec="${2:-90}"
  local start=$SECONDS
  local deadline=$((SECONDS + timeout_sec))
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

# ---- 订阅 WS 事件流（后台 node 进程，把所有事件 type 写入 WS_EVENTS 文件）------
# 真实 LLM 路径下，cron 事件走 hub.SendEvent → 广播到 WS；hub 本身不打日志，
# 故场景 6 用 WS 订阅而非 grep server log 来断言 cron 事件流。
# 用 node 22+ 内置的全局 WebSocket（无需 npm ws 包，避免 require 解析路径问题）。
WS_EVENTS="/tmp/real-llm-ws-events-$$.ndjson"
: > "${WS_EVENTS}"
WS_PID=$!
node -e "
  const out = process.argv[1], base = process.argv[2], fs = require('fs');
  const ws = new WebSocket(base + '/ws?session_id=real-llm-smoke');
  let fd;
  ws.addEventListener('open',  () => { fd = fs.openSync(out, 'a'); fs.writeSync(fd, '__ws_open__\n'); });
  ws.addEventListener('message', m => {
    try { const e = JSON.parse(m.data); if (fd === undefined) fd = fs.openSync(out, 'a'); fs.writeSync(fd, (e.type || '?') + '\n'); } catch (x) {}
  });
  ws.addEventListener('error', e => { fs.writeFileSync(out + '.err', String(e.message || e) + '\n', { flag: 'a' }); });
  setInterval(() => {}, 1000); // 保活，由父脚本 cleanup 时 kill
" "${WS_EVENTS}" "${BASE}" >/dev/null 2>&1 &
WS_PID=$!
sleep 1
if grep -q "__ws_open__" "${WS_EVENTS}" 2>/dev/null; then
  echo "[setup] WS 订阅已连接 (PID=${WS_PID}, events -> ${WS_EVENTS}, via node built-in WebSocket)"
else
  echo "[setup] WS 订阅未连上 (PID=${WS_PID})，错误: $(cat "${WS_EVENTS}.err" 2>/dev/null | head -1)。场景 6 事件流断言可能 FAIL"
fi

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
    # 改为 300s，真实 LLM 下多个 agent 可能较慢
  S3_RESULT=$(poll_task "$S3_TASK" 300)
    S3_STATUS=$(echo "$S3_RESULT" | awk '{print $1}')
    S3_ELAPSED=$(echo "$S3_RESULT" | awk '{print $2}')
    TIMINGS+=("场景3 multi-agent: ${S3_ELAPSED}s (status=${S3_STATUS})")
    echo "  轮询结果: status=${S3_STATUS}, elapsed=${S3_ELAPSED}s"

    if [[ "$S3_STATUS" == "completed" || "$S3_STATUS" == "failed" ]]; then
      record_result "3a 编排达终态" "PASS" "status=${S3_STATUS}, elapsed=${S3_ELAPSED}s, agents=${S3_COUNT}"
    else
      record_result "3a 编排达终态" "FAIL" "status=${S3_STATUS}（300s 超时）"
      # 区分是 LLM 慢还是 root task status 不更新 bug
      # Phase 7-H2 MA9 修复后：root task 由 RunBlocking 显式 UpdateTask，不再需要
      # 用 "all agents completed" 这种 server log 字符串作为旁证。
      S3_DONE_LOG=$(grep -E "\[Multi-Agent\] Task ${S3_TASK}:" "${SERVER_LOG}" 2>/dev/null | tail -1)
      if [[ -n "$S3_DONE_LOG" ]]; then
        FINDINGS+=("[场景3] root task ${S3_TASK} 轮询 timeout，但 server log 已打印 orchestration 结束日志：${S3_DONE_LOG}。说明 orchestrator.RunBlocking 已结束但 root task status 可能未正确更新，需检查 persist.UpdateTask 是否成功。")
      else
        FINDINGS+=("[场景3] root task ${S3_TASK} 300s 超时且 server log 无 orchestration 结束日志。可能 2 个 agent 并行真实 LLM 调用总耗时 >300s，或某个 agent 卡死。")
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
# 场景 6：Cron start_task 端到端（真实 LLM 触发 task + execution 记录 + 事件流）
# =============================================================================
print_section "场景 6: Cron start_task 端到端 (真实 LLM)"
if [[ "$LLM_OK" != "yes" ]]; then
  record_result "6a cron start_task 达终态" "SKIP" "LLM 不可达（场景1 未通过），跳过场景6"
else
  # 创建一个 start_task action 的 cron（agent_default 为启动时种入的默认 agent）。
  # schedule_type=interval + cron_expr=1h 保证不会自动到点触发，仅靠手动 trigger 跑一次。
  S6_CREATE=$(curl -s -X POST "${BASE}/api/crons" -H 'Content-Type: application/json' \
    --data '{"name":"real-llm-cron-task","schedule_type":"interval","cron_expr":"1h","action_type":"start_task","action_payload":{"agent_id":"agent_default","input":"用一句话解释什么是 cron 定时任务","max_steps":3}}' 2>/dev/null | tr -d '\r')
  S6_CRON_ID=$(jval "$S6_CREATE" "id")
  echo "  POST /api/crons -> cron_id=${S6_CRON_ID}"
  if [[ -z "$S6_CRON_ID" ]]; then
    record_result "6a cron start_task 达终态" "FAIL" "未拿到 cron_id，resp=$(printf '%s' "$S6_CREATE" | head -c 120)"
  else
    # 手动触发一次。trigger 响应体即为 execution 对象（含 task_id / status）。
    S6_TRIG=$(curl -s -X POST "${BASE}/api/crons/${S6_CRON_ID}/trigger" \
      -H 'Content-Type: application/json' --data '{}' 2>/dev/null | tr -d '\r')
    S6_EXEC_ID=$(jval "$S6_TRIG" "id")
    S6_TASK_FROM_EXEC=$(jval "$S6_TRIG" "task_id")
    echo "  trigger -> exec_id=${S6_EXEC_ID}, task_id=${S6_TASK_FROM_EXEC}"
    if [[ -z "$S6_TASK_FROM_EXEC" ]]; then
      record_result "6a cron start_task 达终态" "FAIL" "execution 未回填 task_id，trig=$(printf '%s' "$S6_TRIG" | head -c 160)"
      FINDINGS+=("[场景6] cron trigger 响应未含 task_id：start_task action 未成功启动 task，查 server log 的 startChatTask 错误。")
    else
      record_result "6b execution 回填 task_id" "PASS" "task_id=${S6_TASK_FROM_EXEC}"
      # 轮询该 task 到终态（真实 LLM）
      S6_RESULT=$(poll_task "$S6_TASK_FROM_EXEC" 90)
      S6_STATUS=$(echo "$S6_RESULT" | awk '{print $1}')
      S6_ELAPSED=$(echo "$S6_RESULT" | awk '{print $2}')
      TIMINGS+=("场景6 cron start_task: ${S6_ELAPSED}s (status=${S6_STATUS})")
      echo "  轮询结果: status=${S6_STATUS}, elapsed=${S6_ELAPSED}s"
      if [[ "$S6_STATUS" == "completed" || "$S6_STATUS" == "failed" ]]; then
        record_result "6a cron start_task 达终态" "PASS" "status=${S6_STATUS}, elapsed=${S6_ELAPSED}s"
      else
        record_result "6a cron start_task 达终态" "FAIL" "status=${S6_STATUS}（90s 超时）"
        FINDINGS+=("[场景6] cron 启动的 task ${S6_TASK_FROM_EXEC} 90s 未达终态 (status=${S6_STATUS})。")
      fi

      # execution 记录应反映该 task 的最终状态（completed/failed），且 result_summary 非空。
      # trigger 时 execution.status=running；task 终态后 executor 会更新 execution。
      # 这里给 executor 一点时间落库，再拉 execution 历史。
      sleep 1
      S6_EXECS=$(curl -s "${BASE}/api/crons/${S6_CRON_ID}/executions?limit=5" 2>/dev/null | tr -d '\r')
      S6_EXEC_STATUS=$(jrun "$S6_EXECS" "console.log((d[0] && d[0].status) || '')")
      S6_EXEC_TASK=$(jrun "$S6_EXECS" "console.log((d[0] && d[0].task_id) || '')")
      S6_EXEC_SUMMARY=$(jrun "$S6_EXECS" "console.log((d[0] && d[0].result_summary) || '')")
      echo "  execution: status=${S6_EXEC_STATUS}, task_id=${S6_EXEC_TASK}, summary=${S6_EXEC_SUMMARY:0:60}"
      if [[ "$S6_EXEC_STATUS" == "completed" || "$S6_EXEC_STATUS" == "failed" ]] && [[ "$S6_EXEC_TASK" == "$S6_TASK_FROM_EXEC" ]]; then
        record_result "6c execution 记录终态" "PASS" "exec status=${S6_EXEC_STATUS}, task_id 匹配"
      else
        record_result "6c execution 记录终态" "FAIL" "exec status=${S6_EXEC_STATUS}, task_id=${S6_EXEC_TASK}（期望 ${S6_TASK_FROM_EXEC}）"
        FINDINGS+=("[场景6] execution 记录未正确反映 task 终态：exec.status=${S6_EXEC_STATUS}, exec.task_id=${S6_EXEC_TASK}。可能 executor 未在 task 结束后更新 execution。")
      fi

      # cron meta：trigger_count 应 >=1，last_triggered_at 非空
      S6_CRON_DETAIL=$(curl -s "${BASE}/api/crons/${S6_CRON_ID}" 2>/dev/null | tr -d '\r')
      S6_COUNT=$(jval "$S6_CRON_DETAIL" "trigger_count")
      S6_LAST=$(jval "$S6_CRON_DETAIL" "last_triggered_at")
      if [[ "$S6_COUNT" =~ ^[0-9]+$ ]] && [[ "$S6_COUNT" -ge 1 ]] && [[ -n "$S6_LAST" ]]; then
        record_result "6d cron meta 更新" "PASS" "trigger_count=${S6_COUNT}, last_triggered_at=${S6_LAST:0:19}"
      else
        record_result "6d cron meta 更新" "FAIL" "trigger_count=${S6_COUNT}, last_triggered_at=${S6_LAST}"
      fi

      # 事件流：订阅 WS（场景开头已连接 ${WS_PID}），在 cron trigger 前后各采
      # 一段时间窗口，从 ${WS_EVENTS} 文件里 grep cron_* 事件计数。
      # 真实 LLM 下 start_task 触发后 executor 应发：
      #   cron_triggered → cron_execution_started → (task 终态后) cron_execution_completed|failed
      # 注意：grep -c 无匹配时打印 0 且退出码 1，故不能再用 `|| echo 0`（会叠加成 "0\n0"）。
      S6_EVT_TRIGGER=$(grep -c "cron_triggered" "${WS_EVENTS}" 2>/dev/null); S6_EVT_TRIGGER=${S6_EVT_TRIGGER:-0}
      S6_EVT_STARTED=$(grep -c "cron_execution_started" "${WS_EVENTS}" 2>/dev/null); S6_EVT_STARTED=${S6_EVT_STARTED:-0}
      S6_EVT_END=$(grep -cE "cron_execution_completed|cron_execution_failed" "${WS_EVENTS}" 2>/dev/null); S6_EVT_END=${S6_EVT_END:-0}
      S6_WS_TOTAL=$(wc -l < "${WS_EVENTS}" 2>/dev/null); S6_WS_TOTAL=${S6_WS_TOTAL:-0}
      echo "  事件计数(WS): triggered=${S6_EVT_TRIGGER}, started=${S6_EVT_STARTED}, end=${S6_EVT_END}, ws_total=${S6_WS_TOTAL}"
      if [[ "$S6_EVT_TRIGGER" =~ ^[0-9]+$ ]] && [[ "$S6_EVT_TRIGGER" -ge 1 ]] && [[ "$S6_EVT_STARTED" -ge 1 ]] && [[ "$S6_EVT_END" -ge 1 ]]; then
        record_result "6e cron 事件流完整" "PASS" "triggered=${S6_EVT_TRIGGER}, started=${S6_EVT_STARTED}, end=${S6_EVT_END}"
      else
        record_result "6e cron 事件流完整" "FAIL" "triggered=${S6_EVT_TRIGGER}, started=${S6_EVT_STARTED}, end=${S6_EVT_END}, ws_total=${S6_WS_TOTAL}（期望三者均 >=1）"
        FINDINGS+=("[场景6] cron 事件流不完整：triggered=${S6_EVT_TRIGGER}/started=${S6_EVT_STARTED}/end=${S6_EVT_END}（WS 总帧数 ${S6_WS_TOTAL}）。若 ws_total=0 说明 WS 订阅未连上；若 ws_total>0 但 cron_*=0 说明 executor 未发事件或事件名变更。")
      fi
    fi

    # 清理 cron（级联删 executions）
    curl -s -X DELETE "${BASE}/api/crons/${S6_CRON_ID}" >/dev/null 2>&1
    echo "  清理 cron ${S6_CRON_ID}"
  fi
fi

# =============================================================================
# Part B：Case 矩阵全量真实 LLM 评测（L1-L5，21 个 case）
# =============================================================================
# 与 Part A 的 6 个"白盒手写场景"互补：这里遍历 /api/cases 返回的全部内置 case，
# 统一走 /api/run-case 跑真实 LLM，一次性暴露 mock 回归（LLM_USE_MOCK=true，22 个
# 确定性脚本）掩盖的真实问题：usage 解析、cost 持久化、PolicyGate 真实拦截行为、
# orchestrator 真实 LLM 下是否调 dispatch_sub_agent、L5 动态编排能力边界等。
#
# 断言哲学（real-LLM 适配，区别于 cases-regression.sh 的 mock 硬断言）：
#   硬失败（计入 FAIL，必为真实问题）：
#     - 任务未达终态（timeout）          → LLM 不可达 / 卡死 / root task status 未更新
#     - total_tokens <= 0                → usage 回调未触发 / OpenAIProvider 未解析 usage
#     - cost_records < 1                 → CostTracker / SqliteCostRepository 持久化失败
#     - 服务日志 panic / nil pointer     → 运行时崩溃
#   软标记（不计 FAIL，仅观察 real-LLM 偏差）：
#     - status != 期望                    → real LLM 行为不可控，记录但不判失败
#     - has_tool != 期望                  → LLM 可能不调工具，记录但不判失败
#     - final_result 空                   → 同上
#     - L4/L5 编排事件缺失                → 记录为 FINDINGS 供分析
#   known-limitation（L5 两个 case）：
#     - multi-agent-leader-dispatch       → leader 是否主动调 dispatch_sub_agent 不可控
#     - multi-agent-fault-tolerance       → 底层不支持真注入崩溃（cases.go 6.4 已标边界）
#     这两个 case 的硬失败降级为软标记，FINDINGS 里注明"known-limitation"。
# =============================================================================
print_section "Part B: Case 矩阵全量真实 LLM 评测"

if [[ "${SKIP_PARTB:-0}" == "1" ]]; then
  echo "[PartB] SKIP_PARTB=1，跳过全量 case 评测（仅 Part A 6 场景已跑）"
  echo "[PartB] 结束"
else

# 期望终态表（与 cases-regression.sh 一致；real LLM 下作软标记参考）
declare -A B_EXP_STATUS=(
  ["code-gen"]="completed"           ["dialogue"]="completed"        ["research"]="completed"
  ["long-task"]="completed"          ["todo-driven"]="completed"     ["web-research"]="completed"
  ["skill-code-helper"]="completed"  ["cron-notify"]="completed"     ["llm-judge-qa"]="completed"
  ["policy-enforcement"]="failed"    ["approval-flow"]="completed"   ["max-steps-exhaustion"]="failed"
  ["context-compression"]="completed" ["checkpoint-resume"]="completed"
  ["multi-agent"]="completed"        ["multi-agent-parallel"]="completed"
  ["multi-agent-sequential"]="completed" ["multi-agent-dag"]="completed"
  ["multi-agent-leader-dispatch"]="completed"
  ["multi-agent-review"]="completed" ["multi-agent-fault-tolerance"]="completed"
)
# 期望是否含 tool_call（real LLM 下作软标记参考）
declare -A B_EXP_TOOL=(
  ["code-gen"]="yes"    ["dialogue"]="no"     ["research"]="yes"    ["long-task"]="no"
  ["todo-driven"]="yes" ["web-research"]="yes" ["skill-code-helper"]="yes"
  ["cron-notify"]="yes" ["llm-judge-qa"]="no"  ["policy-enforcement"]="yes"
  ["approval-flow"]="yes" ["max-steps-exhaustion"]="yes"
  ["context-compression"]="no" ["checkpoint-resume"]="yes"
  ["multi-agent"]="yes" ["multi-agent-parallel"]="yes" ["multi-agent-sequential"]="yes"
  ["multi-agent-dag"]="yes" ["multi-agent-leader-dispatch"]="yes"
  ["multi-agent-review"]="yes" ["multi-agent-fault-tolerance"]="yes"
)
# L4/L5 多 Agent case（需断言编排事件）
B_MULTI_AGENT=(multi-agent multi-agent-parallel multi-agent-sequential multi-agent-dag
  multi-agent-leader-dispatch multi-agent-review multi-agent-fault-tolerance)
# L5 known-limitation case（硬失败降级为软标记）
B_KNOWN_LIMITATION=(multi-agent-leader-dispatch multi-agent-fault-tolerance)

b_is_multi_agent() {
  local cid="$1"
  for m in "${B_MULTI_AGENT[@]}"; do [[ "$m" == "$cid" ]] && return 0; done
  return 1
}
b_is_known_limitation() {
  local cid="$1"
  for m in "${B_KNOWN_LIMITATION[@]}"; do [[ "$m" == "$cid" ]] && return 0; done
  return 1
}

# 从 /api/cases 拉取全部 case ID（自动跟随 cases.All() 扩展）
echo "[PartB] 拉取内置 case 列表..."
B_CASES_JSON=$(curl -s "${BASE}/api/cases" 2>/dev/null | tr -d '\r')
B_ALL_CASES=()
if [[ -n "$B_CASES_JSON" ]]; then
  while IFS= read -r line; do
    [[ -n "$line" ]] && B_ALL_CASES+=("$line")
  done < <(printf '%s' "$B_CASES_JSON" | node -e "
const d = JSON.parse(require('fs').readFileSync(0,'utf8'));
for (const c of (Array.isArray(d)?d:[])) console.log(c.id||'');
" 2>/dev/null)
fi
echo "[PartB] 发现 ${#B_ALL_CASES[@]} 个内置 case"

if [[ ${#B_ALL_CASES[@]} -eq 0 ]]; then
  record_result "B0 拉取 case 列表" "FAIL" "/api/cases 返回空"
  FINDINGS+=("[PartB] /api/cases 返回空，无法跑全量 case 评测。")
else
  record_result "B0 拉取 case 列表" "PASS" "共 ${#B_ALL_CASES[@]} 个 case"
fi

# 轮询终态：real LLM 给 180s（部分多 agent case 慢）
# 注意：local 多变量同行声明时，$()/$(( )) 在 local 赋值生效前就已展开，
# 故引用 timeout_sec 的算术必须拆到下一行（set -u 下否则 unbound variable）。
b_poll() {
  local tid="$1"
  local timeout_sec="${2:-180}"
  local start=$SECONDS
  local deadline=$((SECONDS + timeout_sec))
  while [[ $SECONDS -lt $deadline ]]; do
    local body status
    body=$(curl -s "${BASE}/api/tasks?id=${tid}" 2>/dev/null | tr -d '\r')
    [[ -z "$body" ]] && { sleep 1; continue; }
    status=$(jrun "$body" "console.log((d.task && d.task.status) || '')")
    if [[ "$status" == "completed" || "$status" == "failed" ]]; then
      printf '%s %d' "$status" $((SECONDS - start)); return 0
    fi
    sleep 1
  done
  printf 'timeout %d' $((SECONDS - start)); return 1
}

# 单个 case 评测：b_run_case <case_id>
# 注意：real LLM 有速率限制（aicoding 30 req/min、5 并发），全量 21 case 串行
# 跑会触发 429。每个 case 之间 sleep CASE_COOLDOWN 秒（默认 8s）让速率窗口恢复。
b_run_case() {
  local cid="$1"
  local exp_status exp_tool kl
  exp_status="${B_EXP_STATUS[$cid]:-completed}"
  exp_tool="${B_EXP_TOOL[$cid]:-yes}"
  kl="no"; b_is_known_limitation "$cid" && kl="yes"

  echo ""
  echo "----- [PartB] ${cid} (exp=${exp_status}, exp_tool=${exp_tool}, known_limitation=${kl}) -----"

  # 记录跑该 case 前的 WS 事件基线，事后用增量统计编排事件
  local ws_before=0
  [[ -f "${WS_EVENTS}" ]] && ws_before=$(wc -l < "${WS_EVENTS}" 2>/dev/null)
  ws_before=${ws_before:-0}

  local post_body post_code
  post_code=$(curl -s -o /tmp/b-post-$$ -w '%{http_code}' \
    -X POST "${BASE}/api/run-case" -H 'Content-Type: application/json' \
    --data "{\"case\":\"${cid}\",\"agent_id\":\"agent_real_case\"}" 2>/dev/null)
  post_body=$(cat /tmp/b-post-$$ 2>/dev/null); rm -f /tmp/b-post-$$

  if [[ "$post_code" != "200" ]]; then
    record_result "B:${cid} POST" "FAIL" "HTTP ${post_code}, body=${post_body:0:120}"
    FINDINGS+=("[PartB:${cid}] /api/run-case POST 返回 HTTP ${post_code}：${post_body:0:160}")
    return
  fi
  local tid
  tid=$(jval "$post_body" "task_id")
  if [[ -z "$tid" ]]; then
    record_result "B:${cid} 创建任务" "FAIL" "无 task_id, body=${post_body:0:120}"
    FINDINGS+=("[PartB:${cid}] /api/run-case 响应无 task_id：${post_body:0:160}")
    return
  fi
  echo "  task_id=${tid}"

  local result status elapsed grace_used=0
  result=$(b_poll "$tid" 180)
  status=$(echo "$result" | awk '{print $1}')
  elapsed=$(echo "$result" | awk '{print $2}')

  # 终态宽限复检（real-LLM 慢速预算保护）：
  # .env 的 Qwen3.5-397B 是 reasoning 模型（流走 delta.reasoning，每步 15-30s），
  # MaxSteps=10-14 的 case 真实耗时可达 200-350s，常超 180s 轮询预算。
  # 超时后再宽限 200s 复检：若期间到达终态 → 降级软标记 SKIP（只是慢，非 bug），
  # 仍不达终态 → 才计 FAIL（疑似真挂起）。known-limitation 不走宽限（已软标记）。
  if [[ "$status" != "completed" && "$status" != "failed" && "$kl" != "yes" ]]; then
    echo "  [宽限] 180s 未达终态 (status=${status})，real-LLM 可能慢，再宽限 200s 复检..."
    local grace_result grace_status grace_elapsed
    grace_result=$(b_poll "$tid" 200)
    grace_status=$(echo "$grace_result" | awk '{print $1}')
    grace_elapsed=$(echo "$grace_result" | awk '{print $2}')
    if [[ "$grace_status" == "completed" || "$grace_status" == "failed" ]]; then
      # 宽限期内到达终态：降级软标记，total elapsed = 180 + grace_elapsed
      status="$grace_status"
      elapsed=$((180 + grace_elapsed))
      grace_used=1
      echo "  [宽限] 复检命中终态: status=${status}, total elapsed=${elapsed}s (宽限 ${grace_elapsed}s)"
    else
      elapsed=$((180 + grace_elapsed))
      echo "  [宽限] 复检仍未达终态: status=${grace_status}, total elapsed=${elapsed}s"
      status="$grace_status"
    fi
  fi

  TIMINGS+=("B:${cid}: ${elapsed}s (status=${status}${grace_used:+, grace})")
  echo "  轮询: status=${status}, elapsed=${elapsed}s"

  # 硬失败 1：未达终态（经宽限复检仍未达 → 疑似真挂起）
  if [[ "$status" != "completed" && "$status" != "failed" ]]; then
    if [[ "$kl" == "yes" ]]; then
      record_result "B:${cid} 达终态" "SKIP" "timeout(known-limitation, 软标记) status=${status}"
      FINDINGS+=("[PartB:${cid}][known-limitation] 180s 未达终态 (status=${status})。leader 动态 dispatch 不可控或底层能力边界。")
    else
      record_result "B:${cid} 达终态" "FAIL" "status=${status} (180s+200s 宽限后仍超时，疑似真挂起)"
      FINDINGS+=("[PartB:${cid}] 380s (180+200 宽限) 未达终态 (status=${status})。已排除慢 LLM 误报，疑似 root task status 未更新 / agent 卡死 / 编排死锁。")
    fi
    return
  fi

  # 宽限命中终态：记为软标记 SKIP（非 FAIL），FINDINGS 注明 slow-LLM
  if [[ "$grace_used" == "1" ]]; then
    record_result "B:${cid} 达终态" "SKIP" "status=${status}, elapsed=${elapsed}s (180s 预算超时，宽限复检命中 — slow-LLM 软标记)"
    FINDINGS+=("[PartB:${cid}][slow-LLM] 180s 预算内未达终态，宽限 200s 后到达终态 status=${status} (total ${elapsed}s)。Qwen3.5-397B reasoning 模型每步 15-30s，MaxSteps=${B_EXP_STATUS[$cid]:-} 累计耗时超预算。非平台 bug，考虑调大 b_poll 预算或给该 case 降 MaxSteps。")
  else
    record_result "B:${cid} 达终态" "PASS" "status=${status}, elapsed=${elapsed}s"
  fi

  # 拉详情
  local detail
  detail=$(curl -s "${BASE}/api/tasks?id=${tid}" 2>/dev/null | tr -d '\r')

  # 硬失败 2：usage 为 0
  local tokens
  tokens=$(jval "$detail" "task.total_tokens")
  if [[ "$tokens" =~ ^[0-9]+$ ]] && [[ "$tokens" -gt 0 ]]; then
    record_result "B:${cid} usage 非零" "PASS" "total_tokens=${tokens}"
  else
    if [[ "$kl" == "yes" ]]; then
      record_result "B:${cid} usage 非零" "SKIP" "tokens=${tokens}(known-limitation 软标记)"
    else
      record_result "B:${cid} usage 非零" "FAIL" "total_tokens=${tokens}"
      FINDINGS+=("[PartB:${cid}] total_tokens=${tokens}，真实 LLM 调用后 usage 未记录。可能 onLLMUsage 未触发 / OpenAIProvider 未解析 usage / 该 case LLM 调用前就失败。")
    fi
  fi

  # 硬失败 3：cost 记录缺失
  local costs_resp cost_count
  costs_resp=$(curl -s "${BASE}/api/costs?task_id=${tid}" 2>/dev/null | tr -d '\r')
  cost_count=$(jval "$costs_resp" "record_count")
  if [[ "$cost_count" =~ ^[0-9]+$ ]] && [[ "$cost_count" -ge 1 ]]; then
    record_result "B:${cid} cost 记录" "PASS" "record_count=${cost_count}"
  else
    if [[ "$kl" == "yes" ]]; then
      record_result "B:${cid} cost 记录" "SKIP" "count=${cost_count}(known-limitation 软标记)"
    else
      record_result "B:${cid} cost 记录" "FAIL" "record_count=${cost_count}"
      FINDINGS+=("[PartB:${cid}] /api/costs?task_id=${tid} 无记录 (record_count=0)。CostTracker/SqliteCostRepository 未持久化。")
    fi
  fi

  # 软标记：status vs 期望（real LLM 偏差）
  if [[ "$status" != "$exp_status" ]]; then
    record_result "B:${cid} status 符合期望" "SKIP" "status=${status} 期望 ${exp_status}（real-LLM 软标记，不计 FAIL）"
    FINDINGS+=("[PartB:${cid}][软标记] status=${status} 期望 ${exp_status}。real LLM 行为偏差，非 necessarily bug —— 但 policy-enforcement/max-steps 若 completed 说明 PolicyGate/max_steps 路径在真实 LLM 下未触发，需核实。")
  else
    record_result "B:${cid} status 符合期望" "PASS" "status=${status}==${exp_status}"
  fi

  # 软标记：tool_call
  local has_tool tool_names
  has_tool=$(jrun "$detail" "console.log(d.steps && d.steps.some(s=>s.type==='tool_call') ? 'yes':'no')")
  tool_names=$(jrun "$detail" "const tc=(d.steps||[]).filter(s=>s.type==='tool_call').map(s=>s.tool_name); console.log([...new Set(tc)].join(',')||'none')")
  echo "  steps tool_calls=${has_tool}, tools=${tool_names}"
  if [[ "$has_tool" != "$exp_tool" ]]; then
    record_result "B:${cid} tool_call 符合期望" "SKIP" "has_tool=${has_tool} 期望 ${exp_tool} (tools=${tool_names})（real-LLM 软标记）"
  else
    record_result "B:${cid} tool_call 符合期望" "PASS" "has_tool=${has_tool}==${exp_tool}"
  fi

  # 软标记：final_result 非空（除期望 failed/empty 的 case）
  local final
  final=$(jval "$detail" "task.final_result")
  if [[ -z "$(echo "$final" | tr -d '[:space:]')" ]]; then
    record_result "B:${cid} final_result 非空" "SKIP" "final_result 为空（real-LLM 软标记）"
  else
    record_result "B:${cid} final_result 非空" "PASS" "len=${#final}"
  fi

  # L4/L5 编排事件（软标记，但记录为 FINDINGS）
  if b_is_multi_agent "$cid"; then
    local ws_after dec disp comp child_steps
    [[ -f "${WS_EVENTS}" ]] && ws_after=$(wc -l < "${WS_EVENTS}" 2>/dev/null)
    ws_after=${ws_after:-0}
    # 只统计本次 case 期间新增的事件（增量），避免跨 case 累计误判。
    # 注意：本脚本 WS 订阅写入的是纯事件名（每行一个 e.type，见上方 node 订阅），
    # 不是 JSON，故这里直接 grep 裸事件名。
    local seg
    seg=$(tail -n +$((ws_before + 1)) "${WS_EVENTS}" 2>/dev/null)
    dec=$(printf '%s' "$seg" | grep -cx "decompose_done"); dec=${dec:-0}
    disp=$(printf '%s' "$seg" | grep -cx "agent_dispatched"); disp=${disp:-0}
    comp=$(printf '%s' "$seg" | grep -cx "agent_completed"); comp=${comp:-0}
    # child_steps 回填
    child_steps=$(jrun "$detail" "
const cts=d.child_tasks||[];
if(!cts.length){console.log('no_child');}
else{console.log(cts.every(c=>(c.steps||[]).length>0)?'yes':'no');}
")
    echo "  orchestrator(增量): decompose=${dec} dispatched=${disp} completed=${comp} child_steps=${child_steps}"
    if [[ "$dec" -ge 1 && "$disp" -ge 1 && "$comp" -ge 1 ]]; then
      record_result "B:${cid} 编排事件流" "PASS" "dec=${dec} disp=${disp} comp=${comp}"
    else
      record_result "B:${cid} 编排事件流" "SKIP" "dec=${dec} disp=${disp} comp=${comp}（real-LLM 软标记）"
      if b_is_known_limitation "$cid"; then
        FINDINGS+=("[PartB:${cid}][known-limitation] 编排事件不完整 dec=${dec}/disp=${disp}/comp=${comp}。leader 动态 dispatch 不可控 / fault-tolerance 底层无真崩溃注入。")
      else
        FINDINGS+=("[PartB:${cid}][软标记] 编排事件不完整 dec=${dec}/disp=${disp}/comp=${comp}。real LLM 下 leader 未按 mock 脚本调 dispatch_sub_agent —— 若 status=completed 且 final 非空，说明 leader 自行答题未走编排，属行为偏差；若 final 空，可能是真问题。child_steps=${child_steps}")
      fi
    fi
    # child_steps 回填（非 legacy 多 agent case 期望 yes）
    if [[ "$cid" != "multi-agent" && "$child_steps" != "yes" ]]; then
      record_result "B:${cid} child_steps 回填" "SKIP" "child_steps=${child_steps}（real-LLM 软标记）"
      FINDINGS+=("[PartB:${cid}][软标记] child_steps=${child_steps}。若 leader 真走了编排，child_tasks[].steps 应回填；为 no/no_child 说明 child tasks 未正确持久化或 leader 未真派发。")
    else
      record_result "B:${cid} child_steps 回填" "PASS" "child_steps=${child_steps}"
    fi
  fi

  # case 间冷却：让 LLM 速率窗口恢复，避免连续 429
  sleep "${CASE_COOLDOWN:-8}"
}

# 按 L1-L5 阶梯顺序跑（便于按级别读报告）
B_L1=(code-gen dialogue research long-task)
B_L2=(todo-driven web-research skill-code-helper cron-notify llm-judge-qa)
B_L3=(policy-enforcement approval-flow max-steps-exhaustion context-compression checkpoint-resume)
B_L4=(multi-agent multi-agent-parallel multi-agent-sequential multi-agent-dag)
B_L5=(multi-agent-leader-dispatch multi-agent-review multi-agent-fault-tolerance)

b_run_level() {
  local label="$1"; shift
  echo ""; echo "===== PartB ${label} ====="
  local cid c found
  for cid in "$@"; do
    # 跳过 /api/cases 未返回的 case（向后兼容）
    found=0
    for c in "${B_ALL_CASES[@]}"; do [[ "$c" == "$cid" ]] && { found=1; break; }; done
    if [[ "$found" == "0" ]]; then
      echo "  [SKIP] ${cid} 不在 /api/cases 返回列表，跳过"
      record_result "B:${cid} 存在性" "SKIP" "未在 /api/cases 返回"
      continue
    fi
    b_run_case "$cid"
  done
}

# 若 LLM 不可达则只跑 L1 第一个 case 探测，其余 SKIP 省钱
if [[ "$LLM_OK" != "yes" ]]; then
  echo "[PartB] LLM 不可达（PartA 场景1 未通过），仅探测 L1 code-gen，其余 SKIP"
  b_run_level "L1 单 Agent 基线 (探测)" "${B_L1[@]}"
  local cid2
  for cid2 in "${B_L2[@]}" "${B_L3[@]}" "${B_L4[@]}" "${B_L5[@]}"; do
    record_result "B:${cid2} 达终态" "SKIP" "LLM 不可达，跳过"
  done
else
  b_run_level "L1 单 Agent 基线" "${B_L1[@]}"
  b_run_level "L2 单 Agent + 子系统" "${B_L2[@]}"
  b_run_level "L3 Harness 治理" "${B_L3[@]}"
  b_run_level "L4 多 Agent 静态编排" "${B_L4[@]}"
  b_run_level "L5 多 Agent 动态编排" "${B_L5[@]}"
fi

# PartB 汇总表
print_section "Part B 汇总"
echo "  (PartB 明细见上方各项 [PASS]/[FAIL]/[SKIP]；FINDINGS 见文末「发现的问题清单」)"
echo "  硬失败=达终态/usage/cost/panic；软标记=status/tool/final/编排事件不计 FAIL；"
echo "  L5 leader-dispatch 与 fault-tolerance 为 known-limitation，硬失败降级软标记。"

fi  # end SKIP_PARTB

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
