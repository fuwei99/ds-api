package toolcall

import (
	"strings"
)

// DetectToolCallIntent reports whether the given text contains a *paired* EPSE
// tool-call intent: an EPSE opening tag (`<|EPSE...`) followed later by an EPSE
// closing tag (`</|EPSE...`), where both live outside CDATA, XML comments, and
// markdown code fences / inline code spans.
//
// The caller is expected to pass the residual text that remains after deducting
// the source regions of successfully parsed tool calls (see
// parseToolCallsDetailedXMLOnly). DetectToolCallIntent itself performs no
// parsing, no deduction and no name-scoped depth pairing: "paired" here is an
// existence test only — is there at least one EPSE open tag with at least one
// EPSE close tag somewhere after it. The local names of the open and close tags
// may differ or be empty.
//
// It must run on the pre-normalization coordinate system (the `original` text),
// never on the normalized text: normalization rewrites valid-local-name EPSE
// shells into canonical `<tool_calls>`/`<invoke>` and strips the `|EPSE|`
// prefix, which would make EPSE detection always fail.
//
// Additionally it reports true when the text carries any DSML-prefixed markup
// tag (`<｜｜DSML｜｜suffix` / `</｜｜DSML｜｜suffix`, any or empty suffix): the
// model sometimes emits tool calls in that alien format instead of the repo's
// EPSE format. DSML wrappers never parse into tool calls (their local names are
// outside the markup name table), so any DSML tag left in the residual is by
// definition unhandled output that must be routed to the proactive repair pass.
// Unlike the EPSE probe there is no open/close pairing requirement — a single
// tag hit (open or close) counts, and the whole residual is handed to repair.
func DetectToolCallIntent(text string) bool {
	if text == "" {
		return false
	}
	// Strip markdown code fences so EPSE literals inside *fully closed* fenced
	// blocks do not participate in detection. stripFencedCodeBlocks preserves
	// the raw tail of an unclosed (or odd-count) fence when that tail carries
	// tool-structural closing tags, so a real tool call whose parameters embed
	// an odd number of ``` fences is not decapitated here: its closing EPSE
	// tags survive and intent detection stays true. This is idempotent when the
	// caller already stripped fences.
	stripped := stripFencedCodeBlocks(text)
	if stripped == "" {
		return false
	}

	openEnd, ok := findEPSETag(stripped, 0, false)
	if ok {
		if _, ok = findEPSETag(stripped, openEnd, true); ok {
			return true
		}
	}
	return containsDSMLTagIntent(stripped)
}

// containsDSMLTagIntent reports whether text carries any DSML-prefixed markup
// tag outside CDATA, XML comments and markdown code fences / inline code
// spans. It reuses the same scanning skeleton as findEPSETag but stops at the
// first hit: DSML output never parses into tool calls, so pairing is not
// meaningful — one tag is enough to flag the residual for repair.
func containsDSMLTagIntent(text string) bool {
	for i := 0; i < len(text); {
		next, advanced, blocked := skipXMLIgnoredSection(text, i)
		if blocked {
			return false
		}
		if advanced {
			i = next
			continue
		}
		if end, ok := markdownCodeSpanEnd(text, i); ok {
			i = end
			continue
		}
		if detectDSMLTagAt(text, i) {
			return true
		}
		i++
	}
	return false
}

// detectDSMLTagAt reports whether a DSML-prefixed markup tag begins at start.
// It mirrors detectEPSETagAt (same `<` / closing-slash / separator primitives,
// fullwidth folding, ignorables) but matches the `dsml` literal instead of
// `epse`, so `<｜｜DSML｜｜calls>`, `<|DSML|invoke name="read">`,
// `</｜｜DSML｜｜invoke>` and separator-less `<DSML>` variants all match,
// regardless of the local-name suffix. The suffix itself is not consumed: only
// the rune right after the `dsml` literal is checked, and it must be a tag
// boundary (separator, whitespace, `>`, `/`, or end of input) so words that
// merely start with "dsml" never match.
func detectDSMLTagAt(text string, start int) bool {
	i, ok := consumeToolMarkupLessThan(text, start)
	if !ok {
		return false
	}
	for {
		next, ok := consumeToolMarkupLessThan(text, i)
		if !ok {
			break
		}
		i = next
	}
	// A closing tag carries its slash between `<` and the DSML prefix.
	if next, ok := consumeToolMarkupClosingSlash(text, i); ok {
		i = next
	}
	// Consume the pipe/separator runes (and any whitespace-like runes the
	// tokenizer may emit between them) that precede the DSML literal.
	for {
		next, ok := consumeToolMarkupSeparator(text, i)
		if ok {
			i = next
			continue
		}
		if spacingLen := toolMarkupWhitespaceLikeLenAt(text, i); spacingLen > 0 {
			i += spacingLen
			continue
		}
		break
	}
	afterDSML, matched := consumeToolKeyword(text, i, "dsml")
	if !matched {
		return false
	}
	if b, size := normalizedASCIIAt(text, afterDSML); size > 0 {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') {
			return false
		}
	}
	return true
}

// DSMLTag is the span and local name of a DSML-prefixed markup tag. Name is the
// token after the `dsml` literal, normalized to lowercase ASCII; it may be
// empty (e.g. `<DSML>`).
type DSMLTag struct {
	Start   int
	End     int
	Name    string
	Closing bool
}

// FindDSMLTagOutsideIgnored returns the first complete (terminator already
// arrived) DSML-prefixed markup tag at or after start, skipping CDATA, XML
// comments and markdown inline code spans. It mirrors
// FindToolMarkupTagOutsideIgnored but matches the DSML prefix instead of the
// tool markup name table.
func FindDSMLTagOutsideIgnored(text string, start int) (DSMLTag, bool) {
	for i := maxInt(start, 0); i < len(text); {
		next, advanced, blocked := skipXMLIgnoredSection(text, i)
		if blocked {
			return DSMLTag{}, false
		}
		if advanced {
			i = next
			continue
		}
		if end, ok := markdownCodeSpanEnd(text, i); ok {
			i = end
			continue
		}
		if tag, ok := scanDSMLTagAt(text, i); ok {
			return tag, true
		}
		i++
	}
	return DSMLTag{}, false
}

// FindMatchingDSMLClose performs local-name depth pairing to find the closing
// tag for open. Empty local names pair with empty local names.
func FindMatchingDSMLClose(text string, open DSMLTag) (DSMLTag, bool) {
	if text == "" || open.Closing || open.End >= len(text) {
		return DSMLTag{}, false
	}
	depth := 1
	for pos := open.End + 1; pos < len(text); {
		tag, ok := FindDSMLTagOutsideIgnored(text, pos)
		if !ok {
			return DSMLTag{}, false
		}
		if tag.Name != open.Name {
			pos = tag.End + 1
			continue
		}
		if tag.Closing {
			depth--
			if depth == 0 {
				return tag, true
			}
		} else {
			depth++
		}
		pos = tag.End + 1
	}
	return DSMLTag{}, false
}

// scanDSMLTagAt parses a complete DSML-prefixed markup tag (local name
// included) beginning at start.
func scanDSMLTagAt(text string, start int) (DSMLTag, bool) {
	i, ok := consumeToolMarkupLessThan(text, start)
	if !ok {
		return DSMLTag{}, false
	}
	for {
		next, ok := consumeToolMarkupLessThan(text, i)
		if !ok {
			break
		}
		i = next
	}
	closing := false
	if next, ok := consumeToolMarkupClosingSlash(text, i); ok {
		closing = true
		i = next
	}
	for {
		next, ok := consumeToolMarkupSeparator(text, i)
		if ok {
			i = next
			continue
		}
		if spacingLen := toolMarkupWhitespaceLikeLenAt(text, i); spacingLen > 0 {
			i += spacingLen
			continue
		}
		break
	}
	afterDSML, matched := consumeToolKeyword(text, i, "dsml")
	if !matched {
		return DSMLTag{}, false
	}
	if b, size := normalizedASCIIAt(text, afterDSML); size > 0 {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') {
			return DSMLTag{}, false
		}
	}
	nameStart := afterDSML
	for {
		next, ok := consumeToolMarkupSeparator(text, nameStart)
		if ok {
			nameStart = next
			continue
		}
		if spacingLen := toolMarkupWhitespaceLikeLenAt(text, nameStart); spacingLen > 0 {
			nameStart += spacingLen
			continue
		}
		break
	}
	nameEnd := nameStart
	for nameEnd < len(text) {
		b, size := normalizedASCIIAt(text, nameEnd)
		if size <= 0 {
			break
		}
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-' {
			nameEnd += size
			continue
		}
		break
	}
	tagEnd := findXMLTagEnd(text, nameStart)
	if tagEnd < 0 {
		return DSMLTag{}, false
	}
	name := normalizedASCIILowerString(text[nameStart:nameEnd])
	return DSMLTag{Start: start, End: tagEnd, Name: name, Closing: closing}, true
}

// findEPSETag scans text from start for the first EPSE-prefixed markup tag whose
// closing-ness matches wantClosing, skipping CDATA / comments (via
// skipXMLIgnoredSection) and inline code spans (via markdownCodeSpanEnd). It
// returns the byte offset just past the consumed `epse` literal and true on a
// match.
func findEPSETag(text string, start int, wantClosing bool) (int, bool) {
	for i := maxInt(start, 0); i < len(text); {
		next, advanced, blocked := skipXMLIgnoredSection(text, i)
		if blocked {
			return 0, false
		}
		if advanced {
			i = next
			continue
		}
		if end, ok := markdownCodeSpanEnd(text, i); ok {
			i = end
			continue
		}
		if afterEPSE, closing, ok := detectEPSETagAt(text, i); ok {
			if closing == wantClosing {
				return afterEPSE, true
			}
			i = afterEPSE
			continue
		}
		i++
	}
	return 0, false
}

// detectEPSETagAt reports whether an EPSE-prefixed markup tag begins at start.
// It walks the same low-level primitives as scanToolMarkupTagAt but only cares
// about the EPSE prefix, not the local name — so `<|EPSE|call>`, `<|EPSE|invoke>`,
// the local-name-less shorthand `</|EPSE>` and `<EPSE ...>` variants all match.
// closing is true when a closing slash sits between `<` and the EPSE prefix.
// The returned offset points just past the consumed `epse` literal.
func detectEPSETagAt(text string, start int) (afterEPSE int, closing bool, ok bool) {
	i, ok := consumeToolMarkupLessThan(text, start)
	if !ok {
		return start, false, false
	}
	for {
		next, ok := consumeToolMarkupLessThan(text, i)
		if !ok {
			break
		}
		i = next
	}
	// A closing tag carries its slash between `<` and the EPSE prefix; it must
	// be consumed via the closing-slash primitive, not the opening path.
	if next, ok := consumeToolMarkupClosingSlash(text, i); ok {
		closing = true
		i = next
	}
	// Consume any pipe/separator runes that precede the EPSE literal (e.g. the
	// leading `|` in `<|EPSE`). Whitespace and `/` are excluded by
	// consumeToolMarkupSeparator, so this only eats the pipe-family separators.
	for {
		next, ok := consumeToolMarkupSeparator(text, i)
		if !ok {
			break
		}
		i = next
	}
	afterEPSE, matched := consumeToolKeyword(text, i, "epse")
	if !matched {
		return start, false, false
	}
	// Guard against words that merely start with "epse" (e.g. "epsentence"):
	// the character immediately after the epse literal must be a tag boundary
	// (separator, whitespace, `>`, `/`, or end of input), never alphanumeric.
	if b, size := normalizedASCIIAt(text, afterEPSE); size > 0 {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') {
			return start, false, false
		}
	}
	return afterEPSE, closing, true
}

// deductSuccessfulToolCallWrappers removes, from the pre-normalization original
// text, the source regions of every successfully-parsed tool call so the
// remainder can be probed for residual intent. Success is determined on
// parseSource (the normalized / CDATA-recovered text that was actually parsed)
// and mapped back to the original by ordinal index, because normalization
// neither adds, removes nor reorders wrappers/invokes.
//
// Two granularities are used, mirroring parseXMLToolCalls:
//   - wrapper granularity when an explicit `<|EPSE|tool_calls>` wrapper exists:
//     a wrapper whose invokes all parsed is deducted shell-and-all (so no empty
//     `<|EPSE|tool_calls></|EPSE|tool_calls>` false positive remains); a wrapper
//     with any failed invoke is left intact.
//   - invoke granularity for bare invokes (§5.1 caveat): when parseSource has no
//     opening tool_calls tag, parseXMLToolCalls synthesizes a wrapper via
//     repairMissingXMLToolCallsOpeningWrapper. The original carries no wrapper
//     shell, so the deduction target degrades to each successful invoke's own
//     region in the original.
func deductSuccessfulToolCallWrappers(original, parseSource string) string {
	sourceWrappers := findToolMarkupElementBlocksByName(parseSource, "tool_calls")
	if len(sourceWrappers) > 0 {
		return deductSuccessfulByWrapper(original, sourceWrappers)
	}
	return deductSuccessfulBareInvokes(original, parseSource)
}

// deductSuccessfulByWrapper handles the explicit-wrapper case with ordinal
// mapping between parseSource wrappers and original wrappers.
func deductSuccessfulByWrapper(original string, sourceWrappers []xmlElementBlock) string {
	origWrappers := findToolMarkupElementBlocksByName(original, "tool_calls")
	n := len(sourceWrappers)
	if len(origWrappers) < n {
		n = len(origWrappers)
	}
	if n == 0 {
		return original
	}

	var b strings.Builder
	b.Grow(len(original))
	prev := 0
	for idx := 0; idx < n; idx++ {
		invokes := findXMLElementBlocks(sourceWrappers[idx].Body, "invoke")
		if len(invokes) == 0 {
			continue
		}
		allOK := true
		for _, inv := range invokes {
			if _, ok := parseSingleXMLToolCall(inv); !ok {
				allOK = false
				break
			}
		}
		if !allOK {
			continue
		}
		ow := origWrappers[idx]
		if ow.Start < prev || ow.Start > ow.End || ow.End > len(original) {
			continue
		}
		b.WriteString(original[prev:ow.Start])
		prev = ow.End
	}
	b.WriteString(original[prev:])
	return b.String()
}

// deductSuccessfulBareInvokes handles the repaired bare-invoke case: parseSource
// gets a synthetic <tool_calls> wrapper (same as parseXMLToolCalls), success is
// evaluated per invoke, and successful invokes are deducted from the original by
// ordinal correspondence at invoke granularity.
func deductSuccessfulBareInvokes(original, parseSource string) string {
	repaired := repairMissingXMLToolCallsOpeningWrapper(parseSource)
	wrappers := findToolMarkupElementBlocksByName(repaired, "tool_calls")
	if len(wrappers) == 0 {
		return original
	}
	var successFlags []bool
	for _, w := range wrappers {
		for _, inv := range findXMLElementBlocks(w.Body, "invoke") {
			_, ok := parseSingleXMLToolCall(inv)
			successFlags = append(successFlags, ok)
		}
	}
	origInvokes := findToolMarkupElementBlocksByName(original, "invoke")
	n := len(successFlags)
	if len(origInvokes) < n {
		n = len(origInvokes)
	}
	if n == 0 {
		return original
	}

	var b strings.Builder
	b.Grow(len(original))
	prev := 0
	for idx := 0; idx < n; idx++ {
		if !successFlags[idx] {
			continue
		}
		oi := origInvokes[idx]
		if oi.Start < prev || oi.Start > oi.End || oi.End > len(original) {
			continue
		}
		b.WriteString(original[prev:oi.Start])
		prev = oi.End
	}
	b.WriteString(original[prev:])
	return b.String()
}
