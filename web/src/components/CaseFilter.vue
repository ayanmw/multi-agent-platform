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
  color: #888;
  font-weight: 500;
}

.category-select {
  background: #252525;
  border: 1px solid #444;
  color: #ccc;
  font-size: 12px;
  padding: 6px 10px;
  border-radius: 6px;
  outline: none;
  min-width: 140px;
}

.category-select:focus {
  border-color: #4a9eff;
}

.tag-pills {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.tag-pill {
  font-size: 11px;
  color: #888;
  background: #333;
  border: 1px solid #444;
  border-radius: 12px;
  padding: 3px 10px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s, border-color 0.15s;
}

.tag-pill:hover {
  background: #3a3a3a;
  color: #ccc;
}

.tag-pill.active {
  background: rgba(74, 158, 255, 0.2);
  color: #4a9eff;
  border-color: #4a9eff;
}

.clear-btn {
  font-size: 11px;
  color: #aaa;
  background: transparent;
  border: 1px dashed #555;
  border-radius: 6px;
  padding: 4px 10px;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.clear-btn:hover {
  background: rgba(231, 76, 60, 0.1);
  color: #e74c3c;
  border-color: #e74c3c;
}
</style>
