package compat

import (
	"strings"
	"testing"

	"ds2api/internal/config"
)

// The credential-flag registry names web UI locale keys rather than carrying
// display text, so translation stays in the locale files. That only works if the
// keys exist: a typo would render a blank toggle on the key modal and a blank
// badge in the key list, with nothing failing. This test is the contract between
// internal/config/credential_flags.go and webui/src/locales, in the same spirit
// as the setting registry's locale test and the content-policy fixtures.
func TestCredentialFlagLocaleKeysExist(t *testing.T) {
	locales := map[string]map[string]string{}
	for _, lang := range []string{"zh", "en"} {
		locales[lang] = loadLocaleSection(t, lang, "accountManager")
	}

	for _, spec := range config.CredentialFlagSpecs() {
		requireCredentialLocaleKey(t, locales, spec.Name, spec.LabelKey)
		requireCredentialLocaleKey(t, locales, spec.Name, spec.HelpKey)
		requireCredentialLocaleKey(t, locales, spec.Name, spec.BadgeKey)
	}
}

// A flag with no label key renders an empty control, and one with no badge key
// is the deliberate "no badge" case, so only the label is required. Registration
// already rejects a missing label; this pins it for the whole registry rather
// than per entry.
func TestCredentialFlagsDeclareLabelKeys(t *testing.T) {
	for _, spec := range config.CredentialFlagSpecs() {
		if strings.TrimSpace(spec.LabelKey) == "" {
			t.Fatalf("credential flag %q declares no label key", spec.Name)
		}
	}
}

func requireCredentialLocaleKey(t *testing.T, locales map[string]map[string]string, owner, key string) {
	t.Helper()
	if strings.TrimSpace(key) == "" {
		return
	}
	if !strings.HasPrefix(key, "accountManager.") {
		t.Fatalf("credential flag %s references %q, which is outside the accountManager locale section", owner, key)
	}
	short := strings.TrimPrefix(key, "accountManager.")
	for lang, section := range locales {
		if _, ok := section[short]; !ok {
			t.Fatalf("credential flag %s references %q, which is missing from the %s locale", owner, key, lang)
		}
	}
}
