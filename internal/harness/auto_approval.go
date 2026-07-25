package harness

// AutoApprovalPolicy 封装前后端一致的自动审批判定规则。
// 只有用户显式允许的标签集合非空时，才可能触发自动审批。
type AutoApprovalPolicy struct {
	// Allowed 是允许自动通过的 tag 集合；空集表示关闭自动审批。
	Allowed map[string]struct{}
}

// NewAutoApprovalPolicy 从标签切片构造策略。
func NewAutoApprovalPolicy(tags []string) *AutoApprovalPolicy {
	allowed := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		allowed[t] = struct{}{}
	}
	return &AutoApprovalPolicy{Allowed: allowed}
}

// ShouldAutoApprove 判定一个审批请求是否应该被自动批准。
// 规则：仅 TagPolicyRule、tags 非空、且所有 tags 都在 Allowed 中。
func (p *AutoApprovalPolicy) ShouldAutoApprove(ruleName string, tags []string) bool {
	if p == nil || len(p.Allowed) == 0 {
		return false
	}
	if ruleName != "TagPolicyRule" {
		return false
	}
	if len(tags) == 0 {
		return false
	}
	for _, tag := range tags {
		if _, ok := p.Allowed[tag]; !ok {
			return false
		}
	}
	return true
}
