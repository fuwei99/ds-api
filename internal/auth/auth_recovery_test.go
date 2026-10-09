package auth

import (
	"context"
	"net/http"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

// TestRecoverAuthFailureRefreshesBeforeSwitching 覆盖共享恢复步骤的第一顺位：
// 本次请求内还没为该账号刷新过时，先强制重新登录，成功就用同一账号原地重试，
// 不换号。
func TestRecoverAuthFailureRefreshesBeforeSwitching(t *testing.T) {
	r, _ := newAuthFailureResolver(t, func(_ context.Context, acc config.Account) (string, error) {
		return "fresh-" + acc.Identifier(), nil
	})
	a := determineManaged(t, r)
	current := a.AccountID

	if got := a.RecoverAuthFailure(context.Background()); got != AuthRecoveryRefreshed {
		t.Fatalf("RecoverAuthFailure = %v, want AuthRecoveryRefreshed", got)
	}
	if a.AccountID != current {
		t.Fatalf("account changed to %q after a successful refresh, want it kept at %q", a.AccountID, current)
	}
	if want := "fresh-" + current; a.DeepSeekToken != want {
		t.Fatalf("token = %q, want %q", a.DeepSeekToken, want)
	}
}

// TestRecoverAuthFailureSwitchesWhenRefreshIsRejected 覆盖第二顺位：本账号刷新
// 不成（登录被上游拒绝）时换到下一个可调度账号，请求得以继续。
func TestRecoverAuthFailureSwitchesWhenRefreshIsRejected(t *testing.T) {
	var first string
	r, _ := newAuthFailureResolver(t, func(_ context.Context, acc config.Account) (string, error) {
		if acc.Identifier() == first {
			return "", ErrLoginRejected
		}
		return "fresh-" + acc.Identifier(), nil
	})
	a := determineManaged(t, r)
	first = a.AccountID

	if got := a.RecoverAuthFailure(context.Background()); got != AuthRecoverySwitched {
		t.Fatalf("RecoverAuthFailure = %v, want AuthRecoverySwitched", got)
	}
	if a.AccountID == first {
		t.Fatalf("account stayed at %q, want a failover to another account", first)
	}
	if a.DeepSeekToken == "" {
		t.Fatal("the switched account must carry a usable token")
	}
}

// TestRecoverAuthFailureCountsRefreshThatUpstreamStillRejects 覆盖"刷新成功但新
// Token 仍被上游拒绝"：这种失败说明账号本身不可用，必须计入账号级鉴权失败，
// 而不是当成又一次可刷新的 Token 过期。
func TestRecoverAuthFailureCountsRefreshThatUpstreamStillRejects(t *testing.T) {
	r, _ := newAuthFailureResolver(t, func(_ context.Context, acc config.Account) (string, error) {
		return "fresh-" + acc.Identifier(), nil
	})
	a := determineManaged(t, r)
	first := a.AccountID

	if got := a.RecoverAuthFailure(context.Background()); got != AuthRecoveryRefreshed {
		t.Fatalf("first recovery = %v, want AuthRecoveryRefreshed", got)
	}
	// 同一个账号第二次收到鉴权失败：刷新额度已用掉，此时应记一次账号级失败并换号。
	if got := a.RecoverAuthFailure(context.Background()); got != AuthRecoverySwitched {
		t.Fatalf("second recovery = %v, want AuthRecoverySwitched", got)
	}

	r.mu.Lock()
	failures := r.authFailures[first]
	r.mu.Unlock()
	if failures != 1 {
		t.Fatalf("consecutive auth failures for %q = %d, want 1", first, failures)
	}
}

// TestRecoverAuthFailureReportsNoneWhenNoAccountIsLeft 覆盖恢复手段用尽：单账号、
// 刷新被拒且没有可换的账号时返回 AuthRecoveryNone，由调用方按失败路径处理。
func TestRecoverAuthFailureReportsNoneWhenNoAccountIsLeft(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[{"email":"only@example.com","password":"pwd","token":"t"}]
	}`)
	store := config.LoadStore()
	r := NewResolver(store, account.NewPool(store), func(_ context.Context, _ config.Account) (string, error) {
		return "", ErrLoginRejected
	})
	acc, ok := store.FindAccount("only@example.com")
	if !ok {
		t.Fatal("test account missing from the store")
	}
	a := &RequestAuth{
		UseConfigToken: true,
		AccountID:      acc.Identifier(),
		Account:        acc,
		DeepSeekToken:  acc.Token,
		TriedAccounts:  map[string]bool{},
		resolver:       r,
	}

	if got := a.RecoverAuthFailure(context.Background()); got != AuthRecoveryNone {
		t.Fatalf("RecoverAuthFailure = %v, want AuthRecoveryNone", got)
	}
}

// TestRecoverAuthFailureIgnoresDirectTokenCallers 覆盖非托管调用方（调用方直传
// Token）：没有账号可刷新也没有账号可换，恢复步骤必须是空操作，否则会把直通
// 请求的 Token 换成号池里别人的。
func TestRecoverAuthFailureIgnoresDirectTokenCallers(t *testing.T) {
	r, _ := newAuthFailureResolver(t, func(_ context.Context, acc config.Account) (string, error) {
		return "fresh-" + acc.Identifier(), nil
	})
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer direct-token")
	a, err := r.Determine(req)
	if err != nil {
		t.Fatalf("determine failed: %v", err)
	}
	if a.UseConfigToken {
		t.Fatal("a caller-supplied token must not be resolved to a managed account")
	}

	if got := a.RecoverAuthFailure(context.Background()); got != AuthRecoveryNone {
		t.Fatalf("RecoverAuthFailure = %v, want AuthRecoveryNone", got)
	}
	if a.DeepSeekToken != "direct-token" {
		t.Fatalf("token = %q, want the caller's own token untouched", a.DeepSeekToken)
	}
}
