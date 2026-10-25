package compat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ds2api/internal/config"
)

// The setting registry names web UI locale keys rather than carrying display
// text, so translation stays in the locale files. That only works if the keys
// exist: a typo would render a blank label on the settings page with nothing
// failing. This test is the contract between internal/config/settings.go and
// webui/src/locales, in the same spirit as the content-policy fixtures: both
// sides are read here and must agree.
func TestSettingLocaleKeysExist(t *testing.T) {
	locales := map[string]map[string]string{}
	for _, lang := range []string{"zh", "en"} {
		locales[lang] = loadLocaleSection(t, lang, "settings")
	}

	for _, spec := range config.SettingSpecs() {
		if spec.Hidden {
			continue
		}
		requireLocaleKey(t, locales, spec.Key, spec.TitleKey)
		requireLocaleKey(t, locales, spec.Key, spec.DescriptionKey)
		for _, f := range spec.Fields {
			requireLocaleKey(t, locales, spec.Key+"."+f.Name, f.LabelKey)
			requireLocaleKey(t, locales, spec.Key+"."+f.Name, f.HelpKey)
		}
	}
	// The settings page renders a card per declaration, so a card's keys are
	// part of the same contract as a setting's.
	for _, card := range config.SettingCards() {
		requireLocaleKey(t, locales, card.Key, card.TitleKey)
		requireLocaleKey(t, locales, card.Key, card.DescriptionKey)
	}
}

// A visible setting is rendered entirely from its spec, so a field without a
// label key would render an empty row. Registration already rejects that; this
// pins it for the whole registry rather than per entry.
func TestVisibleSettingsDeclareLocaleKeys(t *testing.T) {
	for _, spec := range config.SettingSpecs() {
		if spec.Hidden {
			continue
		}
		if strings.TrimSpace(spec.TitleKey) == "" {
			t.Fatalf("visible setting %q declares no title key", spec.Key)
		}
		for _, f := range spec.Fields {
			if strings.TrimSpace(f.LabelKey) == "" {
				t.Fatalf("visible setting %q field %q declares no label key", spec.Key, f.Name)
			}
		}
	}
}

func requireLocaleKey(t *testing.T, locales map[string]map[string]string, owner, key string) {
	t.Helper()
	if strings.TrimSpace(key) == "" {
		return
	}
	if !strings.HasPrefix(key, "settings.") {
		t.Fatalf("%s references %q, which is outside the settings locale section", owner, key)
	}
	short := strings.TrimPrefix(key, "settings.")
	for lang, section := range locales {
		if _, ok := section[short]; !ok {
			t.Fatalf("%s references %q, which is missing from the %s locale", owner, key, lang)
		}
	}
}

func loadLocaleSection(t *testing.T, lang, section string) map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "webui", "src", "locales", lang+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("%s is not a JSON object: %v", path, err)
	}
	rawSection, ok := all[section]
	if !ok {
		t.Fatalf("%s has no %q section", path, section)
	}
	var values map[string]any
	if err := json.Unmarshal(rawSection, &values); err != nil {
		t.Fatalf("%s.%s is not a JSON object: %v", path, section, err)
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		// A key the registry names must resolve to display text, so a nested
		// object here would render as "[object Object]" on the settings page.
		text, ok := value.(string)
		if !ok {
			t.Fatalf("%s.%s.%s must be a string, got %T", path, section, key, value)
		}
		out[key] = text
	}
	return out
}
