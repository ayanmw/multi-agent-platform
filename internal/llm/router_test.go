package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// stubClassifier — a deterministic Provider that returns a fixed intent string
// ---------------------------------------------------------------------------

// stubClassifier is a minimal Provider implementation used to drive the Router's
// classifyIntent path without any network access. It ignores the request and
// returns a pre-configured ChatResponse whose first choice carries the scripted
// intent string. The number of Chat calls is recorded for assertion.
//
// stubClassifier intentionally implements only the two Provider methods (Chat and
// ChatStream). Name() returns "stub-classifier".
type stubClassifier struct {
	intent      string // content returned in the scripted response
	chatErr     error  // if non-nil, Chat returns this error
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

// Compile-time assertion that stubClassifier satisfies the Provider interface.
var _ Provider = (*stubClassifier)(nil)

// ---------------------------------------------------------------------------
// helpers for building registries with a few model profiles
// ---------------------------------------------------------------------------

// newRegistryWith returns a ModelRegistry pre-populated with the given profiles.
func newRegistryWith(profiles ...*ModelProfile) *ModelRegistry {
	r := NewModelRegistry()
	for _, p := range profiles {
		r.Register(p)
	}
	return r
}

// profileFor is a small builder for ModelProfile with sensible defaults so test
// cases only specify the fields they care about.
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
// keywordClassify (Router fallback path)
// ---------------------------------------------------------------------------

// TestKeywordClassify exercises the rule-based fallback classifier that runs
// when the classifier Provider is unavailable. Each row asserts that the given
// input is classified into the expected intent category via keyword matching.
func TestKeywordClassify(t *testing.T) {
	r := &Router{} // keywordClassify does not use registry or classifier
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// multi_step indicators (checked first, so a phrase that also matches
		// a code keyword still wins as multi_step)
		{"multi-step-hyphen", "run a multi-step pipeline", "multi_step"},
		{"multi-step-space", "do this in multi step fashion", "multi_step"},
		{"orchestrate", "orchestrate multiple agents", "multi_step"},
		{"first-then", "first do X, then do Y, after that finish", "multi_step"},
		{"subtask", "decompose into subtasks", "multi_step"},
		{"pipeline-keyword", "build a pipeline for ETL", "multi_step"},

		// code_generation indicators
		{"write-code", "please write code to sort a list", "code_generation"},
		{"implement", "implement a new function", "code_generation"},
		{"debug", "help me debug this error", "code_generation"},
		{"refactor", "refactor this class", "code_generation"},
		{"unit-test", "write a unit test", "code_generation"},
		{"api-endpoint", "add a new api endpoint", "code_generation"},
		{"fix-bug", "fix bug in parser", "code_generation"},

		// complex_reasoning indicators
		{"analyze", "analyze this dataset", "complex_reasoning"},
		{"architecture", "design the architecture for X", "complex_reasoning"},
		{"compare", "compare options A and B", "complex_reasoning"},
		{"trade-off", "evaluate the trade-off", "complex_reasoning"},
		{"math", "prove this mathematical theorem using logic", "complex_reasoning"},

		// simple_chat default
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

// TestKeywordClassifyCaseInsensitive verifies the keyword matcher is
// case-insensitive (it lowercases the input before matching).
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

// TestIntentToTier verifies the intent-to-tier mapping and that unknown intents
// fall back to the cheapest tier (TierEfficient).
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
		{"unknown_intent", TierEfficient}, // default
		{"", TierEfficient},
		{"SIMPLE_CHAT", TierEfficient}, // not matched (case-sensitive), defaults
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
// classifyIntent (uses classifier Provider)
// ---------------------------------------------------------------------------

// TestClassifyIntentValidCategories verifies that when the classifier returns a
// known category, classifyIntent passes it through. Table-driven across all
// four valid categories.
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

// TestClassifyIntentCaseInsensitive verifies that the classifier normalizes its
// response to lowercase before matching against known categories.
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

// TestClassifyIntentTrimsWhitespace verifies that surrounding whitespace in the
// classifier's response is stripped before matching.
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

// TestClassifyIntentUnknownDefaultsToSimpleChat verifies that an unrecognized
// classifier output defaults to "simple_chat" rather than returning an error.
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

// TestClassifyIntentError verifies that a classifier error is wrapped and
// returned. This drives the fallback to keywordClassify inside Select.
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

// TestClassifyIntentEmptyResponse verifies that a classifier returning an empty
// Choices slice yields an error.
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

// emptyChoiceClassifier is a Provider that returns a ChatResponse with no
// Choices, used to exercise the empty-response branch of classifyIntent.
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
// Select — end-to-end routing with stub classifier
// ---------------------------------------------------------------------------

// TestSelectByClassifierIntent verifies that the Router's Select method picks a
// model from the tier matching the classifier's intent. Table-driven across
// the four intents.
func TestSelectByClassifierIntent(t *testing.T) {
	// Registry: one model per tier, all satisfying a no-cap request.
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

// TestSelectFallsBackToKeywordOnClassifierError verifies that when the classifier
// Provider fails, Select falls back to keywordClassify and still routes correctly.
func TestSelectFallsBackToKeywordOnClassifierError(t *testing.T) {
	reg := newRegistryWith(
		profileFor("efficient-m", TierEfficient, nil, 8192, ""),
		profileFor("standard-m", TierStandard, nil, 8192, ""),
		profileFor("premium-m", TierPremium, nil, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{chatErr: errors.New("classifier unavailable")})

	// "implement a function" matches the "implement" code keyword → code_generation → TierStandard.
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
	// Sanity: the classifier was actually called (and failed), proving the
	// fallback path was taken.
	if r.classifier.(*stubClassifier).chatCalls != 1 {
		t.Errorf("classifier chat calls = %d, want 1", r.classifier.(*stubClassifier).chatCalls)
	}
}

// TestSelectFallbackChainResolved verifies that RouteDecision.Fallback is
// resolved via the registry's GetFallback (primary's FallbackModel field).
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

// TestSelectNoFallbackWhenNotConfigured verifies that RouteDecision.Fallback is
// nil when the selected primary has no FallbackModel configured.
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

// TestSelectFilterByCapability verifies that RequiredCaps filters out models
// lacking the needed capability, even if they are in the target tier.
func TestSelectFilterByCapability(t *testing.T) {
	// standard tier has two models: one with tool calling, one without.
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

// TestSelectFilterByContextLen verifies that ContextLen filters out models with
// insufficient context windows.
func TestSelectFilterByContextLen(t *testing.T) {
	reg := newRegistryWith(
		profileFor("small-ctx", TierStandard, nil, 4096, ""),
		profileFor("big-ctx", TierStandard, nil, 32768, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput:  "x",
		ContextLen: 8192, // exceeds small-ctx's 4096
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary.Name != "big-ctx" {
		t.Errorf("Primary.Name = %q, want big-ctx", dec.Primary.Name)
	}
}

// TestSelectNoSuitableModelReturnsError verifies that when no model satisfies the
// hard requirements, Select returns an error mentioning "no suitable model".
func TestSelectNoSuitableModelReturnsError(t *testing.T) {
	// Only one model, and it lacks vision. Request vision.
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

// TestSelectPreferredTierEscalates verifies that PreferredTier is combined with
// the intent-derived tier via max(...), allowing callers to force a higher tier
// than the intent alone would select.
func TestSelectPreferredTierEscalates(t *testing.T) {
	reg := newRegistryWith(
		profileFor("efficient-m", TierEfficient, nil, 8192, ""),
		profileFor("premium-m", TierPremium, nil, 8192, ""),
	)
	// simple_chat would pick TierEfficient, but PreferredTier=Premium escalates.
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

// TestSelectReasonPopulated verifies that the RouteDecision.Reason field is
// populated with a human-readable explanation containing the intent and model
// name (white-box transparency).
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

// TestSelectModelShorthand verifies that SelectModel returns just the model name
// and propagates errors from Select.
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

// TestSelectModelShorthandError verifies that SelectModel surfaces the Select
// error when no suitable model is found.
func TestSelectModelShorthandError(t *testing.T) {
	reg := NewModelRegistry() // empty
	r := NewRouter(reg, &stubClassifier{intent: "simple_chat"})
	_, err := r.SelectModel(context.Background(), &RouteRequest{UserInput: "x"})
	if err == nil {
		t.Fatal("expected error from empty registry")
	}
}

// ---------------------------------------------------------------------------
// Select with empty registry
// ---------------------------------------------------------------------------

// TestSelectEmptyRegistryReturnsError verifies that an empty ModelRegistry plus
// any request yields the "no suitable model" error.
func TestSelectEmptyRegistryReturnsError(t *testing.T) {
	r := NewRouter(NewModelRegistry(), &stubClassifier{intent: "simple_chat"})
	_, err := r.Select(context.Background(), &RouteRequest{UserInput: "hi"})
	if err == nil || !strings.Contains(err.Error(), "no suitable model") {
		t.Fatalf("err = %v, want 'no suitable model'", err)
	}
}

// ---------------------------------------------------------------------------
// ModelRegistry (companion to Router — exercised via Router's deps)
// ---------------------------------------------------------------------------

// TestModelRegistryGetMissingReturnsNil verifies that Get on a missing name
// returns nil (not an error), per the documented contract.
func TestModelRegistryGetMissingReturnsNil(t *testing.T) {
	reg := NewModelRegistry()
	if got := reg.Get("missing"); got != nil {
		t.Errorf("Get(missing) = %v, want nil", got)
	}
}

// TestModelRegistryGetByTierEmpty verifies that GetByTier on an empty tier
// returns an empty (non-nil) slice.
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

// TestModelRegistryGetFallbackMissing verifies that GetFallback returns nil when
// the named model is not registered.
func TestModelRegistryGetFallbackMissing(t *testing.T) {
	reg := NewModelRegistry()
	if got := reg.GetFallback("nope"); got != nil {
		t.Errorf("GetFallback(missing) = %v, want nil", got)
	}
}

// TestModelRegistryGetFallbackNoFallbackConfigured verifies GetFallback returns
// nil when the model exists but has an empty FallbackModel field.
func TestModelRegistryGetFallbackNoFallbackConfigured(t *testing.T) {
	reg := newRegistryWith(profileFor("m", TierStandard, nil, 8192, ""))
	if got := reg.GetFallback("m"); got != nil {
		t.Errorf("GetFallback = %v, want nil when FallbackModel is empty", got)
	}
}

// TestModelRegistryRegisterOverwrites verifies that registering a profile with
// an existing name overwrites the previous entry.
func TestModelRegistryRegisterOverwrites(t *testing.T) {
	reg := NewModelRegistry()
	reg.Register(profileFor("m", TierStandard, nil, 8192, ""))
	reg.Register(profileFor("m", TierPremium, nil, 8192, "")) // overwrite

	got := reg.Get("m")
	if got == nil {
		t.Fatal("Get(m) = nil")
	}
	if got.Tier != TierPremium {
		t.Errorf("Tier = %s, want Premium (overwritten)", got.Tier)
	}
}

// ---------------------------------------------------------------------------
// ModelProfile helpers (used by Router's filtering)
// ---------------------------------------------------------------------------

// TestModelProfileHasCapability is a table-driven test of HasCapability.
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

// TestModelProfileSupportsContextLen verifies the context window check.
func TestModelProfileSupportsContextLen(t *testing.T) {
	mp := &ModelProfile{MaxContextWindow: 8192}
	tests := []struct {
		tokens int
		want   bool
	}{
		{0, true},
		{1, true},
		{8192, true},   // exactly equal
		{8193, false},  // one over
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

// TestModelTierString verifies the human-readable tier names and that unknown
// tiers map to "unknown".
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
// DefaultProfiles (sanity check the registry's seeded data)
// ---------------------------------------------------------------------------

// TestDefaultProfilesShape sanity-checks DefaultProfiles: it returns two
// profiles with expected names and the pro model's fallback points to flash.
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
// NewRouter constructor
// ---------------------------------------------------------------------------

// TestNewRouterNilArgs verifies that NewRouter does not panic when given nil
// arguments — it simply stores them. Calling Select on such a Router would
// panic on the nil registry, but construction itself must be safe.
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
// BudgetUSD filtering
// ---------------------------------------------------------------------------

// TestSelectFiltersByBudgetUSD verifies that a model whose InputPrice exceeds the
// budget ceiling is excluded from candidate selection.
func TestSelectFiltersByBudgetUSD(t *testing.T) {
	// standard-m has InputPrice=1.0; budget=0.5 means it should be filtered out.
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

// TestSelectPassesByBudgetUSD verifies that a model within budget is still selected.
func TestSelectPassesByBudgetUSD(t *testing.T) {
	reg := newRegistryWith(
		profileFor("cheap-m", TierEfficient, nil, 8192, ""), // InputPrice=1.0
	)
	r := NewRouter(reg, &stubClassifier{intent: "simple_chat"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput: "x",
		BudgetUSD: 1.0, // exactly at the limit — InputPrice=1.0 is NOT > 1.0, so passes
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary == nil || dec.Primary.Name != "cheap-m" {
		t.Errorf("Primary.Name = %v, want cheap-m (budget should allow it)", dec.Primary)
	}
}

// ---------------------------------------------------------------------------
// LatencyReq filtering
// ---------------------------------------------------------------------------

// TestSelectFiltersByLatencyReq verifies that models whose AvgLatencyMs exceeds
// the latency requirement are excluded from candidate selection.
func TestSelectFiltersByLatencyReq(t *testing.T) {
	// profileFor defaults to AvgLatencyMs=500. Set a 300ms ceiling to exclude it.
	slowModel := profileFor("slow-m", TierStandard, nil, 8192, "")
	slowModel.AvgLatencyMs = 500

	reg := newRegistryWith(
		slowModel,
		profileFor("fast-m", TierEfficient, nil, 8192, ""), // AvgLatencyMs=500 default
	)
	// Make fast-m actually fast
	for _, m := range reg.List() {
		if m.Name == "fast-m" {
			m.AvgLatencyMs = 200
		}
	}

	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput:    "x",
		LatencyReq:   250 * time.Millisecond,
		PreferredTier: TierEfficient, // force tier so cheap-m is tried first
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
// Zero-value BudgetUSD / LatencyReq disables filtering (backward compat)
// ---------------------------------------------------------------------------

// TestSelectBudgetUSDZeroDisablesFiltering verifies that BudgetUSD=0 means no
// budget filter is applied (existing behavior preserved).
func TestSelectBudgetUSDZeroDisablesFiltering(t *testing.T) {
	reg := newRegistryWith(
		profileFor("standard-m", TierStandard, nil, 8192, ""),
	)
	r := NewRouter(reg, &stubClassifier{intent: "code_generation"})

	dec, err := r.Select(context.Background(), &RouteRequest{
		UserInput: "x",
		// BudgetUSD defaults to 0 — budget filter not applied
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if dec.Primary == nil || dec.Primary.Name != "standard-m" {
		t.Errorf("Primary.Name = %v, want standard-m (no budget filter)", dec.Primary)
	}
}
