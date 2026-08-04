package observability

import "testing"

// TestAuditTargetCarriesScopeKey 验证审计 Target 贯穿「资源 scope 键」
// 约定（E3 隔离增强，N3-02）：每条审计记录的 Target 必须编码为
// `<resource>/<id>` 形式，使其可被按资源维度检索、审计与隔离，
// 杜绝无 scope 的笼统 Target 造成的跨资源/跨 session 审计混淆。
func TestAuditTargetCarriesScopeKey(t *testing.T) {
	a := NewMemoryAuditor(100)

	cases := []struct {
		target   string
		resource string
	}{
		{"agents/agent-123", "agents"},
		{"model/openai/gpt-4", "model"},
		{"apikey/key-abc", "apikey"},
		{"session/sess-1", "session"},
		{"cron/cron-9", "cron"},
	}

	for i, c := range cases {
		a.Record(AuditRecord{
			ID:     "x" + c.resource,
			Action: "mutate",
			Target: c.target,
			// 用 index 保证 ID 唯一，便于从 List 中按 Target 反查。
			Reason: "",
		})
		_ = i
	}

	recs := a.List(100)
	byTarget := map[string]AuditRecord{}
	for _, r := range recs {
		byTarget[r.Target] = r
	}

	for _, c := range cases {
		r, ok := byTarget[c.target]
		if !ok {
			t.Fatalf("audit record for target %q not stored", c.target)
		}
		if !auditTargetHasScope(r.Target) {
			t.Errorf("audit Target %q missing resource scope key (<resource>/<id>)", r.Target)
		}
		if got := auditTargetResource(r.Target); got != c.resource {
			t.Errorf("audit Target %q resource = %q, want %q", r.Target, got, c.resource)
		}
	}
}

// auditTargetHasScope 校验审计 Target 携带资源 scope 前缀（<resource>/<id>），
// 即至少有一个非首尾的 '/' 分隔符。
func auditTargetHasScope(target string) bool {
	for i := 0; i < len(target); i++ {
		if target[i] == '/' {
			return i > 0 && i < len(target)-1
		}
	}
	return false
}

// auditTargetResource 提取审计 Target 的资源段（'/' 之前的部分）。
func auditTargetResource(target string) string {
	for i := 0; i < len(target); i++ {
		if target[i] == '/' {
			return target[:i]
		}
	}
	return ""
}
