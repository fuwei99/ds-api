package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	authn "ds2api/internal/auth"
)

func TestGetSettingsDefaultPasswordWarning(t *testing.T) {
	t.Setenv("DS2API_ADMIN_KEY", "")
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec := httptest.NewRecorder()
	h.getSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	admin, _ := body["admin"].(map[string]any)
	warn, _ := admin["default_password_warning"].(bool)
	if !warn {
		t.Fatalf("expected default password warning true, body=%v", body)
	}
}

func TestGetSettingsReportsSuccess(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	rec := httptest.NewRecorder()
	h.getSettings(rec, httptest.NewRequest(http.MethodGet, "/admin/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	// Every admin endpoint answers with it and both API docs list it as part of
	// this response, so a client that checks it must not see it disappear.
	if body["success"] != true {
		t.Fatalf("GET /admin/settings must report success=true, body=%v", body)
	}
}

func TestGetSettingsIncludesTokenRefreshInterval(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"runtime":{"token_refresh_interval_hours":9}
	}`)
	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec := httptest.NewRecorder()
	h.getSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	runtime, _ := body["runtime"].(map[string]any)
	if got := intFrom(runtime["token_refresh_interval_hours"]); got != 9 {
		t.Fatalf("expected token_refresh_interval_hours=9, got %d body=%v", got, body)
	}
}

func TestGetSettingsIncludesCurrentInputFileDefaults(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	req := httptest.NewRequest(http.MethodGet, "/admin/settings", nil)
	rec := httptest.NewRecorder()
	h.getSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	currentInputFile, _ := body["input_file"].(map[string]any)
	if got := boolFrom(currentInputFile["enabled"]); got {
		t.Fatalf("expected input_file.enabled=false, body=%v", body)
	}
	if got := intFrom(currentInputFile["min_chars"]); got != 0 {
		t.Fatalf("expected input_file.min_chars=0, got %d body=%v", got, body)
	}
	autoRouteVision, _ := body["auto_route_vision"].(map[string]any)
	if got := boolFrom(autoRouteVision["enabled"]); got {
		t.Fatalf("expected auto_route_vision.enabled=false, body=%v", body)
	}
	thinkingInjection, _ := body["thinking_injection"].(map[string]any)
	if got := boolFrom(thinkingInjection["enabled"]); got {
		t.Fatalf("expected thinking_injection.enabled=false, body=%v", body)
	}
	if got, _ := thinkingInjection["prompt"].(string); got != "" {
		t.Fatalf("expected empty custom thinking prompt, got %q body=%v", got, body)
	}
	if got, _ := thinkingInjection["default_prompt"].(string); got == "" {
		t.Fatalf("expected default thinking prompt, body=%v", body)
	}
}

func TestUpdateSettingsValidation(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{
		"runtime": map[string]any{
			"account_max_inflight": 0,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUpdateSettingsValidationRejectsTokenRefreshInterval(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{
		"runtime": map[string]any{
			"token_refresh_interval_hours": 0,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("runtime.token_refresh_interval_hours")) {
		t.Fatalf("expected token refresh validation detail, got %s", rec.Body.String())
	}
}

func TestUpdateSettingsAllowsEmptyEmbeddingsProvider(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{
		"responses": map[string]any{
			"store_ttl_seconds": 600,
		},
		"embeddings": map[string]any{
			"provider": "",
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.SettingInt("responses", "store_ttl_seconds"); got != 600 {
		t.Fatalf("store_ttl_seconds=%d want=600", got)
	}
}

func TestUpdateSettingsValidationWithMergedRuntimeSnapshot(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"runtime":{
			"account_max_inflight":8,
			"global_max_inflight":8
		}
	}`)
	payload := map[string]any{
		"runtime": map[string]any{
			"account_max_inflight": 16,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("runtime.global_max_inflight")) {
		t.Fatalf("expected merged runtime validation detail, got %s", rec.Body.String())
	}
}

func TestUpdateSettingsWithoutRuntimeSkipsMergedRuntimeValidation(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"runtime":{
			"account_max_inflight":8,
			"global_max_inflight":4
		}
	}`)
	payload := map[string]any{
		"responses": map[string]any{
			"store_ttl_seconds": 600,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.SettingInt("responses", "store_ttl_seconds"); got != 600 {
		t.Fatalf("store_ttl_seconds=%d want=600", got)
	}
}

func TestUpdateSettingsCurrentInputFile(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"history_split":{"enabled":true,"trigger_after_turns":2}}`)
	payload := map[string]any{
		"input_file": map[string]any{
			"enabled":   true,
			"min_chars": 12345,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	stored := h.Store.Snapshot().Settings["input_file"]
	if stored["enabled"] != true {
		t.Fatalf("expected input_file.enabled=true, got %#v", stored)
	}
	if stored["min_chars"] != 12345 {
		t.Fatalf("expected input_file.min_chars=12345, got %#v", stored)
	}
	if !h.Store.SettingBool("input_file", "enabled") {
		t.Fatal("expected current input file accessor to stay enabled")
	}
}

func TestUpdateSettingsCurrentInputFilePartialUpdatePreservesEnabled(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"input_file":{"enabled":false,"min_chars":777}}`)
	payload := map[string]any{
		"input_file": map[string]any{
			"min_chars": 5000,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	stored := h.Store.Snapshot().Settings["input_file"]
	if stored["enabled"] != false {
		t.Fatalf("expected input_file.enabled to remain false, got %#v", stored["enabled"])
	}
	if stored["min_chars"] != 5000 {
		t.Fatalf("expected input_file.min_chars=5000, got %#v", stored)
	}
}

func TestUpdateSettingsCurrentInputFilePartialUpdatePreservesMinChars(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"input_file":{"enabled":false,"min_chars":777}}`)
	payload := map[string]any{
		"input_file": map[string]any{
			"enabled": true,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	stored := h.Store.Snapshot().Settings["input_file"]
	if stored["enabled"] != true {
		t.Fatalf("expected input_file.enabled=true, got %#v", stored["enabled"])
	}
	if stored["min_chars"] != 777 {
		t.Fatalf("expected input_file.min_chars to remain 777, got %#v", stored)
	}
}

func TestUpdateSettingsIgnoresHistorySplitPayload(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{
		"history_split": map[string]any{
			"enabled":             true,
			"trigger_after_turns": 3,
		},
		"input_file": map[string]any{
			"enabled":   true,
			"min_chars": 0,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.Snapshot().Settings["input_file"]["enabled"]; got != true {
		t.Fatalf("expected input_file to remain enabled, got %#v", got)
	}
}

func TestUpdateSettingsThinkingInjection(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{
		"thinking_injection": map[string]any{
			"enabled": false,
			"prompt":  " custom thinking prompt ",
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.SettingBool("thinking_injection", "enabled"); got {
		t.Fatalf("expected thinking_injection.enabled=false, got %#v", got)
	}
	if got := h.Store.SettingString("thinking_injection", "prompt"); got != "custom thinking prompt" {
		t.Fatalf("expected custom thinking prompt, got %q", got)
	}
}

func TestUpdateSettingsThinkingInjectionPartialPromptPreservesEnabled(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"thinking_injection":{"enabled":false,"prompt":"original prompt"}}`)
	payload := map[string]any{
		"thinking_injection": map[string]any{
			"prompt": " updated prompt ",
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.SettingBool("thinking_injection", "enabled"); got {
		t.Fatalf("expected thinking_injection.enabled to remain false, got %#v", got)
	}
	if got := h.Store.SettingString("thinking_injection", "prompt"); got != "updated prompt" {
		t.Fatalf("expected updated prompt, got %q", got)
	}
}

func TestUpdateSettingsThinkingInjectionPartialEnabledPreservesPrompt(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"thinking_injection":{"enabled":false,"prompt":"original prompt"}}`)
	payload := map[string]any{
		"thinking_injection": map[string]any{
			"enabled": true,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.SettingBool("thinking_injection", "enabled"); !got {
		t.Fatalf("expected thinking_injection.enabled=true, got %#v", got)
	}
	if got := h.Store.SettingString("thinking_injection", "prompt"); got != "original prompt" {
		t.Fatalf("expected original prompt to be preserved, got %q", got)
	}
}

func TestUpdateSettingsAutoDeleteMode(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"auto_delete":{"sessions":true}}`)

	payload := map[string]any{
		"auto_delete": map[string]any{
			"mode": "single",
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	snap := h.Store.Snapshot()
	if got := snap.AutoDelete.Mode; got != "single" {
		t.Fatalf("auto_delete.mode=%q want=single", got)
	}
	if got := h.Store.AutoDeleteMode(); got != "single" {
		t.Fatalf("AutoDeleteMode()=%q want=single", got)
	}
}

// The legacy sessions boolean is no longer a settings field: a write that still
// carries it is rejected with the replacement named, rather than answered with a
// 200 that changed nothing. The config-file form of the legacy key keeps
// working (see TestUpdateSettingsAutoDeleteMode's starting config).
func TestUpdateSettingsRejectsLegacyAutoDeleteSessions(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"auto_delete":{"sessions":true}}`)

	payload := map[string]any{
		"auto_delete": map[string]any{
			"sessions": false,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"auto_delete.sessions", "auto_delete.mode"} {
		if !bytes.Contains(rec.Body.Bytes(), []byte(want)) {
			t.Fatalf("the error must name %s, got %s", want, rec.Body.String())
		}
	}
	// Nothing was committed: the legacy file value is still in force.
	if got := h.Store.AutoDeleteMode(); got != "all" {
		t.Fatalf("a rejected update must not change the config, AutoDeleteMode()=%q", got)
	}
}

// An explicit 0 in a settings update is the value the user chose, not the
// "unset" sentinel: a bounded int whose declared minimum is positive rejects it
// instead of silently resetting the setting to its default.
func TestUpdateSettingsRejectsExplicitZeroIntSetting(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"responses":{"store_ttl_seconds":600}}`)

	payload := map[string]any{
		"responses": map[string]any{
			"store_ttl_seconds": 0,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("responses.store_ttl_seconds")) {
		t.Fatalf("the error must name the field, got %s", rec.Body.String())
	}
	if got := h.Store.SettingInt("responses", "store_ttl_seconds"); got != 600 {
		t.Fatalf("a rejected update must not reset the setting, got %d", got)
	}
}

// A bounded int whose declared range includes 0 keeps accepting it: 0 is a
// meaningful value there, not an out-of-range one.
func TestUpdateSettingsAcceptsZeroWithinTheDeclaredRange(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"input_file":{"enabled":true,"min_chars":5000}}`)

	payload := map[string]any{
		"input_file": map[string]any{
			"min_chars": 0,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.SettingInt("input_file", "min_chars"); got != 0 {
		t.Fatalf("min_chars=%d want 0", got)
	}
}

// A trimmed string field cannot tell "the user cleared it" from "the user typed
// spaces", so both are the unset value and neither fails the request.
func TestUpdateSettingsAcceptsBlankEmbeddingsProvider(t *testing.T) {
	for _, provider := range []any{"", "   "} {
		h := newAdminTestHandler(t, `{"keys":["k1"],"embeddings":{"provider":"ollama"}}`)
		payload := map[string]any{
			"embeddings": map[string]any{"provider": provider},
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
		rec := httptest.NewRecorder()
		h.updateSettings(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("provider %#v: expected 200, got %d body=%s", provider, rec.Code, rec.Body.String())
		}
		if got := h.Store.SettingString("embeddings", "provider"); got != "" {
			t.Fatalf("provider %#v: expected the value to be cleared, got %q", provider, got)
		}
	}
}

func TestUpdateSettingsAutoRouteVision(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{
		"auto_route_vision": map[string]any{
			"enabled": true,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	// The value round-trips into the config, but the retired feature keeps its
	// kill switch: the effective value is forced off.
	if got := h.Store.Snapshot().Settings["auto_route_vision"]["enabled"]; got != true {
		t.Fatalf("expected auto_route_vision.enabled=true to be persisted, got %#v", got)
	}
	if h.Store.SettingBool("auto_route_vision", "enabled") {
		t.Fatal("expected auto route vision to stay force-disabled")
	}
}

func TestUpdateSettingsAutoRouteVisionPreservesWhenNotProvided(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"auto_route_vision":{"enabled":true}}`)
	payload := map[string]any{
		"responses": map[string]any{
			"store_ttl_seconds": 600,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.Snapshot().Settings["auto_route_vision"]["enabled"]; got != true {
		t.Fatalf("expected auto_route_vision.enabled to remain true, got %#v", got)
	}
}

func TestUpdateSettingsHotReloadRuntime(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"accounts":[{"email":"a@test.com","token":"t1"},{"email":"b@test.com","token":"t2"}]
	}`)

	payload := map[string]any{
		"runtime": map[string]any{
			"account_max_inflight": 3,
			"account_max_queue":    20,
			"global_max_inflight":  5,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	status := h.Pool.Status()
	if got := intFrom(status["max_inflight_per_account"]); got != 3 {
		t.Fatalf("max_inflight_per_account=%d want=3", got)
	}
	if got := intFrom(status["max_queue_size"]); got != 20 {
		t.Fatalf("max_queue_size=%d want=20", got)
	}
	if got := intFrom(status["global_max_inflight"]); got != 5 {
		t.Fatalf("global_max_inflight=%d want=5", got)
	}
}

func TestUpdateSettingsHotReloadTokenRefreshInterval(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"runtime":{"token_refresh_interval_hours":6}
	}`)

	payload := map[string]any{
		"runtime": map[string]any{
			"token_refresh_interval_hours": 12,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := h.Store.RuntimeTokenRefreshIntervalHours(); got != 12 {
		t.Fatalf("token_refresh_interval_hours=%d want=12", got)
	}
}

func TestUpdateConfigPreservesStructuredAPIKeysWhenBothFieldsPresent(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["legacy"],
		"api_keys":[{"key":"legacy","name":"primary","remark":"prod"}],
		"accounts":[]
	}`)

	payload := map[string]any{
		"keys": []any{"legacy", "new-key"},
		"api_keys": []any{
			map[string]any{"key": "legacy", "name": "primary-updated", "remark": "prod-updated"},
			map[string]any{"key": "new-key", "name": "secondary", "remark": "staging"},
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/admin/config", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	snap := h.Store.Snapshot()
	if len(snap.Keys) != 2 || snap.Keys[0] != "legacy" || snap.Keys[1] != "new-key" {
		t.Fatalf("unexpected keys after config update: %#v", snap.Keys)
	}
	if len(snap.APIKeys) != 2 {
		t.Fatalf("unexpected api keys after config update: %#v", snap.APIKeys)
	}
	if snap.APIKeys[0].Name != "primary-updated" || snap.APIKeys[0].Remark != "prod-updated" {
		t.Fatalf("structured metadata for existing key was not preserved: %#v", snap.APIKeys[0])
	}
	if snap.APIKeys[1].Name != "secondary" || snap.APIKeys[1].Remark != "staging" {
		t.Fatalf("structured metadata for new key was not preserved: %#v", snap.APIKeys[1])
	}
}

func TestUpdateConfigLegacyKeysPreserveStructuredMetadata(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"api_keys":[{"key":"legacy","name":"primary","remark":"prod"}],
		"accounts":[]
	}`)

	payload := map[string]any{
		"keys": []any{"legacy", "new-key"},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/admin/config", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	snap := h.Store.Snapshot()
	if len(snap.Keys) != 2 || snap.Keys[0] != "legacy" || snap.Keys[1] != "new-key" {
		t.Fatalf("unexpected keys after legacy config update: %#v", snap.Keys)
	}
	if len(snap.APIKeys) != 2 {
		t.Fatalf("unexpected api keys after legacy config update: %#v", snap.APIKeys)
	}
	if snap.APIKeys[0].Name != "primary" || snap.APIKeys[0].Remark != "prod" {
		t.Fatalf("existing structured metadata was lost: %#v", snap.APIKeys[0])
	}
	if snap.APIKeys[1].Key != "new-key" || snap.APIKeys[1].Name != "" || snap.APIKeys[1].Remark != "" {
		t.Fatalf("new legacy key should remain metadata-free: %#v", snap.APIKeys[1])
	}
}

func TestUpdateConfigReplacesModelAliases(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"model_aliases":{"claude-sonnet-4-6":"deepseek-v4.1-flash"}
	}`)

	payload := map[string]any{
		"model_aliases": map[string]any{
			"gpt-5.5": "deepseek-v4.1-flash",
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/admin/config", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	snap := h.Store.Snapshot()
	if len(snap.ModelAliases) != 1 {
		t.Fatalf("expected aliases to be replaced, got %#v", snap.ModelAliases)
	}
	if snap.ModelAliases["gpt-5.5"] != "deepseek-v4.1-flash" {
		t.Fatalf("expected updated alias, got %#v", snap.ModelAliases)
	}
}

func TestUpdateSettingsPasswordInvalidatesOldJWT(t *testing.T) {
	hash := authn.HashAdminPassword("old-password")
	h := newAdminTestHandler(t, `{"admin":{"password_hash":"`+hash+`"}}`)

	token, err := authn.CreateJWTWithStore(1, h.Store)
	if err != nil {
		t.Fatalf("create jwt failed: %v", err)
	}
	if _, err := authn.VerifyJWTWithStore(token, h.Store); err != nil {
		t.Fatalf("verify before update failed: %v", err)
	}

	body := map[string]any{"new_password": "new-password"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/admin/settings/password", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettingsPassword(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	if _, err := authn.VerifyJWTWithStore(token, h.Store); err == nil {
		t.Fatal("expected old token to be invalid after password update")
	}
	if !authn.VerifyAdminCredential("new-password", h.Store) {
		t.Fatal("expected new password credential to be accepted")
	}
}

func TestConfigImportMergeAndReplace(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"accounts":[{"email":"a@test.com","password":"p1"}]
	}`)

	merge := map[string]any{
		"mode": "merge",
		"config": map[string]any{
			"keys": []any{"k1", "k2"},
			"accounts": []any{
				map[string]any{"email": "a@test.com", "password": "p1"},
				map[string]any{"email": "b@test.com", "password": "p2"},
			},
		},
	}
	mergeBytes, _ := json.Marshal(merge)
	mergeReq := httptest.NewRequest(http.MethodPost, "/admin/config/import?mode=merge", bytes.NewReader(mergeBytes))
	mergeRec := httptest.NewRecorder()
	h.configImport(mergeRec, mergeReq)
	if mergeRec.Code != http.StatusOK {
		t.Fatalf("merge status=%d body=%s", mergeRec.Code, mergeRec.Body.String())
	}
	if got := len(h.Store.Keys()); got != 2 {
		t.Fatalf("keys after merge=%d want=2", got)
	}
	if got := len(h.Store.Accounts()); got != 2 {
		t.Fatalf("accounts after merge=%d want=2", got)
	}

	replace := map[string]any{
		"mode": "replace",
		"config": map[string]any{
			"keys": []any{"k9"},
		},
	}
	replaceBytes, _ := json.Marshal(replace)
	replaceReq := httptest.NewRequest(http.MethodPost, "/admin/config/import?mode=replace", bytes.NewReader(replaceBytes))
	replaceRec := httptest.NewRecorder()
	h.configImport(replaceRec, replaceReq)
	if replaceRec.Code != http.StatusOK {
		t.Fatalf("replace status=%d body=%s", replaceRec.Code, replaceRec.Body.String())
	}
	keys := h.Store.Keys()
	if len(keys) != 1 || keys[0] != "k9" {
		t.Fatalf("unexpected keys after replace: %#v", keys)
	}
	if got := len(h.Store.Accounts()); got != 0 {
		t.Fatalf("accounts after replace=%d want=0", got)
	}
}

func TestConfigImportMergePreservesStructuredAPIKeys(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"api_keys":[{"key":"k1","name":"primary","remark":"prod"}]
	}`)

	merge := map[string]any{
		"mode": "merge",
		"config": map[string]any{
			"api_keys": []any{
				map[string]any{"key": "k1", "name": "should-not-overwrite", "remark": "ignored"},
				map[string]any{"key": "k2", "name": "secondary", "remark": "staging"},
			},
		},
	}
	mergeBytes, _ := json.Marshal(merge)
	mergeReq := httptest.NewRequest(http.MethodPost, "/admin/config/import?mode=merge", bytes.NewReader(mergeBytes))
	mergeRec := httptest.NewRecorder()
	h.configImport(mergeRec, mergeReq)
	if mergeRec.Code != http.StatusOK {
		t.Fatalf("merge status=%d body=%s", mergeRec.Code, mergeRec.Body.String())
	}

	snap := h.Store.Snapshot()
	if len(snap.APIKeys) != 2 {
		t.Fatalf("unexpected api keys after structured merge: %#v", snap.APIKeys)
	}
	if snap.APIKeys[0].Name != "primary" || snap.APIKeys[0].Remark != "prod" {
		t.Fatalf("existing structured metadata was overwritten: %#v", snap.APIKeys[0])
	}
	if snap.APIKeys[1].Name != "secondary" || snap.APIKeys[1].Remark != "staging" {
		t.Fatalf("new structured metadata was lost: %#v", snap.APIKeys[1])
	}
}

func TestConfigImportMergeUpgradesLegacyAPIKeys(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["legacy"],
		"accounts":[]
	}`)

	merge := map[string]any{
		"mode": "merge",
		"config": map[string]any{
			"api_keys": []any{
				map[string]any{"key": "legacy", "name": "primary", "remark": "prod"},
				map[string]any{"key": "new-key", "name": "secondary", "remark": "staging"},
			},
		},
	}
	mergeBytes, _ := json.Marshal(merge)
	mergeReq := httptest.NewRequest(http.MethodPost, "/admin/config/import?mode=merge", bytes.NewReader(mergeBytes))
	mergeRec := httptest.NewRecorder()
	h.configImport(mergeRec, mergeReq)
	if mergeRec.Code != http.StatusOK {
		t.Fatalf("merge status=%d body=%s", mergeRec.Code, mergeRec.Body.String())
	}

	snap := h.Store.Snapshot()
	if len(snap.Keys) != 2 || snap.Keys[0] != "legacy" || snap.Keys[1] != "new-key" {
		t.Fatalf("unexpected keys after legacy import merge: %#v", snap.Keys)
	}
	if len(snap.APIKeys) != 2 {
		t.Fatalf("unexpected api keys after legacy import merge: %#v", snap.APIKeys)
	}
	if snap.APIKeys[0].Name != "primary" || snap.APIKeys[0].Remark != "prod" {
		t.Fatalf("legacy key metadata was not upgraded: %#v", snap.APIKeys[0])
	}
	if snap.APIKeys[1].Name != "secondary" || snap.APIKeys[1].Remark != "staging" {
		t.Fatalf("new structured metadata was not preserved: %#v", snap.APIKeys[1])
	}
}

func TestBatchImportUpgradesLegacyAPIKeys(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["legacy"],
		"accounts":[]
	}`)

	payload := map[string]any{
		"keys": []any{"legacy", "new-key"},
		"api_keys": []any{
			map[string]any{"key": "legacy", "name": "primary", "remark": "prod"},
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/admin/import", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.batchImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	snap := h.Store.Snapshot()
	if len(snap.Keys) != 2 || snap.Keys[0] != "legacy" || snap.Keys[1] != "new-key" {
		t.Fatalf("unexpected keys after batch import: %#v", snap.Keys)
	}
	if len(snap.APIKeys) != 2 {
		t.Fatalf("unexpected api keys after batch import: %#v", snap.APIKeys)
	}
	if snap.APIKeys[0].Name != "primary" || snap.APIKeys[0].Remark != "prod" {
		t.Fatalf("legacy key metadata was not upgraded: %#v", snap.APIKeys[0])
	}
	if snap.APIKeys[1].Name != "" || snap.APIKeys[1].Remark != "" {
		t.Fatalf("new batch-imported key should stay metadata-free: %#v", snap.APIKeys[1])
	}
}

func TestConfigImportAppliesTokenRefreshInterval(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)

	replace := map[string]any{
		"mode": "replace",
		"config": map[string]any{
			"keys": []any{"k9"},
			"runtime": map[string]any{
				"token_refresh_interval_hours": 11,
			},
		},
	}
	replaceBytes, _ := json.Marshal(replace)
	replaceReq := httptest.NewRequest(http.MethodPost, "/admin/config/import?mode=replace", bytes.NewReader(replaceBytes))
	replaceRec := httptest.NewRecorder()
	h.configImport(replaceRec, replaceReq)
	if replaceRec.Code != http.StatusOK {
		t.Fatalf("replace status=%d body=%s", replaceRec.Code, replaceRec.Body.String())
	}
	if got := h.Store.RuntimeTokenRefreshIntervalHours(); got != 11 {
		t.Fatalf("token_refresh_interval_hours=%d want=11", got)
	}
}

func TestConfigImportRejectsInvalidRuntimeBounds(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{
		"mode": "replace",
		"config": map[string]any{
			"keys": []any{"k2"},
			"runtime": map[string]any{
				"account_max_inflight": 300,
			},
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/admin/config/import?mode=replace", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.configImport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("runtime.account_max_inflight")) {
		t.Fatalf("expected runtime bound detail, got %s", rec.Body.String())
	}
	keys := h.Store.Keys()
	if len(keys) != 1 || keys[0] != "k1" {
		t.Fatalf("store should remain unchanged, keys=%v", keys)
	}
}

func TestConfigImportRejectsMergedRuntimeConflict(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"runtime":{
			"account_max_inflight":8,
			"global_max_inflight":8
		}
	}`)
	payload := map[string]any{
		"mode": "merge",
		"config": map[string]any{
			"runtime": map[string]any{
				"account_max_inflight": 16,
			},
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/admin/config/import?mode=merge", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.configImport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("runtime.global_max_inflight")) {
		t.Fatalf("expected merged runtime validation detail, got %s", rec.Body.String())
	}
	snap := h.Store.Snapshot()
	if snap.Runtime.AccountMaxInflight != 8 || snap.Runtime.GlobalMaxInflight != 8 {
		t.Fatalf("runtime should remain unchanged, runtime=%+v", snap.Runtime)
	}
}

func TestConfigImportMergeDedupesMobileAliases(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"accounts":[{"mobile":"+8613800138000","password":"p1"}]
	}`)

	merge := map[string]any{
		"mode": "merge",
		"config": map[string]any{
			"accounts": []any{
				map[string]any{"mobile": "13800138000", "password": "p2"},
			},
		},
	}
	b, _ := json.Marshal(merge)
	req := httptest.NewRequest(http.MethodPost, "/admin/config/import?mode=merge", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.configImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := len(h.Store.Accounts()); got != 1 {
		t.Fatalf("expected merge dedupe by canonical mobile, got=%d", got)
	}
}

func TestUpdateConfigDedupesMobileAliases(t *testing.T) {
	h := newAdminTestHandler(t, `{
		"keys":["k1"],
		"accounts":[{"mobile":"+8613800138000","password":"old"}]
	}`)

	reqBody := map[string]any{
		"accounts": []any{
			map[string]any{"mobile": "+8613800138000"},
			map[string]any{"mobile": "13800138000"},
		},
	}
	b, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/admin/config", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	accounts := h.Store.Accounts()
	if len(accounts) != 1 {
		t.Fatalf("expected update dedupe by canonical mobile, got=%d", len(accounts))
	}
	if accounts[0].Identifier() != "+8613800138000" {
		t.Fatalf("unexpected identifier: %q", accounts[0].Identifier())
	}
}
