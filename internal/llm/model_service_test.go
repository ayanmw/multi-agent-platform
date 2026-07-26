package llm

import (
	"testing"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
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
