package accounts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"ds2api/internal/config"
)

func TestAddAccountPersistsPoolType(t *testing.T) {
	h := newAdminTestHandler(t, `{"accounts":[]}`)

	r := chi.NewRouter()
	r.Post("/admin/accounts", h.addAccount)
	body := []byte(`{"email":"u@example.com","password":"pwd","pool_type":"no_tools"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account to be created")
	}
	if got := config.NormalizePoolType(acc.PoolType); got != config.PoolTypeNoTools {
		t.Fatalf("expected pool_type=%q, got %q", config.PoolTypeNoTools, got)
	}
}

func TestAddAccountDefaultsPoolTypeWhenOmitted(t *testing.T) {
	h := newAdminTestHandler(t, `{"accounts":[]}`)

	r := chi.NewRouter()
	r.Post("/admin/accounts", h.addAccount)
	body := []byte(`{"email":"u@example.com","password":"pwd"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account to be created")
	}
	if got := config.NormalizePoolType(acc.PoolType); got != config.PoolTypeDefault {
		t.Fatalf("expected pool_type=%q, got %q", config.PoolTypeDefault, got)
	}
}

func TestAddAccountNormalizesUnknownPoolTypeToDefault(t *testing.T) {
	h := newAdminTestHandler(t, `{"accounts":[]}`)

	r := chi.NewRouter()
	r.Post("/admin/accounts", h.addAccount)
	body := []byte(`{"email":"u@example.com","password":"pwd","pool_type":"bogus"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account to be created")
	}
	if got := config.NormalizePoolType(acc.PoolType); got != config.PoolTypeDefault {
		t.Fatalf("expected pool_type normalized to %q, got %q", config.PoolTypeDefault, got)
	}
}

func TestAddAccountAcceptsToolsOnlyPoolType(t *testing.T) {
	h := newAdminTestHandler(t, `{"accounts":[]}`)

	r := chi.NewRouter()
	r.Post("/admin/accounts", h.addAccount)
	body := []byte(`{"email":"u@example.com","password":"pwd","pool_type":"TOOLS_ONLY"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account to be created")
	}
	if got := config.NormalizePoolType(acc.PoolType); got != config.PoolTypeToolsOnly {
		t.Fatalf("expected pool_type=%q, got %q", config.PoolTypeToolsOnly, got)
	}
}

func TestAddAccountPoolTypeVisibleInListResponse(t *testing.T) {
	h := newAdminTestHandler(t, `{"accounts":[]}`)

	r := chi.NewRouter()
	RegisterRoutes(r, h)
	body := []byte(`{"email":"u@example.com","password":"pwd","pool_type":"tools_only"}`)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, adminReq(http.MethodPost, "/accounts", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, adminReq(http.MethodGet, "/accounts?page=1&page_size=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if got, _ := first["pool_type"].(string); got != config.PoolTypeToolsOnly {
		t.Fatalf("expected list pool_type=%q, got %q", config.PoolTypeToolsOnly, got)
	}
}

func TestListAccountsPageSizeCapIs5000(t *testing.T) {
	accounts := make([]string, 0, 150)
	for i := range 150 {
		accounts = append(accounts, fmt.Sprintf(`{"email":"u%d@example.com","password":"pwd"}`, i))
	}
	raw := fmt.Sprintf(`{"accounts":[%s]}`, strings.Join(accounts, ","))
	router := newHTTPAdminHarness(t, raw, &testingDSMock{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodGet, "/accounts?page=1&page_size=200", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 150 {
		t.Fatalf("expected all 150 accounts with page_size=200, got %d", len(items))
	}
	if ps, _ := payload["page_size"].(float64); ps != 200 {
		t.Fatalf("expected page_size=200 in response, got %v", payload["page_size"])
	}
}

func TestListAccountsPageSizeAbove5000ClampedTo5000(t *testing.T) {
	router := newHTTPAdminHarness(t, `{"accounts":[{"email":"u@example.com","password":"pwd"}]}`, &testingDSMock{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodGet, "/accounts?page=1&page_size=9999", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ps, _ := payload["page_size"].(float64); ps != 5000 {
		t.Fatalf("expected page_size clamped to 5000, got %v", payload["page_size"])
	}
}

func TestUpdateAccountMetadataPreservesCredentials(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"u@example.com","name":"old name","remark":"old remark","password":"secret"}]
	}`)

	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}", h.updateAccount)

	body := []byte(`{"name":"new name","remark":"new remark"}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/u@example.com", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	snap := h.Store.Snapshot()
	if len(snap.Accounts) != 1 {
		t.Fatalf("unexpected accounts after update: %#v", snap.Accounts)
	}
	acc := snap.Accounts[0]
	if acc.Email != "u@example.com" {
		t.Fatalf("identifier changed unexpectedly: %#v", acc)
	}
	if acc.Name != "new name" || acc.Remark != "new remark" {
		t.Fatalf("metadata update did not persist: %#v", acc)
	}
	if acc.Password != "secret" {
		t.Fatalf("password should be preserved, got %#v", acc)
	}
}

func TestUpdateAccountPersistsPriority(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"u@example.com","password":"pwd"}]
	}`)

	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}", h.updateAccount)

	body := []byte(`{"priority":-4}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/u@example.com", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account to exist")
	}
	if acc.Priority != -4 {
		t.Fatalf("expected priority=-4, got %d", acc.Priority)
	}
}

func TestUpdateAccountWithoutPriorityKeepsExisting(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"u@example.com","password":"pwd","priority":9}]
	}`)

	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}", h.updateAccount)

	body := []byte(`{"name":"renamed"}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/u@example.com", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account to exist")
	}
	if acc.Priority != 9 {
		t.Fatalf("priority should be preserved when omitted, got %d", acc.Priority)
	}
	if acc.Name != "renamed" {
		t.Fatalf("expected name to update, got %q", acc.Name)
	}
}

func TestListAccountsExposesPriority(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"u@example.com","password":"pwd","priority":6}]
	}`)

	req := httptest.NewRequest(http.MethodGet, "/admin/accounts?page=1&page_size=10", nil)
	rec := httptest.NewRecorder()
	h.listAccounts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if got, _ := first["priority"].(float64); got != 6 {
		t.Fatalf("expected list priority=6, got %v", first["priority"])
	}
}

func TestUpdateAccountPriorityReconcilesElasticPool(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"elastic_pool":{"enabled":true,"global_count":1},
		"accounts":[
			{"email":"a@example.com","password":"pwd"},
			{"email":"b@example.com","password":"pwd"}
		]
	}`)

	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}", h.updateAccount)

	// 把原本靠后的 b 提升优先级，弹性号池应改为启用 b、休眠 a。
	body := []byte(`{"priority":10}`)
	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/b@example.com", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	a, ok := h.Store.FindAccount("a@example.com")
	if !ok {
		t.Fatal("expected account a to exist")
	}
	b, ok := h.Store.FindAccount("b@example.com")
	if !ok {
		t.Fatal("expected account b to exist")
	}
	if a.Disabled != true {
		t.Error("account a should be disabled after b takes the only slot")
	}
	if b.Disabled != false {
		t.Error("account b should be enabled after being promoted")
	}
}

func TestToggleAccountEnabledNotifiesOnAccountsChanged(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"u@example.com","password":"pwd","disabled":true}]
	}`)
	called := 0
	h.OnAccountsChanged = func() { called++ }

	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}/enabled", h.toggleAccountEnabled)

	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/u@example.com/enabled", bytes.NewBufferString(`{"enabled":true}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if called != 1 {
		t.Fatalf("expected OnAccountsChanged invoked once, got %d", called)
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok || !acc.IsEnabled() {
		t.Fatalf("account should be enabled after toggle, got %#v", acc)
	}
}

func TestListAccountsMasksTokenPreview(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"u@example.com","password":"pwd"}]
	}`)
	if err := h.Store.UpdateAccountToken("u@example.com", "abcdefgh"); err != nil {
		t.Fatalf("seed runtime token: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/accounts?page=1&page_size=10", nil)
	rec := httptest.NewRecorder()
	h.listAccounts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if got, _ := first["token_preview"].(string); got != "ab****gh" {
		t.Fatalf("expected masked token preview, got %q", got)
	}
}

func TestBatchToggleAccountEnabledFlipsAllAccounts(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@example.com","password":"pwd"},
			{"email":"b@example.com","password":"pwd","disabled":true},
			{"mobile":"13800000000","password":"pwd"}
		]
	}`, &testingDSMock{})

	// disable all
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPost, "/accounts/enabled/batch", []byte(`{"enabled":false}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if total, _ := payload["total"].(float64); total != 3 {
		t.Fatalf("expected total=3, got %v", payload["total"])
	}

	router2 := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@example.com","password":"pwd","disabled":true},
			{"email":"b@example.com","password":"pwd"}
		]
	}`, &testingDSMock{})

	// enable all
	rec2 := httptest.NewRecorder()
	router2.ServeHTTP(rec2, adminReq(http.MethodPost, "/accounts/enabled/batch", []byte(`{"enabled":true}`)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestBatchToggleAccountEnabledRejectsMissingField(t *testing.T) {
	router := newHTTPAdminHarness(t, `{"accounts":[{"email":"a@example.com","password":"pwd"}]}`, &testingDSMock{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPost, "/accounts/enabled/batch", []byte(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing enabled, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestBatchDeleteBannedAccountsRemovesOnlyBanned(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[
			{"email":"a@example.com","password":"pwd"},
			{"email":"b@example.com","password":"pwd","banned":true},
			{"email":"c@example.com","password":"pwd","disabled":true}
		]
	}`)

	r := chi.NewRouter()
	r.Post("/admin/accounts/banned/delete", h.batchDeleteBannedAccounts)
	req := httptest.NewRequest(http.MethodPost, "/admin/accounts/banned/delete", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if total, _ := payload["total"].(float64); total != 1 {
		t.Fatalf("expected total=1, got %v", payload["total"])
	}
	remaining := h.Store.Accounts()
	if len(remaining) != 2 {
		t.Fatalf("expected 2 accounts remaining, got %d", len(remaining))
	}
	for _, acc := range remaining {
		if acc.IsBanned() {
			t.Fatalf("banned account %s should have been removed", acc.Identifier())
		}
	}
}
