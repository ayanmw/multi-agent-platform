package harness

import (
	"testing"
)

func TestAutoApprovalPolicy_ShouldAutoApprove(t *testing.T) {
	lowRisk := NewAutoApprovalPolicy([]string{"network", "mcp"})

	cases := []struct {
		name     string
		rule     string
		tags     []string
		want     bool
	}{
		{"TagPolicyRule network only", "TagPolicyRule", []string{"network"}, true},
		{"TagPolicyRule network+mcp", "TagPolicyRule", []string{"network", "mcp"}, true},
		{"ApprovalRule ignored", "ApprovalRule", []string{"network"}, false},
		{"exec not allowed", "TagPolicyRule", []string{"exec"}, false},
		{"mixed tags not allowed", "TagPolicyRule", []string{"network", "exec"}, false},
		{"empty tags", "TagPolicyRule", []string{}, false},
		{"explicitly allowed high-risk", "TagPolicyRule", []string{"exec"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "explicitly allowed high-risk" {
				policy := NewAutoApprovalPolicy([]string{"network", "exec"})
				if got := policy.ShouldAutoApprove(tc.rule, tc.tags); got != true {
					t.Fatalf("expected true, got %v", got)
				}
				return
			}
			if got := lowRisk.ShouldAutoApprove(tc.rule, tc.tags); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestAutoApprovalPolicy_EmptySetDisables(t *testing.T) {
	empty := NewAutoApprovalPolicy([]string{})
	if empty.ShouldAutoApprove("TagPolicyRule", []string{"network"}) {
		t.Fatal("empty policy should not auto-approve")
	}
}

func TestAutoApprovalPolicy_NilPolicy(t *testing.T) {
	var nilPolicy *AutoApprovalPolicy
	if nilPolicy.ShouldAutoApprove("TagPolicyRule", []string{"network"}) {
		t.Fatal("nil policy should not auto-approve")
	}
}
