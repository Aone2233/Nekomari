package oauthutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderResponseBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{"valid", 200, `{"id":"123"}`, true},
		{"error status", 401, `{"id":"123"}`, false},
		{"malformed", 200, "secret-provider-payload", false},
		{"oversized", 200, `{"id":"` + strings.Repeat("x", MaxResponseSize) + `"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			req, _ := http.NewRequest("GET", server.URL, nil)
			var result map[string]string
			err := JSON(req, &result)
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret-provider-payload") {
				t.Fatal("provider payload leaked")
			}
		})
	}
}

func TestProviderRequestHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:1/?appkey=secret", nil)
	var result any
	if err := JSON(req, &result); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unexpected error: %v", err)
	}
}
