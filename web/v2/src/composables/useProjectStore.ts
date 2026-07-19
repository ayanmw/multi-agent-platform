// useProjectStore — reactive project state management
//
// Manages project CRUD operations against the backend /api/projects API.
// Provides reactive state (projects list, activeProjectId) and async actions
// (load, create, update, delete).
//
// The backend API:
//   GET    /api/projects       — list all projects with session/memory counts
//   POST   /api/projects       — create project (body: ProjectRequest)
//   PUT    /api/projects/{id}  — update project by ID
//   DELETE /api/projects/{id}  — delete project by ID
//
// Projects are top-level organizational units that group sessions.
// The "default" project always exists and cannot be deleted.
import { ref, computed } from 'vue'

/** Project summary returned by GET /api/projects (matches backend projectSummary) */
export interface Project {
  id: string
  name: string
  description: string
  working_directory: string
  session_count?: number
  memory_count?: number
  created_at: string
  updated_at: string
}

/** Request body for POST/PUT /api/projects */
export interface ProjectRequest {
  name: string
  description: string
  working_directory: string
}

/** Singleton state shared across all consumers */
const projects = ref<Project[]>([])
const activeProjectId = ref<string>('default')
const loading = ref(false)
const error = ref<string | null>(null)

export function useProjectStore() {
  /** Computed: the currently active project, or null if not found */
  const activeProject = computed<Project | null>(() =>
    projects.value.find(p => p.id === activeProjectId.value) || null
  )

  /** Load all projects from the backend */
  async function loadProjects(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const resp = await fetch('/api/projects')
      if (!resp.ok) throw new Error(`Failed to load projects: ${resp.status}`)
      projects.value = (await resp.json()) as Project[]
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error'
      throw err
    } finally {
      loading.value = false
    }
  }

  /** Create a new project via POST /api/projects */
  async function createProject(req: ProjectRequest): Promise<Project> {
    error.value = null
    const resp = await fetch('/api/projects', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    })
    if (!resp.ok) throw new Error(`Failed to create project: ${resp.status}`)
    const created = (await resp.json()) as Project
    projects.value.unshift(created)
    return created
  }

  /** Update an existing project via PUT /api/projects/{id} */
  async function updateProject(id: string, req: ProjectRequest): Promise<Project> {
    error.value = null
    const resp = await fetch(`/api/projects/${encodeURIComponent(id)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(req),
    })
    if (!resp.ok) throw new Error(`Failed to update project: ${resp.status}`)
    const updated = (await resp.json()) as Project
    const idx = projects.value.findIndex(p => p.id === id)
    if (idx !== -1) projects.value[idx] = updated
    return updated
  }

  /** Delete a project via DELETE /api/projects/{id} */
  async function deleteProject(id: string): Promise<void> {
    error.value = null
    const resp = await fetch(`/api/projects/${encodeURIComponent(id)}`, { method: 'DELETE' })
    if (!resp.ok) throw new Error(`Failed to delete project: ${resp.status}`)
    projects.value = projects.value.filter(p => p.id !== id)
    if (activeProjectId.value === id) {
      activeProjectId.value = projects.value[0]?.id || 'default'
    }
  }

  /** Set the active project by ID */
  function setActiveProject(id: string) {
    activeProjectId.value = id
  }

  return {
    projects,
    activeProjectId,
    activeProject,
    loading,
    error,
    loadProjects,
    createProject,
    updateProject,
    deleteProject,
    setActiveProject,
  }
}