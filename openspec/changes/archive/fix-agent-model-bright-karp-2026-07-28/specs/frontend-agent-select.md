# spec: 前端 agent 选择下发

## 功能

OptionsFlyout 中选择的 agent 必须传回 App.vue，并通过 `startTask` 等接口随请求下发。

## 组件接口

### OptionsFlyout.vue

```vue
<script setup lang="ts">
const props = defineProps<{
  agents: Agent[];
  modelValue?: string;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', id: string): void;
}>();

const selectedAgentId = computed({
  get: () => props.modelValue || props.agents.find(a => a.is_default)?.id || props.agents[0]?.id || '',
  set: (v) => emit('update:modelValue', v),
});
</script>
```

### App.vue

```ts
const currentAgentId = ref('');

function handleSend(input: string, mode: SendMode) {
  const options = { agentId: currentAgentId.value || undefined };
  if (mode === 'task') startTask(input, options);
  if (mode === 'turn') startTurn(input, options);
  if (mode === 'multi') startMultiAgentTask(input, options);
}
```

```vue
<OptionsFlyout v-model="currentAgentId" :agents="agentOptions" />
```

## 验收标准

- 打开 Options 面板，切换 agent，发送任务，请求体 `agent_id` 与所选一致。
- 首次加载默认选中 default agent。
- 不选择时（`currentAgentId === ''`），后端使用 `agent_default`（store 原有回退逻辑）。
