package accounts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func deviceIDForTest(seed byte) string {
	return "B" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 64))
}

func postJSONBody(t *testing.T, h *Handler, path, body string, fn http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

func TestAddDeviceIDAcceptsRawAndPrefixedForms(t *testing.T) {
	h := newAdminTestHandler(t, `{"accounts":[{"email":"u@example.com","password":"pwd"}]}`)
	raw := strings.TrimPrefix(deviceIDForTest(1), "B")

	rec := postJSONBody(t, h, "/admin/device-ids", `{"id":"`+raw+`"}`, h.addDeviceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	items := h.Store.DeviceIDPoolItems()
	if len(items) != 1 || items[0].ID != deviceIDForTest(1) {
		t.Fatalf("raw form must be stored with B prefix, got %+v", items)
	}

	// 重复提交同一个 id 必须报错。
	rec = postJSONBody(t, h, "/admin/device-ids", `{"id":"`+deviceIDForTest(1)+`"}`, h.addDeviceID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected duplicate rejection, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAddDeviceIDRejectsInvalidFormat(t *testing.T) {
	h := newAdminTestHandler(t, `{"accounts":[{"email":"u@example.com","password":"pwd"}]}`)
	body := strings.TrimSuffix(strings.TrimPrefix(deviceIDForTest(2), "B"), "==")

	rec := postJSONBody(t, h, "/admin/device-ids", `{"id":"`+body+`"}`, h.addDeviceID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed id, got %d body=%s", rec.Code, rec.Body.String())
	}
	if h.Store.DeviceIDPoolSize() != 0 {
		t.Fatal("invalid id must not be stored")
	}
}

func TestDeleteDeviceIDRebindsAccounts(t *testing.T) {
	first, second := deviceIDForTest(3), deviceIDForTest(4)
	h := newAdminTestHandler(t, `{
		"accounts":[
			{"email":"a@example.com","password":"pwd","device_id":"`+first+`","device_id_type":"manual"}
		],
		"device_id_pool":{"items":[{"id":"`+first+`"},{"id":"`+second+`"}]}
	}`)

	rec := postJSONBody(t, h, "/admin/device-ids/delete", `{"id":"`+first+`"}`, h.deleteDeviceID)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc := h.Store.Accounts()[0]
	if acc.DeviceID != second {
		t.Fatalf("account must be rebound to %q, got %q", second, acc.DeviceID)
	}
	if h.Store.DeviceIDPoolSize() != 1 {
		t.Fatalf("expected one remaining id, got %d", h.Store.DeviceIDPoolSize())
	}

	// 删除不存在的 id 返回 404。
	rec = postJSONBody(t, h, "/admin/device-ids/delete", `{"id":"`+first+`"}`, h.deleteDeviceID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing id, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestListDeviceIDsReportsBoundCounts(t *testing.T) {
	first := deviceIDForTest(5)
	h := newAdminTestHandler(t, `{
		"accounts":[
			{"email":"a@example.com","password":"pwd","device_id":"`+first+`","device_id_type":"manual"}
		],
		"device_id_pool":{"items":[{"id":"`+first+`"}]}
	}`)

	req := httptest.NewRequest(http.MethodGet, "/admin/device-ids", nil)
	rec := httptest.NewRecorder()
	h.listDeviceIDs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var payload struct {
		Mode      string `json:"mode"`
		Available int    `json:"available"`
		Items     []struct {
			ID         string `json:"id"`
			BoundCount int    `json:"bound_count"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Mode != "manual" || payload.Available != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if len(payload.Items) != 1 || payload.Items[0].BoundCount != 1 {
		t.Fatalf("expected bound count 1, got %+v", payload.Items)
	}
}

func TestQueueStatusExposesDeviceIDPool(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"u@example.com","password":"pwd"}],
		"device_id_pool":{"items":[{"id":"`+deviceIDForTest(6)+`"}]}
	}`)
	req := httptest.NewRequest(http.MethodGet, "/admin/queue/status", nil)
	rec := httptest.NewRecorder()
	h.queueStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["device_id_mode"] != "manual" {
		t.Fatalf("expected manual mode, got %#v", payload["device_id_mode"])
	}
	if payload["device_id_remaining"] != float64(1) {
		t.Fatalf("expected remaining 1, got %#v", payload["device_id_remaining"])
	}
}

func TestToggleAccountEnabledUnbindsDeviceID(t *testing.T) {
	first := deviceIDForTest(7)
	h := newAdminTestHandler(t, `{
		"accounts":[{"email":"a@example.com","password":"pwd","device_id":"`+first+`","device_id_type":"manual"}],
		"device_id_pool":{"items":[{"id":"`+first+`"}]}
	}`)

	req := httptest.NewRequest(http.MethodPut, "/admin/accounts/a@example.com/enabled", strings.NewReader(`{"enabled":false}`))
	rec := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Put("/admin/accounts/{identifier}/enabled", h.toggleAccountEnabled)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	acc := h.Store.Accounts()[0]
	if acc.DeviceID != "" {
		t.Fatalf("disabled account must be unbound, got %q", acc.DeviceID)
	}
	if items := h.Store.DeviceIDPoolItems(); items[0].Bound != 0 {
		t.Fatalf("bound count must drop to 0, got %+v", items)
	}
}

// 复现「加入第一个 device_id 时全部账号被绑到它身上」的问题：
// 新增 device_id 不得给尚未绑定的账号分配 id。
func TestAddDeviceIDDoesNotBindUnboundAccounts(t *testing.T) {
	first, second := deviceIDForTest(20), deviceIDForTest(21)
	h := newAdminTestHandler(t, `{
		"accounts":[
			{"email":"a@example.com","password":"pwd","device_id":"`+first+`","device_id_type":"manual"},
			{"email":"b@example.com","password":"pwd"},
			{"email":"c@example.com","password":"pwd","device_id":"BLegacyStaleValue"}
		]
	}`)

	for _, id := range []string{first, second} {
		rec := postJSONBody(t, h, "/admin/device-ids", `{"id":"`+id+`"}`, h.addDeviceID)
		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
		}
		for _, acc := range h.Store.Accounts() {
			if acc.DeviceID != "" {
				t.Fatalf("no account may be bound after adding %s, but %s got %q", id, acc.Identifier(), acc.DeviceID)
			}
		}
		for _, item := range h.Store.DeviceIDPoolItems() {
			if item.Bound != 0 {
				t.Fatalf("bound count must stay 0, got %+v", item)
			}
		}
	}
	if h.Store.DeviceIDPoolSize() != 2 {
		t.Fatalf("expected two ids in the pool, got %d", h.Store.DeviceIDPoolSize())
	}
}
