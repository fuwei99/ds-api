package config

import (
	"slices"
	"strings"
)

func (c *Config) ReconcileCredentials(base Config) {
	if c == nil {
		return
	}
	currKeys := normalizeKeys(c.Keys)
	currAPIKeys := normalizeAPIKeys(c.APIKeys)
	baseKeys := normalizeKeys(base.Keys)
	baseAPIKeys := normalizeAPIKeys(base.APIKeys)

	keysChanged := !slices.Equal(currKeys, baseKeys)
	apiKeysChanged := !equalAPIKeys(currAPIKeys, baseAPIKeys)

	if keysChanged && !apiKeysChanged {
		c.APIKeys = apiKeysFromStrings(currKeys, apiKeyMap(baseAPIKeys))
	} else {
		c.APIKeys = currAPIKeys
	}
	c.Keys = apiKeysToStrings(c.APIKeys)
}

func normalizeKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	out := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeAPIKeys(items []APIKey) []APIKey {
	if len(items) == 0 {
		return nil
	}
	out := make([]APIKey, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, NormalizeAPIKey(item))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NormalizeAPIKey trims the typed fields and deep-copies the credential flags,
// so two normalized keys never share a flags map. It is the single place that
// decides how a key is stored, which is why the admin endpoints call it rather
// than rebuilding the struct field by field.
func NormalizeAPIKey(item APIKey) APIKey {
	return APIKey{
		Key:    strings.TrimSpace(item.Key),
		Name:   strings.TrimSpace(item.Name),
		Remark: strings.TrimSpace(item.Remark),
		flags:  cloneCredentialFlags(item.flags),
	}
}

// MergeCredentialFlags folds incoming's flags into existing according to each
// flag's declared merge policy: a restrictive flag (one whose `true` removes a
// capability) is on if either side has it on, so a config import cannot silently
// drop a restriction the user already set. A non-restrictive flag takes
// incoming's value — the imported record — rather than the record the caller
// chose to keep for the key's name and remark.
//
// The result owns its flags map: neither argument is modified.
func MergeCredentialFlags(existing, incoming APIKey) APIKey {
	out := existing
	out.flags = cloneCredentialFlags(existing.flags)
	for _, spec := range CredentialFlagSpecs() {
		if spec.Restrictive {
			out.SetFlag(spec.Name, existing.Flag(spec.Name) || incoming.Flag(spec.Name))
			continue
		}
		out.SetFlag(spec.Name, incoming.Flag(spec.Name))
	}
	return out
}

func cloneCredentialFlags(flags map[string]bool) map[string]bool {
	if len(flags) == 0 {
		return nil
	}
	out := make(map[string]bool, len(flags))
	for name, value := range flags {
		out[name] = value
	}
	return out
}

func apiKeysFromStrings(keys []string, meta map[string]APIKey) []APIKey {
	if len(keys) == 0 {
		return nil
	}
	out := make([]APIKey, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if item, ok := meta[key]; ok {
			out = append(out, NormalizeAPIKey(item))
			continue
		}
		out = append(out, APIKey{Key: key})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func apiKeysToStrings(items []APIKey) []string {
	if len(items) == 0 {
		return nil
	}
	keys := make([]string, 0, len(items))
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}

func apiKeyMap(items []APIKey) map[string]APIKey {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]APIKey, len(items))
	for _, item := range items {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			continue
		}
		if _, ok := out[key]; ok {
			continue
		}
		out[key] = NormalizeAPIKey(item)
	}
	return out
}

func equalAPIKeys(a, b []APIKey) bool {
	if len(a) != len(b) {
		return false
	}
	return slices.EqualFunc(a, b, APIKeyEqual)
}

// APIKeyEqual reports whether two keys are the same stored credential: the same
// trimmed identity and metadata, and the same registered flags.
func APIKeyEqual(x, y APIKey) bool {
	return strings.TrimSpace(x.Key) == strings.TrimSpace(y.Key) &&
		strings.TrimSpace(x.Name) == strings.TrimSpace(y.Name) &&
		strings.TrimSpace(x.Remark) == strings.TrimSpace(y.Remark) &&
		equalCredentialFlags(x.flags, y.flags)
}

// equalCredentialFlags compares two flag sets by the registered flags rather
// than by raw map contents, so a flag that was retired and left behind in a
// loaded config does not read as a change.
func equalCredentialFlags(a, b map[string]bool) bool {
	for _, spec := range CredentialFlagSpecs() {
		av, aok := a[spec.Name]
		bv, bok := b[spec.Name]
		if aok != bok || av != bv {
			return false
		}
	}
	return true
}
