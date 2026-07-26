// Package llm —— ProviderManager：并发编排 Provider 发现与持久化。
//
// # 职责
//
// ProviderManager 持有 .env 配置的 Provider 列表以及由这些配置构造的
// Provider 实例池。它在启动时申请同步（SyncAll），在运行时响应手动刷新
// （SyncProvider）。发现到的模型不直接决定运行时 ModelRegistry，而是先
// 写入 llm_models 表；ModelService 再从 DB 加载，保证 DB 是模型画像的
// 单一事实源。
//
// # 并发模型
//
// SyncAll 对全部 Provider 并发执行 SyncProvider，单个 Provider 内部的写库
// 与 ListModels 调用串行化。ProviderManager 本身不持久化状态，所有状态
// 写入 llm_providers / llm_models 表，便于前端查询。
package llm

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/anmingwei/multi-agent-platform/internal/config"
	"github.com/anmingwei/multi-agent-platform/pkg/db"
)

// ProviderManager 编排 LLM Provider 的模型发现与持久化。
type ProviderManager struct {
	// providers 是按 name 索引的 Provider 实例池。
	providers map[string]Provider

	// configs 保存构造 provider 所用的 LLMProviderConfig（含 api_key）。
	configs map[string]config.LLMProviderConfig

	// resolver 提供统一的 tier / provider-name 解析能力。
	resolver *ProfileResolver

	// syncTimeout 控制单次 ListModels 调用的最大等待时间。
	syncTimeout time.Duration

	// mu 保护 providers / configs 的结构化访问。
	mu sync.RWMutex
}

// NewProviderManager 从全局 Config 创建 ProviderManager。
//
// 它为 cfg.LLMProviders 中的每个条目构造 Provider 实例（mock 模式除外）。
// 若 provider 创建失败，会记录 warning 并跳过；失败的 Provider 仍保留在
// 快照中， healthy=false，便于前端看到配置错误。
func NewProviderManager(cfg *config.Config) (*ProviderManager, error) {
	pm := &ProviderManager{
		providers: make(map[string]Provider),
		configs:   make(map[string]config.LLMProviderConfig),
		resolver:  NewProfileResolver(cfg),
		syncTimeout: 30 * time.Second,
	}

	for _, pc := range cfg.LLMProviders {
		if pc.Name == "" {
			log.Printf("[ProviderManager] skip provider with empty name")
			continue
		}

		// 创建真正的 Provider 实例；mock 不用于发现。
		provider, err := NewProvider(ProviderConfig{
			Name:     pc.Type,
			Endpoint: pc.Endpoint,
			APIKey:   pc.APIKey,
			Model:    "",
		})
		if err != nil {
			log.Printf("[ProviderManager] failed to create provider %q: %v", pc.Name, err)
			// 仍保存配置快照，但 provider 为 nil，同步会失败并记录 healthy=false。
		} else {
			pm.providers[pc.Name] = provider
		}
		pm.configs[pc.Name] = pc
	}

	return pm, nil
}

// SyncAll 并发同步所有 Provider；若某个 Provider 失败，不影响其他 Provider。
func (pm *ProviderManager) SyncAll(ctx context.Context) error {
	pm.mu.RLock()
	names := make([]string, 0, len(pm.configs))
	for name := range pm.configs {
		names = append(names, name)
	}
	pm.mu.RUnlock()

	var wg sync.WaitGroup
	var errMu sync.Mutex
	var errs []error

	// 限制并发数，避免对 endpoint 发起过多同时请求。
	sem := make(chan struct{}, 4)

	for _, name := range names {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := pm.SyncProvider(ctx, n); err != nil {
				errMu.Lock()
				errs = append(errs, fmt.Errorf("sync provider %q: %w", n, err))
				errMu.Unlock()
			}
		}(name)
	}

	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("sync all: %d provider(s) failed", len(errs))
	}
	return nil
}

// SyncProvider 同步单个 Provider 的模型列表并写入持久化存储。
//
// 流程：
//  1. 写/更新 llm_providers 快照（endpoint/type，不含 api_key）。
//  2. 调用 Provider.ListModels 获取可用模型 ID。
//  3. 对返回的每个 model ID 执行 InsertOrReplace（保留已有行可编辑字段）。
//  4. 将本次未返回的模型标记为 missing=true。
//  5. 更新 provider healthy / last_sync_at / last_sync_error。
func (pm *ProviderManager) SyncProvider(ctx context.Context, name string) error {
	pm.mu.RLock()
	provider, ok := pm.providers[name]
	cfg := pm.configs[name]
	pm.mu.RUnlock()

	if !ok {
		return fmt.Errorf("provider %q not configured", name)
	}

	now := time.Now().UTC()

	// 1. 先写入 provider 快照（不含 api_key）。
	if err := pm.writeProviderSnapshot(cfg); err != nil {
		return err
	}

	if provider == nil {
		return pm.finishSync(name, false, now, "provider instance creation failed")
	}

	// 2. 带超时调用 ListModels。
	syncCtx, cancel := context.WithTimeout(ctx, pm.syncTimeout)
	defer cancel()

	models, err := provider.ListModels(syncCtx)
	if err != nil {
		return pm.finishSync(name, false, now, err.Error())
	}

	// 3. 合并发现到的模型。
	seenIDs := make([]string, 0, len(models))
	for _, m := range models {
		id := m.ID
		if id == "" {
			continue
		}
		seenIDs = append(seenIDs, id)

		if err := pm.upsertDiscoveredModel(name, id, now); err != nil {
			log.Printf("[ProviderManager] failed to upsert model %s/%s: %v", name, id, err)
			// 单个模型写入失败不中断整体同步。
		}
	}

	// 4. 标记 missing：把本次未上报的已有模型设为 missing=true。
	if err := db.MarkModelsMissingForProvider(name, seenIDs, true); err != nil {
		log.Printf("[ProviderManager] failed to mark missing models for provider %q: %v", name, err)
	}

	return pm.finishSync(name, true, now, "")
}

// writeProviderSnapshot 将 provider 配置快照持久化（不含 api_key）。
func (pm *ProviderManager) writeProviderSnapshot(cfg config.LLMProviderConfig) error {
	return db.InsertOrReplaceProvider(db.LLMProviderRecord{
		Name:     cfg.Name,
		Type:     cfg.Type,
		Endpoint: cfg.Endpoint,
	})
}

// finishSync 更新 provider 同步状态。
func (pm *ProviderManager) finishSync(name string, healthy bool, now time.Time, syncErr string) error {
	var errMsg string
	if syncErr != "" {
		errMsg = syncErr
	}
	return db.UpdateProviderSyncStatus(name, healthy, errMsg, now)
}

// upsertDiscoveredModel 将发现到的模型写入 DB；不覆盖已有行的可编辑字段。
func (pm *ProviderManager) upsertDiscoveredModel(providerName, modelID string, now time.Time) error {
	existing, found, err := db.GetModel(providerName, modelID)
	if err != nil {
		return fmt.Errorf("get model %s/%s: %w", providerName, modelID, err)
	}

	if found {
		// 仅重置 missing 状态与更新时间；可编辑字段保持不变。
		existing.Missing = false
		existing.UpdatedAt = now
		return db.InsertOrReplaceModel(*existing)
	}

	// 新模型：用保守默认值，tier 从 mapping/推断得到。
	rec := db.LLMModelRecord{
		ProviderName:     providerName,
		ModelID:          modelID,
		DisplayName:      modelID,
		Tier:             pm.resolver.ResolveTier(modelID),
		Capabilities:     nil,
		InputPrice:       0,
		OutputPrice:      0,
		MaxContextWindow: 0,
		MaxOutputTokens:  0,
		Missing:          false,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return db.InsertOrReplaceModel(rec)
}

func matchWildcard(pattern, s string) bool {
	if pattern == "" {
		return s == ""
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return parts[0] == s
	}
	idx := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		n := strings.Index(s[idx:], part)
		if n == -1 {
			return false
		}
		if i == 0 && n != 0 {
			return false
		}
		idx += n + len(part)
	}
	if idx != len(s) && parts[len(parts)-1] != "" {
		return false
	}
	return true
}
