package proxies

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

func newAdminProxyTestHandler(t *testing.T, raw string) *Handler {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", raw)
	store := config.LoadStore()
	return &Handler{
		Store: store,
		Pool:  account.NewPool(store),
	}
}

func TestAddProxyPersistsNormalizedProxy(t *testing.T) {
	h := newAdminProxyTestHandler(t, `{"accounts":[]}`)

	r := chi.NewRouter()
	r.Post("/admin/proxies", h.addProxy)

	req := httptest.NewRequest(http.MethodPost, "/admin/proxies", bytes.NewBufferString(`{
		"name":"  HK Exit  ",
		"type":" SOCKS5H ",
		"host":" 127.0.0.1 ",
		"port":1081,
		"username":" user ",
		"password":" pass "
	}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	proxies := h.Store.Snapshot().Proxies
	if len(proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(proxies))
	}
	if proxies[0].Name != "HK Exit" {
		t.Fatalf("unexpected proxy name: %#v", proxies[0])
	}
	if proxies[0].Type != "socks5h" {
		t.Fatalf("unexpected proxy type: %#v", proxies[0])
	}
	if proxies[0].Username != "user" || proxies[0].Password != "pass" {
		t.Fatalf("expected trimmed credentials, got %#v", proxies[0])
	}
	if proxies[0].ID == "" {
		t.Fatalf("expected generated proxy id, got %#v", proxies[0])
	}
}

func TestAddProxyDoesNotFailOnUnrelatedInvalidRuntimeConfig(t *testing.T) {
	router := newHTTPAdminHarness(t, `{
		"keys":["k1"],
		"runtime":{
			"account_max_inflight":8,
			"global_max_inflight":4
		}
	}`, &testingDSMock{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminReq(http.MethodPost, "/proxies", []byte(`{
		"name":"HK Exit",
		"type":"socks5h",
		"host":"127.0.0.1",
		"port":1080
	}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected add proxy success despite unrelated runtime issue, got %d body=%s", rec.Code, rec.Body.String())
	}

	readRec := httptest.NewRecorder()
	router.ServeHTTP(readRec, adminReq(http.MethodGet, "/config", nil))
	if readRec.Code != http.StatusOK {
		t.Fatalf("config read status=%d body=%s", readRec.Code, readRec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(readRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode config response: %v", err)
	}
	proxies, _ := payload["proxies"].([]any)
	if len(proxies) != 1 {
		t.Fatalf("expected proxy to be persisted, got %#v", payload["proxies"])
	}
}

func TestDeleteProxyClearsAssignedAccountProxyID(t *testing.T) {
	h := newAdminProxyTestHandler(t, `{
		"proxies":[{"id":"proxy-1","name":"Node 1","type":"socks5","host":"127.0.0.1","port":1080}],
		"accounts":[{"email":"u@example.com","password":"pwd","proxy_id":"proxy-1"}]
	}`)

	r := chi.NewRouter()
	r.Delete("/admin/proxies/{proxyID}", h.deleteProxy)

	req := httptest.NewRequest(http.MethodDelete, "/admin/proxies/proxy-1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	snap := h.Store.Snapshot()
	if len(snap.Proxies) != 0 {
		t.Fatalf("expected proxy removed, got %#v", snap.Proxies)
	}
	if len(snap.Accounts) != 1 {
		t.Fatalf("expected account kept, got %#v", snap.Accounts)
	}
	if snap.Accounts[0].ProxyID != "" {
		t.Fatalf("expected proxy assignment cleared, got %#v", snap.Accounts[0])
	}
}

func TestUpdateProxyResponseDoesNotExposeStoredPassword(t *testing.T) {
	h := newAdminProxyTestHandler(t, `{
		"proxies":[{"id":"proxy-1","name":"Node 1","type":"socks5h","host":"127.0.0.1","port":1080,"username":"u","password":"secret"}]
	}`)

	r := chi.NewRouter()
	r.Put("/admin/proxies/{proxyID}", h.updateProxy)

	req := httptest.NewRequest(http.MethodPut, "/admin/proxies/proxy-1", bytes.NewBufferString(`{
		"name":"Node 1",
		"type":"socks5h",
		"host":"127.0.0.2",
		"port":1081,
		"username":"u2"
	}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	proxy, _ := payload["proxy"].(map[string]any)
	if _, exists := proxy["password"]; exists {
		t.Fatalf("response should not expose password, got %#v", proxy)
	}
	if hasPassword, _ := proxy["has_password"].(bool); !hasPassword {
		t.Fatalf("expected has_password=true, got %#v", proxy)
	}
}

func TestUpdateAccountProxyAssignsProxyID(t *testing.T) {
	h := newAdminProxyTestHandler(t, `{
		"proxies":[{"id":"proxy-1","name":"Node 1","type":"socks5h","host":"127.0.0.1","port":1080}],
		"accounts":[{"email":"u@example.com","password":"pwd"}]
	}`)

	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}/proxy", h.updateAccountProxy)

	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/u@example.com/proxy", bytes.NewBufferString(`{"proxy_id":"proxy-1"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account")
	}
	if acc.ProxyID != "proxy-1" {
		t.Fatalf("expected proxy assigned, got %#v", acc)
	}
	if acc.NoProxy {
		t.Fatal("assigning a proxy must clear no_proxy")
	}
	// 手动指定代理由用户掌握：账号退出自动调度。
	if !acc.ManualBinding {
		t.Fatal("assigning a proxy must set manual_binding")
	}
}

func TestUpdateAccountProxyClearsAssignmentSetsNoProxy(t *testing.T) {
	h := newAdminProxyTestHandler(t, `{
		"proxies":[{"id":"proxy-1","name":"Node 1","type":"socks5h","host":"127.0.0.1","port":1080}],
		"accounts":[{"email":"u@example.com","password":"pwd","proxy_id":"proxy-1"}]
	}`)

	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}/proxy", h.updateAccountProxy)

	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/u@example.com/proxy", bytes.NewBufferString(`{"proxy_id":""}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account")
	}
	if acc.ProxyID != "" {
		t.Fatalf("expected proxy assignment cleared, got %#v", acc)
	}
	if !acc.NoProxy {
		t.Fatal("explicitly clearing proxy assignment must set no_proxy")
	}
	// 手动绑定随解绑一并撤销，账号回到可被自动调度接管的状态。
	if acc.ManualBinding {
		t.Fatal("explicitly clearing proxy assignment must clear manual_binding")
	}
}

func TestTestProxyUsesStoredProxy(t *testing.T) {
	h := newAdminProxyTestHandler(t, `{
		"proxies":[{"id":"proxy-1","name":"Node 1","type":"socks5h","host":"127.0.0.1","port":1080}]
	}`)

	original := proxyConnectivityTester
	defer func() { proxyConnectivityTester = original }()

	var got config.Proxy
	proxyConnectivityTester = func(_ context.Context, proxy config.Proxy) map[string]any {
		got = proxy
		return map[string]any{
			"success":       true,
			"proxy_id":      proxy.ID,
			"proxy_type":    proxy.Type,
			"response_time": 12,
		}
	}

	r := chi.NewRouter()
	r.Post("/admin/proxies/test", h.testProxy)

	req := httptest.NewRequest(http.MethodPost, "/admin/proxies/test", bytes.NewBufferString(`{"proxy_id":"proxy-1"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if got.ID != "proxy-1" || got.Type != "socks5h" {
		t.Fatalf("expected stored proxy passed to tester, got %#v", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ok, _ := payload["success"].(bool); !ok {
		t.Fatalf("expected success payload, got %#v", payload)
	}
}

// 手动绑定之后回到自动调度的唯一入口：mode=auto 清空绑定并同时清除
// no_proxy 与 manual_binding，账号重新参与桥的故障转移/补位。
func TestUpdateAccountProxyAutoModeReturnsAccountToAutoScheduling(t *testing.T) {
	h := newAdminProxyTestHandler(t, `{
		"proxies":[{"id":"mihomo-1","name":"香港 01","type":"socks5","host":"127.0.0.1","port":10801}],
		"accounts":[{"email":"u@example.com","password":"pwd","proxy_id":"mihomo-1","manual_binding":true}]
	}`)

	var reconciles int
	h.OnAccountsChanged = func() { reconciles++ }

	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}/proxy", h.updateAccountProxy)

	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/u@example.com/proxy", bytes.NewBufferString(`{"mode":"auto"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account")
	}
	if acc.ProxyID != "" {
		t.Fatalf("binding must be cleared, got %q", acc.ProxyID)
	}
	if acc.ManualBinding {
		t.Fatal("mode=auto must clear manual_binding")
	}
	if acc.NoProxy {
		t.Fatal("mode=auto must clear no_proxy")
	}
	// 交回自动调度后应立即触发一次绑定调和（按已有测速结果补位）。
	if reconciles != 1 {
		t.Fatalf("expected one reconcile request, got %d", reconciles)
	}
}

// 桥未装配（OnAccountsChanged 为 nil，见 deps.go）时 mode=auto 不能 panic：
// 回调是可选依赖，账号 handler 侧的 notifyAccountsChanged 用的是同一套 nil 规则。
func TestUpdateAccountProxyAutoModeWithoutBridgeCallback(t *testing.T) {
	h := newAdminProxyTestHandler(t, `{
		"proxies":[{"id":"mihomo-1","name":"香港 01","type":"socks5","host":"127.0.0.1","port":10801}],
		"accounts":[{"email":"u@example.com","password":"pwd","proxy_id":"mihomo-1","manual_binding":true}]
	}`)
	if h.OnAccountsChanged != nil {
		t.Fatal("precondition: no bridge callback wired")
	}

	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}/proxy", h.updateAccountProxy)

	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/u@example.com/proxy", bytes.NewBufferString(`{"mode":"auto"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc, ok := h.Store.FindAccount("u@example.com")
	if !ok {
		t.Fatal("expected account")
	}
	if acc.ProxyID != "" || acc.ManualBinding || acc.NoProxy {
		t.Fatalf("mode=auto must return the account to auto-scheduling: %+v", acc)
	}
}
