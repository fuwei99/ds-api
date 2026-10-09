package config

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The section registry exists so that a new config section needs one
// declaration and nothing else: no codec case, no validation line, no Clone
// line, no admin handler, no web UI component. These tests exercise that path
// for a section registered at test time, which is the same path a real one
// takes.

type testSectionConfig struct {
	Enabled bool   `json:"enabled,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Label   string `json:"label,omitempty"`
}

const (
	testSectionKey       = "test_sections_registered"
	testSectionFormKey   = "test_sections_form"
	testSectionEnumField = "mode"
)

var testSectionValue testSectionConfig

func init() {
	RegisterSection(SectionSpec{
		Key: testSectionKey,
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &testSectionValue)
		},
		Encode: func(c Config) (any, bool) {
			if !testSectionValue.Enabled && testSectionValue.Limit == 0 && testSectionValue.Label == "" {
				return nil, false
			}
			return testSectionValue, true
		},
		Clone: func(dst, src *Config) {
			// The section's storage is a package variable in this test, so there
			// is nothing to copy; the real sections copy their typed field.
		},
	})

	// A section with a form: its read, write and rendering all come from the
	// declaration, so the admin API and the web UI pick it up with no change.
	testSectionValue = testSectionConfig{}
	RegisterSection(SectionSpec{
		Key: testSectionFormKey,
		Form: &SectionForm{
			TitleKey:       "settings.testSectionTitle",
			DescriptionKey: "settings.testSectionDescription",
			Fields: []SectionField{
				SectionBool(testSectionFormKey+".enabled", "settings.testEnabled",
					func(s *Store) bool { return testSectionValue.Enabled },
					func(c *Config, v bool) { testSectionValue.Enabled = v }),
				SectionInt(testSectionFormKey+".limit", "settings.testLimit", 1, 100,
					func(s *Store) int { return testSectionValue.Limit },
					func(c *Config, v int) { testSectionValue.Limit = v }),
				SectionString(testSectionFormKey+".label", "settings.testLabel",
					func(s *Store) string { return testSectionValue.Label },
					func(c *Config, v string) { testSectionValue.Label = v }),
				SectionEnum(testSectionFormKey+".mode", "settings.testMode",
					[]SectionOption{
						{Value: "fast", LabelKey: "settings.testModeFast"},
						{Value: "slow", LabelKey: "settings.testModeSlow"},
					},
					func(s *Store) string { return "fast" },
					func(c *Config, v string) error { return nil }),
				SectionGroup(testSectionFormKey+".nested", "settings.testNested",
					SectionBool(testSectionFormKey+".nested.on", "settings.testNestedOn",
						func(s *Store) bool { return false },
						func(c *Config, v bool) {})),
			},
		},
		Decode: func(c *Config, raw json.RawMessage) error {
			return json.Unmarshal(raw, &testSectionValue)
		},
		Encode: func(c Config) (any, bool) {
			return testSectionValue, true
		},
	})
}

func TestRegisterSectionRejectsBadSpecs(t *testing.T) {
	ok := func() SectionSpec {
		return SectionSpec{
			Key:    "test_section_ok_shape",
			Decode: func(*Config, json.RawMessage) error { return nil },
		}
	}
	withForm := func(mutate func(*SectionForm)) SectionSpec {
		spec := ok()
		spec.Key = "test_section_bad_form"
		spec.Form = &SectionForm{TitleKey: "settings.x"}
		mutate(spec.Form)
		return spec
	}
	leaf := func() SectionField {
		return SectionField{Name: "enabled", Kind: FieldBool, LabelKey: "settings.x",
			Read: func(*Store) any { return false }, Apply: func(*Config, any) error { return nil }}
	}

	cases := []struct {
		name string
		spec SectionSpec
	}{
		{"empty key", SectionSpec{Key: "  ", Decode: func(*Config, json.RawMessage) error { return nil }}},
		{"no decode", SectionSpec{Key: "test_section_no_decode"}},
		// A registered setting owns the key: the codec checks settings first, so
		// the section would silently never be decoded.
		{"setting key", SectionSpec{Key: "responses", Decode: func(*Config, json.RawMessage) error { return nil }}},
		{"duplicate", SectionSpec{Key: testSectionKey, Decode: func(*Config, json.RawMessage) error { return nil }}},
		{"form without title", SectionSpec{Key: "test_section_no_title", Form: &SectionForm{},
			Decode: func(*Config, json.RawMessage) error { return nil }}},
		{"field without label", withForm(func(f *SectionForm) {
			field := leaf()
			field.LabelKey = ""
			f.Fields = []SectionField{field}
		})},
		{"field without name", withForm(func(f *SectionForm) {
			field := leaf()
			field.Name = " "
			f.Fields = []SectionField{field}
		})},
		{"field without read", withForm(func(f *SectionForm) {
			field := leaf()
			field.Read = nil
			f.Fields = []SectionField{field}
		})},
		{"field without apply", withForm(func(f *SectionForm) {
			field := leaf()
			field.Apply = nil
			f.Fields = []SectionField{field}
		})},
		{"field with unknown kind", withForm(func(f *SectionForm) {
			field := leaf()
			field.Kind = "float"
			f.Fields = []SectionField{field}
		})},
		{"empty group", withForm(func(f *SectionForm) {
			f.Fields = []SectionField{{Name: "group", Kind: FieldGroup, LabelKey: "settings.x"}}
		})},
		{"enum without options", withForm(func(f *SectionForm) {
			field := leaf()
			field.Kind = FieldEnum
			f.Fields = []SectionField{field}
		})},
		{"enum option without label", withForm(func(f *SectionForm) {
			field := leaf()
			field.Kind = FieldEnum
			field.Options = []SectionOption{{Value: "a"}, {Value: "b", LabelKey: "settings.b"}}
			f.Fields = []SectionField{field}
		})},
		{"enum option without value", withForm(func(f *SectionForm) {
			field := leaf()
			field.Kind = FieldEnum
			field.Options = []SectionOption{{LabelKey: "settings.a"}, {Value: "b", LabelKey: "settings.b"}}
			f.Fields = []SectionField{field}
		})},
	}
	// Register the duplicate's first instance so the second registration below
	// collides with it.
	RegisterSection(SectionSpec{Key: "test_section_dup", Decode: func(*Config, json.RawMessage) error { return nil }})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("expected RegisterSection(%+v) to panic", tc.spec)
				}
			}()
			spec := tc.spec
			if spec.Key == "test_section_dup" {
				RegisterSection(spec)
			}
			RegisterSection(spec)
		})
	}
}

// A section registered at test time is decoded, encoded and cloned by the codec
// with no change to the codec: that is the whole point of the registry.
func TestCodecDispatchesOnTheSectionRegistry(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"`+testSectionKey+`":{"enabled":true,"limit":7,"label":"x"}}`), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !testSectionValue.Enabled || testSectionValue.Limit != 7 || testSectionValue.Label != "x" {
		t.Fatalf("section not decoded: %+v", testSectionValue)
	}
	// An undeclared key still falls through to AdditionalFields.
	if _, ok := cfg.AdditionalFields["test_sections_unknown"]; ok {
		t.Fatal("unexpected passthrough")
	}

	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(out), `"`+testSectionKey+`"`) {
		t.Fatalf("a set section must be written back, got %s", out)
	}

	// A section at its zero value stays out of the file.
	testSectionValue = testSectionConfig{}
	out, err = json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(out), `"`+testSectionKey+`"`) {
		t.Fatalf("an unset section must be omitted, got %s", out)
	}
}

// A section that declares a form gets its read and write from the declaration:
// the settings API reads it field by field and a settings update writes it field
// by field, including nested groups.
func TestSectionFormDrivesReadAndWrite(t *testing.T) {
	store := &Store{cfg: Config{}}
	values := store.SectionValues(testSectionFormKey)
	for _, name := range []string{"enabled", "limit", "label", "mode", "nested"} {
		if _, ok := values[name]; !ok {
			t.Fatalf("declared field %q is not reported: %#v", name, values)
		}
	}
	if nested, ok := values["nested"].(map[string]any); !ok || len(nested) != 1 {
		t.Fatalf("a group must be reported as an object of its fields: %#v", values["nested"])
	}

	cfg := Config{}
	updates := SectionUpdates(map[string]any{
		testSectionFormKey: map[string]any{
			"enabled": true,
			"limit":   5,
			"nested":  map[string]any{"on": true},
			"bogus":   "ignored",
		},
	})
	if _, ok := updates[testSectionFormKey]; !ok {
		t.Fatalf("a section the request carries must be reported: %#v", updates)
	}
	if err := ApplySections(&cfg, updates); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !testSectionValue.Enabled || testSectionValue.Limit != 5 {
		t.Fatalf("fields not applied: %+v", testSectionValue)
	}
	if testSectionValue.Label != "" {
		t.Fatalf("a field the request did not carry must stay untouched: %+v", testSectionValue)
	}

	// An out-of-range value is rejected, and the section's own error text names
	// the field the way the config file does.
	err := ApplySections(&cfg, SectionUpdates(map[string]any{
		testSectionFormKey: map[string]any{"limit": 0},
	}))
	if err == nil || !strings.Contains(err.Error(), testSectionFormKey+".limit") {
		t.Fatalf("expected a range error naming the field, got %v", err)
	}
	err = ApplySections(&cfg, SectionUpdates(map[string]any{
		testSectionFormKey: map[string]any{"mode": "sideways"},
	}))
	if err == nil || !strings.Contains(err.Error(), testSectionFormKey+".mode") {
		t.Fatalf("expected an enum error naming the field, got %v", err)
	}
}

// A settings update never resets a section it does not mention, and a section
// the registry does not declare is ignored rather than applied.
func TestSectionUpdatesIgnoreUndeclaredKeys(t *testing.T) {
	updates := SectionUpdates(map[string]any{"test_sections_not_registered": map[string]any{"a": 1}})
	if len(updates) != 0 {
		t.Fatalf("undeclared section applied: %#v", updates)
	}
	if err := ApplySections(&Config{}, updates); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// The config import merges through the registry: a section with a form merges
// field by field (exactly as the settings page writes it), a section that
// declares Merge follows its own rule, and a record list is left to its own
// endpoints. Before the registry this list was hand-written, and a section the
// list forgot — auto_delete among them — was silently dropped from an import.
func TestMergeRegisteredSections(t *testing.T) {
	next := Config{}
	next.Runtime.AccountMaxInflight = 4
	next.Runtime.MuteReport.URL = "http://127.0.0.1:9999"
	next.AutoDelete.Mode = "single"

	incoming := Config{}
	incoming.Runtime.AccountMaxInflight = 8
	enabled := true
	incoming.Runtime.MuteReport.Enabled = &enabled
	incoming.AutoDelete.Mode = "all"
	incoming.Admin.PasswordHash = "hash"
	incoming.DeviceIDPool.Items = []DeviceIDPoolItem{{ID: "d1"}}

	if err := MergeRegisteredSections(&next, incoming); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if next.Runtime.AccountMaxInflight != 8 {
		t.Fatalf("a field the import carries must win: %+v", next.Runtime)
	}
	if next.Runtime.MuteReport.Enabled == nil || !*next.Runtime.MuteReport.Enabled {
		t.Fatalf("a nested group field must merge: %+v", next.Runtime.MuteReport)
	}
	if next.Runtime.MuteReport.URL != "http://127.0.0.1:9999" {
		t.Fatalf("a field the import does not carry must be left alone: %+v", next.Runtime.MuteReport)
	}
	// auto_delete declares a no-op Merge on purpose: a merge must never turn on
	// post-request deletion of remote conversations, so only replace applies it.
	if next.AutoDelete.Mode != "single" {
		t.Fatalf("auto_delete must not be merged, got %+v", next.AutoDelete)
	}
	if next.Admin.PasswordHash != "hash" {
		t.Fatalf("the admin section's own merge rule must run, got %+v", next.Admin)
	}
	if len(next.DeviceIDPool.Items) != 0 {
		t.Fatalf("a record list without a Merge hook must be left alone, got %+v", next.DeviceIDPool)
	}
}

// A field the section removed is rejected with an error naming its replacement:
// the settings API must never answer 200 for a write it did not perform.
func TestApplySectionsRejectsRemovedFields(t *testing.T) {
	updates := SectionUpdates(map[string]any{"auto_delete": map[string]any{"sessions": true}})
	if len(updates) != 1 {
		t.Fatalf("the section must be reported as touched: %#v", updates)
	}
	err := ApplySections(&Config{}, updates)
	if err == nil {
		t.Fatal("expected a removed field to be rejected")
	}
	if !strings.Contains(err.Error(), "auto_delete.sessions") || !strings.Contains(err.Error(), "auto_delete.mode") {
		t.Fatalf("the error must name the removed field and its replacement, got %v", err)
	}
	// A request that carries only declared fields is unaffected.
	if err := ApplySections(&Config{}, SectionUpdates(map[string]any{
		"auto_delete": map[string]any{"mode": "single"},
	})); err != nil {
		t.Fatalf("declared field rejected: %v", err)
	}
}

// 枚举取值的旧名在写入侧迁移为新名，而不是被枚举校验拒绝：改名前保存的备份
// 走合并导入、仍发旧名的客户端走设置写入，都按新名生效
// （runtime.device_id_mode 的 real → auto）。
func TestApplySectionsMigratesRenamedEnumValue(t *testing.T) {
	cfg := Config{}
	if err := ApplySections(&cfg, SectionUpdates(map[string]any{
		"runtime": map[string]any{"device_id_mode": "REAL"},
	})); err != nil {
		t.Fatalf("a renamed value must migrate, not fail: %v", err)
	}
	if cfg.Runtime.DeviceIDMode != DeviceIDTypeAuto {
		t.Fatalf("DeviceIDMode=%q want=%q", cfg.Runtime.DeviceIDMode, DeviceIDTypeAuto)
	}

	// 合并导入（PUT /admin/config，merge）走的是同一条字段套用路径。
	next := Config{}
	incoming := Config{Runtime: RuntimeConfig{DeviceIDMode: deviceIDTypeLegacyReal}}
	if err := MergeRegisteredSections(&next, incoming); err != nil {
		t.Fatalf("merge an older backup: %v", err)
	}
	if next.Runtime.DeviceIDMode != DeviceIDTypeAuto {
		t.Fatalf("merged DeviceIDMode=%q want=%q", next.Runtime.DeviceIDMode, DeviceIDTypeAuto)
	}

	// 旧名只用于迁移：它不是声明的取值，所以不会出现在下拉选项里。
	spec, ok := LookupSection("runtime")
	if !ok {
		t.Fatal("runtime section is not registered")
	}
	for _, field := range spec.Form.Fields {
		if field.Name != "device_id_mode" {
			continue
		}
		for _, opt := range field.Options {
			if opt.Value == deviceIDTypeLegacyReal {
				t.Fatalf("the legacy name must not be a rendered option: %#v", field.Options)
			}
		}
	}
}

// The legacy sessions key keeps working where it always did — in the config
// file — even though the settings API rejects a write that carries it.
func TestAutoDeleteLegacySessionsStillLoadsFromTheFile(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"auto_delete":{"sessions":true}}`), &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	store := &Store{cfg: cfg}
	if got := store.AutoDeleteMode(); got != "all" {
		t.Fatalf("AutoDeleteMode()=%q want=all", got)
	}
}

// Config.Clone must deep-copy every reference-typed field, or a snapshot handed
// to a caller would share storage with the store. The typed sections are copied
// by their own Clone hook, so a section added to the registry is covered; this
// test fails when a reference field is added to Config without one.
func TestConfigCloneDeepCopiesEveryReferenceField(t *testing.T) {
	enabled := true
	cfg := Config{
		Keys:         []string{"k1"},
		APIKeys:      []APIKey{{Key: "k1"}},
		Accounts:     []Account{{Email: "a@example.com"}},
		Proxies:      []Proxy{{ID: "p1"}},
		ModelAliases: map[string]string{"a": "b"},
		Runtime: RuntimeConfig{
			MuteReport:       MuteReportConfig{Enabled: &enabled},
			AccountRateLimit: AccountRateLimitConfig{Enabled: &enabled},
		},
		DeviceIDPool:     DeviceIDPoolConfig{Items: []DeviceIDPoolItem{{ID: "d1"}}},
		Mihomo:           MihomoConfig{Subscriptions: []MihomoSubscription{{ID: "s1", Nodes: []MihomoNode{{Name: "n"}}}}, PortMap: map[string]int{"a": 1}},
		AdditionalFields: map[string]any{"x": 1},
		Settings:         map[string]map[string]any{"s": {"f": 1}},
	}
	clone := cfg.Clone()

	src := reflect.ValueOf(cfg)
	dst := reflect.ValueOf(clone)
	typ := src.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		sv, dv := src.Field(i), dst.Field(i)
		switch sv.Kind() {
		case reflect.Slice, reflect.Map, reflect.Pointer:
			if sv.IsNil() {
				continue
			}
			if sv.Pointer() == dv.Pointer() {
				t.Errorf("Config.%s shares storage with its clone; give the section that owns it a Clone hook in sections.go", field.Name)
			}
		}
	}
}
