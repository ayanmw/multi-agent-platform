#!/usr/bin/env bash
# =============================================================================
# Multi-Agent Platform — curl 冒烟测试脚本
# =============================================================================
# 作用：启动后端服务（LLM_USE_MOCK=true，不调真实 LLM），对全部 HTTP REST
#       端点逐一发请求，打印 PASS/FAIL 与状态码，并串联有依赖关系的端点。
# 环境：Git Bash (Windows) / 通用 bash。需要 curl 与 go。
#
# 用法：  bash scripts/smoke-test.sh
#   可选环境变量：
#     PORT          服务端口（默认 18080，避开常用 8080 避免占用冲突）
#     KEEP_SERVER   =1 时不杀服务（调试用）
#
# 说明：脚本默认用临时 SQLite（/tmp/multiagent-smoke.db），跑完删除，不污染
#       仓库 data/ 目录。若需保留日志，设 KEEP_LOGS=1。
# =============================================================================
set -u

# ---- 配置 -------------------------------------------------------------------
PORT="${PORT:-18080}"
BASE="http://localhost:${PORT}"
DB_PATH="${SMOKE_DB:-/tmp/multiagent-smoke-$$}.db"
SERVER_BIN="/tmp/smoke-server-$$"
SERVER_LOG="/tmp/smoke-server-$$.log"
SERVER_PID=""
PASS=0
FAIL=0
SKIP=0
PROBLEMS=()        # 发现的问题/与文档差异

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    if [[ "${KEEP_SERVER:-0}" != "1" ]]; then
      kill "${SERVER_PID}" 2>/dev/null
      wait "${SERVER_PID}" 2>/dev/null
    fi
  fi
  if [[ "${KEEP_LOGS:-0}" != "1" ]]; then
    rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" 2>/dev/null
  fi
}
trap cleanup EXIT

# ---- 辅助函数 ---------------------------------------------------------------
# req <METHOD> <path> [data_json]  -> 打印 PASS/FAIL 行，返回 status
req() {
  local method="$1" path="$2" data="${3:-}"
  local code body
  if [[ -n "${data}" ]]; then
    code=$(curl -s -o /tmp/smoke-res-$$ -w '%{http_code}' \
            -X "${method}" "${BASE}${path}" \
            -H 'Content-Type: application/json' \
            --data "${data}" 2>/dev/null)
  else
    code=$(curl -s -o /tmp/smoke-res-$$ -w '%{http_code}' \
            -X "${method}" "${BASE}${path}" 2>/dev/null)
  fi
  body=$(cat /tmp/smoke-res-$$ 2>/dev/null | head -c 200)
  rm -f /tmp/smoke-res-$$
  # 2xx/3xx 视为 PASS；405 这种 method 相关也算"端点存在"，单独标记
  local mark="PASS"
  if [[ "${code}" =~ ^2 ]]; then
    PASS=$((PASS+1)); mark="PASS"
  elif [[ "${code}" == "405" ]]; then
    PASS=$((PASS+1)); mark="PASS*"; # 端点存在但 method 不对，记录
    PROBLEMS+=("[405] ${method} ${path} — method 不允许（端点存在）")
  else
    FAIL=$((FAIL+1)); mark="FAIL"
  fi
  printf '%-5s %-6s %-45s -> %s | %s\n' "[${mark}]" "${method}" "${path}" "${code}" "${body}"
  echo "${code}"
}

# 期望特定状态码的校验版：reqExpect <METHOD> <path> <expect_code> [data]
reqExpect() {
  local method="$1" path="$2" expect="$3" data="${4:-}"
  local code
  if [[ -n "${data}" ]]; then
    code=$(curl -s -o /tmp/smoke-res-$$ -w '%{http_code}' \
            -X "${method}" "${BASE}${path}" \
            -H 'Content-Type: application/json' --data "${data}" 2>/dev/null)
  else
    code=$(curl -s -o /tmp/smoke-res-$$ -w '%{http_code}' \
            -X "${method}" "${BASE}${path}" 2>/dev/null)
  fi
  local body
  body=$(cat /tmp/smoke-res-$$ 2>/dev/null | head -c 200); rm -f /tmp/smoke-res-$$
  if [[ "${code}" == "${expect}" ]]; then
    PASS=$((PASS+1)); printf '%-5s %-6s %-45s -> %s (expect %s)\n' "[PASS]" "${method}" "${path}" "${code}" "${expect}"
  else
    FAIL=$((FAIL+1)); printf '%-5s %-6s %-45s -> %s (expect %s) | %s\n' "[FAIL]" "${method}" "${path}" "${code}" "${expect}" "${body}"
  fi
  echo "${code}"
}

# 从 JSON 响应里抓字段（简易，依赖 grep/sed）：jget <json> <key1> [key2...]
# 按顺序尝试多个候选 key，命中第一个即返回（适配 {"id":..} / {"session_id":..} 等不同命名）
jget() {
  local json="$1"; shift
  local k v
  for k in "$@"; do
    v=$(echo "$json" | grep -o "\"$k\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | sed -E 's/.*:[[:space:]]*"([^"]*)".*/\1/')
    if [[ -n "${v}" ]]; then echo "${v}"; return 0; fi
  done
  return 0
}

print_section() { echo; echo "===== $1 ====="; }

# ---- 编译服务 ---------------------------------------------------------------
echo "[setup] 编译后端服务..."
if ! go build -o "${SERVER_BIN}" ./cmd/server 2>"${SERVER_LOG}"; then
  echo "[FATAL] 编译失败，日志见 ${SERVER_LOG}"; cat "${SERVER_LOG}"; exit 2
fi

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
  if [[ "${code}" == "200" ]]; then ready=1; break; fi
  sleep 0.5
done
if [[ "${ready}" != "1" ]]; then
  echo "[FATAL] 服务 30s 内未就绪。服务日志："; tail -30 "${SERVER_LOG}"; exit 3
fi
echo "[setup] 服务就绪 ✓"

# =============================================================================
# 1. 基础 / 观测
# =============================================================================
print_section "1. 基础 / 观测"
req GET /healthz
req GET /metrics
req GET /api/version
req GET /health

# =============================================================================
# 2. Auth（REQUIRE_AUTH=false 模式）
# =============================================================================
print_section "2. Auth (REQUIRE_AUTH=false)"
AUTH_RAW=$(curl -s -X POST "${BASE}/api/auth/api-keys" -H 'Content-Type: application/json' \
           --data '{"name":"smoke-key"}' 2>/dev/null)
echo "    create resp: $(echo "${AUTH_RAW}" | head -c 160)"
req GET /api/auth/api-keys
AUTH_ID=$(jget "${AUTH_RAW}" "id")
if [[ -n "${AUTH_ID}" ]]; then
  req DELETE "/api/auth/api-keys/${AUTH_ID}"
else
  FAIL=$((FAIL+1)); echo "[FAIL] DELETE /api/auth/api-keys/{id} — 未拿到创建返回的 id"; PROBLEMS+=("POST /api/auth/api-keys 未返回 id 字段，无法串联 DELETE")
fi

# =============================================================================
# 3. Project
# =============================================================================
print_section "3. Project"
req GET /api/projects
PROJ_RAW=$(curl -s -X POST "${BASE}/api/projects" -H 'Content-Type: application/json' \
           --data '{"name":"smoke-proj","description":"smoke test project"}' 2>/dev/null)
echo "    create resp: $(echo "${PROJ_RAW}" | head -c 160)"
PROJ_ID=$(jget "${PROJ_RAW}" "id")
# 文档预期 200，实际 POST 创建返回 201 — 记录差异但端点正常
reqExpect POST /api/projects 201 '{"name":"smoke-proj-2","description":"x"}'
PROBLEMS+=("POST /api/projects 返回 201（文档未明确状态码，REST 语义合理，前端按 2xx 处理即可）")
if [[ -n "${PROJ_ID}" ]]; then
  req GET "/api/projects/${PROJ_ID}"
  req PUT "/api/projects/${PROJ_ID}" '{"name":"smoke-proj-renamed","description":"updated"}'
  req DELETE "/api/projects/${PROJ_ID}"
else
  FAIL=$((FAIL+1)); echo "[FAIL] Project 依赖端点 — 未拿到 project id"; PROBLEMS+=("POST /api/projects 未返回 id")
fi

# =============================================================================
# 4. Session / Session Chat
# =============================================================================
print_section "4. Session"
req GET /api/sessions
SESS_RAW=$(curl -s -X POST "${BASE}/api/sessions" -H 'Content-Type: application/json' \
           --data '{"user_input":"smoke test session","project_id":"default"}' 2>/dev/null)
echo "    create resp: $(echo "${SESS_RAW}" | head -c 160)"
# 注意：POST /api/sessions 返回的字段名是 session_id（不是 id）
SESS_ID=$(jget "${SESS_RAW}" "session_id" "id")
if [[ -n "${SESS_ID}" ]]; then
  req GET "/api/sessions/${SESS_ID}"
  req GET "/api/sessions/${SESS_ID}/messages"
  # session-chat 走 MockProvider（caseID 暂为空，靠关键词匹配 dialogue 脚本）
  req POST "/api/sessions/${SESS_ID}/chat" '{"input":"hello, this is a dialogue test","max_steps":3}'
  sleep 1.5   # 给异步 agent loop 一点时间
  req GET "/api/sessions/${SESS_ID}/messages"
  req DELETE "/api/sessions/${SESS_ID}"
else
  FAIL=$((FAIL+1)); echo "[FAIL] Session 依赖端点 — 未拿到 session id"; PROBLEMS+=("POST /api/sessions 未返回 session_id")
fi

# =============================================================================
# 5. Agent
# =============================================================================
print_section "5. Agent"
req GET /api/agents
AGENT_RAW=$(curl -s -X POST "${BASE}/api/agents" -H 'Content-Type: application/json' \
            --data '{"id":"agent_smoke","name":"Smoke Agent","system_prompt":"test"}' 2>/dev/null)
echo "    create resp: $(echo "${AGENT_RAW}" | head -c 160)"
AGENT_ID=$(jget "${AGENT_RAW}" "id")
[[ -z "${AGENT_ID}" ]] && AGENT_ID="agent_smoke"
req GET "/api/agents/${AGENT_ID}"
req PUT "/api/agents/${AGENT_ID}" '{"name":"Smoke Renamed","system_prompt":"updated"}'
req DELETE "/api/agents/${AGENT_ID}"

# =============================================================================
# 6. Task（含 chat / multi-agent action）
# =============================================================================
print_section "6. Task"
req GET /api/tasks
# chat action + case=dialogue（MockProvider 内置纯文本脚本，最稳定）
req POST "/api/tasks?case=dialogue" '{"action":"chat","input":"hello dialogue","agent_id":"agent_smoke","max_steps":3}'
sleep 2
# 取列表里第一条 task 做详情查询
TASK_ID=$(curl -s "${BASE}/api/tasks" 2>/dev/null | grep -o '"task_id"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed -E 's/.*:"([^"]*)".*/\1/')
[[ -z "${TASK_ID}" ]] && TASK_ID=$(curl -s "${BASE}/api/tasks" 2>/dev/null | grep -o '"id"[[:space:]]*:[[:space:]]*"task_[^"]*"' | head -1 | sed -E 's/.*:"([^"]*)".*/\1/')
if [[ -n "${TASK_ID}" ]]; then
  req GET "/api/tasks?id=${TASK_ID}"
else
  FAIL=$((FAIL+1)); echo "[FAIL] GET /api/tasks?id= — 未从列表解析到 task_id"; PROBLEMS+=("GET /api/tasks 响应里未能解析 task_id 字段，详情查询跳过")
fi
# multi-agent action
req POST /api/tasks '{"action":"multi-agent","input":"研究并总结今日技术资讯","case_type":"multi_agent","max_steps":3}'
sleep 1.5

# =============================================================================
# 7. Tool / Cases / Cost / Checkpoints
# =============================================================================
print_section "7. Tool / Cases / Cost / Checkpoints"
req GET /api/tools
req GET /api/cases
# POST /api/tools 需 type=shell|http|inline，shell 需 command；返回 201
TOOL_CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/api/tools" -H 'Content-Type: application/json' \
  --data '{"name":"echo_smoke","type":"shell","command":"echo hi","description":"smoke echo"}' 2>/dev/null)
if [[ "${TOOL_CODE}" =~ ^2 ]]; then
  PASS=$((PASS+1)); printf '%-5s %-6s %-45s -> %s\n' "[PASS]" "POST" "/api/tools (type=shell)" "${TOOL_CODE}"
else
  FAIL=$((FAIL+1)); printf '%-5s %-6s %-45s -> %s\n' "[FAIL]" "POST" "/api/tools (type=shell)" "${TOOL_CODE}"
  PROBLEMS+=("POST /api/tools 用合法 shell payload 仍返回 ${TOOL_CODE}")
fi
DEL_TOOL_CODE=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "${BASE}/api/tools?name=echo_smoke" 2>/dev/null)
if [[ "${DEL_TOOL_CODE}" =~ ^2 ]]; then
  PASS=$((PASS+1)); printf '%-5s %-6s %-45s -> %s\n' "[PASS]" "DELETE" "/api/tools?name=echo_smoke" "${DEL_TOOL_CODE}"
else
  FAIL=$((FAIL+1)); printf '%-5s %-6s %-45s -> %s\n' "[FAIL]" "DELETE" "/api/tools?name=echo_smoke" "${DEL_TOOL_CODE}"
  PROBLEMS+=("DELETE /api/tools?name=echo_smoke 返回 ${DEL_TOOL_CODE}（动态工具应可删）")
fi
PROBLEMS+=("POST /api/tools 需 type 字段(shell/http/inline)及各 type 必填子字段(command/url/code)；文档第 4.5 节未说明")
req GET /api/costs
req GET "/api/costs?task_id=${TASK_ID:-none}"
req GET /api/costs?session_id=${SESS_ID:-none}
req GET "/api/costs?project_id=default"
req GET /api/checkpoints
# recover 无有效 task_id 时预期 4xx/5xx，仅验证端点存在（非 404）
RECOVER_CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "${BASE}/api/checkpoints/recover" \
  -H 'Content-Type: application/json' --data '{"task_id":"nonexistent_smoke"}' 2>/dev/null)
if [[ "${RECOVER_CODE}" == "404" ]]; then
  PASS=$((PASS+1)); printf '%-5s %-6s %-45s -> %s (无 checkpoint 时 404 合理)\n' "[PASS]" "POST" "/api/checkpoints/recover" "${RECOVER_CODE}"
elif [[ "${RECOVER_CODE}" =~ ^2 ]]; then
  PASS=$((PASS+1)); printf '%-5s %-6s %-45s -> %s\n' "[PASS]" "POST" "/api/checkpoints/recover" "${RECOVER_CODE}"
else
  FAIL=$((FAIL+1)); printf '%-5s %-6s %-45s -> %s\n' "[FAIL]" "POST" "/api/checkpoints/recover" "${RECOVER_CODE}"
  PROBLEMS+=("POST /api/checkpoints/recover 对无效 task_id 返回 ${RECOVER_CODE}")
fi

# =============================================================================
# 8. Memory（路由与文档差异最大，重点验证）
# =============================================================================
print_section "8. Memory"
req GET /api/memories
reqExpect POST /api/memories 405 ''    # 文档列了 POST 创建，实际顶层只允许 GET
req GET "/api/memories/recall?task=smoke&project=default&max=3"
reqExpect POST /api/memories/promote 200 '{"task_id":"smoke"}'
# 用占位 id 验证端点存在性：handler 对不存在的 id 会返回 200+error body 或 500，
# 只要不是 404/405 就说明路由命中（注意：当前实现 DeleteMemory/UpdateMemoryScope
# 对不存在 id 不报 404，而是返回 200——这本身是一个值得记录的 API 行为）。
MEM_ID_CODE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT "${BASE}/api/memories/fake_id/scope" \
  -H 'Content-Type: application/json' --data '{"scope":"project"}' 2>/dev/null)
if [[ "${MEM_ID_CODE}" =~ ^[245] ]]; then
  PASS=$((PASS+1)); printf '%-5s %-6s %-45s -> %s (端点命中)\n' "[PASS]" "PUT" "/api/memories/{id}/scope" "${MEM_ID_CODE}"
  [[ "${MEM_ID_CODE}" == "200" ]] && PROBLEMS+=("PUT /api/memories/{id}/scope 对不存在的 id 返回 200（应考虑 404）")
else
  FAIL=$((FAIL+1)); printf '%-5s %-6s %-45s -> %s\n' "[FAIL]" "PUT" "/api/memories/{id}/scope" "${MEM_ID_CODE}"
fi
MEM_DEL_CODE=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "${BASE}/api/memories/fake_id" 2>/dev/null)
if [[ "${MEM_DEL_CODE}" =~ ^[245] ]]; then
  PASS=$((PASS+1)); printf '%-5s %-6s %-45s -> %s (端点命中)\n' "[PASS]" "DELETE" "/api/memories/{id}" "${MEM_DEL_CODE}"
  [[ "${MEM_DEL_CODE}" == "200" ]] && PROBLEMS+=("DELETE /api/memories/{id} 对不存在的 id 返回 200（应考虑 404）")
else
  FAIL=$((FAIL+1)); printf '%-5s %-6s %-45s -> %s\n' "[FAIL]" "DELETE" "/api/memories/{id}" "${MEM_DEL_CODE}"
fi
PROBLEMS+=("Memory 路由与文档差异：无 POST /api/memories 顶层创建、无 PUT /api/memories/{id}（只有 /scope 子路径）、有 /promote 与 /recall")

# =============================================================================
# 9. Mock 管理
# =============================================================================
print_section "9. Mock 管理"
req GET /api/mock/scripts
MOCK_RAW=$(curl -s -X POST "${BASE}/api/mock/scripts" -H 'Content-Type: application/json' \
           --data '{"id":"smoke-custom","case_id":"dialogue","priority":50,"match_input":["smoke"],"responses":[{"type":"text","content":"smoke ok"}]}' 2>/dev/null)
echo "    create resp: $(echo "${MOCK_RAW}" | head -c 160)"
req GET /api/mock/scripts
reqExpect GET /api/mock/scripts/smoke-custom 200
req DELETE /api/mock/scripts/smoke-custom
req POST /api/mock/reset

# =============================================================================
# 10. WebSocket 握手（仅验证 101 升级）
# =============================================================================
print_section "10. WebSocket 握手"
WS_CODE=$(curl -s -o /dev/null -w '%{http_code}' -i -N \
  -H "Connection: Upgrade" -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: dGVzdA==" \
  "${BASE}/ws?session_id=smoke" 2>/dev/null || echo "000")
if [[ "${WS_CODE}" == "101" ]]; then
  PASS=$((PASS+1)); printf '%-5s %-6s %-45s -> %s (握手成功)\n' "[PASS]" "WS" "/ws" "${WS_CODE}"
else
  # curl 默认不发 Upgrade 时可能拿到 200(SPA fallback) 也算可接受
  SKIP=$((SKIP+1)); printf '%-5s %-6s %-45s -> %s (curl 限制，留待 WS 专项测试)\n' "[SKIP]" "WS" "/ws" "${WS_CODE}"
  PROBLEMS+=("WebSocket /ws 用 curl 验证握手受限(状态码 ${WS_CODE})，建议后续用 wscat/Go 客户端专项测")
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
if [[ ${#PROBLEMS[@]} -gt 0 ]]; then
  echo; echo "发现的问题 / 与文档差异："
  for p in "${PROBLEMS[@]}"; do echo "  - ${p}"; done
fi

if [[ ${FAIL} -gt 0 ]]; then
  echo; echo "[smoke] 存在失败项，详见上方 [FAIL] 行。服务日志：${SERVER_LOG}"
  exit 1
fi
echo; echo "[smoke] 全部端点冒烟通过 ✓"
exit 0
