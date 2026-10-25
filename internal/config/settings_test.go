package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// withSetting builds a Config carrying one registered-setting field, which is
// the shape the codec and the validators see after a config file or an admin
// update has set it.
func withSetting(key, field string, value any) Config {
	cfg := Config{}
	cfg.SetSetting(key, field, value)
	return cfg
}

func TestRegisterSettingRejectsBadSpecs(t *testing.T) {
	cases := []struct {
		name string
		spec SettingSpec
	}{
		{"empty key", SettingSpec{Key: "   ", Hidden: true, Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false}}}},
		// A registered section owns the key, so the setting entry would never be
		// read: the guard turns that into a startup failure.
		{"section key", SettingSpec{Key: "runtime", Hidden: true, Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false}}}},
		{"legacy section key", SettingSpec{Key: "current_input_file", Hidden: true, Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false}}}},
		{"duplicate", SettingSpec{Key: "test_duplicate_setting", Hidden: true, Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false}}}},
		{"no fields", SettingSpec{Key: "test_no_fields", Hidden: true}},
		{"empty field name", SettingSpec{Key: "test_empty_field", Hidden: true, Fields: []FieldSpec{{Name: " ", Kind: FieldBool, Default: false}}}},
		{"duplicate field", SettingSpec{Key: "test_dup_field", Hidden: true, Fields: []FieldSpec{
			{Name: "enabled", Kind: FieldBool, Default: false},
			{Name: "enabled", Kind: FieldBool, Default: false},
		}}},
		{"unknown kind", SettingSpec{Key: "test_unknown_kind", Hidden: true, Fields: []FieldSpec{{Name: "value", Kind: "float", Default: 0}}}},
		{"bool default type mismatch", SettingSpec{Key: "test_bool_default", Hidden: true, Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: "yes"}}}},
		{"int default type mismatch", SettingSpec{Key: "test_int_default", Hidden: true, Fields: []FieldSpec{{Name: "n", Kind: FieldInt, Default: "3"}}}},
		{"int default out of range", SettingSpec{Key: "test_int_range", Hidden: true, Fields: []FieldSpec{{Name: "n", Kind: FieldInt, Default: 5, Min: 10, Max: 20}}}},
		{"force value type mismatch", SettingSpec{Key: "test_force_type", Hidden: true, Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false, ForceValue: 1}}}},
		// A visible section is rendered from the spec, so a missing locale key
		// would render a blank label instead of failing the build.
		{"visible without title key", SettingSpec{Key: "test_no_title", Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false, LabelKey: "settings.x"}}}},
		{"visible field without label key", SettingSpec{Key: "test_no_label", TitleKey: "settings.x", Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false}}}},
	}
	// Register the duplicate's first instance so the second registration below
	// collides with it.
	RegisterSetting(SettingSpec{Key: "test_duplicate_setting", Hidden: true, Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false}}})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("expected RegisterSetting(%+v) to panic", tc.spec)
				}
			}()
			RegisterSetting(tc.spec)
		})
	}
}

func TestConfigKeysAreRegisteredSections(t *testing.T) {
	// The codec dispatches on the two registries alone, so every top-level key
	// this project knows has to be declared in one of them. A key that is
	// neither falls through to AdditionalFields, where it is carried but never
	// read — which is what an undeclared setting or section would look like.
	for _, key := range []string{
		"keys", "api_keys", "accounts", "proxies", "model_aliases", "admin",
		"runtime", "auto_delete", "elastic_pool", "device_id_pool", "mihomo",
		"vercel", "_vercel_sync_hash", "_vercel_sync_time", "tool_marker_secret",
		"claude_mapping", "claude_model_mapping", "compat", "toolcall",
		"history_split", "current_input_file",
	} {
		if !IsSectionKey(key) {
			t.Fatalf("config key %q is not a registered section, so a setting could shadow it", key)
		}
		if IsSettingKey(key) {
			t.Fatalf("config key %q must not be a registered setting key", key)
		}
	}
	// A key may not be both: the codec checks settings first, so a section of the
	// same name would silently never be decoded.
	for _, key := range SettingKeys() {
		if IsSectionKey(key) {
			t.Fatalf("config key %q is registered as both a setting and a section", key)
		}
	}
}

func TestSettingReadersFallBackToTheDeclaredDefault(t *testing.T) {
	RegisterSetting(SettingSpec{
		Key:      "test_defaults",
		TitleKey: "settings.x",
		Fields: []FieldSpec{
			{Name: "on_by_default", Kind: FieldBool, Default: true, LabelKey: "settings.x"},
			{Name: "off_by_default", Kind: FieldBool, Default: false, LabelKey: "settings.x"},
			{Name: "count", Kind: FieldInt, Default: 7, Min: 1, Max: 100, LabelKey: "settings.x"},
			{Name: "text", Kind: FieldString, Default: "fallback", LabelKey: "settings.x"},
		},
	})
	store := &Store{cfg: Config{}}

	if !store.SettingBool("test_defaults", "on_by_default") {
		t.Fatal("an unset bool must read as its declared default (true)")
	}
	if store.SettingBool("test_defaults", "off_by_default") {
		t.Fatal("an unset bool must read as its declared default (false)")
	}
	if got := store.SettingInt("test_defaults", "count"); got != 7 {
		t.Fatalf("count=%d want 7", got)
	}
	if got := store.SettingString("test_defaults", "text"); got != "fallback" {
		t.Fatalf("text=%q want fallback", got)
	}

	// A stale caller after a setting is retired must lose the value, not take
	// the process down.
	if store.SettingBool("test_never_registered", "enabled") {
		t.Fatal("an unregistered setting must read as false")
	}
	if got := store.SettingInt("test_defaults", "nope"); got != 0 {
		t.Fatalf("an unregistered field must read as 0, got %d", got)
	}
}

func TestStoredSettingValuesWinOverDefaults(t *testing.T) {
	RegisterSetting(SettingSpec{
		Key:      "test_stored",
		TitleKey: "settings.x",
		Fields: []FieldSpec{
			{Name: "flag", Kind: FieldBool, Default: true, LabelKey: "settings.x"},
			{Name: "count", Kind: FieldInt, Default: 7, Min: 0, Max: 100, LabelKey: "settings.x"},
			{Name: "text", Kind: FieldString, Default: "d", Trim: true, LabelKey: "settings.x"},
		},
	})
	store := &Store{cfg: Config{}}
	store.cfg.SetSetting("test_stored", "flag", false)
	store.cfg.SetSetting("test_stored", "text", "  spaced  ")

	if store.SettingBool("test_stored", "flag") {
		t.Fatal("a stored value must win over the declared default")
	}
	if got := store.SettingString("test_stored", "text"); got != "spaced" {
		t.Fatalf("a Trim field must read trimmed, got %q", got)
	}

	// Zero means "unset" for an int field, the same rule ValidateIntRange and
	// the hand-written accessors applied, so a stored 0 reads as the default
	// instead of disabling the setting.
	store.cfg.SetSetting("test_stored", "count", 0)
	if got := store.SettingInt("test_stored", "count"); got != 7 {
		t.Fatalf("a stored 0 must read as the default, got %d", got)
	}
	store.cfg.SetSetting("test_stored", "count", 42)
	if got := store.SettingInt("test_stored", "count"); got != 42 {
		t.Fatalf("count=%d want 42", got)
	}
}

// A retired feature keeps its kill switch in the declaration, so the accessor,
// the settings API and the config round-trip all agree without a hard-coded
// accessor body.
func TestForceValueOverridesStoredValue(t *testing.T) {
	RegisterSetting(SettingSpec{
		Key:      "test_retired",
		TitleKey: "settings.x",
		Fields: []FieldSpec{
			{Name: "enabled", Kind: FieldBool, Default: false, ForceValue: false, LabelKey: "settings.x"},
		},
	})
	store := &Store{cfg: Config{}}
	store.cfg.SetSetting("test_retired", "enabled", true)

	if store.SettingBool("test_retired", "enabled") {
		t.Fatal("a ForceValue field must ignore a stored true")
	}
	if values := store.SettingValues("test_retired"); values["enabled"] != false {
		t.Fatalf("SettingValues must report the forced value, got %#v", values)
	}
	// The value still round-trips, so a config file written before the
	// retirement is not silently rewritten.
	raw, err := json.Marshal(store.cfg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(raw), `"test_retired":{"enabled":true}`) {
		t.Fatalf("a forced field must still round-trip, got %s", raw)
	}
}

// The whole point of the registry is that a setting is a first-class config
// field with no per-key codec work, so it must survive a file round-trip in the
// same shape a hand-written section uses.
func TestSettingRoundTripsThroughTheConfigFile(t *testing.T) {
	RegisterSetting(SettingSpec{
		Key:      "test_round_trip",
		TitleKey: "settings.x",
		Fields: []FieldSpec{
			{Name: "enabled", Kind: FieldBool, Default: false, LabelKey: "settings.x"},
			{Name: "limit", Kind: FieldInt, Default: 0, Min: 0, Max: 1000, LabelKey: "settings.x"},
			{Name: "label", Kind: FieldString, Default: "", LabelKey: "settings.x"},
		},
	})

	var cfg Config
	if err := json.Unmarshal([]byte(`{"test_round_trip":{"enabled":true,"limit":25,"label":"hi"}}`), &cfg); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	stored := cfg.Settings["test_round_trip"]
	if stored["enabled"] != true || stored["limit"] != 25 || stored["label"] != "hi" {
		t.Fatalf("setting not decoded into the store: %#v", stored)
	}
	if _, leaked := cfg.AdditionalFields["test_round_trip"]; leaked {
		t.Fatal("a registered setting must not land in AdditionalFields")
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if !strings.Contains(string(raw), `"test_round_trip":{"enabled":true,"label":"hi","limit":25}`) {
		t.Fatalf("setting must be written in the section shape, got %s", raw)
	}

	// An undeclared field inside a registered section is dropped, which is what
	// decoding a hand-written section into its struct already did.
	var dropped Config
	if err := json.Unmarshal([]byte(`{"test_round_trip":{"enabled":true,"nope":1}}`), &dropped); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if _, ok := dropped.Settings["test_round_trip"]["nope"]; ok {
		t.Fatalf("an undeclared field must be dropped, got %#v", dropped.Settings["test_round_trip"])
	}
}

// A value whose JSON type contradicts its declared kind must fail the load
// rather than be silently ignored: the setting would otherwise read as its
// default and the user would have no way to notice.
func TestSettingRejectsValueOfTheWrongKind(t *testing.T) {
	RegisterSetting(SettingSpec{
		Key:      "test_kinds",
		TitleKey: "settings.x",
		Fields: []FieldSpec{
			{Name: "count", Kind: FieldInt, Default: 0, Min: 0, Max: 1000, LabelKey: "settings.x"},
		},
	})
	var cfg Config
	err := json.Unmarshal([]byte(`{"test_kinds":{"count":"lots"}}`), &cfg)
	if err == nil {
		t.Fatal("expected a wrong-typed value to fail the load")
	}
	if !strings.Contains(err.Error(), "test_kinds") {
		t.Fatalf("error must name the section, got %v", err)
	}
}

func TestSettingCloneIsIndependent(t *testing.T) {
	RegisterSetting(SettingSpec{
		Key:      "test_clone",
		TitleKey: "settings.x",
		Fields:   []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false, LabelKey: "settings.x"}},
	})
	cfg := withSetting("test_clone", "enabled", true)

	clone := cfg.Clone()
	clone.Settings["test_clone"]["enabled"] = false

	if cfg.Settings["test_clone"]["enabled"] != true {
		t.Fatal("mutating a clone must not change the original")
	}
}

// An unknown key that is not a registered setting keeps the historical
// passthrough behaviour.
func TestUnregisteredKeysStillGoToAdditionalFields(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"some_unknown_section":{"enabled":true}}`), &cfg); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if _, ok := cfg.AdditionalFields["some_unknown_section"]; !ok {
		t.Fatalf("unknown key must be preserved in AdditionalFields: %#v", cfg.AdditionalFields)
	}
	if len(cfg.Settings) != 0 {
		t.Fatalf("unknown key must not become a setting: %#v", cfg.Settings)
	}
}

func TestSettingSpecsKeepDeclarationOrder(t *testing.T) {
	RegisterSetting(SettingSpec{Key: "test_order_a", TitleKey: "settings.x",
		Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false, LabelKey: "settings.x"}}})
	RegisterSetting(SettingSpec{Key: "test_order_b", TitleKey: "settings.x",
		Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false, LabelKey: "settings.x"}}})
	RegisterSetting(SettingSpec{Key: "test_order_c", TitleKey: "settings.x",
		Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false, LabelKey: "settings.x"}}})

	var seen []string
	for _, spec := range SettingSpecs() {
		switch spec.Key {
		case "test_order_a", "test_order_b", "test_order_c":
			seen = append(seen, spec.Key)
		}
	}
	want := []string{"test_order_a", "test_order_b", "test_order_c"}
	if len(seen) != len(want) {
		t.Fatalf("expected all three registered settings, got %v", seen)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("declaration order not preserved: got %v, want %v", seen, want)
		}
	}
}

func TestCoerceSettingFieldValidatesAndNormalizes(t *testing.T) {
	spec := SettingSpec{
		Key:      "test_coerce",
		TitleKey: "settings.x",
		Fields: []FieldSpec{
			{Name: "enabled", Kind: FieldBool, Default: false, LabelKey: "settings.x"},
			{Name: "count", Kind: FieldInt, Default: 0, Min: 10, Max: 20, LabelKey: "settings.x"},
			{Name: "optional", Kind: FieldInt, Default: 0, Min: 0, Max: 100, LabelKey: "settings.x"},
			{Name: "provider", Kind: FieldString, Default: "", NonBlank: true, LabelKey: "settings.x"},
		},
	}
	boolField, intField, optionalField, strField := spec.Fields[0], spec.Fields[1], spec.Fields[2], spec.Fields[3]

	// A JSON number arrives as float64, and a form field may arrive as a string.
	for _, raw := range []any{float64(15), 15, "15"} {
		v, present, err := CoerceSettingField(spec, intField, raw)
		if err != nil || !present || v != 15 {
			t.Fatalf("raw %#v: value=%#v present=%v err=%v", raw, v, present, err)
		}
	}
	if _, _, err := CoerceSettingField(spec, intField, float64(5)); err == nil {
		t.Fatal("expected a below-min value to be rejected")
	}
	if _, _, err := CoerceSettingField(spec, intField, "lots"); err == nil {
		t.Fatal("expected a non-numeric value to be rejected")
	}
	// The request mentioning a field is what makes it set, so an explicit 0 is
	// the value the user chose: a field whose declared minimum is positive has
	// no meaningful zero and rejects it, while a field whose range includes 0
	// keeps accepting it.
	for _, raw := range []any{0, float64(0), "0"} {
		if _, _, err := CoerceSettingField(spec, intField, raw); err == nil {
			t.Fatalf("raw %#v: expected an explicit 0 below the declared minimum to be rejected", raw)
		}
	}
	if v, present, err := CoerceSettingField(spec, optionalField, 0); err != nil || !present || v != 0 {
		t.Fatalf("a zero inside the declared range must be accepted, value=%#v present=%v err=%v", v, present, err)
	}
	// A nil value is absent rather than an error, so a partial update never
	// resets a field it did not mention.
	if _, present, err := CoerceSettingField(spec, intField, nil); err != nil || present {
		t.Fatalf("nil must be reported as absent, present=%v err=%v", present, err)
	}

	if v, present, err := CoerceSettingField(spec, boolField, "true"); err != nil || !present || v != true {
		t.Fatalf("string bool: value=%#v present=%v err=%v", v, present, err)
	}
	if _, _, err := CoerceSettingField(spec, boolField, "maybe"); err == nil {
		t.Fatal("expected a non-boolean value to be rejected")
	}

	if v, _, _ := CoerceSettingField(spec, strField, "  ollama  "); v != "ollama" {
		t.Fatalf("a string field must be trimmed, got %#v", v)
	}
	// A trimmed field cannot tell "cleared" from "spaces", so both are the unset
	// (empty) value: rejecting one while accepting the other would fail a whole
	// settings request over two inputs that mean the same thing.
	for _, raw := range []any{"", "   ", " \t "} {
		v, present, err := CoerceSettingField(spec, strField, raw)
		if err != nil || !present || v != "" {
			t.Fatalf("raw %#v: a blank value must be accepted as unset, value=%#v present=%v err=%v", raw, v, present, err)
		}
	}
}

// A NonBlank field still rejects a blank stored value on the load path, where a
// hand-edited file can carry one: that is the only place the flag still acts.
func TestValidateRegisteredSettingsRejectsBlankStoredString(t *testing.T) {
	if err := ValidateConfig(withSetting("embeddings", "provider", "   ")); err == nil {
		t.Fatal("expected a stored blank value to fail validation")
	}
	if err := ValidateConfig(withSetting("embeddings", "provider", "")); err != nil {
		t.Fatalf("an empty stored value must stay valid: %v", err)
	}
}

// A JSON null in the config file means "this field is not set", not "set to the
// zero value". The hand-written sections this registry replaced held pointers,
// so null read as the declared default — for a default-true switch, storing the
// zero value would silently turn it off.
func TestSettingNullMeansUnset(t *testing.T) {
	var cfg Config
	raw := `{
		"bottom_format_injection":{"enabled":null,"prompt":null},
		"expert_text_file_inline":{"enabled":null},
		"responses":{"store_ttl_seconds":null},
		"embeddings":{"provider":null},
		"input_file":{"enabled":true,"min_chars":null}
	}`
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if _, ok := cfg.Settings["bottom_format_injection"]; ok {
		t.Fatalf("a section whose fields are all null must not be stored: %#v", cfg.Settings["bottom_format_injection"])
	}
	store := &Store{cfg: cfg}
	if !store.SettingBool("bottom_format_injection", "enabled") {
		t.Fatal("a null bool must read as its declared default (true), not as false")
	}
	if !store.SettingBool("expert_text_file_inline", "enabled") {
		t.Fatal("a null bool must read as its declared default (true), not as false")
	}
	if got := store.SettingInt("responses", "store_ttl_seconds"); got != 900 {
		t.Fatalf("store_ttl_seconds=%d want the declared default 900", got)
	}
	if got := store.SettingString("embeddings", "provider"); got != "" {
		t.Fatalf("provider=%q want the declared default (empty)", got)
	}
	// A field the file does set is still stored, so null means unset rather than
	// the section being dropped.
	if !store.SettingBool("input_file", "enabled") {
		t.Fatal("a non-null sibling field must still be stored")
	}

	// An explicit false is still an explicit false: only null reads as unset.
	var off Config
	if err := json.Unmarshal([]byte(`{"bottom_format_injection":{"enabled":false}}`), &off); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if (&Store{cfg: off}).SettingBool("bottom_format_injection", "enabled") {
		t.Fatal("an explicit false must keep turning the switch off")
	}

	// The null never reaches the file again: the field is not set, so it is not
	// written back as a false.
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if strings.Contains(string(out), `"bottom_format_injection"`) {
		t.Fatalf("an unset field must not be written back, got %s", out)
	}
	if !strings.Contains(string(out), `"input_file":{"enabled":true}`) {
		t.Fatalf("a set field must round-trip, got %s", out)
	}
}

func TestNormalizeRegisteredSettingsTrimsInPlace(t *testing.T) {
	RegisterSetting(SettingSpec{
		Key:      "test_normalize",
		TitleKey: "settings.x",
		Fields: []FieldSpec{
			{Name: "prompt", Kind: FieldString, Default: "", Trim: true, LabelKey: "settings.x"},
			{Name: "raw", Kind: FieldString, Default: "", LabelKey: "settings.x"},
		},
	})
	cfg := Config{}
	cfg.SetSetting("test_normalize", "prompt", "  trimmed  ")
	cfg.SetSetting("test_normalize", "raw", "  kept  ")

	NormalizeRegisteredSettings(&cfg)
	if got := cfg.Settings["test_normalize"]["prompt"]; got != "trimmed" {
		t.Fatalf("prompt=%q want trimmed", got)
	}
	if got := cfg.Settings["test_normalize"]["raw"]; got != "  kept  " {
		t.Fatalf("raw=%q want untouched", got)
	}
}

// TestRegisteredSettingDefaults pins the effective value of every setting this
// repository declares. These defaults and trim rules are the contract the
// request path and the settings API rely on, so moving a value into the
// registry must not change any of them: a careless spec edit fails here.
func TestRegisteredSettingDefaults(t *testing.T) {
	store := &Store{cfg: Config{}}

	cases := []struct {
		setting string
		field   string
		want    any
	}{
		{"responses", "store_ttl_seconds", 900},
		{"embeddings", "provider", ""},
		{"thinking_injection", "enabled", false},
		{"thinking_injection", "prompt", ""},
		{"bottom_format_injection", "enabled", true},
		{"bottom_format_injection", "prompt", ""},
		{"input_file", "enabled", false},
		{"input_file", "min_chars", 0},
		{"expert_prompt_segment", "enabled", false},
		{"expert_prompt_segment", "max_chars", 160000},
		{"expert_text_file_inline", "enabled", true},
		{"expert_text_file_inline", "max_file_bytes", 3 * 1024 * 1024},
		{"auto_route_vision", "enabled", false},
	}
	for _, tc := range cases {
		got := store.SettingValues(tc.setting)[tc.field]
		if got != tc.want {
			t.Errorf("%s.%s default = %#v, want %#v", tc.setting, tc.field, got, tc.want)
		}
	}
}

// TestRegisteredStringFieldsAreTrimmed pins which string settings read trimmed.
// Every accessor for a free-text setting trimmed, so the registry has to say so;
// a field that loses its Trim flag would start feeding padded text to the
// upstream request.
func TestRegisteredStringFieldsAreTrimmed(t *testing.T) {
	store := &Store{cfg: Config{}}
	for _, key := range []string{"embeddings", "thinking_injection", "bottom_format_injection"} {
		for _, f := range []string{"provider", "prompt"} {
			if _, ok := LookupSettingField(key, f); !ok {
				continue
			}
			store.cfg.SetSetting(key, f, "  padded  ")
			if got := store.SettingString(key, f); got != "padded" {
				t.Errorf("%s.%s read %q, want trimmed %q", key, f, got, "padded")
			}
		}
	}
}

// The card registry decides what the settings page looks like: which settings
// share a card, and where that card sits. Each rejected shape below would
// otherwise show up as a setting rendered on the wrong card, on two cards, or
// nowhere at all.
func TestRegisterSettingCardRejectsBadSpecs(t *testing.T) {
	RegisterSetting(SettingSpec{Key: "test_card_member", TitleKey: "settings.x",
		Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false, LabelKey: "settings.x"}}})
	RegisterSetting(SettingSpec{Key: "test_card_hidden", Hidden: true,
		Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false}}})

	cases := []struct {
		name string
		spec SettingCardSpec
	}{
		{"empty key", SettingCardSpec{Key: "  ", TitleKey: "settings.x", Settings: []string{"test_card_member"}}},
		{"no title key", SettingCardSpec{Key: "test_card_no_title", Settings: []string{"test_card_member"}}},
		{"no settings", SettingCardSpec{Key: "test_card_empty", TitleKey: "settings.x"}},
		{"empty member", SettingCardSpec{Key: "test_card_blank_member", TitleKey: "settings.x", Settings: []string{" "}}},
		{"unknown member", SettingCardSpec{Key: "test_card_unknown", TitleKey: "settings.x", Settings: []string{"test_card_missing"}}},
		// A hidden setting is config-file only; a card claiming it would put a
		// control for it on the settings page.
		{"hidden member", SettingCardSpec{Key: "test_card_hidden_member", TitleKey: "settings.x", Settings: []string{"test_card_hidden"}}},
		{"duplicate", SettingCardSpec{Key: "test_card_duplicate", TitleKey: "settings.x", Settings: []string{"test_card_member"}}},
	}
	// Register the duplicate's first instance so the second registration below
	// collides with it.
	RegisterSettingCard(SettingCardSpec{Key: "test_card_duplicate", TitleKey: "settings.x", Settings: []string{"test_card_member"}})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("expected RegisterSettingCard(%+v) to panic", tc.spec)
				}
			}()
			RegisterSettingCard(tc.spec)
		})
	}
}

// A setting belongs to at most one card: two cards claiming it would render the
// same setting twice, with two controls writing one config field.
func TestRegisterSettingCardRejectsASecondClaim(t *testing.T) {
	RegisterSetting(SettingSpec{Key: "test_card_claim", TitleKey: "settings.x",
		Fields: []FieldSpec{{Name: "enabled", Kind: FieldBool, Default: false, LabelKey: "settings.x"}}})
	RegisterSettingCard(SettingCardSpec{Key: "test_card_claim_a", TitleKey: "settings.x", Settings: []string{"test_card_claim"}})

	defer func() {
		if recover() == nil {
			t.Fatal("expected a second card claiming the same setting to panic")
		}
	}()
	RegisterSettingCard(SettingCardSpec{Key: "test_card_claim_b", TitleKey: "settings.x", Settings: []string{"test_card_claim"}})
}

// The settings page renders the page as one sequence of cards, so the two
// registries that declare them must agree on where each one sits: the injection
// card and the split threshold card belong between the runtime and the
// auto-delete cards, which is what the declared orders say.
func TestSettingCardOrdersInterleaveWithSectionCards(t *testing.T) {
	sectionOrder := map[string]int{}
	for _, spec := range SectionSpecs() {
		if spec.Form != nil {
			sectionOrder[spec.Key] = spec.Form.Order
		}
	}
	want := map[string]int{
		"admin":       1,
		"runtime":     2,
		"behavior":    3,
		"input_file":  4,
		"auto_delete": 6,
	}
	got := map[string]int{}
	for key, order := range sectionOrder {
		if _, ok := want[key]; ok {
			got[key] = order
		}
	}
	for _, card := range SettingCards() {
		if _, ok := want[card.Key]; ok {
			got[card.Key] = card.Order
		}
	}
	for key, order := range want {
		if got[key] != order {
			t.Errorf("card %q sits at order %d, want %d (all: %v)", key, got[key], order, got)
		}
	}
}

// Every visible setting must be reachable from the page: either a card names it,
// or it renders in a card of its own. This pins the production cards' member
// lists, so a setting that silently drops off the page fails here.
func TestVisibleSettingsAreGroupedIntoTheDeclaredCards(t *testing.T) {
	grouped := map[string][]string{}
	for _, card := range SettingCards() {
		grouped[card.Key] = append([]string(nil), card.Settings...)
	}
	want := map[string][]string{
		"behavior":   {"responses", "embeddings", "thinking_injection", "bottom_format_injection"},
		"input_file": {"input_file"},
	}
	for key, members := range want {
		if strings.Join(grouped[key], ",") != strings.Join(members, ",") {
			t.Errorf("card %q holds %v, want %v", key, grouped[key], members)
		}
	}
	// A setting a card names reports that card; a hidden one reports none, so it
	// never reaches a card even if one is added for it later.
	card, ok := SettingCardOf("thinking_injection")
	if !ok || card.Key != "behavior" {
		t.Fatalf("thinking_injection must report the behavior card, got %+v ok=%v", card, ok)
	}
	for _, hidden := range []string{"auto_route_vision", "expert_prompt_segment"} {
		if _, ok := SettingCardOf(hidden); ok {
			t.Fatalf("hidden setting %q must not be claimed by a card", hidden)
		}
	}
}
