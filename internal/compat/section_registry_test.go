package compat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ds2api/internal/config"
)

// The section registry (internal/config/sections.go) names web UI locale keys
// rather than carrying display text, and the web UI renders a section's card
// straight from those keys. A typo would render a blank label with nothing
// failing, so this test is the contract between the registry and
// webui/src/locales — in the same spirit as the setting registry's check.
func TestSectionLocaleKeysExist(t *testing.T) {
	locales := map[string]map[string]string{}
	for _, lang := range []string{"zh", "en"} {
		locales[lang] = loadLocaleSection(t, lang, "settings")
	}

	for _, spec := range config.SectionSpecs() {
		if spec.Form == nil {
			continue
		}
		requireLocaleKey(t, locales, spec.Key, spec.Form.TitleKey)
		requireLocaleKey(t, locales, spec.Key, spec.Form.DescriptionKey)
		checkSectionFieldLocaleKeys(t, locales, spec.Key, spec.Form.Fields)
	}
}

func checkSectionFieldLocaleKeys(t *testing.T, locales map[string]map[string]string, owner string, fields []config.SectionField) {
	t.Helper()
	for _, f := range fields {
		label := owner + "." + f.Name
		requireLocaleKey(t, locales, label, f.LabelKey)
		requireLocaleKey(t, locales, label, f.HelpKey)
		for _, opt := range f.Options {
			requireLocaleKey(t, locales, label+"="+opt.Value, opt.LabelKey)
			requireLocaleKey(t, locales, label+"="+opt.Value, opt.DescriptionKey)
			requireLocaleKey(t, locales, label+"="+opt.Value, opt.WarningKey)
		}
		checkSectionFieldLocaleKeys(t, locales, label, f.Fields)
	}
}

// The web UI renders a section card from its declaration, so a component that
// names a section key by hand is a section that will not follow its own
// registry entry any more. A section whose declaration marks it bespoke is
// allowed its own component — that is what bespoke means.
func TestNonBespokeSectionsHaveNoHandWrittenCard(t *testing.T) {
	dir := filepath.Join("..", "..", "webui", "src", "features", "settings")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var sources []struct {
		path string
		body string
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsx") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		sources = append(sources, struct {
			path string
			body string
		}{path, string(raw)})
	}
	if len(sources) == 0 {
		t.Fatal("no web UI settings sources found; the check would pass vacuously")
	}

	for _, spec := range config.SectionSpecs() {
		if spec.Form == nil || spec.Form.Bespoke {
			continue
		}
		for _, src := range sources {
			for _, needle := range []string{"form." + spec.Key, "form?." + spec.Key, `form["` + spec.Key + `"]`} {
				if strings.Contains(src.body, needle) {
					t.Errorf("%s reads %q by hand, so section %q would no longer follow its registry entry; render it from section_specs or mark the section bespoke",
						src.path, needle, spec.Key)
				}
			}
		}
	}
}

// The codec dispatches on the two registries. A hand-written case for a
// registered section would shadow its declaration — the exact drift this
// registry exists to remove — so the codec must not name one.
func TestCodecHasNoHandWrittenSectionCases(t *testing.T) {
	path := filepath.Join("..", "config", "codec.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	for _, key := range config.SectionKeys() {
		for _, needle := range []string{`case "` + key + `":`, `m["` + key + `"] =`} {
			if strings.Contains(body, needle) {
				t.Errorf("%s carries %q for the registered section %q; declare the section in sections.go instead", path, needle, key)
			}
		}
	}
	for _, key := range config.SettingKeys() {
		if strings.Contains(body, `case "`+key+`":`) {
			t.Errorf("%s carries a hand-written case for the registered setting %q", path, key)
		}
	}
}
