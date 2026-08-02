package harness

import (
	"testing"
	"time"

	"github.com/ayanmw/multi-agent-platform/pkg/event"
)

type fakeApprovalEventSender struct{}

func (f *fakeApprovalEventSender) SendEvent(e event.Event) {}

func TestWebSocketApprovalHandler_WaitForDecision_AutoApproveGracePeriod(t *testing.T) {
	h := NewWebSocketApprovalHandler(&fakeApprovalEventSender{})
	h.SetAutoApprovalPolicy(NewAutoApprovalPolicy([]string{"network"}))

	if err := h.RequestApproval("a1", "core/web_search", "test", nil, "TagPolicyRule", "", []string{"network"}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	approved, err := h.WaitForDecision("a1", 100*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Fatal("expected auto-approved")
	}
	if elapsed > 120*time.Millisecond {
		t.Fatalf("auto-approve path should return early, took %v", elapsed)
	}
}

func TestWebSocketApprovalHandler_WaitForDecision_NoMatchFallsThroughToTimeout(t *testing.T) {
	h := NewWebSocketApprovalHandler(&fakeApprovalEventSender{})
	h.SetAutoApprovalPolicy(NewAutoApprovalPolicy([]string{"network"}))

	if err := h.RequestApproval("a2", "run_shell", "test", nil, "TagPolicyRule", "", []string{"exec"}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	approved, err := h.WaitForDecision("a2", 200*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if approved {
		t.Fatal("expected not approved")
	}
	if elapsed < 180*time.Millisecond {
		t.Fatalf("should wait full timeout, took %v", elapsed)
	}
}

func TestWebSocketApprovalHandler_WaitForDecision_EmptyPolicyNoAutoApprove(t *testing.T) {
	h := NewWebSocketApprovalHandler(&fakeApprovalEventSender{})
	h.SetAutoApprovalPolicy(NewAutoApprovalPolicy([]string{}))

	if err := h.RequestApproval("a3", "core/web_search", "test", nil, "TagPolicyRule", "", []string{"network"}); err != nil {
		t.Fatal(err)
	}

	approved, err := h.WaitForDecision("a3", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if approved {
		t.Fatal("expected not approved")
	}
}

func TestWebSocketApprovalHandler_WaitForDecision_FrontendDecisionWins(t *testing.T) {
	h := NewWebSocketApprovalHandler(&fakeApprovalEventSender{})
	h.SetAutoApprovalPolicy(NewAutoApprovalPolicy([]string{"network"}))

	if err := h.RequestApproval("a4", "core/web_search", "test", nil, "TagPolicyRule", "", []string{"network"}); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		h.HandleDecision("a4", false)
	}()

	approved, err := h.WaitForDecision("a4", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved {
		t.Fatal("expected frontend deny")
	}
}
