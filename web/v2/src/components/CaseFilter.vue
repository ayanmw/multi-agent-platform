<!-- CaseFilter — filter bar for the case library
     Props:
       selectedTags: array of currently selected tag filters
       selectedCategory: currently selected category or null for all
       allTags: available tags to render as pills
       allCategories: available categories for the dropdown

     Emits:
       toggle-tag: user clicked a tag pill
       set-category: user selected a category (or null for all)
       clear-filters: user wants to reset all filters

     Behavior:
       - "All categories" option resets category filter
       - Tag pills toggle in/out of selectedTags
       - Clear filters button appears only when at least one filter is active
-->
<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  selectedTags: string[]
  selectedCategory: string | null
  allTags: string[]
  allCategories: string[]
}>()

const emit = defineEmits<{
  'toggle-tag': [tag: string]
  'set-category': [category: string | null]
  'clear-filters': []
}>()

/** True when any filter is active, controls clear button visibility */
const hasActiveFilters = computed(() => {
  return props.selectedTags.length > 0 || props.selectedCategory !== null
})

/** Handle category dropdown change — emit null for the "All" option */
function handleCategoryChange(e: Event) {
  const value = (e.target as HTMLSelectElement).value
  emit('set-category', value === '' ? null : value)
}
</script>

<template>
  <div class="case-filter">
    <label class="filter-label" for="category-select">Category</label>
    <select
      id="category-select"
      class="category-select"
      :value="selectedCategory ?? ''"
      @change="handleCategoryChange"
    >
      <option value="">All categories</option>
      <option v-for="cat in allCategories" :key="cat" :value="cat">{{ cat }}</option>
    </select>

    <div v-if="allTags.length > 0" class="tag-pills">
      <button
        v-for="tag in allTags"
        :key="tag"
        :class="['tag-pill', { active: selectedTags.includes(tag) }]"
        @click="emit('toggle-tag', tag)"
      >
        {{ tag }}
      </button>
    </div>

    <button
      v-if="hasActiveFilters"
      class="clear-btn"
      @click="emit('clear-filters')"
    >
      Clear filters
    </button>
  </div>
</template>

<style scoped>
.case-filter {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 10px 0;
  margin-bottom: 12px;
}

.filter-label {
  font-size: 12px;
  color: var(--text-secondary);
  font-weight: 500;
}

.category-select {
  background: var(--bg-panel);
  border: 1px solid var(--border-default);
  color: var(--text-primary);
  font-size: 12px;
  padding: 6px 10px;
  border-radius: 6px;
  outline: none;
  min-width: 140px;
}

.category-select:focus {
  border-color: var(--accent-running);
}

.tag-pills {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.tag-pill {
  font-size: 11px;
  color: var(--text-secondary);
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: 12px;
  padding: 3px 10px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.tag-pill:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.tag-pill.active {
  background: rgba(0, 229, 255, 0.2);
  color: var(--accent-running);
  border-color: var(--accent-running);
}

.clear-btn {
  font-size: 11px;
  color: var(--text-secondary);
  background: transparent;
  border: 1px dashed var(--border-default);
  border-radius: 6px;
  padding: 4px 10px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.clear-btn:hover {
  background: rgba(255, 77, 77, 0.1);
  color: var(--accent-danger);
  border-color: var(--accent-danger);
}
</style>
