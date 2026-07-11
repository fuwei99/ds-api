package configmgmt

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestKeyEndpointsPreserveStructuredMetadata(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"api_keys":[{"key":"k1","name":"primary","remark":"prod"}]
	}`)

	r := chi.NewRouter()
	r.Post("/admin/keys", h.addKey)
	r.Put("/admin/keys/{key}", h.updateKey)
	r.Delete("/admin/keys/{key}", h.deleteKey)

	addBody := []byte(`{"key":"k2","name":"secondary","remark":"staging"}`)
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
	if snap.APIKeys[0].Name != "primary" || snap.APIKeys[0].Remark != "prod" {
		t.Fatalf("existing metadata was lost after add: %#v", snap.APIKeys[0])
	}
	if snap.APIKeys[1].Name != "secondary" || snap.APIKeys[1].Remark != "staging" {
		t.Fatalf("new metadata was lost after add: %#v", snap.APIKeys[1])
	}

	updateBody := map[string]any{
		"name":   "primary-updated",
		"remark": "prod-updated",
	}
	updateBytes, _ := json.Marshal(updateBody)
	updateReq := httptest.NewRequest(http.MethodPut, "/admin/keys/k1", bytes.NewReader(updateBytes))
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}

	snap = h.Store.Snapshot()
	if len(snap.APIKeys) != 2 {
		t.Fatalf("unexpected api keys after update: %#v", snap.APIKeys)
	}
	if snap.APIKeys[0].Key != "k1" || snap.APIKeys[0].Name != "primary-updated" || snap.APIKeys[0].Remark != "prod-updated" {
		t.Fatalf("metadata update did not persist: %#v", snap.APIKeys[0])
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/admin/keys/k1", nil)
	deleteRec := httptest.NewRecorder()
	r.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	snap = h.Store.Snapshot()
	if len(snap.APIKeys) != 1 || snap.APIKeys[0].Key != "k2" {
		t.Fatalf("unexpected api keys after delete: %#v", snap.APIKeys)
	}
	if len(snap.Keys) != 1 || snap.Keys[0] != "k2" {
		t.Fatalf("unexpected legacy keys after delete: %#v", snap.Keys)
	}
}

func TestKeyEndpointsPersistToolsEnabled(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"api_keys":[{"key":"k1","name":"primary","remark":"prod"}]
	}`)

	r := chi.NewRouter()
	r.Post("/admin/keys", h.addKey)
	r.Put("/admin/keys/{key}", h.updateKey)

	addBody := []byte(`{"key":"k2","name":"secondary","remark":"staging","tools_enabled":true}`)
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
	if snap.APIKeys[0].ToolsEnabled != false {
		t.Fatalf("existing key tools_enabled should default false: %#v", snap.APIKeys[0])
	}
	if snap.APIKeys[1].ToolsEnabled != true {
		t.Fatalf("new key tools_enabled was not persisted: %#v", snap.APIKeys[1])
	}
	if !h.Store.APIKeyToolsEnabled("k2") {
		t.Fatalf("APIKeyToolsEnabled should return true for k2")
	}
	if h.Store.APIKeyToolsEnabled("k1") {
		t.Fatalf("APIKeyToolsEnabled should return false for k1")
	}

	updateBody := map[string]any{
		"tools_enabled": true,
	}
	updateBytes, _ := json.Marshal(updateBody)
	updateReq := httptest.NewRequest(http.MethodPut, "/admin/keys/k1", bytes.NewReader(updateBytes))
	updateRec := httptest.NewRecorder()
	r.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}

	snap = h.Store.Snapshot()
	if snap.APIKeys[0].ToolsEnabled != true {
		t.Fatalf("tools_enabled update did not persist: %#v", snap.APIKeys[0])
	}
	if !h.Store.APIKeyToolsEnabled("k1") {
		t.Fatalf("APIKeyToolsEnabled should return true for k1 after update")
	}
}
