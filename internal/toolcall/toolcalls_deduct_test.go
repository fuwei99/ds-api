package toolcall

import (
	"strings"
	"testing"
)

// TestBadToolMarkupSpansDSMLBlockKeepsProse covers the span-based deduction:
// only the DSML wrapper is a removable span, so prose next to the bad call is
// preserved instead of being deducted with the whole residual.
func TestBadToolMarkupSpansDSMLBlockKeepsProse(t *testing.T) {
	text := `before <DSML calls><DSML invoke name="read"><DSML parameter name="path">/tmp/a.txt</DSML parameter></DSML invoke></DSML calls> after`
	spans := BadToolMarkupSpans(text)
	if len(spans) == 0 {
		t.Fatalf("expected at least one DSML span, got none")
	}
	got := RemoveTextSpans(text, spans)
	if strings.Contains(got, "DSML") {
		t.Fatalf("expected DSML markup removed, got %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("expected prose preserved, got %q", got)
	}
}

// TestBadToolMarkupSpansKeepsParseableBlock ensures a block that parses into a
// real tool call is never deducted, so a successful call's markup survives.
func TestBadToolMarkupSpansKeepsParseableBlock(t *testing.T) {
	text := `<|EPSE|tool_calls><|EPSE|invoke name="Bash"><|EPSE|parameter name="command">pwd</|EPSE|parameter></|EPSE|invoke></|EPSE|tool_calls>`
	if spans := BadToolMarkupSpans(text); len(spans) != 0 {
		t.Fatalf("expected no spans for a parseable block, got %#v", spans)
	}
}

// TestBadToolMarkupSpansSkipsFencedCode ensures a documented DSML example inside
// a fenced block is not mistaken for a real bad call.
func TestBadToolMarkupSpansSkipsFencedCode(t *testing.T) {
	text := "example:\n```\n<DSML calls><DSML invoke name=\"read\"></DSML invoke></DSML calls>\n```\n"
	if spans := BadToolMarkupSpans(text); len(spans) != 0 {
		t.Fatalf("expected fenced DSML example to be skipped, got %#v", spans)
	}
}

// TestBadToolMarkupSpansUnclosedDSMLKeepsTrailingProse covers the truncated
// wrapper case: the unclosed tag run is deducted while trailing prose survives.
func TestBadToolMarkupSpansUnclosedDSMLKeepsTrailingProse(t *testing.T) {
	text := `before <DSML calls><DSML invoke name="read"> trailing prose`
	spans := BadToolMarkupSpans(text)
	if len(spans) == 0 {
		t.Fatalf("expected an unclosed DSML span, got none")
	}
	got := RemoveTextSpans(text, spans)
	if strings.Contains(got, "DSML") {
		t.Fatalf("expected unclosed DSML tags removed, got %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "trailing prose") {
		t.Fatalf("expected prose preserved, got %q", got)
	}
}
