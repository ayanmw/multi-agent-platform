package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/internal/config"
	"github.com/ayanmw/multi-agent-platform/pkg/db"
)

// fakeProvider 实现 Provider 接口，用于测试。
type fakeProvider struct {
	name    string
	models  []ModelInfo
	listErr error
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) Chat(req ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{}, nil
}
func (p *fakeProvider) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	return "", Usage{}, nil, nil
}
func (p *fakeProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return p.models, p.listErr
}

// TestProviderManager_SyncProvider 验证单个 Provider 的模型发现写入 DB。
func TestProviderManager_SyncProvider(t *testing.T) {
	if err := db.Init(":memory:"); err != nil {
		t.Fatalf("db init failed: %v", err)
	}
	defer db.Close()

	pm := &ProviderManager{
		providers: map[string]Provider{
			"deepseek": &fakeProvider{
				name: "deepseek",
				models: []ModelInfo{
					{ID: "deepseek-v4-flash", Provider: "deepseek"},
					{ID: "deepseek-v4-pro", Provider: "deepseek"},
				},
			},
		},
		resolver: NewProfileResolver(&config.Config{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pm.SyncProvider(ctx, "deepseek"); err != nil {
		t.Fatalf("SyncProvider failed: %v", err)
	}

	models, err := db.ListModelsByProvider("deepseek")
	if err != nil {
		t.Fatalf("ListModelsByProvider failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	rec, found, err := db.GetModel("deepseek", "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("GetModel failed: %v", err)
	}
	if !found {
		t.Fatal("expected deepseek-v4-flash to exist")
	}
	if rec.Missing {
		t.Fatal("newly discovered model should not be missing")
	}
}

// TestProviderManager_SyncProvider_ListError 验证 ListModels 失败时记录 healthy=false。
func TestProviderManager_SyncProvider_ListError(t *testing.T) {
	if err := db.Init(":memory:"); err != nil {
		t.Fatalf("db init failed: %v", err)
	}
	defer db.Close()

	pm := &ProviderManager{
		providers: map[string]Provider{
			"broken": &fakeProvider{
				name:    "broken",
				listErr: errors.New("network unreachable"),
			},
		},
		configs: map[string]config.LLMProviderConfig{
			"broken": {Name: "broken", Type: "openai", Endpoint: "http://broken"},
		},
		resolver: NewProfileResolver(&config.Config{}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// SyncProvider 在失败时会写入快照并把 healthy 设为 false，但不会返回 error
	//（避免启动时一个 Provider 失败影响整体服务启动）。
	if err := pm.SyncProvider(ctx, "broken"); err != nil {
		t.Fatalf("SyncProvider should not return error, got %v", err)
	}

	providers, err := db.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders failed: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider snapshot, got %d", len(providers))
	}
	if providers[0].Healthy {
		t.Fatal("expected provider to be unhealthy after sync error")
	}
}

// TestProviderManager_SyncAll 验证并发同步多个 Provider 且互不影响。
func TestProviderManager_SyncAll(t *testing.T) {
	if err := db.Init(":memory:"); err != nil {
		t.Fatalf("db init failed: %v", err)
	}
	defer db.Close()

	pm := &ProviderManager{
		providers: map[string]Provider{
			"a": &fakeProvider{name: "a", models: []ModelInfo{{ID: "m1", Provider: "a"}}},
			"b": &fakeProvider{name: "b", models: []ModelInfo{{ID: "m2", Provider: "b"}}},
		},
		configs: map[string]config.LLMProviderConfig{
			"a": {Name: "a", Type: "openai"},
			"b": {Name: "b", Type: "openai"},
		},
		resolver: NewProfileResolver(&config.Config{}),
	}

	if err := pm.SyncAll(context.Background()); err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	for _, provider := range []string{"a", "b"} {
		models, err := db.ListModelsByProvider(provider)
		if err != nil {
			t.Fatalf("ListModelsByProvider(%s) failed: %v", provider, err)
		}
		if len(models) != 1 {
			t.Fatalf("expected 1 model for provider %s, got %d", provider, len(models))
		}
	}
}
