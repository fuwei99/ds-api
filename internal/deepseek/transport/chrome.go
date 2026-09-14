package transport

import (
	"strings"

	"github.com/sardanioss/httpcloak/fingerprint"
)

// ChromeMajorVersion is the Chrome generation advertised at the HTTP layer
// (User-Agent and sec-ch-ua, both built from it in the protocol package).
//
// It is set from a real capture of chat.deepseek.com's web client. Keep it
// current: Chrome ships roughly every four weeks and the overwhelming majority
// of real users sit within a few versions of latest, so a stale User-Agent is
// itself an anomaly — and it is the cheapest thing in the world for a server to
// check.
const ChromeMajorVersion = "150"

// ChromePresetName is the httpcloak browser preset every upstream request is
// dialed with. It supplies the TLS ClientHello and HTTP/2 fingerprint.
//
// Keep it in step with ChromeMajorVersion: the preset name is the HTTP-layer
// version, while its TLS base can lag a few versions because uTLS only models
// handshakes that were actually captured. Never pin the TLS generation as a
// second constant — resolve it from the preset via ChromeClientHelloName.
// Pinning it is exactly the drift that left a stale "TLS is Chrome 133" claim
// behind after the transport moved to a newer preset.
const ChromePresetName = "chrome-150-windows"

// ChromeClientHelloName resolves the TLS ClientHello the configured preset
// actually reproduces, as its canonical httpcloak name (e.g.
// "chrome-146-windows"). Reading it from the preset keeps the answer honest
// across preset bumps.
func ChromeClientHelloName() (string, bool) {
	preset := fingerprint.GetStrict(ChromePresetName)
	if preset == nil {
		return "", false
	}
	return fingerprint.ClientHelloIDName(preset.ClientHelloID)
}

// ChromeClientHelloMajor returns just the major version of the resolved TLS
// ClientHello (e.g. "146"), or false when it cannot be derived.
func ChromeClientHelloMajor() (string, bool) {
	name, ok := ChromeClientHelloName()
	if !ok {
		return "", false
	}
	major, _, _ := strings.Cut(strings.TrimPrefix(name, "chrome-"), "-")
	if major == "" {
		return "", false
	}
	return major, true
}

// chromeHeaderOrder pins the request header order. httpcloak applies it on top
// of its browser preset so every request reaches chat.deepseek.com with a
// stable, Chrome-like header order.
//
// Names must be lowercase — httpcloak lowercases keys before looking them up in
// the order map, and headers absent from this list are appended afterwards in
// lexicographic order (still deterministic).
//
// This mirrors commonly observed Chrome fetch/XHR ordering. It is worth
// re-validating against a live capture of chat.deepseek.com if the upstream
// web client changes which headers it sets.
var chromeHeaderOrder = []string{
	"content-length",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"authorization",
	"x-client-bundle-id",
	"x-client-locale",
	"x-client-platform",
	"x-client-timezone-offset",
	"x-client-version",
	"x-ds-pow-response",
	"user-agent",
	"content-type",
	"accept",
	"origin",
	"sec-fetch-site",
	"sec-fetch-mode",
	"sec-fetch-dest",
	"referer",
	"accept-encoding",
	"accept-language",
	"cookie",
	"priority",
}

// ChromeHeaderOrder returns a copy of the pinned request header order, for
// tooling that needs to reproduce or diff the exact on-wire ordering.
func ChromeHeaderOrder() []string {
	return append([]string(nil), chromeHeaderOrder...)
}

// ChromePseudoHeaderOrder returns a copy of the pinned HTTP/2 pseudo-header
// order.
func ChromePseudoHeaderOrder() []string {
	return append([]string(nil), chromePseudoHeaderOrder...)
}

// chromePseudoHeaderOrder is Chrome's :method,:authority,:scheme,:path order.
var chromePseudoHeaderOrder = []string{":method", ":authority", ":scheme", ":path"}
