package accounts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func listAccountItems(t *testing.T, router http.Handler) map[string]map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodGet, "/accounts?page=1&page_size=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected list status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	items := payload["items"].([]any)
	byEmail := map[string]map[string]any{}
	for _, item := range items {
		m := item.(map[string]any)
		byEmail[m["email"].(string)] = m
	}
	return byEmail
}

func TestListAccountsExposesAuthFailed(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@x.com","password":"p","auth_failed":true,"disabled":true,"disabled_reason":"账号鉴权持续失败"},
			{"email":"b@x.com","password":"p"}
		]
	}`, &testingDSMock{})

	byEmail := listAccountItems(t, router)
	if got, ok := byEmail["a@x.com"]["auth_failed"].(bool); !ok || !got {
		t.Errorf("expected auth_failed=true for a@x.com, got %#v", byEmail["a@x.com"]["auth_failed"])
	}
	if got, ok := byEmail["b@x.com"]["auth_failed"].(bool); !ok || got {
		t.Errorf("expected auth_failed=false for b@x.com, got %#v", byEmail["b@x.com"]["auth_failed"])
	}
	if byEmail["a@x.com"]["disabled_reason"].(string) == "" {
		t.Error("expected disabled_reason to be surfaced for the anomalous account")
	}
}

func TestToggleAccountEnabledClearsAuthFailed(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@x.com","password":"p","auth_failed":true,"disabled":true,"disabled_reason":"账号鉴权持续失败"}
		]
	}`, &testingDSMock{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/a@x.com/enabled", []byte(`{"enabled":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	acc := listAccountItems(t, router)["a@x.com"]
	if got := acc["auth_failed"].(bool); got {
		t.Error("manually enabling the account should clear auth_failed")
	}
	if got := acc["enabled"].(bool); !got {
		t.Error("account should be enabled after the manual re-enable")
	}
	if got := acc["disabled_reason"].(string); got != "" {
		t.Errorf("disabled_reason should be cleared, got %q", got)
	}
}

func TestBatchToggleAccountEnabledClearsAuthFailed(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@x.com","password":"p","auth_failed":true,"disabled":true},
			{"email":"b@x.com","password":"p","auth_failed":true,"disabled":true}
		]
	}`, &testingDSMock{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPost, "/accounts/enabled/batch", []byte(`{"enabled":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	for email, acc := range listAccountItems(t, router) {
		if acc["auth_failed"].(bool) {
			t.Errorf("account %s should no longer be flagged auth_failed", email)
		}
		if !acc["enabled"].(bool) {
			t.Errorf("account %s should be enabled", email)
		}
	}
}

func TestToggleAccountDisableKeepsAuthFailedFlag(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@x.com","password":"p"}
		]
	}`, &testingDSMock{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/a@x.com/enabled", []byte(`{"enabled":false}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if got := listAccountItems(t, router)["a@x.com"]["enabled"].(bool); got {
		t.Error("account should be disabled")
	}
}
