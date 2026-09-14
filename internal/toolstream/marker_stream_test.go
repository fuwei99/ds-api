package toolstream

import (
	"strings"
	"testing"
)

const testMarker = "Q7ZK3M"

func markerToolCallText(marker string) string {
	return "<|" + marker + "|tool_calls>\n" +
		"  <|" + marker + "|invoke name=\"read_file\">\n" +
		"    <|" + marker + "|parameter name=\"path\">README.MD</|" + marker + "|parameter>\n" +
		"  </|" + marker + "|invoke>\n" +
		"</|" + marker + "|tool_calls>"
}

// TestProcessToolSieveInterceptsMarkerToolCallWithoutLeak is the marker-aware
// counterpart of TestProcessToolSieveInterceptsEPSEToolCallWithoutLeak: the
// model answers with the caller-specific marker, so the sieve must normalize it
// back to EPSE and intercept the call. Splitting the input one character at a
// time is the worst case for the marker normalizer (every SSE fragment can cut
// the marker anywhere).
func TestProcessToolSieveInterceptsMarkerToolCallWithoutLeak(t *testing.T) {
	var state State
	state.Marker = testMarker
	var events []Event
	for _, ch := range strings.Split(markerToolCallText(testMarker), "") {
		events = append(events, ProcessChunk(&state, ch, []string{"read_file"})...)
	}
	events = append(events, Flush(&state, []string{"read_file"})...)

	var textContent string
	toolCalls := 0
	for _, evt := range events {
		textContent += evt.Content
		toolCalls += len(evt.ToolCalls)
	}
	if strings.Contains(textContent, testMarker) {
		t.Fatalf("marker leaked to visible text: %q", textContent)
	}
	if strings.Contains(strings.ToLower(textContent), "epse") {
		t.Fatalf("canonical keyword leaked to visible text: %q", textContent)
	}
	if strings.Contains(textContent, "read_file") {
		t.Fatalf("tool name leaked to visible text: %q", textContent)
	}
	if toolCalls != 1 {
		t.Fatalf("expected one tool call, got %d events=%#v", toolCalls, events)
	}
}

// TestProcessToolSieveMarkerDoesNotLeakPartialMarker keeps the streaming
// invariant tight: no proper prefix of the marker may be released as visible
// text while the stream is still open.
func TestProcessToolSieveMarkerDoesNotLeakPartialMarker(t *testing.T) {
	var state State
	state.Marker = testMarker
	var textContent string
	for _, ch := range strings.Split("<|"+testMarker+"|invoke>", "") {
		for _, evt := range ProcessChunk(&state, ch, nil) {
			textContent += evt.Content
			if strings.Contains(textContent, "Q7Z") {
				t.Fatalf("partial marker leaked to visible text: %q", textContent)
			}
		}
	}
	for _, evt := range Flush(&state, nil) {
		textContent += evt.Content
		if strings.Contains(textContent, "Q7Z") {
			t.Fatalf("partial marker leaked at flush: %q", textContent)
		}
	}
}

// TestProcessToolSieveNeedsMarkerNormalization pins why the normalizer exists:
// the sieve/parser only recognizes the canonical EPSE shell (plus the legacy
// canonical XML one), so a marker-form tool call would be released as visible
// text unless the marker is normalized back to EPSE first.
func TestProcessToolSieveNeedsMarkerNormalization(t *testing.T) {
	run := func(marker string) (int, string) {
		var state State
		state.Marker = marker
		var events []Event
		for _, ch := range strings.Split(markerToolCallText(testMarker), "") {
			events = append(events, ProcessChunk(&state, ch, []string{"read_file"})...)
		}
		events = append(events, Flush(&state, []string{"read_file"})...)
		toolCalls := 0
		textContent := ""
		for _, evt := range events {
			toolCalls += len(evt.ToolCalls)
			textContent += evt.Content
		}
		return toolCalls, textContent
	}

	calls, leaked := run("")
	if calls != 0 {
		t.Fatalf("precondition: the marker-form call must not parse without normalization, got %d", calls)
	}
	if !strings.Contains(leaked, testMarker) {
		t.Fatalf("precondition: expected the raw marker markup to leak as text, got %q", leaked)
	}

	calls, leaked = run(testMarker)
	if calls != 1 {
		t.Fatalf("expected the marker to be normalized into one tool call, got %d", calls)
	}
	if strings.Contains(leaked, testMarker) {
		t.Fatalf("marker leaked to visible text: %q", leaked)
	}
}
