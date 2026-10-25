package config

import (
	"encoding/json"
	"testing"
)

// The hand-written codec decoded a section straight into its struct, and
// encoding/json matches object keys against struct tags case-insensitively. A
// config file that spells a declared field with different capitalisation must
// therefore still load — and must survive a save, or the field would be erased
// from the file on the next write with nothing to notice it by.

func TestCaseVariantSettingFieldNamesLoad(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"input_file":{"Enabled":true},
		"thinking_injection":{"Prompt":"CASE-PROMPT"},
		"bottom_format_injection":{"Enabled":false}
	}`)

	store := LoadStore()
	if !store.CurrentInputFileEnabled() {
		t.Fatal("a field spelled `Enabled` must load, as the struct decode accepted it")
	}
	if got := store.ThinkingInjectionPrompt(); got != "CASE-PROMPT" {
		t.Fatalf("thinking_injection.Prompt = %q, want CASE-PROMPT", got)
	}
	// The default of this switch is true, so a case-variant false is the case
	// that tells a loaded value from a defaulted one.
	if store.BottomFormatInjectionEnabled() {
		t.Fatal("a field spelled `Enabled` must load its explicit false")
	}

	// A save writes the declared spelling, so the section survives the round
	// trip instead of being erased.
	encoded, err := json.Marshal(store.Snapshot())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(encoded, &onDisk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	section, ok := onDisk["input_file"].(map[string]any)
	if !ok {
		t.Fatalf("input_file must survive a save, got %s", encoded)
	}
	if section["enabled"] != true {
		t.Fatalf("input_file must be written under its declared field name, got %#v", section)
	}
}

func TestCaseVariantAPIKeyFieldNamesLoad(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"api_keys":[{"Key":"casekey","Name":"n","Remark":"r","Force_Disable_Tools":true}]
	}`)

	store := LoadStore()
	if !store.HasAPIKey("casekey") {
		t.Fatal("a key spelled `Key` must load as a managed key, not as an unknown caller")
	}
	snap := store.Snapshot()
	if len(snap.APIKeys) != 1 {
		t.Fatalf("expected one api key, got %#v", snap.APIKeys)
	}
	if snap.APIKeys[0].Name != "n" || snap.APIKeys[0].Remark != "r" {
		t.Fatalf("metadata spelled with capitals must load: %#v", snap.APIKeys[0])
	}
	if !store.APIKeyFlag("casekey", CredentialFlagForceDisableTools) {
		t.Fatalf("a flag spelled `Force_Disable_Tools` must load: %#v", snap.APIKeys[0])
	}
}

// A field no declared spelling resolves is still dropped — that is what the
// struct decode did — but the value must not come back on the next save.
func TestUnknownSettingFieldIsDropped(t *testing.T) {
	t.Setenv("DS2API_CONFIG_JSON", `{
		"keys":["k1"],
		"input_file":{"enabled":true,"not_a_field":7}
	}`)

	encoded, err := json.Marshal(LoadStore().Snapshot())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(encoded, &onDisk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	section, _ := onDisk["input_file"].(map[string]any)
	if _, present := section["not_a_field"]; present {
		t.Fatalf("an undeclared field must not be carried into the file: %s", encoded)
	}
	if section["enabled"] != true {
		t.Fatalf("the declared field beside it must still load: %s", encoded)
	}
}
