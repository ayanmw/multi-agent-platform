<!-- CronForm.vue — 新建/编辑 Cron 定时器的 Modal 表单

     Props:
       cronData: 已存在的 Cron（编辑模式）或 null（新建模式）
       visible: 是否显示

     Emits:
       close: 关闭弹窗
       save: 表单校验通过，提交持久化。新建时 emit CreateCronInput，编辑时 emit UpdateCronInput。

     表单分两段：
       1) 调度规则（schedule_type = preset | interval | cron | once）
          - preset：预设标签下拉（每分钟/每小时/每天0点…），选中后写入对应 cron_expr + schedule_type
          - interval：自由输入间隔（30s / 5m / 1h）
          - cron：6 域秒级 cron 表达式
          - once：datetime-local，提交时转 RFC3339
          切换到自由 interval/cron 后 display_type 锁定，不回退 preset。
       2) 触发动作（action_type = start_task | script | webhook | notify_session）
          按 action_type 切换 4 种 payload 子表单。

     数据来源：
       - agents 列表来自 useAgentStore()，用于 start_task 的 agent_id 下拉
       - availableTools 来自 useAgentStore()，用于 script 的 tool 名下拉
       - session_id 字段用文本输入（避免引入 session picker 组件依赖）
-->
<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useAgentStore } from '@/composables/useAgentStore'
import type {
  Cron,
  CreateCronInput,
  UpdateCronInput,
  ScheduleType,
  ActionType,
  DisplayType,
} from '@/types/cron'

const props = defineProps<{
  cronData: Cron | null
  visible: boolean
}>()

const emit = defineEmits<{
  close: []
  save: [req: CreateCronInput | UpdateCronInput]
}>()

const { agents, loadAgents, availableTools, loadAvailableTools } = useAgentStore()

// ---- 基础字段 ----
const name = ref('')
const description = ref('')
const allowConcurrent = ref(false)
const error = ref<string | null>(null)

// ---- 调度规则 ----
// displayType 驱动 UI 分段；scheduleType 是真正传给后端的调度类型。
// preset 模式下 scheduleType 由预设决定；interval/cron/once 直接对应。
const displayType = ref<DisplayType>('preset')
const scheduleType = ref<ScheduleType>('interval')
const cronExpr = ref('')        // interval / cron 共用此字段
const onceAt = ref('')          // datetime-local 字符串（YYYY-MM-DDTHH:mm）
const timezone = ref('')
const presetKey = ref('1m')     // preset 下拉当前值

/** 预设调度选项。value 同时编码 schedule_type + cron_expr。 */
const PRESETS: Array<{ key: string; label: string; scheduleType: ScheduleType; expr: string }> = [
  { key: '1m', label: '每分钟', scheduleType: 'interval', expr: '1m' },
  { key: '5m', label: '每 5 分钟', scheduleType: 'interval', expr: '5m' },
  { key: '30m', label: '每 30 分钟', scheduleType: 'interval', expr: '30m' },
  { key: '1h', label: '每小时', scheduleType: 'interval', expr: '1h' },
  { key: '6h', label: '每 6 小时', scheduleType: 'interval', expr: '6h' },
  { key: 'daily', label: '每天 00:00', scheduleType: 'cron', expr: '0 0 0 * * *' },
  { key: 'weekly', label: '每周一 00:00', scheduleType: 'cron', expr: '0 0 0 * * 1' },
  { key: 'monthly', label: '每月 1 日 00:00', scheduleType: 'cron', expr: '0 0 0 1 * *' },
]

/** 切换 displayType 时同步 scheduleType / cronExpr 的合理默认值。 */
function onDisplayTypeChange(dt: DisplayType) {
  displayType.value = dt
  if (dt === 'preset') {
    applyPreset(presetKey.value)
  } else if (dt === 'interval') {
    scheduleType.value = 'interval'
    if (!cronExpr.value || cronExpr.value.includes(' ') || cronExpr.value.includes('*')) {
      cronExpr.value = '1h'
    }
  } else if (dt === 'cron') {
    scheduleType.value = 'cron'
    if (!cronExpr.value.includes(' ') && !cronExpr.value.includes('*')) {
      cronExpr.value = '0 0 0 * * *'
    }
  } else if (dt === 'once') {
    scheduleType.value = 'once'
  }
}

/** 应用某个 preset：同步 scheduleType + cronExpr。 */
function applyPreset(key: string) {
  const p = PRESETS.find(x => x.key === key)
  if (!p) return
  scheduleType.value = p.scheduleType
  cronExpr.value = p.expr
}

function onPresetChange(key: string) {
  presetKey.value = key
  applyPreset(key)
}

// ---- 动作 payload ----
const actionType = ref<ActionType>('notify_session')

// start_task
const stAgentId = ref('')
const stSessionId = ref('')
const stInput = ref('')
const stMaxSteps = ref(0)
const stTimeoutSeconds = ref(0)

// script：以 JSON 文本编辑 tool_calls 数组，降低动态行复杂度。
const scriptJson = ref('[]')

// webhook
const whMethod = ref('POST')
const whUrl = ref('')
const whHeadersJson = ref('{}')
const whBody = ref('')
const whTimeoutSeconds = ref(10)

// notify_session
const nsSessionId = ref('')
const nsMessage = ref('')
const nsEventType = ref('')

const isEditing = computed(() => props.cronData !== null)
const modalTitle = computed(() => (isEditing.value ? '编辑定时器' : '新建定时器'))

/** 把 datetime-local 字符串转成 RFC3339（后端 once_at 期望 RFC3339）。 */
function toRFC3339(local: string): string {
  if (!local) return ''
  // datetime-local 形如 "2026-07-21T13:30"；new Date 能解析为本地时间。
  const d = new Date(local)
  if (isNaN(d.getTime())) return local
  return d.toISOString()
}

/** 把 RFC3339 / ISO 字符串转成 datetime-local 输入框可用的值。 */
function fromRFC3339(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  // toISOString() => "2026-07-21T05:30:00.000Z"，取前 16 位得 "2026-07-21T05:30"。
  return d.toISOString().slice(0, 16)
}

/** 重置表单到指定 cron（编辑）或默认值（新建）。 */
function resetForm(c: Cron | null) {
  error.value = null
  allowConcurrent.value = false
  if (c) {
    name.value = c.name
    description.value = c.description
    allowConcurrent.value = c.allow_concurrent
    scheduleType.value = c.schedule_type
    cronExpr.value = c.cron_expr || ''
    onceAt.value = fromRFC3339(c.once_at)
    timezone.value = c.timezone
    actionType.value = c.action_type
    // display_type 还原：若后端标记为 preset，尝试反查匹配的预设 key。
    const dt = (c.display_type || '') as DisplayType
    if (dt === 'preset') {
      displayType.value = 'preset'
      const matched = PRESETS.find(p => p.scheduleType === c.schedule_type && p.expr === c.cron_expr)
      presetKey.value = matched ? matched.key : '1m'
    } else if (dt === 'interval' || dt === 'cron' || dt === 'once') {
      displayType.value = dt
    } else {
      // 兜底：按 schedule_type 推断
      displayType.value = c.schedule_type === 'once' ? 'once' : c.schedule_type === 'cron' ? 'cron' : 'interval'
    }
    // payload 还原
    const p = c.action_payload || {}
    if (c.action_type === 'start_task') {
      stAgentId.value = (p.agent_id as string) || ''
      stSessionId.value = (p.session_id as string) || ''
      stInput.value = (p.input as string) || ''
      stMaxSteps.value = (p.max_steps as number) || 0
      stTimeoutSeconds.value = (p.timeout_seconds as number) || 0
    } else if (c.action_type === 'script') {
      scriptJson.value = p.tool_calls ? JSON.stringify(p.tool_calls, null, 2) : '[]'
    } else if (c.action_type === 'webhook') {
      whMethod.value = (p.method as string) || 'POST'
      whUrl.value = (p.url as string) || ''
      whHeadersJson.value = p.headers ? JSON.stringify(p.headers, null, 2) : '{}'
      whBody.value = (p.body as string) || ''
      whTimeoutSeconds.value = (p.timeout_seconds as number) || 10
    } else if (c.action_type === 'notify_session') {
      nsSessionId.value = (p.session_id as string) || ''
      nsMessage.value = (p.message as string) || ''
      nsEventType.value = (p.event_type as string) || ''
    }
  } else {
    name.value = ''
    description.value = ''
    displayType.value = 'preset'
    presetKey.value = '1m'
    scheduleType.value = 'interval'
    cronExpr.value = '1m'
    onceAt.value = ''
    timezone.value = ''
    actionType.value = 'notify_session'
    stAgentId.value = ''
    stSessionId.value = ''
    stInput.value = ''
    stMaxSteps.value = 0
    stTimeoutSeconds.value = 0
    scriptJson.value = '[]'
    whMethod.value = 'POST'
    whUrl.value = ''
    whHeadersJson.value = '{}'
    whBody.value = ''
    whTimeoutSeconds.value = 10
    nsSessionId.value = ''
    nsMessage.value = ''
    nsEventType.value = ''
  }
  // preset 模式下确保 cronExpr 与 presetKey 同步
  if (displayType.value === 'preset') applyPreset(presetKey.value)
}

watch(
  () => props.visible,
  (visible, prev) => {
    if (visible && !prev) {
      resetForm(props.cronData)
      // 打开时确保 agents / tools 已加载（供 start_task / script 下拉）
      loadAgents().catch(() => {})
      loadAvailableTools().catch(() => {})
    }
  },
)

/** 根据当前 actionType 构造 action_payload，并做字段级校验。返回 { payload, error }。 */
function buildPayload(): { payload: Record<string, unknown> | null; err: string | null } {
  switch (actionType.value) {
    case 'start_task': {
      if (!stAgentId.value.trim()) return { payload: null, err: 'start_task 必须指定 agent_id' }
      const payload: Record<string, unknown> = {
        agent_id: stAgentId.value.trim(),
        input: stInput.value,
      }
      if (stSessionId.value.trim()) payload.session_id = stSessionId.value.trim()
      if (stMaxSteps.value > 0) payload.max_steps = stMaxSteps.value
      if (stTimeoutSeconds.value > 0) payload.timeout_seconds = stTimeoutSeconds.value
      return { payload, err: null }
    }
    case 'script': {
      let calls: unknown
      try {
        calls = JSON.parse(scriptJson.value || '[]')
      } catch {
        return { payload: null, err: 'script 的 tool_calls 不是合法 JSON' }
      }
      if (!Array.isArray(calls)) return { payload: null, err: 'script 的 tool_calls 必须是数组' }
      return { payload: { tool_calls: calls }, err: null }
    }
    case 'webhook': {
      if (!whUrl.value.trim()) return { payload: null, err: 'webhook 必须指定 url' }
      let headers: unknown
      try {
        headers = JSON.parse(whHeadersJson.value || '{}')
      } catch {
        return { payload: null, err: 'webhook headers 不是合法 JSON' }
      }
      const payload: Record<string, unknown> = {
        method: whMethod.value,
        url: whUrl.value.trim(),
        timeout_seconds: whTimeoutSeconds.value || 10,
      }
      if (headers && typeof headers === 'object') payload.headers = headers
      if (whBody.value) payload.body = whBody.value
      return { payload, err: null }
    }
    case 'notify_session': {
      if (!nsSessionId.value.trim()) return { payload: null, err: 'notify_session 必须指定 session_id' }
      const payload: Record<string, unknown> = {
        session_id: nsSessionId.value.trim(),
        message: nsMessage.value,
      }
      if (nsEventType.value.trim()) payload.event_type = nsEventType.value.trim()
      return { payload, err: null }
    }
  }
  return { payload: null, err: '未知 action_type' }
}

/** 构造调度字段，返回 { scheduleType, cronExpr, onceAt, displayType, err }。 */
function buildSchedule(): {
  st: ScheduleType
  expr: string
  once: string
  dt: string
  err: string | null
} {
  if (displayType.value === 'preset' || displayType.value === 'interval') {
    if (!cronExpr.value.trim()) return { st: 'interval', expr: '', once: '', dt: '', err: '间隔不能为空（如 1h / 30s）' }
    return { st: 'interval', expr: cronExpr.value.trim(), once: '', dt: displayType.value, err: null }
  }
  if (displayType.value === 'cron') {
    if (!cronExpr.value.trim()) return { st: 'cron', expr: '', once: '', dt: '', err: 'cron 表达式不能为空' }
    return { st: 'cron', expr: cronExpr.value.trim(), once: '', dt: 'cron', err: null }
  }
  if (displayType.value === 'once') {
    if (!onceAt.value) return { st: 'once', expr: '', once: '', dt: '', err: '请选择触发时间' }
    return { st: 'once', expr: '', once: toRFC3339(onceAt.value), dt: 'once', err: null }
  }
  return { st: scheduleType.value, expr: cronExpr.value, once: '', dt: displayType.value, err: null }
}

/** 校验并提交。 */
function handleSave() {
  if (!name.value.trim()) {
    error.value = '名称不能为空'
    return
  }
  const sched = buildSchedule()
  if (sched.err) {
    error.value = sched.err
    return
  }
  const { payload, err } = buildPayload()
  if (err || !payload) {
    error.value = err
    return
  }

  if (props.cronData) {
    const req: UpdateCronInput = {
      name: name.value.trim(),
      description: description.value.trim(),
      schedule_type: sched.st,
      cron_expr: sched.expr,
      once_at: sched.once,
      timezone: timezone.value,
      display_type: sched.dt,
      action_type: actionType.value,
      action_payload: payload,
      allow_concurrent: allowConcurrent.value,
    }
    emit('save', req)
  } else {
    const req: CreateCronInput = {
      name: name.value.trim(),
      description: description.value.trim(),
      schedule_type: sched.st,
      cron_expr: sched.expr,
      once_at: sched.once,
      timezone: timezone.value,
      display_type: sched.dt,
      action_type: actionType.value,
      action_payload: payload,
      allow_concurrent: allowConcurrent.value,
      source: 'user',
    }
    emit('save', req)
  }
}

function handleClose() {
  emit('close')
}

/** 给 script JSON 编辑器填充一个示例 tool_call，降低上手门槛。 */
function appendScriptExample() {
  const toolName = availableTools.value[0]?.name || 'run_shell'
  const example = { tool: toolName, input: { command: 'echo hello' } }
  try {
    const arr = JSON.parse(scriptJson.value || '[]') as unknown[]
    arr.push(example)
    scriptJson.value = JSON.stringify(arr, null, 2)
  } catch {
    scriptJson.value = JSON.stringify([example], null, 2)
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="modal-overlay" @click.self="handleClose">
        <div class="modal-content">
          <div class="modal-header">
            <h2 class="modal-title">{{ modalTitle }}</h2>
            <button class="modal-close-btn" @click="handleClose" title="关闭">✕</button>
          </div>

          <div class="modal-body">
            <div v-if="error" class="form-error">{{ error }}</div>

            <!-- 基础信息 -->
            <div class="form-field">
              <label for="cron-name">名称 <span class="required">*</span></label>
              <input id="cron-name" v-model="name" type="text" placeholder="如：每日晨报推送" />
            </div>
            <div class="form-field">
              <label for="cron-desc">描述</label>
              <input id="cron-desc" v-model="description" type="text" placeholder="可选" />
            </div>

            <!-- 调度规则 -->
            <div class="form-field">
              <label>调度类型</label>
              <div class="seg-bar">
                <button
                  v-for="dt in (['preset','interval','cron','once'] as DisplayType[])"
                  :key="dt"
                  type="button"
                  class="seg-btn"
                  :class="{ active: displayType === dt }"
                  @click="onDisplayTypeChange(dt)"
                >{{ { preset: '预设', interval: '间隔', cron: 'Cron', once: '一次' }[dt] }}</button>
              </div>
            </div>

            <div v-if="displayType === 'preset'" class="form-field">
              <label for="cron-preset">预设方案</label>
              <select id="cron-preset" :value="presetKey" @change="onPresetChange(($event.target as HTMLSelectElement).value)">
                <option v-for="p in PRESETS" :key="p.key" :value="p.key">{{ p.label }}</option>
              </select>
            </div>

            <div v-else-if="displayType === 'interval'" class="form-field">
              <label for="cron-interval">间隔表达式</label>
              <input id="cron-interval" v-model="cronExpr" type="text" placeholder="如 30s / 5m / 1h" />
              <p class="field-help">支持 Go duration 格式：s / m / h。</p>
            </div>

            <div v-else-if="displayType === 'cron'" class="form-field">
              <label for="cron-expr">Cron 表达式（6 域秒级）</label>
              <input id="cron-expr" v-model="cronExpr" type="text" placeholder="秒 分 时 日 月 周，如 0 0 0 * * *" />
              <p class="field-help">格式：秒 分 时 日 月 周。每天 0 点 = <code>0 0 0 * * *</code>。</p>
            </div>

            <div v-else-if="displayType === 'once'" class="form-field">
              <label for="cron-once">触发时间</label>
              <input id="cron-once" v-model="onceAt" type="datetime-local" />
              <p class="field-help">到点触发一次后自动移除。</p>
            </div>

            <div class="form-field">
              <label for="cron-tz">时区（可选）</label>
              <input id="cron-tz" v-model="timezone" type="text" placeholder="如 Asia/Shanghai，空=服务器本地" />
            </div>

            <!-- 触发动作 -->
            <div class="form-field">
              <label for="cron-action">触发动作</label>
              <select id="cron-action" v-model="actionType">
                <option value="notify_session">通知 Session（notify_session）</option>
                <option value="start_task">启动 Agent Task（start_task）</option>
                <option value="script">脚本工具链（script）</option>
                <option value="webhook">Webhook 回调（webhook）</option>
              </select>
            </div>

            <!-- start_task payload -->
            <div v-if="actionType === 'start_task'" class="payload-block">
              <div class="form-field">
                <label for="st-agent">Agent <span class="required">*</span></label>
                <select id="st-agent" v-model="stAgentId">
                  <option value="">请选择</option>
                  <option v-for="a in agents" :key="a.id" :value="a.id">{{ a.name }} ({{ a.id }})</option>
                </select>
              </div>
              <div class="form-field">
                <label for="st-session">Session ID（可选，空=新建）</label>
                <input id="st-session" v-model="stSessionId" type="text" placeholder="复用已有 session" />
              </div>
              <div class="form-field">
                <label for="st-input">输入内容</label>
                <textarea id="st-input" v-model="stInput" rows="3" placeholder="发送给 Agent 的输入，支持模板占位符"></textarea>
              </div>
              <div class="form-row">
                <div class="form-field">
                  <label for="st-maxsteps">最大步数（可选）</label>
                  <input id="st-maxsteps" v-model.number="stMaxSteps" type="number" min="0" />
                </div>
                <div class="form-field">
                  <label for="st-timeout">超时秒数（可选）</label>
                  <input id="st-timeout" v-model.number="stTimeoutSeconds" type="number" min="0" />
                </div>
              </div>
            </div>

            <!-- script payload -->
            <div v-else-if="actionType === 'script'" class="payload-block">
              <div class="form-field">
                <label for="script-json">tool_calls（JSON 数组）</label>
                <textarea id="script-json" v-model="scriptJson" rows="6" class="code-area"
                  placeholder='[{"tool":"run_shell","input":{"command":"echo hi"}}]'></textarea>
                <p class="field-help">按顺序执行；tool 名必须在服务端白名单内。
                  <button type="button" class="link-btn" @click="appendScriptExample">+ 插入示例</button>
                </p>
              </div>
            </div>

            <!-- webhook payload -->
            <div v-else-if="actionType === 'webhook'" class="payload-block">
              <div class="form-row">
                <div class="form-field">
                  <label for="wh-method">Method</label>
                  <select id="wh-method" v-model="whMethod">
                    <option>GET</option><option>POST</option><option>PUT</option><option>DELETE</option><option>PATCH</option>
                  </select>
                </div>
                <div class="form-field">
                  <label for="wh-url">URL <span class="required">*</span></label>
                  <input id="wh-url" v-model="whUrl" type="text" placeholder="https://example.com/hook" />
                </div>
              </div>
              <div class="form-field">
                <label for="wh-headers">Headers（JSON 对象）</label>
                <textarea id="wh-headers" v-model="whHeadersJson" rows="3" class="code-area"
                  placeholder='{"Authorization":"Bearer xxx"}'></textarea>
              </div>
              <div class="form-field">
                <label for="wh-body">Body</label>
                <textarea id="wh-body" v-model="whBody" rows="3" placeholder="请求体（支持模板占位符）"></textarea>
              </div>
              <div class="form-field">
                <label for="wh-timeout">超时秒数</label>
                <input id="wh-timeout" v-model.number="whTimeoutSeconds" type="number" min="1" />
              </div>
            </div>

            <!-- notify_session payload -->
            <div v-else-if="actionType === 'notify_session'" class="payload-block">
              <div class="form-field">
                <label for="ns-session">Session ID <span class="required">*</span></label>
                <input id="ns-session" v-model="nsSessionId" type="text" placeholder="目标 session" />
              </div>
              <div class="form-field">
                <label for="ns-message">消息内容</label>
                <textarea id="ns-message" v-model="nsMessage" rows="3"
                  placeholder="支持模板：{{.Count}} {{.Now}} {{.CronName}}"></textarea>
              </div>
              <div class="form-field">
                <label for="ns-event">事件类型（可选）</label>
                <input id="ns-event" v-model="nsEventType" type="text" placeholder="默认 cron_notification" />
              </div>
            </div>

            <!-- 并发开关 -->
            <div class="form-field checkbox-field">
              <label>
                <input v-model="allowConcurrent" type="checkbox" />
                <span>允许并发执行（默认关闭：上一轮仍在跑则跳过）</span>
              </label>
            </div>
          </div>

          <div class="modal-footer">
            <button class="modal-cancel-btn" @click="handleClose">取消</button>
            <button class="modal-save-btn" @click="handleSave">保存</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.modal-overlay {
  position: fixed; inset: 0;
  background: rgba(0, 0, 0, 0.6);
  backdrop-filter: blur(4px);
  display: flex; align-items: center; justify-content: center;
  z-index: 10000; padding: 20px;
}
.modal-content {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: 12px;
  max-width: 600px; width: 100%; max-height: 90vh;
  display: flex; flex-direction: column;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}
.modal-header {
  display: flex; justify-content: space-between; align-items: center;
  padding: 16px 20px; border-bottom: 1px solid var(--bg-elevated);
}
.modal-title { font-size: 16px; font-weight: 700; color: var(--text-primary); margin: 0; }
.modal-close-btn {
  background: none; border: none; color: var(--text-secondary);
  font-size: 18px; cursor: pointer; padding: 4px 8px; border-radius: 4px;
  transition: color 0.2s, background 0.2s;
}
.modal-close-btn:hover { color: var(--text-primary); background: var(--bg-elevated); }
.modal-body { padding: 16px 20px; overflow-y: auto; flex: 1; }
.modal-footer {
  display: flex; justify-content: flex-end; gap: 10px;
  padding: 14px 20px; border-top: 1px solid var(--bg-elevated);
}
.form-error {
  background: rgba(255, 77, 77, 0.1);
  border: 1px solid rgba(255, 77, 77, 0.3);
  color: var(--accent-danger);
  font-size: 12px; padding: 8px 10px; border-radius: 6px; margin-bottom: 12px;
}
.form-field { display: flex; flex-direction: column; gap: 4px; margin-bottom: 12px; }
.form-field label {
  font-size: 11px; color: var(--text-secondary);
  font-weight: 600; text-transform: uppercase; letter-spacing: 0.3px;
}
.form-field label .required { color: var(--accent-danger); }
.form-field input,
.form-field select,
.form-field textarea {
  background: var(--bg-canvas);
  border: 1px solid var(--bg-elevated);
  border-radius: 6px; padding: 8px 10px;
  color: var(--text-primary); font-size: 13px; outline: none;
  transition: border-color 0.2s; font-family: inherit;
}
.form-field input:focus,
.form-field select:focus,
.form-field textarea:focus { border-color: var(--accent-running); }
.form-field textarea { resize: vertical; line-height: 1.4; }
.code-area { font-family: var(--font-mono); }
.form-row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.field-help { font-size: 11px; color: var(--text-secondary); margin: 2px 0 0; line-height: 1.4; }
.field-help code { background: var(--bg-elevated); padding: 1px 4px; border-radius: 3px; }
.link-btn {
  background: none; border: none; color: var(--accent-running);
  cursor: pointer; font-size: 11px; padding: 0; text-decoration: underline;
}
.seg-bar { display: flex; gap: 6px; }
.seg-btn {
  flex: 1; background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: 6px; padding: 8px 0;
  color: var(--text-secondary); font-size: 12px; font-weight: 600;
  cursor: pointer; transition: all 0.15s;
}
.seg-btn:hover { color: var(--text-primary); border-color: var(--border-active); }
.seg-btn.active {
  background: rgba(0, 229, 255, 0.12);
  border-color: var(--accent-running);
  color: var(--accent-running);
}
.payload-block {
  padding: 12px; margin-bottom: 12px;
  background: var(--bg-canvas);
  border: 1px solid var(--bg-elevated);
  border-radius: 8px;
}
.checkbox-field label {
  flex-direction: row; align-items: center; gap: 8px;
  text-transform: none; letter-spacing: normal; font-weight: 400;
  font-size: 13px; color: var(--text-primary); cursor: pointer;
}
.checkbox-field input[type='checkbox'] {
  width: 16px; height: 16px; accent-color: var(--accent-running); cursor: pointer;
}
.modal-cancel-btn {
  padding: 8px 20px; background: var(--bg-elevated); color: var(--text-primary);
  border: 1px solid var(--border-default); border-radius: 6px;
  font-size: 13px; cursor: pointer; transition: background 0.2s;
}
.modal-cancel-btn:hover { background: var(--border-default); }
.modal-save-btn {
  padding: 8px 24px; background: var(--accent-running); color: var(--text-on-accent);
  border: none; border-radius: 6px; font-size: 13px; font-weight: 600;
  cursor: pointer; transition: filter 0.2s;
}
.modal-save-btn:hover { filter: brightness(1.1); }
.modal-enter-active, .modal-leave-active { transition: all 0.25s ease; }
.modal-enter-from, .modal-leave-to { opacity: 0; }
.modal-enter-from .modal-content, .modal-leave-to .modal-content { transform: scale(0.95); }
</style>
