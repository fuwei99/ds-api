package configmgmt

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestKeyEndpointsPersistForceDisableTools 验证 /admin/keys 能新增与更新
// 「强制禁用工具调用」开关，且只传部分字段时其他字段不受影响。
func TestKeyEndpointsPersistForceDisableTools(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"api_keys":[{"key":"k1","name":"primary","remark":"prod"}]
	}`)

	r := chi.NewRouter()
	r.Post("/admin/keys", h.addKey)
	r.Put("/admin/keys/{key}", h.updateKey)

	// 新增：默认关闭。
	addBody := []byte(`{"key":"k2","name":"secondary"}`)
	addReq := httptest.NewRequest(http.MethodPost, "/admin/keys", bytes.NewReader(addBody))
	addRec := httptest.NewRecorder()
	r.ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add status=%d body=%s", addRec.Code, addRec.Body.String())
	}
	snap := h.Store.Snapshot()
	if len(snap.APIKeys) != 2 {
		t.Fatalf("unexpected api keys after add: %#v", snap.APIKeys)
	}
	if snap.APIKeys[1].ForceDisableTools {
		t.Fatalf("expected new key to default force_disable_tools=false: %#v", snap.APIKeys[1])
	}
	if h.Store.APIKeyForceDisableTools("k2") {
		t.Fatal("expected store index to report false for a default key")
	}

	// 更新 k1：只传开关，name/remark 应保持原值。
	updateBody := map[string]any{"force_disable_tools": true}
	updateBytes, _ := json.Marshal(updateBody)
	updateReq := httptest.NewRequest(http.MethodPut, "/admin/keys/k1", bytes.NewReader(updateBytes))
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	snap = h.Store.Snapshot()
	if !snap.APIKeys[0].ForceDisableTools {
		t.Fatalf("expected force_disable_tools to be persisted: %#v", snap.APIKeys[0])
	}
	if snap.APIKeys[0].Name != "primary" || snap.APIKeys[0].Remark != "prod" {
		t.Fatalf("partial update must not clear metadata: %#v", snap.APIKeys[0])
	}
	if !h.Store.APIKeyForceDisableTools("k1") {
		t.Fatal("expected store index to report true after enabling")
	}

	// 再关闭，确认开关可双向翻转。
	updateBody = map[string]any{"force_disable_tools": false}
	updateBytes, _ = json.Marshal(updateBody)
	updateReq = httptest.NewRequest(http.MethodPut, "/admin/keys/k1", bytes.NewReader(updateBytes))
	updateRec = httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update-off status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	if h.Store.APIKeyForceDisableTools("k1") {
		t.Fatal("expected store index to report false after disabling")
	}
}

// TestAddKeyAcceptsForceDisableToolsOnCreate 验证新增时可直接带上开关。
func TestAddKeyAcceptsForceDisableToolsOnCreate(t *testing.T) {
	h := newAdminTestHandler(t, `{"api_keys":[]}`)

	r := chi.NewRouter()
	r.Post("/admin/keys", h.addKey)

	addBody := []byte(`{"key":"blocked","force_disable_tools":true}`)
	addReq := httptest.NewRequest(http.MethodPost, "/admin/keys", bytes.NewReader(addBody))
	addRec := httptest.NewRecorder()
	r.ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add status=%d body=%s", addRec.Code, addRec.Body.String())
	}
	if !h.Store.APIKeyForceDisableTools("blocked") {
		t.Fatal("expected force_disable_tools=true to be persisted on create")
	}
}
