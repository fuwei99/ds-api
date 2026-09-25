package account

import (
	"context"
	"fmt"
	"testing"
	"time"

	"ds2api/internal/config"
)

func newRateLimitedPoolForTest(t *testing.T, enabled bool, limit int, accounts ...string) (*Pool, *config.Store) {
	t.Helper()
	accountsJSON := ""
	for i, acc := range accounts {
		if i > 0 {
			accountsJSON += ","
		}
		accountsJSON += fmt.Sprintf(`{"email":%q,"token":"tok-%s"}`, acc, acc)
	}
	cfgJSON := fmt.Sprintf(`{
		"keys":["test-key"],
		"accounts":[%s],
		"runtime":{
			"account_max_inflight":10,
			"account_max_queue":10,
			"account_rate_limit":{
				"enabled":%t,
				"max_per_minute":%d
			}
		}
	}`, accountsJSON, enabled, limit)

	t.Setenv("DS2API_CONFIG_JSON", cfgJSON)
	store := config.LoadStore()
	pool := NewPool(store)
	return pool, store
}

func TestAccountRateLimitMarkedUnavailableAndQueues(t *testing.T) {
	pool, _ := newRateLimitedPoolForTest(t, true, 2, "acc1@example.com")
	// Use 150ms window for fast test
	pool.SetRateLimitWindow(150 * time.Millisecond)

	// First request: window starts, count = 1
	acc1, ok := pool.Acquire("", nil, nil)
	if !ok {
		t.Fatal("expected first acquire to succeed")
	}
	pool.Release(acc1.Identifier())
	if pool.IsAccountRateLimited("acc1@example.com") {
		t.Fatal("account should not be rate limited after 1 request when limit is 2")
	}

	// Second request: count = 2 (reaches limit) -> marked unavailable
	acc2, ok := pool.Acquire("", nil, nil)
	if !ok {
		t.Fatal("expected second acquire to succeed")
	}
	pool.Release(acc2.Identifier())
	if !pool.IsAccountRateLimited("acc1@example.com") {
		t.Fatal("account should be rate limited after 2 requests")
	}

	// Immediate Acquire should fail because account is unavailable
	if _, ok := pool.Acquire("", nil, nil); ok {
		t.Fatal("expected acquire to fail when account is rate limited")
	}

	// Check status
	status := pool.Status()
	if status["available"].(int) != 0 {
		t.Fatalf("expected available = 0, got %v", status["available"])
	}
	rateLimited := status["rate_limited_accounts"].([]string)
	if len(rateLimited) != 1 || rateLimited[0] != "acc1@example.com" {
		t.Fatalf("expected acc1@example.com in rate_limited_accounts, got %v", rateLimited)
	}

	// Third request via AcquireWait should queue, then succeed after 150ms window resets
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resCh := make(chan string, 1)
	go func() {
		acc, ok := pool.AcquireWait(ctx, "", nil, nil)
		if ok {
			resCh <- acc.Identifier()
		} else {
			resCh <- ""
		}
	}()

	waitForWaitingCount(t, pool, 1)

	select {
	case id := <-resCh:
		if id != "acc1@example.com" {
			t.Fatalf("expected acquire to succeed with acc1@example.com after reset, got %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued request to be awakened by rate limit reset")
	}

	// Account should now be in a new window with count=1, and not unavailable
	if pool.IsAccountRateLimited("acc1@example.com") {
		t.Fatal("account should not be marked unavailable right after reset")
	}
}

func TestAccountRateLimitMultiAccountFallback(t *testing.T) {
	pool, _ := newRateLimitedPoolForTest(t, true, 1, "acc1@example.com", "acc2@example.com")
	pool.SetRateLimitWindow(150 * time.Millisecond)

	// 1st request gets an account (e.g. acc1 or acc2)
	first, ok := pool.Acquire("", nil, nil)
	if !ok {
		t.Fatal("expected first acquire to succeed")
	}
	pool.Release(first.Identifier())
	if !pool.IsAccountRateLimited(first.Identifier()) {
		t.Fatalf("expected %s to be rate limited", first.Identifier())
	}

	// 2nd request should get the other account
	second, ok := pool.Acquire("", nil, nil)
	if !ok {
		t.Fatal("expected second acquire to succeed with the other account")
	}
	if second.Identifier() == first.Identifier() {
		t.Fatalf("expected different account, got %s twice", second.Identifier())
	}
	pool.Release(second.Identifier())
	if !pool.IsAccountRateLimited(second.Identifier()) {
		t.Fatalf("expected %s to be rate limited", second.Identifier())
	}

	// Both are now unavailable! 3rd immediate acquire fails
	if _, ok := pool.Acquire("", nil, nil); ok {
		t.Fatal("expected acquire to fail when all accounts are rate limited")
	}

	// 3rd request queues via AcquireWait and succeeds after window resets
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	acc, ok := pool.AcquireWait(ctx, "", nil, nil)
	if !ok {
		t.Fatal("expected AcquireWait to succeed after reset")
	}
	if acc.Identifier() == "" {
		t.Fatal("expected valid account identifier")
	}
}

func TestAccountRateLimitDisabledAllowsUnlimited(t *testing.T) {
	pool, _ := newRateLimitedPoolForTest(t, false, 2, "acc1@example.com")
	pool.SetRateLimitWindow(150 * time.Millisecond)

	for i := 0; i < 10; i++ {
		acc, ok := pool.Acquire("", nil, nil)
		if !ok {
			t.Fatalf("step %d: expected acquire to succeed when rate limit disabled", i)
		}
		pool.Release(acc.Identifier())
		if pool.IsAccountRateLimited("acc1@example.com") {
			t.Fatalf("step %d: account should never be rate limited when disabled", i)
		}
	}
}

func TestAccountRateLimitSyncRuntimeSettings(t *testing.T) {
	pool, store := newRateLimitedPoolForTest(t, true, 1, "acc1@example.com")
	pool.SetRateLimitWindow(time.Minute)

	acc, ok := pool.Acquire("", nil, nil)
	if !ok {
		t.Fatal("expected acquire to succeed")
	}
	pool.Release(acc.Identifier())
	if !pool.IsAccountRateLimited("acc1@example.com") {
		t.Fatal("expected acc1 to be rate limited")
	}

	// Dynamically disable rate limit in store
	disabled := false
	_ = store.Update(func(c *config.Config) error {
		c.Runtime.AccountRateLimit.Enabled = &disabled
		return nil
	})
	pool.SyncRateLimits()

	if pool.IsAccountRateLimited("acc1@example.com") {
		t.Fatal("account should immediately be available after disabling rate limit")
	}
	acc2, ok := pool.Acquire("", nil, nil)
	if !ok || acc2.Identifier() != "acc1@example.com" {
		t.Fatal("expected immediate acquire to succeed after disabling rate limit")
	}
	pool.Release(acc2.Identifier())
}
