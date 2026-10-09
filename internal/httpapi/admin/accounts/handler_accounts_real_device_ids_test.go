package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dsclient "ds2api/internal/deepseek/client"
)

// fakeRealDeviceRefresher 同时实现 DeepSeekCaller（通过内嵌 testingDSMock）
// 与可选的 realDeviceIDRefresher。
type fakeRealDeviceRefresher struct {
	testingDSMock
	refreshed map[string]string
	failFor   map[string]string
	resetSeen map[string]bool
}

func (f *fakeRealDeviceRefresher) RefreshRealDeviceID(_ context.Context, identifier string, resetProfile bool) (string, error) {
	if f.resetSeen == nil {
		f.resetSeen = map[string]bool{}
	}
	f.resetSeen[identifier] = resetProfile
	if detail, ok := f.failFor[identifier]; ok {
		return "", errors.New(detail)
	}
	if id, ok := f.refreshed[identifier]; ok {
		return id, nil
	}
	return "", errors.New("未预期到的账号：" + identifier)
}

func (f *fakeRealDeviceRefresher) RefreshRealDeviceIDs(ctx context.Context, identifiers []string, resetProfile bool) []dsclient.RealDeviceIDRefreshResult {
	results := make([]dsclient.RealDeviceIDRefreshResult, 0, len(identifiers))
	for _, identifier := range identifiers {
		deviceID, err := f.RefreshRealDeviceID(ctx, identifier, resetProfile)
		item := dsclient.RealDeviceIDRefreshResult{Identifier: identifier, DeviceID: deviceID}
		if err != nil {
			item.Error = err.Error()
		}
		results = append(results, item)
	}
	return results
}

const realModeConfig = `{
	"accounts":[
		{"email":"real@example.com","password":"p","device_id":"Breal","device_id_type":"auto"},
		{"email":"manual@example.com","password":"p","device_id":"Bmanual","device_id_type":"manual"},
		{"email":"plain@example.com","password":"p"}
	],
	"runtime":{"device_id_mode":"auto"}
}`

func realDeviceRequest(t *testing.T, h *Handler, method, target, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func decodeJSONBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v body=%s", err, rec.Body.String())
	}
	return payload
}

// auto 模式下的清单只含标注为 auto 的账号（旧名 real 会迁移为 auto）：manual 与未标注的账号不在其中。
func TestListRealDeviceIDsOnlyRealAccounts(t *testing.T) {
	h := newAdminTestHandler(t, realModeConfig)
	rec := realDeviceRequest(t, h, http.MethodGet, "/admin/device-ids/real", "", h.listRealDeviceIDs)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONBody(t, rec)
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected only the real account, got %v", payload["items"])
	}
	first, _ := items[0].(map[string]any)
	if first["identifier"] != "real@example.com" || first["device_id"] != "Breal" {
		t.Fatalf("unexpected item: %v", first)
	}
	if total, _ := payload["total"].(float64); total != 1 {
		t.Fatalf("unexpected total: %v", payload["total"])
	}
}

// 旧名 real 自动迁移为 auto：改名前保存的配置仍能列出设备清单，响应里的
// mode 也报新名，管理端不需要认识旧名。
func TestListRealDeviceIDsMigratesLegacyRealNames(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"legacy@example.com","password":"p","device_id":"B1","device_id_type":"real"}],
		"runtime":{"device_id_mode":"real"}
	}`)
	rec := realDeviceRequest(t, h, http.MethodGet, "/admin/device-ids/real", "", h.listRealDeviceIDs)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONBody(t, rec)
	if payload["mode"] != "auto" {
		t.Fatalf(`expected mode reported as "auto", got %#v`, payload["mode"])
	}
	if total, _ := payload["total_all"].(float64); total != 1 {
		t.Fatalf("expected the migrated account to be listed, got %#v", payload["total_all"])
	}
}

// 清单必须分页：page/page_size 决定切片，total/total_pages 供翻页控件使用；
// 批量刷新走全量清单，不受当前页影响。
func TestListRealDeviceIDsPaginates(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[
			{"email":"r1@example.com","password":"p","device_id":"B1","device_id_type":"auto"},
			{"email":"r2@example.com","password":"p","device_id":"B2","device_id_type":"auto"},
			{"email":"r3@example.com","password":"p","device_id":"B3","device_id_type":"auto"}
		],
		"runtime":{"device_id_mode":"auto"}
	}`)
	rec := realDeviceRequest(t, h, http.MethodGet, "/admin/device-ids/real?page=2&page_size=2", "", h.listRealDeviceIDs)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONBody(t, rec)
	if page, _ := payload["page"].(float64); page != 2 {
		t.Fatalf("unexpected page echo: %v", payload["page"])
	}
	if total, _ := payload["total"].(float64); total != 3 {
		t.Fatalf("unexpected total: %v", payload["total"])
	}
	if pages, _ := payload["total_pages"].(float64); pages != 2 {
		t.Fatalf("unexpected total_pages: %v", payload["total_pages"])
	}
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("second page should hold the remaining account, got %v", items)
	}
	last, _ := items[0].(map[string]any)
	if last["identifier"] != "r3@example.com" {
		t.Fatalf("unexpected page-2 item: %v", last)
	}

	// 越界页返回空列表而不是报错，翻页控件据此收敛。
	rec = realDeviceRequest(t, h, http.MethodGet, "/admin/device-ids/real?page=9&page_size=2", "", h.listRealDeviceIDs)
	if rec.Code != http.StatusOK {
		t.Fatalf("out-of-range page must not fail: %d", rec.Code)
	}
	if items, _ := decodeJSONBody(t, rec)["items"].([]any); len(items) != 0 {
		t.Fatalf("out-of-range page should be empty, got %v", items)
	}

	// 批量刷新覆盖全量 auto 账号，不受分页影响。
	h.DS = &fakeRealDeviceRefresher{refreshed: map[string]string{
		"r1@example.com": "Bn1", "r2@example.com": "Bn2", "r3@example.com": "Bn3",
	}}
	rec = realDeviceRequest(t, h, http.MethodPost, "/admin/device-ids/real/refresh-all", `{}`, h.refreshAllRealDeviceIDs)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if total, _ := decodeJSONBody(t, rec)["total"].(float64); total != 3 {
		t.Fatalf("bulk refresh must cover every real account, got %v", total)
	}
}

// 清单要按账号标识搜索，并把已启用账号排在最前面；total_all 始终是过滤前的全量，
// 批量刷新按钮据此判断有没有可刷新的账号（搜索筛空不算没有）。
func TestListRealDeviceIDsSearchesAndSortsEnabledFirst(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[
			{"email":"zzz-disabled@example.com","password":"p","device_id":"B1","device_id_type":"auto","disabled":true},
			{"email":"bbb-active@example.com","password":"p","device_id":"B2","device_id_type":"auto"},
			{"email":"aaa-active@example.com","password":"p","device_id":"B3","device_id_type":"auto"}
		],
		"runtime":{"device_id_mode":"auto"}
	}`)
	rec := realDeviceRequest(t, h, http.MethodGet, "/admin/device-ids/real?page=1&page_size=10", "", h.listRealDeviceIDs)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONBody(t, rec)
	items, _ := payload["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected all three real accounts, got %v", items)
	}
	if first, _ := items[0].(map[string]any)["identifier"].(string); first != "bbb-active@example.com" {
		t.Fatalf("enabled accounts must come first, got %q first", first)
	}
	if last, _ := items[2].(map[string]any)["identifier"].(string); last != "zzz-disabled@example.com" {
		t.Fatalf("disabled account must sink to the end, got %q last", last)
	}
	if totalAll, _ := payload["total_all"].(float64); totalAll != 3 {
		t.Fatalf("unexpected total_all: %v", payload["total_all"])
	}

	// 搜索：按标识大小写不敏感匹配，total_all 不随过滤变化。
	rec = realDeviceRequest(t, h, http.MethodGet, "/admin/device-ids/real?q=DISABLED", "", h.listRealDeviceIDs)
	payload = decodeJSONBody(t, rec)
	items, _ = payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("search should match one account, got %v", items)
	}
	if got, _ := items[0].(map[string]any)["identifier"].(string); got != "zzz-disabled@example.com" {
		t.Fatalf("unexpected search hit: %q", got)
	}
	if total, _ := payload["total"].(float64); total != 1 {
		t.Fatalf("filtered total should be 1, got %v", payload["total"])
	}
	if totalAll, _ := payload["total_all"].(float64); totalAll != 3 {
		t.Fatalf("total_all must not follow the filter, got %v", payload["total_all"])
	}

	// 搜索无命中：当前页为空，但批量入口仍有账号可刷。
	rec = realDeviceRequest(t, h, http.MethodGet, "/admin/device-ids/real?q=nobody", "", h.listRealDeviceIDs)
	payload = decodeJSONBody(t, rec)
	if items, _ := payload["items"].([]any); len(items) != 0 {
		t.Fatalf("expected no hits, got %v", items)
	}
	if totalAll, _ := payload["total_all"].(float64); totalAll != 3 {
		t.Fatalf("total_all must survive an empty search, got %v", payload["total_all"])
	}
}

// manual 模式下这些接口没有意义，必须直接拒绝，而不是生成一堆用不上的 id。
func TestRealDeviceIDEndpointsRejectManualMode(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"real@example.com","password":"p","device_id":"Breal","device_id_type":"auto"}],
		"runtime":{"device_id_mode":"manual"}
	}`)
	cases := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
	}{
		{"list", h.listRealDeviceIDs, http.MethodGet, "/admin/device-ids/real"},
		{"refresh", h.refreshRealDeviceID, http.MethodPost, "/admin/device-ids/real/refresh"},
		{"refresh-all", h.refreshAllRealDeviceIDs, http.MethodPost, "/admin/device-ids/real/refresh-all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := realDeviceRequest(t, h, tc.method, tc.target, `{"identifier":"real@example.com"}`, tc.handler)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRefreshRealDeviceIDValidatesRequest(t *testing.T) {
	h := newAdminTestHandler(t, realModeConfig)
	h.DS = &fakeRealDeviceRefresher{refreshed: map[string]string{"real@example.com": "Bnew"}}

	rec := realDeviceRequest(t, h, http.MethodPost, "/admin/device-ids/real/refresh", `{}`, h.refreshRealDeviceID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing identifier must be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
	rec = realDeviceRequest(t, h, http.MethodPost, "/admin/device-ids/real/refresh", `{"identifier":"nobody@example.com"}`, h.refreshRealDeviceID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown account must 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// DS 未实现刷新能力时如实返回 501，而不是假装成功。
func TestRefreshRealDeviceIDWithoutRefresher(t *testing.T) {
	h := newAdminTestHandler(t, realModeConfig)
	h.DS = &testingDSMock{}
	rec := realDeviceRequest(t, h, http.MethodPost, "/admin/device-ids/real/refresh",
		`{"identifier":"real@example.com"}`, h.refreshRealDeviceID)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
}

// reset_profile 必须原样透传给客户端：它决定"复用档案重算"还是"删档案换设备"。
func TestRefreshRealDeviceIDPassesResetFlag(t *testing.T) {
	h := newAdminTestHandler(t, realModeConfig)
	ds := &fakeRealDeviceRefresher{refreshed: map[string]string{"real@example.com": "Bnew"}}
	h.DS = ds

	rec := realDeviceRequest(t, h, http.MethodPost, "/admin/device-ids/real/refresh",
		`{"identifier":"real@example.com","reset_profile":true}`, h.refreshRealDeviceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONBody(t, rec)
	if payload["device_id"] != "Bnew" {
		t.Fatalf("unexpected payload: %v", payload)
	}
	if !ds.resetSeen["real@example.com"] {
		t.Fatal("reset_profile must be forwarded to the client")
	}

	rec = realDeviceRequest(t, h, http.MethodPost, "/admin/device-ids/real/refresh",
		`{"identifier":"real@example.com"}`, h.refreshRealDeviceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if ds.resetSeen["real@example.com"] {
		t.Fatal("reset_profile defaults to false")
	}
}

// 生成失败要带上原因，方便管理端逐行提示。
func TestRefreshRealDeviceIDReportsGenerationFailure(t *testing.T) {
	h := newAdminTestHandler(t, realModeConfig)
	h.DS = &fakeRealDeviceRefresher{failFor: map[string]string{"real@example.com": "devid: 数美不可用"}}
	rec := realDeviceRequest(t, h, http.MethodPost, "/admin/device-ids/real/refresh",
		`{"identifier":"real@example.com"}`, h.refreshRealDeviceID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if detail, _ := decodeJSONBody(t, rec)["detail"].(string); !strings.Contains(detail, "数美不可用") {
		t.Fatalf("failure reason must be surfaced, got %v", detail)
	}
}

// 批量刷新只覆盖 auto 账号，并按账号返回结果（失败不连坐）。
func TestRefreshAllRealDeviceIDsReportsPerAccountResults(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[
			{"email":"ok@example.com","password":"p","device_id":"B1","device_id_type":"auto"},
			{"email":"bad@example.com","password":"p","device_id":"B2","device_id_type":"auto"},
			{"email":"manual@example.com","password":"p","device_id":"B3","device_id_type":"manual"}
		],
		"runtime":{"device_id_mode":"auto"}
	}`)
	h.DS = &fakeRealDeviceRefresher{
		refreshed: map[string]string{"ok@example.com": "Bnew"},
		failFor:   map[string]string{"bad@example.com": "devid: 数美不可用"},
	}

	rec := realDeviceRequest(t, h, http.MethodPost, "/admin/device-ids/real/refresh-all", `{}`, h.refreshAllRealDeviceIDs)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONBody(t, rec)
	if refreshed, _ := payload["refreshed"].(float64); refreshed != 1 {
		t.Fatalf("unexpected refreshed count: %v", payload)
	}
	if failed, _ := payload["failed"].(float64); failed != 1 {
		t.Fatalf("unexpected failed count: %v", payload)
	}
	if success, _ := payload["success"].(bool); success {
		t.Fatalf("partial failure must not report success: %v", payload)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("manual accounts must not be refreshed, got %v", items)
	}
	second, _ := items[1].(map[string]any)
	if second["identifier"] != "bad@example.com" || second["error"] == "" {
		t.Fatalf("failed account must carry its reason: %v", second)
	}
}

// 没有 auto 账号时批量刷新是空操作，不该报错。
func TestRefreshAllRealDeviceIDsWithoutAccounts(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"manual@example.com","password":"p","device_id":"B1","device_id_type":"manual"}],
		"runtime":{"device_id_mode":"auto"}
	}`)
	h.DS = &fakeRealDeviceRefresher{}
	rec := realDeviceRequest(t, h, http.MethodPost, "/admin/device-ids/real/refresh-all", `{}`, h.refreshAllRealDeviceIDs)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	payload := decodeJSONBody(t, rec)
	if total, _ := payload["total"].(float64); total != 0 {
		t.Fatalf("expected an empty batch, got %v", payload)
	}
	if success, _ := payload["success"].(bool); !success {
		t.Fatalf("empty batch should report success: %v", payload)
	}
}
