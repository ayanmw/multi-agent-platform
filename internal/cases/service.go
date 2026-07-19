package cases

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/harness"
)

// CreateCaseRequest 是创建新自定义用例的请求 payload。
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

// UpdateCaseRequest 是更新已存在自定义用例的请求 payload。
// 所有字段均为可选；省略的字段保留其现有值。
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

// Service 提供 cases 管理的业务逻辑。
// 它将不可变的内置用例与持久化的自定义用例组合在一起。
type Service struct {
	repo      *Repository
	builtins  []Case
	builtinBy map[string]*Case
}

// Repository 返回底层的 cases.Repository，便于调用方直接持久化
// evaluation 结果（例如来自 runtime Engine）。
func (s *Service) Repository() *Repository {
	return s.repo
}

// Init 创建一个新的 Service，若数据库为空则种子化内置用例，并对 builtins 建立索引。
func Init(db *sql.DB) (*Service, error) {
	repo := NewRepository(db)
	svc := &Service{
		repo:      repo,
		builtins:  All(),
		builtinBy: make(map[string]*Case, len(All())),
	}
	for i := range svc.builtins {
		c := &svc.builtins[i]
		svc.builtinBy[c.ID] = c
	}

	count, err := repo.CountAll()
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

// seedBuiltins 将所有内置用例以 IsBuiltin=1 插入数据库。
func (s *Service) seedBuiltins() error {
	for _, c := range s.builtins {
		if _, err := s.repo.Create(c); err != nil {
			return fmt.Errorf("seed case %s: %w", c.ID, err)
		}
	}
	return nil
}

// validateContract 检查用于创建/更新的 contract 约束。
func validateContract(contract harness.TaskContract) error {
	if contract.MaxSteps <= 0 || contract.MaxSteps > 100 {
		return fmt.Errorf("max_steps must be between 1 and 100, got %d", contract.MaxSteps)
	}
	return nil
}

// validateCreate 校验一个创建请求。
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

// Create 校验请求并持久化一条新的自定义用例。
func (s *Service) Create(req CreateCaseRequest) (*Case, error) {
	c, err := validateCreate(req)
	if err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}
	return s.repo.Create(c)
}

// isBuiltin 判断给定 id 是否指向内置用例。
func (s *Service) isBuiltin(id string) bool {
	_, ok := s.builtinBy[id]
	return ok
}

// Get 按 id 返回 case，优先查内置用例，其次查数据库用例。
func (s *Service) Get(id string) (*Case, error) {
	if c, ok := s.builtinBy[id]; ok {
		return c, nil
	}
	return s.repo.GetByID(id)
}

// List 返回所有用例（内置 + 自定义），可按 tag/category 过滤。
// tags 过滤采用 OR 语义：只要 case 包含所请求 tag 中的任意一个即视为匹配。
func (s *Service) List(tags []string, category string) ([]Case, error) {
	category = strings.TrimSpace(category)
	custom, err := s.repo.List(category)
	if err != nil {
		return nil, fmt.Errorf("list custom cases: %w", err)
	}
	all := make([]Case, 0, len(s.builtins)+len(custom))
	all = append(all, s.builtins...)
	all = append(all, custom...)

	// 规范化 tag 过滤
	filterTags := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" {
			filterTags = append(filterTags, strings.ToLower(t))
		}
	}

	if len(filterTags) == 0 && category == "" {
		return all, nil
	}

	result := make([]Case, 0)
	for _, c := range all {
		if category != "" && !strings.EqualFold(c.Category, category) {
			continue
		}
		if !hasAnyTag(c.Tags, filterTags) {
			continue
		}
		result = append(result, c)
	}
	return result, nil
}

// hasAnyTag 判断 caseTags 是否至少包含 required 中的一个 tag（大小写不敏感）。
// required 为空集时匹配每一个 case。
func hasAnyTag(caseTags, required []string) bool {
	if len(required) == 0 {
		return true
	}
	for _, t := range caseTags {
		lower := strings.ToLower(strings.TrimSpace(t))
		for _, r := range required {
			if lower == r {
				return true
			}
		}
	}
	return false
}

// Update 修改已存在的自定义用例；内置用例不可更新。
var ErrBuiltinImmutable = errors.New("cannot modify or delete built-in case")
func (s *Service) Update(id string, req UpdateCaseRequest) (*Case, error) {
	if s.isBuiltin(id) {
		return nil, ErrBuiltinImmutable
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

// Delete 按 id 删除自定义用例；内置用例不可删除。
func (s *Service) Delete(id string) error {
	if s.isBuiltin(id) {
		return ErrBuiltinImmutable
	}
	return s.repo.Delete(id)
}

// BuiltinIDs 返回所有内置用例的 id。
func (s *Service) BuiltinIDs() []string {
	ids := make([]string, 0, len(s.builtins))
	for _, c := range s.builtins {
		ids = append(ids, c.ID)
	}
	return ids
}
