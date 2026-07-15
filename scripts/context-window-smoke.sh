#!/usr/bin/env bash
# =============================================================================
# Multi-Agent Platform — Context Window 可观测性冒烟测试
# =============================================================================
# 作用：启动真实 LLM 模式服务，触发一次简单对话，通过 WS 事件流验证
#       context_window_snapshot 事件被正确发射，并包含 model、
#       max_context_tokens、estimated_total_tokens、estimated_usage_ratio、
#       messages 等字段。
#
# 原则：本脚本只验证后端事件与 API；前端只做静态构建验证。
#
# 环境：Windows + Git Bash。需要 go / curl / node。
# 端口 18300，独立临时 DB /tmp/context-window-$$.db。
# =============================================================================
set -u

PORT="${PORT:-18300}"
BASE="http://localhost:${PORT}"
WS_BASE="ws://localhost:${PORT}/ws"
DB_PATH="/tmp/context-window-$$.db"
SERVER_BIN="/tmp/context-window-server-$$.exe"
SERVER_LOG="/tmp/context-window-server-$$.log"
SERVER_PID=""
EVENTS_FILE="/tmp/context-window-events-$$.json"

PASS=0
FAIL=0
RESULTS=()

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

record_result() {
  local name="$1" result="$2" evidence="$3"
  RESULTS+=("[${result}] ${name}: ${evidence}")
  case "$result" in
    PASS) PASS=$((PASS+1)) ;;
    FAIL) FAIL=$((FAIL+1)) ;;
  esac
  printf '%-6s %-42s %s\n' "[${result}]" "$name" "$evidence"
}

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null
    wait "${SERVER_PID}" 2>/dev/null
  fi
  rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" "${EVENTS_FILE}" 2>/dev/null
}
trap cleanup EXIT

kill_orphans_on_port() {
  local port="$1"
  local pids
  pids=$(netstat -ano 2>/dev/null | grep -E ":${port} " | grep -i "LISTEN" | awk '{print $NF}' | sort -u)
  for pid in $pids; do
    if [[ "$pid" =~ ^[0-9]+$ ]] && [[ "$pid" != "$$" ]]; then
      taskkill //F //PID "$pid" 2>/dev/null || kill -9 "$pid" 2>/dev/null || true
      sleep 1
    fi
  done
}

echo "===== 编译服务 ====="
if ! go build -o "${SERVER_BIN}" ./cmd/server 2>"${SERVER_LOG}"; then
  echo "[FATAL] 编译失败"
  cat "${SERVER_LOG}"
  exit 2
fi

echo "===== 启动服务 (port=${PORT}, LLM_USE_MOCK=false, REQUIRE_AUTH=false) ====="
kill_orphans_on_port "${PORT}"
LLM_USE_MOCK=false \
REQUIRE_AUTH=false \
SERVER_PORT="${PORT}" \
DB_PATH="${DB_PATH}" \
  "${SERVER_BIN}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!

ready=0
for i in $(seq 1 60); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/healthz" 2>/dev/null || true)
  if [[ "${code}" == "200" ]]; then ready=1; break; fi
  sleep 0.5
done
if [[ "${ready}" != "1" ]]; then
  echo "[FATAL] 服务未就绪"
  tail -30 "${SERVER_LOG}"
  exit 3
fi

# 启动后台 WS 收集器
echo "===== 启动 WS 事件收集器 ====="
EVENTS_FILE="/tmp/context-window-events-$$.json"
(
  go run -tags cwsmoke ./scripts/context-window-smoke.go "${WS_BASE}" "${EVENTS_FILE}" >/tmp/context-window-ws-out-$$.log 2>/tmp/context-window-ws-err-$$.log
) &
WS_PID=$!

sleep 1

echo "===== 创建并轮询任务 ====="
TASK_RESP=$(curl -s -X POST "${BASE}/api/tasks" -H 'Content-Type: application/json' \
  --data '{"action":"chat","agent_id":"agent_ctx_win","max_steps":5,"input":"你好，请简短自我介绍"}' 2>/dev/null | tr -d '\r')
TASK_ID=$(jval "$TASK_RESP" "task_id")
echo "  POST /api/tasks -> task_id=${TASK_ID}"

if [[ -z "$TASK_ID" ]]; then
  record_result "任务创建" "FAIL" "未拿到 task_id: $(printf '%s' "$TASK_RESP" | head -c 120)"
  exit 4
fi

poll_task() {
  local tid="$1"
  local start=$SECONDS
  while [[ $SECONDS -lt $((start + 90)) ]]; do
    local body status
    body=$(curl -s "${BASE}/api/tasks?id=${tid}" 2>/dev/null | tr -d '\r')
    status=$(printf '%s' "$body" | node -e "console.log((JSON.parse(require('fs').readFileSync(0,'utf8')).task||{}).status||'not_found')" 2>/dev/null)
    if [[ "$status" == "completed" || "$status" == "failed" ]]; then
      echo "${status} $((SECONDS - start))"
      return 0
    fi
    sleep 1
  done
  echo "timeout 90"
}

RESULT=$(poll_task "$TASK_ID")
STATUS=$(echo "$RESULT" | awk '{print $1}')
ELAPSED=$(echo "$RESULT" | awk '{print $2}')
echo "  轮询结果: status=${STATUS}, elapsed=${ELAPSED}s"

# 等待收集器结束并读取事件
sleep 2
if kill -0 "$WS_PID" 2>/dev/null; then kill "$WS_PID" 2>/dev/null; wait "$WS_PID" 2>/dev/null; fi
sleep 1

WS_EVENTS="[]"
if [[ -f "$EVENTS_FILE" ]]; then
  WS_EVENTS=$(cat "$EVENTS_FILE")
fi

echo "===== 验证 context_window_snapshot 事件 ====="
COUNT=$(jval "$WS_EVENTS" "length")
echo "  收集到 context_window_snapshot 事件数: ${COUNT}"

if ! [[ "$COUNT" =~ ^[0-9]+$ ]] || [[ "$COUNT" -lt 1 ]]; then
  record_result "事件数量" "FAIL" "收集=${COUNT}，期望 >=1"
else
  record_result "事件数量" "PASS" "收集=${COUNT}"

  FIRST=$(node -e "const d=JSON.parse(require('fs').readFileSync(0,'utf8')); console.log(JSON.stringify(d[0]||{}))" <<<"$WS_EVENTS")
  DATA=$(jval "$FIRST" "data")

  MODEL=$(jval "$DATA" "model")
  MAX_TOKENS=$(jval "$DATA" "max_context_tokens")
  EST_TOTAL=$(jval "$DATA" "estimated_total_tokens")
  EST_RATIO=$(jval "$DATA" "estimated_usage_ratio")
  MESSAGES=$(jval "$DATA" "messages")
  MSG_LEN=$(jval "$MESSAGES" "length")

  echo "  model=${MODEL}, max_context_tokens=${MAX_TOKENS}, estimated_total_tokens=${EST_TOTAL}, estimated_usage_ratio=${EST_RATIO}, messages.len=${MSG_LEN}"

  if [[ -n "$MODEL" ]]; then record_result "字段 model" "PASS" "${MODEL}"; else record_result "字段 model" "FAIL" "为空"; fi
  if [[ "$MAX_TOKENS" =~ ^[0-9]+$ ]] && [[ "$MAX_TOKENS" -gt 0 ]]; then record_result "字段 max_context_tokens" "PASS" "${MAX_TOKENS}"; else record_result "字段 max_context_tokens" "FAIL" "${MAX_TOKENS}"; fi
  if [[ "$EST_TOTAL" =~ ^[0-9]+$ ]] && [[ "$EST_TOTAL" -gt 0 ]]; then record_result "字段 estimated_total_tokens" "PASS" "${EST_TOTAL}"; else record_result "字段 estimated_total_tokens" "FAIL" "${EST_TOTAL}"; fi
  if [[ "$EST_RATIO" =~ ^[0-9]+(\.[0-9]+)?$ ]] || [[ "$EST_RATIO" =~ ^[0-9]+\.[0-9]+(e-[0-9]+)?$ ]]; then record_result "字段 estimated_usage_ratio" "PASS" "${EST_RATIO}"; else record_result "字段 estimated_usage_ratio" "FAIL" "${EST_RATIO}"; fi
  if [[ "$MSG_LEN" =~ ^[0-9]+$ ]] && [[ "$MSG_LEN" -ge 2 ]]; then record_result "messages 数量" "PASS" "${MSG_LEN}"; else record_result "messages 数量" "FAIL" "${MSG_LEN}"; fi

  FIRST_MSG=$(node -e "const d=JSON.parse(require('fs').readFileSync(0,'utf8')); const msgs=(d[0]&&d[0].data&&d[0].data.messages)||[]; console.log(JSON.stringify(msgs[0]||{}))" <<<"$WS_EVENTS")
  MSG_ROLE=$(jval "$FIRST_MSG" "role")
  MSG_TOKENS=$(jval "$FIRST_MSG" "estimated_tokens")
  MSG_CONTENT=$(jval "$FIRST_MSG" "content")

  if [[ "$MSG_ROLE" == "system" || "$MSG_ROLE" == "user" ]]; then record_result "首 message role" "PASS" "${MSG_ROLE}"; else record_result "首 message role" "FAIL" "${MSG_ROLE}"; fi
  if [[ "$MSG_TOKENS" =~ ^[0-9]+$ ]]; then record_result "首 message estimated_tokens" "PASS" "${MSG_TOKENS}"; else record_result "首 message estimated_tokens" "FAIL" "${MSG_TOKENS}"; fi
  if [[ -n "$MSG_CONTENT" ]]; then record_result "首 message content" "PASS" "长度=${#MSG_CONTENT}"; else record_result "首 message content" "FAIL" "为空"; fi
fi

echo ""
echo "===== 汇总 ====="
echo "  PASS: ${PASS}"
echo "  FAIL: ${FAIL}"
echo ""
echo "详细结果:"
for r in "${RESULTS[@]}"; do echo "  ${r}"; done

if [[ "$FAIL" -gt 0 ]]; then exit 1; fi
exit 0
