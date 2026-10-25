package accounts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

func TestAddAccountReconcilesElasticPool(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@x.com","password":"p"},
			{"email":"b@x.com","password":"p"}
		],
		"elastic_pool":{"enabled":true,"global_count":2}
	}`, &testingDSMock{})

	body := []byte(`{"email":"c@x.com","password":"p"}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPost, "/accounts", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, adminReq(http.MethodGet, "/accounts?page=1&page_size=10", nil))
	var listPayload map[string]any
	_ = json.Unmarshal(listRec.Body.Bytes(), &listPayload)
	items := listPayload["items"].([]any)
	enabled := map[string]bool{}
	for _, item := range items {
		m := item.(map[string]any)
		enabled[m["email"].(string)] = m["enabled"].(bool)
	}
	if !enabled["a@x.com"] || !enabled["b@x.com"] {
		t.Errorf("expected first two accounts to stay enabled, got %+v", enabled)
	}
	if enabled["c@x.com"] {
		t.Errorf("expected newly added account to be disabled by elastic pool, got %+v", enabled)
	}
}

func TestUpdateElasticPoolEnableGlobal(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@x.com","password":"p"},
			{"email":"b@x.com","password":"p"},
			{"email":"c@x.com","password":"p"},
			{"email":"d@x.com","password":"p"}
		]
	}`, &testingDSMock{})

	body := []byte(`{"enabled":true,"per_pool":false,"global_count":2}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/elastic-pool", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload["success"].(bool) {
		t.Fatal("expected success=true")
	}
	ep := payload["elastic_pool"].(map[string]any)
	if !ep["enabled"].(bool) {
		t.Error("expected enabled=true in response")
	}

	h := router.(*chi.Mux)
	_ = h
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, adminReq(http.MethodGet, "/accounts?page=1&page_size=10", nil))
	var listPayload map[string]any
	_ = json.Unmarshal(listRec.Body.Bytes(), &listPayload)
	items := listPayload["items"].([]any)
	enabledCount := 0
	for _, item := range items {
		m := item.(map[string]any)
		if m["enabled"].(bool) {
			enabledCount++
		}
	}
	if enabledCount != 2 {
		t.Errorf("expected 2 enabled accounts, got %d", enabledCount)
	}
}

func TestUpdateElasticPoolDisableRestoresAll(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@x.com","password":"p","disabled":true},
			{"email":"b@x.com","password":"p","disabled":true}
		]
	}`, &testingDSMock{})

	body := []byte(`{"enabled":false,"per_pool":false,"global_count":0}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/elastic-pool", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, adminReq(http.MethodGet, "/accounts?page=1&page_size=10", nil))
	var listPayload map[string]any
	_ = json.Unmarshal(listRec.Body.Bytes(), &listPayload)
	items := listPayload["items"].([]any)
	for i, item := range items {
		m := item.(map[string]any)
		if !m["enabled"].(bool) {
			t.Errorf("account %d should be enabled after disabling elastic pool", i)
		}
	}
}

func TestUpdateElasticPoolMissingEnabled(t *testing.T) {
	router := newHTTPAdminHarness(t, `{"accounts":[]}`, &testingDSMock{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/elastic-pool", []byte(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateElasticPoolGlobalCountZeroDisablesAll(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"a@x.com","password":"p"},
			{"email":"b@x.com","password":"p"},
			{"email":"c@x.com","password":"p"}
		]
	}`, &testingDSMock{})

	body := []byte(`{"enabled":true,"per_pool":false,"global_count":0}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/elastic-pool", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	ep := payload["elastic_pool"].(map[string]any)
	if int(ep["global_count"].(float64)) != 0 {
		t.Errorf("expected global_count=0, got %v", ep["global_count"])
	}

	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, adminReq(http.MethodGet, "/accounts?page=1&page_size=10", nil))
	var listPayload map[string]any
	_ = json.Unmarshal(listRec.Body.Bytes(), &listPayload)
	items := listPayload["items"].([]any)
	for i, item := range items {
		m := item.(map[string]any)
		if m["enabled"].(bool) {
			t.Errorf("account %d should be disabled when global_count=0", i)
		}
	}
}

func TestUpdateElasticPoolInvalidJSON(t *testing.T) {
	router := newHTTPAdminHarness(t, `{"accounts":[]}`, &testingDSMock{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/elastic-pool", []byte(`not json`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestUpdateElasticPoolPerPoolMode(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"accounts":[
			{"email":"d1@x.com","password":"p","pool_type":"default"},
			{"email":"d2@x.com","password":"p","pool_type":"default"},
			{"email":"d3@x.com","password":"p","pool_type":"default"},
			{"email":"n1@x.com","password":"p","pool_type":"no_tools"},
			{"email":"n2@x.com","password":"p","pool_type":"no_tools"}
		]
	}`, &testingDSMock{})

	body := []byte(`{"enabled":true,"per_pool":true,"default_count":2,"no_tools_count":1,"tools_only_count":1}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/elastic-pool", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	ep := payload["elastic_pool"].(map[string]any)
	if !ep["per_pool"].(bool) {
		t.Error("expected per_pool=true in response")
	}

	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, adminReq(http.MethodGet, "/accounts?page=1&page_size=10", nil))
	var listPayload map[string]any
	_ = json.Unmarshal(listRec.Body.Bytes(), &listPayload)
	items := listPayload["items"].([]any)
	enabledByType := map[string]int{}
	for _, item := range items {
		m := item.(map[string]any)
		if m["enabled"].(bool) {
			pt := m["pool_type"].(string)
			enabledByType[pt]++
		}
	}
	if enabledByType["default"] != 2 {
		t.Errorf("expected 2 enabled default accounts, got %d", enabledByType["default"])
	}
	if enabledByType["no_tools"] != 1 {
		t.Errorf("expected 1 enabled no_tools account, got %d", enabledByType["no_tools"])
	}
}

// 复现并守住上报的缺陷：弹性号池 + 代理桥自动调度开启时，改小账号数量会把
// 正在生成回复的账号一起禁用（随后代理桥解除其绑定并重启 mihomo，回复中断）。
// 有在途请求的账号必须保持启用。
func TestUpdateElasticPoolKeepsGeneratingAccountEnabled(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"accounts":[
			{"email":"a@x.com","token":"ta"},
			{"email":"b@x.com","token":"tb"},
			{"email":"c@x.com","token":"tc"}
		],
		"elastic_pool":{"enabled":true,"per_pool":false,"global_count":3}
	}`)
	store := config.LoadStore()
	pool := account.NewPool(store)
	h := &Handler{Store: store, Pool: pool}
	router := chi.NewRouter()
	RegisterRoutes(router, h)

	// c 正在生成回复（持有并发名额），此时用户把每批启用数改成 1。
	if _, ok := pool.Acquire("c@x.com", nil, nil); !ok {
		t.Fatal("failed to acquire the account that is generating")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/elastic-pool", []byte(`{"enabled":true,"per_pool":false,"global_count":1}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	enabled := map[string]bool{}
	for _, acc := range store.Snapshot().Accounts {
		enabled[acc.Email] = acc.IsEnabled()
	}
	if !enabled["c@x.com"] {
		t.Error("account generating a reply must stay enabled after the count change")
	}
	if !enabled["a@x.com"] {
		t.Error("first account should keep the remaining slot")
	}
	if enabled["b@x.com"] {
		t.Error("idle account beyond the new count should be disabled")
	}

	// 生成结束、名额归还后，下一次重算按新数量收敛：c 回到休眠。
	pool.Release("c@x.com")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPut, "/accounts/elastic-pool", []byte(`{"enabled":true,"per_pool":false,"global_count":1}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if acc, _ := store.FindAccount("c@x.com"); acc.IsEnabled() {
		t.Error("account should sleep once it stops generating")
	}
}
