package configmgmt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postConfigImport sends one import payload through h and returns the recorder.
func postConfigImport(t *testing.T, h *Handler, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/config/import", strings.NewReader(string(b)))
	rec := httptest.NewRecorder()
	h.configImport(rec, req)
	return rec
}

// auto_delete declares an explicit no-op Merge, so a merge import must never
// turn on post-request deletion of remote conversations: only replace applies
// it. A merge that wrote the imported mode would silently switch on a
// destructive behavior from a backup file.
func TestConfigImportMergeLeavesAutoDeleteAlone(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"auto_delete":{"mode":"none"}}`)
	if got := h.Store.AutoDeleteMode(); got != "none" {
		t.Fatalf("precondition: AutoDeleteMode()=%q want none", got)
	}

	rec := postConfigImport(t, h, map[string]any{
		"mode": "merge",
		"config": map[string]any{
			"keys":        []any{"k1"},
			"auto_delete": map[string]any{"mode": "all"},
			// A registered setting travels with the same payload, so the test
			// shows the merge ran and skipped auto_delete rather than the whole
			// import doing nothing.
			"responses": map[string]any{"store_ttl_seconds": 600},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.SettingInt("responses", "store_ttl_seconds"); got != 600 {
		t.Fatalf("the merge must apply the settings it carries, store_ttl_seconds=%d", got)
	}
	if got := h.Store.AutoDeleteMode(); got != "none" {
		t.Fatalf("a merge import must leave auto_delete alone, AutoDeleteMode()=%q", got)
	}
}

// The replace half of the same contract: replace applies auto_delete the way it
// applies everything else, so a backup restores the configured behavior.
func TestConfigImportReplaceAppliesAutoDelete(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"auto_delete":{"mode":"none"}}`)

	rec := postConfigImport(t, h, map[string]any{
		"mode": "replace",
		"config": map[string]any{
			"keys":        []any{"k1"},
			"auto_delete": map[string]any{"mode": "all"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.AutoDeleteMode(); got != "all" {
		t.Fatalf("replace must apply the imported auto_delete, AutoDeleteMode()=%q", got)
	}
}
