// Package llm —— ModelService：启动期模型画像合并与运行时刷新。
//
// # 职责
//
// ModelService 从多个来源合并模型画像：
//   - 内置 DefaultProfiles()
//   - cfg.LLMModel（单模型字段）
//   - cfg.Models（显式静态模型配置）
//   - ProviderManager 发现的模型
//
// 合并后写入 llm_models 表，并负责从 DB 加载到 ModelRegistry。
// 它是 ProviderManager 与 ModelRegistry 之间的胶水层。
//
// # 合并优先级
//
// 1. .env 中显式配置的静态模型（cfg.Models）。
// 2. Provider 发现返回的模型（新模型）。
// 3. 内置 DefaultProfiles()。
// 4. 保守 fallback（仅填充 ID/Provider）。
//
// 可编辑字段保护：DB 中已存在的行运行时不会被 Provider 发现覆盖；
// 仅在 Seed 模式（OnStart）下对首次出现的行从 DefaultProfiles 补默认值。
package llm

import (
	"fmt"
	"log"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// ModelService 负责启动期模型种子写入与 DB→Registry 加载。
type ModelService struct {
	cfg      *config.Config
	resolver *ProfileResolver
}

// NewModelService 创建 ModelService。
func NewModelService(cfg *config.Config) *ModelService {
	return &ModelService{cfg: cfg, resolver: NewProfileResolver(cfg)}
}

// SeedModels 在启动时写入种子模型。
//
// 合并顺序：
//  1. cfg.LLMModel（legacy 单模型）与 cfg.Models（显式静态模型）。
//  2. ProviderManager 发现（调用方在 pm.SyncAll 后调用 LoadModelsToRegistry）。
//  3. DefaultProfiles() 作为默认值补充，仅用于新行，不覆盖已有 DB 行。
func (s *ModelService) SeedModels() error {
	now := time.Now().UTC()

	// 1. 写入 LLM_MODELS 与 legacy 单模型。
	if err := s.seedStaticModels(now); err != nil {
		return fmt.Errorf("seed static models: %w", err)
	}

	// 2. 补充 DefaultProfiles：若 DB 中尚无同名模型，用默认值填充。
	if err := s.seedDefaultProfiles(now); err != nil {
		return fmt.Errorf("seed default profiles: %w", err)
	}

	return nil
}

// LoadModelsToRegistry 从 llm_models 表加载全部模型到 ModelRegistry。
// key 使用 "{provider}/{model_id}"，同时注册短名 "{model_id}" 作为兼容性别名。
func (s *ModelService) LoadModelsToRegistry(registry *ModelRegistry) error {
	records, err := db.ListModels()
	if err != nil {
		return fmt.Errorf("list models from db: %w", err)
	}

	// 记录短名是否已占用，避免不同 Provider 同名导致覆盖。
	shortNameRegistered := make(map[string]string)

	for _, rec := range records {
		profile := dbRecordToProfile(rec)
		registry.Register(profile)
		// 只在短名未被注册时注册短名，避免冲突。
		if _, ok := shortNameRegistered[rec.ModelID]; !ok {
			short := *profile
			short.Name = rec.ModelID
			// 注册短名，但不覆盖之前的短名条目。
			registry.Register(&short)
			shortNameRegistered[rec.ModelID] = rec.ProviderName
		}
	}

	log.Printf("ModelRegistry: loaded %d model(s) from persistent storage", len(records))
	return nil
}

// seedStaticModels 把 cfg.LLMModel 和 cfg.Models 写入 DB。
// 静态模型在 DB 中标记为 missing=false，因为它们是 .env 显式声明的。
func (s *ModelService) seedStaticModels(now time.Time) error {
	cfg := s.cfg

	// legacy 单模型：provider 取 "default"（即未配置 LLM_PROVIDERS 时的合成 Provider）。
	if cfg.LLMModel != "" {
		providerName := s.resolver.ResolveProviderNameForModel(cfg.LLMModel)
		if err := s.upsertModel(providerName, cfg.LLMModel, true, now); err != nil {
			log.Printf("[ModelService] failed to seed legacy model %s/%s: %v", providerName, cfg.LLMModel, err)
		}
	}

	// cfg.Models 中显式声明的模型。
	for _, mc := range cfg.Models {
		if mc.Name == "" {
			continue
		}
		providerName := mc.Provider
		if providerName == "" {
			providerName = s.resolver.ResolveProviderNameForModel(mc.Name)
		}
		if providerName == "" {
			providerName = "default"
		}
		if err := s.upsertModel(providerName, mc.Name, true, now); err != nil {
			log.Printf("[ModelService] failed to seed static model %s/%s: %v", providerName, mc.Name, err)
		}
	}

	return nil
}

// seedDefaultProfiles 把 DefaultProfiles() 中尚未写入 DB 的模型做初始填充。
// 主要用于新部署的开箱即用。不覆盖已有行的可编辑字段。
func (s *ModelService) seedDefaultProfiles(now time.Time) error {
	for _, p := range DefaultProfiles() {
		if p == nil || p.Name == "" {
			continue
		}
		providerName := p.Provider
		if providerName == "" {
			providerName = s.resolver.ResolveProviderNameForModel(p.Name)
		}

		existing, found, err := db.GetModel(providerName, p.Name)
		if err != nil {
			log.Printf("[ModelService] failed to query model %s/%s: %v", providerName, p.Name, err)
			continue
		}
		if found {
			// 已有行：仅补充缺失的显示名/能力/价格等，不覆盖非空字段。
			merged := mergeDefaultIntoExisting(existing, p)
			merged.UpdatedAt = now
			if err := db.InsertOrReplaceModel(merged); err != nil {
				log.Printf("[ModelService] failed to merge default profile %s/%s: %v", providerName, p.Name, err)
			}
			continue
		}

		// 新行：用 DefaultProfile 完整填充，并标记 Source 为 default_profile。
		// 只有对应 provider 实际配置后，Router 才会把它们纳入候选池。
		rec := defaultProfileToDBRecord(providerName, p, now)
		if err := db.InsertOrReplaceModel(rec); err != nil {
			log.Printf("[ModelService] failed to seed default profile %s/%s: %v", providerName, p.Name, err)
		}
	}
	return nil
}

// upsertModel 对静态/legacy 模型执行插入（若不存在）或重置 missing。
// 不覆盖已存在的可编辑字段。
func (s *ModelService) upsertModel(providerName, modelID string, missing bool, now time.Time) error {
	existing, found, err := db.GetModel(providerName, modelID)
	if err != nil {
		return fmt.Errorf("get model %s/%s: %w", providerName, modelID, err)
	}
	if found {
		// 静态模型未标记 missing，因为 .env 显式声明意味着可用。
		existing.Missing = missing
		existing.UpdatedAt = now
		return db.InsertOrReplaceModel(*existing)
	}

	// 新行：tier 通过通配符映射，其他字段默认空。
	rec := db.LLMModelRecord{
		ProviderName:     providerName,
		ModelID:          modelID,
		DisplayName:      modelID,
		Tier:             s.resolver.ResolveTier(modelID),
		Missing:          missing,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return db.InsertOrReplaceModel(rec)
}

// mergeDefaultIntoExisting 把 DefaultProfile 中缺失的字段补进已有记录，不覆盖非空。
func mergeDefaultIntoExisting(existing *db.LLMModelRecord, p *ModelProfile) db.LLMModelRecord {
	merged := *existing
	if merged.DisplayName == "" {
		merged.DisplayName = p.Name
	}
	if merged.Tier == "" {
		merged.Tier = p.Tier.String()
	}
	if len(merged.Capabilities) == 0 {
		merged.Capabilities = capabilityStrings(p.Capabilities)
	}
	if merged.InputPrice == 0 {
		merged.InputPrice = p.InputPrice
	}
	if merged.OutputPrice == 0 {
		merged.OutputPrice = p.OutputPrice
	}
	if merged.MaxContextWindow == 0 {
		merged.MaxContextWindow = p.MaxContextWindow
	}
	if merged.MaxOutputTokens == 0 {
		merged.MaxOutputTokens = p.MaxOutputTokens
	}
	if merged.FallbackModel == "" && p.FallbackModel != "" {
		merged.FallbackModel = p.FallbackModel
	}
	if merged.RateLimitRPM == 0 {
		merged.RateLimitRPM = p.RateLimitRPM
	}
	if merged.AvgLatencyMs == 0 {
		merged.AvgLatencyMs = p.AvgLatencyMs
	}
	return merged
}

// dbRecordToProfile 把 db.LLMModelRecord 转换为 llm.ModelProfile。
func dbRecordToProfile(rec db.LLMModelRecord) *ModelProfile {
	return &ModelProfile{
		Name:             fmt.Sprintf("%s/%s", rec.ProviderName, rec.ModelID),
		DisplayName:      rec.DisplayName,
		Provider:         rec.ProviderName,
		Tier:             ParseTier(rec.Tier),
		Capabilities:     parseCapabilities(rec.Capabilities),
		InputPrice:       rec.InputPrice,
		OutputPrice:      rec.OutputPrice,
		MaxContextWindow: rec.MaxContextWindow,
		MaxOutputTokens:  rec.MaxOutputTokens,
		FallbackModel:    rec.FallbackModel,
		RateLimitRPM:     rec.RateLimitRPM,
		AvgLatencyMs:     rec.AvgLatencyMs,
		Missing:          rec.Missing,
		Source:           SourceConfiguredProvider,
	}
}

// defaultProfileToDBRecord 把内置 DefaultProfile 转换为 db.LLMModelRecord，
// 并标记 Source 为 default_profile，以便 Router 区分实际可用模型。
func defaultProfileToDBRecord(providerName string, p *ModelProfile, now time.Time) db.LLMModelRecord {
	return db.LLMModelRecord{
		ProviderName:     providerName,
		ModelID:          p.Name,
		DisplayName:      p.Name,
		Tier:             p.Tier.String(),
		Capabilities:     capabilityStrings(p.Capabilities),
		InputPrice:       p.InputPrice,
		OutputPrice:      p.OutputPrice,
		MaxContextWindow: p.MaxContextWindow,
		MaxOutputTokens:  p.MaxOutputTokens,
		FallbackModel:    p.FallbackModel,
		RateLimitRPM:     p.RateLimitRPM,
		AvgLatencyMs:     p.AvgLatencyMs,
		Missing:          false,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// capabilityStrings 把 ModelCapability slice 转为普通字符串 slice。
func capabilityStrings(caps []ModelCapability) []string {
	if caps == nil {
		return nil
	}
	result := make([]string, len(caps))
	for i, c := range caps {
		result[i] = string(c)
	}
	return result
}

// parseCapabilities 把字符串 slice 转为 ModelCapability slice，支持未知值。
func parseCapabilities(caps []string) []ModelCapability {
	if caps == nil {
		return nil
	}
	result := make([]ModelCapability, len(caps))
	for i, c := range caps {
		result[i] = ModelCapability(c)
	}
	return result
}
