package sse

import "strings"

// ToolRoleMarker is the plain-text role marker that introduces a tool result in
// the assembled prompt (see internal/prompt/messages.go). It is prompt
// structure, never reply text.
//
// The model occasionally hallucinates it: after emitting a tool call it prints
// `[Tool]:` itself, invents a tool result, and then keeps answering its own
// invented dialogue. Everything from the marker on is fabricated, so the
// compatibility layer cuts the turn there — the text produced before the marker
// is delivered normally, the marker and everything after it is dropped.
const ToolRoleMarker = "[Tool]:"

// toolRoleMarkerHoldbackLen is the longest proper prefix of ToolRoleMarker that
// Feed withholds while waiting for the rest of a marker that a later fragment
// may still complete.
const toolRoleMarkerHoldbackLen = len(ToolRoleMarker) - 1

// ToolRoleMarkerGuard is a streaming detector for a hallucinated `[Tool]:` role
// marker on the raw text channel.
//
// Text arrives in arbitrary fragments — SSE deltas routinely split the marker
// across frames — so Feed withholds a trailing fragment that could still grow
// into the marker and resumes matching on the next call. Once tripped the guard
// stays tripped and returns nothing, so the caller ends the upstream read and
// finishes with the text it already forwarded.
//
// Matching is case-insensitive, so a drifted `[tool]:` is cut as well.
//
// The withheld fragment is deliberately never released: by construction it is
// the beginning of a marker, and a reply that ends inside one ends inside
// fabricated text. That costs at most len(ToolRoleMarker)-1 trailing bytes and
// only when the reply stops on a marker prefix such as a dangling `[To`.
type ToolRoleMarkerGuard struct {
	pending string
	tripped bool
}

// Tripped reports whether a role marker was already detected.
func (g *ToolRoleMarkerGuard) Tripped() bool {
	return g != nil && g.tripped
}

// Feed consumes the next raw text fragment and returns the prefix that is safe
// to forward downstream. A trailing fragment that could still grow into the
// marker is withheld until a later Feed.
//
// Once tripped it returns "" forever: the marker and everything after it is
// dropped.
func (g *ToolRoleMarkerGuard) Feed(text string) string {
	if g == nil || g.tripped {
		return ""
	}
	data := g.pending + text
	g.pending = ""
	if idx := indexToolRoleMarker(data); idx >= 0 {
		g.tripped = true
		return data[:idx]
	}
	hold := trailingToolRoleMarkerPrefixLen(data)
	g.pending = data[len(data)-hold:]
	return data[:len(data)-hold]
}

// TrimToolRoleMarker removes the first `[Tool]:` role marker and everything
// after it, reporting whether a marker was found. It is the non-streaming
// counterpart of ToolRoleMarkerGuard.
func TrimToolRoleMarker(text string) (string, bool) {
	idx := indexToolRoleMarker(text)
	if idx < 0 {
		return text, false
	}
	return text[:idx], true
}

// indexToolRoleMarker returns the offset of the first case-insensitive
// occurrence of the role marker in text, or -1.
func indexToolRoleMarker(text string) int {
	if len(text) < len(ToolRoleMarker) {
		return -1
	}
	for i := 0; i+len(ToolRoleMarker) <= len(text); i++ {
		if strings.EqualFold(text[i:i+len(ToolRoleMarker)], ToolRoleMarker) {
			return i
		}
	}
	return -1
}

// trailingToolRoleMarkerPrefixLen returns the length of the longest suffix of
// text that is a proper prefix of the role marker (case-insensitive ASCII), so
// Feed can withhold a marker that is still arriving one fragment at a time.
//
// Slicing at an arbitrary byte offset is safe: a fragment that starts inside a
// multi-byte rune never folds to the ASCII marker, so the scan cannot match
// there.
func trailingToolRoleMarkerPrefixLen(text string) int {
	limit := toolRoleMarkerHoldbackLen
	if len(text) < limit {
		limit = len(text)
	}
	for l := limit; l > 0; l-- {
		if strings.EqualFold(text[len(text)-l:], ToolRoleMarker[:l]) {
			return l
		}
	}
	return 0
}
