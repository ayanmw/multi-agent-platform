# CaseForm UX 优化 v3 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: 使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 逐步实施。Steps 使用 `- [ ]` 语法以便跟踪。

**Goal:** 将 `CaseForm.vue` 从自由文本输入改造为结构化、中文友好、受控选择的 Case 创建/编辑表单：分类可搜索下拉并允许新增、标签多选 chips 并允许新增、图标仅能从 20 个预设 emoji 选择、新增 Permissions 权限配置、验收标准支持逐条添加/编辑/删除，同时为 Goal 提供中文说明与示例。

**Architecture:** 不引入外部 UI 库，仅使用原生 Vue 3（`<script setup>`、`ref`、`computed`、`watch`）与 scoped CSS 实现所有交互。新增一个通用 `useClickOutside` composable 处理 dropdown / tag / emoji picker 的外部点击关闭。所有状态变更集中在 `CaseForm.vue` 内部，保存时统一组装为 `CreateCaseRequest` / `UpdateCaseRequest`。

**Tech Stack:** Vue 3, TypeScript, Vite, scoped CSS。

---

## File Structure

| 文件 | 职责 | 关键改动 |
|------|------|----------|
| `web/src/composables/useClickOutside.ts` | 新建 tiny composable | 提供 `ref` + `mousedown` 监听，让 dropdown 在外部点击时自动关闭 |
| `web/src/components/CaseForm.vue` | 主要改动文件 | 导入 store/composable，新增分类/标签/图标/权限/验收标准状态与 UI，重写 `resetForm`/`handleSave`，所有文案中文化 |
| `web/src/types/case.ts` | 只读 | 已包含 `TaskContract.permissions` 与 `AcceptanceCriterion` 定义 |
| `web/src/composables/useCaseStore.ts` | 只读 | 复用 `allTags` / `allCategories` 作为已有选项来源 |

---

## Task 1：新增通用 `useClickOutside` composable

**Files:**
- Create: `web/src/composables/useClickOutside.ts`

- [ ] **Step 1：写入 click-outside 通用逻辑**

```ts
import { ref, onMounted, onUnmounted, type Ref } from 'vue'

/**
 * 监听元素外部的鼠标按下事件，触发 handler。
 * 常用于 dropdown / popover / picker 等需要外部点击关闭的组件。
 */
export function useClickOutside(handler: () => void): Ref<HTMLElement | null> {
  const elRef = ref<HTMLElement | null>(null)

  function listener(event: MouseEvent) {
    if (elRef.value && !elRef.value.contains(event.target as Node)) {
      handler()
    }
  }

  onMounted(() => document.addEventListener('mousedown', listener))
  onUnmounted(() => document.removeEventListener('mousedown', listener))

  return elRef
}
```

- [ ] **Step 2：验证文件存在且 `vue-tsc` 通过**

```bash
cd D:/Claude-Code-MultiAgent/web && ls src/composables/useClickOutside.ts
```

Expected: 文件存在。

```bash
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit
```

Expected: 无新增类型错误（当前 CaseForm 尚未引用此文件）。

---

## Task 2：重构 `CaseForm.vue` 顶层脚本 — 导入、基础字段与通用常量

**Files:**
- Modify: `web/src/components/CaseForm.vue`

本任务保留旧的 `tagsText`、`acceptanceCriteriaText` 及相关解析函数，直到 Task 9 清理，保证中间步骤可编译。后续任务将逐步替换相关模板与保存逻辑。

- [ ] **Step 1：替换 `<script setup>` 顶部导入**

```ts
<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import type { Case, CreateCaseRequest, UpdateCaseRequest, TaskContract, AcceptanceCriterion } from '../types/case'
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
```

- [ ] **Step 2：新增 icon、权限常量与基础响应式字段**

```ts
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

const tagsText = ref('')
const acceptanceCriteriaText = ref('')
const error = ref<string | null>(null)

const isEditing = computed(() => props.caseData !== null)
const modalTitle = computed(() => (isEditing.value ? '编辑 Case' : '新建 Case'))
```

- [ ] **Step 3：保留旧解析函数、resetForm、handleSave 与 watch（便于后续逐步替换）**

在 Step 2 字段之后保留以下代码：

```ts
/** Convert acceptance criteria to textarea format */
function formatAcceptanceCriteria(list?: AcceptanceCriterion[]): string {
  if (!list || list.length === 0) return ''
  return list
    .map(ac => {
      const parts = [ac.type, ac.target]
      if (ac.description) parts.push(ac.description)
      return parts.join('|')
    })
    .join('\n')
}

/** Parse acceptance criteria textarea lines into typed objects */
function parseAcceptanceCriteria(text: string): AcceptanceCriterion[] {
  const result: AcceptanceCriterion[] = []
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const parts = trimmed.split('|')
    if (parts.length < 2) continue
    const [type, target, description] = parts
    result.push({
      type: type.trim(),
      target: target.trim(),
      description: description ? description.trim() : '',
    })
  }
  return result
}

/** Normalize comma-separated tags into a clean array */
function parseTags(text: string): string[] {
  return text
    .split(',')
    .map(t => t.trim())
    .filter(t => t.length > 0)
}

/** Reset form fields from the provided case or default values */
function resetForm(c: Case | null) {
  if (c) {
    name.value = c.name
    category.value = c.category
    icon.value = c.icon
    description.value = c.description
    systemPrompt.value = c.system_prompt
    defaultInput.value = c.default_input
    maxSteps.value = c.contract.max_steps
    tagsText.value = c.tags.join(', ')
    goal.value = c.contract.goal
    acceptanceCriteriaText.value = formatAcceptanceCriteria(c.contract.acceptance_criteria)
  } else {
    name.value = ''
    category.value = ''
    icon.value = ''
    description.value = ''
    systemPrompt.value = ''
    defaultInput.value = ''
    maxSteps.value = 10
    tagsText.value = ''
    goal.value = ''
    acceptanceCriteriaText.value = ''
  }
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
    error.value = 'Name is required'
    return
  }
  if (!category.value.trim()) {
    error.value = 'Category is required'
    return
  }

  const contract = {
    goal: goal.value.trim(),
    max_steps: maxSteps.value,
    acceptance_criteria: parseAcceptanceCriteria(acceptanceCriteriaText.value),
  }

  const tags = parseTags(tagsText.value)

  if (props.caseData) {
    const req: UpdateCaseRequest = {
      name: name.value.trim(),
      category: category.value.trim(),
      icon: icon.value.trim() || undefined,
      description: description.value.trim() || undefined,
      system_prompt: systemPrompt.value.trim() || undefined,
      default_input: defaultInput.value.trim() || undefined,
      contract,
      tags,
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
      tags,
    }
    emit('save', req)
  }
}

function handleClose() {
  emit('close')
}
</script>
```

- [ ] **Step 4：运行类型检查，确认中间状态可编译**

```bash
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit
```

Expected: 无新增错误。

---

## Task 3：Category 可搜索下拉 + 允许新增

**Files:**
- Modify: `web/src/components/CaseForm.vue`

- [ ] **Step 1：在 `<script setup>` 中新增 category 状态与计算属性**

在 `const error = ref<string | null>(null)` 之后插入：

```ts
const categorySearch = ref('')
const categoryOpen = ref(false)
const categoryWrapRef = useClickOutside(() => { categoryOpen.value = false })

const filteredCategories = computed(() => {
  const q = categorySearch.value.trim().toLowerCase()
  if (!q) return store.allCategories
  return store.allCategories.filter(c => c.toLowerCase().includes(q))
})

const canAddCategory = computed(() => {
  const q = categorySearch.value.trim()
  return q && !store.allCategories.some(c => c.toLowerCase() === q.toLowerCase())
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
  if (e.key !== 'Enter') return
  e.preventDefault()
  if (canAddCategory.value) {
    addCustomCategory()
  } else if (filteredCategories.value.length > 0) {
    selectCategory(filteredCategories.value[0])
  }
}
```

- [ ] **Step 2：更新 `resetForm` 对 categorySearch 的回填与清空**

替换 `resetForm` 中的分类相关行：

编辑分支：
```ts
    category.value = c.category
    categorySearch.value = c.category
```

创建分支：
```ts
    category.value = ''
    categorySearch.value = ''
```

并在函数末尾（`error.value = null` 之前）加入：

```ts
  categoryOpen.value = false
```

- [ ] **Step 3：替换模板中的 Category 输入为可搜索下拉**

找到原代码：

```html
              <div class="form-field">
                <label for="case-category">Category <span class="required">*</span></label>
                <input id="case-category" v-model="category" type="text" placeholder="e.g. Web Dev" />
              </div>
```

替换为：

```html
              <div class="form-field category-field" ref="categoryWrapRef">
                <label for="case-category">分类 <span class="required">*</span></label>
                <input
                  id="case-category"
                  v-model="categorySearch"
                  type="text"
                  placeholder="搜索已有分类，或输入后按回车新增"
                  @input="categoryOpen = true"
                  @focus="categoryOpen = true"
                  @keydown="onCategoryKeydown"
                />
                <div v-if="categoryOpen" class="dropdown-panel">
                  <div
                    v-for="cat in filteredCategories"
                    :key="cat"
                    class="dropdown-item"
                    @mousedown.prevent="selectCategory(cat)"
                  >
                    {{ cat }}
                  </div>
                  <div v-if="canAddCategory" class="dropdown-item add-item" @mousedown.prevent="addCustomCategory">
                    新增“{{ categorySearch.trim() }}”
                  </div>
                  <div v-if="filteredCategories.length === 0 && !canAddCategory" class="dropdown-empty">
                    暂无分类，输入后按回车新增
                  </div>
                </div>
              </div>
```

- [ ] **Step 4：为 category dropdown 补齐 scoped CSS**

在 `<style scoped>` 末尾追加：

```css
.category-field {
  position: relative;
}

.dropdown-panel {
  position: absolute;
  top: calc(100% - 2px);
  left: 0;
  right: 0;
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 6px;
  max-height: 200px;
  overflow-y: auto;
  z-index: 10;
  box-shadow: 0 8px 16px rgba(0, 0, 0, 0.35);
}

.dropdown-item {
  padding: 8px 10px;
  font-size: 13px;
  color: #ccc;
  cursor: pointer;
  transition: background 0.15s;
}

.dropdown-item:hover,
.dropdown-item.active {
  background: #333;
}

.dropdown-item.add-item {
  color: #4a9eff;
  font-weight: 600;
}

.dropdown-empty {
  padding: 10px;
  font-size: 12px;
  color: #666;
}
```

- [ ] **Step 5：类型检查**

```bash
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit
```

Expected: 无错误。

---

## Task 4：Tags 多选 chips + 允许新增

**Files:**
- Modify: `web/src/components/CaseForm.vue`

- [ ] **Step 1：在 `<script setup>` 中新增 tags 状态与计算属性**

在 category 相关代码之后插入：

```ts
const tags = ref<string[]>([])
const tagSearch = ref('')
const tagOpen = ref(false)
const tagWrapRef = useClickOutside(() => { tagOpen.value = false })
const tagInputEl = ref<HTMLInputElement | null>(null)

const availableTags = computed(() => {
  const q = tagSearch.value.trim().toLowerCase()
  return store.allTags
    .filter(t => !tags.value.includes(t))
    .filter(t => (q ? t.toLowerCase().includes(q) : true))
})

function selectTag(value: string) {
  if (!tags.value.includes(value)) tags.value.push(value)
  tagSearch.value = ''
  tagOpen.value = false
}

function addCustomTag() {
  const q = tagSearch.value.trim()
  if (!q) return
  selectTag(q)
}

function removeTag(value: string) {
  tags.value = tags.value.filter(t => t !== value)
}

function onTagKeydown(e: KeyboardEvent) {
  if (e.key !== 'Enter') return
  e.preventDefault()
  if (availableTags.value.length > 0) {
    selectTag(availableTags.value[0])
  } else {
    addCustomTag()
  }
}
```

- [ ] **Step 2：更新 `resetForm` 使用 tags 数组**

替换：

```ts
    tagsText.value = c.tags.join(', ')
```

为：

```ts
    tags.value = [...c.tags]
```

创建分支：

```ts
    tags.value = []
```

并在函数末尾（error 之前）加入：

```ts
  tagOpen.value = false
  tagSearch.value = ''
```

- [ ] **Step 3：更新 `handleSave` 使用 `tags.value` 并移除旧解析**

替换 `handleSave` 中的：

```ts
  const tags = parseTags(tagsText.value)
```

为：

```ts
  const formTags = [...tags.value]
```

并将两处 `tags,`（位于 `contract` 之后）替换为 `tags: formTags,`。

- [ ] **Step 4：替换模板中的 Tags 输入为 chip + 搜索**

找到原代码：

```html
            <div class="form-field">
              <label for="case-tags">Tags</label>
              <input
                id="case-tags"
                v-model="tagsText"
                type="text"
                placeholder="comma, separated, tags"
              />
            </div>
```

替换为：

```html
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
                  placeholder="搜索已有标签，或输入新标签后按回车"
                  @input="tagOpen = true"
                  @focus="tagOpen = true"
                  @keydown="onTagKeydown"
                />
              </div>
              <div v-if="tagOpen" class="dropdown-panel">
                <div
                  v-for="tag in availableTags"
                  :key="tag"
                  class="dropdown-item"
                  @mousedown.prevent="selectTag(tag)"
                >
                  {{ tag }}
                </div>
                <div
                  v-if="tagSearch.trim() && !tags.includes(tagSearch.trim()) && availableTags.length === 0"
                  class="dropdown-item add-item"
                  @mousedown.prevent="addCustomTag"
                >
                  新增“{{ tagSearch.trim() }}”
                </div>
                <div
                  v-if="availableTags.length === 0 && (!tagSearch.trim() || tags.includes(tagSearch.trim()))"
                  class="dropdown-empty"
                >
                  暂无可用标签
                </div>
              </div>
            </div>
```

- [ ] **Step 5：补全 tags 相关 CSS**

在 `<style scoped>` 末尾追加：

```css
.tags-field {
  position: relative;
}

.tags-input {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 6px;
  padding: 5px 8px;
  min-height: 36px;
  cursor: text;
}

.tags-input:focus-within {
  border-color: #4a9eff;
}

.tags-input input {
  flex: 1;
  min-width: 100px;
  background: transparent;
  border: none;
  padding: 4px 2px;
  color: #ddd;
  font-size: 13px;
  outline: none;
}

.tag-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: #333;
  color: #ddd;
  font-size: 12px;
  padding: 3px 8px;
  border-radius: 4px;
}

.tag-remove {
  background: none;
  border: none;
  color: #888;
  font-size: 11px;
  cursor: pointer;
  padding: 0;
  line-height: 1;
}

.tag-remove:hover {
  color: #e74c3c;
}
```

- [ ] **Step 6：类型检查**

```bash
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit
```

Expected: 无错误。

---

## Task 5：Icon 仅允许从 20 个预设 emoji 选择

**Files:**
- Modify: `web/src/components/CaseForm.vue`

- [ ] **Step 1：新增 icon picker 相关状态与函数**

在 tags 相关代码之后插入：

```ts
const iconOpen = ref(false)
const iconWrapRef = useClickOutside(() => { iconOpen.value = false })

function selectIcon(value: string) {
  icon.value = value
  iconOpen.value = false
}
```

- [ ] **Step 2：更新 `resetForm` 对 iconOpen 的清空**

在 `resetForm` 函数末尾（`error.value = null` 之前）加入：

```ts
  iconOpen.value = false
```

- [ ] **Step 3：替换模板中的 Icon 输入为 emoji 选择器**

找到原代码：

```html
              <div class="form-field">
                <label for="case-icon">Icon</label>
                <input id="case-icon" v-model="icon" type="text" placeholder="e.g. 🚀" />
              </div>
```

替换为：

```html
              <div class="form-field icon-field" ref="iconWrapRef">
                <label>图标</label>
                <button
                  type="button"
                  class="icon-trigger"
                  @click="iconOpen = !iconOpen"
                >
                  <span v-if="icon" class="icon-selected">{{ icon }}</span>
                  <span v-else class="icon-placeholder">点击选择 emoji 图标</span>
                </button>
                <div v-if="iconOpen" class="icon-grid">
                  <button
                    v-for="emoji in ICON_OPTIONS"
                    :key="emoji"
                    type="button"
                    class="icon-btn"
                    :class="{ active: icon === emoji }"
                    @click="selectIcon(emoji)"
                    :title="emoji"
                  >
                    {{ emoji }}
                  </button>
                </div>
              </div>
```

- [ ] **Step 4：补全 icon 选择器 CSS**

在 `<style scoped>` 末尾追加：

```css
.icon-field {
  position: relative;
}

.icon-trigger {
  width: 100%;
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 6px;
  padding: 8px 10px;
  color: #ddd;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.2s;
}

.icon-trigger:hover {
  border-color: #4a9eff;
}

.icon-selected {
  font-size: 18px;
}

.icon-placeholder {
  color: #888;
}

.icon-grid {
  position: absolute;
  top: calc(100% - 2px);
  left: 0;
  right: 0;
  display: grid;
  grid-template-columns: repeat(10, 1fr);
  gap: 6px;
  background: #1e1e1e;
  border: 1px solid #444;
  border-radius: 6px;
  padding: 8px;
  z-index: 10;
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
  background: #333;
}

.icon-btn.active {
  background: #2b4a6f;
  border-color: #4a9eff;
}
```

- [ ] **Step 5：类型检查与简单回归**

```bash
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit
```

Expected: 无错误。

---

## Task 6：Permissions 权限配置区块

**Files:**
- Modify: `web/src/components/CaseForm.vue`

- [ ] **Step 1：新增 permissions 状态**

在 icon 相关代码之后插入：

```ts
const permissions = ref({
  allow_network: false,
  allow_file_delete: false,
  allow_file_write: false,
  allow_shell: false,
  allow_shell_dangerous: false,
})
```

- [ ] **Step 2：更新 `resetForm` 对 permissions 的回填与清空**

在编辑分支 `goal.value = c.contract.goal` 之后加入：

```ts
    permissions.value = {
      allow_network: c.contract.permissions?.allow_network ?? false,
      allow_file_delete: c.contract.permissions?.allow_file_delete ?? false,
      allow_file_write: c.contract.permissions?.allow_file_write ?? false,
      allow_shell: c.contract.permissions?.allow_shell ?? false,
      allow_shell_dangerous: c.contract.permissions?.allow_shell_dangerous ?? false,
    }
```

在创建分支 `goal.value = ''` 之后加入：

```ts
    permissions.value = {
      allow_network: false,
      allow_file_delete: false,
      allow_file_write: false,
      allow_shell: false,
      allow_shell_dangerous: false,
    }
```

- [ ] **Step 3：更新 `handleSave` 写入 permissions**

替换 `handleSave` 中的 `const contract = { ... }` 为：

```ts
  const contract = {
    goal: goal.value.trim(),
    max_steps: maxSteps.value,
    permissions: { ...permissions.value },
    acceptance_criteria: parseAcceptanceCriteria(acceptanceCriteriaText.value),
  }
```

- [ ] **Step 4：在模板中插入 Permissions 区块**

在 Default Input 字段之后、Acceptance Criteria 字段之前插入（找到原 `case-acceptance` div，在其上方插入）：

```html
            <div class="form-field permissions-field">
              <label>权限配置</label>
              <div class="permissions-grid">
                <label v-for="p in PERMISSION_LABELS" :key="p.key" class="permission-row">
                  <input v-model="permissions[p.key]" type="checkbox" />
                  <span>{{ p.label }}</span>
                </label>
              </div>
            </div>
```

- [ ] **Step 5：补全 permissions CSS**

在 `<style scoped>` 末尾追加：

```css
.permissions-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 8px 16px;
  background: #1e1e1e;
  border: 1px solid #333;
  border-radius: 6px;
  padding: 12px;
}

.permission-row {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #ccc;
  font-size: 12px;
  cursor: pointer;
  text-transform: none;
  letter-spacing: normal;
  font-weight: 400;
}

.permission-row input[type='checkbox'] {
  width: 16px;
  height: 16px;
  accent-color: #4a9eff;
  cursor: pointer;
}
```

- [ ] **Step 6：类型检查**

```bash
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit
```

Expected: 无错误。

---

## Task 7：Goal 中文说明/示例 + 表单其余文案中文化

**Files:**
- Modify: `web/src/components/CaseForm.vue`

- [ ] **Step 1：更新模板中所有英文静态文案**

按以下映射进行批量替换（保持对应 `v-model` 与 input `type` 不变）：

| 原文 | 替换为 |
|------|--------|
| `title="Close"` | `title="关闭"` |
| `Name <span class="required">*</span>` | `名称 <span class="required">*</span>` |
| `placeholder="Case name"` | `placeholder="请输入 Case 名称"` |
| `Max Steps` | `最大步数` |
| `Description` | `描述` |
| `placeholder="Short description"` | `placeholder="简短描述该 Case 的用途"` |
| `System Prompt` | `系统提示词` |
| `placeholder="System prompt sent to the agent"` | `placeholder="发送给 Agent 的系统提示词"` |
| `Default Input` | `默认输入` |
| `placeholder="Default user input"` | `placeholder="用户默认输入内容"` |
| `Cancel` | `取消` |
| `Save` | `保存` |

替换 Goal 字段为带中文说明与示例的版本。找到：

```html
            <div class="form-field">
              <label for="case-goal">Goal</label>
              <input
                id="case-goal"
                v-model="goal"
                type="text"
                placeholder="High-level goal for this case"
              />
            </div>
```

替换为：

```html
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
```

- [ ] **Step 2：将 `handleSave` 校验错误文案改为中文**

替换：

```ts
  if (!name.value.trim()) {
    error.value = 'Name is required'
    return
  }
  if (!category.value.trim()) {
    error.value = 'Category is required'
    return
  }
```

为：

```ts
  if (!name.value.trim()) {
    error.value = '名称不能为空'
    return
  }
  if (!category.value.trim()) {
    error.value = '分类不能为空'
    return
  }
```

- [ ] **Step 3：添加 field-help 通用样式**

在 `<style scoped>` 末尾追加：

```css
.field-help {
  font-size: 11px;
  color: #777;
  margin: 2px 0 0;
  line-height: 1.4;
}
```

- [ ] **Step 4：类型检查**

```bash
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit
```

Expected: 无错误。

---

## Task 8：Acceptance Criteria 逐条编辑器

**Files:**
- Modify: `web/src/components/CaseForm.vue`

- [ ] **Step 1：新增 criteria 行内编辑状态与函数**

在 permissions 相关代码之后插入：

```ts
interface CriterionRow {
  type: string
  target: string
  description: string
}

const criteria = ref<CriterionRow[]>([{ type: '', target: '', description: '' }])

function addCriterion() {
  criteria.value.push({ type: '', target: '', description: '' })
}

function removeCriterion(index: number) {
  criteria.value.splice(index, 1)
  if (criteria.value.length === 0) addCriterion()
}
```

- [ ] **Step 2：更新 `resetForm` 使用 criteria 数组**

替换 `resetForm` 中：

```ts
    acceptanceCriteriaText.value = formatAcceptanceCriteria(c.contract.acceptance_criteria)
```

为：

```ts
    criteria.value =
      c.contract.acceptance_criteria && c.contract.acceptance_criteria.length > 0
        ? c.contract.acceptance_criteria.map(ac => ({
            type: ac.type,
            target: ac.target,
            description: ac.description ?? '',
          }))
        : [{ type: '', target: '', description: '' }]
```

创建分支中 `acceptanceCriteriaText.value = ''` 替换为：

```ts
    criteria.value = [{ type: '', target: '', description: '' }]
```

- [ ] **Step 3：更新 `handleSave` 使用 criteria 数组**

替换 `handleSave` 中的 `contract` 组装：

```ts
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
```

- [ ] **Step 4：替换 Acceptance Criteria 模板**

找到原代码：

```html
            <div class="form-field">
              <label for="case-acceptance">
                Acceptance Criteria
                <span class="hint">one per line: type|target|description</span>
              </label>
              <textarea
                id="case-acceptance"
                v-model="acceptanceCriteriaText"
                rows="4"
                placeholder="exact_match|output.txt|content matches expected"
              ></textarea>
            </div>
```

替换为：

```html
            <div class="form-field criteria-field">
              <label>验收标准</label>
              <p class="field-help">
                用于评估 Agent 任务是否完成。每条由 type（评估类型）、target（评估目标）、description（补充说明）组成。
                示例：exact_match | hello.go | 文件包含 /hello 路由处理函数。
              </p>
              <div
                v-for="(ac, idx) in criteria"
                :key="idx"
                class="criterion-row"
              >
                <input v-model="ac.type" placeholder="类型，如 exact_match" />
                <input v-model="ac.target" placeholder="目标，如 hello.go" />
                <input v-model="ac.description" placeholder="补充说明（可选）" />
                <button type="button" class="criterion-remove" @click="removeCriterion(idx)" title="删除此条">✕</button>
              </div>
              <button type="button" class="add-criterion-btn" @click="addCriterion">+ 新增一条</button>
            </div>
```

- [ ] **Step 5：补全 criteria 编辑器 CSS**

在 `<style scoped>` 末尾追加：

```css
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
  color: #888;
  font-size: 14px;
  cursor: pointer;
  padding: 6px;
  border-radius: 4px;
}

.criterion-remove:hover {
  color: #e74c3c;
  background: #333;
}

.add-criterion-btn {
  background: transparent;
  border: 1px dashed #555;
  color: #aaa;
  border-radius: 6px;
  padding: 6px 12px;
  font-size: 12px;
  cursor: pointer;
  margin-top: 4px;
  transition: border-color 0.2s, color 0.2s;
}

.add-criterion-btn:hover {
  border-color: #4a9eff;
  color: #4a9eff;
}
```

- [ ] **Step 6：类型检查**

```bash
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit
```

Expected: 无错误。

---

## Task 9：清理废弃字段、最终验证与构建

**Files:**
- Modify: `web/src/components/CaseForm.vue`

- [ ] **Step 1：删除已废弃的 ref、函数与相关模板引用**

确认并删除 `<script setup>` 中以下内容：

```ts
const tagsText = ref('')
const acceptanceCriteriaText = ref('')
```

以及以下三个函数：

```ts
function formatAcceptanceCriteria(list?: AcceptanceCriterion[]): string { ... }
function parseAcceptanceCriteria(text: string): AcceptanceCriterion[] { ... }
function parseTags(text: string): string[] { ... }
```

同时从 `import type` 中移除 `AcceptanceCriterion`（如 Task 8 之后已无其他引用）。

- [ ] **Step 2：最终类型检查**

```bash
cd D:/Claude-Code-MultiAgent/web && npx vue-tsc --noEmit
```

Expected: 无错误。

- [ ] **Step 3：构建验证**

```bash
cd D:/Claude-Code-MultiAgent/web && npm run build
```

Expected: 构建成功，`dist/` 目录正常生成，无 TypeScript / Vue 编译错误。

- [ ] **Step 4：浏览器手工回归清单**

```bash
cd D:/Claude-Code-MultiAgent/web && npm run dev
```

逐项验证：

1. 打开「新建 Case」弹窗，分类下拉显示已有分类。
2. 输入新分类后按回车，category 被更新；保存后重新打开，新分类出现在下拉。
3. 标签输入框输入新 tag 回车生成 chip；点击 ✕ 删除；已选 tag 不再出现在下拉。
4. 图标选择器展开 20 个 emoji，选择后保存请求携带正确 `icon`。
5. Permissions 区块默认全不勾选；勾选后保存请求写入 `contract.permissions`。
6. Acceptance Criteria 点击「新增一条」出现 3 个输入框；填写后保存；编辑时正确回填。
7. Goal 显示中文说明与示例。
8. 编辑已有 Case 时，分类、标签、图标、权限、验收标准全部正确回填。
9. 名称/分类留空时点击保存，显示中文错误提示。
10. 点击模态框外部或关闭按钮可关闭；dropdown 外部点击可收起。

---

## Self-Review 清单

### 1. Spec Coverage

| 需求 | 实现位置 |
|------|----------|
| category 可搜索下拉，列出已有 category | Task 3 Step 3 + Task 3 Step 1 `filteredCategories` |
| category 允许新增（回车或点击「新增」） | Task 3 Step 1 `canAddCategory` / `addCustomCategory` / `onCategoryKeydown` |
| tags 多选 chip，列出已有 tags | Task 4 Step 4 + Task 4 Step 1 `availableTags` |
| tags 允许新增 tag、chip 可删除 | Task 4 Step 1 `addCustomTag` / `removeTag` |
| icon 只能从 20 个预设 emoji 选择 | Task 2 Step 2 `ICON_OPTIONS` + Task 5 |
| Permissions 权限区块、5 项勾选、默认不勾选、中文说明 | Task 6 |
| Goal 中文说明与示例 | Task 7 Step 1 |
| Acceptance Criteria 中文说明/示例、逐条添加/编辑/删除 | Task 8 |
| 所有表单说明、placeholder、提示、按钮文字中文 | Task 7 |
| 不引入外部 UI 库 | 全部使用原生 Vue 3 + scoped CSS |
| 只修改前端文件，新增 tiny composable 在必要时 | 新增 `useClickOutside.ts`，其余改动仅 `CaseForm.vue` |

### 2. Placeholder Scan

- 计划中没有 `TBD`、`TODO`、`implement later`、`*类似 Task N*` 等占位表述。
- 每个 step 都包含可直接写入文件的代码块或明确的验证命令。
- 所有函数、类型、ref 名称在首次出现时即完整定义，后续任务直接使用。

### 3. Type Consistency

- `ICON_OPTIONS` 20 个 emoji 与需求列表完全一致，模板中 `icon` 仅被赋值为其中一项。
- `permissions` 的 5 个 key 与 `TaskContract.permissions` 定义完全一致。
- `criteria` 数组元素类型与 `AcceptanceCriterion` 兼容，保存时仅写入 `type/target/description`，过滤空行。
- `tags` 在 `handleSave` 中通过 `formTags = [...tags.value]` 送入 `CreateCaseRequest.tags` / `UpdateCaseRequest.tags`。
- `category` 仍为 `string`，最终通过 `category.value.trim()` 保存。
- `useClickOutside` 返回 `Ref<HTMLElement | null>`，模板中通过 `:ref`（Vue 3 自动解包）绑定到 div 上。

---

**Plan complete and saved to `docs/superpowers/plans/2026-07-17-case-form-ux-optimization.md`. Two execution options:**

1. **Subagent-Driven（推荐）** — 每个 Task 由一个独立 subagent 实现，之后进行 spec + code quality 两阶段 review。请使用 `superpowers:subagent-driven-development`。
2. **Inline Execution** — 在当前会话按 Task 顺序逐步执行，使用 `superpowers:executing-plans`。

**Which approach?**
