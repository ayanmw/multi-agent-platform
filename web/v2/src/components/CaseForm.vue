<!-- CaseForm — modal form to create or edit a custom case
     Props:
       caseData: an existing Case to edit, or null to create a new case
       visible: whether the modal is shown

     Emits:
       close: user cancelled or closed the modal
       save: the form is valid and should be persisted.
             Emits CreateCaseRequest when creating, UpdateCaseRequest when editing.

     Behavior:
       - Resets form fields from caseData when opened (visible false -> true)
       - Validates that name and category are non-empty
       - Tags are edited via chips: type to search existing tags or create new ones by pressing Enter
       - Acceptance criteria are edited as structured rows (type, target, description)
-->
<script setup lang="ts">
import { ref, watch, computed, nextTick } from 'vue'
import type { Case, CreateCaseRequest, UpdateCaseRequest, TaskContract } from '../types/case'
import { useCaseStore } from '../composables/useCaseStore'
import { useClickOutside } from '../composables/useClickOutside'

const props = defineProps<{
  caseData: Case | null
  visible: boolean
}>()

const emit = defineEmits<{
  close: []
  save: [req: CreateCaseRequest | UpdateCaseRequest]
}>()

const store = useCaseStore()

// 20 个预设 emoji 图标，禁止自由输入
const ICON_OPTIONS = [
  '🚀', '🧩', '⚙️', '📝', '🔍', '💡', '🐛', '🔧', '🧪', '📊',
  '🎨', '🗂️', '🌐', '🔒', '⏱️', '✅', '📦', '🧠', '🔄', '🎯',
]

// 5 项权限的中文标签，与 TaskContract.permissions 键名完全一致
const PERMISSION_LABELS: { key: keyof NonNullable<TaskContract['permissions']>; label: string }[] = [
  { key: 'allow_network', label: '允许访问网络' },
  { key: 'allow_file_delete', label: '允许删除文件' },
  { key: 'allow_file_write', label: '允许写入文件' },
  { key: 'allow_shell', label: '允许执行 shell 命令' },
  { key: 'allow_shell_dangerous', label: '允许执行危险 shell 命令' },
]

const name = ref('')
const category = ref('')
const icon = ref('')
const description = ref('')
const systemPrompt = ref('')
const defaultInput = ref('')
const maxSteps = ref(10)
const goal = ref('')

const error = ref<string | null>(null)

const permissions = ref({
  allow_network: false,
  allow_file_delete: false,
  allow_file_write: false,
  allow_shell: false,
  allow_shell_dangerous: false,
})

interface CriterionRow {
  id: string
  type: string
  target: string
  description: string
}

/**
 * 生成唯一 id：优先使用 crypto.randomUUID（需 secure context），
 * 否则回退到时间戳 + 随机数，避免在非 HTTPS / 内网 IP 环境下抛错导致组件挂载失败。
 */
function makeId(): string {
  const g: { crypto?: Crypto } =
    (typeof globalThis !== 'undefined' ? (globalThis as unknown as { crypto?: Crypto }) : {}) ||
    (typeof window !== 'undefined' ? (window as unknown as { crypto?: Crypto }) : {})
  const c: Crypto | undefined = g.crypto
  if (c && typeof c.randomUUID === 'function') {
    try {
      return c.randomUUID()
    } catch {
      /* fall through */
    }
  }
  return `c_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`
}

const criteria = ref<CriterionRow[]>([{ id: makeId(), type: '', target: '', description: '' }])

function addCriterion() {
  criteria.value.push({ id: makeId(), type: '', target: '', description: '' })
}

function removeCriterion(index: number) {
  if (criteria.value.length === 1) {
    criteria.value[0].type = ''
    criteria.value[0].target = ''
    criteria.value[0].description = ''
  } else {
    criteria.value.splice(index, 1)
  }
}

const categorySearch = ref('')
const categoryOpen = ref(false)
const categoryWrapRef = useClickOutside(() => { categoryOpen.value = false })
const categoryPanelRef = ref<HTMLElement | null>(null)
const categoryInputRef = ref<HTMLInputElement | null>(null)

const tags = ref<string[]>([])
const tagSearch = ref('')
const tagOpen = ref(false)
const tagWrapRef = useClickOutside(() => { tagOpen.value = false })
const tagInputEl = ref<HTMLInputElement | null>(null)
const tagPanelRef = ref<HTMLElement | null>(null)

const iconOpen = ref(false)
const iconWrapRef = useClickOutside(() => { iconOpen.value = false })
const iconGridRef = ref<HTMLElement | null>(null)

/*
 * 计算图标选择网格在 viewport 中的固定位置。
 * 网格会被 <Teleport> 移动到 body 下，脱离 .modal-body 的剪裁上下文，
 * 因此需要用 fixed 定位并基于 icon trigger 的 getBoundingClientRect() 计算坐标。
 */
function updateIconGridPosition() {
  const wrap = iconWrapRef.value
  const grid = iconGridRef.value
  if (!wrap || !grid) return
  const rect = wrap.getBoundingClientRect()
  grid.style.setProperty('--icon-grid-top', `${rect.bottom}px`)
  grid.style.setProperty('--icon-grid-left', `${rect.left}px`)
  grid.style.setProperty('--icon-grid-width', `${rect.width}px`)
}

watch(iconOpen, async (open) => {
  if (open) {
    await nextTick()
    updateIconGridPosition()
  }
})

function selectIcon(value: string) {
  icon.value = value
  iconOpen.value = false
}

const availableTags = computed(() => {
  const q = tagSearch.value.trim().toLowerCase()
  return store.allTags.value
    .filter(t => !tags.value.some(existing => existing.toLowerCase() === t.toLowerCase()))
    .filter(t => (q ? t.toLowerCase().includes(q) : true))
})

function selectTag(value: string) {
  const lower = value.toLowerCase()
  if (!tags.value.some(t => t.toLowerCase() === lower)) tags.value.push(value)
  tagSearch.value = ''
  tagInputEl.value?.focus()
}

function addCustomTag() {
  const q = tagSearch.value.trim()
  if (!q) return
  selectTag(q)
}

function removeTag(value: string) {
  tags.value = tags.value.filter(t => t !== value)
}

function updateTagDropdownPosition() {
  const wrap = tagWrapRef.value
  const panel = tagPanelRef.value
  if (!wrap || !panel) return
  const rect = wrap.getBoundingClientRect()
  panel.style.setProperty('--dropdown-top', `${rect.bottom}px`)
  panel.style.setProperty('--dropdown-left', `${rect.left}px`)
  panel.style.setProperty('--dropdown-width', `${rect.width}px`)
  panel.style.setProperty('--dropdown-max-height', `${Math.min(220, window.innerHeight - rect.bottom - 8)}px`)
}

watch(tagOpen, async (open) => {
  if (open) {
    await nextTick()
    updateTagDropdownPosition()
  }
})

function onTagKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    tagOpen.value = false
    return
  }
  if (e.key !== 'Enter') return
  e.preventDefault()
  const q = tagSearch.value.trim()
  const alreadyExists = q && tags.value.some(t => t.toLowerCase() === q.toLowerCase())
  if (q && !alreadyExists) {
    selectTag(q)
  } else if (availableTags.value.length > 0) {
    selectTag(availableTags.value[0])
  }
}

const filteredCategories = computed(() => {
  const q = categorySearch.value.trim().toLowerCase()
  if (!q) return store.allCategories.value
  return store.allCategories.value.filter((c: string) => c.toLowerCase().includes(q))
})

const canAddCategory = computed(() => {
  const q = categorySearch.value.trim()
  return q && !store.allCategories.value.some((c: string) => c.toLowerCase() === q.toLowerCase())
})

function selectCategory(value: string) {
  category.value = value
  categorySearch.value = value
  categoryOpen.value = false
}

function addCustomCategory() {
  const q = categorySearch.value.trim()
  if (!q) return
  selectCategory(q)
}

function onCategoryKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    categoryOpen.value = false
    return
  }
  // Enter 仅在 dropdown 打开时创建/选择分类，避免表单提交被意外拦截
  if (e.key !== 'Enter') return
  if (!categoryOpen.value) return
  e.preventDefault()
  if (canAddCategory.value) {
    addCustomCategory()
  } else if (filteredCategories.value.length > 0) {
    selectCategory(filteredCategories.value[0])
  }
}

/*
 * 计算分类下拉面板在 viewport 中的固定位置。
 * 下拉面板会被 <Teleport> 移动到 body 下，脱离 .modal-body 的剪裁上下文，
 * 因此需要用 fixed 定位并基于 category input 的 getBoundingClientRect() 计算坐标。
 */
function updateCategoryDropdownPosition() {
  const wrap = categoryWrapRef.value
  const panel = categoryPanelRef.value
  if (!wrap || !panel) return
  const rect = wrap.getBoundingClientRect()
  panel.style.setProperty('--dropdown-top', `${rect.bottom}px`)
  panel.style.setProperty('--dropdown-left', `${rect.left}px`)
  panel.style.setProperty('--dropdown-width', `${rect.width}px`)
  panel.style.setProperty('--dropdown-max-height', `${Math.min(220, window.innerHeight - rect.bottom - 8)}px`)
}

watch(categoryOpen, async (open) => {
  if (open) {
    await nextTick()
    updateCategoryDropdownPosition()
    // input focus 后弹出的 dropdown 需要定位；input 自身保持聚焦。
    categoryInputRef.value?.focus()
  }
})

const isEditing = computed(() => props.caseData !== null)
const modalTitle = computed(() => (isEditing.value ? '编辑 Case' : '新建 Case'))

/** Reset form fields from the provided case or default values */
function resetForm(c: Case | null) {
  if (c) {
    name.value = c.name
    category.value = c.category
    categorySearch.value = c.category
    icon.value = c.icon
    description.value = c.description
    systemPrompt.value = c.system_prompt
    defaultInput.value = c.default_input
    maxSteps.value = c.contract.max_steps
    tags.value = [...c.tags]
    goal.value = c.contract.goal
    permissions.value = {
      allow_network: c.contract.permissions?.allow_network ?? false,
      allow_file_delete: c.contract.permissions?.allow_file_delete ?? false,
      allow_file_write: c.contract.permissions?.allow_file_write ?? false,
      allow_shell: c.contract.permissions?.allow_shell ?? false,
      allow_shell_dangerous: c.contract.permissions?.allow_shell_dangerous ?? false,
    }
    criteria.value =
      c.contract.acceptance_criteria && c.contract.acceptance_criteria.length > 0
        ? c.contract.acceptance_criteria.map(ac => ({
            id: makeId(),
            type: ac.type,
            target: ac.target,
            description: ac.description ?? '',
          }))
        : [{ id: makeId(), type: '', target: '', description: '' }]
  } else {
    name.value = ''
    category.value = ''
    categorySearch.value = ''
    icon.value = ''
    description.value = ''
    systemPrompt.value = ''
    defaultInput.value = ''
    maxSteps.value = 10
    tags.value = []
    goal.value = ''
    permissions.value = {
      allow_network: false,
      allow_file_delete: false,
      allow_file_write: false,
      allow_shell: false,
      allow_shell_dangerous: false,
    }
    criteria.value = [{ id: makeId(), type: '', target: '', description: '' }]
  }
  categoryOpen.value = false
  tagOpen.value = false
  iconOpen.value = false
  tagSearch.value = ''
  error.value = null
}

watch(
  () => props.visible,
  (visible, prevVisible) => {
    if (visible && !prevVisible) {
      resetForm(props.caseData)
    }
  }
)

/** Validate and emit save event */
function handleSave() {
  if (!name.value.trim()) {
    error.value = '名称不能为空'
    return
  }
  if (!category.value.trim()) {
    error.value = '分类不能为空'
    return
  }

  const contract = {
    goal: goal.value.trim(),
    max_steps: maxSteps.value,
    permissions: { ...permissions.value },
    acceptance_criteria: criteria.value
      .map(ac => ({
        type: ac.type.trim(),
        target: ac.target.trim(),
        description: ac.description.trim(),
      }))
      .filter(ac => ac.type && ac.target),
  }

  const formTags = [...tags.value]

  if (props.caseData) {
    const req: UpdateCaseRequest = {
      name: name.value.trim(),
      category: category.value.trim(),
      icon: icon.value.trim() || undefined,
      description: description.value.trim() || undefined,
      system_prompt: systemPrompt.value.trim() || undefined,
      default_input: defaultInput.value.trim() || undefined,
      contract,
      tags: formTags,
    }
    emit('save', req)
  } else {
    const req: CreateCaseRequest = {
      name: name.value.trim(),
      category: category.value.trim(),
      icon: icon.value.trim() || undefined,
      description: description.value.trim() || undefined,
      system_prompt: systemPrompt.value.trim() || undefined,
      default_input: defaultInput.value.trim() || undefined,
      contract,
      tags: formTags,
    }
    emit('save', req)
  }
}

function handleClose() {
  emit('close')
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

            <div class="form-grid">
              <div class="form-field">
                <label for="case-name">名称 <span class="required">*</span></label>
                <input id="case-name" v-model="name" type="text" placeholder="请输入 Case 名称" />
              </div>

              <div class="form-field category-field" ref="categoryWrapRef">
                <label for="case-category">分类 <span class="required">*</span></label>
                <input
                  id="case-category"
                  ref="categoryInputRef"
                  v-model="categorySearch"
                  type="text"
                  role="combobox"
                  aria-haspopup="listbox"
                  :aria-expanded="categoryOpen"
                  aria-autocomplete="list"
                  aria-controls="category-listbox"
                  placeholder="搜索已有分类，或输入后按回车新增"
                  @input="categoryOpen = true; category = categorySearch.trim()"
                  @focus="categoryOpen = true"
                  @keydown="onCategoryKeydown"
                />
                <Teleport to="body">
                  <div
                    v-if="categoryOpen"
                    id="category-listbox"
                    ref="categoryPanelRef"
                    role="listbox"
                    class="dropdown-panel"
                  >
                    <div
                      v-for="cat in filteredCategories"
                      :key="cat"
                      role="option"
                      :aria-selected="cat === category"
                      class="dropdown-item"
                      @mousedown.prevent="selectCategory(cat)"
                    >
                      {{ cat }}
                    </div>
                    <div
                      v-if="canAddCategory"
                      role="option"
                      aria-selected="false"
                      class="dropdown-item add-item"
                      @mousedown.prevent="addCustomCategory"
                    >
                      新增“{{ categorySearch.trim() }}”
                    </div>
                    <div v-if="filteredCategories.length === 0 && !canAddCategory" class="dropdown-empty">
                      暂无分类，输入后按回车新增
                    </div>
                  </div>
                </Teleport>
              </div>

              <div class="form-field icon-field" ref="iconWrapRef">
                <label>图标</label>
                <button
                  type="button"
                  class="icon-trigger"
                  aria-haspopup="grid"
                  :aria-expanded="iconOpen"
                  :aria-label="`选择图标，当前：${icon || '无'}`"
                  @click="iconOpen = !iconOpen"
                  @keydown.esc="iconOpen = false"
                >
                  <span v-if="icon" class="icon-selected">{{ icon }}</span>
                  <span v-else class="icon-placeholder">点击选择 emoji 图标</span>
                </button>
                <Teleport to="body">
                  <div
                    v-if="iconOpen"
                    ref="iconGridRef"
                    class="icon-grid"
                    role="grid"
                    tabindex="-1"
                    @mousedown.stop
                    @keydown.esc="iconOpen = false"
                  >
                    <button
                      v-for="emoji in ICON_OPTIONS"
                      :key="emoji"
                      type="button"
                      class="icon-btn"
                      :class="{ active: icon === emoji }"
                      :aria-label="`选择图标 ${emoji}`"
                      :title="emoji"
                      @click="selectIcon(emoji)"
                    >
                      {{ emoji }}
                    </button>
                  </div>
                </Teleport>
              </div>

              <div class="form-field">
                <label for="case-max-steps">最大步数</label>
                <input id="case-max-steps" v-model.number="maxSteps" type="number" min="1" />
              </div>
            </div>

            <div class="form-field tags-field" ref="tagWrapRef">
              <label for="case-tags">标签</label>
              <div class="tags-input" @click="tagInputEl?.focus()">
                <span v-for="tag in tags" :key="tag" class="tag-chip">
                  {{ tag }}
                  <button type="button" class="tag-remove" @click.stop="removeTag(tag)" title="删除标签">✕</button>
                </span>
                <input
                  id="case-tags"
                  ref="tagInputEl"
                  v-model="tagSearch"
                  type="text"
                  role="combobox"
                  aria-haspopup="listbox"
                  :aria-expanded="tagOpen"
                  aria-autocomplete="list"
                  aria-controls="tag-listbox"
                  placeholder="搜索已有标签，或输入新标签后按回车"
                  @input="tagOpen = true"
                  @focus="tagOpen = true"
                  @keydown="onTagKeydown"
                />
              </div>
              <Teleport to="body">
                <div
                  v-if="tagOpen"
                  id="tag-listbox"
                  ref="tagPanelRef"
                  role="listbox"
                  class="dropdown-panel"
                >
                  <div
                    v-for="tag in availableTags"
                    :key="tag"
                    role="option"
                    :aria-selected="tag === tagSearch.trim()"
                    class="dropdown-item"
                    @mousedown.prevent="selectTag(tag)"
                  >
                    {{ tag }}
                  </div>
                  <div
                    v-if="tagSearch.trim() && !tags.some(t => t.toLowerCase() === tagSearch.trim().toLowerCase()) && availableTags.length === 0"
                    role="option"
                    aria-selected="false"
                    class="dropdown-item add-item"
                    @mousedown.prevent="addCustomTag"
                  >
                    新增“{{ tagSearch.trim() }}”
                  </div>
                  <div
                    v-if="availableTags.length === 0 && (!tagSearch.trim() || tags.some(t => t.toLowerCase() === tagSearch.trim().toLowerCase()))"
                    class="dropdown-empty"
                  >
                    暂无可用标签
                  </div>
                </div>
              </Teleport>
            </div>

            <div class="form-field">
              <label for="case-goal">任务目标</label>
              <input
                id="case-goal"
                v-model="goal"
                type="text"
                placeholder="例如：生成一个可独立运行的 Go HTTP 服务，提供 /hello 接口"
              />
              <p class="field-help">用于 system prompt，定义任务的总体目标。</p>
            </div>

            <div class="form-field">
              <label for="case-description">描述</label>
              <textarea id="case-description" v-model="description" rows="3" placeholder="简短描述该 Case 的用途"></textarea>
            </div>

            <div class="form-field">
              <label for="case-system-prompt">系统提示词</label>
              <textarea
                id="case-system-prompt"
                v-model="systemPrompt"
                rows="4"
                placeholder="发送给 Agent 的系统提示词"
              ></textarea>
            </div>

            <div class="form-field">
              <label for="case-default-input">默认输入</label>
              <textarea
                id="case-default-input"
                v-model="defaultInput"
                rows="3"
                placeholder="用户默认输入内容"
              ></textarea>
            </div>

            <div class="form-field permissions-field">
              <label>权限配置</label>
              <div class="permissions-grid">
                <label v-for="p in PERMISSION_LABELS" :key="p.key" class="permission-row">
                  <input v-model="permissions[p.key]" type="checkbox" />
                  <span>{{ p.label }}</span>
                </label>
              </div>
            </div>

            <div class="form-field criteria-field">
              <label>验收标准</label>
              <p class="field-help">
                用于评估 Agent 任务是否完成。每条由 type（评估类型）、target（评估目标）、description（补充说明）组成。
                示例：exact_match | hello.go | 文件包含 /hello 路由处理函数。
              </p>
              <div
                v-for="(ac, idx) in criteria"
                :key="ac.id"
                class="criterion-row"
              >
                <input v-model="ac.type" placeholder="类型，如 exact_match" />
                <input v-model="ac.target" placeholder="目标，如 hello.go" />
                <input v-model="ac.description" placeholder="补充说明（可选）" />
                <button
                  type="button"
                  class="criterion-remove"
                  @click="removeCriterion(idx)"
                  :title="criteria.length === 1 ? '清空此条' : '删除此条'"
                >✕</button>
              </div>
              <button type="button" class="add-criterion-btn" @click="addCriterion">+ 新增一条</button>
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
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10000;
  padding: 20px;
}

.modal-content {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  border-radius: 12px;
  max-width: 640px;
  width: 100%;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.4);
}

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  border-bottom: 1px solid var(--bg-elevated);
}

.modal-title {
  font-size: 16px;
  font-weight: 700;
  color: var(--text-primary);
  margin: 0;
}

.modal-close-btn {
  background: none;
  border: none;
  color: var(--text-secondary);
  font-size: 18px;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  transition: color 0.2s, background 0.2s;
}

.modal-close-btn:hover {
  color: var(--text-primary);
  background: var(--bg-elevated);
}

.modal-body {
  padding: 16px 20px;
  overflow-y: auto;
  flex: 1;
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 14px 20px;
  border-top: 1px solid var(--bg-elevated);
}

.form-error {
  background: rgba(255, 77, 77, 0.1);
  border: 1px solid rgba(255, 77, 77, 0.3);
  color: var(--accent-danger);
  font-size: 12px;
  padding: 8px 10px;
  border-radius: 6px;
  margin-bottom: 12px;
}

.form-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 12px;
}

.form-field label {
  font-size: 11px;
  color: var(--text-secondary);
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.3px;
}

.form-field label .required {
  color: var(--accent-danger);
}

.form-field label .hint {
  text-transform: none;
  color: var(--text-muted);
  font-weight: 400;
  margin-left: 4px;
}

.form-field input,
.form-field textarea {
  background: var(--bg-canvas);
  border: 1px solid var(--bg-elevated);
  border-radius: 6px;
  padding: 8px 10px;
  color: var(--text-primary);
  font-size: 13px;
  outline: none;
  transition: border-color 0.2s;
  font-family: inherit;
}

.form-field input:focus,
.form-field textarea:focus {
  border-color: var(--accent-running);
}

.form-field textarea {
  resize: vertical;
  line-height: 1.4;
}

.modal-cancel-btn {
  padding: 8px 20px;
  background: var(--bg-elevated);
  color: var(--text-primary);
  border: 1px solid var(--border-default);
  border-radius: 6px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s;
}

.modal-cancel-btn:hover {
  background: var(--border-default);
}

.modal-save-btn {
  padding: 8px 24px;
  background: var(--accent-running);
  color: var(--text-on-accent);
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s, filter 0.2s;
}

.modal-save-btn:hover {
  background: var(--accent-running);
  filter: brightness(1.1);
}

.modal-enter-active,
.modal-leave-active {
  transition: all 0.25s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .modal-content,
.modal-leave-to .modal-content {
  transform: scale(0.95);
}

.category-field {
  position: relative;
}

.dropdown-panel {
  position: fixed;
  top: var(--dropdown-top, auto);
  left: var(--dropdown-left, auto);
  width: var(--dropdown-width, auto);
  max-height: var(--dropdown-max-height, 200px);
  background: var(--bg-canvas);
  border: 1px solid var(--border-default);
  border-radius: 6px;
  overflow-y: auto;
  z-index: 10001;
  box-shadow: 0 8px 16px rgba(0, 0, 0, 0.35);
}

.dropdown-item {
  padding: 8px 10px;
  font-size: 13px;
  color: var(--text-primary);
  cursor: pointer;
  transition: background 0.15s;
}

.dropdown-item:hover {
  background: var(--bg-elevated);
}

.dropdown-item.add-item {
  color: var(--accent-running);
  font-weight: 600;
}

.dropdown-empty {
  padding: 10px;
  font-size: 12px;
  color: var(--text-muted);
}

.tags-field {
  position: relative;
}

.tags-input {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  background: var(--bg-canvas);
  border: 1px solid var(--bg-elevated);
  border-radius: 6px;
  padding: 5px 8px;
  min-height: 36px;
  cursor: text;
}

.tags-input:focus-within {
  border-color: var(--accent-running);
}

.tags-input input {
  flex: 1;
  min-width: 100px;
  background: transparent;
  border: none;
  padding: 4px 2px;
  color: var(--text-primary);
  font-size: 13px;
  outline: none;
}

.tag-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: var(--bg-elevated);
  color: var(--text-primary);
  font-size: 12px;
  padding: 3px 8px;
  border-radius: 4px;
}

.tag-remove {
  background: none;
  border: none;
  color: var(--text-secondary);
  font-size: 11px;
  cursor: pointer;
  padding: 0;
  line-height: 1;
}

.tag-remove:hover {
  color: var(--accent-danger);
}

.icon-field {
  position: relative;
}

.icon-trigger {
  width: 100%;
  background: var(--bg-canvas);
  border: 1px solid var(--bg-elevated);
  border-radius: 6px;
  padding: 8px 10px;
  color: var(--text-primary);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.2s;
}

.icon-trigger:hover {
  border-color: var(--accent-running);
}

.icon-selected {
  font-size: 18px;
}

.icon-placeholder {
  color: var(--text-secondary);
}

.icon-grid {
  position: fixed;
  top: var(--icon-grid-top, auto);
  left: var(--icon-grid-left, auto);
  width: var(--icon-grid-width, auto);
  min-width: 320px;
  display: grid;
  grid-template-columns: repeat(10, minmax(32px, 1fr));
  gap: 6px;
  background: var(--bg-canvas);
  border: 1px solid var(--border-default);
  border-radius: 6px;
  padding: 8px;
  z-index: 10001;
  box-shadow: 0 8px 16px rgba(0, 0, 0, 0.35);
}

.icon-btn {
  background: transparent;
  border: 1px solid transparent;
  border-radius: 4px;
  font-size: 18px;
  padding: 4px;
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.icon-btn:hover {
  background: var(--bg-elevated);
}

.icon-btn.active {
  background: rgba(0, 229, 255, 0.15);
  border-color: var(--accent-running);
}

.permissions-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 8px 16px;
  background: var(--bg-canvas);
  border: 1px solid var(--bg-elevated);
  border-radius: 6px;
  padding: 12px;
}

.permission-row {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-primary);
  font-size: 12px;
  cursor: pointer;
  text-transform: none;
  letter-spacing: normal;
  font-weight: 400;
}

.permission-row input[type='checkbox'] {
  width: 16px;
  height: 16px;
  accent-color: var(--accent-running);
  cursor: pointer;
}

.field-help {
  font-size: 11px;
  color: var(--text-secondary);
  margin: 2px 0 0;
  line-height: 1.4;
}

.criteria-field {
  margin-bottom: 0;
}

.criterion-row {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr 32px;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}

.criterion-row input {
  min-width: 0;
}

.criterion-remove {
  background: none;
  border: none;
  color: var(--text-secondary);
  font-size: 14px;
  cursor: pointer;
  padding: 6px;
  border-radius: 4px;
}

.criterion-remove:hover {
  color: var(--accent-danger);
  background: var(--bg-elevated);
}

.add-criterion-btn {
  background: transparent;
  border: 1px dashed var(--border-default);
  color: var(--text-secondary);
  border-radius: 6px;
  padding: 6px 12px;
  font-size: 12px;
  cursor: pointer;
  margin-top: 4px;
  transition: border-color 0.2s, color 0.2s;
}

.add-criterion-btn:hover {
  border-color: var(--accent-running);
  color: var(--accent-running);
}
</style>
