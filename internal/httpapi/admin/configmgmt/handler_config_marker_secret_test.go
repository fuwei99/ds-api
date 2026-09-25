package configmgmt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

const markerSecretConfig = `{
	"keys":["k1"],
	"tool_marker_secret":"secret-keep-me",
	"accounts":[{"email":"acc1@test.com","password":"pwd"}]
}`

// newMarkerSecretHandler keeps the concrete *config.Store around so the tests can
// observe the marker secret, which the admin ConfigStore interface does not expose.
func newMarkerSecretHandler(t *testing.T, raw string) (*Handler, *config.Store) {
	t.Helper()
	t.Setenv("DS2API_CONFIG_JSON", raw)
	store := config.LoadStore()
	return &Handler{Store: store, Pool: account.NewPool(store)}, store
}

// TestConfigImportReplaceKeepsToolMarkerSecret pins the "never exposed" contract
// for the server-generated tool-call marker secret: an imported payload cannot
// carry it, so a replace import must keep the running value. Otherwise every API
// key's tool-call marker would silently change until the next process start.
func TestConfigImportReplaceKeepsToolMarkerSecret(t *testing.T) {
	h, store := newMarkerSecretHandler(t, markerSecretConfig)
	if got := store.ToolMarkerSecret(); got != "secret-keep-me" {
		t.Fatalf("precondition: unexpected marker secret %q", got)
	}

	payload, _ := json.Marshal(map[string]any{
		"mode": "replace",
		"config": map[string]any{
			"keys":     []any{"k2"},
			"accounts": []any{map[string]any{"email": "acc2@test.com", "password": "pwd"}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/admin/config/import?mode=replace", strings.NewReader(string(payload)))
	rec := httptest.NewRecorder()

	h.configImport(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := store.ToolMarkerSecret(); got != "secret-keep-me" {
		t.Fatalf("replace import must preserve the marker secret, got %q", got)
	}
}

// TestConfigExportDropsToolMarkerSecret pins the other half of the contract: the
// export payload must never carry the server-internal marker secret.
func TestConfigExportDropsToolMarkerSecret(t *testing.T) {
	_, store := newMarkerSecretHandler(t, markerSecretConfig)

	jsonStr, b64, err := store.ExportJSONAndBase64()
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if strings.Contains(jsonStr, "tool_marker_secret") || strings.Contains(jsonStr, "secret-keep-me") {
		t.Fatalf("export leaked the marker secret: %s", jsonStr)
	}
	if strings.Contains(b64, "secret-keep-me") {
		t.Fatalf("base64 export leaked the marker secret: %s", b64)
	}
}
