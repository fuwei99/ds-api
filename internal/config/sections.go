package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
)

// A registered section is a top-level config key that needs hand-written
// semantics: a nested object, an enum, a derived value, a record list. Those are
// the shapes a registered setting (settings.go) deliberately does not cover, and
// this registry is what keeps them from being wired one file at a time.
//
// A section is declared once, in the registry below, and everything else is
// derived from that declaration:
//
//   - the config-file codec: decode, encode and Clone,
//   - validation and read-side normalization of the stored value,
//   - the config-import merge, for a section that declares a form,
//   - the admin settings read and write API,
//   - the web UI form, which the settings API describes field by field,
//     including nested groups and enums.
//
// Adding or changing a section is therefore:
//
//  1. a RegisterSection entry — its own file, holding the section's Go type
//     when the section is new, and
//  2. the code that reads it: a Store accessor, or the section's own admin
//     endpoints when its UI is bespoke (the account manager, the Vercel sync
//     page, the device-id pool dialog), plus the locale keys the declaration
//     names.
//
// The section's Go type and its behaviour stay hand-written; the registry owns
// the plumbing, not the semantics. A section that is a list of records with its
// own CRUD endpoints (accounts, api_keys, device_id_pool) registers for the
// codec and the lifecycle hooks and keeps those endpoints; a section the
// settings page edits declares a Form and gets its read, write and rendering for
// free.
type SectionSpec struct {
	// Key is the config-file key, e.g. "runtime". It must not collide with a
	// registered setting or another section.
	Key string
	// Form describes the settings-page card and the fields the settings API
	// reads and writes. A nil Form means the section is not on the settings
	// page at all: it is edited by its own endpoints, or only in the config
	// file.
	Form *SectionForm
	// Decode reads the section out of the config file. It is the only required
	// hook: a section the codec cannot read would be a silently ignored key.
	Decode func(c *Config, raw json.RawMessage) error
	// Encode returns the section's config-file value. ok=false omits the key
	// entirely, which is how an untouched section stays out of the file. A nil
	// Encode marks a key the codec still reads but never writes — a removed or
	// renamed section that is kept only so old config files do not fail.
	Encode func(c Config) (any, bool)
	// Clone deep-copies the section from src into dst. A nil Clone means the
	// section holds nothing that needs copying.
	Clone func(dst, src *Config)
	// Normalize canonicalizes the section in place on every load and every
	// store update.
	Normalize func(c *Config)
	// Validate checks the stored section. A config file that carries an invalid
	// value is rejected at load rather than at the point of use.
	Validate func(c Config) error
	// Merge folds an imported config's section into next. A nil Merge with a
	// Form means the fields the imported section carries are applied field by
	// field, exactly as a settings update applies them; a nil Merge without a
	// Form means the import leaves the section alone (a record list merges
	// through its own endpoints).
	Merge func(next, incoming *Config) error
}

// SectionForm describes a section on the settings page. The settings API reads
// it through each field's reader and writes it through each field's applier, so
// a section declares what it looks like once and both sides follow.
type SectionForm struct {
	// TitleKey and DescriptionKey are web UI locale keys for the section card.
	TitleKey       string
	DescriptionKey string
	// Order places the card on the settings page: a smaller value comes first,
	// and two sections that declare the same value keep their declaration order.
	// It exists because a card's position is a layout decision, not a property of
	// the file the declaration happens to live in — the settings page is the one
	// place where declaration order alone would make a filename load-bearing.
	Order int
	// Bespoke marks a section whose card is rendered by its own web UI
	// component (the security card's password flow). Its fields still travel
	// through the settings API, so its values round-trip like any other
	// section's; the generic renderer leaves the card alone.
	Bespoke bool
	// Fields are the section's fields, in the order the web UI renders them.
	Fields []SectionField
	// RemovedFields names fields this section used to declare and no longer
	// accepts, mapped to the field that replaced them. A settings update that
	// still carries one is rejected with an error naming the replacement: a
	// write the server cannot honour must say so, not return success having
	// changed nothing. It says nothing about the config file — a legacy key
	// there is read by the section's own Decode, so an old file keeps loading.
	RemovedFields map[string]string
	// Extra reports section-level values that are not form fields — UI metadata
	// such as admin.has_password_hash, which the settings API reports next to
	// the fields but no control edits.
	Extra func(s *Store) map[string]any
}

// SectionField declares one field of a section form. A leaf field (bool, int,
// string, enum) reads through Read and writes through Apply; a FieldGroup nests
// one level of declared fields and needs neither.
type SectionField struct {
	// Name is the JSON key of the field inside the section, e.g. "enabled".
	Name string
	// Kind is the field's value type. It decides how a value is coerced on
	// write and which input the web UI renders.
	Kind FieldKind
	// LabelKey and HelpKey are web UI locale keys. Keeping the keys here rather
	// than the text keeps translation in the locale files, and lets the web UI
	// render a new field with no component change. A FieldGroup may leave
	// LabelKey empty: a group that only binds a sub-feature's switch to the
	// settings it governs has no name of its own, and the web UI then renders
	// its fields straight into the card instead of printing a header that would
	// repeat the switch's own label.
	LabelKey string
	HelpKey  string
	// Min and Max bound an int field, on write and in the rendered input.
	Min int
	Max int
	// Multiline renders a textarea instead of a single-line input.
	Multiline bool
	// Options are the accepted values of a FieldEnum field, in render order.
	Options []SectionOption
	// RenamedValues maps a value this field used to accept onto the option that
	// replaced it. It exists so renaming an enum value stays a migration instead
	// of a breaking change: a write that still carries the old name — a stale
	// client, or a merge import of a config saved before the rename — is applied
	// as the new one. The old name is deliberately not an option, so it is never
	// rendered and the config is written back under the new name. Lookup uses the
	// same trimmed, lowercase form the enum check does; withRenamedValues
	// normalizes the keys for a declaration.
	RenamedValues map[string]string
	// Fields are the declared fields of a FieldGroup.
	Fields []SectionField
	// Read reports the field's effective value for the settings API: the value
	// the field behaves as, not necessarily the value the config file carries
	// (an environment fallback, a derived recommendation, a forced value).
	Read func(s *Store) any
	// Apply merges a value the request carried into the config. It is called
	// only when the request holds the field, so an absent field is left
	// untouched. It reports a user-facing error naming the field.
	Apply func(c *Config, raw any) error
}

// SectionOption is one accepted value of a FieldEnum field.
type SectionOption struct {
	Value string
	// LabelKey is the option's label in the select control.
	LabelKey string
	// DescriptionKey, when set, is shown below the control while this option is
	// selected.
	DescriptionKey string
	// WarningKey, when set, is shown as a warning while this option is
	// selected.
	WarningKey string
}

var (
	sectionMu    sync.RWMutex
	sectionSpecs = map[string]SectionSpec{}
	sectionOrder []string
)

// RegisterSection declares a section. It panics on a malformed spec: every
// rejected shape below is a programming error that would otherwise surface as a
// section that silently never loads, never saves, or renders a blank control.
func RegisterSection(spec SectionSpec) {
	key := strings.TrimSpace(spec.Key)
	if key == "" {
		panic("config: section key must not be empty")
	}
	if IsSettingKey(key) {
		panic(fmt.Sprintf("config: section key %q collides with the registered setting of the same name", key))
	}
	if spec.Decode == nil {
		panic(fmt.Sprintf("config: section %q declares no Decode, so the config file could not be read", key))
	}
	if spec.Form != nil {
		if strings.TrimSpace(spec.Form.TitleKey) == "" {
			panic(fmt.Sprintf("config: section %q declares a form without a title locale key", key))
		}
		for _, f := range spec.Form.Fields {
			if err := checkSectionField(key, f); err != nil {
				panic(err.Error())
			}
		}
		// A removed field that is also a declared one would make that field
		// impossible to write, which is the opposite of what either declaration
		// says. An empty name would match nothing and silently do nothing.
		for name := range spec.Form.RemovedFields {
			if strings.TrimSpace(name) == "" {
				panic(fmt.Sprintf("config: section %q declares a removed field with an empty name", key))
			}
			if declaredSectionField(spec.Form.Fields, name) {
				panic(fmt.Sprintf("config: section %q declares %q as both a field and a removed field", key, name))
			}
		}
	}
	sectionMu.Lock()
	defer sectionMu.Unlock()
	if _, dup := sectionSpecs[key]; dup {
		panic(fmt.Sprintf("config: section %q is already registered", key))
	}
	spec.Key = key
	sectionSpecs[key] = spec
	sectionOrder = append(sectionOrder, key)
}

// declaredSectionField reports whether fields declares name at the top level.
// Nested group fields are not considered: a removed field is a top-level key of
// the section object, which is where a settings update carries it.
func declaredSectionField(fields []SectionField, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func checkSectionField(section string, f SectionField) error {
	name := strings.TrimSpace(f.Name)
	if name == "" {
		return fmt.Errorf("config: section %q has a field with an empty name", section)
	}
	if strings.TrimSpace(f.LabelKey) == "" && f.Kind != FieldGroup {
		return fmt.Errorf("config: section %q field %q must declare a label locale key", section, name)
	}
	switch f.Kind {
	case FieldGroup:
		if len(f.Fields) == 0 {
			return fmt.Errorf("config: section %q field %q is a group with no fields", section, name)
		}
		for _, nested := range f.Fields {
			if err := checkSectionField(section, nested); err != nil {
				return err
			}
		}
		return nil
	case FieldEnum:
		if len(f.Options) < 2 {
			return fmt.Errorf("config: section %q field %q is an enum with fewer than two options", section, name)
		}
		for _, opt := range f.Options {
			if strings.TrimSpace(opt.Value) == "" {
				return fmt.Errorf("config: section %q field %q has an option with an empty value", section, name)
			}
			if strings.TrimSpace(opt.LabelKey) == "" {
				return fmt.Errorf("config: section %q field %q option %q must declare a label locale key", section, name, opt.Value)
			}
		}
	case FieldBool, FieldInt, FieldString:
	default:
		return fmt.Errorf("config: section %q field %q has unknown kind %q", section, name, f.Kind)
	}
	if f.Read == nil {
		return fmt.Errorf("config: section %q field %q declares no Read, so the settings API could not report it", section, name)
	}
	if f.Apply == nil {
		return fmt.Errorf("config: section %q field %q declares no Apply, so a settings update could not write it", section, name)
	}
	return nil
}

// SectionSpecs returns the registered sections in declaration order.
func SectionSpecs() []SectionSpec {
	sectionMu.RLock()
	defer sectionMu.RUnlock()
	out := make([]SectionSpec, 0, len(sectionOrder))
	for _, key := range sectionOrder {
		out = append(out, sectionSpecs[key])
	}
	return out
}

// SectionKeys returns the registered keys in declaration order.
func SectionKeys() []string {
	sectionMu.RLock()
	defer sectionMu.RUnlock()
	return append([]string(nil), sectionOrder...)
}

// LookupSection returns the declaration registered for key.
func LookupSection(key string) (SectionSpec, bool) {
	sectionMu.RLock()
	defer sectionMu.RUnlock()
	spec, ok := sectionSpecs[key]
	return spec, ok
}

// IsSectionKey reports whether key names a registered section. The codec uses it
// to decide whether a top-level config key belongs to the registry, and
// RegisterSetting uses it to refuse a name a section already owns.
func IsSectionKey(key string) bool {
	sectionMu.RLock()
	defer sectionMu.RUnlock()
	_, ok := sectionSpecs[key]
	return ok
}

// forEachSection walks the registry without copying it, for the codec paths that
// run on every config load and every snapshot.
func forEachSection(fn func(SectionSpec)) {
	sectionMu.RLock()
	defer sectionMu.RUnlock()
	for _, key := range sectionOrder {
		fn(sectionSpecs[key])
	}
}

// decodeRegisteredSection hands one config-file key to its section. handled
// reports whether the key is registered at all, which is what keeps an unknown
// key falling through to AdditionalFields.
func decodeRegisteredSection(c *Config, key string, raw json.RawMessage) (handled bool, err error) {
	spec, ok := LookupSection(key)
	if !ok {
		return false, nil
	}
	if spec.Decode == nil {
		return true, nil
	}
	return true, spec.Decode(c, raw)
}

// encodeRegisteredSections writes every registered section that reports itself
// as set. A section at its zero value stays out of the file, so an untouched
// config keeps the shape it had before the section existed.
func encodeRegisteredSections(m map[string]any, c Config) {
	forEachSection(func(spec SectionSpec) {
		if spec.Encode == nil {
			return
		}
		if value, ok := spec.Encode(c); ok {
			m[spec.Key] = value
		}
	})
}

func cloneRegisteredSections(dst *Config, src Config) {
	forEachSection(func(spec SectionSpec) {
		if spec.Clone != nil {
			spec.Clone(dst, &src)
		}
	})
}

func normalizeRegisteredSections(c *Config) {
	forEachSection(func(spec SectionSpec) {
		if spec.Normalize != nil {
			spec.Normalize(c)
		}
	})
}

func validateRegisteredSections(c Config) error {
	for _, spec := range SectionSpecs() {
		if spec.Validate == nil {
			continue
		}
		if err := spec.Validate(c); err != nil {
			return err
		}
	}
	return nil
}

// SectionValues reports the effective value of every field of a registered
// section: what the settings API hands to the web UI, and what a client reads
// back. Each field is read through its own reader, so a value the config does
// not carry is reported as the value it behaves as rather than as zero.
func (s *Store) SectionValues(key string) map[string]any {
	spec, ok := LookupSection(key)
	if !ok || spec.Form == nil {
		return nil
	}
	out := map[string]any{}
	readSectionFields(s, spec.Form.Fields, out)
	if spec.Form.Extra != nil {
		for name, value := range spec.Form.Extra(s) {
			out[name] = value
		}
	}
	return out
}

func readSectionFields(s *Store, fields []SectionField, out map[string]any) {
	for _, f := range fields {
		if f.Kind == FieldGroup {
			group := map[string]any{}
			readSectionFields(s, f.Fields, group)
			out[f.Name] = group
			continue
		}
		if f.Read != nil {
			out[f.Name] = f.Read(s)
		}
	}
}

// SectionUpdates reports the sections a settings update carries, so the write
// path knows what to apply and which post-write hooks to run without naming any
// section. A key the registry does not declare is ignored, which is what the
// hand-written parsers did.
func SectionUpdates(req map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, spec := range SectionSpecs() {
		if spec.Form == nil {
			continue
		}
		if raw, ok := req[spec.Key].(map[string]any); ok {
			out[spec.Key] = raw
		}
	}
	return out
}

// ApplySections merges the sections a settings update carries into c. Only the
// fields the request actually holds are applied, so a partial update never
// resets a field it did not mention — the rule the hand-written parsers
// followed, now derived from the declaration. A field the section removed is
// rejected rather than ignored, so a stale client is told instead of getting a
// success for a write that changed nothing.
func ApplySections(c *Config, updates map[string]map[string]any) error {
	for _, spec := range SectionSpecs() {
		if spec.Form == nil {
			continue
		}
		raw, ok := updates[spec.Key]
		if !ok {
			continue
		}
		if err := rejectRemovedFields(spec, raw); err != nil {
			return err
		}
		if err := applySectionFields(c, spec.Key, spec.Form.Fields, raw); err != nil {
			return err
		}
	}
	return nil
}

// rejectRemovedFields refuses a settings update that still carries a field its
// section no longer declares, naming the field that replaced it. Fields are
// checked in a stable order so the error does not depend on map iteration.
func rejectRemovedFields(spec SectionSpec, raw map[string]any) error {
	if len(spec.Form.RemovedFields) == 0 {
		return nil
	}
	removed := make([]string, 0, len(spec.Form.RemovedFields))
	for name := range spec.Form.RemovedFields {
		removed = append(removed, name)
	}
	sort.Strings(removed)
	for _, name := range removed {
		if _, present := raw[name]; !present {
			continue
		}
		return fmt.Errorf("%s.%s is no longer accepted; use %s.%s instead",
			spec.Key, name, spec.Key, spec.Form.RemovedFields[name])
	}
	return nil
}

// ValidateAppliedSections checks the sections a settings update touched, on the
// merged config, so a value that is only invalid next to what the config already
// carries (global_max_inflight below account_max_inflight) is rejected before
// the update is committed.
func ValidateAppliedSections(c Config, updates map[string]map[string]any) error {
	for _, spec := range SectionSpecs() {
		if _, touched := updates[spec.Key]; !touched || spec.Validate == nil {
			continue
		}
		if err := spec.Validate(c); err != nil {
			return err
		}
	}
	return nil
}

func applySectionFields(c *Config, section string, fields []SectionField, raw map[string]any) error {
	for _, f := range fields {
		value, present := raw[f.Name]
		if !present {
			continue
		}
		if f.Kind == FieldGroup {
			nested, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s must be an object", section, f.Name)
			}
			if err := applySectionFields(c, section, f.Fields, nested); err != nil {
				return err
			}
			continue
		}
		if f.Apply == nil {
			continue
		}
		if renamed, ok := f.RenamedValues[normalizedFieldValue(value)]; ok {
			value = renamed
		}
		if err := f.Apply(c, value); err != nil {
			return err
		}
	}
	return nil
}

// MergeRegisteredSections folds an imported config into next. A section that
// declares Merge follows its own rule; a section with a form and no Merge is
// merged field by field, through the same appliers a settings update uses, so an
// imported section cannot drift from what the settings page saves. A section
// with neither is left alone: a record list merges through its own endpoints.
func MergeRegisteredSections(next *Config, incoming Config) error {
	for _, spec := range SectionSpecs() {
		if spec.Merge != nil {
			if err := spec.Merge(next, &incoming); err != nil {
				return err
			}
			continue
		}
		if spec.Form == nil || spec.Encode == nil {
			continue
		}
		if err := mergeSectionFields(next, incoming, spec); err != nil {
			return err
		}
	}
	return nil
}

// mergeSectionFields applies the fields the incoming section reports as set.
// The incoming config is encoded by the section's own encoder, so "what the
// import carries" is exactly "what the config file would hold" — a field at its
// zero value is absent and is therefore not imported over the current value.
func mergeSectionFields(next *Config, incoming Config, spec SectionSpec) error {
	encoded, ok := spec.Encode(incoming)
	if !ok {
		return nil
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		// The section is not a JSON object (a scalar or a list); there is no
		// field-wise merge to do.
		return nil
	}
	return applySectionFields(next, spec.Key, spec.Form.Fields, fields)
}

// SectionGroup declares a nested object field of a section form. The group's own
// fields are declared inside it, and both the settings read and the settings
// write walk them, so a nested object needs no code of its own.
//
// An empty labelKey declares a group that has no header: its fields render into
// the card, which is what a sub-feature whose switch already carries the name
// wants (see SectionField.LabelKey).
func SectionGroup(path, labelKey string, fields ...SectionField) SectionField {
	return SectionField{
		Name:     sectionFieldName(path),
		Kind:     FieldGroup,
		LabelKey: labelKey,
		Fields:   fields,
	}
}

// SectionBool declares a boolean field of a section form: it reads through
// read and writes through apply, which is called only when a request carries
// the field. path is the field's full name ("runtime.mute_report.enabled"),
// which is what the config file, the web UI and the error messages use.
func SectionBool(path, labelKey string, read func(s *Store) bool, apply func(c *Config, v bool)) SectionField {
	return SectionField{
		Name:     sectionFieldName(path),
		Kind:     FieldBool,
		LabelKey: labelKey,
		Read:     func(s *Store) any { return read(s) },
		Apply: func(c *Config, raw any) error {
			v, err := coerceBool(raw)
			if err != nil {
				return fmt.Errorf("%s must be a boolean", path)
			}
			apply(c, v)
			return nil
		},
	}
}

// SectionInt declares a bounded integer field of a section form. A value a
// request carries must be an integer inside [min, max]; zero is not accepted as
// "unset" here, because a field the request mentions is a field the user set.
func SectionInt(path, labelKey string, min, max int, read func(s *Store) int, apply func(c *Config, v int)) SectionField {
	return SectionField{
		Name:     sectionFieldName(path),
		Kind:     FieldInt,
		LabelKey: labelKey,
		Min:      min,
		Max:      max,
		Read:     func(s *Store) any { return read(s) },
		Apply: func(c *Config, raw any) error {
			v, err := coerceInt(raw)
			if err != nil {
				return fmt.Errorf("%s must be an integer", path)
			}
			if err := ValidateIntRange(path, v, min, max, true); err != nil {
				return err
			}
			apply(c, v)
			return nil
		},
	}
}

// SectionOptionalInt declares an integer field whose "unset" state is a state of
// its own: read reports (value, ok), and a field without a value is sent to the
// web UI as null, which renders an empty box rather than a 0 the user never
// typed (the web UI's IntFieldInput shows null as empty and leaves it out of the
// save payload, so an empty box keeps the stored value instead of writing one).
// Use it for a setting whose default is "not filled in", where a 0 would read as
// a number the operator chose; a field whose default is a real number uses
// SectionInt and ships that default.
func SectionOptionalInt(path, labelKey string, min, max int, read func(s *Store) (int, bool), apply func(c *Config, v int)) SectionField {
	field := SectionInt(path, labelKey, min, max, func(s *Store) int {
		v, _ := read(s)
		return v
	}, apply)
	field.Read = func(s *Store) any {
		v, ok := read(s)
		if !ok {
			return nil
		}
		return v
	}
	return field
}

// SectionString declares a free-text field of a section form. The value is
// trimmed, which is what every hand-written accessor for such a field does.
func SectionString(path, labelKey string, read func(s *Store) string, apply func(c *Config, v string)) SectionField {
	return SectionField{
		Name:     sectionFieldName(path),
		Kind:     FieldString,
		LabelKey: labelKey,
		Read:     func(s *Store) any { return read(s) },
		Apply: func(c *Config, raw any) error {
			apply(c, strings.TrimSpace(fmt.Sprintf("%v", raw)))
			return nil
		},
	}
}

// SectionEnum declares a field whose value is one of a declared set. A
// non-empty value that is not one of the options is rejected here, so the
// section's apply hook only has to decide what an empty value means: some enums
// accept "unset" and some do not.
func SectionEnum(path, labelKey string, options []SectionOption, read func(s *Store) string, apply func(c *Config, v string) error) SectionField {
	values := make([]string, 0, len(options))
	for _, opt := range options {
		values = append(values, opt.Value)
	}
	allowed := strings.Join(values, ", ")
	return SectionField{
		Name:     sectionFieldName(path),
		Kind:     FieldEnum,
		LabelKey: labelKey,
		Options:  options,
		Read:     func(s *Store) any { return read(s) },
		Apply: func(c *Config, raw any) error {
			v := normalizedFieldValue(raw)
			if v != "" && !slices.Contains(values, v) {
				return fmt.Errorf("%s must be one of %s", path, allowed)
			}
			return apply(c, v)
		},
	}
}

// withRenamedValues attaches renamed values to a declared field, so a field
// helper's call stays one expression (the same shape as withHelp for help keys).
// Keys are normalized the way the value is compared, so a declaration does not
// have to spell the old name in the casing the enum check uses.
func withRenamedValues(f SectionField, renamed map[string]string) SectionField {
	if len(renamed) == 0 {
		return f
	}
	normalized := make(map[string]string, len(renamed))
	for old, current := range renamed {
		normalized[normalizedFieldValue(old)] = current
	}
	f.RenamedValues = normalized
	return f
}

// normalizedFieldValue reduces an incoming field value to the form every enum
// comparison uses: a trimmed, lowercased string.
func normalizedFieldValue(raw any) string {
	return strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", raw)))
}

// sectionFieldName is the field's JSON key inside its section: the last segment
// of its declared path.
func sectionFieldName(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
