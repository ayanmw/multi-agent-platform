package cron_test

import (
	"testing"

	"github.com/ayanmw/multi-agent-platform/internal/cron"
)

// 测试 cron.URLValidator 对 webhook URL 的 scheme 与私有地址校验。
// URLValidator 是导出类型，测试放在 cron_test 包以验证公开 API。

func TestURLValidator_ValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		allowPrivate bool
		wantErr      string
	}{
		{"valid https", "https://example.com/hook", false, ""},
		{"valid http with port", "http://example.com:8080/hook", false, ""},
		{"file scheme", "file:///etc/passwd", false, "unsupported URL scheme"},
		{"ftp scheme", "ftp://example.com/hook", false, "unsupported URL scheme"},
		{"localhost blocked", "http://localhost:8080/hook", false, "private"},
		{"127.0.0.1 blocked", "http://127.0.0.1/hook", false, "private"},
		{"private IPv4 blocked", "http://192.168.1.1/hook", false, "private"},
		{"private IPv6 blocked", "http://[fc00::1]/hook", false, "private"},
		{"link local blocked", "http://169.254.1.1/hook", false, "private"},
		{"zero unspecified blocked", "http://0.0.0.0/hook", false, "private"},
		{"localhost allowed when configured", "http://localhost:8080/hook", true, ""},
		{"private allowed when configured", "http://10.0.0.1/hook", true, ""},
		{"missing host", "http:///hook", false, "URL host is empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := cron.URLValidator{AllowPrivate: tt.allowPrivate}
			err := v.ValidateWebhookURL(tt.url)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestNormalizePrivateEnv(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"True", true},
		{"1", true},
		{"yes", true},
		{"", false},
		{"false", false},
		{"0", false},
		{"no", false},
		{"maybe", false},
	}
	for _, c := range cases {
		if got := cron.NormalizePrivateEnv(c.in); got != c.want {
			t.Errorf("NormalizePrivateEnv(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func contains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
