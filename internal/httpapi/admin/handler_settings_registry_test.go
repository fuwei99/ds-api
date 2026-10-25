package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ds2api/internal/account"
	"ds2api/internal/config"
)

// The setting registry exists so that a new setting needs a registry entry plus
// the code that reads it, and nothing else: no codec case, no accessor, no admin
// handler, no web UI component. These tests exercise the whole path for a
// setting registered at test time, which is the same path a real one takes.

const (
	testSettingKey       = "test_settings_registered"
	testSettingField     = "enabled"
	testSettingIntField  = "limit"
	testSettingTextfield = "label"
)

func init() {
	config.RegisterSetting(config.SettingSpec{
		Key:            testSettingKey,
		TitleKey:       "settings.testTitle",
		DescriptionKey: "settings.testDescription",
		Fields: []config.FieldSpec{
			{Name: testSettingField, Kind: config.FieldBool, Default: false,
				LabelKey: "settings.testEnabled"},
			{Name: testSettingIntField, Kind: config.FieldInt, Default: 5, Min: 1, Max: 100,
				LabelKey: "settings.testLimit"},
			{Name: testSettingTextfield, Kind: config.FieldString, Default: "", Trim: true, Multiline: true,
				LabelKey: "settings.testLabel"},
		},
	})
}

func TestGetSettingsReportsRegisteredSettings(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	rec := httptest.NewRecorder()
	h.getSettings(rec, httptest.NewRequest(http.MethodGet, "/admin/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	// The section is reported with the effective value of every declared field,
	// so an untouched setting shows what it actually behaves as.
	section, ok := body[testSettingKey].(map[string]any)
	if !ok {
		t.Fatalf("registered setting missing from the settings response: %v", body)
	}
	if section[testSettingField] != false {
		t.Fatalf("expected the declared default (false), got %#v", section[testSettingField])
	}
	if section[testSettingIntField] != float64(5) {
		t.Fatalf("expected the declared int default (5), got %#v", section[testSettingIntField])
	}

	// The web UI renders one control per declared field, so the spec has to
	// travel with the response — otherwise a new setting would need a component.
	specs, _ := body["setting_specs"].([]any)
	var found map[string]any
	for _, raw := range specs {
		spec, _ := raw.(map[string]any)
		if spec["key"] == testSettingKey {
			found = spec
			break
		}
	}
	if found == nil {
		t.Fatalf("setting_specs must describe the registered setting, body=%v", body)
	}
	if found["title_key"] != "settings.testTitle" {
		t.Fatalf("spec title key missing: %#v", found)
	}
	fields, _ := found["fields"].([]any)
	if len(fields) != 3 {
		t.Fatalf("expected three declared fields, got %#v", fields)
	}
	first, _ := fields[0].(map[string]any)
	if first["name"] != testSettingField || first["kind"] != "bool" || first["label_key"] != "settings.testEnabled" {
		t.Fatalf("field spec incomplete: %#v", first)
	}
}

// A hidden setting is registered and round-trips, but must not reach the web UI:
// otherwise registering a config-file-only setting would put a control on the
// settings page.
func TestSettingSpecPayloadOmitsHiddenSettings(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	rec := httptest.NewRecorder()
	h.getSettings(rec, httptest.NewRequest(http.MethodGet, "/admin/settings", nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	hidden := []string{"auto_route_vision", "expert_text_file_inline", "expert_prompt_segment"}
	specs, _ := body["setting_specs"].([]any)
	for _, raw := range specs {
		spec, _ := raw.(map[string]any)
		for _, key := range hidden {
			if spec["key"] == key {
				t.Fatalf("hidden setting %v must not be offered to the web UI", key)
			}
		}
	}
	// Each is still reported as a value, so a client can read it.
	for _, key := range hidden {
		if _, ok := body[key].(map[string]any); !ok {
			t.Fatalf("hidden setting %q must still be reported, body=%v", key, body)
		}
	}
}

func TestUpdateSettingsWritesRegisteredSettings(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{testSettingKey: map[string]any{
		testSettingField:     true,
		testSettingIntField:  42,
		testSettingTextfield: "  hello  ",
	}}
	b, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	h.updateSettings(rec, httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !h.Store.SettingBool(testSettingKey, testSettingField) {
		t.Fatal("the setting must be readable through SettingBool after the update")
	}
	if got := h.Store.SettingInt(testSettingKey, testSettingIntField); got != 42 {
		t.Fatalf("limit=%d want 42", got)
	}
	if got := h.Store.SettingString(testSettingKey, testSettingTextfield); got != "hello" {
		t.Fatalf("label=%q want trimmed hello", got)
	}
	stored := h.Store.Snapshot().Settings[testSettingKey]
	if stored[testSettingField] != true || stored[testSettingIntField] != 42 {
		t.Fatalf("the setting must be persisted in the config snapshot: %#v", stored)
	}
}

// A value outside the declared range is rejected with a 400, and the same
// bounds apply whether the value arrives from the settings API or from a file.
func TestUpdateSettingsRejectsOutOfRangeSetting(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{testSettingKey: map[string]any{testSettingIntField: 5000}}
	b, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	h.updateSettings(rec, httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(testSettingKey+"."+testSettingIntField)) {
		t.Fatalf("the error must name the field, got %s", rec.Body.String())
	}
}

func TestUpdateSettingsPreservesSettingsNotProvided(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"],"`+testSettingKey+`":{"enabled":true,"limit":9}}`)
	if !h.Store.SettingBool(testSettingKey, testSettingField) {
		t.Fatal("the setting must load from the config JSON")
	}

	// A partial update that does not mention the setting must not reset it.
	payload := map[string]any{"responses": map[string]any{"store_ttl_seconds": 600}}
	b, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	h.updateSettings(rec, httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !h.Store.SettingBool(testSettingKey, testSettingField) {
		t.Fatal("a partial update must leave an unmentioned setting untouched")
	}
	if got := h.Store.SettingInt(testSettingKey, testSettingIntField); got != 9 {
		t.Fatalf("limit=%d want 9", got)
	}
}

// The setting must reach the config file in the same section shape a hand-written
// setting uses, and come back on the next load. This needs a file-backed store:
// the env-backed store the other admin tests use deliberately skips writing.
func TestUpdateSettingsPersistsSettingToConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"keys":["k1"]}`), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	t.Setenv("DS2API_CONFIG_JSON", "")
	t.Setenv("DS2API_CONFIG_PATH", path)

	store := config.LoadStore()
	h := &Handler{Store: store, Pool: account.NewPool(store)}

	payload := map[string]any{testSettingKey: map[string]any{testSettingField: true}}
	b, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	h.updateSettings(rec, httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("config file is not valid JSON: %v\n%s", err, raw)
	}
	section, ok := onDisk[testSettingKey].(map[string]any)
	if !ok || section[testSettingField] != true {
		t.Fatalf("setting not written to the config file in section shape: %s", raw)
	}

	if !config.LoadStore().SettingBool(testSettingKey, testSettingField) {
		t.Fatal("the setting must survive a config reload")
	}
}

// TestSettingSpecPayloadCoversTheVisibleRegistry pins which settings the
// settings page offers. The web UI renders exactly this list, so a setting that
// silently drops out of it (or a hidden one that leaks in) is a page regression
// rather than a backend one.
func TestSettingSpecPayloadCoversTheVisibleRegistry(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	rec := httptest.NewRecorder()
	h.getSettings(rec, httptest.NewRequest(http.MethodGet, "/admin/settings", nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	var got []string
	for _, raw := range body["setting_specs"].([]any) {
		spec, _ := raw.(map[string]any)
		key, _ := spec["key"].(string)
		got = append(got, key)
	}
	want := []string{
		"responses", "embeddings", "thinking_injection",
		"bottom_format_injection", "input_file",
	}
	// The registry also carries settings registered by other tests in this
	// package, so assert on the production keys' presence and order instead of
	// an exact list.
	var filtered []string
	for _, key := range got {
		for _, w := range want {
			if key == w {
				filtered = append(filtered, key)
			}
		}
	}
	if strings.Join(filtered, ",") != strings.Join(want, ",") {
		t.Fatalf("visible settings = %v, want %v (all specs: %v)", filtered, want, got)
	}
}

// The settings page renders cards, so the response has to say which settings
// share one and where each card sits. That is what lets a setting move to
// another card — or a new card appear — without a web UI component change.
func TestGetSettingsReportsSettingCards(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	rec := httptest.NewRecorder()
	h.getSettings(rec, httptest.NewRequest(http.MethodGet, "/admin/settings", nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	byKey := map[string]map[string]any{}
	for _, raw := range body["setting_cards"].([]any) {
		card, _ := raw.(map[string]any)
		key, _ := card["key"].(string)
		byKey[key] = card
	}
	behavior, ok := byKey["behavior"]
	if !ok {
		t.Fatalf("the behavior card must be reported, body=%v", body["setting_cards"])
	}
	if behavior["title_key"] != "settings.behaviorTitle" {
		t.Fatalf("card title key missing: %#v", behavior)
	}
	if behavior["order"] != float64(3) {
		t.Fatalf("card order must travel with the declaration, got %#v", behavior["order"])
	}

	// Each setting names the card it renders in, so the web UI can group them
	// without knowing any setting by name.
	cardOf := map[string]string{}
	for _, raw := range body["setting_specs"].([]any) {
		spec, _ := raw.(map[string]any)
		key, _ := spec["key"].(string)
		value, _ := spec["card"].(string)
		cardOf[key] = value
	}
	if cardOf["thinking_injection"] != "behavior" || cardOf["bottom_format_injection"] != "behavior" {
		t.Fatalf("both injection settings belong to the behavior card: %v", cardOf)
	}
	if cardOf["input_file"] != "input_file" {
		t.Fatalf("input_file must name its own card: %v", cardOf)
	}
	// A setting no card names reports none — it then renders in a card of its
	// own, which is what keeps a new setting one registry entry.
	if got, ok := cardOf[testSettingKey]; !ok || got != "" {
		t.Fatalf("an ungrouped setting must report no card, got %q (present=%v)", got, ok)
	}
}
