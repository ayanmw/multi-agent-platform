package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// stubClassifier —— 一个确定性 Provider，返回固定的 intent 字符串
// ---------------------------------------------------------------------------

// stubClassifier 是一个最小化的 Provider 实现，用于在无网络访问的情况下
// 驱动 Router 的 classifyIntent 路径。它忽略请求并返回预设的 ChatResponse，
// 其第一个 choice 携带脚本化的 intent 字符串。Chat 调用次数会被记录以供断言。
//
// stubClassifier 刻意只实现 Provider 的两个方法（Chat 与 ChatStream）。
// Name() 返回 "stub-classifier"。
type stubClassifier struct {
	intent      string // 脚本响应中返回的 content
	chatErr     error  // 非 nil 时 Chat 返回该 error
	chatCalls   int
	streamCalls int
}

func (s *stubClassifier) Name() string { return "stub-classifier" }

func (s *stubClassifier) Chat(req ChatRequest) (*ChatResponse, error) {
	s.chatCalls++
	if s.chatErr != nil {
		return nil, s.chatErr
	}
	return &ChatResponse{
		Choices: []Choice{
			{
				Index:        0,
				Message:      Message{Role: "assistant", Content: s.intent},
				FinishReason: "stop",
			},
		},
	}, nil
}

func (s *stubClassifier) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	s.streamCalls++
	if onChunk != nil {
		_ = onChunk(StreamChunk{Delta: Delta{Content: s.intent}})
	}
	return s.intent, Usage{}, nil, nil
}

// 编译期断言：stubClassifier 满足 Provider 接口。
var _ Provider = (*stubClassifier)(nil)

// ---------------------------------------------------------------------------
// 构建带若干 model profile 的 registry 的 helper
// ---------------------------------------------------------------------------

// newRegistryWith 返回一个预填充给定 profile 的 ModelRegistry。
func newRegistryWith(profiles ...*ModelProfile) *ModelRegistry {
	r := NewModelRegistry()
	for _, p := range profiles {
		r.Register(p)
	}
	return r
}

// profileFor 是一个轻量 ModelProfile builder，带合理默认值，
// 让测试用例只需指定关心的字段。
func profileFor(name string, tier ModelTier, caps []ModelCapability, ctxWindow int, fallback string) *ModelProfile {
	return &ModelProfile{
		Name:             name,
		Provider:         "test",
		Tier:             tier,
		Capabilities:     caps,
		InputPrice:       1.0,
		OutputPrice:      2.0,
		MaxContextWindow: ctxWindow,
		MaxOutputTokens:  4096,
		RateLimitRPM:     100,
		FallbackModel:    fallback,
		AvgLatencyMs:     500,
	}
}

// ---------------------------------------------------------------------------
// keywordClassify（Router fallback 路径）
// ---------------------------------------------------------------------------

// TestKeywordClassify 测试分类 Provider 不可用时的基于规则的回退分类器。
// 每一行断言给定输入会通过关键字匹配被归入期望的 intent 类别。
func TestKeywordClassify(t *testing.T) {
	r := &Router{} // keywordClassify 不使用 registry 或 classifier
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// multi_step 指示词（最先检查，因此即便短语也命中 code 关键字，
		// 仍按 multi_step 胜出）
		{"multi-step-hyphen", "run a multi-step pipeline", "multi_step"},
		{"multi-step-space", "do this in multi step fashion", "multi_step"},
		{"orchestrate", "orchestrate multiple agents", "multi_step"},
		{"first-then", "first do X, then do Y, after that finish", "multi_step"},
		{"subtask", "decompose into subtasks", "multi_step"},
		{"pipeline-keyword", "build a pipeline for ETL", "multi_step"},

		// code_generation 指示词
		{"write-code", "please write code to sort a list", "code_generation"},
		{"implement", "implement a new function", "code_generation"},
		{"debug", "help me debug this error", "code_generation"},
		{"refactor", "refactor this class", "code_generation"},
		{"unit-test", "write a unit test", "code_generation"},
		{"api-endpoint", "add a new api endpoint", "code_generation"},
		{"fix-bug", "fix bug in parser", "code_generation"},

		// complex_reasoning 指示词
		{"analyze", "analyze this dataset", "complex_reasoning"},
		{"architecture", "design the architecture for X", "complex_reasoning"},
		{"compare", "compare options A and B", "complex_reasoning"},
		{"trade-off", "evaluate the trade-off", "complex_reasoning"},
		{"math", "prove this mathematical theorem using logic", "complex_reasoning"},

		// simple_chat 默认
		{"greeting", "hello there", "simple_chat"},
		{"empty", "", "simple_chat"},
		{"chitchat", "how are you today", "simple_chat"},
		{"unknown", "this is some random text with no keywords", "simple_chat"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.keywordClassify(tc.input)
			if got != tc.want {
				t.Errorf("keywordClassify(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestKeywordClassifyCaseInsensitive 验证关键字匹配器大小写不敏感
//（匹配前会把输入转小写）。
func TestKeywordClassifyCaseInsensitive(t *testing.T) {
	r := &Router{}
	if got := r.keywordClassify("IMPLEMENT a function"); got != "code_generation" {
		t.Errorf("uppercase IMPLEMENT: got %q, want code_generation", got)
	}
	if got := r.keywordClassify("WRITE CODE"); got != "code_generation" {
		t.Errorf("uppercase WRITE CODE: got %q, want code_generation", got)
	}
}

// ---------------------------------------------------------------------------
// intentToTier
// ---------------------------------------------------------------------------

// TestIntentToTier 验证 intent 到层级的映射，以及未知 intent 回退到
// 最便宜层级（TierEfficient）。
func TestIntentToTier(t *testing.T) {
	r := &Router{}
	tests := []struct {
		intent string
		want   ModelTier
	}{
		{"simple_chat", TierEfficient},
		{"code_generation", TierStandard},
		{"complex_reasoning", TierPremium},
		{"multi_step", TierStandard},
		{"unknown_intent", TierEfficient}, // 默认
		{"", TierEfficient},
		{"SIMPLE_CHAT", TierEfficient}, // 大小写敏感不匹配，走默认
	}
	for _, tc := range tests {
		t.Run(tc.intent, func(t *testing.T) {
			got := r.intentToTier(tc.intent)
			if got != tc.want {
				t.Errorf("intentToTier(%q) = %s, want %s", tc.intent, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// classifyIntent（使用分类 Provider）
// ---------------------------------------------------------------------------

// TestClassifyIntentValidCategories 验证分类器返回已知类别时，
// classifyIntent 会将其透传。对四个合法类别做表驱动测试。
func TestClassifyIntentValidCategories(t *testing.T) {
	for _, intent := range []string{"simple_chat", "code_generation", "complex_reasoning", "multi_step"} {
		t.Run(intent, func(t *testing.T) {
			r := NewRouter(NewModelRegistry(), &stubClassifier{intent: intent})
			got, err := r.classifyIntent(context.Background(), "anything")
			if err != nil {
				t.Fatalf("classifyIntent: %v", err)
			}
			if got != intent {
				t.Errorf("got %q, want %q", got, intent)
			}
		})
	}
}

// TestClassifyIntentCaseInsensitive 验证分类器在匹配已知类别前
// 会把响应归一化为小写。
func TestClassifyIntentCaseInsensitive(t *testing.T) {
	r := NewRouter(NewModelRegistry(), &stubClassifier{intent: "Code_Generation"})
	got, err := r.classifyIntent(context.Background(), "x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "code_generation" {
		t.Errorf("got %q, want code_generation", got)
	}
}

// TestClassifyIntentTrimsWhitespace 验证分类器响应中的首尾空白
// 会在匹配前被剥离。
func TestClassifyIntentTrimsWhitespace(t *testing.T) {
	r := NewRouter(NewModelRegistry(), &stubClassifier{intent: "  multi_step  "})
	got, err := r.classifyIntent(context.Background(), "x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "multi_step" {
		t.Errorf("got %q, want multi_step", got)
	}
}

// TestClassifyIntentUnknownDefaultsToSimpleChat 验证无法识别的分类器输出
// 默认为 "simple_chat"，而不是返回 error。
func TestClassifyIntentUnknownDefaultsToSimpleChat(t *testing.T) {
	r := NewRouter(NewModelRegistry(), &stubClassifier{intent: "totally-unknown-category"})
	got, err := r.classifyIntent(context.Background(), "x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "simple_chat" {
		t.Errorf("got %q, want simple_chat", got)
	}
}

// TestClassifyIntentError 验证分类器 error 会被包装并返回。
// 这驱动 Select 内部回退到 keywordClassify。
func TestClassifyIntentError(t *testing.T) {
	sentinel := errors.New("network down")
	r := NewRouter(NewModelRegistry(), &stubClassifier{chatErr: sentinel})
	_, err := r.classifyIntent(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "classifier call failed") {
		t.Errorf("error should mention 'classifier call failed', got %q", err.Error())
	}
}

// TestClassifyIntentEmptyResponse 验证分类器返回空 Choices slice 时会得到 error。
func TestClassifyIntentEmptyResponse(t *testing.T) {
	r := NewRouter(NewModelRegistry(), &emptyChoiceClassifier{})
	_, err := r.classifyIntent(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("error should mention 'empty response', got %q", err.Error())
	}
}

// emptyChoiceClassifier 是一个返回无 Choices 的 ChatResponse 的 Provider，
// 用于触发 classifyIntent 的 empty-response 分支。
type emptyChoiceClassifier struct{}

func (emptyChoiceClassifier) Name() string { return "empty" }
func (emptyChoiceClassifier) Chat(req ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{Choices: nil}, nil
}
func (emptyChoiceClassifier) ChatStream(req ChatRequest, onChunk func(StreamChunk) error) (string, Usage, []ToolCall, error) {
	return "", Usage{}, nil, nil
}

var _ Provider = emptyChoiceClassifier{}

// ---------------------------------------------------------------------------
// Select —— 用 stub 分类器做端到端路由
// ---------------------------------------------------------------------------

// TestSelectByClassifierIntent 验证 Router 的 Select 会从分类器 intent
// 对应的层级中选 model。对四个 intent 做表驱动测试。
func TestSelectByClassifierIntent(t *testing.T) {
	// Registry：每个层级一个 model，都满足无能力要求的请求。
	reg := newRegistryWith(
		profileFor("efficient-m", TierEfficient, nil, 8192, ""),
		profileFor("standard-m", TierStandard, nil, 8192, ""),
		profileFor("premium-m", TierPremium, nil, 8192, ""),
	)

	tests := []struct {
		intent    string
		wantModel string
		wantTier  ModelTier
	}{
		{"simple_chat", "efficient-m", TierEfficient},
		{"code_generation", "standard-m", TierStandard},
		{"complex_reasoning", "premium-m", TierPremium},
		{"multi_step", "standard-m", TierStandard},
	}
	for _, tc := range tests {
		t.Run(tc.intent, func(t *testing.T) {
			r := NewRouter(reg, &stubClassifier{intent: tc.intent})
			dec, err := r.Select(context.Background(), &RouteRequest{UserInput: "x"})
			if err != nil {
				t.Fatalf("Select: %v", err)
			}
			if dec.Primary == nil {
				t.Fatal("Primary is nil")
			}
			if dec.Primary.Name != tc.wantModel {
				t.Errorf("Primary.Name = %q, want %q", dec.Primary.Name, tc.wantModel)
			}
			if dec.Tier != tc.wantTier {
				t.Errorf("Tier = %s, want %s", dec.Tier, tc.wantTier)
			}
			if dec.Intent != tc.intent {
				t.Errorf("Intent = %q, want %q", dec.Intent, tc.intent)
			}
		})
	}
}

// TestSelectFallsBackToKeywordOnClassifierError 验证当分类
// Provider 失败时，Select 会回退到 keywordClassify 并仍能正确路由。
func TestSelectFallsBackToKeywordOnClassifierError(t *testing.T) {
	reg := newRegistryWith(
		profileFor("efficient-m", TierEfficient, nil, 8192, ""),
		profileFor("standard-m", TierStandard, nil, 8192, ""),
		profileFor("premium-m", TierPremium, nil, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{chatErr: errors.New("classifier unavailable")})

	// "implement a function" 命中 "implement" code 关键字 → code_generation → TierStandard。
	dec, err := r.Select(context.Background(), &RouteRequest{UserInput: "implement a function"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary.Name != "standard-m" {
		t.Errorf("Primary.Name = %q, want standard-m", dec.Primary.Name)
	}
	if dec.Intent != "code_generation" {
		t.Errorf("Intent = %q, want code_generation", dec.Intent)
	}
	// Sanity：分类器确实被调用过（且失败了），证明走了 fallback 路径。
	if r.classifier.(*stubClassifier).chatCalls != 1 {
		t.Errorf("classifier chat calls = %d, want 1", r.classifier.(*stubClassifier).chatCalls)
	}
}

// TestSelectFallbackChainResolved 验证 RouteDecision.Fallback 通过
// registry 的 GetFallback（primary 的 FallbackModel 字段）解析得到。
func TestSelectFallbackChainResolved(t *testing.T) {
	reg := newRegistryWith(
		profileFor("standard-m", TierStandard, nil, 8192, "efficient-m"),
		profileFor("efficient-m", TierEfficient, nil, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{UserInput: "x"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Fallback == nil {
		t.Fatal("Fallback is nil, want efficient-m")
	}
	if dec.Fallback.Name != "efficient-m" {
		t.Errorf("Fallback.Name = %q, want efficient-m", dec.Fallback.Name)
	}
}

// TestSelectNoFallbackWhenNotConfigured 验证当选中的 primary 未配置
// FallbackModel 时，RouteDecision.Fallback 为 nil。
func TestSelectNoFallbackWhenNotConfigured(t *testing.T) {
	reg := newRegistryWith(
		profileFor("standard-m", TierStandard, nil, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{UserInput: "x"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Fallback != nil {
		t.Errorf("Fallback = %v, want nil", dec.Fallback)
	}
}

// TestSelectFilterByCapability 验证 RequiredCaps 会过滤掉缺少所需能力的
// model，即使它们在目标层级内。
func TestSelectFilterByCapability(t *testing.T) {
	// standard 层级有两个 model：一个带 tool calling，一个不带。
	reg := newRegistryWith(
		profileFor("standard-notool", TierStandard, nil, 8192, ""),
		profileFor("standard-tool", TierStandard, []ModelCapability{CapToolCalling}, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput:    "x",
		RequiredCaps: []ModelCapability{CapToolCalling},
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary.Name != "standard-tool" {
		t.Errorf("Primary.Name = %q, want standard-tool (the only one with CapToolCalling)", dec.Primary.Name)
	}
}

// TestSelectFilterByContextLen 验证 ContextLen 会过滤掉上下文窗口不足的 model。
func TestSelectFilterByContextLen(t *testing.T) {
	reg := newRegistryWith(
		profileFor("small-ctx", TierStandard, nil, 4096, ""),
		profileFor("big-ctx", TierStandard, nil, 32768, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput:  "x",
		ContextLen: 8192, // 超过 small-ctx 的 4096
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary.Name != "big-ctx" {
		t.Errorf("Primary.Name = %q, want big-ctx", dec.Primary.Name)
	}
}

// TestSelectNoSuitableModelReturnsError 验证当没有 model 满足硬性要求时，
// Select 会返回含 "no suitable model" 的 error。
func TestSelectNoSuitableModelReturnsError(t *testing.T) {
	// 仅一个 model，且不支持 vision。请求要求 vision。
	reg := newRegistryWith(
		profileFor("no-vision", TierStandard, nil, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	_, err := r.Select(context.Background(), &RouteRequest{
		UserInput:    "x",
		RequiredCaps: []ModelCapability{CapVision},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no suitable model") {
		t.Errorf("error should mention 'no suitable model', got %q", err.Error())
	}
}

// TestSelectPreferredTierEscalates 验证 PreferredTier 会与 intent 推导出的
// 层级通过 max(...) 合并，让调用方可以强制升到比 intent 单独决定的更高的层级。
func TestSelectPreferredTierEscalates(t *testing.T) {
	reg := newRegistryWith(
		profileFor("efficient-m", TierEfficient, nil, 8192, ""),
		profileFor("premium-m", TierPremium, nil, 8192, ""),
	)
	// simple_chat 本会选 TierEfficient，但 PreferredTier=Premium 升级。
	r := NewRouter(reg, &stubClassifier{intent: "simple_chat"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput:     "x",
		PreferredTier: TierPremium,
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary.Name != "premium-m" {
		t.Errorf("Primary.Name = %q, want premium-m (PreferredTier escalation)", dec.Primary.Name)
	}
}

// TestSelectReasonPopulated 验证 RouteDecision.Reason 字段被填入人类可读说明，
// 包含 intent 与 model 名（白盒透明度）。
func TestSelectReasonPopulated(t *testing.T) {
	reg := newRegistryWith(
		profileFor("standard-m", TierStandard, nil, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{UserInput: "x"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Reason == "" {
		t.Fatal("Reason is empty")
	}
	if !strings.Contains(dec.Reason, "code_generation") {
		t.Errorf("Reason should contain intent, got %q", dec.Reason)
	}
	if !strings.Contains(dec.Reason, "standard-m") {
		t.Errorf("Reason should contain model name, got %q", dec.Reason)
	}
}

// TestSelectModelShorthand 验证 SelectModel 只返回 model 名，
// 并透传 Select 的 error。
func TestSelectModelShorthand(t *testing.T) {
	reg := newRegistryWith(
		profileFor("standard-m", TierStandard, nil, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	name, err := r.SelectModel(context.Background(), &RouteRequest{UserInput: "x"})
	if err != nil {
		t.Fatalf("SelectModel: %v", err)
	}
	if name != "standard-m" {
		t.Errorf("SelectModel = %q, want standard-m", name)
	}
}

// TestSelectModelShorthandError 验证找不到合适 model 时，
// SelectModel 会透出 Select 的 error。
func TestSelectModelShorthandError(t *testing.T) {
	reg := NewModelRegistry() // 空
	r := NewRouter(reg, &stubClassifier{intent: "simple_chat"})
	_, err := r.SelectModel(context.Background(), &RouteRequest{UserInput: "x"})
	if err == nil {
		t.Fatal("expected error from empty registry")
	}
}

// ---------------------------------------------------------------------------
// 空 registry 下的 Select
// ---------------------------------------------------------------------------

// TestSelectEmptyRegistryReturnsError 验证空 ModelRegistry 加任意请求
// 会得到 "no suitable model" error。
func TestSelectEmptyRegistryReturnsError(t *testing.T) {
	r := NewRouter(NewModelRegistry(), &stubClassifier{intent: "simple_chat"})
	_, err := r.Select(context.Background(), &RouteRequest{UserInput: "hi"})
	if err == nil || !strings.Contains(err.Error(), "no suitable model") {
		t.Fatalf("err = %v, want 'no suitable model'", err)
	}
}

// ---------------------------------------------------------------------------
// ModelRegistry（Router 的伴生组件 —— 经由 Router 依赖被测试覆盖）
// ---------------------------------------------------------------------------

// TestModelRegistryGetMissingReturnsNil 验证对不存在的名字调用 Get
// 返回 nil（而非 error），符合文档契约。
func TestModelRegistryGetMissingReturnsNil(t *testing.T) {
	reg := NewModelRegistry()
	if got := reg.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}
}

// TestModelRegistryGetByTierEmpty 验证对空层级调用 GetByTier
// 返回空（非 nil）slice。
func TestModelRegistryGetByTierEmpty(t *testing.T) {
	reg := NewModelRegistry()
	got := reg.GetByTier(TierPremium)
	if got == nil {
		t.Fatal("GetByTier returned nil slice")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

// TestModelRegistryGetFallbackMissing 验证当指定 model 未注册时
// GetFallback 返回 nil。
func TestModelRegistryGetFallbackMissing(t *testing.T) {
	reg := NewModelRegistry()
	if got := reg.GetFallback("nope"); got != nil {
		t.Errorf("GetFallback(missing) = %v, want nil", got)
	}
}

// TestModelRegistryGetFallbackNoFallbackConfigured 验证当 model 存在
// 但 FallbackModel 字段为空时，GetFallback 返回 nil。
func TestModelRegistryGetFallbackNoFallbackConfigured(t *testing.T) {
	reg := newRegistryWith(profileFor("m", TierStandard, nil, 8192, ""))
	if got := reg.GetFallback("m"); got != nil {
		t.Errorf("GetFallback = %v, want nil when FallbackModel is empty", got)
	}
}

// TestModelRegistryRegisterOverwrites 验证以已存在的名字注册 profile
// 会覆盖之前的条目。
func TestModelRegistryRegisterOverwrites(t *testing.T) {
	reg := NewModelRegistry()
	reg.Register(profileFor("m", TierStandard, nil, 8192, ""))
	reg.Register(profileFor("m", TierPremium, nil, 8192, "")) // 覆盖

	got := reg.Get("m")
	if got == nil {
		t.Fatal("Get(m) = nil")
	}
	if got.Tier != TierPremium {
		t.Errorf("Tier = %s, want Premium (overwritten)", got.Tier)
	}
}

// ---------------------------------------------------------------------------
// ModelProfile helper（Router 过滤使用）
// ---------------------------------------------------------------------------

// TestModelProfileHasCapability 是 HasCapability 的表驱动测试。
func TestModelProfileHasCapability(t *testing.T) {
	mp := &ModelProfile{Capabilities: []ModelCapability{CapToolCalling, CapStreaming}}
	tests := []struct {
		cap  ModelCapability
		want bool
	}{
		{CapToolCalling, true},
		{CapStreaming, true},
		{CapVision, false},
		{CapReasoning, false},
		{CapJSONMode, false},
	}
	for _, tc := range tests {
		t.Run(string(tc.cap), func(t *testing.T) {
			if got := mp.HasCapability(tc.cap); got != tc.want {
				t.Errorf("HasCapability(%s) = %v, want %v", tc.cap, got, tc.want)
			}
		})
	}
}

// TestModelProfileSupportsContextLen 验证上下文窗口检查。
func TestModelProfileSupportsContextLen(t *testing.T) {
	mp := &ModelProfile{MaxContextWindow: 8192}
	tests := []struct {
		tokens int
		want   bool
	}{
		{0, true},
		{1, true},
		{8192, true},   // 恰好相等
		{8193, false},  // 多 1
		{100000, false},
	}
	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			if got := mp.SupportsContextLen(tc.tokens); got != tc.want {
				t.Errorf("SupportsContextLen(%d) = %v, want %v", tc.tokens, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ModelTier.String()
// ---------------------------------------------------------------------------

// TestModelTierString 验证人类可读的层级名，以及未知层级映射到 "unknown"。
func TestModelTierString(t *testing.T) {
	tests := []struct {
		tier ModelTier
		want string
	}{
		{TierFree, "free"},
		{TierEfficient, "efficient"},
		{TierLightweight, "lightweight"},
		{TierStandard, "standard"},
		{TierPremium, "premium"},
		{ModelTier(99), "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.tier.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DefaultProfiles（对 registry 的种子数据做 sanity check）
// ---------------------------------------------------------------------------

// TestDefaultProfilesShape 对 DefaultProfiles 做 sanity check：
// 返回两个 profile，名字符合预期，且 pro model 的 fallback 指向 flash。
func TestDefaultProfilesShape(t *testing.T) {
	profiles := DefaultProfiles()
	if len(profiles) != 2 {
		t.Fatalf("expected 2 default profiles, got %d", len(profiles))
	}
	byName := map[string]*ModelProfile{}
	for _, p := range profiles {
		byName[p.Name] = p
	}
	flash, ok := byName["deepseek-v4-flash"]
	if !ok {
		t.Fatal("missing deepseek-v4-flash")
	}
	pro, ok := byName["deepseek-v4-pro"]
	if !ok {
		t.Fatal("missing deepseek-v4-pro")
	}
	if flash.FallbackModel != "" {
		t.Errorf("flash FallbackModel = %q, want empty", flash.FallbackModel)
	}
	if pro.FallbackModel != "deepseek-v4-flash" {
		t.Errorf("pro FallbackModel = %q, want deepseek-v4-flash", pro.FallbackModel)
	}
	if !flash.HasCapability(CapToolCalling) {
		t.Error("flash should support CapToolCalling")
	}
	if !pro.HasCapability(CapReasoning) {
		t.Error("pro should support CapReasoning")
	}
}

// ---------------------------------------------------------------------------
// NewRouter 构造器
// ---------------------------------------------------------------------------

// TestNewRouterNilArgs 验证 NewRouter 在传入 nil 参数时不会 panic ——
// 它只是把它们存起来。对这样的 Router 调用 Select 会在 nil registry 上
// panic，但构造本身必须安全。
func TestNewRouterNilArgs(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewRouter with nil args panicked: %v", r)
		}
	}()
	r := NewRouter(nil, nil)
	if r == nil {
		t.Fatal("NewRouter returned nil")
	}
}

// ---------------------------------------------------------------------------
// BudgetUSD 过滤
// ---------------------------------------------------------------------------

// TestSelectFiltersByBudgetUSD 验证 InputPrice 超过预算上限的 model
// 会被排除出候选。
func TestSelectFiltersByBudgetUSD(t *testing.T) {
	// standard-m 的 InputPrice=1.0；budget=0.5 意味着应被过滤。
	reg := newRegistryWith(
		profileFor("expensive-m", TierStandard, nil, 8192, ""), // InputPrice=1.0
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	_, err := r.Select(context.Background(), &RouteRequest{
		UserInput: "x",
		BudgetUSD: 0.5,
	})
	if err == nil || !strings.Contains(err.Error(), "no suitable model") {
		t.Errorf("expected 'no suitable model' error when budget is too low, got: %v", err)
	}
}

// TestSelectPassesByBudgetUSD 验证预算内的 model 仍会被选中。
func TestSelectPassesByBudgetUSD(t *testing.T) {
	reg := newRegistryWith(
		profileFor("cheap-m", TierEfficient, nil, 8192, ""), // InputPrice=1.0
	)
	r := NewRouter(reg, &stubClassifier{intent: "simple_chat"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput: "x",
		BudgetUSD: 1.0, // 恰好到上限 —— InputPrice=1.0 不大于 1.0，所以通过
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary == nil || dec.Primary.Name != "cheap-m" {
		t.Errorf("Primary.Name = %v, want cheap-m (budget should allow it)", dec.Primary)
	}
}

// ---------------------------------------------------------------------------
// LatencyReq 过滤
// ---------------------------------------------------------------------------

// TestSelectFiltersByLatencyReq 验证 AvgLatencyMs 超过延迟要求的 model
// 会被排除出候选。
func TestSelectFiltersByLatencyReq(t *testing.T) {
	// profileFor 默认 AvgLatencyMs=500。设 300ms 上限以排除它。
	slowModel := profileFor("slow-m", TierStandard, nil, 8192, "")
	slowModel.AvgLatencyMs = 500

	reg := newRegistryWith(
		slowModel,
		profileFor("fast-m", TierEfficient, nil, 8192, ""), // 默认 AvgLatencyMs=500
	)
	// 把 fast-m 真正调快
	for _, m := range reg.List() {
		if m.Name == "fast-m" {
			m.AvgLatencyMs = 200
		}
	}

	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput:    "x",
		LatencyReq:   250 * time.Millisecond,
		PreferredTier: TierEfficient, // 强制层级，让 cheap-m 先被尝试
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary.Name != "fast-m" {
		t.Errorf("Primary.Name = %q, want fast-m (slow-m filtered by latency)",
			dec.Primary.Name)
	}
}

// ---------------------------------------------------------------------------
// BudgetUSD / LatencyReq 零值时禁用过滤（向后兼容）
// ---------------------------------------------------------------------------

// TestSelectBudgetUSDZeroDisablesFiltering 验证 BudgetUSD=0 表示
// 不应用预算过滤（保留既有行为）。
func TestSelectBudgetUSDZeroDisablesFiltering(t *testing.T) {
	reg := newRegistryWith(
		profileFor("standard-m", TierStandard, nil, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput: "x",
		// BudgetUSD 默认 0 —— 不应用预算过滤
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary == nil || dec.Primary.Name != "standard-m" {
		t.Errorf("Primary.Name = %v, want standard-m (no budget filter)", dec.Primary)
	}
}
