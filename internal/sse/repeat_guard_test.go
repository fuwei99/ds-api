package sse

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

const (
	shorthandClose = "</|EPSE>"
	invokeClose    = "</|EPSE|invoke>"
	toolCallsClose = "</|EPSE|tool_calls>"
)

func TestRepeatLoopGuardTripsOnConsecutiveIdenticalTags(t *testing.T) {
	g := RepeatLoopGuard{}
	if g.Feed(strings.Repeat(shorthandClose, DefaultRepeatLoopThreshold-1)) {
		t.Fatalf("%d consecutive tags must not trip the guard", DefaultRepeatLoopThreshold-1)
	}
	if !g.Feed(shorthandClose) {
		t.Fatalf("tag #%d must trip the guard", DefaultRepeatLoopThreshold)
	}
	if !g.Tripped() {
		t.Fatalf("guard must stay tripped")
	}
}

// The observed loop alternates between two different closing shells, so the run
// must count closing tags regardless of their names.
func TestRepeatLoopGuardTripsOnAlternatingTags(t *testing.T) {
	g := RepeatLoopGuard{}
	body := strings.Repeat(invokeClose+"\n"+toolCallsClose+"\n", DefaultRepeatLoopThreshold)
	if !g.Feed(body) {
		t.Fatalf("alternating closing tags must trip the guard")
	}
}

func TestRepeatLoopGuardAllowsNewlineSeparatedTags(t *testing.T) {
	g := RepeatLoopGuard{}
	if !g.Feed(strings.Repeat(shorthandClose+"\n", DefaultRepeatLoopThreshold)) {
		t.Fatalf("newline-separated repeats must still trip the guard")
	}
}

func TestRepeatLoopGuardResetsOnInterveningText(t *testing.T) {
	g := RepeatLoopGuard{}
	half := DefaultRepeatLoopThreshold - 1
	body := strings.Repeat(shorthandClose, half) + "x" + strings.Repeat(shorthandClose, half)
	if g.Feed(body) {
		t.Fatalf("intervening text must reset the consecutive run")
	}
}

func TestRepeatLoopGuardIgnoresWellFormedToolCall(t *testing.T) {
	// A real tool call closes each parameter and then the invoke / tool_calls
	// shells, but every deeper level is broken up by an opening tag, so many
	// occurrences must not trip the guard.
	g := RepeatLoopGuard{}
	call := `<|EPSE|tool_calls>`
	for i := 0; i < 10; i++ {
		call += `<|EPSE|invoke name="Bash">`
		for j := 0; j < 5; j++ {
			call += `<|EPSE|parameter name="command"><![CDATA[pwd]]>` + shorthandClose
		}
		call += invokeClose
	}
	call += toolCallsClose
	if g.Feed(call) {
		t.Fatalf("well-formed tool call must not trip the guard")
	}
}

// The observed loop also emits the normalized canonical shells, which are plain
// XML closing tags (`</invoke>` / `</parameter>`). Any long run of non-HTML
// closing tags must trip the guard now.
func TestRepeatLoopGuardTripsOnAnyClosingTagRun(t *testing.T) {
	g := RepeatLoopGuard{}
	body := strings.Repeat("</invoke>\n</parameter>\n", DefaultRepeatLoopThreshold)
	if !g.Feed(body) {
		t.Fatalf("canonical </invoke>/</parameter> loop must trip the guard")
	}

	for _, tag := range []string{"</invoke>", "</parameter>", "</tool_calls>", "</think>", "</dsml>"} {
		if g := (&RepeatLoopGuard{}); !g.Feed(strings.Repeat(tag+"\n", DefaultRepeatLoopThreshold)) {
			t.Fatalf("a run of %d %s tags must trip the guard", DefaultRepeatLoopThreshold, tag)
		}
	}
}

// Common HTML/SVG closing tags legitimately stack in deeply nested markup and
// are excluded by name, so a long run of them must not trip the guard.
func TestRepeatLoopGuardIgnoresHTMLClosingTagRun(t *testing.T) {
	for _, tag := range []string{"</div>", "</span>", "</li>", "</td>", "</p>", "</section>", "</svg>", "</g>", "</path>"} {
		if g := (&RepeatLoopGuard{}); g.Feed(strings.Repeat(tag+"\n", DefaultRepeatLoopThreshold*3)) {
			t.Fatalf("a run of %s HTML tags must not trip the guard", tag)
		}
	}
	// Case and trailing whitespace do not defeat the exclusion.
	g := RepeatLoopGuard{}
	if g.Feed(strings.Repeat("</DIV >\n", DefaultRepeatLoopThreshold*3)) {
		t.Fatalf("uppercase HTML closing tags must not trip the guard")
	}
}

// A model that over-closes (duplicate `</parameter>` before closing invoke and
// tool_calls) or that explains the format by listing closing-shell variants one
// per line produces the longest legitimate runs observed. Both must stay below
// the threshold.
func TestRepeatLoopGuardIgnoresOverCloseAndFormatProse(t *testing.T) {
	overClose := `<|EPSE|parameter name="a"><![CDATA[v]]>` +
		`</|EPSE|parameter></|EPSE|parameter></|EPSE|parameter>` + invokeClose + toolCallsClose
	if g := (&RepeatLoopGuard{}); g.Feed(overClose) {
		t.Fatalf("over-closed tool call must not trip the guard")
	}

	// Six variants is the documented longest legitimate run; keep it below the
	// threshold so format-explaining prose is not cut.
	variants := strings.Join([]string{
		shorthandClose, `</|EPSE|parameter>`, invokeClose, toolCallsClose,
		`</epse|parameter>`, `</|EPSE parameter>`,
	}, "\n")
	if g := (&RepeatLoopGuard{}); g.Feed("闭合外壳的各种写法：\n" + variants + "\n") {
		t.Fatalf("format-explaining prose must not trip the guard")
	}
}

func TestRepeatLoopGuardHandlesTagsSplitAcrossFragments(t *testing.T) {
	g := RepeatLoopGuard{}
	fragments := []string{"</|", "EPSE>", "</|EP", "SE|invoke>", "</|EPSE|tool", "_calls>"}
	tripped := false
	for i := 0; i < DefaultRepeatLoopThreshold; i++ {
		for _, f := range fragments {
			if g.Feed(f) {
				tripped = true
			}
		}
	}
	if !tripped {
		t.Fatalf("tags split across fragments must still be counted")
	}
}

// An unterminated `</|` must not be buffered forever: once the retained tail
// grows past the cap the text is released and scanning continues.
func TestRepeatLoopGuardReleasesUnterminatedTagTail(t *testing.T) {
	g := RepeatLoopGuard{}
	if g.Feed("</|" + strings.Repeat("a", maxPendingTagLen+64)) {
		t.Fatalf("unterminated tag must not trip the guard")
	}
	if got := len(g.pending); got > maxPendingTagLen {
		t.Fatalf("retained tail = %d bytes, want <= %d", got, maxPendingTagLen)
	}
}

func TestCollectStreamStopsOnRepeatLoopAndKeepsContent(t *testing.T) {
	lines := []string{
		`data: {"p":"response/content","v":"visible answer"}`,
		``,
	}
	for i := 0; i < DefaultRepeatLoopThreshold+2; i++ {
		lines = append(lines, `data: {"p":"response/content","v":"`+invokeClose+`"}`, ``)
	}
	// A late chunk that must never be consumed once the loop was detected.
	lines = append(lines, `data: {"p":"response/content","v":"tail"}`, ``)
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Join(lines, "\n")))}

	got := CollectStream(resp, false, true)
	if !got.RepeatLoop {
		t.Fatalf("expected repeat loop to be reported")
	}
	if !strings.HasPrefix(got.Text, "visible answer") {
		t.Fatalf("content before the loop must be kept, got %q", got.Text)
	}
	if strings.Contains(got.Text, "tail") {
		t.Fatalf("collection must stop at the loop, got %q", got.Text)
	}
}
