package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"ds2api/internal/config"
)

func TestExtractCreateSessionIDSupportsLegacyShape(t *testing.T) {
	resp := map[string]any{
		"data": map[string]any{
			"biz_data": map[string]any{
				"id": "legacy-session-id",
			},
		},
	}

	if got := extractCreateSessionID(resp); got != "legacy-session-id" {
		t.Fatalf("expected legacy session id, got %q", got)
	}
}

func TestExtractCreateSessionIDSupportsNestedChatSessionShape(t *testing.T) {
	resp := map[string]any{
		"data": map[string]any{
			"biz_data": map[string]any{
				"chat_session": map[string]any{
					"id":         "nested-session-id",
					"model_type": "default",
				},
			},
		},
	}

	if got := extractCreateSessionID(resp); got != "nested-session-id" {
		t.Fatalf("expected nested session id, got %q", got)
	}
}

func TestLoginPayloadUsesAndroidOS(t *testing.T) {
	var capturedPayload map[string]any
	doer := &payloadCaptureDoer{
		onPost: func(payload map[string]any) {
			capturedPayload = payload
		},
	}
	c := &Client{
		regular:  doer,
		fallback: doer,
		cookies:  newCookieJar(),
	}

	acc := config.Account{
		Email:    "test@example.com",
		Password: "password123",
	}

	_, err := c.loginWithDeviceID(context.Background(), acc, "dev-123")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}

	if capturedPayload == nil {
		t.Fatal("expected payload to be captured")
	}
	if gotOS, ok := capturedPayload["os"].(string); !ok || gotOS != "android" {
		t.Fatalf("expected os=android in login payload, got %v", capturedPayload["os"])
	}
	if gotEmail, _ := capturedPayload["email"].(string); gotEmail != "test@example.com" {
		t.Fatalf("expected email=test@example.com, got %v", gotEmail)
	}
	if gotDev, _ := capturedPayload["device_id"].(string); gotDev != "dev-123" {
		t.Fatalf("expected device_id=dev-123, got %v", gotDev)
	}
}

type payloadCaptureDoer struct {
	onPost func(payload map[string]any)
}

func (p *payloadCaptureDoer) Do(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "/login") && req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if p.onPost != nil {
			p.onPost(m)
		}
	}
	resp := map[string]any{
		"code": 0,
		"data": map[string]any{
			"biz_code": 0,
			"biz_data": map[string]any{
				"user": map[string]any{
					"token": "test-token",
					"id":    "test-id",
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}, nil
}
