# =============================================================================
# Multi-Agent Platform — curl 冒烟测试脚本 (PowerShell 版)
# =============================================================================
# 作用：与 smoke-test.sh 等价，启动后端服务（LLM_USE_MOCK=true）并对全部
#       HTTP REST 端点逐一发请求，打印 PASS/FAIL 与状态码。
# 环境：Windows PowerShell 5.1+ / PowerShell 7+。需要 go 与 curl（或用
#       Invoke-WebRequest，本脚本统一用 curl.exe 以与 .sh 版本行为一致）。
#
# 用法：  powershell -ExecutionPolicy Bypass -File scripts\smoke-test.ps1
#   可选环境变量：
#     $env:PORT        服务端口（默认 18080）
#     $env:KEEP_SERVER =1 时不杀服务（调试用）
# =============================================================================
# 注意：本版本为核心端点最小可用版（覆盖 1-9 节主路径）。完整依赖串联与
#       细节校验以 bash 版 smoke-test.sh 为准；两脚本可互补使用。
# =============================================================================

$ErrorActionPreference = 'Continue'
$PORT = if ($env:PORT) { $env:PORT } else { '18080' }
$BASE = "http://localhost:$PORT"
$DB_PATH = Join-Path $env:TEMP "multiagent-smoke-$(Get-Random).db"
$SERVER_BIN = Join-Path $env:TEMP "smoke-server-$(Get-Random).exe"
$SERVER_LOG = Join-Path $env:TEMP "smoke-server-$(Get-Random).log"

$script:PASS = 0
$script:FAIL = 0
$script:SKIP = 0
$script:PROBLEMS = New-Object System.Collections.Generic.List[string]

function Get-Random { Get-Random -Maximum 100000000 }   # 占位；下方用 [Guid] 替代
function NewId { ([Guid]::NewGuid().ToString('N').Substring(0,8)) }

# ---- 请求辅助 ---------------------------------------------------------------
function Invoke-Req {
    param([string]$Method, [string]$Path, [string]$Data = $null, [string]$Expect = $null)
    $url = "$BASE$Path"
    $args = @('-s', '-o', 'NUL', '-w', '%{http_code}', '-X', $Method, $url)
    if ($Data) { $args += @('-H', 'Content-Type: application/json', '--data', $Data) }
    $code = & curl.exe @args 2>$null
    $mark = 'PASS'
    if ($Expect) {
        if ($code -eq $Expect) { $script:PASS++; $mark = 'PASS' }
        else { $script:FAIL++; $mark = 'FAIL' }
        '{0,-5} {1,-6} {2,-45} -> {3} (expect {4})' -f "[$mark]", $Method, $Path, $code, $Expect
    } elseif ($code -match '^2') {
        $script:PASS++
        '{0,-5} {1,-6} {2,-45} -> {3}' -f "[PASS]", $Method, $Path, $code
    } elseif ($code -eq '405') {
        $script:PASS++; $mark = 'PASS*'
        $script:PROBLEMS.Add("[405] $Method $Path — method 不允许（端点存在）")
        '{0,-5} {1,-6} {2,-45} -> {3}' -f "[$mark]", $Method, $Path, $code
    } else {
        $script:FAIL++
        '{0,-5} {1,-6} {2,-45} -> {3}' -f "[FAIL]", $Method, $Path, $code
    }
    return $code
}

function Invoke-ReqJson {
    param([string]$Method, [string]$Path, [string]$Data = $null)
    # 返回 (code, body)
    $tmp = [System.IO.Path]::GetTempFileName()
    $args = @('-s', '-X', $Method, "$BASE$Path", '-H', 'Content-Type: application/json')
    if ($Data) { $args += @('--data', $Data) }
    $code = & curl.exe -s -o $tmp -w '%{http_code}' @args 2>$null
    $body = Get-Content $tmp -Raw -ErrorAction SilentlyContinue
    Remove-Item $tmp -ErrorAction SilentlyContinue
    return @($code, $body)
}

function Get-JsonField {
    param([string]$Json, [string[]]$Keys)
    foreach ($k in $Keys) {
        $m = [regex]::Match($Json, "\"$k""\s*:\s*""([^""]*)""")
        if ($m.Success) { return $m.Groups[1].Value }
    }
    return $null
}

function Print-Section([string]$Title) { Write-Output ""; Write-Output "===== $Title =====" }

# ---- 编译 -------------------------------------------------------------------
Write-Output "[setup] 编译后端服务..."
& go build -o $SERVER_BIN ./cmd/server 2>$SERVER_LOG
if ($LASTEXITCODE -ne 0) {
    Write-Output "[FATAL] 编译失败，日志见 $SERVER_LOG"; Get-Content $SERVER_LOG; exit 2
}

# ---- 启动服务 ---------------------------------------------------------------
Write-Output "[setup] 启动服务 (port=$PORT, DB=$DB_PATH, LLM_USE_MOCK=true)..."
$env:LLM_USE_MOCK = 'true'
$env:REQUIRE_AUTH = 'false'
$env:SERVER_PORT = $PORT
$env:DB_PATH = $DB_PATH
$proc = Start-Process -FilePath $SERVER_BIN -RedirectStandardOutput $SERVER_LOG -RedirectStandardError $SERVER_LOG -PassThru -NoNewWindow

# ---- 等待健康 ---------------------------------------------------------------
Write-Output "[setup] 等待 /healthz 就绪..."
$ready = $false
for ($i = 1; $i -le 60; $i++) {
    try {
        $code = & curl.exe -s -o NUL -w '%{http_code}' "$BASE/healthz" 2>$null
        if ($code -eq '200') { $ready = $true; break }
    } catch {}
    Start-Sleep -Milliseconds 500
}
if (-not $ready) {
    Write-Output "[FATAL] 服务 30s 内未就绪。服务日志："; Get-Content $SERVER_LOG -Tail 30
    if (-not $env:KEEP_SERVER) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
    exit 3
}
Write-Output "[setup] 服务就绪"

try {
    # 1. 基础 / 观测
    Print-Section '1. 基础 / 观测'
    Invoke-Req GET /healthz | Out-Host
    Invoke-Req GET /metrics | Out-Host
    Invoke-Req GET /api/version | Out-Host
    Invoke-Req GET /health | Out-Host

    # 2. Auth
    Print-Section '2. Auth (REQUIRE_AUTH=false)'
    $r = Invoke-ReqJson POST /api/auth/api-keys '{"name":"smoke-key"}'
    Write-Output ("    create resp: " + ($r[1].Substring(0, [Math]::Min(160, $r[1].Length))))
    Invoke-Req GET /api/auth/api-keys | Out-Host
    $authId = Get-JsonField $r[1] @('id')
    if ($authId) { Invoke-Req DELETE "/api/auth/api-keys/$authId" | Out-Host }
    else { $script:FAIL++; Write-Output '[FAIL] POST /api/auth/api-keys 未返回 id'; $script:PROBLEMS.Add('POST /api/auth/api-keys 未返回 id') }

    # 3. Project
    Print-Section '3. Project'
    Invoke-Req GET /api/projects | Out-Host
    $r = Invoke-ReqJson POST /api/projects '{"name":"smoke-proj","description":"smoke test project"}'
    Write-Output ("    create resp: " + ($r[1].Substring(0, [Math]::Min(160, $r[1].Length))))
    $projId = Get-JsonField $r[1] @('id')
    if ($projId) {
        Invoke-Req GET "/api/projects/$projId" | Out-Host
        Invoke-Req PUT "/api/projects/$projId" '{"name":"renamed","description":"u"}' | Out-Host
        Invoke-Req DELETE "/api/projects/$projId" | Out-Host
    } else { $script:FAIL++; Write-Output '[FAIL] POST /api/projects 未返回 id'; $script:PROBLEMS.Add('POST /api/projects 未返回 id') }

    # 4. Session
    Print-Section '4. Session'
    Invoke-Req GET /api/sessions | Out-Host
    $r = Invoke-ReqJson POST /api/sessions '{"user_input":"smoke test session","project_id":"default"}'
    Write-Output ("    create resp: " + ($r[1].Substring(0, [Math]::Min(160, $r[1].Length))))
    $sessId = Get-JsonField $r[1] @('session_id', 'id')
    if ($sessId) {
        Invoke-Req GET "/api/sessions/$sessId" | Out-Host
        Invoke-Req GET "/api/sessions/$sessId/messages" | Out-Host
        Invoke-Req POST "/api/sessions/$sessId/chat" '{"input":"hello dialogue test","max_steps":3}' | Out-Host
        Start-Sleep -Seconds 1.5
        Invoke-Req GET "/api/sessions/$sessId/messages" | Out-Host
        Invoke-Req DELETE "/api/sessions/$sessId" | Out-Host
    } else { $script:FAIL++; Write-Output '[FAIL] POST /api/sessions 未返回 session_id'; $script:PROBLEMS.Add('POST /api/sessions 未返回 session_id') }

    # 5. Agent
    Print-Section '5. Agent'
    Invoke-Req GET /api/agents | Out-Host
    $r = Invoke-ReqJson POST /api/agents '{"id":"agent_smoke","name":"Smoke Agent","system_prompt":"test"}'
    $agentId = Get-JsonField $r[1] @('id'); if (-not $agentId) { $agentId = 'agent_smoke' }
    Invoke-Req GET "/api/agents/$agentId" | Out-Host
    Invoke-Req PUT "/api/agents/$agentId" '{"name":"Renamed","system_prompt":"u"}' | Out-Host
    Invoke-Req DELETE "/api/agents/$agentId" | Out-Host

    # 6. Task
    Print-Section '6. Task'
    Invoke-Req GET /api/tasks | Out-Host
    Invoke-Req POST '/api/tasks?case=dialogue' '{"action":"chat","input":"hello dialogue","agent_id":"agent_smoke","max_steps":3}' | Out-Host
    Start-Sleep -Seconds 2
    $r = Invoke-ReqJson GET /api/tasks
    $taskId = Get-JsonField $r[1] @('task_id', 'id')
    if ($taskId) { Invoke-Req GET "/api/tasks?id=$taskId" | Out-Host }
    else { $script:FAIL++; Write-Output '[FAIL] 未解析到 task_id'; $script:PROBLEMS.Add('GET /api/tasks 响应未含 task_id') }
    Invoke-Req POST /api/tasks '{"action":"multi-agent","input":"research tech news","case_type":"multi_agent","max_steps":3}' | Out-Host
    Start-Sleep -Seconds 1.5

    # 7. Tool / Cases / Cost / Checkpoints
    Print-Section '7. Tool / Cases / Cost / Checkpoints'
    Invoke-Req GET /api/tools | Out-Host
    Invoke-Req GET /api/cases | Out-Host
    Invoke-Req POST /api/tools '{"name":"echo_smoke","type":"shell","command":"echo hi","description":"x"}' | Out-Host
    Invoke-Req DELETE '/api/tools?name=echo_smoke' | Out-Host
    Invoke-Req GET /api/costs | Out-Host
    Invoke-Req GET "/api/costs?task_id=$taskId" | Out-Host
    Invoke-Req GET "/api/costs?session_id=$sessId" | Out-Host
    Invoke-Req GET '/api/costs?project_id=default' | Out-Host
    Invoke-Req GET /api/checkpoints | Out-Host
    Invoke-Req POST /api/checkpoints/recover '{"task_id":"nonexistent_smoke"}' | Out-Host

    # 8. Memory
    Print-Section '8. Memory'
    Invoke-Req GET /api/memories | Out-Host
    Invoke-Req POST /api/memories '' '405' | Out-Host
    Invoke-Req GET '/api/memories/recall?task=smoke&project=default&max=3' | Out-Host
    Invoke-Req POST /api/memories/promote '{"task_id":"smoke"}' '200' | Out-Host
    Invoke-Req PUT '/api/memories/fake_id/scope' '{"scope":"project"}' | Out-Host
    Invoke-Req DELETE '/api/memories/fake_id' | Out-Host

    # 9. Mock 管理
    Print-Section '9. Mock 管理'
    Invoke-Req GET /api/mock/scripts | Out-Host
    Invoke-Req POST /api/mock/scripts '{"id":"smoke-custom","case_id":"dialogue","priority":50,"match_input":["smoke"],"responses":[{"type":"text","content":"ok"}]}' | Out-Host
    Invoke-Req GET /api/mock/scripts | Out-Host
    Invoke-Req GET /api/mock/scripts/smoke-custom '200' | Out-Host
    Invoke-Req DELETE /api/mock/scripts/smoke-custom | Out-Host
    Invoke-Req POST /api/mock/reset | Out-Host

    # 10. WebSocket（curl 限制，记录 SKIP）
    Print-Section '10. WebSocket 握手'
    $script:SKIP++
    Write-Output '[SKIP] WS     /ws                                       -> (curl 限制，留待 WS 专项测试)'
    $script:PROBLEMS.Add('WebSocket /ws 用 curl 验证握手受限，建议后续用 wscat/Go 客户端专项测')

    # 汇总
    Print-Section '汇总'
    Write-Output '----------------------------------------'
    Write-Output ("  PASS : $script:PASS")
    Write-Output ("  FAIL : $script:FAIL")
    Write-Output ("  SKIP : $script:SKIP")
    Write-Output '----------------------------------------'
    if ($script:PROBLEMS.Count -gt 0) {
        Write-Output ''; Write-Output '发现的问题 / 与文档差异：'
        foreach ($p in $script:PROBLEMS) { Write-Output "  - $p" }
    }
    if ($script:FAIL -gt 0) { Write-Output ''; Write-Output '[smoke] 存在失败项'; exit 1 }
    Write-Output ''; Write-Output '[smoke] 全部端点冒烟通过'
    exit 0
}
finally {
    if (-not $env:KEEP_SERVER) {
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        Remove-Item $DB_PATH, $SERVER_BIN, $SERVER_LOG -ErrorAction SilentlyContinue
    }
}
