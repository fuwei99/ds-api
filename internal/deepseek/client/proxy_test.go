package client

import (
	dsprotocol "ds2api/internal/deepseek/protocol"
	"net/http"
	"strings"
	"testing"
)

func TestApplyProxyConnectivityHeadersUsesBaseHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://chat.deepseek.com/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest returned error: %v", err)
	}

	applyProxyConnectivityHeaders(req)

	for key, want := range dsprotocol.BaseHeaders {
		if got := req.Header.Get(key); got != want {
			t.Fatalf("expected header %q=%q, got %q", key, want, got)
		}
	}
}

func TestProxyConnectivityStatus(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		success    bool
		wantText   string
	}{
		{name: "ok", statusCode: 200, success: true, wantText: "HTTP 200"},
		{name: "challenge", statusCode: 403, success: true, wantText: "风控或挑战"},
		{name: "upstream error", statusCode: 502, success: false, wantText: "HTTP 502"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			success, message := proxyConnectivityStatus(tc.statusCode)
			if success != tc.success {
				t.Fatalf("expected success=%v, got %v", tc.success, success)
			}
			if message == "" || !strings.Contains(message, tc.wantText) {
				t.Fatalf("expected message to contain %q, got %q", tc.wantText, message)
			}
		})
	}
}
