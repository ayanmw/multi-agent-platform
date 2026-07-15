package cases

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
)

// CreateCaseRequest is the payload for creating a new custom case.
type CreateCaseRequest struct {
	Name         string                `json:"name"`
	Description  string                `json:"description"`
	Icon         string                `json:"icon"`
	Category     string                `json:"category"`
	SystemPrompt string                `json:"system_prompt"`
	DefaultInput string                `json:"default_input"`
	Contract     *harness.TaskContract `json:"contract,omitempty"`
	Tags         []string              `json:"tags"`
}

// UpdateCaseRequest is the payload for updating an existing custom case.
// All fields are optional; omitted fields keep their existing values.
type UpdateCaseRequest struct {
	Name         *string                `json:"name,omitempty"`
	Description  *string                `json:"description,omitempty"`
	Icon         *string                `json:"icon,omitempty"`
	Category     *string                `json:"category,omitempty"`
	SystemPrompt *string                `json:"system_prompt,omitempty"`
	DefaultInput *string                `json:"default_input,omitempty"`
	Contract     *harness.TaskContract  `json:"contract,omitempty"`
	Tags         *[]string              `json:"tags,omitempty"`
}

// Service provides the business logic for managing cases.
// It combines immutable built-in cases with persisted custom cases.
type Service struct {
	repo      *Repository
	builtins  []Case
	builtinBy map[string]Case
}

// Init creates a new Service, seeds builtin cases if the database is empty, and indexes builtins.
func Init(db *sql.DB) (*Service, error) {
	repo := NewRepository(db)
	svc := &Service{
		repo:      repo,
		builtins:  All(),
		builtinBy: make(map[string]Case, len(All())),
	}
	for _, c := range svc.builtins {
		svc.builtinBy[c.ID] = c
	}

	count, err := repo.Count()
	if err != nil {
		return nil, fmt.Errorf("count cases: %w", err)
	}
	if count == 0 {
		if err := svc.seedBuiltins(); err != nil {
			return nil, fmt.Errorf("seed builtins: %w", err)
		}
	}
	return svc, nil
}

// seedBuiltins inserts all built-in cases into the database with IsBuiltin=1.
func (s *Service) seedBuiltins() error {
	for _, c := range s.builtins {
		c.IsBuiltin = true
		if _, err := s.repo.Create(c); err != nil {
			return fmt.Errorf("seed case %s: %w", c.ID, err)
		}
	}
	return nil
}

// validateContract checks the contract constraints for creation/update.
func validateContract(contract harness.TaskContract) error {
	if contract.MaxSteps <= 0 || contract.MaxSteps > 100 {
		return fmt.Errorf("max_steps must be between 1 and 100, got %d", contract.MaxSteps)
	}
	return nil
}

// validateCreate validates a create request.
func validateCreate(req CreateCaseRequest) (Case, error) {
	if strings.TrimSpace(req.Name) == "" {
		return Case{}, errors.New("name cannot be empty")
	}
	if strings.TrimSpace(req.Category) == "" {
		return Case{}, errors.New("category cannot be empty")
	}
	contract := harness.DefaultContract(req.Name)
	if req.Contract != nil {
		contract = *req.Contract
	}
	if err := validateContract(contract); err != nil {
		return Case{}, err
	}
	return Case{
		Name:         strings.TrimSpace(req.Name),
		Description:  req.Description,
		Icon:         req.Icon,
		Category:     strings.TrimSpace(req.Category),
		SystemPrompt: req.SystemPrompt,
		DefaultInput: req.DefaultInput,
		Contract:     contract,
		Tags:         req.Tags,
		IsBuiltin:    false,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}, nil
}

// Create validates the request and persists a new custom case.
func (s *Service) Create(req CreateCaseRequest) (*Case, error) {
	c, err := validateCreate(req)
	if err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}
	return s.repo.Create(c)
}

// isBuiltin reports whether the given id refers to a built-in case.
func (s *Service) isBuiltin(id string) bool {
	_, ok := s.builtinBy[id]
	return ok
}

// Get returns a case by id, preferring builtin cases, then database cases.
func (s *Service) Get(id string) (*Case, error) {
	if c, ok := s.builtinBy[id]; ok {
		return &c, nil
	}
	return s.repo.GetByID(id)
}

// List returns all cases (builtin + custom) with optional in-memory tag/category filtering.
// tags filter uses AND semantics: a case must contain all requested tags.
func (s *Service) List(tags []string, category string) ([]Case, error) {
	custom, err := s.repo.List()
	if err != nil {
		return nil, fmt.Errorf("list custom cases: %w", err)
	}
	all := make([]Case, 0, len(s.builtins)+len(custom))
	all = append(all, s.builtins...)
	all = append(all, custom...)

	// Normalize tag filter
	filterTags := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" {
			filterTags = append(filterTags, t)
		}
	}
	category = strings.TrimSpace(category)

	if len(filterTags) == 0 && category == "" {
		return all, nil
	}

	var result []Case
	for _, c := range all {
		if category != "" && !strings.EqualFold(c.Category, category) {
			continue
		}
		if !hasAllTags(c.Tags, filterTags) {
			continue
		}
		result = append(result, c)
	}
	return result, nil
}

// hasAllTags reports whether caseTags contains every tag in required (case-insensitive).
func hasAllTags(caseTags, required []string) bool {
	if len(required) == 0 {
		return true
	}
	lower := make(map[string]struct{}, len(caseTags))
	for _, t := range caseTags {
		lower[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	for _, t := range required {
		if _, ok := lower[strings.ToLower(t)]; !ok {
			return false
		}
	}
	return true
}

// Update modifies an existing custom case; builtin cases cannot be updated.
func (s *Service) Update(id string, req UpdateCaseRequest) (*Case, error) {
	if s.isBuiltin(id) {
		return nil, fmt.Errorf("cannot update built-in case %s", id)
	}
	c, err := s.repo.GetByID(id)
	if err != nil {
		return nil, fmt.Errorf("get case: %w", err)
	}
	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			return nil, errors.New("name cannot be empty")
		}
		c.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		c.Description = *req.Description
	}
	if req.Icon != nil {
		c.Icon = *req.Icon
	}
	if req.Category != nil {
		if strings.TrimSpace(*req.Category) == "" {
			return nil, errors.New("category cannot be empty")
		}
		c.Category = strings.TrimSpace(*req.Category)
	}
	if req.SystemPrompt != nil {
		c.SystemPrompt = *req.SystemPrompt
	}
	if req.DefaultInput != nil {
		c.DefaultInput = *req.DefaultInput
	}
	if req.Contract != nil {
		if err := validateContract(*req.Contract); err != nil {
			return nil, fmt.Errorf("validation failed: %w", err)
		}
		c.Contract = *req.Contract
	}
	if req.Tags != nil {
		c.Tags = *req.Tags
	}
	c.UpdatedAt = time.Now()
	return s.repo.Update(*c)
}

// Delete removes a custom case by id; builtin cases cannot be deleted.
func (s *Service) Delete(id string) error {
	if s.isBuiltin(id) {
		return fmt.Errorf("cannot delete built-in case %s", id)
	}
	return s.repo.Delete(id)
}

// BuiltinIDs returns the ids of all builtin cases.
func (s *Service) BuiltinIDs() []string {
	ids := make([]string, 0, len(s.builtins))
	for _, c := range s.builtins {
		ids = append(ids, c.ID)
	}
	return ids
}
