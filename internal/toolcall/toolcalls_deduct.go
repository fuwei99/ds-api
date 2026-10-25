package toolcall

import (
	"sort"
	"strings"
)

// TextSpan is a half-open byte range [Start, End) inside some text.
type TextSpan struct {
	Start int
	End   int
}

// BadToolMarkupSpans returns the ordered, non-overlapping spans of text that
// carry tool-call markup which cannot become a tool call and which the finalize
// repair has already replaced. Callers remove these spans from the visible text
// instead of removing the whole residual, so prose that merely travelled next
// to a bad call survives, and a residual that is not a contiguous substring
// (because a successfully parsed wrapper was cut out of its middle) still
// yields removable spans.
//
// Two families are covered:
//   - DSML wrappers (any local name, e.g. <DSML calls>): they never parse into a
//     tool call. A complete wrapper spans its opening tag through the matching
//     closing tag; an unclosed wrapper spans its opening tag through the end of
//     the whitespace-separated DSML tag run that follows it, so trailing prose
//     is not swallowed.
//   - EPSE / canonical markup blocks that fail to parse into any call (for
//     example a wrapper whose invoke carries no name). Blocks are located with
//     the same balanced scan the parser uses and the scan skips past a matched
//     block, so nested tags inside an accepted block are never evaluated - and
//     never removed - on their own.
//
// Markup inside a fenced code block is documentation rather than a call, and is
// skipped.
func BadToolMarkupSpans(text string) []TextSpan {
	if text == "" {
		return nil
	}
	spans := append(dsmlBadSpans(text), unparsedToolMarkupSpans(text)...)
	return mergeTextSpans(spans)
}

// RemoveTextSpans deletes the given spans from text and returns the remainder
// with surrounding whitespace trimmed. Spans may be unordered or overlapping;
// out-of-range spans are clamped.
func RemoveTextSpans(text string, spans []TextSpan) string {
	if text == "" || len(spans) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	prev := 0
	for _, span := range mergeTextSpans(spans) {
		start, end := span.Start, span.End
		if start < prev {
			start = prev
		}
		if end > len(text) {
			end = len(text)
		}
		if start > end {
			continue
		}
		if start > prev {
			b.WriteString(text[prev:start])
		}
		prev = end
	}
	if prev < len(text) {
		b.WriteString(text[prev:])
	}
	return strings.TrimSpace(b.String())
}

// dsmlBadSpans collects the spans of DSML wrappers in text.
func dsmlBadSpans(text string) []TextSpan {
	var spans []TextSpan
	for pos := 0; pos < len(text); {
		tag, ok := FindDSMLTagOutsideIgnored(text, pos)
		if !ok {
			break
		}
		if insideFencedCode(text, tag.Start) {
			pos = tag.End + 1
			continue
		}
		if tag.Closing {
			// An orphan closing shell is alien markup with no body to keep.
			spans = append(spans, TextSpan{Start: tag.Start, End: tag.End + 1})
			pos = tag.End + 1
			continue
		}
		if closeTag, ok := FindMatchingDSMLClose(text, tag); ok {
			spans = append(spans, TextSpan{Start: tag.Start, End: closeTag.End + 1})
			pos = closeTag.End + 1
			continue
		}
		end := dsmlTagRunEnd(text, tag)
		spans = append(spans, TextSpan{Start: tag.Start, End: end})
		pos = end
	}
	return spans
}

// dsmlTagRunEnd returns the end offset of the run of DSML tags that starts at
// open. Only whitespace-separated tags join the run, so an unclosed wrapper is
// hidden together with its own tag run while prose after the run is preserved.
// The returned offset is always greater than open.Start, so callers make
// progress.
func dsmlTagRunEnd(text string, open DSMLTag) int {
	end := open.End + 1
	for pos := end; pos < len(text); {
		tag, ok := FindDSMLTagOutsideIgnored(text, pos)
		if !ok {
			return end
		}
		if strings.TrimSpace(text[end:tag.Start]) != "" {
			return end
		}
		end = tag.End + 1
		pos = tag.End + 1
	}
	return end
}

// unparsedToolMarkupSpans collects the spans of balanced EPSE / canonical
// markup blocks that parse into zero tool calls.
func unparsedToolMarkupSpans(text string) []TextSpan {
	var spans []TextSpan
	for pos := 0; pos < len(text); {
		tag, ok := FindToolMarkupTagOutsideIgnored(text, pos)
		if !ok {
			break
		}
		if tag.Closing {
			pos = tag.End + 1
			continue
		}
		if insideFencedCode(text, tag.Start) {
			pos = tag.End + 1
			continue
		}
		closeTag, ok := FindMatchingToolMarkupClose(text, tag)
		if !ok {
			// Leave an unterminated shell alone: without a matching close the
			// block boundary is unknown, so the caller's verbatim-residual
			// fallback owns that case.
			pos = tag.End + 1
			continue
		}
		block := text[tag.Start : closeTag.End+1]
		if parsed := ParseStandaloneToolCallsDetailed(block, nil); len(parsed.Calls) == 0 {
			spans = append(spans, TextSpan{Start: tag.Start, End: closeTag.End + 1})
		}
		pos = closeTag.End + 1
	}
	return spans
}

// mergeTextSpans returns the spans sorted by start offset with overlapping or
// adjacent spans merged.
func mergeTextSpans(spans []TextSpan) []TextSpan {
	if len(spans) == 0 {
		return nil
	}
	ordered := make([]TextSpan, len(spans))
	copy(ordered, spans)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Start != ordered[j].Start {
			return ordered[i].Start < ordered[j].Start
		}
		return ordered[i].End < ordered[j].End
	})
	merged := make([]TextSpan, 0, len(ordered))
	merged = append(merged, ordered[0])
	for _, span := range ordered[1:] {
		last := &merged[len(merged)-1]
		if span.Start <= last.End {
			if span.End > last.End {
				last.End = span.End
			}
			continue
		}
		merged = append(merged, span)
	}
	return merged
}

// insideFencedCode reports whether idx sits inside a fenced code block. Fenced
// regions are documentation: markup there is not a tool call and must not be
// deducted from visible output.
func insideFencedCode(text string, idx int) bool {
	if text == "" || idx <= 0 || idx >= len(text) {
		return false
	}
	inFence := false
	marker := ""
	lineStart := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		lineEnd := lineStart + len(line)
		if idx < lineStart {
			break
		}
		if idx < lineEnd {
			return inFence
		}
		trimmed := strings.TrimLeft(line, " \t")
		if !inFence {
			if open, ok := parseFenceOpen(trimmed); ok {
				inFence = true
				marker = open
			}
		} else if isFenceClose(trimmed, marker) {
			inFence = false
			marker = ""
		}
		lineStart = lineEnd
	}
	return false
}
