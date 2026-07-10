#!/usr/bin/env bash
# =============================================================================
# Multi-Agent Platform — Auth 开启模式全链路评测脚本 (维度 D)
# =============================================================================
# 评测 REQUIRE_AUTH=true 下的 Bearer token 全链路：
#   - 受保护写端点无 token → 401
#   - GET 端点一律豁免（实现偏差记录）
#   - admin token 创建/吊销新 key 的完整生命周期
#   - 已吊销 key 再次访问 → 401
#   - 健康检查类端点无 token 可访问
#   - role 校验机制存在性确认
#
# 独立端口 18104 + 独立临时 DB，不污染仓库环境。
# 约束: 只读后端源码，不改后端；仅本脚本可自由调整。
# =============================================================================
set -u

# ---- 配置 -------------------------------------------------------------------
PORT=18104
BASE="http://localhost:${PORT}"
DB_PATH="/tmp/auth-smoke-$$.db"
SERVER_BIN="/tmp/auth-smoke-server-$$.exe"
SERVER_LOG="/tmp/auth-smoke-server-$$.log"
SERVER_PID=""
ADMIN_KEY=""
PASS=0; FAIL=0; SKIP=0
RESULTS=()      # 测试结果数组
FINDINGS=()     # 发现的后端 auth bug / 缺口
DEVIATIONS=()   # 与文档/预期的偏差

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null
    wait "${SERVER_PID}" 2>/dev/null
  fi
  rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" 2>/dev/null
  # SQLite 可能产生 -wal / -shm 文件
  rm -f "${DB_PATH}-wal" "${DB_PATH}-shm" 2>/dev/null
}
trap cleanup EXIT

# ---- 辅助函数 ---------------------------------------------------------------

print_section() { echo; echo "===== $1 ====="; }

# 记录结果: record_result <name> <PASS|FAIL|SKIP> <evidence>
record_result() {
  local name="$1" result="$2" evidence="$3"
  RESULTS+=("[${result}] ${name}: ${evidence}")
  case "$result" in
    PASS) PASS=$((PASS+1)) ;;
    FAIL) FAIL=$((FAIL+1)) ;;
    SKIP) SKIP=$((SKIP+1)) ;;
  esac
  printf '%-6s %-40s %s\n' "[${result}]" "$name" "$evidence"
}

# 从 JSON 响应中提取字符串字段值（grep + sed，无 jq 依赖）
# jget <json> <key>  →  value
jget() {
  local json="$1" key="$2"
  echo "$json" | grep -o "\"${key}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 \
    | sed -E "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"([^\"]*)\".*/\1/"
}

# HTTP 请求只取状态码
status_of() {
  curl -s -o /dev/null -w '%{http_code}' "$@" 2>/dev/null || echo "000"
}

# ---- 编译服务 ---------------------------------------------------------------
echo "[setup] 编译后端服务..."
if ! go build -o "${SERVER_BIN}" ./cmd/server 2>"${SERVER_LOG}"; then
  echo "[FATAL] 编译失败，日志见 ${SERVER_LOG}"
  cat "${SERVER_LOG}"
  exit 2
fi
echo "[setup] 编译成功"

# ---- 启动服务 ---------------------------------------------------------------
echo "[setup] 启动服务 (port=${PORT}, DB=${DB_PATH}, LLM_USE_MOCK=true, REQUIRE_AUTH=true)..."
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
  if [[ "$code" == "200" ]]; then ready=1; break; fi
  sleep 0.5
done
if [[ "$ready" != "1" ]]; then
  echo "[FATAL] 服务 30s 内未就绪。服务日志："
  tail -40 "${SERVER_LOG}"
  exit 3
fi
echo "[setup] 服务就绪"

# ---- 抓取 DEFAULT ADMIN API KEY --------------------------------------------
print_section "抓取启动日志中的 DEFAULT ADMIN API KEY"
# 日志格式: "DEFAULT ADMIN API KEY: sk_xxxx"
ADMIN_KEY=$(grep "DEFAULT ADMIN API KEY" "${SERVER_LOG}" | head -1 \
  | sed -E 's/.*DEFAULT ADMIN API KEY:[[:space:]]*(sk_[A-Za-z0-9_-]+).*/\1/')
if [[ -z "${ADMIN_KEY}" ]]; then
  echo "[FATAL] 未能从启动日志抓取 DEFAULT ADMIN API KEY"
  echo "--- 服务日志 (前 60 行) ---"
  head -60 "${SERVER_LOG}"
  exit 4
fi
echo "[setup] 抓取到 admin key: ${ADMIN_KEY} (长度=${#ADMIN_KEY})"

# 校验 key 格式: sk_ + 43 base64url = 46 字符
if [[ ${#ADMIN_KEY} -ne 46 || "${ADMIN_KEY:0:3}" != "sk_" ]]; then
  echo "[WARN] admin key 格式异常 (期望 sk_+43=46 字符, 实际=${#ADMIN_KEY})"
  DEVIATIONS+=("admin key 长度=${#ADMIN_KEY}, 期望 46")
fi

# 确认启动日志中 Auth 模式标记
if grep -q "Auth:.*enabled" "${SERVER_LOG}"; then
  echo "[setup] 启动日志确认 Auth: enabled"
else
  echo "[WARN] 启动日志未发现 'Auth: enabled' 标记"
  grep -i "Auth:" "${SERVER_LOG}" | head -3
fi

# =============================================================================
# 测试矩阵
# =============================================================================
print_section "测试矩阵"

# ---- a. 无 token 访问受保护写端点 (POST /api/tasks) → 期望 401 ----
echo ""
echo "--- a. 无 token POST /api/tasks (受保护写) → 期望 401 ---"
CODE_A=$(status_of -X POST "${BASE}/api/tasks" \
  -H 'Content-Type: application/json' \
  --data '{"action":"chat","input":"hi","max_steps":1}')
echo "  实际状态码: ${CODE_A}"
if [[ "$CODE_A" == "401" ]]; then
  record_result "a. 无 token 受保护写 → 401" "PASS" "POST /api/tasks 无 token 返回 401"
else
  record_result "a. 无 token 受保护写 → 401" "FAIL" "期望 401, 实际 ${CODE_A}"
  FINDINGS+=("[严重] REQUIRE_AUTH=true 下 POST /api/tasks 无 token 未返回 401, 实际 ${CODE_A}")
fi

# ---- a2. 无 token GET /api/tasks → 期望 200 (实现偏差：GET 一律豁免) ----
echo ""
echo "--- a2. 无 token GET /api/tasks → 实际应为 200 (GET 一律豁免) ---"
CODE_A2=$(status_of "${BASE}/api/tasks")
echo "  实际状态码: ${CODE_A2}"
# 代码 auth_http.go:91 — `if !requiresAuth || r.Method == http.MethodGet` 直接放行
if [[ "$CODE_A2" == "200" ]]; then
  record_result "a2. 无 token GET /api/tasks → 200" "PASS" "GET 方法一律豁免 (auth_http.go:91), 返回 200"
  DEVIATIONS+=("GET 请求一律豁免 auth: 无 token GET /api/tasks 返回 200 (任务描述预期部分敏感读需 auth, 实现是全部 GET 豁免)")
else
  record_result "a2. 无 token GET /api/tasks → 200" "FAIL" "期望 200, 实际 ${CODE_A2}"
fi

# ---- a3. 无 token GET /api/auth/api-keys (列出 key) → 期望 200 (敏感读豁免偏差) ----
echo ""
echo "--- a3. 无 token GET /api/auth/api-keys (列出 API key) → 期望 200 (敏感读豁免偏差) ---"
CODE_A3=$(status_of "${BASE}/api/auth/api-keys")
echo "  实际状态码: ${CODE_A3}"
if [[ "$CODE_A3" == "200" ]]; then
  record_result "a3. 无 token GET /api/auth/api-keys → 200" "PASS" "GET 豁免, 返回 200 (敏感读无 auth 保护)"
  DEVIATIONS+=("敏感读端点 GET /api/auth/api-keys 在 REQUIRE_AUTH=true 下无 token 可访问, 列出所有 key 元数据 (prefix/id/name). 属于设计选择但与'部分敏感读需 auth'的预期不符")
  FINDINGS+=("[中危] GET /api/auth/api-keys 无 token 可列出所有 API key 元数据 (prefix/id/name/created_at), 仅不含 raw key. 但 prefix 暴露可降低离线碰撞成本. 无 role/owner 校验")
else
  record_result "a3. 无 token GET /api/auth/api-keys → 200" "FAIL" "期望 200, 实际 ${CODE_A3}"
fi

# ---- b. 带 admin token 访问受保护写端点 → 期望非 401 (证明 token 通过) ----
echo ""
echo "--- b. admin token POST /api/tasks (action=chat, input='') → 期望 400 (token 通过, handler 拒绝空 input) ---"
CODE_B=$(status_of -X POST "${BASE}/api/tasks" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer ${ADMIN_KEY}" \
  --data '{"action":"chat","input":"","max_steps":1}')
echo "  实际状态码: ${CODE_B}"
# token 通过中间件后, handler 检测 input 为空返回 400. 400 != 401 证明 token 有效.
if [[ "$CODE_B" == "401" ]]; then
  record_result "b. admin token 受保护写 → 非 401" "FAIL" "admin token 仍返回 401, token 校验未通过"
  FINDINGS+=("[严重] admin token (启动日志打印的 DEFAULT ADMIN API KEY) 在 REQUIRE_AUTH=true 下访问 POST /api/tasks 返回 401. 默认 key 无法使用")
elif [[ "$CODE_B" == "400" || "$CODE_B" == "200" ]]; then
  record_result "b. admin token 受保护写 → 非 401" "PASS" "admin token 通过 (状态码 ${CODE_B}, 非 401) → handler 层处理请求"
else
  record_result "b. admin token 受保护写 → 非 401" "PASS" "admin token 通过 (状态码 ${CODE_B}, 非 401)"
  DEVIATIONS+=("admin token POST /api/tasks 空输入返回 ${CODE_B} 而非 400")
fi

# ---- c. admin token POST /api/auth/api-keys 创建新 key → 期望 201 + 返回新 key ----
echo ""
echo "--- c. admin token POST /api/auth/api-keys 创建新 key → 期望 201 ---"
RESP_C=$(curl -s -w '\n%{http_code}' -X POST "${BASE}/api/auth/api-keys" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer ${ADMIN_KEY}" \
  --data '{"name":"smoke-test-key"}' 2>/dev/null)
CODE_C=$(echo "$RESP_C" | tail -1)
BODY_C=$(echo "$RESP_C" | sed '$d')
echo "  状态码: ${CODE_C}"
echo "  响应体: ${BODY_C}"
NEW_KEY=$(jget "$BODY_C" "key")
NEW_KEY_ID=$(jget "$BODY_C" "id")
NEW_KEY_PREFIX=$(jget "$BODY_C" "prefix")
echo "  新 key: ${NEW_KEY} (id=${NEW_KEY_ID}, prefix=${NEW_KEY_PREFIX})"
if [[ "$CODE_C" == "201" && -n "$NEW_KEY" && -n "$NEW_KEY_ID" ]]; then
  record_result "c. admin 创建新 key → 201" "PASS" "新 key id=${NEW_KEY_ID}, prefix=${NEW_KEY_PREFIX}, 长度=${#NEW_KEY}"
  # 校验新 key 格式
  if [[ ${#NEW_KEY} -ne 46 || "${NEW_KEY:0:3}" != "sk_" ]]; then
    DEVIATIONS+=("新创建 key 长度=${#NEW_KEY}, 期望 46")
  fi
else
  record_result "c. admin 创建新 key → 201" "FAIL" "期望 201 + key, 实际 ${CODE_C}, body=${BODY_C}"
  FINDINGS+=("[严重] admin token POST /api/auth/api-keys 未返回 201/key, 实际 ${CODE_C}: ${BODY_C}")
fi

# ---- d. 用新 key 访问受保护写端点 → 期望非 401 ----
echo ""
echo "--- d. 新 key POST /api/tasks (action=chat, input='') → 期望非 401 (新 key 可用) ---"
if [[ -z "$NEW_KEY" ]]; then
  record_result "d. 新 key 受保护写 → 非 401" "SKIP" "前一步未取得新 key, 跳过"
else
  CODE_D=$(status_of -X POST "${BASE}/api/tasks" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer ${NEW_KEY}" \
    --data '{"action":"chat","input":"","max_steps":1}')
  echo "  实际状态码: ${CODE_D}"
  if [[ "$CODE_D" == "401" ]]; then
    record_result "d. 新 key 受保护写 → 非 401" "FAIL" "新 key 返回 401, 未生效"
    FINDINGS+=("[严重] 新创建的 API key 立即使用返回 401, Create/Verify 链路不一致")
  else
    record_result "d. 新 key 受保护写 → 非 401" "PASS" "新 key 通过 (状态码 ${CODE_D})"
  fi
fi

# ---- d2. 新 key 也能列出 /api/auth/api-keys (带 token GET) ----
echo ""
echo "--- d2. 新 key GET /api/auth/api-keys (带 token) → 期望 200 ---"
if [[ -z "$NEW_KEY" ]]; then
  record_result "d2. 新 key GET api-keys → 200" "SKIP" "无新 key"
else
  CODE_D2=$(status_of "${BASE}/api/auth/api-keys" -H "Authorization: Bearer ${NEW_KEY}")
  echo "  实际状态码: ${CODE_D2}"
  if [[ "$CODE_D2" == "200" ]]; then
    record_result "d2. 新 key GET api-keys → 200" "PASS" "新 key GET 通过"
  else
    record_result "d2. 新 key GET api-keys → 200" "FAIL" "期望 200, 实际 ${CODE_D2}"
  fi
fi

# ---- e. admin token DELETE /api/auth/api-keys/<新key id> 吊销新 key → 期望 200 ----
echo ""
echo "--- e. admin token DELETE /api/auth/api-keys/<新key id> → 期望 200 ---"
if [[ -z "$NEW_KEY_ID" ]]; then
  record_result "e. 吊销新 key → 200" "SKIP" "无新 key id"
else
  CODE_E=$(status_of -X DELETE "${BASE}/api/auth/api-keys/${NEW_KEY_ID}" \
    -H "Authorization: Bearer ${ADMIN_KEY}")
  echo "  实际状态码: ${CODE_E}"
  if [[ "$CODE_E" == "200" || "$CODE_E" == "204" ]]; then
    record_result "e. 吊销新 key → 200/204" "PASS" "DELETE 返回 ${CODE_E}"
  else
    record_result "e. 吊销新 key → 200/204" "FAIL" "期望 200/204, 实际 ${CODE_E}"
    FINDINGS+=("[中危] DELETE /api/auth/api-keys/<id> 未返回 200/204, 实际 ${CODE_E}")
  fi
fi

# ---- f. 用已吊销的新 key 再访问受保护端点 → 期望 401 ----
echo ""
echo "--- f. 已吊销的新 key POST /api/tasks → 期望 401 ---"
if [[ -z "$NEW_KEY" ]]; then
  record_result "f. 已吊销 key → 401" "SKIP" "无新 key"
else
  CODE_F=$(status_of -X POST "${BASE}/api/tasks" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer ${NEW_KEY}" \
    --data '{"action":"chat","input":"","max_steps":1}')
  echo "  实际状态码: ${CODE_F}"
  if [[ "$CODE_F" == "401" ]]; then
    record_result "f. 已吊销 key → 401" "PASS" "吊销后访问返回 401"
  else
    record_result "f. 已吊销 key → 401" "FAIL" "期望 401, 实际 ${CODE_F} (吊销未生效)"
    FINDINGS+=("[严重] 已吊销的 API key 仍可访问受保护端点, 返回 ${CODE_F}. 吊销机制未生效")
  fi
fi

# ---- f2. 已吊销 key 的 GET 仍豁免 (再次确认 GET 豁免偏差) ----
echo ""
echo "--- f2. 已吊销的新 key GET /api/tasks → 期望 200 (GET 一律豁免, 与吊销无关) ---"
if [[ -z "$NEW_KEY" ]]; then
  record_result "f2. 已吊销 key GET → 200" "SKIP" "无新 key"
else
  CODE_F2=$(status_of "${BASE}/api/tasks" -H "Authorization: Bearer ${NEW_KEY}")
  echo "  实际状态码: ${CODE_F2}"
  if [[ "$CODE_F2" == "200" ]]; then
    record_result "f2. 已吊销 key GET → 200" "PASS" "GET 豁免与 token 吊销状态无关, 返回 200 (设计偏差)"
  else
    record_result "f2. 已吊销 key GET → 200" "FAIL" "期望 200, 实际 ${CODE_F2}"
  fi
fi

# ---- g. 健康检查类端点无 token 可访问 → 期望 200 ----
echo ""
echo "--- g. 健康检查类端点 (无 token) → 期望 200 ---"
CODE_G1=$(status_of "${BASE}/healthz")
CODE_G2=$(status_of "${BASE}/api/version")
CODE_G3=$(status_of "${BASE}/health")
echo "  /healthz=${CODE_G1}, /api/version=${CODE_G2}, /health=${CODE_G3}"
if [[ "$CODE_G1" == "200" && "$CODE_G2" == "200" && "$CODE_G3" == "200" ]]; then
  record_result "g. healthz/version/health 无 token → 200" "PASS" "三个端点均 200"
else
  record_result "g. healthz/version/health 无 token → 200" "FAIL" "/healthz=${CODE_G1}, /api/version=${CODE_G2}, /health=${CODE_G3}"
fi

# ---- g2. 其它读端点无 token 也可访问 (GET 豁免面) ----
echo ""
echo "--- g2. 其它 GET 读端点无 token 可访问 (确认 GET 豁免面) ---"
CODE_G2A=$(status_of "${BASE}/api/agents")
CODE_G2B=$(status_of "${BASE}/api/cases")
CODE_G2C=$(status_of "${BASE}/api/costs")
CODE_G2D=$(status_of "${BASE}/metrics")
CODE_G2E=$(status_of "${BASE}/api/memories")
CODE_G2F=$(status_of "${BASE}/api/checkpoints")
echo "  /api/agents=${CODE_G2A}, /api/cases=${CODE_G2B}, /api/costs=${CODE_G2C}, /metrics=${CODE_G2D}, /api/memories=${CODE_G2E}, /api/checkpoints=${CODE_G2F}"
EXPOSED=""
for pair in "/api/agents:${CODE_G2A}" "/api/cases:${CODE_G2B}" "/api/costs:${CODE_G2C}" "/metrics:${CODE_G2D}" "/api/memories:${CODE_G2E}" "/api/checkpoints:${CODE_G2F}"; do
  p="${pair%%:*}"; c="${pair##*:}"
  if [[ "$c" == "200" ]]; then EXPOSED="${EXPOSED} ${p}"; fi
done
if [[ -n "$EXPOSED" ]]; then
  record_result "g2. 读端点无 token 暴露面" "PASS" "GET 豁免端点:${EXPOSED} (记录为偏差/缺口)"
  DEVIATIONS+=("GET 一律豁免导致以下读端点在 REQUIRE_AUTH=true 下无 token 可访问:${EXPOSED}")
else
  record_result "g2. 读端点无 token 暴露面" "FAIL" "无端点返回 200, 异常"
fi

# ---- h. role 校验机制存在性 ----
echo ""
echo "--- h. role 校验机制存在性 ---"
echo "  代码审查: auth_http.go NewAuthMiddleware 只解出 userID, 无 role 检查;"
echo "           DefaultProtectedRoutes 无 admin 专属端点; 无 RequireAdmin/IsAdmin 调用。"
echo "  结论: 当前实现无 role-based access control, 所有有效 token 权限等同。"
record_result "h. role 校验机制" "SKIP" "代码无 role 校验逻辑, 新建 key 默认继承创建者 userID, 无 admin/user/viewer 权限区分. 记录为缺口"

# ---- i. 错误 token (非 sk_ 前缀/格式错误) → 期望 401 ----
echo ""
echo "--- i. 错误格式 token → 期望 401 ---"
CODE_I1=$(status_of -X POST "${BASE}/api/tasks" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer not_a_real_key" \
  --data '{"action":"chat","input":"","max_steps":1}')
CODE_I2=$(status_of -X POST "${BASE}/api/tasks" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer " \
  --data '{"action":"chat","input":"","max_steps":1}')
CODE_I3=$(status_of -X POST "${BASE}/api/tasks" \
  -H 'Content-Type: application/json' \
  -H "Authorization: sk_${ADMIN_KEY:3}" \
  --data '{"action":"chat","input":"","max_steps":1}')
echo "  错误 token: '${CODE_I1}', 空 Bearer: '${CODE_I2}', 缺 Bearer 前缀: '${CODE_I3}'"
if [[ "$CODE_I1" == "401" && "$CODE_I3" == "401" ]]; then
  record_result "i. 错误/缺前缀 token → 401" "PASS" "非 Bearer 与错误 key 均返回 401"
else
  record_result "i. 错误/缺前缀 token → 401" "FAIL" "i1=${CODE_I1}, i3=${CODE_I3}"
fi
# 空 Bearer (Authorization: Bearer 空字符串) — 中间件 rawKey="" → ErrInvalidKey → 401
if [[ "$CODE_I2" == "401" ]]; then
  record_result "i2. 空 Bearer token → 401" "PASS" "空 Bearer 返回 401"
else
  record_result "i2. 空 Bearer token → 401" "FAIL" "期望 401, 实际 ${CODE_I2}"
fi

# ---- j. 删除不存在的 key id → 期望 404 (ownership 检查) ----
echo ""
echo "--- j. admin token DELETE 不存在的 key id → 期望 404 ---"
CODE_J=$(status_of -X DELETE "${BASE}/api/auth/api-keys/ak_nonexistent_zzz" \
  -H "Authorization: Bearer ${ADMIN_KEY}")
echo "  实际状态码: ${CODE_J}"
if [[ "$CODE_J" == "404" ]]; then
  record_result "j. DELETE 不存在 key → 404" "PASS" "ownership 检查返回 404"
else
  record_result "j. DELETE 不存在 key → 404" "FAIL" "期望 404, 实际 ${CODE_J}"
  DEVIATIONS+=("DELETE 不存在 key id 返回 ${CODE_J} 而非 404")
fi

# ---- k. 列出 admin 自己的 key (确认 default key 在列表中) ----
echo ""
echo "--- k. admin token GET /api/auth/api-keys 确认 default key 在列表 ---"
RESP_K=$(curl -s "${BASE}/api/auth/api-keys" -H "Authorization: Bearer ${ADMIN_KEY}")
echo "  响应: ${RESP_K}"
# 列表中应有 default admin key (prefix 应匹配 ADMIN_KEY 的前 12 字符)
ADMIN_PREFIX="${ADMIN_KEY:0:12}"
if echo "$RESP_K" | grep -q "\"prefix\":\"${ADMIN_PREFIX}\""; then
  record_result "k. default admin key 在列表中" "PASS" "prefix=${ADMIN_PREFIX} 出现在 GET /api/auth/api-keys"
else
  record_result "k. default admin key 在列表中" "FAIL" "prefix=${ADMIN_PREFIX} 未出现在列表"
  FINDINGS+=("[中危] 默认 admin key 的 prefix 未出现在 GET /api/auth/api-keys 列表中, 可能 seed user 与 verify user 不一致")
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

echo ""
echo "========================================"
echo "与文档/预期偏差 (DEVIATIONS)"
echo "========================================"
if [[ ${#DEVIATIONS[@]} -eq 0 ]]; then
  echo "  (无)"
else
  for d in "${DEVIATIONS[@]}"; do
    echo "  * ${d}"
  done
fi

echo ""
echo "========================================"
echo "后端 auth bug / 缺口清单 (FINDINGS)"
echo "========================================"
if [[ ${#FINDINGS[@]} -eq 0 ]]; then
  echo "  (无)"
else
  for f in "${FINDINGS[@]}"; do
    echo "  * ${f}"
  done
fi

echo ""
echo "========================================"
echo "受保护端点清单确认 (REQUIRE_AUTH=true)"
echo "========================================"
echo "代码位置: internal/auth/auth_http.go DefaultProtectedRoutes()"
echo "中间件逻辑: auth_http.go:91 — !requiresAuth || r.Method==GET 则放行"
echo "受保护 (需 Bearer token, 非 GET):"
echo "  POST   /api/tasks"
echo "  DELETE /api/tasks/"
echo "  POST   /api/agents"
echo "  PUT    /api/agents/"
echo "  DELETE /api/agents/"
echo "  POST   /api/sessions"
echo "  POST   /api/sessions/"
echo "  DELETE /api/sessions/"
echo "  POST   /api/projects"
echo "  PUT    /api/projects/"
echo "  DELETE /api/projects/"
echo "  POST   /api/multi-agent"
echo "  POST   /api/checkpoints/"
echo "  DELETE /api/memories/"
echo "  PUT    /api/memories/"
echo "  POST   /api/memories/promote"
echo "  POST   /api/tools"
echo "  PUT    /api/tools"
echo "  DELETE /api/tools"
echo "  POST   /api/auth/api-keys"
echo "  DELETE /api/auth/api-keys/"
echo "豁免 (无 token 可访问):"
echo "  所有 GET 请求 (auth_http.go:91 r.Method==http.MethodGet 短路)"
echo "  /healthz, /health, /api/version, /metrics (不在 protectedRoutes 且通常 GET)"

# 最终结论
echo ""
echo "========================================"
echo "最终结论"
echo "========================================"
if [[ $FAIL -eq 0 ]]; then
  echo "  全部测试通过 (PASS=${PASS}, SKIP=${SKIP})"
  exit 0
else
  echo "  存在失败项 (FAIL=${FAIL}, PASS=${PASS}, SKIP=${SKIP})"
  exit 1
fi
