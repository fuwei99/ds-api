package toolcall

import "strings"

// This file implements the element-boundary repair for a greedy CDATA range that
// stole a later parameter's `]]>`.
//
// Background: findToolCDATAEnd searches forward without an element bound, so a
// `<![CDATA[` whose own `]]>` is missing keeps scanning until the next `]]>`
// anywhere in the text — which belongs to a *later* parameter. The value then
// silently absorbs `</|EPSE|parameter><|EPSE|parameter name="...">` plus the
// next parameter's body, and that later parameter disappears from the parsed
// input. The defect is already documented as the root cause of a repair failure
// in plan/tool-call-fallback-cdata-slots.md §3, where the boundary-limited scan
// (scanBoundedCDATARanges) was introduced — but that scan only served the LLM
// repair path's slotting, so the deterministic parser kept mis-parsing.
//
// The repair here is deliberately narrow: the greedy interpretation stays
// authoritative and is only replaced when the boundary interpretation parses
// into strictly more tool calls / parameters (see boundaryRepairOutranks).

// cdataRangeSwallowedNextParameter reports whether a greedy CDATA range consumed
// the close marker of a later parameter.
//
// The signature has two halves, and both are required:
//
//   - an *unbalanced* CDATA opener inside the body — an opener with no `]]>` of
//     its own before the body ends. Only possible when the body ate a later
//     parameter's opener and terminated on that parameter's close marker.
//   - the range's own `</parameter>` close tag sitting inside the body, before
//     that opener. This is what distinguishes "escaped my element" from "quotes
//     tool-call markup".
//
// The second half is what keeps a legitimately quoted example intact. A parameter
// value that embeds a tool-call sample (this repository's own prompt/spec files, a
// fenced example inside a Write payload) makes the greedy scan stop at the
// sample's inner `]]>`, so the body ends up holding the sample's *opening*
// parameter tag and its unbalanced opener — but never a close tag preceding them,
// because the value never left its own element. Requiring the close tag leaves
// every such input on the greedy interpretation.
func cdataRangeSwallowedNextParameter(text string, r cdataRange) bool {
	if r.contentStart < 0 || r.contentEnd > len(text) || r.contentEnd <= r.contentStart {
		return false
	}
	body := text[r.contentStart:r.contentEnd]
	for pos := 0; pos < len(body); {
		open := indexToolCDATAOpen(body, pos)
		if open < 0 {
			return false
		}
		contentStart := open + toolCDATAOpenLenAt(body, open)
		closeIdx := indexToolCDATAClose(body, contentStart)
		if closeIdx < 0 {
			return cdataOpenerEscapedOwnElement(body, open)
		}
		pos = closeIdx + toolCDATACloseLenAt(body, closeIdx)
	}
	return false
}

// cdataOpenerEscapedOwnElement reports whether the unbalanced CDATA opener at
// open is preceded by the tag sequence that can only mean the body ran out of its
// own element and into the next parameter: a closing `parameter` tag somewhere
// earlier, and an opening `parameter` tag immediately before the opener.
func cdataOpenerEscapedOwnElement(body string, open int) bool {
	if open <= 0 || open > len(body) {
		return false
	}
	head := strings.TrimRight(body[:open], " \t\r\n")
	if head == "" {
		return false
	}
	sawOwnClose := false
	var last ToolMarkupTag
	found := false
	for from := 0; from < len(head); {
		tag, ok := FindToolMarkupTagOutsideIgnored(head, from)
		if !ok {
			break
		}
		if tag.Closing && tag.Name == "parameter" {
			sawOwnClose = true
		}
		last = tag
		found = true
		from = tag.End + 1
	}
	if !sawOwnClose || !found {
		return false
	}
	return !last.Closing && last.Name == "parameter" && last.End == len(head)-1
}

// repairSwallowedCDATARanges closes every greedy CDATA range that swallowed a
// later parameter at its own element boundary, leaving all other text — and all
// other CDATA ranges — byte-identical.
//
// For an offending range the missing `]]>` is inserted at the enclosing
// element's boundary and the swallowed tail is released back into the markup, so
// the following `</|EPSE|parameter><|EPSE|parameter name="...">` shell and the
// close marker the range had stolen return to the parameter they belong to.
//
// Releasing a tail can expose a further unclosed parameter inside it (two
// consecutive parameters both missing their `]]>` present as a single greedy
// range), so the pass is repeated to a fixpoint. The iteration is bounded by the
// number of CDATA openers, since every pass closes at least one of them.
func repairSwallowedCDATARanges(text string) string {
	repaired := text
	for budget := countToolCDATAOpeners(text); budget > 0; budget-- {
		next := repairSwallowedCDATARangesOnce(repaired)
		if next == repaired {
			break
		}
		repaired = next
	}
	return repaired
}

func countToolCDATAOpeners(text string) int {
	count := 0
	for pos := 0; pos < len(text); {
		open := indexToolCDATAOpen(text, pos)
		if open < 0 {
			break
		}
		count++
		pos = open + toolCDATAOpenLenAt(text, open)
	}
	return count
}

func repairSwallowedCDATARangesOnce(text string) string {
	if text == "" {
		return text
	}
	var b strings.Builder
	prev := 0
	changed := false
	for _, r := range scanClosedCDATARanges(text) {
		if r.contentStart < prev || !cdataRangeSwallowedNextParameter(text, r) {
			continue
		}
		limit := nextToolMarkupStructuralBoundary(text, r.contentStart)
		if limit < 0 || limit > r.contentEnd {
			continue
		}
		contentEnd, closeLen := boundedCDATAContentEnd(text, r.contentStart, limit)
		if contentEnd <= r.contentStart || contentEnd+closeLen > len(text) {
			continue
		}
		if !changed {
			b.Grow(len(text) + len("]]>"))
			changed = true
		}
		b.WriteString(text[prev:contentEnd])
		b.WriteString("]]>")
		prev = contentEnd + closeLen
	}
	if !changed {
		return text
	}
	b.WriteString(text[prev:])
	return b.String()
}

// reparseWithCDATAElementBoundaries retries parsing with swallowed CDATA ranges
// closed at their element boundary. It reports ok only when the boundary
// interpretation yields strictly better tool calls than the greedy one, so the
// greedy interpretation remains authoritative for every input it handles
// correctly.
//
// The repair runs on the pre-normalization original: the tail a greedy range
// swallowed was inside a CDATA body when normalizeEPSEToolCallMarkup ran, so its
// EPSE tags were left untouched as parameter content. Releasing that tail is only
// useful if it is normalized afterwards, which means repairing first and
// normalizing second.
//
// The returned source replaces parseSource for residual-intent deduction: the
// repair only inserts `]]>` inside CDATA and never adds, removes or reorders
// wrappers, so the ordinal wrapper correspondence the deduction relies on (see
// plan/tool-call-fallback-design-phase1.md §4.2) still holds.
func reparseWithCDATAElementBoundaries(parsed []ParsedToolCall, original string) ([]ParsedToolCall, string, bool) {
	repaired := repairSwallowedCDATARanges(original)
	if repaired == original {
		return nil, "", false
	}
	normalized, ok := normalizeEPSEToolCallMarkup(repaired)
	if !ok {
		return nil, "", false
	}
	candidate := parseXMLToolCalls(normalized)
	if !boundaryRepairOutranks(candidate, parsed) {
		return nil, "", false
	}
	return candidate, normalized, true
}

// boundaryRepairOutranks applies the same arbitration as the LLM repair path
// (pickBetterRepairResult): more tool calls wins, then more populated
// parameters. Ties keep the greedy result.
func boundaryRepairOutranks(candidate, current []ParsedToolCall) bool {
	if len(candidate) == 0 {
		return false
	}
	if len(candidate) != len(current) {
		return len(candidate) > len(current)
	}
	return countToolCallInputs(candidate) > countToolCallInputs(current)
}
