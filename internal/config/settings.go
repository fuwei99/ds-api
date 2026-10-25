package config

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// A registered setting is a top-level config section that needs no hand-written
// plumbing. It is declared once, in the registry below, and everything else is
// derived from that declaration:
//
//   - the config-file codec (decode, encode and Clone),
//   - validation of the stored values,
//   - the admin settings read and write API,
//   - the web UI form, which the settings API describes field by field,
//     including which settings share a card and where that card sits on the
//     page (RegisterSettingCard, below).
//
// Adding or changing a setting is therefore:
//
//  1. a RegisterSetting entry in this file, and
//  2. the code that reads it, through Store.SettingBool / SettingInt /
//     SettingString.
//
// Putting it on an existing card is one more line in that card's declaration.
//
// No codec case, no accessor plumbing, no admin handler, no per-protocol wiring
// and no web UI edit. The JSON shape is the same one the hand-written sections
// use, so clients treat registered and unregistered sections uniformly:
//
//	{ "<key>": { "<field>": <value>, ... } }
//
// A section whose values need custom semantics (a nested object, an enum, a
// derived value, a per-key list) is not a registered setting; it is a
// registered section (sections.go), which owns the same plumbing for the
// hand-written shapes.
type FieldKind string

const (
	FieldBool   FieldKind = "bool"
	FieldInt    FieldKind = "int"
	FieldString FieldKind = "string"
	// FieldEnum and FieldGroup belong to a registered section's form (see
	// sections.go). A registered setting is a scalar by construction, so they
	// are rejected there and only rendered for a section field.
	FieldEnum  FieldKind = "enum"
	FieldGroup FieldKind = "group"
)

// FieldSpec declares one field of a registered setting.
type FieldSpec struct {
	// Name is the JSON key of the field inside the section, e.g. "enabled".
	Name string
	// Kind is the field's value type. It decides how the value is decoded from
	// the config file, how it is validated, and which input the web UI renders.
	Kind FieldKind
	// Default is what a reader gets when the config file does not mention the
	// field. Its Go type must match Kind. For an int field, a stored 0 also
	// reads as the default: zero means "unset" throughout this codebase, which
	// is the same rule ValidateIntRange applies.
	Default any
	// Min and Max bound an int field. They are enforced on write and on load.
	// On write the value the request carries is checked as it stands — the
	// request mentioning the field is what makes it set — so an explicit 0 is
	// rejected when Min is positive. On load a stored 0 skips the check, so a
	// field that is unset is never out of range.
	Min int
	Max int
	// Trim trims a string field on read, which is what every hand-written
	// accessor for a free-text field does.
	Trim bool
	// NonBlank marks a field whose *stored* value must not be blank. It applies
	// to the config file, not to the settings API: a stored value that is not
	// empty but trims to nothing is a typo and fails the load, while the write
	// path cannot tell "the user cleared the field" from "the user typed
	// spaces" and stores both as the unset (empty) value.
	NonBlank bool
	// ForceValue, when non-nil, is the value the field always reads as, whatever
	// the config says. It is how a retired feature keeps its kill switch in one
	// place: the config still round-trips the field and the settings API still
	// reports it, but a config file that predates the retirement cannot turn the
	// feature back on by itself. Its Go type must match Kind.
	ForceValue any
	// Multiline renders a textarea instead of a single-line input.
	Multiline bool
	// PrefillDefault makes the web UI start a text field with the section's
	// `default_<field>` text instead of an empty box, so editing it saves a copy
	// of the built-in text. A field without it shows that text as a placeholder
	// instead, which keeps "empty means use the built-in" true.
	PrefillDefault bool
	// LabelKey and HelpKey are web UI locale keys. Keeping the keys here rather
	// than the text keeps translation in the locale files, and lets the web UI
	// render a new field with no component change.
	LabelKey string
	HelpKey  string
}

// SettingSpec declares a registered setting: one top-level config section and
// the fields it holds.
type SettingSpec struct {
	// Key is the config-file and settings-API key. It must not collide with a
	// hand-written config section.
	Key string
	// TitleKey and DescriptionKey are web UI locale keys naming the setting
	// itself. Inside a card that holds several settings (see SettingCardSpec)
	// the title labels the setting's switch and the description explains it; a
	// setting no card names is a card of its own and the two keys title that
	// card, while the switch falls back to its field's LabelKey so the card does
	// not say the same thing twice.
	TitleKey       string
	DescriptionKey string
	// Hidden keeps the section out of the web UI form. It is for settings that
	// are config-file only, so that registering one does not put a control on
	// the settings page.
	Hidden bool
	// Fields are the section's fields, in the order the web UI renders them.
	Fields []FieldSpec
}

// SettingCardSpec declares one card of the settings page: a titled block that
// holds one or more registered settings.
//
// The card exists because the page's unit is a subject the user recognizes, not
// a config section: the two injection switches and their prompts are one card,
// while the config file keeps them as four sections. The registry is what keeps
// that grouping declared — moving a setting to another card is an edit here,
// not an edit in a web UI component.
//
// Grouping is optional. A setting no card names renders in a card of its own,
// so a new setting still needs nothing but its RegisterSetting entry.
type SettingCardSpec struct {
	// Key is the card's id. It names the card for the web UI and never reaches
	// the config file: the settings it holds are still stored under their own
	// keys.
	Key string
	// TitleKey and DescriptionKey are web UI locale keys for the card.
	TitleKey       string
	DescriptionKey string
	// Order places the card on the settings page, in the same sequence as
	// SectionForm.Order: a smaller value comes first, and two cards that declare
	// the same value keep their declaration order. Sections and setting cards
	// share one sequence, so a card's position is declared rather than inherited
	// from which registry happens to hold it.
	Order int
	// Settings are the registered settings that render in this card, in render
	// order. A setting must not be claimed by two cards, and a hidden setting
	// cannot be claimed at all — it has no controls to render.
	Settings []string
}

var (
	settingMu     sync.RWMutex
	settingSpecs  = map[string]SettingSpec{}
	settingOrder  []string
	fieldSpecsFor = map[string]map[string]FieldSpec{}

	settingCardMu    sync.RWMutex
	settingCardSpecs = map[string]SettingCardSpec{}
	settingCardOrder []string
	settingCardOf    = map[string]string{}
)

func init() {
	for _, spec := range []SettingSpec{
		{
			Key:      "responses",
			TitleKey: "settings.responsesTitle",
			Fields: []FieldSpec{
				{Name: "store_ttl_seconds", Kind: FieldInt, Default: 900, Min: 30, Max: 86400,
					LabelKey: "settings.responsesTTL"},
			},
		},
		{
			Key:      "embeddings",
			TitleKey: "settings.embeddingsTitle",
			Fields: []FieldSpec{
				// Trim matches the accessor, which trims: the provider name is
				// used as-is by the embeddings call. An empty value means
				// "unset"; a value that only contains spaces is stored as that
				// same empty value, because a text input cannot tell the two
				// apart. NonBlank therefore only rejects a blank-but-not-empty
				// *stored* value, which a hand-edited config file can carry.
				{Name: "provider", Kind: FieldString, Default: "", Trim: true, NonBlank: true,
					LabelKey: "settings.embeddingsProvider"},
			},
		},
		{
			Key:            "thinking_injection",
			TitleKey:       "settings.thinkingInjectionTitle",
			DescriptionKey: "settings.thinkingInjectionDesc",
			Fields: []FieldSpec{
				{Name: "enabled", Kind: FieldBool, Default: false,
					LabelKey: "settings.fieldEnabled"},
				{Name: "prompt", Kind: FieldString, Default: "", Trim: true, Multiline: true,
					LabelKey: "settings.thinkingInjectionPrompt",
					HelpKey:  "settings.thinkingInjectionPromptHelp"},
			},
		},
		{
			Key:            "bottom_format_injection",
			TitleKey:       "settings.bottomFormatInjectionTitle",
			DescriptionKey: "settings.bottomFormatInjectionDesc",
			Fields: []FieldSpec{
				{Name: "enabled", Kind: FieldBool, Default: true,
					LabelKey: "settings.fieldEnabled"},
				{Name: "prompt", Kind: FieldString, Default: "", Trim: true, Multiline: true,
					LabelKey:       "settings.bottomFormatInjectionPrompt",
					HelpKey:        "settings.bottomFormatInjectionPromptHelp",
					PrefillDefault: true},
			},
		},
		{
			Key:            "input_file",
			TitleKey:       "settings.inputFileEnabled",
			DescriptionKey: "settings.inputFileDesc",
			Fields: []FieldSpec{
				{Name: "enabled", Kind: FieldBool, Default: false,
					LabelKey: "settings.fieldEnabled"},
				{Name: "min_chars", Kind: FieldInt, Default: 0, Min: 0, Max: 100000000,
					LabelKey: "settings.inputFileMinChars",
					HelpKey:  "settings.inputFileHelp"},
			},
		},
		// expert_prompt_segment is hidden: the settings page dropped the switch
		// when the feature was retired (the section component went away), and the
		// registry must keep it off the page the same way rather than letting the
		// generic renderer give it a card again. It stays registered so the config
		// keeps round-tripping and the settings API keeps reporting both fields.
		{
			Key:            "expert_prompt_segment",
			Hidden:         true,
			TitleKey:       "settings.expertPromptSegmentEnabled",
			DescriptionKey: "settings.expertPromptSegmentDesc",
			Fields: []FieldSpec{
				// Retired: the enable switch is forced off so a config file that
				// predates the retirement cannot turn segmentation back on.
				{Name: "enabled", Kind: FieldBool, Default: false, ForceValue: false,
					LabelKey: "settings.fieldEnabled"},
				{Name: "max_chars", Kind: FieldInt, Default: 160000, Min: 1000, Max: 100000000,
					LabelKey: "settings.expertPromptSegmentMaxChars",
					HelpKey:  "settings.expertPromptSegmentMaxCharsHelp"},
			},
		},
		// expert_text_file_inline is config-file only: the expert path reads it
		// but the settings page never exposed it, so registering it must not put
		// a control on the page.
		{
			Key:    "expert_text_file_inline",
			Hidden: true,
			Fields: []FieldSpec{
				{Name: "enabled", Kind: FieldBool, Default: true},
				{Name: "max_file_bytes", Kind: FieldInt, Default: 3 * 1024 * 1024, Min: 1, Max: 100 << 20},
			},
		},
		// auto_route_vision is retained for config compatibility. Retired, for the
		// same reason as expert_prompt_segment.
		{
			Key:    "auto_route_vision",
			Hidden: true,
			Fields: []FieldSpec{
				{Name: "enabled", Kind: FieldBool, Default: false, ForceValue: false},
			},
		},
	} {
		RegisterSetting(spec)
	}
	// The settings-page cards, in the order they appear on the page: the two
	// injection switches sit with the TTL and provider they are configured next
	// to, and the input-file split threshold gets a card of its own. The
	// remaining orders (1, 2 and 6) belong to the section cards declared in
	// section_admin.go, section_runtime.go and section_auto_delete.go.
	for _, card := range []SettingCardSpec{
		{
			Key:      "behavior",
			TitleKey: "settings.behaviorTitle",
			Order:    3,
			Settings: []string{"responses", "embeddings", "thinking_injection", "bottom_format_injection"},
		},
		{
			Key:            "input_file",
			TitleKey:       "settings.inputFileTitle",
			DescriptionKey: "settings.inputFileDesc",
			Order:          4,
			Settings:       []string{"input_file"},
		},
	} {
		RegisterSettingCard(card)
	}
}

// RegisterSettingCard declares a settings-page card. It panics on a malformed
// spec: every rejected shape below is a programming error that would otherwise
// surface as a setting rendered on the wrong card, on two cards, or nowhere.
func RegisterSettingCard(spec SettingCardSpec) {
	key := strings.TrimSpace(spec.Key)
	if key == "" {
		panic("config: setting card key must not be empty")
	}
	if strings.TrimSpace(spec.TitleKey) == "" {
		panic(fmt.Sprintf("config: setting card %q must declare a title locale key", key))
	}
	if len(spec.Settings) == 0 {
		panic(fmt.Sprintf("config: setting card %q names no settings", key))
	}
	settingCardMu.Lock()
	defer settingCardMu.Unlock()
	if _, dup := settingCardSpecs[key]; dup {
		panic(fmt.Sprintf("config: setting card %q is already registered", key))
	}
	for _, member := range spec.Settings {
		name := strings.TrimSpace(member)
		if name == "" {
			panic(fmt.Sprintf("config: setting card %q names a setting with an empty key", key))
		}
		// A card naming an unknown setting would silently render nothing, and a
		// hidden one would put a config-file-only setting on the settings page.
		setting, ok := LookupSetting(name)
		if !ok {
			panic(fmt.Sprintf("config: setting card %q names %q, which is not a registered setting", key, name))
		}
		if setting.Hidden {
			panic(fmt.Sprintf("config: setting card %q names %q, which is a hidden setting", key, name))
		}
		if owner, taken := settingCardOf[name]; taken {
			panic(fmt.Sprintf("config: setting %q is claimed by both card %q and card %q", name, owner, key))
		}
		settingCardOf[name] = key
	}
	spec.Key = key
	settingCardSpecs[key] = spec
	settingCardOrder = append(settingCardOrder, key)
}

// SettingCards returns the declared cards in declaration order.
func SettingCards() []SettingCardSpec {
	settingCardMu.RLock()
	defer settingCardMu.RUnlock()
	out := make([]SettingCardSpec, 0, len(settingCardOrder))
	for _, key := range settingCardOrder {
		out = append(out, settingCardSpecs[key])
	}
	return out
}

// SettingCardOf returns the card a registered setting renders in. A setting no
// card names reports false and renders in a card of its own.
func SettingCardOf(key string) (SettingCardSpec, bool) {
	settingCardMu.RLock()
	defer settingCardMu.RUnlock()
	card, ok := settingCardSpecs[settingCardOf[key]]
	return card, ok
}

// RegisterSetting declares a setting. It panics on a malformed spec: every
// rejected shape below is a programming error that would otherwise surface as a
// setting that silently never loads, never validates, or is written to the
// wrong place.
func RegisterSetting(spec SettingSpec) {
	key := strings.TrimSpace(spec.Key)
	if key == "" {
		panic("config: setting key must not be empty")
	}
	if IsSectionKey(key) {
		panic(fmt.Sprintf("config: setting key %q collides with the registered section of the same name", key))
	}
	if len(spec.Fields) == 0 {
		panic(fmt.Sprintf("config: setting %q declares no fields", key))
	}
	// A visible section is rendered by the web UI straight from this spec, so a
	// missing locale key would show up as a blank label rather than as a build
	// failure. A hidden section has no UI and needs no keys.
	if !spec.Hidden && strings.TrimSpace(spec.TitleKey) == "" {
		panic(fmt.Sprintf("config: visible setting %q must declare a title locale key", key))
	}
	fields := make(map[string]FieldSpec, len(spec.Fields))
	for _, f := range spec.Fields {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			panic(fmt.Sprintf("config: setting %q has a field with an empty name", key))
		}
		if _, dup := fields[name]; dup {
			panic(fmt.Sprintf("config: setting %q declares field %q twice", key, name))
		}
		if !spec.Hidden && strings.TrimSpace(f.LabelKey) == "" {
			panic(fmt.Sprintf("config: setting %q field %q must declare a label locale key", key, name))
		}
		if err := checkFieldSpec(key, f); err != nil {
			panic(err.Error())
		}
		f.Name = name
		fields[name] = f
	}
	settingMu.Lock()
	defer settingMu.Unlock()
	if _, dup := settingSpecs[key]; dup {
		panic(fmt.Sprintf("config: setting %q is already registered", key))
	}
	spec.Key = key
	settingSpecs[key] = spec
	fieldSpecsFor[key] = fields
	settingOrder = append(settingOrder, key)
}

func checkFieldSpec(key string, f FieldSpec) error {
	switch f.Kind {
	case FieldBool:
		if _, ok := f.Default.(bool); !ok {
			return fmt.Errorf("config: setting %q field %q must declare a bool default, got %T", key, f.Name, f.Default)
		}
		if f.ForceValue != nil {
			if _, ok := f.ForceValue.(bool); !ok {
				return fmt.Errorf("config: setting %q field %q must declare a bool ForceValue, got %T", key, f.Name, f.ForceValue)
			}
		}
	case FieldInt:
		def, ok := f.Default.(int)
		if !ok {
			return fmt.Errorf("config: setting %q field %q must declare an int default, got %T", key, f.Name, f.Default)
		}
		if def != 0 && (def < f.Min || def > f.Max) {
			return fmt.Errorf("config: setting %q field %q has default %d outside [%d, %d]", key, f.Name, def, f.Min, f.Max)
		}
		if f.ForceValue != nil {
			if _, ok := f.ForceValue.(int); !ok {
				return fmt.Errorf("config: setting %q field %q must declare an int ForceValue, got %T", key, f.Name, f.ForceValue)
			}
		}
	case FieldString:
		if _, ok := f.Default.(string); !ok {
			return fmt.Errorf("config: setting %q field %q must declare a string default, got %T", key, f.Name, f.Default)
		}
		if f.ForceValue != nil {
			if _, ok := f.ForceValue.(string); !ok {
				return fmt.Errorf("config: setting %q field %q must declare a string ForceValue, got %T", key, f.Name, f.ForceValue)
			}
		}
	default:
		return fmt.Errorf("config: setting %q field %q has unknown kind %q", key, f.Name, f.Kind)
	}
	return nil
}

// SettingSpecs returns the registered settings in declaration order, which is
// the order the settings API and the web UI report them in.
func SettingSpecs() []SettingSpec {
	settingMu.RLock()
	defer settingMu.RUnlock()
	out := make([]SettingSpec, 0, len(settingOrder))
	for _, key := range settingOrder {
		out = append(out, settingSpecs[key])
	}
	return out
}

// LookupSetting returns the declaration registered for key.
func LookupSetting(key string) (SettingSpec, bool) {
	settingMu.RLock()
	defer settingMu.RUnlock()
	spec, ok := settingSpecs[key]
	return spec, ok
}

// LookupSettingField returns the declaration of one field of a registered
// setting.
func LookupSettingField(key, field string) (FieldSpec, bool) {
	settingMu.RLock()
	defer settingMu.RUnlock()
	spec, ok := fieldSpecsFor[key][field]
	return spec, ok
}

// LookupSettingFieldFolded returns the declaration of the field of spec that
// matches name, preferring an exact match and falling back to a match that
// differs only in case.
//
// The fallback exists because the hand-written codec decoded a section straight
// into its struct, and encoding/json matches object keys against struct tags
// case-insensitively. A config file that spells `Enabled` rather than `enabled`
// therefore used to load; without the fallback the registry lookup would drop
// the field silently and the next save would erase the section from the file.
func LookupSettingFieldFolded(spec SettingSpec, name string) (FieldSpec, bool) {
	if f, ok := LookupSettingField(spec.Key, name); ok {
		return f, true
	}
	for _, f := range spec.Fields {
		if strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// IsSettingKey reports whether key names a registered setting. The codec uses
// it to decide whether a top-level config key belongs to the registry.
func IsSettingKey(key string) bool {
	settingMu.RLock()
	defer settingMu.RUnlock()
	_, ok := settingSpecs[key]
	return ok
}

// SettingKeys returns the registered keys in declaration order.
func SettingKeys() []string {
	settingMu.RLock()
	defer settingMu.RUnlock()
	return append([]string(nil), settingOrder...)
}

// SettingValues reports the effective value of every field of a registered
// setting: the stored value when the config carries one, the declared default
// otherwise. This is what the settings API hands to the web UI, so a field the
// user never touched is reported with the value it actually behaves as.
func (s *Store) SettingValues(key string) map[string]any {
	spec, ok := LookupSetting(key)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(spec.Fields))
	for _, f := range spec.Fields {
		out[f.Name] = s.settingValue(key, f)
	}
	return out
}

// SettingBool reads a bool field of a registered setting. An unregistered key
// or field reads as false rather than panicking: a stale caller after a setting
// is retired should lose the value, not take the process down.
func (s *Store) SettingBool(key, field string) bool {
	f, ok := LookupSettingField(key, field)
	if !ok {
		return false
	}
	v, _ := s.settingValue(key, f).(bool)
	return v
}

// SettingInt reads an int field of a registered setting.
func (s *Store) SettingInt(key, field string) int {
	f, ok := LookupSettingField(key, field)
	if !ok {
		return 0
	}
	v, _ := s.settingValue(key, f).(int)
	return v
}

// SettingString reads a string field of a registered setting.
func (s *Store) SettingString(key, field string) string {
	f, ok := LookupSettingField(key, field)
	if !ok {
		return ""
	}
	v, _ := s.settingValue(key, f).(string)
	return v
}

func (s *Store) settingValue(key string, f FieldSpec) any {
	if f.ForceValue != nil {
		return f.ForceValue
	}
	if s == nil {
		return f.Default
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	stored, ok := s.cfg.Settings[key][f.Name]
	if !ok {
		return f.Default
	}
	switch f.Kind {
	case FieldInt:
		// Zero means "unset", the same rule ValidateIntRange and the
		// hand-written accessors apply, so a stored 0 reads as the default
		// instead of disabling the setting.
		if n, ok := stored.(int); ok && n == 0 {
			return f.Default
		}
	case FieldString:
		str, _ := stored.(string)
		if f.Trim {
			return strings.TrimSpace(str)
		}
	}
	return stored
}

// CoerceSettingField validates one value coming from the admin settings API or
// a config import, and returns the canonical Go value to store. A nil value is
// reported as absent rather than coerced, so a partial update never resets a
// field it did not mention. A value that is present is validated as the value
// the user set: the "0 means unset" rule belongs to a *stored* config file and
// is applied on read (settingValue), not here.
func CoerceSettingField(spec SettingSpec, f FieldSpec, raw any) (any, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	name := spec.Key + "." + f.Name
	switch f.Kind {
	case FieldBool:
		v, err := coerceBool(raw)
		if err != nil {
			return nil, false, fmt.Errorf("%s must be a boolean", name)
		}
		return v, true, nil
	case FieldInt:
		v, err := coerceInt(raw)
		if err != nil {
			return nil, false, fmt.Errorf("%s must be an integer", name)
		}
		// The request holds this field, so 0 is a value the user chose, not the
		// "unset" sentinel: it is range-checked like any other. A field whose
		// declared minimum is positive therefore rejects an explicit 0, while
		// one whose range includes 0 (input_file.min_chars) keeps accepting it.
		if err := ValidateIntRange(name, v, f.Min, f.Max, true); err != nil {
			return nil, false, err
		}
		return v, true, nil
	case FieldString:
		// A trimmed field cannot distinguish "the user cleared it" from "the
		// user typed spaces", so the two behave identically: both are the unset
		// (empty) value. Rejecting the second would fail the whole settings
		// request over a value that means the same as an accepted one.
		return strings.TrimSpace(fmt.Sprintf("%v", raw)), true, nil
	}
	return nil, false, fmt.Errorf("%s has unknown kind %q", name, f.Kind)
}

func coerceBool(raw any) (bool, error) {
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, err
		}
		return parsed, nil
	}
	return false, fmt.Errorf("unsupported bool value %T", raw)
}

func coerceInt(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("not an integer: %v", v)
		}
		return int(v), nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, fmt.Errorf("empty")
		}
		return strconv.Atoi(trimmed)
	}
	return 0, fmt.Errorf("unsupported int value %T", raw)
}

// SetSetting stores one field of a registered setting, creating the section on
// first write.
func (c *Config) SetSetting(key, field string, value any) {
	if c.Settings == nil {
		c.Settings = map[string]map[string]any{}
	}
	if c.Settings[key] == nil {
		c.Settings[key] = map[string]any{}
	}
	c.Settings[key][field] = value
}

// ValidateRegisteredSettings checks every stored value of every registered
// setting, so a config file that carries an out-of-range value is rejected at
// load rather than at the point of use.
func ValidateRegisteredSettings(c Config) error {
	for _, spec := range SettingSpecs() {
		stored := c.Settings[spec.Key]
		for _, f := range spec.Fields {
			v, ok := stored[f.Name]
			if !ok {
				continue
			}
			switch f.Kind {
			case FieldInt:
				n, ok := v.(int)
				if !ok {
					return fmt.Errorf("%s.%s must be an integer", spec.Key, f.Name)
				}
				if err := ValidateIntRange(spec.Key+"."+f.Name, n, f.Min, f.Max, false); err != nil {
					return err
				}
			case FieldString:
				str, ok := v.(string)
				if !ok {
					return fmt.Errorf("%s.%s must be a string", spec.Key, f.Name)
				}
				if f.NonBlank {
					if err := ValidateTrimmedString(spec.Key+"."+f.Name, str, false); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// NormalizeRegisteredSettings applies the read-side normalization of every
// registered setting in place, so a stored value and the value a reader sees
// agree. It is what the hand-written NormalizeSettingsConfig used to do per
// section.
func NormalizeRegisteredSettings(c *Config) {
	if c == nil {
		return
	}
	for _, spec := range SettingSpecs() {
		for _, f := range spec.Fields {
			if f.Kind != FieldString || !f.Trim {
				continue
			}
			stored, ok := c.Settings[spec.Key][f.Name]
			if !ok {
				continue
			}
			str, ok := stored.(string)
			if !ok {
				continue
			}
			c.Settings[spec.Key][f.Name] = strings.TrimSpace(str)
		}
	}
}

// reservedConfigKeys used to list the top-level keys the codec handled by hand.
// Every one of them is now a registered section (sections.go), so the list is
// the registry itself and cannot drift from the codec:
// TestEveryCodecKeyIsRegistered fails when a codec case has no declaration.
