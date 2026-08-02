package llm

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/internal/config"
	"github.com/ayanmw/multi-agent-platform/pkg/db"
)

// TestModelService_SeedModels 验证 SeedModels 写入 cfg.LLMModel、cfg.Models。
func TestModelService_SeedModels(t *testing.T) {
	if err := db.Init(":memory:"); err != nil {
		t.Fatalf("db init failed: %v", err)
	}
	defer db.Close()

	cfg := &config.Config{
		LLMModel: "deepseek-v4-flash",
		Models: []config.ModelConfig{
			{Name: "claude-sonnet-4-6", Provider: "anthropic"},
		},
	}
	svc := NewModelService(cfg)
	if err := svc.SeedModels(); err != nil {
		t.Fatalf("SeedModels failed: %v", err)
	}

	models, err := db.ListModels()
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) < 2 {
		t.Fatalf("expected at least 2 seeded models, got %d", len(models))
	}

	found := map[string]bool{}
	for _, m := range models {
		key := m.ProviderName + "/" + m.ModelID
		found[key] = true
	}

	// cfg.LLMModel 归属默认 Provider。
	if !found["default/deepseek-v4-flash"] {
		t.Fatalf("expected default/deepseek-v4-flash in seeded models, got %v", found)
	}
	if !found["anthropic/claude-sonnet-4-6"] {
		t.Fatalf("expected anthropic/claude-sonnet-4-6 in seeded models, got %v", found)
	}
}

// TestModelService_LoadModelsToRegistry 验证从 DB 加载模型到 ModelRegistry。
func TestModelService_LoadModelsToRegistry(t *testing.T) {
	if err := db.Init(":memory:"); err != nil {
		t.Fatalf("db init failed: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	_ = db.InsertOrReplaceModel(db.LLMModelRecord{
		ProviderName: "deepseek",
		ModelID:      "deepseek-v4-flash",
		DisplayName:  "DeepSeek V4 Flash",
		Tier:         "efficient",
		InputPrice:   0.14,
		OutputPrice:  0.28,
		Missing:      false,
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	svc := NewModelService(&config.Config{})
	reg := NewModelRegistry()
	if err := svc.LoadModelsToRegistry(reg); err != nil {
		t.Fatalf("LoadModelsToRegistry failed: %v", err)
	}

	// 全名 key 必须存在。
	full := reg.Get("deepseek/deepseek-v4-flash")
	if full == nil {
		t.Fatal("expected full name profile to exist")
	}
	if full.InputPrice != 0.14 {
		t.Fatalf("expected input price 0.14, got %f", full.InputPrice)
	}

	// 短名别名也应存在。
	short := reg.Get("deepseek-v4-flash")
	if short == nil {
		t.Fatal("expected short name alias to exist")
	}
}

// TestModelService_DoesNotOverwriteExistingEditableFields 验证重新发现/种子不会覆盖用户已编辑字段。
func TestModelService_DoesNotOverwriteExistingEditableFields(t *testing.T) {
	if err := db.Init(":memory:"); err != nil {
		t.Fatalf("db init failed: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	_ = db.InsertOrReplaceModel(db.LLMModelRecord{
		ProviderName: "deepseek",
		ModelID:      "deepseek-v4-flash",
		DisplayName:  "User Renamed",
		Tier:         "premium", // 用户手动改为 premium
		InputPrice:   9.99,
		OutputPrice:  19.99,
		Missing:      false,
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	cfg := &config.Config{
		LLMModel: "deepseek-v4-flash",
		Models: []config.ModelConfig{
			{Name: "deepseek-v4-flash", Provider: "deepseek"},
		},
	}
	svc := NewModelService(cfg)
	if err := svc.SeedModels(); err != nil {
		t.Fatalf("SeedModels failed: %v", err)
	}

	rec, found, err := db.GetModel("deepseek", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("GetModel failed: %v", err)
	}
	if !found {
		t.Fatal("expected model to exist")
	}
	if rec.DisplayName != "User Renamed" {
		t.Fatalf("display name should not be overwritten, got %q", rec.DisplayName)
	}
	if rec.Tier != "premium" {
		t.Fatalf("tier should not be overwritten, got %q", rec.Tier)
	}
	if rec.InputPrice != 9.99 {
		t.Fatalf("input_price should not be overwritten, got %f", rec.InputPrice)
	}
}

// TestAvailableProfiles 验证 AvailableProfiles 按 provider 配置、Missing 标记、Source 正确过滤。
func TestAvailableProfiles(t *testing.T) {
	reg := NewModelRegistry()

	// 已配置 provider 的可用模型。
	reg.Register(&ModelProfile{
		Name:    "deepseek/deepseek-v4-flash",
		Provider: "deepseek",
		Tier:    TierEfficient,
		Missing: false,
		Source:  SourceConfiguredProvider,
	})
	// 同 provider 但缺失标记为 true，应被排除。
	reg.Register(&ModelProfile{
		Name:    "deepseek/deepseek-v4-pro",
		Provider: "deepseek",
		Tier:    TierStandard,
		Missing: true,
		Source:  SourceConfiguredProvider,
	})
	// 来自 DefaultProfiles 的未配置 provider 模型，allowDefault=false 应排除。
	reg.Register(&ModelProfile{
		Name:     "anthropic/claude-sonnet-4-6",
		Provider: "anthropic",
		Tier:     TierStandard,
		Missing:  false,
		Source:   SourceDefaultProfile,
	})
	// 未配置 provider 的 configured_provider 模型，allowDefault=false 不依赖 provider 配置仍放行。
	reg.Register(&ModelProfile{
		Name:     "openai/gpt-5.4",
		Provider: "openai",
		Tier:     TierStandard,
		Missing:  false,
		Source:   SourceConfiguredProvider,
	})

	configured := map[string]bool{"deepseek": true}

	// Case 1: 仅 deepseek 已配置，不允许 default profile → 只返回 deepseek 模型。
	got := reg.AvailableProfiles(configured, false)
	want := []string{"deepseek/deepseek-v4-flash"}
	assertProfileNames(t, got, want)

	// Case 2: 允许 default profile 后，未配置 provider 的 DefaultProfile 也放行。
	got = reg.AvailableProfiles(configured, true)
	want = []string{"deepseek/deepseek-v4-flash", "anthropic/claude-sonnet-4-6"}
	assertProfileNames(t, got, want)

	// Case 3: openai 也配置后，应包含 openai 的 configured_provider 模型。
	configured["openai"] = true
	got = reg.AvailableProfiles(configured, false)
	want = []string{"deepseek/deepseek-v4-flash", "openai/gpt-5.4"}
	assertProfileNames(t, got, want)

	// Case 4: 空配置且 allowDefault=false 时，只有 SourceConfiguredProvider 仍视为可用
	//（模拟未配置 LLM_PROVIDERS 但 DB 有实际模型记录的场景）。
	configuredEmpty := map[string]bool{}
	got = reg.AvailableProfiles(configuredEmpty, false)
	want = []string{"deepseek/deepseek-v4-flash", "openai/gpt-5.4"}
	assertProfileNames(t, got, want)

	// Case 5: 空配置但 allowDefault=true 时返回所有非 missing 模型。
	got = reg.AvailableProfiles(configuredEmpty, true)
	want = []string{"deepseek/deepseek-v4-flash", "anthropic/claude-sonnet-4-6", "openai/gpt-5.4"}
	assertProfileNames(t, got, want)
}

func assertProfileNames(t *testing.T, got []*ModelProfile, want []string) {
	t.Helper()
	names := make([]string, len(got))
	for i, p := range got {
		names[i] = p.Name
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("expected available profiles %v, got %v", want, names)
	}
}

// TestAvailableProfiles_ShortNameAliasNotDuplicated 验证短名别名不会被重复计入可用集合。
func TestAvailableProfiles_ShortNameAliasNotDuplicated(t *testing.T) {
	// 使用真实 DB 加载路径：短名alias 与全名共享同一个 ModelProfile 的只有 Name 不同。
	if err := db.Init(":memory:"); err != nil {
		t.Fatalf("db init failed: %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	_ = db.InsertOrReplaceModel(db.LLMModelRecord{
		ProviderName: "deepseek",
		ModelID:      "deepseek-v4-flash",
		DisplayName:  "DeepSeek V4 Flash",
		Tier:         "efficient",
		Missing:      false,
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	svc := NewModelService(&config.Config{})
	reg := NewModelRegistry()
	if err := svc.LoadModelsToRegistry(reg); err != nil {
		t.Fatalf("LoadModelsToRegistry failed: %v", err)
	}

	available := reg.AvailableProfiles(map[string]bool{"deepseek": true}, false)
	if len(available) != 1 {
		t.Fatalf("expected 1 available profile, got %d: %v", len(available), profileNames(available))
	}
	if !strings.Contains(available[0].Name, "/") {
		t.Fatalf("expected available profile to use full provider/model_id identity, got %q", available[0].Name)
	}
}

func profileNames(profiles []*ModelProfile) []string {
	names := make([]string, len(profiles))
	for i, p := range profiles {
		names[i] = p.Name
	}
	return names
}
