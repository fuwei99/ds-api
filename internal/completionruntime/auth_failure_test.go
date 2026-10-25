package completionruntime

import (
	"context"
	"net/http"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/auth"
	"ds2api/internal/config"
	dsclient "ds2api/internal/deepseek/client"
	"ds2api/internal/promptcompat"
)

// newAuthFailureRuntime 构造一个双账号托管请求，用于验证 401 场景下
// "先刷新 Token、再换号"的恢复路径。
func newAuthFailureRuntime(t *testing.T) *auth.RequestAuth {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[
			{"email":"acc1@test.com","password":"pwd"},
			{"email":"acc2@test.com","password":"pwd"}
		]
	}`)
	store := config.LoadStore()
	resolver := auth.NewResolver(store, account.NewPool(store), func(_ context.Context, acc config.Account) (string, error) {
		return "token-" + acc.Identifier(), nil
	})
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer managed-key")
	a, err := resolver.Determine(req)
	if err != nil {
		t.Fatalf("determine failed: %v", err)
	}
	t.Cleanup(func() { resolver.Release(a) })
	return a
}

func authFailureStdReq() promptcompat.StandardRequest {
	return promptcompat.StandardRequest{
		Surface:         "test",
		ResponseModel:   "deepseek-v4.1-flash",
		PromptTokenText: "prompt",
		FinalPrompt:     "final prompt",
	}
}

// TestNonStreamUnauthorizedRefreshesTokenOnSameAccount 验证首次 401 时先刷新
// Token 并用同一账号重试，而不是直接把 "Account token is invalid" 抛给客户端。
func TestNonStreamUnauthorizedRefreshesTokenOnSameAccount(t *testing.T) {
	a := newAuthFailureRuntime(t)
	ds := &fakeDeepSeekCaller{
		sessionByAccount: true,
		responses: []*http.Response{
			sseHTTPResponse(http.StatusUnauthorized, `{"error":"invalid token"}`),
			sseHTTPResponse(http.StatusOK, `data: {"response_message_id":21,"p":"response/content","v":"recovered after refresh"}`),
		},
	}

	result, outErr := ExecuteNonStreamWithRetry(context.Background(), ds, a, authFailureStdReq(), Options{RetryEnabled: true})
	if outErr != nil {
		t.Fatalf("unexpected output error after token refresh: %#v", outErr)
	}
	if result.Turn.Text != "recovered after refresh" {
		t.Fatalf("text mismatch after refresh retry: %q", result.Turn.Text)
	}
	if a.AccountID != "acc1@test.com" {
		t.Fatalf("expected the refresh to keep the request on acc1, got %q", a.AccountID)
	}
	if a.DeepSeekToken != "token-acc1@test.com" {
		t.Fatalf("expected a freshly minted token, got %q", a.DeepSeekToken)
	}
	wantAccounts := []string{"acc1@test.com", "acc1@test.com"}
	if len(ds.completionAccounts) != len(wantAccounts) {
		t.Fatalf("completion account count mismatch: got %v want %v", ds.completionAccounts, wantAccounts)
	}
}

// TestNonStreamUnauthorizedSwitchesAccountAfterRefreshFails 验证刷新后仍被 401
// 拒绝时自动切到下一个可调度账号，保证请求不中断。
func TestNonStreamUnauthorizedSwitchesAccountAfterRefreshFails(t *testing.T) {
	a := newAuthFailureRuntime(t)
	ds := &fakeDeepSeekCaller{
		sessionByAccount: true,
		responses: []*http.Response{
			sseHTTPResponse(http.StatusUnauthorized, `{"error":"invalid token"}`),
			sseHTTPResponse(http.StatusUnauthorized, `{"error":"invalid token"}`),
			sseHTTPResponse(http.StatusOK, `data: {"response_message_id":31,"p":"response/content","v":"ok from second account"}`),
		},
	}

	result, outErr := ExecuteNonStreamWithRetry(context.Background(), ds, a, authFailureStdReq(), Options{RetryEnabled: true})
	if outErr != nil {
		t.Fatalf("unexpected output error after account switch: %#v", outErr)
	}
	if result.Turn.Text != "ok from second account" {
		t.Fatalf("text mismatch after account switch: %q", result.Turn.Text)
	}
	if a.AccountID != "acc2@test.com" {
		t.Fatalf("expected the request to land on acc2, got %q", a.AccountID)
	}
	wantAccounts := []string{"acc1@test.com", "acc1@test.com", "acc2@test.com"}
	if len(ds.completionAccounts) != len(wantAccounts) {
		t.Fatalf("completion account count mismatch: got %v want %v", ds.completionAccounts, wantAccounts)
	}
	for i, want := range wantAccounts {
		if ds.completionAccounts[i] != want {
			t.Fatalf("completion account %d = %q want %q (all=%v)", i, ds.completionAccounts[i], want, ds.completionAccounts)
		}
	}
}

// TestStartCompletionSwitchesAccountWhenSessionUnauthorized 验证开跑阶段
// CreateSession 因 Token 失效失败、且该账号重新登录也被上游拒绝时，会换到
// 下一个可调度账号继续，而不是把 401 透传给客户端。
func TestStartCompletionSwitchesAccountWhenSessionUnauthorized(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[
			{"email":"acc1@test.com","password":"pwd"},
			{"email":"acc2@test.com","password":"pwd"}
		]
	}`)
	store := config.LoadStore()
	// env 配置会丢弃内联 token（ClearAccountTokens），测试里显式给 acc1 一个
	// 陈旧 token，让它先通过取号、在 CreateSession 阶段才被上游拒绝。
	if err := store.UpdateAccountToken("acc1@test.com", "stale-token"); err != nil {
		t.Fatalf("seed stale token failed: %v", err)
	}
	resolver := auth.NewResolver(store, account.NewPool(store), func(_ context.Context, acc config.Account) (string, error) {
		if acc.Identifier() == "acc1@test.com" {
			return "", auth.ErrLoginRejected
		}
		return "token-" + acc.Identifier(), nil
	})
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer managed-key")
	a, err := resolver.Determine(req)
	if err != nil {
		t.Fatalf("determine failed: %v", err)
	}
	defer resolver.Release(a)
	if a.AccountID != "acc1@test.com" {
		t.Fatalf("precondition: request should start on acc1, got %q", a.AccountID)
	}

	ds := &fakeDeepSeekCaller{
		sessionByAccount: true,
		sessionErrors: []error{
			&dsclient.RequestFailure{Op: "create session", Kind: dsclient.FailureManagedUnauthorized, Message: "expired token"},
		},
		responses: []*http.Response{
			sseHTTPResponse(http.StatusOK, `data: {"response_message_id":41,"p":"response/content","v":"ok from second account"}`),
		},
	}

	result, outErr := ExecuteNonStreamWithRetry(context.Background(), ds, a, authFailureStdReq(), Options{RetryEnabled: true})
	if outErr != nil {
		t.Fatalf("unexpected output error after session-stage account switch: %#v", outErr)
	}
	if result.Turn.Text != "ok from second account" {
		t.Fatalf("text mismatch after session-stage switch: %q", result.Turn.Text)
	}
	if a.AccountID != "acc2@test.com" {
		t.Fatalf("expected the request to land on acc2, got %q", a.AccountID)
	}
}

// TestStartCompletionPromotesUnauthorizedInitialResponse 验证开跑阶段（流式与非
// 流式共用的 StartCompletion）拿到上游 401 时不会把原始 401 交给调用方，
// 而是升级为 account_unauthorized 并先刷新 Token 重试。
func TestStartCompletionPromotesUnauthorizedInitialResponse(t *testing.T) {
	a := newAuthFailureRuntime(t)
	ds := &fakeDeepSeekCaller{
		sessionByAccount: true,
		responses: []*http.Response{
			sseHTTPResponse(http.StatusUnauthorized, `{"error":"invalid token"}`),
			sseHTTPResponse(http.StatusOK, `data: {"response_message_id":51,"p":"response/content","v":"ok"}`),
		},
	}

	start, outErr := StartCompletion(context.Background(), ds, a, authFailureStdReq(), Options{RetryEnabled: true})
	if outErr != nil {
		t.Fatalf("unexpected output error after initial 401: %#v", outErr)
	}
	if start.Response == nil {
		t.Fatal("expected a completion response after refreshing the token")
	}
	defer func() { _ = start.Response.Body.Close() }()
	if a.AccountID != "acc1@test.com" {
		t.Fatalf("expected the refresh to keep the request on acc1, got %q", a.AccountID)
	}
	wantAccounts := []string{"acc1@test.com", "acc1@test.com"}
	if len(ds.completionAccounts) != len(wantAccounts) {
		t.Fatalf("completion account count mismatch: got %v want %v", ds.completionAccounts, wantAccounts)
	}
}

// TestStartCompletionSwitchesAccountWhenInitialUnauthorizedAndRefreshFails 验证
// 首次 completion 调用被 401 拒绝、且该账号重新登录也被拒绝时会换号重放。
func TestStartCompletionSwitchesAccountWhenInitialUnauthorizedAndRefreshFails(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[
			{"email":"acc1@test.com","password":"pwd"},
			{"email":"acc2@test.com","password":"pwd"}
		]
	}`)
	store := config.LoadStore()
	if err := store.UpdateAccountToken("acc1@test.com", "stale-token"); err != nil {
		t.Fatalf("seed stale token failed: %v", err)
	}
	resolver := auth.NewResolver(store, account.NewPool(store), func(_ context.Context, acc config.Account) (string, error) {
		if acc.Identifier() == "acc1@test.com" {
			return "", auth.ErrLoginRejected
		}
		return "token-" + acc.Identifier(), nil
	})
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer managed-key")
	a, err := resolver.Determine(req)
	if err != nil {
		t.Fatalf("determine failed: %v", err)
	}
	defer resolver.Release(a)
	if a.AccountID != "acc1@test.com" {
		t.Fatalf("precondition: request should start on acc1, got %q", a.AccountID)
	}

	ds := &fakeDeepSeekCaller{
		sessionByAccount: true,
		responses: []*http.Response{
			sseHTTPResponse(http.StatusUnauthorized, `{"error":"invalid token"}`),
			sseHTTPResponse(http.StatusOK, `data: {"response_message_id":61,"p":"response/content","v":"ok from second account"}`),
		},
	}

	start, outErr := StartCompletion(context.Background(), ds, a, authFailureStdReq(), Options{RetryEnabled: true})
	if outErr != nil {
		t.Fatalf("unexpected output error after switching accounts: %#v", outErr)
	}
	if start.Response == nil {
		t.Fatal("expected a completion response from the alternate account")
	}
	defer func() { _ = start.Response.Body.Close() }()
	if a.AccountID != "acc2@test.com" {
		t.Fatalf("expected the request to land on acc2, got %q", a.AccountID)
	}
	if got := accountStateOf(t, store, "acc1@test.com"); got.AuthFailed {
		t.Error("a single rejected re-login must only switch accounts, not retire the account")
	}
}

func accountStateOf(t *testing.T, store *config.Store, identifier string) config.Account {
	t.Helper()
	acc, ok := store.FindAccount(identifier)
	if !ok {
		t.Fatalf("account %q not found", identifier)
	}
	return acc
}

// TestStartCompletionUnauthorizedErrorCodeIsAccountUnauthorized 验证账号级鉴权
// 失败带独立的错误码，而不是笼统的 "error"，便于上游/客户端区分可重试与终态错误。
func TestStartCompletionUnauthorizedErrorCodeIsAccountUnauthorized(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["managed-key"],
		"accounts":[{"email":"only@test.com","password":"pwd"}]
	}`)
	store := config.LoadStore()
	resolver := auth.NewResolver(store, account.NewPool(store), func(_ context.Context, acc config.Account) (string, error) {
		return "token-" + acc.Identifier(), nil
	})
	req, _ := http.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer managed-key")
	a, err := resolver.Determine(req)
	if err != nil {
		t.Fatalf("determine failed: %v", err)
	}
	defer resolver.Release(a)

	ds := &fakeDeepSeekCaller{
		sessionByAccount: true,
		sessionErrors: []error{
			&dsclient.RequestFailure{Op: "create session", Kind: dsclient.FailureManagedUnauthorized, Message: "expired token"},
			&dsclient.RequestFailure{Op: "create session", Kind: dsclient.FailureManagedUnauthorized, Message: "expired token"},
			&dsclient.RequestFailure{Op: "create session", Kind: dsclient.FailureManagedUnauthorized, Message: "expired token"},
		},
	}

	_, outErr := StartCompletion(context.Background(), ds, a, authFailureStdReq(), Options{RetryEnabled: true})
	if outErr == nil {
		t.Fatal("expected the exhausted account pool to surface an error")
	}
	if outErr.Status != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", outErr.Status)
	}
	if outErr.Code != OutputCodeAccountUnauthorized {
		t.Fatalf("expected code %q, got %q", OutputCodeAccountUnauthorized, outErr.Code)
	}
}

// TestPowOutputErrorKeepsAuthFailuresNarrow 是 P2 的回归测试：GetPow 的网络类
// 失败不能被当成账号级鉴权失败（否则会触发无谓的强制重新登录与换号），只有上游
// 明确的托管鉴权失败才带 account_unauthorized；直传 token 保持原有 401 语义。
func TestPowOutputErrorKeepsAuthFailuresNarrow(t *testing.T) {
	a := &auth.RequestAuth{UseConfigToken: true, AccountID: "acc1@test.com"}

	networkErr := &dsclient.RequestFailure{Op: "get pow", Message: "dial tcp: connection reset"}
	got := powOutputError(a, networkErr)
	if got.Code != "upstream_unavailable" || got.Status != http.StatusBadGateway {
		t.Fatalf("network error must map to upstream_unavailable, got %#v", got)
	}
	if isManagedUnauthorized(got) {
		t.Error("a network error must not be classified as a managed auth failure")
	}

	authErr := &dsclient.RequestFailure{Op: "get pow", Kind: dsclient.FailureManagedUnauthorized, Message: "expired token"}
	got = powOutputError(a, authErr)
	if got.Code != OutputCodeAccountUnauthorized || got.Status != http.StatusUnauthorized {
		t.Fatalf("managed unauthorized must keep account_unauthorized, got %#v", got)
	}
	if !isManagedUnauthorized(got) {
		t.Error("managed unauthorized must be classified as an account auth failure")
	}

	direct := &auth.RequestAuth{UseConfigToken: false}
	got = powOutputError(direct, networkErr)
	if got.Status != http.StatusUnauthorized || got.Code != "error" {
		t.Fatalf("direct-token errors must keep the legacy 401 shape, got %#v", got)
	}
}
