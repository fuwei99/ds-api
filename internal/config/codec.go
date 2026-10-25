package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

func (c Config) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	for k, v := range c.AdditionalFields {
		m[k] = v
	}
	// Registered sections (sections.go) declare their own encode condition, so a
	// section at its zero value stays out of the file instead of being frozen
	// into it. The registry also guarantees the key cannot collide with a
	// registered setting or with another section.
	encodeRegisteredSections(m, c)
	// Registered settings are written at the top level in the same shape a
	// hand-written section uses. Only the fields the config actually carries are
	// written, so an unset field stays unset instead of being frozen into the
	// file. json.Marshal sorts map keys, so the output stays deterministic.
	for _, key := range SettingKeys() {
		stored := c.Settings[key]
		if len(stored) == 0 {
			continue
		}
		section := make(map[string]any, len(stored))
		for field, value := range stored {
			section[field] = value
		}
		m[key] = section
	}
	return json.Marshal(m)
}

func (c *Config) UnmarshalJSON(b []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	c.AdditionalFields = map[string]any{}
	c.Settings = map[string]map[string]any{}
	for k, v := range raw {
		// A registered setting is decoded generically, field by field, in the
		// same shape a hand-written section uses. Checking the registries before
		// AdditionalFields is what makes a declared key a first-class config
		// field rather than an opaque passthrough value. A field neither spec
		// declares is dropped, which is what decoding a hand-written section
		// into its struct already did.
		if spec, ok := LookupSetting(k); ok {
			section, err := decodeSettingSection(spec, v)
			if err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
			if len(section) > 0 {
				c.Settings[k] = section
			}
			continue
		}
		if handled, err := decodeRegisteredSection(c, k, v); handled {
			if err != nil {
				return fmt.Errorf("invalid field %q: %w", k, err)
			}
			continue
		}
		var anyVal any
		if err := json.Unmarshal(v, &anyVal); err == nil {
			c.AdditionalFields[k] = anyVal
		}
	}
	c.NormalizeCredentials()
	return nil
}

// decodeSettingSection decodes one registered section from the config file,
// applying the declared field kinds. A value that does not match its declared
// kind is an error rather than a silently ignored field: the setting would
// otherwise load as its default and the user would have no way to notice.
//
// A field name is resolved exactly first and case-insensitively after that (see
// LookupSettingFieldFolded), because the hand-written sections this registry
// replaced decoded into structs, whose fields encoding/json matches
// case-insensitively. A field neither spelling resolves is dropped, which is
// what the struct decode did too, but it is reported: the value would otherwise
// disappear from the file on the next save with nothing to notice it by.
//
// An explicit JSON null means "this field is not set", not "set to the zero
// value": the hand-written sections this registry replaced held pointer fields
// for their switches, so a null left the pointer nil and read as the declared
// default. Decoding null into the plain value of a declared field would instead
// store a false or a 0, which for a field whose default is true
// (bottom_format_injection.enabled) turns the switch off. A null field is
// therefore left out of the stored section, and a section whose fields are all
// null is not stored at all — exactly what the pointer version did.
func decodeSettingSection(spec SettingSpec, raw json.RawMessage) (map[string]any, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(fields))
	for name, fieldRaw := range fields {
		f, ok := LookupSettingFieldFolded(spec, name)
		if !ok {
			warnDroppedConfigField(spec.Key, name)
			continue
		}
		if isJSONNull(fieldRaw) {
			continue
		}
		var err error
		switch f.Kind {
		case FieldBool:
			var v bool
			err = json.Unmarshal(fieldRaw, &v)
			out[f.Name] = v
		case FieldInt:
			var v int
			err = json.Unmarshal(fieldRaw, &v)
			out[f.Name] = v
		case FieldString:
			var v string
			err = json.Unmarshal(fieldRaw, &v)
			out[f.Name] = v
		default:
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return out, nil
}

// warnDroppedConfigField reports a config-file field this version does not
// declare. The field is dropped — the same thing the hand-written struct decode
// did — but a dropped field is erased from the file on the next save, so it is
// worth one line in the log rather than silence.
func warnDroppedConfigField(section, field string) {
	if Logger == nil {
		return
	}
	Logger.Warn("[config] unknown config field ignored", "section", section, "field", field)
}

// isJSONNull reports whether a decoded config value is the JSON literal null.
func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func (c Config) Clone() Config {
	// The typed sections are copied by the registry (sections.go), so a section
	// added there is deep-copied without a second edit here. Everything else is
	// a value, or is replaced below.
	clone := c
	clone.AdditionalFields = map[string]any{}
	clone.Settings = cloneSettings(c.Settings)
	cloneRegisteredSections(&clone, c)
	for k, v := range c.AdditionalFields {
		clone.AdditionalFields[k] = v
	}
	return clone
}

// cloneAPIKeys deep-copies the key list. Each key carries a flags map, so a
// shallow slice copy would let a caller mutating a snapshot's flags reach back
// into the store.
func cloneAPIKeys(in []APIKey) []APIKey {
	if len(in) == 0 {
		return nil
	}
	out := make([]APIKey, len(in))
	for i, item := range in {
		out[i] = NormalizeAPIKey(item)
	}
	return out
}

// cloneMihomoConfig 深拷贝订阅/节点/端口映射切片与 map。
// 节点的 Raw map 解析完成后不再原地修改（总是整体替换），因此共享引用。
func cloneMihomoConfig(in MihomoConfig) MihomoConfig {
	out := MihomoConfig{
		Enabled:    in.Enabled,
		BinaryPath: in.BinaryPath,
		BasePort:   in.BasePort,
		APIPort:    in.APIPort,
		AutoBind:   in.AutoBind,
	}
	if len(in.Subscriptions) > 0 {
		out.Subscriptions = make([]MihomoSubscription, len(in.Subscriptions))
		for i, sub := range in.Subscriptions {
			out.Subscriptions[i] = MihomoSubscription{
				ID:        sub.ID,
				Name:      sub.Name,
				URL:       sub.URL,
				UpdatedAt: sub.UpdatedAt,
				Nodes:     slices.Clone(sub.Nodes),
			}
		}
	}
	if len(in.PortMap) > 0 {
		out.PortMap = make(map[string]int, len(in.PortMap))
		for k, v := range in.PortMap {
			out.PortMap[k] = v
		}
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

// cloneSettings deep-copies the registered-setting store. The values are bool,
// int and string, so a shallow copy per section is enough.
func cloneSettings(in map[string]map[string]any) map[string]map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]any, len(in))
	for section, fields := range in {
		if len(fields) == 0 {
			continue
		}
		copied := make(map[string]any, len(fields))
		for field, value := range fields {
			copied[field] = value
		}
		out[section] = copied
	}
	return out
}

func parseConfigString(raw string) (Config, error) {
	var cfg Config
	candidates := []string{raw}
	if normalized := normalizeConfigInput(raw); normalized != raw {
		candidates = append(candidates, normalized)
	}
	for _, candidate := range candidates {
		if err := json.Unmarshal([]byte(candidate), &cfg); err == nil {
			return cfg, nil
		}
	}

	base64Input := candidates[len(candidates)-1]
	decoded, err := decodeConfigBase64(base64Input)
	if err != nil {
		return Config{}, fmt.Errorf("invalid DS2API_CONFIG_JSON: %w", err)
	}
	if err := json.Unmarshal(decoded, &cfg); err != nil {
		return Config{}, fmt.Errorf("invalid DS2API_CONFIG_JSON decoded JSON: %w", err)
	}
	return cfg, nil
}

func normalizeConfigInput(raw string) string {
	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return normalized
	}
	for {
		changed := false
		if len(normalized) >= 2 {
			first := normalized[0]
			last := normalized[len(normalized)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				normalized = strings.TrimSpace(normalized[1 : len(normalized)-1])
				changed = true
			}
		}
		if strings.HasPrefix(strings.ToLower(normalized), "base64:") {
			normalized = strings.TrimSpace(normalized[len("base64:"):])
			changed = true
		}
		if !changed {
			break
		}
	}
	return strings.TrimSpace(normalized)
}

func decodeConfigBase64(raw string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(raw)
		if err == nil {
			return decoded, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("base64 decode failed")
}
