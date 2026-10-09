package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/auth"
	"ds2api/internal/config"
	"ds2api/internal/promptcompat"
)

// Regression: when the shared recovery step (auth.RequestAuth.RecoverAuthFailure)
// had already tried and failed to fail over, handleVercelStreamSwitch called
// SwitchAccount a second time. Every SwitchAccount starts with
// Pool.Release(current), so the second call decremented the account's refcount
// again and could hand a slot still held by a concurrent request back to the
// pool.
//
// The scenario below holds the account with two requests (account_max_inflight=2)
// and drives the switch handler through the recovery-failure path: the managed
// login fails, and the single account leaves no failover target. After the handler
// returns, exactly one holder's slot must have been released.
func TestHandleVercelStreamSwitchReleasesAccountOnceWhenRecoveryFails(t *testing.T) {
	t.Setenv("VERCEL", "1")
	t.Setenv("DS2API_VERCEL_INTERNAL_SECRET", "stream-secret")
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[{"email":"acc1@test.com","password":"pwd"}],
		"runtime":{"account_max_inflight":2,"global_max_inflight":2}
	}`)

	store := config.LoadStore()
	pool := account.NewPool(store)
	loginCalls := 0
	resolver := auth.NewResolver(store, pool, func(_ context.Context, acc config.Account) (string, error) {
		loginCalls++
		if loginCalls == 1 {
			return "token-" + acc.Identifier(), nil
		}
		// 恢复路径上的强制刷新 / 换号登录一律失败：刷新与换号都不成。
		return "", errors.New("login failed in test")
	})

	determine := func() *auth.RequestAuth {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("Authorization", "Bearer managed-key")
		a, err := resolver.Determine(req)
		if err != nil {
			t.Fatalf("determine failed: %v", err)
		}
		return a
	}
	holder := determine()
	defer resolver.Release(holder)
	other := determine()
	defer resolver.Release(other)
	if got := pool.Status()["in_use"]; got != 2 {
		t.Fatalf("expected both requests to hold the account, in_use=%v", got)
	}

	h := &Handler{Auth: resolver}
	leaseID := h.holdStreamLease(holder, promptcompat.StandardRequest{}, "")
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions?__stream_switch=1",
		strings.NewReader(`{"lease_id":"`+leaseID+`","auth_failed":true}`),
	)
	req.Header.Set("X-Ds2-Internal-Token", "stream-secret")
	rec := httptest.NewRecorder()

	h.handleVercelStreamSwitch(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "account_unauthorized") {
		t.Fatalf("expected account_unauthorized, got %s", rec.Body.String())
	}
	// 失败请求只释放自己那一次：另一个持有者仍占着一个名额。
	if got := pool.Status()["in_use"]; got != 1 {
		t.Fatalf("expected the account to be released exactly once, in_use=%v", got)
	}
}
