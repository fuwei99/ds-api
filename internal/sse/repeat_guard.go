package sse

import "strings"

// DefaultRepeatLoopThreshold is how many consecutive closing tags are treated
// as a repetition loop.
//
// DeepSeek occasionally degenerates at the end of a generation and emits
// closing shells forever without ever sending a terminal status, so the stream
// only ends when the idle watchdog fires minutes later. The repeated shell is
// not always the same tag: observed loops include a plain `</|EPSE>` run, an
// alternating `</|EPSE|invoke>` / `</|EPSE|tool_calls>` run, and a run of the
// normalized canonical shells `</invoke>` / `</parameter>`. The guard therefore
// keys on "any closing tag `</…>`" rather than on one literal or one prefix.
//
// A well-formed tool call closes at most three shells back to back (the last
// parameter, then invoke, then tool_calls); any deeper structure is broken up by
// an opening tag. Measured worst cases for legitimate output: 3 for well-formed
// calls of every shape, 5 when a model over-closes (duplicate `</parameter>`
// before closing invoke and tool_calls), and 6 when a model explains the format
// by listing closing-shell variants one per line — that last shape has no
// structural upper bound, which is why the threshold keeps real headroom above
// it rather than sitting at the measured maximum.
//
// Ordinary HTML closing tags (see htmlClosingTagNames) are excluded: deeply
// nested markup legitimately ends with a long run of `</div>` / `</li>` /
// `</td>` lines, and those must not be mistaken for the degenerate protocol
// loop. The exclusion is by name, not by shape, so the canonical `</invoke>` /
// `</parameter>` shells still count.
//
// Keep in sync with REPEAT_LOOP_THRESHOLD and HTML_CLOSING_TAG_NAMES in
// internal/js/chat-stream/repeat_guard.js (Node/Vercel runtime).
const DefaultRepeatLoopThreshold = 10

// htmlClosingTagNames lists the common HTML/SVG element names whose closing
// tags can legitimately stack up in deeply nested markup. A closing tag whose
// name is in this set is skipped and breaks the run instead of being counted.
var htmlClosingTagNames = map[string]struct{}{
	"a": {}, "article": {}, "aside": {}, "b": {}, "blockquote": {}, "body": {},
	"button": {}, "code": {}, "dd": {}, "div": {}, "dl": {}, "dt": {}, "em": {},
	"footer": {}, "form": {}, "g": {}, "h1": {}, "h2": {}, "h3": {}, "h4": {},
	"h5": {}, "h6": {}, "head": {}, "header": {}, "html": {}, "i": {}, "label": {},
	"li": {}, "main": {}, "nav": {}, "ol": {}, "option": {}, "p": {}, "path": {},
	"pre": {}, "script": {}, "section": {}, "select": {}, "span": {}, "strong": {},
	"style": {}, "svg": {}, "table": {}, "tbody": {}, "td": {}, "tfoot": {},
	"th": {}, "thead": {}, "tr": {}, "ul": {},
}

// maxPendingTagLen bounds how much text is retained while waiting for a closing
// tag's `>` to arrive in a later SSE fragment. Beyond this the `</` was not a
// tag opener at all and the retained text is released.
const maxPendingTagLen = 512

// RepeatLoopGuard is a streaming detector for the repetition loop described on
// DefaultRepeatLoopThreshold. Text is fed in arbitrary fragments — SSE chunks
// routinely split a tag across frames — so the guard retains a short tail and
// resumes matching on the next call instead of scanning each fragment in
// isolation.
//
// Once tripped the guard stays tripped: the caller is expected to end the
// upstream read and keep the content produced so far.
type RepeatLoopGuard struct {
	// Threshold overrides DefaultRepeatLoopThreshold when > 0.
	Threshold int

	pending string
	run     int
	tripped bool
}

// Tripped reports whether a repetition loop was already detected.
func (g *RepeatLoopGuard) Tripped() bool {
	return g != nil && g.tripped
}

func (g *RepeatLoopGuard) threshold() int {
	if g.Threshold > 0 {
		return g.Threshold
	}
	return DefaultRepeatLoopThreshold
}

// Feed consumes the next raw text fragment and reports whether a repetition
// loop has been detected (either by this fragment or by an earlier one).
//
// Whitespace between two closing tags does not break the run: the degenerate
// output puts each repeated tag on its own line. Anything else — prose, an
// opening tag, a parameter body — resets the run to zero.
func (g *RepeatLoopGuard) Feed(text string) bool {
	if g == nil {
		return false
	}
	if g.tripped {
		return true
	}
	if text == "" {
		return false
	}
	buf := g.pending + text
	g.pending = ""
	limit := g.threshold()
	for i := 0; i < len(buf); {
		next, status := scanProtocolClosingTag(buf, i)
		switch status {
		case closingTagMatched:
			g.run++
			if g.run >= limit {
				g.tripped = true
				g.run = 0
				return true
			}
			i = next
			continue
		case closingTagNeedMore:
			// The tag may be split across SSE fragments: keep the tail and
			// resume matching once the rest arrives.
			g.pending = buf[i:]
			return false
		}
		if g.run > 0 && isRepeatLoopSeparator(buf[i]) {
			i++
			continue
		}
		g.run = 0
		if next > i {
			i = next
			continue
		}
		i++
	}
	return false
}

type closingTagStatus int

const (
	closingTagNone closingTagStatus = iota
	closingTagMatched
	closingTagNeedMore
)

// scanProtocolClosingTag reports whether a closing tag starts at buf[i]. On a
// match it returns the index just past the tag's `>`; otherwise it returns an
// index the caller may safely resume from (never inside a region already proven
// free of `<`).
//
// A closing tag is `</`, at least one non-whitespace body byte, then `>`. The
// body may be a normal XML element name (`</invoke>`, `</parameter>`,
// `</tool_calls>`) or a separator-drift EPSE shell (`</|EPSE>`,
// `</|EPSE|invoke>`, `</、EPSE、tool_calls>`). Matching every closing tag — not
// just the protocol-shaped ones — is required because the degenerate upstream
// loop also emits the normalized canonical shells `</invoke>` / `</parameter>`,
// which look exactly like ordinary XML closing tags. The only names skipped are
// the common HTML/SVG elements in htmlClosingTagNames, whose closing tags
// legitimately stack in deeply nested markup.
//
// Only the ASCII `>` terminates a tag here. A drifted terminator (`＞`, `〉`)
// simply leaves the run at zero, which loses detection but never produces a
// false positive.
func scanProtocolClosingTag(buf string, i int) (int, closingTagStatus) {
	if buf[i] != '<' {
		return i, closingTagNone
	}
	if i+1 >= len(buf) {
		return i, closingTagNeedMore
	}
	if buf[i+1] != '/' {
		return i, closingTagNone
	}
	if i+2 >= len(buf) {
		return i, closingTagNeedMore
	}
	if !isClosingTagBodyStart(buf[i+2]) {
		return i, closingTagNone
	}
	for j := i + 3; j < len(buf); j++ {
		if buf[j] == '>' {
			if isHTMLClosingTag(buf, i, j+1) {
				// Ordinary HTML closing tag: not part of a protocol-shell loop.
				// Return it as a non-match so the run resets and scanning
				// resumes past the tag.
				return j + 1, closingTagNone
			}
			return j + 1, closingTagMatched
		}
		if buf[j] == '<' {
			// A new tag starts before this one closed, so `</…` was not a
			// closing tag after all. Resuming at j is safe: no `<` was skipped.
			return j, closingTagNone
		}
		if j-i >= maxPendingTagLen {
			return j, closingTagNone
		}
	}
	if len(buf)-i >= maxPendingTagLen {
		return len(buf), closingTagNone
	}
	return i, closingTagNeedMore
}

// isHTMLClosingTag reports whether the closing tag spanning buf[start:end]
// (start at `<`, end just past `>`) names an ordinary HTML/SVG element from
// htmlClosingTagNames. The tag name runs from after `</` to the first whitespace
// or `>`, so `</div>`, `</div >` and `</DIV>` all match, while separator-drift
// shells (`</|EPSE|invoke>`) and protocol shells (`</invoke>`, `</parameter>`)
// do not.
func isHTMLClosingTag(buf string, start, end int) bool {
	nameStart := start + 2
	nameEnd := nameStart
	for nameEnd < end-1 && !isRepeatLoopSeparator(buf[nameEnd]) {
		nameEnd++
	}
	if nameEnd == nameStart {
		return false
	}
	_, ok := htmlClosingTagNames[strings.ToLower(buf[nameStart:nameEnd])]
	return ok
}

// isClosingTagBodyStart reports whether c may appear directly after `</` in a
// closing tag. Whitespace, `<` and `>` are excluded so an unterminated `</` or
// a stray `</ >` is not mistaken for a tag; every other byte is accepted, which
// keeps both XML element names (`</invoke>`) and separator-drift EPSE shells
// (`</|EPSE>`) eligible.
func isClosingTagBodyStart(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '<', '>':
		return false
	}
	return true
}

// isRepeatLoopSeparator reports whether c may appear between two repeated
// closing tags without breaking the run.
func isRepeatLoopSeparator(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}
