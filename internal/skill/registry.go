package skill

import "sync"

type Registry struct {
	mu     sync.RWMutex
	skills map[string]Skill
}

func NewRegistry() *Registry {
	return &Registry{skills: make(map[string]Skill)}
}

func (r *Registry) Register(s Skill) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.ID] = s
	return s.ID
}

func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.skills, id)
}

func (r *Registry) Get(id string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[id]
	return s, ok
}

func (r *Registry) List(source *SkillSource) []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []Skill
	for _, s := range r.skills {
		if source != nil && s.Source != *source {
			continue
		}
		result = append(result, s)
	}
	return result
}

func (r *Registry) Exists(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.skills[id]
	return ok
}

func (r *Registry) UpdateState(id string, state SkillState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.skills[id]
	if !ok {
		return false
	}
	s.State = state
	r.skills[id] = s
	return true
}
