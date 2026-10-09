package auth

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

// newDailyLimitedResolver 构造一个单账号、当天额度只有 1 次的解析器：第一次请求
// 用掉额度，之后的请求应当立即以"没有空闲账号"失败，而不是排队等到跨天重置。
func newDailyLimitedResolver(t *testing.T) *Resolver {
	t.Helper()
	t.Setenv("DS2API_ACCOUNT_DAILY_LIMIT_PATH", filepath.Join(t.TempDir(), "account_daily_limit.json"))
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[{"email":"only@example.com","token":"tok-only"}],
		"runtime":{
			"account_max_inflight":2,
			"account_max_queue":10,
			"account_daily_limit":{"enabled":true,"max_per_day":1}
		}
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	return NewResolver(store, pool, func(_ context.Context, _ config.Account) (string, error) {
		return "fresh-token", nil
	})
}

func managedRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer managed-key")
	return req
}

func TestDetermineReportsNoIdleAccountWhenDailyQuotaIsUsedUp(t *testing.T) {
	r := newDailyLimitedResolver(t)

	first, err := r.Determine(managedRequest(t))
	if err != nil {
		t.Fatalf("the first request must be served: %v", err)
	}
	r.Release(first)

	_, err = r.Determine(managedRequest(t))
	if err == nil {
		t.Fatal("expected the request after the daily limit to fail")
	}
	if err != ErrNoIdleAccount {
		t.Fatalf("expected ErrNoIdleAccount once every account is out of daily quota, got %v", err)
	}
	if !IsNoIdleAccountError(err) {
		t.Fatal("IsNoIdleAccountError must recognise the daily-limit error so protocol adapters map it to 429")
	}
	if err.Error() == "" {
		t.Fatal("the error must carry a message naming the reason")
	}
}

func TestIsNoIdleAccountErrorCoversBothSentinels(t *testing.T) {
	if !IsNoIdleAccountError(ErrNoAccount) {
		t.Fatal("ErrNoAccount belongs to the no-idle-account class")
	}
	if !IsNoIdleAccountError(ErrNoIdleAccount) {
		t.Fatal("ErrNoIdleAccount belongs to the no-idle-account class")
	}
	if IsNoIdleAccountError(ErrUnauthorized) {
		t.Fatal("an unrelated error must not be classified as no-idle-account")
	}
}
