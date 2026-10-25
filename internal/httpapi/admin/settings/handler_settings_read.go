package settings

import (
	"net/http"
	"sort"

	authn "ds2api/internal/auth"
	"ds2api/internal/config"
	"ds2api/internal/promptcompat"
)

func (h *Handler) getSettings(w http.ResponseWriter, _ *http.Request) {
	snap := h.Store.Snapshot()
	needsSync := config.IsVercel() && snap.VercelSyncHash != "" && snap.VercelSyncHash != h.computeSyncHash()
	body := map[string]any{
		// Every admin endpoint answers with this, and API.md / API.en.md list it
		// as part of the GET /admin/settings response.
		"success":           true,
		"model_aliases":     snap.ModelAliases,
		"env_backed":        h.Store.IsEnvBacked(),
		"needs_vercel_sync": needsSync,
	}
	// Registered sections (internal/config/sections.go) are reported generically,
	// one entry per declaration, in the same shape a hand-written section uses:
	// the registry reads every declared field through its own reader, so a new
	// section — or a new field on an existing one — needs no change here.
	for _, spec := range config.SectionSpecs() {
		if spec.Form == nil {
			continue
		}
		body[spec.Key] = h.Store.SectionValues(spec.Key)
	}
	// One value no section can report: whether the admin key is still the
	// default is a question for the auth package, not for the config store.
	setSectionField(body, "admin", "default_password_warning", authn.UsingDefaultAdminKey(h.Store))
	// Registered settings (internal/config/settings.go) are reported the same way.
	for _, spec := range config.SettingSpecs() {
		body[spec.Key] = h.Store.SettingValues(spec.Key)
	}
	// A setting whose text field falls back to a built-in prompt ships that text as
	// UI metadata (the web UI shows it as a placeholder or a prefill), so the user
	// can see what they are overriding. The text belongs to internal/promptcompat,
	// which is asked per declared field; nothing here names a setting, so a new
	// setting that ships a built-in prompt needs no change in this handler.
	for _, spec := range config.SettingSpecs() {
		for _, f := range spec.Fields {
			if text, ok := promptcompat.SettingDefaultPrompt(spec.Key, f.Name); ok {
				setSectionField(body, spec.Key, "default_prompt", text)
			}
		}
	}
	body["setting_specs"] = settingSpecPayload()
	body["setting_cards"] = settingCardPayload()
	body["section_specs"] = sectionSpecPayload()
	writeJSON(w, http.StatusOK, body)
}

func setSectionField(body map[string]any, key, field string, value any) {
	section, ok := body[key].(map[string]any)
	if !ok {
		section = map[string]any{}
		body[key] = section
	}
	section[field] = value
}

// settingSpecPayload describes the registered settings for the web UI, which
// renders one control per declared field. Labels, help texts and the section
// card all come from the registry in internal/config/settings.go, so a new
// setting needs no web UI component and no change here.
func settingSpecPayload() []map[string]any {
	specs := config.SettingSpecs()
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		if spec.Hidden {
			continue
		}
		fields := make([]map[string]any, 0, len(spec.Fields))
		for _, f := range spec.Fields {
			fields = append(fields, map[string]any{
				"name":            f.Name,
				"kind":            string(f.Kind),
				"default":         f.Default,
				"min":             f.Min,
				"max":             f.Max,
				"multiline":       f.Multiline,
				"prefill_default": f.PrefillDefault,
				"label_key":       f.LabelKey,
				"help_key":        f.HelpKey,
			})
		}
		out = append(out, map[string]any{
			"key":             spec.Key,
			"card":            settingCardKey(spec.Key),
			"title_key":       spec.TitleKey,
			"description_key": spec.DescriptionKey,
			"fields":          fields,
		})
	}
	return out
}

// settingCardKey reports the card a setting renders in, empty when no card
// names it: the web UI then gives the setting a card of its own, so a setting
// registered without a card still reaches the page.
func settingCardKey(key string) string {
	card, ok := config.SettingCardOf(key)
	if !ok {
		return ""
	}
	return card.Key
}

// settingCardPayload describes the settings page's cards: which settings render
// together, in which order, under which title. The web UI renders one card per
// entry, so moving a setting to another card — or to a card of its own — is a
// registry edit rather than a component edit. A card whose every setting is
// hidden is left out: it would render an empty card.
func settingCardPayload() []map[string]any {
	out := make([]map[string]any, 0)
	for _, card := range config.SettingCards() {
		if !cardHasVisibleSetting(card) {
			continue
		}
		out = append(out, map[string]any{
			"key":             card.Key,
			"title_key":       card.TitleKey,
			"description_key": card.DescriptionKey,
			"order":           card.Order,
		})
	}
	return out
}

func cardHasVisibleSetting(card config.SettingCardSpec) bool {
	for _, key := range card.Settings {
		if spec, ok := config.LookupSetting(key); ok && !spec.Hidden {
			return true
		}
	}
	return false
}

// sectionSpecPayload describes the registered sections that have a settings-page
// form, in the same spirit: the web UI renders one control per declared field,
// including nested groups and enums, so a new section needs no component. A
// bespoke section (its card is its own component) is still described, because
// its fields travel through the same read and write path.
func sectionSpecPayload() []map[string]any {
	specs := config.SectionSpecs()
	// The cards are ordered by the declaration's Order, with the declaration
	// order breaking ties, so the page's layout is declared rather than inherited
	// from the file names the sections live in.
	sort.SliceStable(specs, func(i, j int) bool {
		return sectionOrder(specs[i]) < sectionOrder(specs[j])
	})
	out := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		if spec.Form == nil {
			continue
		}
		out = append(out, map[string]any{
			"key":             spec.Key,
			"title_key":       spec.Form.TitleKey,
			"description_key": spec.Form.DescriptionKey,
			"bespoke":         spec.Form.Bespoke,
			"order":           spec.Form.Order,
			"fields":          sectionFieldPayload(spec.Form.Fields),
		})
	}
	return out
}

func sectionOrder(spec config.SectionSpec) int {
	if spec.Form == nil {
		return 0
	}
	return spec.Form.Order
}

func sectionFieldPayload(fields []config.SectionField) []map[string]any {
	out := make([]map[string]any, 0, len(fields))
	for _, f := range fields {
		field := map[string]any{
			"name":      f.Name,
			"kind":      string(f.Kind),
			"min":       f.Min,
			"max":       f.Max,
			"multiline": f.Multiline,
			"label_key": f.LabelKey,
			"help_key":  f.HelpKey,
		}
		if len(f.Options) > 0 {
			options := make([]map[string]any, 0, len(f.Options))
			for _, opt := range f.Options {
				options = append(options, map[string]any{
					"value":           opt.Value,
					"label_key":       opt.LabelKey,
					"description_key": opt.DescriptionKey,
					"warning_key":     opt.WarningKey,
				})
			}
			field["options"] = options
		}
		if len(f.Fields) > 0 {
			field["fields"] = sectionFieldPayload(f.Fields)
		}
		out = append(out, field)
	}
	return out
}
