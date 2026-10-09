package completionruntime

import (
	"context"
	"errors"
	"net/http"
	"testing"

	dsclient "ds2api/internal/deepseek/client"
)

func TestIsTransientUpstreamStatus(t *testing.T) {
	cases := map[int]bool{
		http.StatusBadRequest:          false,
		http.StatusUnauthorized:        false,
		http.StatusTooManyRequests:     false,
		http.StatusInternalServerError: true,
		http.StatusBadGateway:          true,
		http.StatusServiceUnavailable:  true,
		http.StatusGatewayTimeout:      true,
	}
	for status, want := range cases {
		if got := IsTransientUpstreamStatus(status); got != want {
			t.Fatalf("IsTransientUpstreamStatus(%d) = %v want %v", status, got, want)
		}
	}
}

func TestIsTransientUpstreamErrorMessage(t *testing.T) {
	transient := []string{
		"服务器暂时不可用",
		"系统繁忙，请稍后重试",
		"Service temporarily unavailable",
		"upstream timeout",
	}
	for _, msg := range transient {
		if !IsTransientUpstreamErrorMessage(msg) {
			t.Fatalf("expected %q to be transient", msg)
		}
	}
	permanent := []string{
		"内容超长，请删减后再试",
		"content filter",
		"",
	}
	for _, msg := range permanent {
		if IsTransientUpstreamErrorMessage(msg) {
			t.Fatalf("expected %q to be non-transient", msg)
		}
	}
}

func TestCompletionCallOutputErrorClassification(t *testing.T) {
	if got := completionCallOutputError(context.Background(), errors.New("boom")); got == nil || got.Code != "upstream_unavailable" {
		t.Fatalf("transport error should map to upstream_unavailable, got %#v", got)
	}
	if got := completionCallOutputError(context.Background(), &dsclient.RequestFailure{Kind: dsclient.FailureMuted}); got == nil || got.Code != "account_muted" {
		t.Fatalf("muted error should map to account_muted, got %#v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := completionCallOutputError(ctx, errors.New("boom")); got == nil || got.Code != "error" {
		t.Fatalf("canceled context should keep generic error, got %#v", got)
	}
}

func TestTransientUpstreamError(t *testing.T) {
	if got := transientUpstreamError(http.StatusBadRequest, "bad"); got != nil {
		t.Fatalf("non-transient status should return nil, got %#v", got)
	}
	got := transientUpstreamError(http.StatusServiceUnavailable, "  服务器暂时不可用  ")
	if got == nil || got.Code != "upstream_unavailable" || got.Status != http.StatusServiceUnavailable || got.Message != "服务器暂时不可用" {
		t.Fatalf("unexpected transient error: %#v", got)
	}
}
