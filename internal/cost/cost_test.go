package cost

// cost_test.go — CostTracker / CostReport / CalculateCost / BuildRecordFromProfile /
// ResolveFallbackChain / IsRetryableError 的单元测试。
//
// 全部为纯逻辑测试，不依赖网络与数据库。表驱动 + t.Run 子测试。

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/anmingwei/multi-agent-platform/internal/llm"
)

// --- 辅助 --------------------------------------------------------------------

func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// fakeEmbedProvider 仅用于维度校验测试（memory 包用），这里不用。
func sampleProfile(inputPrice, outputPrice float64, tier llm.ModelTier) *llm.ModelProfile {
	return &llm.ModelProfile{
		Name:        "test-model",
		Provider:    "openai",
		Tier:        tier,
		InputPrice:  inputPrice,
		OutputPrice: outputPrice,
	}
}

// ============================================================================
// CalculateCost — 整数精度计算
// ============================================================================

func TestCalculateCost(t *testing.T) {
	ct := NewCostTracker()

	t.Run("nil profile returns 0", func(t *testing.T) {
		if got := ct.CalculateCost(nil, llm.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}); got != 0 {
			t.Errorf("nil profile should return 0, got %d", got)
		}
	})

	t.Run("zero total tokens returns 0", func(t *testing.T) {
		p := sampleProfile(1.0, 2.0, llm.TierStandard)
		// TotalTokens=0 triggers the early return even if prompt/completion are non-zero
		if got := ct.CalculateCost(p, llm.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 0}); got != 0 {
			t.Errorf("TotalTokens=0 should return 0, got %d", got)
		}
	})

	t.Run("basic cost computation per 1M tokens", func(t *testing.T) {
		// InputPrice=$1/1M, OutputPrice=$2/1M
		// 1M input tokens  → 100 cents * 1M / 1M = 100 cents = $1.00
		// 1M output tokens → 200 cents * 1M / 1M = 200 cents = $2.00
		p := sampleProfile(1.0, 2.0, llm.TierStandard)
		usage := llm.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, TotalTokens: 2_000_000}
		got := ct.CalculateCost(p, usage)
		// 1M input * (1.0*100 cents) + 1M output * (2.0*100 cents) = 100M + 200M = 300M cents / 1M = 300 cents
		if got != 300 {
			t.Errorf("expected 300 cents, got %d", got)
		}
	})

	t.Run("small token count truncates toward zero", func(t *testing.T) {
		// $1/1M input → 0.1 cents for 1000 tokens → integer division truncates to 0
		p := sampleProfile(1.0, 0.0, llm.TierStandard)
		usage := llm.Usage{PromptTokens: 1000, CompletionTokens: 0, TotalTokens: 1000}
		got := ct.CalculateCost(p, usage)
		// 1000 * 100 cents / 1_000_000 = 0.1 → truncated to 0
		if got != 0 {
			t.Errorf("1000 tokens at $1/1M should truncate to 0 cents, got %d", got)
		}
	})

	t.Run("large amount no overflow", func(t *testing.T) {
		// $1000/1M input, 1B tokens → 1000*100 * 1e9 / 1e6 = 1e8 cents = $1,000,000
		p := sampleProfile(1000.0, 0.0, llm.TierPremium)
		usage := llm.Usage{PromptTokens: 1_000_000_000, CompletionTokens: 0, TotalTokens: 1_000_000_000}
		got := ct.CalculateCost(p, usage)
		// 1e9 * 100000 cents / 1e6 = 1e9 * 0.1 = 1e8 cents
		if got != 100_000_000 {
			t.Errorf("expected 100,000,000 cents, got %d", got)
		}
	})

	t.Run("only output tokens", func(t *testing.T) {
		p := sampleProfile(0.0, 4.0, llm.TierStandard)
		usage := llm.Usage{PromptTokens: 0, CompletionTokens: 500_000, TotalTokens: 500_000}
		got := ct.CalculateCost(p, usage)
		// 500k * (4.0*100=400 cents) / 1e6 = 200 cents
		if got != 200 {
			t.Errorf("expected 200 cents, got %d", got)
		}
	})
}

// ============================================================================
// CostTracker — Record / 聚合查询
// ============================================================================

func TestCostTrackerRecordAndAggregate(t *testing.T) {
	ct := NewCostTracker()

	records := []CostRecord{
		{ID: "1", TaskID: "t1", SessionID: "s1", ProjectID: "p1", AgentID: "a1", Model: "m1", Tier: "standard", TotalTokens: 100, InputTokens: 60, OutputTokens: 40, CostCents: 50, CostUSD: 0.50},
		{ID: "2", TaskID: "t1", SessionID: "s1", ProjectID: "p1", AgentID: "a2", Model: "m2", Tier: "premium", TotalTokens: 200, InputTokens: 150, OutputTokens: 50, CostCents: 150, CostUSD: 1.50},
		{ID: "3", TaskID: "t2", SessionID: "s2", ProjectID: "p1", AgentID: "a1", Model: "m1", Tier: "standard", TotalTokens: 50, InputTokens: 30, OutputTokens: 20, CostCents: 20, CostUSD: 0.20},
	}
	for _, r := range records {
		ct.Record(r)
	}

	t.Run("TaskCost aggregates by task", func(t *testing.T) {
		rep, err := ct.TaskCost("t1")
		if err != nil {
			t.Fatal(err)
		}
		if rep.RecordCount != 2 {
			t.Errorf("RecordCount = %d, want 2", rep.RecordCount)
		}
		if rep.TotalCostCents != 200 {
			t.Errorf("TotalCostCents = %d, want 200", rep.TotalCostCents)
		}
		if rep.TotalTokens != 300 {
			t.Errorf("TotalTokens = %d, want 300", rep.TotalTokens)
		}
		if rep.ByModel["m1"] != 50 || rep.ByModel["m2"] != 150 {
			t.Errorf("ByModel = %v, want m1=50 m2=150", rep.ByModel)
		}
		if rep.ByAgent["a1"] != 50 || rep.ByAgent["a2"] != 150 {
			t.Errorf("ByAgent = %v, want a1=50 a2=150", rep.ByAgent)
		}
	})

	t.Run("SessionCost aggregates by session", func(t *testing.T) {
		rep, _ := ct.SessionCost("s2")
		if rep.RecordCount != 1 || rep.TotalCostCents != 20 {
			t.Errorf("SessionCost(s2) = count=%d cents=%d, want 1/20", rep.RecordCount, rep.TotalCostCents)
		}
	})

	t.Run("ProjectCost aggregates across sessions/tasks", func(t *testing.T) {
		rep, _ := ct.ProjectCost("p1")
		if rep.RecordCount != 3 || rep.TotalCostCents != 220 {
			t.Errorf("ProjectCost(p1) = count=%d cents=%d, want 3/220", rep.RecordCount, rep.TotalCostCents)
		}
	})

	t.Run("nonexistent task returns empty report", func(t *testing.T) {
		rep, _ := ct.TaskCost("nope")
		if rep.RecordCount != 0 || rep.TotalCostCents != 0 {
			t.Errorf("expected empty report, got count=%d cents=%d", rep.RecordCount, rep.TotalCostCents)
		}
		// maps should be initialized (not nil) to avoid下游 nil panic
		if rep.ByModel == nil {
			t.Error("ByModel should be initialized even when empty")
		}
	})

	t.Run("ByTier breakdown", func(t *testing.T) {
		rep, _ := ct.TaskCost("t1")
		if rep.ByTier["standard"] != 50 || rep.ByTier["premium"] != 150 {
			t.Errorf("ByTier = %v, want standard=50 premium=150", rep.ByTier)
		}
	})
}

// ============================================================================
// CostTracker — onRecord 回调
// ============================================================================

func TestCostTrackerOnRecordCallback(t *testing.T) {
	var mu sync.Mutex
	var seen []CostRecord
	ct := NewCostTracker(WithOnRecord(func(r CostRecord) {
		mu.Lock()
		seen = append(seen, r)
		mu.Unlock()
	}))

	ct.Record(CostRecord{ID: "x1", TaskID: "t", CostCents: 7})
	ct.Record(CostRecord{ID: "x2", TaskID: "t", CostCents: 9})

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Errorf("callback should fire twice, got %d", len(seen))
	}
	if seen[0].ID != "x1" || seen[1].ID != "x2" {
		t.Errorf("callback order wrong: %v", []string{seen[0].ID, seen[1].ID})
	}
}

func TestCostTrackerCallbackPanicIsContained(t *testing.T) {
	ct := NewCostTracker(WithOnRecord(func(r CostRecord) {
		panic("boom")
	}))
	// Record must not propagate the callback panic
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("Record should not propagate callback panic, got %v", rec)
		}
	}()
	ct.Record(CostRecord{ID: "p", TaskID: "t"})
}

// ============================================================================
// CostTracker — 并发安全
// ============================================================================

func TestCostTrackerConcurrentRecord(t *testing.T) {
	ct := NewCostTracker()
	var wg sync.WaitGroup
	const goroutines = 20
	const perG = 50
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				ct.Record(CostRecord{ID: "r", TaskID: "shared", AgentID: "a", CostCents: 1})
			}
		}(g)
	}
	wg.Wait()
	rep, _ := ct.TaskCost("shared")
	want := int64(goroutines * perG)
	if rep.RecordCount != goroutines*perG || rep.TotalCostCents != want {
		t.Errorf("after concurrent records: count=%d cents=%d, want %d/%d", rep.RecordCount, rep.TotalCostCents, goroutines*perG, want)
	}
}

// ============================================================================
// BuildRecordFromProfile
// ============================================================================

func TestBuildRecordFromProfile(t *testing.T) {
	t.Run("without registry tier is unknown", func(t *testing.T) {
		ct := NewCostTracker()
		p := sampleProfile(1.0, 2.0, llm.TierStandard)
		usage := llm.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, TotalTokens: 2_000_000}
		rec := ct.BuildRecordFromProfile("task1", "sess1", "proj1", "agent1", 3, "test-model", p, usage)
		if rec.TaskID != "task1" || rec.SessionID != "sess1" || rec.ProjectID != "proj1" || rec.AgentID != "agent1" {
			t.Errorf("identifiers not propagated: %+v", rec)
		}
		if rec.StepIndex != 3 {
			t.Errorf("StepIndex = %d, want 3", rec.StepIndex)
		}
		if rec.Model != "test-model" {
			t.Errorf("Model = %q, want test-model", rec.Model)
		}
		if rec.Tier != "unknown" {
			t.Errorf("without registry Tier should be unknown, got %q", rec.Tier)
		}
		if rec.CostCents != 300 {
			t.Errorf("CostCents = %d, want 300", rec.CostCents)
		}
		if !approxEq(rec.CostUSD, 3.0) {
			t.Errorf("CostUSD = %f, want 3.0", rec.CostUSD)
		}
		if rec.InputTokens != 1_000_000 || rec.OutputTokens != 1_000_000 || rec.TotalTokens != 2_000_000 {
			t.Errorf("tokens not propagated: %+v", rec)
		}
		if rec.ID == "" {
			t.Error("ID should be auto-generated")
		}
	})

	t.Run("with registry tier resolved", func(t *testing.T) {
		reg := llm.NewModelRegistry()
		reg.Register(&llm.ModelProfile{Name: "test-model", Tier: llm.TierPremium})
		ct := NewCostTracker(WithRegistry(reg))
		p := sampleProfile(1.0, 2.0, llm.TierStandard) // profile's own tier differs from registry
		rec := ct.BuildRecordFromProfile("t", "s", "p", "a", 0, "test-model", p, llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
		// Tier is resolved from registry.Get(model), not from the passed profile
		if rec.Tier != "premium" {
			t.Errorf("Tier should be resolved from registry as premium, got %q", rec.Tier)
		}
	})

	t.Run("nil profile yields zero cost record", func(t *testing.T) {
		ct := NewCostTracker()
		rec := ct.BuildRecordFromProfile("t", "s", "p", "a", 0, "m", nil, llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
		if rec.CostCents != 0 {
			t.Errorf("nil profile should yield 0 cost, got %d", rec.CostCents)
		}
		if rec.Tier != "unknown" {
			t.Errorf("nil profile Tier should be unknown, got %q", rec.Tier)
		}
	})
}

// ============================================================================
// ResolveFallbackChain
// ============================================================================

func TestResolveFallbackChain(t *testing.T) {
	t.Run("nil inputs return nil", func(t *testing.T) {
		reg := llm.NewModelRegistry()
		if got := ResolveFallbackChain(nil, &llm.ModelProfile{}, 3); got != nil {
			t.Error("nil registry should return nil")
		}
		if got := ResolveFallbackChain(reg, nil, 3); got != nil {
			t.Error("nil primary should return nil")
		}
		if got := ResolveFallbackChain(reg, &llm.ModelProfile{Name: "x"}, 0); got != nil {
			t.Error("maxDepth<=0 should return nil")
		}
	})

	t.Run("no fallback configured returns just primary", func(t *testing.T) {
		reg := llm.NewModelRegistry()
		primary := &llm.ModelProfile{Name: "solo"}
		reg.Register(primary)
		chain := ResolveFallbackChain(reg, primary, 3)
		if len(chain) != 1 || chain[0].Name != "solo" {
			t.Errorf("expected [solo], got %v", chainNames(chain))
		}
	})

	t.Run("linear chain pro -> flash", func(t *testing.T) {
		reg := llm.NewModelRegistry()
		pro := &llm.ModelProfile{Name: "pro", FallbackModel: "flash"}
		flash := &llm.ModelProfile{Name: "flash", FallbackModel: ""}
		reg.Register(pro)
		reg.Register(flash)
		chain := ResolveFallbackChain(reg, pro, 3)
		if len(chain) != 2 || chain[0].Name != "pro" || chain[1].Name != "flash" {
			t.Errorf("expected [pro, flash], got %v", chainNames(chain))
		}
	})

	t.Run("maxDepth caps chain length", func(t *testing.T) {
		reg := llm.NewModelRegistry()
		a := &llm.ModelProfile{Name: "a", FallbackModel: "b"}
		b := &llm.ModelProfile{Name: "b", FallbackModel: "c"}
		c := &llm.ModelProfile{Name: "c", FallbackModel: ""}
		reg.Register(a)
		reg.Register(b)
		reg.Register(c)
		chain := ResolveFallbackChain(reg, a, 2)
		if len(chain) != 2 || chain[0].Name != "a" || chain[1].Name != "b" {
			t.Errorf("maxDepth=2 should give [a, b], got %v", chainNames(chain))
		}
	})

	t.Run("circular reference is broken", func(t *testing.T) {
		reg := llm.NewModelRegistry()
		x := &llm.ModelProfile{Name: "x", FallbackModel: "y"}
		y := &llm.ModelProfile{Name: "y", FallbackModel: "x"} // circular
		reg.Register(x)
		reg.Register(y)
		chain := ResolveFallbackChain(reg, x, 10)
		if len(chain) != 2 {
			t.Errorf("circular ref should be broken at length 2, got %v", chainNames(chain))
		}
	})

	t.Run("fallback model not in registry stops chain", func(t *testing.T) {
		reg := llm.NewModelRegistry()
		primary := &llm.ModelProfile{Name: "p", FallbackModel: "missing"}
		reg.Register(primary)
		chain := ResolveFallbackChain(reg, primary, 3)
		if len(chain) != 1 || chain[0].Name != "p" {
			t.Errorf("missing fallback should stop at [p], got %v", chainNames(chain))
		}
	})
}

func chainNames(chain []*llm.ModelProfile) []string {
	names := make([]string, len(chain))
	for i, p := range chain {
		names[i] = p.Name
	}
	return names
}

// ============================================================================
// IsRetryableError
// ============================================================================

func TestIsRetryableError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"context deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline", fmtError("operation: context deadline exceeded"), true},
		{"timeout in message", fmtError("read tcp: i/o timeout"), true},
		{"HTTP 429", fmtError("API error 429: Too Many Requests"), true},
		{"HTTP 500", fmtError("API error 500: Internal Server Error"), true},
		{"HTTP 503", fmtError("API error 503: Service Unavailable"), true},
		{"rate limit", fmtError("rate limit exceeded"), true},
		{"connection refused", fmtError("dial tcp: connection refused"), true},
		{"no such host", fmtError("dial tcp: no such host"), true},
		{"HTTP 400", fmtError("API error 400: Bad Request"), false},
		{"HTTP 401", fmtError("API error 401: Unauthorized"), false},
		{"HTTP 403", fmtError("API error 403: Forbidden"), false},
		{"HTTP 404", fmtError("API error 404: Not Found"), false},
		{"invalid api key", fmtError("invalid api key"), false},
		{"unauthorized", fmtError("401 unauthorized"), false},
		{"model not found", fmtError("model not found: gpt-99"), false},
		{"unknown error defaults retryable", fmtError("something weird happened"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := IsRetryableError(c.err)
			if got != c.want {
				t.Errorf("IsRetryableError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// fmtError wraps a string as an error for table-driven tests.
func fmtError(s string) error { return errors.New(s) }
