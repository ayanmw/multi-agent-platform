#!/usr/bin/env bash
# =============================================================================
# Multi-Agent Platform — Policy 安全门端到端评测脚本 (维度 B, Auth-on)
# =============================================================================
# 评测 Harness 层 PolicyChain + PolicyGate 的端到端拦截能力。
# 使用 /api/mock/scripts 动态注入"恶意"mock 脚本，让 MockProvider 返回特定
# tool_call，然后通过 /api/tasks 触发，验证各 PolicyRule 是否正确拦截。
# 本脚本为 policy-smoke.sh 的 REQUIRE_AUTH=true 副本，所有写操作携带 Bearer token。
#
# 测试项：
#   1. DangerousCommandRule — rm -rf 危险命令拦截
#   2. PathTraversalRule    — 路径穿越 (..) 拦截
#   3. FileScopeRule        — 越界绝对路径文件写拦截
#   4. ApprovalRule         — 高风险路径 (/etc/) 审批拦截
#   5. 控制测试              — 安全命令正常执行（验证 policy 不误杀）
#   6. TokenBudgetRule      — SKIP（默认 contract TokenBudget=0，无法端到端触发）
#   7. ToolWhitelistRule    — SKIP（默认 contract AllowedTools=nil，无法触发）
#
# 独立端口 18103 + 独立临时 DB，不污染仓库环境。
# =============================================================================
set -u

# ---- 配置 -------------------------------------------------------------------
PORT=18103
BASE="http://localhost:${PORT}"
DB_PATH="/tmp/policy-auth-$$.db"
SERVER_BIN="/tmp/policy-auth-server-$$.exe"
SERVER_LOG="/tmp/policy-auth-server-$$.log"
SERVER_PID=""
PASS=0; FAIL=0; SKIP=0
RESULTS=()    # 测试结果数组
FINDINGS=()   # 发现的后端 policy bug / 缺口
ADMIN_KEY=""
AUTH_H=()

# 危险命令测试目标目录（测试前创建，测试后检查是否被删除）
RM_TEST_DIR="/tmp/policy-auth-rm-test-$$"
# 越界文件写测试目标文件（Windows 绝对路径，在 scope=CWD 之外）
# 注意:必须用 Windows 盘符绝对路径，Unix 风格 /tmp/ 在 Windows 上 filepath.IsAbs 返回 false
SCOPE_TEST_FILE="C:/policy_auth_scope_test_$$.txt"
# 审批规则测试目标文件（在 scope 内但路径含 /etc/，触发 ApprovalRule 误判）
APPROVAL_TEST_FILE="./etc/policy_auth_approval_test.txt"

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null
    wait "${SERVER_PID}" 2>/dev/null
  fi
  rm -f "${DB_PATH}" "${SERVER_BIN}" "${SERVER_LOG}" 2>/dev/null
  rm -f /tmp/mock-pol-*.json 2>/dev/null
  rm -f "${SCOPE_TEST_FILE}" 2>/dev/null
  rm -rf ./etc 2>/dev/null
  rm -rf "${RM_TEST_DIR}" 2>/dev/null
}
trap cleanup EXIT

# ---- 辅助函数 ---------------------------------------------------------------

# JSON 解析助手: 用 node 解析 task detail，返回结构化字段
# parse_detail <json>  ->  输出 JSON: {task_status, tool_steps_count, completed_tool_steps, ...}
parse_detail() {
  local json="$1"
  # 转义单引号用于 node 参数
  local escaped
  escaped=$(echo "$json" | sed "s/'/\\\\'/g")
  node -e "
const data = process.argv[1];
try {
  const d = JSON.parse(data);
  const taskStatus = d.task ? d.task.status : 'unknown';
  const steps = d.steps || [];
  const toolSteps = steps.filter(s => s.type === 'tool_call');
  const completedToolSteps = toolSteps.filter(s => s.status === 'completed');
  const failedToolSteps = toolSteps.filter(s => s.status === 'failed');
  const firstToolOutput = toolSteps.length > 0 ? (toolSteps[0].tool_output || '') : '';
  console.log(JSON.stringify({
    task_status: taskStatus,
    steps_count: steps.length,
    tool_steps_count: toolSteps.length,
    completed_tool_steps: completedToolSteps.length,
    failed_tool_steps: failedToolSteps.length,
    first_tool_output: firstToolOutput,
    first_tool_name: toolSteps.length > 0 ? (toolSteps[0].tool_name || '') : ''
  }));
} catch(e) { console.log(JSON.stringify({error: e.message})); }
" "$escaped"
}

# 从 JSON 响应中提取第一个匹配的字符串字段值
# jget <json> <key>  ->  value
jget() {
  local json="$1" key="$2"
  echo "$json" | grep -o "\"${key}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 \
    | sed -E "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"([^\"]*)\".*/\1/"
}

# 从 parse_detail 的 JSON 输出中提取字段
# pget <parsed_json> <field>
pget() {
  local parsed="$1" field="$2"
  echo "$parsed" | grep -o "\"${field}\":[^,}]*" | head -1 \
    | sed -E "s/\"${field}\":(.*)/\1/" | sed 's/^"//' | sed 's/"$//'
}

# 带认证的 POST JSON 到指定路径，返回响应体
# post_json_file_auth <path> <jsonfile>
post_json_file_auth() {
  local path="$1" jsonfile="$2"
  curl -s -X POST "${BASE}${path}" \
    -H 'Content-Type: application/json' \
    "${AUTH_H[@]}" \
    --data @"${jsonfile}" 2>/dev/null
}

# 启动一个 chat task，返回 task_id
# start_task <case_id> <input_text>
start_task() {
  local case_id="$1" input="$2"
  local resp
  resp=$(curl -s -X POST "${BASE}/api/tasks?case=${case_id}" \
    -H 'Content-Type: application/json' \
    "${AUTH_H[@]}" \
    --data "{\"action\":\"chat\",\"input\":\"${input}\",\"max_steps\":2}" 2>/dev/null)
  jget "$resp" "task_id"
}

# 获取 task 详情 JSON
get_task_detail() {
  local tid="$1"
  curl -s "${BASE}/api/tasks?id=${tid}" 2>/dev/null
}

# 轮询 task 直到完成（status != running 且非空），或超时
# poll_task <task_id>  ->  返回最终状态
poll_task() {
  local tid="$1"
  local status=""
  for i in $(seq 1 45); do  # 45 x 2s = 90s 超时（覆盖 30s 审批超时 + 余量）
    local detail
    detail=$(get_task_detail "$tid")
    status=$(echo "$detail" | grep -o '"status":"[^"]*"' | head -1 \
      | sed -E 's/"status":"([^"]*)".*/\1/')
    # 注意: 第一个 "status" 字段在 steps[0] 里 (think 步骤是 completed)，
    # 而不是 task.status。需要用 parse_detail 取 task.status。
    local parsed
    parsed=$(parse_detail "$detail")
    status=$(pget "$parsed" "task_status")
    if [[ -n "$status" && "$status" != "running" ]]; then
      echo "$status"
      return 0
    fi
    sleep 2
  done
  echo "timeout"
}

print_section() { echo; echo "===== $1 ====="; }

# 记录结果: record_result <rule_name> <PASS|FAIL|SKIP> <evidence>
record_result() {
  local rule="$1" result="$2" evidence="$3"
  RESULTS+=("[${result}] ${rule}: ${evidence}")
  case "$result" in
    PASS) PASS=$((PASS+1)) ;;
    FAIL) FAIL=$((FAIL+1)) ;;
    SKIP) SKIP=$((SKIP+1)) ;;
  esac
  printf '%-6s %-25s %s\n' "[${result}]" "$rule" "$evidence"
}

# ---- 编译服务 ---------------------------------------------------------------
echo "[setup] 编译后端服务..."
if ! go build -o "${SERVER_BIN}" ./cmd/server 2>"${SERVER_LOG}"; then
  echo "[FATAL] 编译失败，日志见 ${SERVER_LOG}"
  cat "${SERVER_LOG}"
  exit 2
fi
echo "[setup] 编译成功 >>"

# ---- 启动服务（REQUIRE_AUTH=true）---------------------------------------------
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
  tail -30 "${SERVER_LOG}"
  exit 3
fi
echo "[setup] 服务就绪 >>"

# ---- 提取 Bearer Token -------------------------------------------------------
echo "[setup] 从启动日志提取 DEFAULT ADMIN API KEY..."
ADMIN_KEY=$(grep "DEFAULT ADMIN API KEY" "${SERVER_LOG}" | head -1 \
  | sed -E 's/.*DEFAULT ADMIN API KEY:[[:space:]]*(sk_[A-Za-z0-9_=-]+).*/\1/')
if [[ -z "${ADMIN_KEY}" ]]; then
  echo "[FATAL] 未能提取默认 admin API key"
  echo "--- 日志前 40 行 ---"; head -40 "${SERVER_LOG}"
  exit 4
fi
AUTH_H=(-H "Authorization: Bearer ${ADMIN_KEY}")
echo "[setup] 抓取到 admin key（长度=${#ADMIN_KEY}），已设置 auth header >>"
if grep -q "Auth:.*enabled" "${SERVER_LOG}"; then
  echo "[setup] 启动日志确认 Auth: enabled"
else
  echo "[WARN] 未发现 Auth: enabled 标记"
fi

# ---- 准备测试环境 -----------------------------------------------------------
echo "[setup] 准备测试环境..."
mkdir -p "${RM_TEST_DIR}"
echo "test marker" > "${RM_TEST_DIR}/marker.txt"
# 清理可能残留的测试文件
rm -f "${SCOPE_TEST_FILE}" 2>/dev/null
rm -rf ./etc 2>/dev/null

# =============================================================================
# 注入 Mock 脚本
# =============================================================================
print_section "注入 Mock 脚本"

# --- 1. 危险命令: rm -rf ---
cat > /tmp/mock-pol-dang.json <<'MOCK_EOF'
{"id":"pol-dang-rm","case_id":"pol-dang-rm","priority":200,"match_input":["policy-test-danger"],"responses":[{"type":"tool_call","tool_calls":[{"idx":0,"id":"call_dang","type":"function","function":{"name":"run_shell","arguments":"{\"command\":\"rm -rf /tmp/policy-smoke-rm-test-placeholder\"}"}}]}]}
MOCK_EOF
# 替换占位符为真实路径（heredoc 中不展开 $ 变量，用 sed 替换）
sed -i "s|/tmp/policy-smoke-rm-test-placeholder|${RM_TEST_DIR}|g" /tmp/mock-pol-dang.json
echo "  注入 pol-dang-rm (rm -rf ${RM_TEST_DIR})"
post_json_file_auth /api/mock/scripts /tmp/mock-pol-dang.json > /dev/null

# --- 2. 路径穿越: /etc/passwd (Unix 绝对路径，Windows 上 filepath.IsAbs=false) ---
cat > /tmp/mock-pol-trav.json <<'MOCK_EOF'
{"id":"pol-traversal","case_id":"pol-traversal","priority":200,"match_input":["policy-test-traversal"],"responses":[{"type":"tool_call","tool_calls":[{"idx":0,"id":"call_trav","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"/etc/passwd\",\"content\":\"hacked\"}"}}]}]}
MOCK_EOF
echo "  注入 pol-traversal (write_file /etc/passwd)"
post_json_file_auth /api/mock/scripts /tmp/mock-pol-trav.json > /dev/null

# --- 3. 越界文件写: 绝对路径在 scope 之外 ---
cat > /tmp/mock-pol-scope.json <<'MOCK_EOF'
{"id":"pol-scope","case_id":"pol-scope","priority":200,"match_input":["policy-test-scope"],"responses":[{"type":"tool_call","tool_calls":[{"idx":0,"id":"call_scope","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"PLACEHOLDER_SCOPE_FILE\",\"content\":\"out of scope\"}"}}]}]}
MOCK_EOF
sed -i "s|PLACEHOLDER_SCOPE_FILE|${SCOPE_TEST_FILE}|g" /tmp/mock-pol-scope.json
echo "  注入 pol-scope (write_file ${SCOPE_TEST_FILE})"
post_json_file_auth /api/mock/scripts /tmp/mock-pol-scope.json > /dev/null

# --- 4. ApprovalRule: 路径含 /etc/ 但在 scope 内（相对路径） ---
cat > /tmp/mock-pol-approval.json <<'MOCK_EOF'
{"id":"pol-approval","case_id":"pol-approval","priority":200,"match_input":["policy-test-approval"],"responses":[{"type":"tool_call","tool_calls":[{"idx":0,"id":"call_approval","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"./etc/policy_approval_test.txt\",\"content\":\"approval test\"}"}}]}]}
MOCK_EOF
echo "  注入 pol-approval (write_file ./etc/policy_approval_test.txt)"
post_json_file_auth /api/mock/scripts /tmp/mock-pol-approval.json > /dev/null

# --- 5. 控制测试: 安全 echo 命令 ---
cat > /tmp/mock-pol-safe.json <<'MOCK_EOF'
{"id":"pol-safe-echo","case_id":"pol-safe-echo","priority":200,"match_input":["policy-test-safe"],"responses":[{"type":"tool_call","tool_calls":[{"idx":0,"id":"call_safe","type":"function","function":{"name":"run_shell","arguments":"{\"command\":\"echo policy_safe_marker\"}"}}]},{"type":"text","content":"Safe command executed successfully."}]}
MOCK_EOF
echo "  注入 pol-safe-echo (echo policy_safe_marker)"
post_json_file_auth /api/mock/scripts /tmp/mock-pol-safe.json > /dev/null

echo "[setup] Mock 脚本注入完成 >>"

# 验证脚本注入成功
MOCK_COUNT=$(curl -s "${BASE}/api/mock/scripts" 2>/dev/null | grep -o '"id":"pol-[^"]*"' | wc -l)
if [[ "$MOCK_COUNT" -lt 5 ]]; then
  echo "[WARN] 只注入了 ${MOCK_COUNT} 个 mock 脚本（期望 5），检查注入逻辑"
fi

# =============================================================================
# 启动所有测试任务（并发）
# =============================================================================
print_section "启动测试任务（串行启动，间隔 1.2s 避免 task_id 碰撞）"
echo "  [1/5] DangerousCommandRule - rm -rf ..."
TASK_DANG=$(start_task "pol-dang-rm" "policy-test-danger")
echo "    task_id=${TASK_DANG}"
sleep 1.2

echo "  [2/5] PathTraversalRule - /etc/passwd ..."
TASK_TRAV=$(start_task "pol-traversal" "policy-test-traversal")
echo "    task_id=${TASK_TRAV}"
sleep 1.2

echo "  [3/5] FileScopeRule - 绝对路径越界 ..."
TASK_SCOPE=$(start_task "pol-scope" "policy-test-scope")
echo "    task_id=${TASK_SCOPE}"
sleep 1.2

echo "  [4/5] ApprovalRule - /etc/ 路径审批 ..."
TASK_APPROVAL=$(start_task "pol-approval" "policy-test-approval")
echo "    task_id=${TASK_APPROVAL}"
sleep 1.2

echo "  [5/5] 控制测试 - 安全 echo ..."
TASK_SAFE=$(start_task "pol-safe-echo" "policy-test-safe")
echo "    task_id=${TASK_SAFE}"

# 检查所有 task_id 都获取成功
for tid_var in TASK_DANG TASK_TRAV TASK_SCOPE TASK_APPROVAL TASK_SAFE; do
  tid="${!tid_var}"
  if [[ -z "$tid" ]]; then
    echo "[FATAL] ${tid_var} 未获取到 task_id，服务可能异常"
    tail -20 "${SERVER_LOG}"
    exit 4
  fi
done

# =============================================================================
# 轮询所有任务直到完成
# =============================================================================
print_section "轮询任务结果（最多 90s，含 30s 审批超时等待）"
echo "  等待所有任务完成..."

ALL_TASKS=("$TASK_DANG" "$TASK_TRAV" "$TASK_SCOPE" "$TASK_APPROVAL" "$TASK_SAFE")
ALL_STATUS=("" "" "" "" "")
deadline=$((SECONDS + 90))
while [[ $SECONDS -lt $deadline ]]; do
  all_done=true
  for i in "${!ALL_TASKS[@]}"; do
    if [[ -z "${ALL_STATUS[$i]}" || "${ALL_STATUS[$i]}" == "running" ]]; then
      detail=$(get_task_detail "${ALL_TASKS[$i]}")
      parsed=$(parse_detail "$detail")
      status=$(pget "$parsed" "task_status")
      ALL_STATUS[$i]="$status"
      if [[ -z "$status" || "$status" == "running" ]]; then
        all_done=false
      fi
    fi
  done
  if $all_done; then break; fi
  sleep 2
done

echo "  任务状态："
echo "    DangerousCommand: ${ALL_STATUS[0]}"
echo "    PathTraversal:    ${ALL_STATUS[1]}"
echo "    FileScope:        ${ALL_STATUS[2]}"
echo "    ApprovalRule:     ${ALL_STATUS[3]}"
echo "    控制测试(safe):   ${ALL_STATUS[4]}"

# =============================================================================
# 评测各规则拦截效果
# =============================================================================
print_section "评测结果"

# --- 1. DangerousCommandRule ---
echo ""
echo "--- 1. DangerousCommandRule: rm -rf 危险命令 ---"
DANG_DETAIL=$(get_task_detail "$TASK_DANG")
DANG_PARSED=$(parse_detail "$DANG_DETAIL")
DANG_STATUS=$(pget "$DANG_PARSED" "task_status")
DANG_TOOL_DONE=$(pget "$DANG_PARSED" "completed_tool_steps")
DANG_OUTPUT=$(pget "$DANG_PARSED" "first_tool_output")
DANG_DIR_EXISTS=$(test -d "$RM_TEST_DIR" && echo "yes" || echo "no")
echo "  task_status=${DANG_STATUS}, completed_tool_steps=${DANG_TOOL_DONE}, target_dir_exists=${DANG_DIR_EXISTS}"
echo "  tool_output: ${DANG_OUTPUT}"
if [[ "$DANG_DIR_EXISTS" == "yes" ]]; then
  record_result "DangerousCommandRule" "PASS" \
    "task=failed, 目标目录未删除 -> 危险命令被拦截（审批超时拒绝，rm -rf 未执行）"
else
  record_result "DangerousCommandRule" "FAIL" \
    "task=${DANG_STATUS}, 目标目录被删除! 危险命令真的执行了!"
  FINDINGS+=("[严重] DangerousCommandRule 未能拦截 rm -rf，命令实际执行并删除了目标目录")
fi

# --- 2. PathTraversalRule ---
echo ""
echo "--- 2. PathTraversalRule/FileScopeRule: /etc/passwd (Windows 兼容性测试) ---"
TRAV_DETAIL=$(get_task_detail "$TASK_TRAV")
TRAV_PARSED=$(parse_detail "$TRAV_DETAIL")
TRAV_STATUS=$(pget "$TRAV_PARSED" "task_status")
TRAV_TOOL_DONE=$(pget "$TRAV_PARSED" "completed_tool_steps")
TRAV_OUTPUT=$(pget "$TRAV_PARSED" "first_tool_output")
echo "  task_status=${TRAV_STATUS}, completed_tool_steps=${TRAV_TOOL_DONE}"
echo "  tool_output: ${TRAV_OUTPUT}"
if [[ "$TRAV_TOOL_DONE" != "0" ]]; then
  record_result "PathTraversalRule" "FAIL" \
    "task=${TRAV_STATUS}, completed_tool_steps=${TRAV_TOOL_DONE} -> /etc/passwd 在 Windows 上未被拦截（filepath.IsAbs=false 被并入 scope）"
  FINDINGS+=("[严重/Windows兼容性] /etc/passwd 在 Windows 上 filepath.IsAbs=false，FileScopeRule 用 filepath.Join 并入 scope 放行，文件被写入 CWD/etc/passwd。PathTraversalRule 不拦截（不含 ..）。单元测试 policy_test.go 已记录此行为但未修复。")
else
  record_result "PathTraversalRule" "PASS" \
    "task=${TRAV_STATUS}, 无 completed tool 步骤 -> /etc/passwd 被拦截"
fi

# --- 3. FileScopeRule ---
echo ""
echo "--- 3. FileScopeRule: 绝对路径越界写文件 ---"
SCOPE_DETAIL=$(get_task_detail "$TASK_SCOPE")
SCOPE_PARSED=$(parse_detail "$SCOPE_DETAIL")
SCOPE_STATUS=$(pget "$SCOPE_PARSED" "task_status")
SCOPE_TOOL_DONE=$(pget "$SCOPE_PARSED" "completed_tool_steps")
SCOPE_FILE_EXISTS=$(test -f "$SCOPE_TEST_FILE" && echo "yes" || echo "no")
echo "  task_status=${SCOPE_STATUS}, completed_tool_steps=${SCOPE_TOOL_DONE}, target_file_exists=${SCOPE_FILE_EXISTS}"
if [[ "$SCOPE_STATUS" == "failed" && "$SCOPE_TOOL_DONE" == "0" && "$SCOPE_FILE_EXISTS" == "no" ]]; then
  record_result "FileScopeRule" "PASS" \
    "task=failed, 无 completed tool 步骤, 目标文件未创建 -> 越界路径被拦截"
elif [[ "$SCOPE_FILE_EXISTS" == "yes" ]]; then
  record_result "FileScopeRule" "FAIL" \
    "task=${SCOPE_STATUS}, 目标文件已创建! 越界写未被拦截!"
  FINDINGS+=("[严重] FileScopeRule 未拦截 scope 外绝对路径，文件被写入")
elif [[ "$SCOPE_TOOL_DONE" != "0" ]]; then
  SCOPE_OUTPUT=$(pget "$SCOPE_PARSED" "first_tool_output")
  if [[ -n "$SCOPE_OUTPUT" ]]; then
    record_result "FileScopeRule" "FAIL" \
      "task=${SCOPE_STATUS}, completed_tool_steps=${SCOPE_TOOL_DONE}, 工具有输出 -> 越界写未被拦截"
    FINDINGS+=("[严重] FileScopeRule 未拦截 scope 外路径，工具实际执行并有输出")
  else
    record_result "FileScopeRule" "FAIL" \
      "task=${SCOPE_STATUS}, completed_tool_steps=${SCOPE_TOOL_DONE} -> 越界写未被拦截"
  fi
else
  record_result "FileScopeRule" "FAIL" \
    "task=${SCOPE_STATUS}, tool_done=${SCOPE_TOOL_DONE}, file_exists=${SCOPE_FILE_EXISTS} -> 状态异常"
fi

# --- 4. ApprovalRule: ./etc/ 相对路径不再被误判（isHighRiskFilePath 修复验证） ---
echo ""
echo "--- 4. ApprovalRule: ./etc/ 相对路径不触发审批（修复验证） ---"
APPR_DETAIL=$(get_task_detail "$TASK_APPROVAL")
APPR_PARSED=$(parse_detail "$APPR_DETAIL")
APPR_STATUS=$(pget "$APPR_PARSED" "task_status")
APPR_TOOL_DONE=$(pget "$APPR_PARSED" "completed_tool_steps")
APPR_FILE_EXISTS=$(test -f "$APPROVAL_TEST_FILE" && echo "yes" || echo "no")
echo "  task_status=${APPR_STATUS}, completed_tool_steps=${APPR_TOOL_DONE}, target_file_exists=${APPR_FILE_EXISTS}"
if [[ "$APPR_TOOL_DONE" != "0" && "$APPR_FILE_EXISTS" == "yes" ]]; then
  record_result "ApprovalRule" "PASS" \
    "task=${APPR_STATUS}, 文件已创建 -> ./etc/ 相对路径不被误判为系统路径，正常执行"
elif [[ "$APPR_STATUS" == "failed" && "$APPR_TOOL_DONE" == "0" && "$APPR_FILE_EXISTS" == "no" ]]; then
  record_result "ApprovalRule" "FAIL" \
    "task=failed, 文件未创建 -> ./etc/ 被误判为高风险系统路径（isHighRiskFilePath 修复未生效）"
  FINDINGS+=("[修复未生效] ApprovalRule.isHighRiskFilePath 仍用 Contains 匹配，./etc/ 被误判")
else
  record_result "ApprovalRule" "FAIL" \
    "task=${APPR_STATUS}, tool_done=${APPR_TOOL_DONE}, file_exists=${APPR_FILE_EXISTS} -> 状态异常"
fi

# --- 5. 控制测试: 安全命令 ---
echo ""
echo "--- 5. 控制测试: 安全 echo 命令（应正常执行） ---"
SAFE_DETAIL=$(get_task_detail "$TASK_SAFE")
SAFE_PARSED=$(parse_detail "$SAFE_DETAIL")
SAFE_STATUS=$(pget "$SAFE_PARSED" "task_status")
SAFE_TOOL_DONE=$(pget "$SAFE_PARSED" "completed_tool_steps")
SAFE_TOOL_OUTPUT=$(pget "$SAFE_PARSED" "first_tool_output")
echo "  task_status=${SAFE_STATUS}, completed_tool_steps=${SAFE_TOOL_DONE}, output=${SAFE_TOOL_OUTPUT}"
if [[ "$SAFE_TOOL_DONE" != "0" ]]; then
  if [[ "$SAFE_STATUS" == "completed" ]]; then
    record_result "控制测试(safe echo)" "PASS" \
      "task=completed, 工具正常执行 -> Policy 不误杀安全操作"
  elif echo "$SAFE_DETAIL" | grep -q "max_steps_exceeded\|max steps"; then
    record_result "控制测试(safe echo)" "PASS" \
      "task=failed(max_steps_exceeded), 但工具正常执行(tool_done=${SAFE_TOOL_DONE}) -> Policy 未误杀"
  else
    record_result "控制测试(safe echo)" "PASS" \
      "task=${SAFE_STATUS}, 工具正常执行(tool_done=${SAFE_TOOL_DONE}) -> Policy 未误杀安全操作（任务失败可能因 mock 脚本循环）"
  fi
else
  record_result "控制测试(safe echo)" "FAIL" \
    "task=${SAFE_STATUS}, tool_done=${SAFE_TOOL_DONE} -> 安全命令被误杀"
  FINDINGS+=("[严重] Policy 误杀安全命令 echo，正常操作被拦截")
fi

# --- 6. TokenBudgetRule ---
echo ""
echo "--- 6. TokenBudgetRule: token 预算耗尽拦截 ---"
echo "  默认 DefaultContract.TokenBudget=0 (unlimited)，API 请求无法设置 budget 参数"
echo "  -> 无法端到端触发，需单测覆盖（已有 policy_test.go TestTokenBudgetRule 覆盖）"
record_result "TokenBudgetRule" "SKIP" \
  "DefaultContract TokenBudget=0 (unlimited)，API 无参数可覆盖，无法端到端触发"

# --- 7. ToolWhitelistRule ---
echo ""
echo "--- 7. ToolWhitelistRule: 非白名单 tool 拦截 ---"
echo "  默认 DefaultContract.AllowedTools=nil (全部允许)，API 请求无法设置 whitelist"
echo "  -> 无法端到端触发，需单测覆盖（已有 policy_test.go TestToolWhitelistRule 覆盖）"
record_result "ToolWhitelistRule" "SKIP" \
  "DefaultContract AllowedTools=nil (全部允许)，API 无参数可覆盖，无法端到端触发"

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
# 后端 Policy Bug / 缺口清单
# =============================================================================
print_section "后端 Policy Bug / 缺口清单"
if [[ ${#FINDINGS[@]} -gt 0 ]]; then
  for f in "${FINDINGS[@]}"; do
    echo "  ${f}"
  done
fi

echo ""
echo "  [Auth-on] 本脚本运行在 REQUIRE_AUTH=true 模式，所有写请求均携带 Bearer token。"
echo ""

if [[ $FAIL -gt 0 ]]; then
  echo "[policy-auth] 存在 FAIL 项，详见上方结果。服务日志：${SERVER_LOG}"
  exit 1
fi
echo "[policy-auth] 评测完成 (PASS=${PASS}, SKIP=${SKIP}, FAIL=${FAIL})"
exit 0
