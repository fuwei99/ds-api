package client

import (
	"context"
	"net/http"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/auth"
	"ds2api/internal/config"
)

func TestShouldAttemptRefreshOnTokenInvalidSignal(t *testing.T) {
	if !shouldAttemptRefresh(401, 0, 0, "unauthorized", "") {
		t.Fatal("expected refresh when response indicates invalid token")
	}
}

func TestShouldAttemptRefreshOnAuthIndicativeBizCodeFailure(t *testing.T) {
	if !shouldAttemptRefresh(200, 0, 400123, "", "login expired, token invalid") {
		t.Fatal("expected refresh on auth-indicative biz_code failure")
	}
}

func TestShouldAttemptRefreshFalseOnNonAuthBizCodeFailure(t *testing.T) {
	if shouldAttemptRefresh(200, 0, 400123, "", "session create failed: quota reached") {
		t.Fatal("did not expect refresh on non-auth biz_code failure")
	}
}

func TestShouldAttemptRefreshFalseOnGenericServerError(t *testing.T) {
	if shouldAttemptRefresh(500, 500, 0, "internal error", "") {
		t.Fatal("did not expect refresh on generic server error")
	}
}

// TestRecoverManagedAuthSwitchesAccountOnNonAuthFailure 锁定「非鉴权失败也要换号」
// 这条行为：HTTP 429/5xx 或非鉴权业务码并没有让 Token 失效，因此不该刷新，但
// 承载它的账号本次请求已经不可用，必须换到下一个可调度账号——上传等没有外层
// 兜底的调用点全靠这一步，否则一次上游 500 就会直接硬失败。
func TestRecoverManagedAuthSwitchesAccountOnNonAuthFailure(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		code    int
		bizCode int
		msg     string
		bizMsg  string
	}{
		{name: "http_5xx", status: http.StatusInternalServerError, code: 500, msg: "internal error"},
		{name: "http_429", status: http.StatusTooManyRequests, code: 429, msg: "rate limited"},
		{name: "non_auth_biz_code", status: http.StatusOK, code: 0, bizCode: 400123, bizMsg: "quota reached"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DS2API_CONFIG_JSON", `{
				"keys":["managed-key"],
				"accounts":[
					{"email":"acc1@example.com"},
					{"email":"acc2@example.com"}
				]
			}`)
			store := config.LoadStore()
			pool := account.NewPool(store)
			logins := map[string]int{}
			resolver := auth.NewResolver(store, pool, func(_ context.Context, acc config.Account) (string, error) {
				logins[acc.Identifier()]++
				return "token-" + acc.Identifier(), nil
			})
			req, _ := http.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("Authorization", "Bearer managed-key")
			reqAuth, err := resolver.Determine(req)
			if err != nil {
				t.Fatalf("determine failed: %v", err)
			}
			defer resolver.Release(reqAuth)
			first := reqAuth.AccountID

			client := &Client{Store: store, Auth: resolver}
			attempts := 0
			if !client.recoverManagedAuth(context.Background(), reqAuth, &attempts, tc.status, tc.code, tc.bizCode, tc.msg, tc.bizMsg) {
				t.Fatal("a non-auth upstream failure must fail over to another pooled account")
			}
			if attempts != 1 {
				t.Fatalf("attempts = %d, want 1 (a switch costs one attempt)", attempts)
			}
			if reqAuth.AccountID == first {
				t.Fatalf("account stayed at %q, want a failover to another account", first)
			}
			if logins[first] != 1 {
				t.Fatalf("logins for %q = %d, want 1 (a non-auth failure must not refresh the token)", first, logins[first])
			}
			if reqAuth.DeepSeekToken == "" {
				t.Fatal("the switched account must carry a usable token")
			}
		})
	}
}
