package promptcompat

import (
	"net/http"

	"ds2api/internal/config"
)

// CredentialFlagReader reports the per-key credential flags of the caller behind
// a request (see internal/config/credential_flags.go). auth.Resolver implements
// it, and every protocol exit already holds one for account routing.
type CredentialFlagReader interface {
	CredentialFlagForRequest(req *http.Request, flag string) bool
}

// ApplyCallerFlags applies the registered per-key credential flags that rewrite
// the request body, and returns whether the rewritten body carries tool
// definitions — the value the exit stores with auth.WithToolsPresent.
//
// The two are one call because their order is the contract: a flag that strips
// tools has to run before presence is computed, or the request would still be
// routed to a tool account pool and would still get the tool prompt injected.
// Keeping the order inside one step is what lets a new flag be a case here
// rather than a change in every exit.
//
// This is the only place on the request path that reads a per-key flag. An exit
// calls it once, before anything that depends on the request's tool content, and
// never reads a flag itself.
func ApplyCallerFlags(flags CredentialFlagReader, r *http.Request, req map[string]any) bool {
	if flags != nil && flags.CredentialFlagForRequest(r, config.CredentialFlagForceDisableTools) {
		DisableToolsForRequest(req)
	}
	return RequestBodyHasTools(req)
}
