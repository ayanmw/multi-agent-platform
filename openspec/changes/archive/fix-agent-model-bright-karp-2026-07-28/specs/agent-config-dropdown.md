# spec: AgentConfig 模型下拉体验增强

## 功能

在已有 model dropdown + filter 基础上，提升选择体验，并将 `preferred_model` 也改为下拉。

## 交互

- filter input 输入时实时过滤 dropdown 列表。
- 按 Enter 时，若过滤后只剩一个选项，自动将其设为当前值。
- 保存时：
  - `model_mode === 'single_model'` 且 `form.model` 为空 → 提示必须选择模型。
  - `model_mode === 'auto_route'` 且 `form.preferred_model` 为空 → 允许为空（由 Router 自行选择）。

## 数据结构

可用模型列表来自父组件 prop `availableModels: string[]`（与现有实现一致）。

## 复用

将 model dropdown 抽成局部组件 `ModelDropdown`（内联在 AgentConfig.vue 中），`model` 与 `preferred_model` 共用。

## 验收标准

- 输入 filter 后，Enter 快速选中唯一匹配项。
- `single_model` 空 model 时保存被拦截并提示。
- `preferred_model` 也是下拉，体验与 model 一致。
