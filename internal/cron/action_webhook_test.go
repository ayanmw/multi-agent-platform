package cron

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRunWebhookBlocksPrivateAddress 验证 webhook action 默认拒绝 loopback、
// link-local 和私有地址。
func TestRunWebhookBlocksPrivateAddress(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"localhost", "http://localhost:8080/hook", "private"},
		{"127.0.0.1", "http://127.0.0.1/hook", "private"},
		{"private IPv4", "http://192.168.1.1/hook", "private"},
		{"link local", "http://169.254.1.1/hook", "private"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewActionRunner(ActionRunnerConfig{})
			_, err := r.Run(context.Background(), Cron{ID: "c", ActionType: ActionWebhook}, map[string]any{
				"url": tt.url,
			})
			if err == nil {
				t.Fatalf("expected error for %s", tt.url)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestRunWebhookAllowsPrivateWhenConfigured 验证 WebhookAllowPrivate=true 时
// 本地 webhook 可以访问。
func TestRunWebhookAllowsPrivateWhenConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewActionRunner(ActionRunnerConfig{WebhookAllowPrivate: true})
	_, err := r.Run(context.Background(), Cron{ID: "c", ActionType: ActionWebhook}, map[string]any{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatalf("expected local webhook to be allowed: %v", err)
	}
}

// TestRunWebhookBlocksFileScheme 验证 file:// 等非 http/https scheme 被拒绝。
func TestRunWebhookBlocksFileScheme(t *testing.T) {
	r := NewActionRunner(ActionRunnerConfig{})
	_, err := r.Run(context.Background(), Cron{ID: "c", ActionType: ActionWebhook}, map[string]any{
		"url": "file:///etc/passwd",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported URL scheme") {
		t.Fatalf("expected unsupported scheme error, got %v", err)
	}
}
