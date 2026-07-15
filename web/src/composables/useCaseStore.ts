// useCaseStore — reactive case management state
//
// Manages case CRUD operations against the backend /api/cases API and provides
// client-side filtering by tags (OR) and category. Follows the module-level
// singleton pattern used by useTaskStore / useProjectStore.
//
// Backend API:
//   GET    /api/cases           — list all cases
//   POST   /api/cases           — create a case
//   PUT    /api/cases/:id       — update a case
//   DELETE /api/cases/:id       — delete a case
import { ref, computed } from 'vue'
import type { Case, CreateCaseRequest, UpdateCaseRequest } from '../types/case'

/** Singleton state shared across all consumers */
const cases = ref<Case[]>([])
const loading = ref(false)
const error = ref<string | null>(null)
const selectedTags = ref<string[]>([])
const selectedCategory = ref<string | null>(null)

export function useCaseStore() {
  /** Cases filtered by selected tags (OR) and selected category (exact match) */
  const filteredCases = computed<Case[]>(() => {
    if (selectedTags.value.length === 0 && !selectedCategory.value) {
      return cases.value
    }
    return cases.value.filter(c => {
      const tagMatch =
        selectedTags.value.length === 0 ||
        selectedTags.value.some(tag => c.tags.includes(tag))
      const categoryMatch =
        !selectedCategory.value || c.category === selectedCategory.value
      return tagMatch && categoryMatch
    })
  })

  /** Unique sorted tags across all loaded cases */
  const allTags = computed<string[]>(() => {
    const set = new Set<string>()
    for (const c of cases.value) {
      for (const tag of c.tags) {
        set.add(tag)
      }
    }
    return Array.from(set).sort()
  })

  /** Unique sorted categories across all loaded cases */
  const allCategories = computed<string[]>(() => {
    const set = new Set<string>()
    for (const c of cases.value) {
      set.add(c.category)
    }
    return Array.from(set).sort()
  })

  /** Load all cases from the backend */
  async function loadCases(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const resp = await fetch('/api/cases')
      if (!resp.ok) throw new Error(`Failed to load cases: ${resp.status}`)
      cases.value = (await resp.json()) as Case[]
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    } finally {
      loading.value = false
    }
  }

  /** Create a new case via POST /api/cases, then reload the list */
  async function createCase(req: CreateCaseRequest): Promise<Case> {
    error.value = null
    try {
      const resp = await fetch('/api/cases', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      })
      if (!resp.ok) throw new Error(`Failed to create case: ${resp.status}`)
      await loadCases()
      return (await resp.json()) as Case
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    }
  }

  /** Update an existing case via PUT /api/cases/:id, then reload the list */
  async function updateCase(id: string, req: UpdateCaseRequest): Promise<Case> {
    error.value = null
    try {
      const resp = await fetch(`/api/cases/${encodeURIComponent(id)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      })
      if (!resp.ok) throw new Error(`Failed to update case: ${resp.status}`)
      await loadCases()
      return (await resp.json()) as Case
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    }
  }

  /** Delete a case via DELETE /api/cases/:id, then reload the list */
  async function deleteCase(id: string): Promise<void> {
    error.value = null
    try {
      const resp = await fetch(`/api/cases/${encodeURIComponent(id)}`, {
        method: 'DELETE',
      })
      if (!resp.ok) throw new Error(`Failed to delete case: ${resp.status}`)
      await loadCases()
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    }
  }

  /** Toggle a tag in the selectedTags filter */
  function toggleTag(tag: string): void {
    const idx = selectedTags.value.indexOf(tag)
    if (idx === -1) {
      selectedTags.value.push(tag)
    } else {
      selectedTags.value.splice(idx, 1)
    }
  }

  /** Set the selected category filter */
  function setCategory(category: string | null): void {
    selectedCategory.value = category
  }

  /** Clear all active filters */
  function clearFilters(): void {
    selectedTags.value = []
    selectedCategory.value = null
  }

  return {
    cases,
    loading,
    error,
    selectedTags,
    selectedCategory,
    filteredCases,
    allTags,
    allCategories,
    loadCases,
    createCase,
    updateCase,
    deleteCase,
    toggleTag,
    setCategory,
    clearFilters,
  }
}
