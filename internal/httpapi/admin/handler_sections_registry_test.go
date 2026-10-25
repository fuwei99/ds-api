package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ds2api/internal/config"
)

// The section registry exists so that a new config section needs a declaration
// plus the code that reads it, and nothing else: no codec case, no validation
// line, no Clone line, no admin handler, no web UI component. These tests
// exercise the whole path for a section registered at test time, which is the
// same path a real one takes.

const (
	testSectionRegistryKey        = "test_section_registry"
	testSectionRegistryBool       = "enabled"
	testSectionRegistryLimitField = "limit"
)

var (
	testSectionRegistryEnabled    bool
	testSectionRegistryLimitValue int
)

func init() {
	config.RegisterSection(config.SectionSpec{
		Key: testSectionRegistryKey,
		Form: &config.SectionForm{
			TitleKey:       "settings.testSectionTitle",
			DescriptionKey: "settings.testSectionDescription",
			Fields: []config.SectionField{
				config.SectionBool(testSectionRegistryKey+"."+testSectionRegistryBool, "settings.testSectionEnabled",
					func(*config.Store) bool { return testSectionRegistryEnabled },
					func(_ *config.Config, v bool) { testSectionRegistryEnabled = v }),
				config.SectionInt(testSectionRegistryKey+"."+testSectionRegistryLimitField, "settings.testSectionLimit", 1, 100,
					func(*config.Store) int { return testSectionRegistryLimitValue },
					func(_ *config.Config, v int) { testSectionRegistryLimitValue = v }),
			},
		},
		Decode: func(_ *config.Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &struct {
				Enabled *bool `json:"enabled"`
				Limit   *int  `json:"limit"`
			}{Enabled: &testSectionRegistryEnabled, Limit: &testSectionRegistryLimitValue})
		},
		Encode: func(config.Config) (any, bool) {
			return map[string]any{
				testSectionRegistryBool:       testSectionRegistryEnabled,
				testSectionRegistryLimitField: testSectionRegistryLimitValue,
			}, true
		},
	})
}

func TestGetSettingsReportsRegisteredSections(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	rec := httptest.NewRecorder()
	h.getSettings(rec, httptest.NewRequest(http.MethodGet, "/admin/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	// The section is reported under its own config key, with one entry per
	// declared field.
	section, ok := body[testSectionRegistryKey].(map[string]any)
	if !ok {
		t.Fatalf("registered section missing from the settings response: %v", body)
	}
	if _, ok := section[testSectionRegistryBool]; !ok {
		t.Fatalf("declared field missing: %#v", section)
	}

	// The web UI renders one control per declared field, so the spec has to
	// travel with the response — otherwise a new section would need a component.
	specs, _ := body["section_specs"].([]any)
	var found map[string]any
	for _, raw := range specs {
		spec, _ := raw.(map[string]any)
		if spec["key"] == testSectionRegistryKey {
			found = spec
			break
		}
	}
	if found == nil {
		t.Fatalf("section_specs must describe the registered section, body=%v", body)
	}
	if found["title_key"] != "settings.testSectionTitle" {
		t.Fatalf("spec title key missing: %#v", found)
	}
	fields, _ := found["fields"].([]any)
	if len(fields) != 2 {
		t.Fatalf("expected two declared fields, got %#v", fields)
	}
	first, _ := fields[0].(map[string]any)
	if first["name"] != testSectionRegistryBool || first["kind"] != "bool" {
		t.Fatalf("field spec incomplete: %#v", first)
	}
}

// The card order is declared (SectionForm.Order), not inherited from the file a
// section lives in: otherwise renaming a file would rearrange the settings page.
func TestSectionSpecPayloadFollowsDeclaredOrder(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	rec := httptest.NewRecorder()
	h.getSettings(rec, httptest.NewRequest(http.MethodGet, "/admin/settings", nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	var order []string
	for _, raw := range body["section_specs"].([]any) {
		spec, _ := raw.(map[string]any)
		key, _ := spec["key"].(string)
		order = append(order, key)
		// The web UI places setting cards and section cards in one sequence, so
		// each card has to carry the order it was declared with.
		if declared, ok := config.LookupSection(key); ok && declared.Form != nil {
			if spec["order"] != float64(declared.Form.Order) {
				t.Fatalf("section %q reports order %v, want %d", key, spec["order"], declared.Form.Order)
			}
		}
	}
	want := []string{"admin", "runtime", "auto_delete"}
	position := map[string]int{}
	for i, key := range order {
		position[key] = i
	}
	for i := 1; i < len(want); i++ {
		if position[want[i-1]] > position[want[i]] {
			t.Fatalf("section cards out of declared order: %v", order)
		}
	}
}

func TestUpdateSettingsWritesRegisteredSections(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{
		testSectionRegistryKey: map[string]any{
			testSectionRegistryBool:       true,
			testSectionRegistryLimitField: 42,
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.updateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !testSectionRegistryEnabled || testSectionRegistryLimitValue != 42 {
		t.Fatalf("section not applied: enabled=%v limit=%d", testSectionRegistryEnabled, testSectionRegistryLimitValue)
	}

	// A partial update leaves the fields it does not mention alone.
	payload = map[string]any{testSectionRegistryKey: map[string]any{testSectionRegistryBool: false}}
	b, _ = json.Marshal(payload)
	rec = httptest.NewRecorder()
	h.updateSettings(rec, httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if testSectionRegistryEnabled || testSectionRegistryLimitValue != 42 {
		t.Fatalf("partial update touched an unmentioned field: enabled=%v limit=%d", testSectionRegistryEnabled, testSectionRegistryLimitValue)
	}
}

func TestUpdateSettingsRejectsBadSectionValues(t *testing.T) {
	h := newAdminTestHandler(t, `{"keys":["k1"]}`)
	payload := map[string]any{testSectionRegistryKey: map[string]any{testSectionRegistryLimitField: 0}}
	b, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	h.updateSettings(rec, httptest.NewRequest(http.MethodPut, "/admin/settings", bytes.NewReader(b)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(testSectionRegistryKey+"."+testSectionRegistryLimitField)) {
		t.Fatalf("expected the field to be named in the error, got %s", rec.Body.String())
	}
}
