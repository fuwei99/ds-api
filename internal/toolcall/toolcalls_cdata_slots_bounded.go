package toolcall

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// toolMarkupStructuralTagPrefixes lists the canonical (non-EPSE) tool-markup tag
// openings that delimit a parameter value. EPSE-prefixed tags are matched
// separately via detectEPSETagAt, which covers every prefix/fullwidth variant.
var toolMarkupStructuralTagPrefixes = []string{
	"<tool_calls", "</tool_calls",
	"<tool-calls", "</tool-calls",
	"<toolcalls", "</toolcalls",
	"<invoke", "</invoke",
	"<parameter", "</parameter",
}

// nextToolMarkupStructuralBoundary returns the offset of the first tool-markup
// structural tag at or after from, or -1 when there is none.
//
// Unlike every other scanner in this package it deliberately does NOT skip CDATA
// regions: it is called from inside a CDATA body precisely to find where the
// enclosing element ends when that body has no `]]>` of its own. Skipping CDATA
// here would defeat the purpose.
func nextToolMarkupStructuralBoundary(text string, from int) int {
	for i := maxInt(from, 0); i < len(text); i++ {
		// Cheap guard: every candidate starts with a `<` (or a fullwidth/CJK
		// variant of it). Bodies are routinely tens of KB, so the expensive
		// rune-normalizing matchers must not run on every byte.
		if xmlTagStartDelimiterLenAt(text, i) == 0 {
			continue
		}
		if _, _, ok := detectEPSETagAt(text, i); ok {
			return i
		}
		for _, prefix := range toolMarkupStructuralTagPrefixes {
			n, ok := matchASCIIPrefixFoldAt(text, i, prefix)
			if ok && hasXMLTagBoundary(text, i+n) {
				return i
			}
		}
	}
	return -1
}

// scanBoundedCDATARanges is the boundary-limited counterpart of
// scanClosedCDATARanges. For each CDATA opener it first locates the enclosing
// element's boundary (the next tool-markup structural tag) and only looks for
// `]]>` before it. Two behaviours differ from the greedy scan:
//
//   - A missing `]]>` can no longer make the scan swallow the following
//     `</|EPSE|parameter><|EPSE|parameter name="...">` shell into one slot, which
//     would delete a whole parameter node from the skeleton handed to the repair
//     layer (root cause of the "repair produced a skeleton with a missing
//     parameter" failure).
//   - A CDATA that is unclosed *within its own element* is still slotted, with
//     the truncated close marker (if any) kept out of the slot body. The
//     oversized body is lifted out while the defect stays visible in the
//     skeleton, so the repair layer can actually see and fix it instead of
//     receiving tens of KB of raw content.
//
// It is used only by the repair path (see buildToolCallRepairPlan); the
// deterministic parser and the streaming sieve keep the greedy interpretation.
func scanBoundedCDATARanges(text string) []cdataRange {
	var ranges []cdataRange
	pos := 0
	for pos < len(text) {
		start := indexToolCDATAOpen(text, pos)
		if start < 0 {
			break
		}
		openLen := toolCDATAOpenLenAt(text, start)
		contentStart := start + openLen
		limit := nextToolMarkupStructuralBoundary(text, contentStart)
		if limit < 0 {
			limit = len(text)
		}

		contentEnd, closeLen := boundedCDATAContentEnd(text, contentStart, limit)
		if contentEnd > contentStart {
			ranges = append(ranges, cdataRange{
				openStart:    start,
				contentStart: contentStart,
				contentEnd:   contentEnd,
				closeLen:     closeLen,
			})
		}
		pos = maxInt(contentEnd+closeLen, contentStart)
	}
	return ranges
}

// boundedCDATAContentEnd splits an element-bounded CDATA region into its content
// and its close marker.
//
// A complete `]]>` before the boundary is used as-is. Otherwise the body is
// checked for a *truncated* close marker: a trailing `]]` (optionally followed by
// whitespace) is the `]]>` the model forgot the `>` on, and must NOT become part
// of the slot body — otherwise the restored parameter value carries a stray `]]`
// that the repair model never gets a chance to see, let alone fix. Keeping the
// `]]` in the skeleton instead leaves the actual defect (the missing `>`) in
// plain view.
func boundedCDATAContentEnd(text string, contentStart, limit int) (contentEnd, closeLen int) {
	if endRel := findToolCDATAEnd(text[:limit], contentStart); endRel >= 0 {
		return endRel, toolCDATACloseLenAt(text, endRel)
	}
	end := limit
	for end > contentStart {
		r, size := utf8.DecodeLastRuneInString(text[contentStart:end])
		if size <= 0 || !unicode.IsSpace(r) {
			break
		}
		end -= size
	}
	if end-contentStart >= len("]]") && text[end-len("]]"):end] == "]]" {
		return end - len("]]"), len("]]")
	}
	return limit, 0
}

// slotCDATAContentBounded slots text using the boundary-limited CDATA scan.
func slotCDATAContentBounded(text string) cdataSlotResult {
	if text == "" {
		return cdataSlotResult{skeleton: text}
	}
	res := buildCDATASlotResult(text, scanBoundedCDATARanges(text))
	res.bounded = true
	return res
}

// slotContentSwallowedStructure reports whether a slot body looks like it
// swallowed the tool-markup structure that followed it.
//
// The signature is deliberately narrow: a structural tag inside the body AND a
// further CDATA opener after that tag. A genuine long parameter may well mention
// `</|EPSE|parameter>` in its text (this repository's own prompt/spec files do),
// and such a body satisfies only the first half, so the greedy interpretation is
// kept for it. Requiring a trailing CDATA opener means the body spans at least
// into the *next* parameter, which no single parameter value legitimately does.
func slotContentSwallowedStructure(content string) bool {
	boundary := nextToolMarkupStructuralBoundary(content, 0)
	if boundary < 0 {
		return false
	}
	return indexToolCDATAOpen(content, boundary) >= 0
}

// greedySlottingDegraded reports whether the greedy slot result mishandled text
// badly enough that the repair layer should be given the bounded interpretation
// instead, plus a short reason for the degradation log.
//
// Two triggers, matching the two ways greedy slotting fails on malformed input:
//
//	swallowed_structure     a greedy slot body ate the following parameter shell
//	unslotted_unclosed_cdata  greedy skipped an unclosed opener that bounded can
//	                          slot, leaving raw (possibly huge) content in the
//	                          skeleton
func greedySlottingDegraded(greedy, bounded cdataSlotResult) (bool, string) {
	for _, content := range greedy.slots {
		if slotContentSwallowedStructure(content) {
			return true, "swallowed_structure"
		}
	}
	if len(bounded.slots) > len(greedy.slots) {
		return true, "unslotted_unclosed_cdata"
	}
	return false, ""
}

// placeholderChecklist renders the "every placeholder must come back verbatim"
// self-check line appended to the repair prompt. It returns "" when there is
// nothing to slot.
func placeholderChecklist(res cdataSlotResult) string {
	if len(res.slots) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("本次输入共包含 ")
	b.WriteString(strconv.Itoa(len(res.slots)))
	b.WriteString(" 个参数占位符：")
	for i := range res.slots {
		if i > 0 {
			b.WriteString("、")
		}
		b.WriteString(res.token.format(i))
	}
	b.WriteString("。输出中必须全部出现，且每个只出现一次。")
	return b.String()
}
