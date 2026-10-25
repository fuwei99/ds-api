package config

import (
	"fmt"
	"strings"
	"sync"
)

// A credential flag is a boolean carried by one API key (`api_keys` entry),
// as opposed to a registered setting, which is global. It is declared once, in
// the registry below, and everything else is derived from that declaration:
//
//   - the config-file codec of APIKey (decode, encode and Clone),
//   - credential normalization, deduplication and equality,
//   - the O(1) per-key lookup index the request path reads,
//   - the admin key endpoints (add, update, config import and merge),
//   - the web UI, which renders one toggle and one list badge per declaration.
//
// Adding or changing a flag is therefore:
//
//  1. a RegisterCredentialFlag entry in this file, and
//  2. the code that acts on it, read through Resolver.CredentialFlagForRequest
//     (or Store.APIKeyFlag outside a request).
//
// No codec case, no normalization plumbing, no index rebuild, no admin handler,
// no protocol wiring and no web UI edit.
//
// The JSON shape is unchanged from the hand-written field this replaces: a flag
// is a top-level field of the key object, next to `key`, `name` and `remark`.
// Only flags whose value differs from the declared default are stored, so a key
// that never turned a flag on keeps the same compact form it has today.
type CredentialFlagSpec struct {
	// Name is the flag's identifier and its config-file field name, e.g.
	// "force_disable_tools".
	Name string
	// Default is what a reader gets when the key does not carry the flag. It is
	// also what the config API and the web UI report for a key that never set
	// it, and what a caller outside the managed-key set reads.
	Default bool
	// Restrictive marks a flag whose `true` value removes a capability. When two
	// definitions of the same key are merged (config import), a restrictive flag
	// is on if either side has it on, so importing a config cannot silently drop
	// a restriction the user had already set. A non-restrictive flag takes the
	// imported record's value, whatever record the merge keeps for the key's name
	// and remark.
	Restrictive bool
	// LabelKey, HelpKey and BadgeKey are web UI locale keys: the toggle label,
	// its help text, and the short label shown next to a key in the list. An
	// empty BadgeKey means the flag shows no badge there. Keeping the keys here
	// rather than the text keeps translation in the locale files, and lets the
	// web UI render a new flag with no component change.
	LabelKey string
	HelpKey  string
	BadgeKey string
}

const (
	// CredentialFlagForceDisableTools is the per-key "force disable tool
	// calling" switch. Its behavior lives in the protocol adapters, which clear
	// the normalized tool definitions when the flag is on.
	CredentialFlagForceDisableTools = "force_disable_tools"
)

var (
	credentialFlagMu    sync.RWMutex
	credentialFlagSpecs = map[string]CredentialFlagSpec{}
	credentialFlagOrder []string
)

func init() {
	for _, spec := range []CredentialFlagSpec{
		{
			Name:        CredentialFlagForceDisableTools,
			Default:     false,
			Restrictive: true,
			LabelKey:    "accountManager.forceDisableToolsLabel",
			HelpKey:     "accountManager.forceDisableToolsHint",
			BadgeKey:    "accountManager.forceDisableToolsOn",
		},
	} {
		RegisterCredentialFlag(spec)
	}
}

// RegisterCredentialFlag declares a credential flag. It panics on a malformed
// spec: every rejected shape below is a programming error that would otherwise
// surface as a flag that silently never loads, never persists, or renders an
// empty control on the settings page.
func RegisterCredentialFlag(spec CredentialFlagSpec) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		panic("config: credential flag name must not be empty")
	}
	if strings.TrimSpace(spec.LabelKey) == "" {
		panic(fmt.Sprintf("config: credential flag %q must declare a label locale key", name))
	}
	credentialFlagMu.Lock()
	defer credentialFlagMu.Unlock()
	if _, dup := credentialFlagSpecs[name]; dup {
		panic(fmt.Sprintf("config: credential flag %q is already registered", name))
	}
	spec.Name = name
	credentialFlagSpecs[name] = spec
	credentialFlagOrder = append(credentialFlagOrder, name)
}

// CredentialFlagSpecs returns the registered flags in declaration order, which
// is the order the admin API and the web UI report them in.
func CredentialFlagSpecs() []CredentialFlagSpec {
	credentialFlagMu.RLock()
	defer credentialFlagMu.RUnlock()
	out := make([]CredentialFlagSpec, 0, len(credentialFlagOrder))
	for _, name := range credentialFlagOrder {
		out = append(out, credentialFlagSpecs[name])
	}
	return out
}

// CredentialFlagNames returns the registered flag names in declaration order.
func CredentialFlagNames() []string {
	credentialFlagMu.RLock()
	defer credentialFlagMu.RUnlock()
	return append([]string(nil), credentialFlagOrder...)
}

// LookupCredentialFlag returns the declaration registered for name.
func LookupCredentialFlag(name string) (CredentialFlagSpec, bool) {
	credentialFlagMu.RLock()
	defer credentialFlagMu.RUnlock()
	spec, ok := credentialFlagSpecs[name]
	return spec, ok
}
