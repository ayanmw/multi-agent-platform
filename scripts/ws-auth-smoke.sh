#!/usr/bin/env bash
# =============================================================================
# WebSocket Auth-on 模式冒烟测试脚本
# =============================================================================
# 在 REQUIRE_AUTH=true 模式下，编译并启动 server，从启动日志提取默认 admin key，
# 然后调用 Go 客户端验证 WebSocket 在 auth-on 模式下对 Authorization header 的处理：
#   1. 合法 key 可连接
#   2. 无 key 被拒绝
#   3. 非法 key 被拒绝
#
# 用法：bash scripts/ws-auth-smoke.sh
# =============================================================================
set -u

# ---- 配置 -------------------------------------------------------------------
PORT=18103
BASE="http://localhost:${PORT}"
DB_PATH="/tmp/ws-auth-$$.db"
SERVER_BIN="/tmp/ws-auth-server-$$"
SERVER_LOG="/tmp/ws-auth-server-$$.log"
SERVER_PID=""
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

# ---- 编译服务 ---------------------------------------------------------------
echo "[setup] 编译后端服务..."
if ! go build -o "${SERVER_BIN}" ./cmd/server 2>"${SERVER_LOG}"; then
  echo "[FATAL] 编译失败"
  cat "${SERVER_LOG}"
  exit 2
fi
echo "[setup] 编译成功"

# ---- 启动服务（REQUIRE_AUTH=true）--------------------------------------------
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
  echo "[FATAL] 服务 30s 内未就绪"
  tail -30 "${SERVER_LOG}"
  exit 3
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

# ---- 调用 Go 客户端进行 WebSocket auth 测试 -----------------------------------
echo
echo "[test] 启动 WebSocket auth 测试客户端..."
WS_GO="scripts/ws-auth.go"
if [[ ! -f "${WS_GO}" ]]; then
  WS_GO="D:\\Claude-Code-MultiAgent\\scripts\\ws-auth.go"
fi

# ws-auth.go 使用 `//go:build authsmoke` 约束，需带 -tags authsmoke 才能编译；
# 该约束用于和同目录的 ws-smoke.go (//go:build wsauth) 隔离，避免 package main
# 重复声明 (Event / main) 冲突。
go run -tags authsmoke "${WS_GO}" "${ADMIN_KEY}" "${BASE}"
exit $?
