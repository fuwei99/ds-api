package auth

import (
	"context"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

// 号池的引用计数是「这个账号当前被多少个请求持有」。SwitchAccount 在换号前先归还
// 当前账号的名额——这是号池打满时还能换号的原因——因此换号失败后请求仍带着一个
// 已归还的 AccountID 走到收尾的 Release。若收尾再归还一次，该账号的持有数会掉到
// 真实持有数之下：两个请求共用一个账号、其中一个换号失败并收尾后，账号会显示零
// 持有者，而另一个请求还在用它，号池于是会在这个账号上放行超过
// account_max_inflight 的请求。
func TestRequestEndReleaseDoesNotDoubleReleaseASwitchedAwayAccount(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[{"email":"a@example.com","password":"pwd","token":"ta"}]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	r := NewResolver(store, pool, func(_ context.Context, acc config.Account) (string, error) {
		return "token-" + acc.Identifier(), nil
	})

	first := determineManaged(t, r)
	second := determineManaged(t, r)
	if first.AccountID != second.AccountID {
		t.Fatalf("precondition: both requests must hold the same account, got %q and %q", first.AccountID, second.AccountID)
	}
	if got := pool.Status()["in_use"]; got != 2 {
		t.Fatalf("precondition: in_use=%v, want the two holders", got)
	}

	// 换号失败：唯一的账号刚被归还，池里没有下一个可调度账号。
	if first.SwitchAccount(context.Background()) {
		t.Fatal("precondition: switching must fail with a single-account pool")
	}

	// 第一个请求收尾：它的名额已经在换号时还过了，不能再减一次。
	r.Release(first)
	if got := pool.Status()["in_use"]; got != 1 {
		t.Fatalf("in_use=%v after the first request released, want 1 (the second request still holds the account)", got)
	}

	// 第二个请求收尾：名额正好减到零。
	r.Release(second)
	if got := pool.Status()["in_use"]; got != 0 {
		t.Fatalf("in_use=%v after both requests released, want 0", got)
	}
}

// 换号成功时，归还的必须是「上一个」账号，而收尾归还的是当前账号：两次归还各减
// 一个不同的账号，且每个账号恰好减一次。
func TestSwitchAccountReleasesEachAccountExactlyOnce(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[
			{"email":"a@example.com","password":"pwd","token":"ta"},
			{"email":"b@example.com","password":"pwd","token":"tb"}
		]
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	r := NewResolver(store, pool, func(_ context.Context, acc config.Account) (string, error) {
		return "token-" + acc.Identifier(), nil
	})

	a := determineManaged(t, r)
	previous := a.AccountID
	if !a.SwitchAccount(context.Background()) {
		t.Fatal("expected a failover to the second account")
	}
	if a.AccountID == previous {
		t.Fatalf("account stayed at %q, want a failover", previous)
	}
	if got := pool.Status()["in_use"]; got != 1 {
		t.Fatalf("in_use=%v after a successful switch, want 1 (the new account)", got)
	}
	r.Release(a)
	if got := pool.Status()["in_use"]; got != 0 {
		t.Fatalf("in_use=%v after the request ended, want 0", got)
	}
}
