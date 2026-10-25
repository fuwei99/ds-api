package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

// newAuthFailureResolver 构造一个开启弹性号池（启用前两个账号，第三个休眠）
// 的三账号解析器，用于验证"连续鉴权失败 -> 移出号池 -> 自动补位"的行为。
func newAuthFailureResolver(t *testing.T, login LoginFunc) (*Resolver, *config.Store) {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[
			{"email":"a@example.com","password":"pwd","token":"ta"},
			{"email":"b@example.com","password":"pwd","token":"tb"},
			{"email":"c@example.com","password":"pwd","token":"tc"}
		],
		"elastic_pool":{"enabled":true,"global_count":2}
	}`)
	store := config.LoadStore()
	// 配置加载不会自动重算弹性号池，测试里显式算一次，让第三个账号进入休眠。
	if err := store.Update(func(c *config.Config) error {
		account.ReconcileElasticPool(c)
		return nil
	}); err != nil {
		t.Fatalf("reconcile elastic pool failed: %v", err)
	}
	pool := account.NewPool(store)
	return NewResolver(store, pool, login), store
}

func accountState(t *testing.T, store *config.Store, identifier string) config.Account {
	t.Helper()
	acc, ok := store.FindAccount(identifier)
	if !ok {
		t.Fatalf("account %q not found", identifier)
	}
	return acc
}

// determineManaged 模拟一次托管请求的取号过程。
func determineManaged(t *testing.T, r *Resolver) *RequestAuth {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer managed-key")
	a, err := r.Determine(req)
	if err != nil {
		t.Fatalf("determine failed: %v", err)
	}
	return a
}

// TestDetermineRetiresAccountAfterRepeatedRejectedLogins 覆盖用户诉求的主路径：
// 第一次轮到这个账号时只换号（请求不中断）；下一次又轮到他、且重新登录仍被拒时，
// 把它判为异常账号移出号池，休眠账号自动补位。
func TestDetermineRetiresAccountAfterRepeatedRejectedLogins(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(_ context.Context, acc config.Account) (string, error) {
		if acc.Identifier() == "a@example.com" {
			return "", ErrLoginRejected
		}
		return "token-" + acc.Identifier(), nil
	})

	first := determineManaged(t, r)
	if first.AccountID != "b@example.com" {
		t.Fatalf("first request should fail over to b, got %q", first.AccountID)
	}
	r.Release(first)
	if accountState(t, store, "a@example.com").AuthFailed {
		t.Error("a single rejected login must not retire the account yet")
	}
	if !accountState(t, store, "c@example.com").Disabled {
		t.Error("the standby account should still be sleeping after the first failure")
	}

	second := determineManaged(t, r)
	if second.AccountID != "b@example.com" {
		t.Fatalf("second request should also land on b, got %q", second.AccountID)
	}
	r.Release(second)
	retired := accountState(t, store, "a@example.com")
	if !retired.AuthFailed {
		t.Error("the account should be retired once its login is rejected again")
	}
	if !retired.Disabled {
		t.Error("the retired account should be disabled")
	}
	if got := accountState(t, store, "c@example.com"); got.Disabled {
		t.Error("the standby account should be backfilled into the pool")
	}
}

func TestMarkAuthFailureFirstOccurrenceKeepsAccountInPool(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "fresh-token", nil
	})
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}

	if r.MarkAuthFailure(a, "token rejected") {
		t.Fatal("first failure must not classify the account as anomalous")
	}
	acc := accountState(t, store, "a@example.com")
	if acc.AuthFailed {
		t.Error("account must not be flagged auth_failed after a single failure")
	}
	if !acc.IsEnabled() {
		t.Error("account must stay enabled so the request can still be retried on it")
	}
}

func TestMarkAuthFailureSecondOccurrenceRemovesAccountAndBackfills(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "fresh-token", nil
	})
	if got := accountState(t, store, "c@example.com"); !got.Disabled {
		t.Fatal("standby account c should start disabled")
	}
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}

	r.MarkAuthFailure(a, "token rejected")
	if !r.MarkAuthFailure(a, "token rejected") {
		t.Fatal("second consecutive failure must classify the account as anomalous")
	}
	acc := accountState(t, store, "a@example.com")
	if !acc.AuthFailed {
		t.Error("account should be flagged auth_failed")
	}
	if acc.IsEnabled() {
		t.Error("auth-failed account should be disabled")
	}
	if acc.DisabledReason == "" {
		t.Error("auth-failed account should record a disabled_reason for the admin")
	}
	if acc.IsSchedulable() {
		t.Error("auth-failed account must not be schedulable")
	}
	if got := accountState(t, store, "c@example.com"); got.Disabled {
		t.Error("standby account c should be backfilled into the pool")
	}
}

func TestMarkAuthFailureIgnoresDirectTokenRequests(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "fresh-token", nil
	})
	a := &RequestAuth{UseConfigToken: false, AccountID: "a@example.com", resolver: r}
	for i := 0; i < authFailureThreshold+1; i++ {
		if r.MarkAuthFailure(a, "token rejected") {
			t.Fatal("direct-token requests must never flag a pooled account")
		}
	}
	if acc := accountState(t, store, "a@example.com"); acc.AuthFailed {
		t.Error("account must stay untouched for direct-token requests")
	}
}

func TestNoteLoginSuccessClearsAuthFailedState(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "fresh-token", nil
	})
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}
	r.MarkAuthFailure(a, "token rejected")
	r.MarkAuthFailure(a, "token rejected")
	if !accountState(t, store, "a@example.com").AuthFailed {
		t.Fatal("precondition: account should be flagged auth_failed")
	}

	r.NoteLoginSuccess("a@example.com")

	acc := accountState(t, store, "a@example.com")
	if acc.AuthFailed {
		t.Error("successful login should clear the auth_failed flag")
	}
	if !acc.IsEnabled() {
		t.Error("account should be re-enabled after a successful login")
	}
	if acc.DisabledReason != "" {
		t.Errorf("disabled_reason should be cleared, got %q", acc.DisabledReason)
	}
	if got := accountState(t, store, "c@example.com"); !got.Disabled {
		t.Error("the backfilled account should return to standby once a recovers")
	}
}

func TestNoteLoginSuccessResetsFailureCounter(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "fresh-token", nil
	})
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}
	r.MarkAuthFailure(a, "token rejected")
	r.NoteLoginSuccess("a@example.com")
	if r.MarkAuthFailure(a, "token rejected") {
		t.Fatal("failure counter should restart after a successful login")
	}
	if acc := accountState(t, store, "a@example.com"); acc.AuthFailed {
		t.Error("account must not be flagged after a single failure following a reset")
	}
}

func TestRefreshTokenErrMarksAuthFailureWhenLoginRejected(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "", ErrLoginRejected
	})
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}

	if err := r.RefreshTokenErr(context.Background(), a); !errors.Is(err, ErrLoginRejected) {
		t.Fatalf("expected ErrLoginRejected, got %v", err)
	}
	if accountState(t, store, "a@example.com").AuthFailed {
		t.Fatal("a single rejected re-login must not classify the account yet")
	}
	if err := r.RefreshTokenErr(context.Background(), a); !errors.Is(err, ErrLoginRejected) {
		t.Fatalf("expected ErrLoginRejected, got %v", err)
	}
	if acc := accountState(t, store, "a@example.com"); !acc.AuthFailed {
		t.Error("repeated rejected re-logins should classify the account as anomalous")
	}
}

func TestRefreshTokenErrDoesNotCountTransientLoginFailures(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "", errors.New("dial tcp: connection reset")
	})
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}
	for i := 0; i < authFailureThreshold+1; i++ {
		_ = r.RefreshTokenErr(context.Background(), a)
	}
	if acc := accountState(t, store, "a@example.com"); acc.AuthFailed {
		t.Error("network failures must not classify a healthy account as anomalous")
	}
}

func TestRefreshTokenOnceRefreshesEachAccountOnce(t *testing.T) {
	loginCalls := 0
	r, _ := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		loginCalls++
		return "fresh-token", nil
	})
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}

	if !a.RefreshTokenOnce(context.Background()) {
		t.Fatal("first refresh should succeed")
	}
	if a.RefreshTokenOnce(context.Background()) {
		t.Fatal("second refresh for the same account must be skipped")
	}
	if loginCalls != 1 {
		t.Fatalf("expected exactly one login attempt, got %d", loginCalls)
	}

	// 换号后新账号拥有独立的刷新额度。
	a.AccountID = "b@example.com"
	if !a.RefreshTokenOnce(context.Background()) {
		t.Fatal("a different account should get its own refresh budget")
	}
	if loginCalls != 2 {
		t.Fatalf("expected two login attempts, got %d", loginCalls)
	}
}

// TestSuccessfulUseBreaksConsecutiveAuthFailures 覆盖"相邻请求"语义：账号在一次
// 请求中鉴权失败后，只要下一次真正使用它时正常跑完（Release 结算），历史失败
// 计数就清零，不会与更早的偶发失败累加后被误判为鉴权异常。
func TestSuccessfulUseBreaksConsecutiveAuthFailures(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "fresh-token", nil
	})
	failed := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}
	if r.MarkAuthFailure(failed, "token rejected") {
		t.Fatal("first failure must not retire the account")
	}

	// 下一次请求正常使用了该账号：Release 结算应清零历史失败计数。
	ok := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}
	ok.markAccountUsed()
	r.Release(ok)

	if r.MarkAuthFailure(failed, "token rejected") {
		t.Fatal("a successful use between failures must break the consecutive chain")
	}
	if acc := accountState(t, store, "a@example.com"); acc.AuthFailed {
		t.Error("account must not be retired when failures are not adjacent")
	}
}

// TestReleaseKeepsCounterWhenRequestRecordedAuthFailure 验证同一请求内发生过
// 鉴权失败时 Release 不会清零计数：相邻请求上的失败仍然会累计并退役账号。
func TestReleaseKeepsCounterWhenRequestRecordedAuthFailure(t *testing.T) {
	r, store := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "fresh-token", nil
	})
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}
	a.markAccountUsed()
	if r.MarkAuthFailure(a, "token rejected") {
		t.Fatal("first failure must not retire the account")
	}
	r.Release(a)

	next := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}
	if !r.MarkAuthFailure(next, "token rejected") {
		t.Fatal("failures on adjacent requests must still retire the account")
	}
	if !accountState(t, store, "a@example.com").AuthFailed {
		t.Error("account should be retired after two adjacent failures")
	}
}

// TestRejectedRefreshCountsFailureOnce 是 P1 的回归测试：重新登录被上游拒绝时，
// 只允许 RefreshTokenErr 内部计一次失败；调用方（Vercel switch / client
// handleManagedAuthFailure）通过 AuthRefreshAlreadySucceeded 判断后不得重复计数，
// 否则首次失败就会直接达到阈值把账号退役。
func TestRejectedRefreshCountsFailureOnce(t *testing.T) {
	loginCalls := 0
	r, store := newAuthFailureResolver(t, func(_ context.Context, acc config.Account) (string, error) {
		loginCalls++
		if acc.Identifier() == "a@example.com" {
			return "", ErrLoginRejected
		}
		return "token-" + acc.Identifier(), nil
	})
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", Account: config.Account{Email: "a@example.com"}, resolver: r}

	if a.RefreshTokenOnce(context.Background()) {
		t.Fatal("refresh must fail when the upstream rejects the login")
	}
	if a.AuthRefreshAlreadySucceeded() {
		t.Error("a failed refresh must not report a successful refresh")
	}
	if loginCalls != 1 {
		t.Fatalf("expected exactly one login attempt, got %d", loginCalls)
	}
	if acc := accountState(t, store, "a@example.com"); acc.AuthFailed {
		t.Error("a single rejected refresh must not retire the account")
	}

	// 下一次相邻请求再次被拒：这时才达到阈值并退役。
	next := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", Account: config.Account{Email: "a@example.com"}, resolver: r}
	next.RefreshTokenOnce(context.Background())
	if acc := accountState(t, store, "a@example.com"); !acc.AuthFailed {
		t.Error("repeated rejected refreshes should retire the account")
	}
}

// TestFailedRefreshDoesNotCountAsSuccessfulRefresh 验证"刷新成功但新 Token 仍被
// 拒绝"与"刷新失败"能被区分：前者是账号级异常，后者不是。
func TestFailedRefreshDoesNotCountAsSuccessfulRefresh(t *testing.T) {
	r, _ := newAuthFailureResolver(t, func(context.Context, config.Account) (string, error) {
		return "", errors.New("dial tcp: connection reset")
	})
	a := &RequestAuth{UseConfigToken: true, AccountID: "a@example.com", resolver: r}
	if a.RefreshTokenOnce(context.Background()) {
		t.Fatal("a network-failed refresh must report failure")
	}
	if a.AuthRefreshAlreadySucceeded() {
		t.Error("a network-failed refresh must not look like a successful one")
	}
}
