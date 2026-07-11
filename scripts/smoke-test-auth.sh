#!/usr/bin/env bash
# =============================================================================
# Multi-Agent Platform — Auth-on 模式冒烟测试脚本
# =============================================================================
# 在 REQUIRE_AUTH=true 模式下，对全部 HTTP REST 端点逐一发请求，
# 使用 Bearer token（从启动日志提取 DEFAULT ADMIN API KEY）认证。
# 独立端口 18081 + 独立临时 DB，不污染主脚本。
#
# 用法：REQUIRE_AUTH=true bash scripts/smoke-test-auth.sh
#
# 从 scripts/smoke-test.sh 衍生而来，核心改动：
#   1. 固定 REQUIRE_AUTH=true
#   2. 自动从日志提取 Bearer token
#   3. 所有 POST/PUT/DELETE 请求附加 Authorization Bearer header
#   4. GET 请求不加 header（GET 在 auth_http.go 中豁免）
# =============================================================================
set -u

# ---- 配置 -------------------------------------------------------------------
PORT=18081
BASE="http://localhost:${PORT}"
DB_PATH="/tmp/smoke-auth-$$.db"
SERVER_BIN="/tmp/smoke-auth-server-$$"
SERVER_LOG="/tmp/smoke-auth-server-$$.log"
SERVER_PID=""
PASS=0
FAIL=0
SKIP=0
PROBLEMS=()
ADMIN_KEY=""

# 清理函数
cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null
    wait "${SERVER_PID}" 2>/dev/null
  fi
  rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" 2>/dev/null
  rm -f "${DB_PATH}-wal" "${DB_PATH}-shm" 2>/dev/null
}
trap cleanup EXIT

# ---- 辅助函数 ---------------------------------------------------------------

# 带认证的 POST/PUT/DELETE 请求
# auth_req <METHOD> <path> [data_json]  -> 打印 PASS/FAIL 行，返回 status
auth_req() {
  local method="$1" path="$2" data="${3:-}"
  local code body
  if [[ -n "${data}" ]]; then
    code=$(curl -s -o /tmp/smoke-auth-res-$$ -w '%{http_code}' \
            -X "${method}" "${BASE}${path}" \
            -H 'Content-Type: application/json' \
            2>/dev/null)
  else
    code=$(curl -s -o /tmp/smoke-auth-res-$$ -w '%{http_code}' \
            -X "${method}" "${BASE}${path}" \
            2>/dev/null)
  fi
  body=$(cat /tmp/smoke-auth-res-$$ 2>/dev/null | head -c 200)
  rm -f /tmp/smoke-auth-res-$$
  local mark="PASS"
  if [[ "${code}" =~ ^2 ]]; then
    PASS=$((PASS+1)); mark="PASS"
  elif [[ "${code}" == "405" ]]; then
    PASS=$((PASS+1)); mark="PASS*"
    PROBLEMS+=("[405] ${method} ${path}")
  else
    FAIL=$((FAIL+1)); mark="FAIL"
  fi
  printf '%-5s %-6s %-45s -> %s | %s\n' "[${mark}]" "${method}" "${path}" "${code}" "${body}"
  echo "${code}"
}

# 无认证的 GET 请求（GET 豁免）
# get_req <METHOD> <path>  -> 打印 PASS/FAIL 行，返回 status
get_req() {
  local method="$1" path="$2"
  local code body
  code=$(curl -s -o /tmp/smoke-auth-res-$$ -w '%{http_code}' \
         -X "${method}" "${BASE}${path}" 2>/dev/null)
  body=$(cat /tmp/smoke-auth-res-$$ 2>/dev/null | head -c 200)
  rm -f /tmp/smoke-auth-res-$$
  local mark="PASS"
  if [[ "${code}" =~ ^2 ]]; then
    PASS=$((PASS+1)); mark="PASS"
  elif [[ "${code}" == "405" ]]; then
    PASS=$((PASS+1)); mark="PASS*"
    PROBLEMS+=("[405] ${method} ${path}")
  else
    FAIL=$((FAIL+1)); mark="FAIL"
  fi
  printf '%-5s %-6s %-45s -> %s | %s\n' "[${mark}]" "${method}" "${path}" "${code}" "${body}"
  echo "${code}"
}

# 期望特定状态码
# reqExpect <METHOD> <path> <expect_code> [data]
reqExpect() {
  local method="$1" path="$2" expect="$3" data="${4:-}"
  local code
  if [[ -n "${data}" ]]; then
    code=$(curl -s -o /tmp/smoke-auth-res-$$ -w '%{http_code}' \
            -X "${method}" "${BASE}${path}" \
            -H 'Content-Type: application/json' \
            2>/dev/null)
  else
    code=$(curl -s -o /tmp/smoke-auth-res-$$ -w '%{http_code}' \
            -X "${method}" "${BASE}${path}" 2>/dev/null)
  fi
  local body
  body=$(cat /tmp/smoke-auth-res-$$ 2>/dev/null | head -c 200); rm -f /tmp/smoke-auth-res-$$
  if [[ "${code}" == "${expect}" ]]; then
    PASS=$((PASS+1)); printf '%-5s %-6s %-45s -> %s (expect %s)\n' "[PASS]" "${method}" "${path}" "${code}" "${expect}"
  else
    FAIL=$((FAIL+1)); printf '%-5s %-6s %-45s -> %s (expect %s) | %s\n' "[FAIL]" "${method}" "${path}" "${code}" "${expect}" "${body}"
  fi
  echo "${code}"
}

# JSON 字段提取
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
  echo "[FATAL] 编译失败"; cat "${SERVER_LOG}"; exit 2
fi
echo "[setup] 编译成功"

# ---- 启动服务（REQUIRE_AUTH=true）----------------------------------------------
echo "[setup] 启动服务 (port=${PORT}, DB=${DB_PATH}, REQUIRE_AUTH=true)..."
LLM_USE_MOCK=true \
REQUIRE_AUTH=true \
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
  echo "[FATAL] 服务 30s 内未就绪"; tail -30 "${SERVER_LOG}"; exit 3
fi
echo "[setup] 服务就绪 ✓"

# ---- 提取 Bearer Token -------------------------------------------------------
echo "[setup] 从启动日志提取 DEFAULT ADMIN API KEY..."
ADMIN_KEY=$(grep "DEFAULT ADMIN API KEY" "${SERVER_LOG}" | head -1 \
  | sed -E 's/.*DEFAULT ADMIN API KEY:[[:space:]]*(sk_[A-Za-z0-9_=-]+).*/\1/')
if [[ -z "${ADMIN_KEY}" ]]; then
  echo "[FATAL] 未能提取默认 admin API key"
  echo "--- 日志前 40 行 ---"; head -40 "${SERVER_LOG}"
  exit 4
fi
echo "[setup] 抓取到 admin key (长度=${#ADMIN_KEY})"
if grep -q "Auth:.*enabled" "${SERVER_LOG}"; then
  echo "[setup] 启动日志确认 Auth: enabled"
else
  echo "[WARN] 未发现 Auth: enabled 标记"
fi

# =============================================================================
# 测试开始
# =============================================================================

# -----------------------------------------------------------------------------
# 0. 无 token 访问受保护写端点（验证 auth 生效）
# -----------------------------------------------------------------------------
print_section "0. 无 token 访问写端点 (期望 401)"

CODE=$(curl -s -o /dev/null -w '%{http_code}' \
  -X POST "${BASE}/api/tasks" \
  -H 'Content-Type: application/json' \
  --data '{"action":"chat","input":"hi","max_steps":1}' 2>/dev/null)
if [[ "${CODE}" == "401" ]]; then
  PASS=$((PASS+1)); echo "[PASS] POST /api/tasks 无 token → 401"
else
  FAIL=$((FAIL+1)); echo "[FAIL] 期望 401，实际 ${CODE}"
fi

# -----------------------------------------------------------------------------
# 1. 基础 / 观测（GET 豁免）
# -----------------------------------------------------------------------------
print_section "1. 基础 / 观测 (GET 豁免)"
get_req GET /healthz
get_req GET /metrics
get_req GET /api/version
get_req GET /health

# -----------------------------------------------------------------------------
# 2. Auth（带 token）
# -----------------------------------------------------------------------------
print_section "2. Auth (带 Bearer token)"
# 获取 Auth header
AUTH_H=(-H "Authorization: Bearer ${ADMIN_KEY}")

RESP=$(curl -s -X POST "${BASE}/api/auth/api-keys" \
       -H 'Content-Type: application/json' \
       "${AUTH_H[@]}" \
       --data '{"name":"smoke-auth-key"}' 2>/dev/null)
echo "    create resp: $(echo "${RESP}" | head -c 120)"
AUTH_ID=$(jget "${RESP}" "id")
if [[ -n "${AUTH_ID}" ]]; then
  code=$(curl -s -o /dev/null -w '%{http_code}' \
    -X DELETE "${BASE}/api/auth/api-keys/${AUTH_ID}" \
    "${AUTH_H[@]}" 2>/dev/null)
  if [[ "${code}" =~ ^2 ]]; then
    PASS=$((PASS+1)); echo "[PASS] DELETE /api/auth/api-keys/${AUTH_ID}"
  else
    FAIL=$((FAIL+1)); echo "[FAIL] DELETE key → ${code}"
  fi
else
  FAIL=$((FAIL+1)); echo "[FAIL] 创建 key 未返回 id"
fi

# -----------------------------------------------------------------------------
# 3. Project
# -----------------------------------------------------------------------------
print_section "3. Project (带 token)"
get_req GET /api/projects

PROJ_RESP=$(curl -s -X POST "${BASE}/api/projects" \
            -H 'Content-Type: application/json' \
            "${AUTH_H[@]}" \
            --data '{"name":"smoke-auth-proj","description":"auth test"}' 2>/dev/null)
PROJ_ID=$(jget "${PROJ_RESP}" "id")
if [[ -n "${PROJ_ID}" ]]; then
  get_req GET "/api/projects/${PROJ_ID}"
  code=$(curl -s -o /dev/null -w '%{http_code}' \
    -X PUT "${BASE}/api/projects/${PROJ_ID}" \
    -H 'Content-Type: application/json' \
    "${AUTH_H[@]}" \
    --data '{"name":"renamed-auth"}' 2>/dev/null)
  [[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))
  code=$(curl -s -o /dev/null -w '%{http_code}' \
    -X DELETE "${BASE}/api/projects/${PROJ_ID}" \
    "${AUTH_H[@]}" 2>/dev/null)
  [[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))
else
  FAIL=$((FAIL+1)); echo "[FAIL] 创建 project 未返回 id"
fi

# -----------------------------------------------------------------------------
# 4. Session
# -----------------------------------------------------------------------------
print_section "4. Session (带 token)"
get_req GET /api/sessions

SESS_RESP=$(curl -s -X POST "${BASE}/api/sessions" \
            -H 'Content-Type: application/json' \
            "${AUTH_H[@]}" \
            --data '{"user_input":"auth smoke test","project_id":"default"}' 2>/dev/null)
SESS_ID=$(jget "${SESS_RESP}" "session_id" "id")
if [[ -n "${SESS_ID}" ]]; then
  get_req GET "/api/sessions/${SESS_ID}"
  get_req GET "/api/sessions/${SESS_ID}/messages"
  # 发一次 chat
  curl -s -X POST "${BASE}/api/sessions/${SESS_ID}/chat" \
    -H 'Content-Type: application/json' \
    "${AUTH_H[@]}" \
    --data '{"input":"auth test chat","max_steps":2}' > /dev/null 2>&1
  sleep 1
  get_req GET "/api/sessions/${SESS_ID}/messages"
  code=$(curl -s -o /dev/null -w '%{http_code}' \
    -X DELETE "${BASE}/api/sessions/${SESS_ID}" \
    "${AUTH_H[@]}" 2>/dev/null)
  [[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))
else
  FAIL=$((FAIL+1)); echo "[FAIL] 创建 session 未返回 id"
fi

# -----------------------------------------------------------------------------
# 5. Agent
# -----------------------------------------------------------------------------
print_section "5. Agent (带 token)"
get_req GET /api/agents

AGENT_RESP=$(curl -s -X POST "${BASE}/api/agents" \
             -H 'Content-Type: application/json' \
             "${AUTH_H[@]}" \
             --data '{"id":"agent_auth_smoke","name":"Auth Smoke","system_prompt":"test"}' 2>/dev/null)
AGENT_ID=$(jget "${AGENT_RESP}" "id")
if [[ -n "${AGENT_ID}" ]]; then
  get_req GET "/api/agents/${AGENT_ID}"
  code=$(curl -s -o /dev/null -w '%{http_code}' \
    -X PUT "${BASE}/api/agents/${AGENT_ID}" \
    -H 'Content-Type: application/json' \
    "${AUTH_H[@]}" \
    --data '{"name":"Renamed"}' 2>/dev/null)
  [[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))
  code=$(curl -s -o /dev/null -w '%{http_code}' \
    -X DELETE "${BASE}/api/agents/${AGENT_ID}" \
    "${AUTH_H[@]}" 2>/dev/null)
  [[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))
else
  FAIL=$((FAIL+1)); echo "[FAIL] 创建 agent 未返回 id"
fi

# -----------------------------------------------------------------------------
# 6. Task
# -----------------------------------------------------------------------------
print_section "6. Task (带 token)"
get_req GET /api/tasks

code=$(curl -s -o /dev/null -w '%{http_code}' \
  -X POST "${BASE}/api/tasks?case=dialogue" \
  -H 'Content-Type: application/json' \
  "${AUTH_H[@]}" \
  --data '{"action":"chat","input":"auth dialogue test","agent_id":"agent_auth_smoke","max_steps":3}' 2>/dev/null)
[[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))
sleep 2

# multi-agent
code=$(curl -s -o /dev/null -w '%{http_code}' \
  -X POST "${BASE}/api/tasks" \
  -H 'Content-Type: application/json' \
  "${AUTH_H[@]}" \
  --data '{"action":"multi-agent","input":"auth multi test","case_type":"multi_agent","max_steps":3}' 2>/dev/null)
[[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))

# -----------------------------------------------------------------------------
# 7. Tool / Cases / Cost / Checkpoints
# -----------------------------------------------------------------------------
print_section "7. Tool / Cases / Cost / Checkpoints"
get_req GET /api/tools
get_req GET /api/cases

# POST 创建 tool
code=$(curl -s -o /dev/null -w '%{http_code}' \
  -X POST "${BASE}/api/tools" \
  -H 'Content-Type: application/json' \
  "${AUTH_H[@]}" \
  --data '{"name":"auth_echo","type":"shell","command":"echo auth_ok","description":"auth test"}' 2>/dev/null)
[[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))

# DELETE tool
code=$(curl -s -o /dev/null -w '%{http_code}' \
  -X DELETE "${BASE}/api/tools?name=auth_echo" \
  "${AUTH_H[@]}" 2>/dev/null)
[[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))

# Costs
get_req GET /api/costs
# Checkpoints
get_req GET /api/checkpoints
# Recover (no checkpoint)
code=$(curl -s -o /dev/null -w '%{http_code}' \
  -X POST "${BASE}/api/checkpoints/recover" \
  -H 'Content-Type: application/json' \
  "${AUTH_H[@]}" \
  --data '{"task_id":"nonexistent_auth"}' 2>/dev/null)
[[ "${code}" =~ ^[245] ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))

# -----------------------------------------------------------------------------
# 8. Memory
# -----------------------------------------------------------------------------
print_section "8. Memory (带 token)"
get_req GET /api/memories
get_req GET "/api/memories/recall?task=auth&project=default&max=3"

code=$(curl -s -o /dev/null -w '%{http_code}' \
  -X POST "${BASE}/api/memories/promote" \
  -H 'Content-Type: application/json' \
  "${AUTH_H[@]}" \
  --data '{"task_id":"smoke"}' 2>/dev/null)
[[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))

# 不存在的 memory id 操作（应 404 而非 200）
code=$(curl -s -o /dev/null -w '%{http_code}' \
  -X PUT "${BASE}/api/memories/fake_auth_id/scope" \
  -H 'Content-Type: application/json' \
  "${AUTH_H[@]}" \
  --data '{"scope":"project"}' 2>/dev/null)
if [[ "${code}" == "404" ]]; then
  PASS=$((PASS+1)); echo "[PASS] PUT /api/memories/{id}/scope 不存在 → 404"
else
  FAIL=$((FAIL+1)); echo "[FAIL] 期望 404，实际 ${code}"
fi

code=$(curl -s -o /dev/null -w '%{http_code}' \
  -X DELETE "${BASE}/api/memories/fake_auth_id" \
  "${AUTH_H[@]}" 2>/dev/null)
if [[ "${code}" == "404" ]]; then
  PASS=$((PASS+1)); echo "[PASS] DELETE /api/memories/{id} 不存在 → 404"
else
  FAIL=$((FAIL+1)); echo "[FAIL] 期望 404，实际 ${code}"
fi

# -----------------------------------------------------------------------------
# 9. Mock 管理
# -----------------------------------------------------------------------------
print_section "9. Mock (带 token)"
get_req GET /api/mock/scripts

MOCK_RESP=$(curl -s -X POST "${BASE}/api/mock/scripts" \
            -H 'Content-Type: application/json' \
            "${AUTH_H[@]}" \
            --data '{"id":"auth-smoke","case_id":"dialogue","priority":50,"match_input":["auth"],"responses":[{"type":"text","content":"auth ok"}]}' 2>/dev/null)
echo "    create resp: $(echo "${MOCK_RESP}" | head -c 120)"

get_req GET /api/mock/scripts
get_req GET "/api/mock/scripts/auth-smoke"
code=$(curl -s -o /dev/null -w '%{http_code}' \
  -X DELETE "${BASE}/api/mock/scripts/auth-smoke" \
  "${AUTH_H[@]}" 2>/dev/null)
[[ "${code}" =~ ^2 ]] && PASS=$((PASS+1)) || FAIL=$((FAIL+1))

get_req POST "/api/mock/reset"

# -----------------------------------------------------------------------------
# 汇总
# -----------------------------------------------------------------------------
print_section "汇总"
echo "----------------------------------------"
echo "  PASS : ${PASS}"
echo "  FAIL : ${FAIL}"
echo "  SKIP : ${SKIP}"
echo "----------------------------------------"
if [[ ${#PROBLEMS[@]} -gt 0 ]]; then
  echo; echo "发现的问题："
  for p in "${PROBLEMS[@]}"; do echo "  - ${p}"; done
fi

if [[ ${FAIL} -gt 0 ]]; then
  echo; echo "[smoke-auth] 存在失败项，详见上方 [FAIL] 行"
  exit 1
fi
echo; echo "[smoke-auth] 全部端点冒烟通过 ✓"
exit 0
