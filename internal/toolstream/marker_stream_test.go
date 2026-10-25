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

// selfOverlappingMarkers all end with a proper prefix of themselves. Such a
// marker is rejected by DeriveToolMarker, but the sieve must stay correct for it
// anyway: the marker is an opaque caller-scoped value and a deployment may pin
// one out of band.
var selfOverlappingMarkers = []string{"N3KZ5N", "ABABAB", "A2B2A2", "M29MM2", "ABCDEA"}

// TestProcessToolSieveInterceptsSelfOverlappingMarkerWithoutLeak is the
// regression guard for the chunk-boundary leak: a complete marker landing
// exactly on a chunk boundary used to be split by the normalizer, so the sieve
// saw neither a complete marker nor a partial tag and released the whole tool
// block as visible text. Sweeping the chunk size covers every alignment.
func TestProcessToolSieveInterceptsSelfOverlappingMarkerWithoutLeak(t *testing.T) {
	for _, marker := range selfOverlappingMarkers {
		text := markerToolCallText(marker)
		for _, chunk := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 11, len(text)} {
			var state State
			state.Marker = marker
			var events []Event
			for i := 0; i < len(text); i += chunk {
				end := i + chunk
				if end > len(text) {
					end = len(text)
				}
				events = append(events, ProcessChunk(&state, text[i:end], []string{"read_file"})...)
			}
			events = append(events, Flush(&state, []string{"read_file"})...)

			var textContent string
			toolCalls := 0
			for _, evt := range events {
				textContent += evt.Content
				toolCalls += len(evt.ToolCalls)
			}
			if toolCalls != 1 {
				t.Fatalf("marker %q chunk %d: expected one tool call, got %d (leaked=%q)", marker, chunk, toolCalls, textContent)
			}
			if strings.Contains(textContent, marker) {
				t.Fatalf("marker %q chunk %d leaked to visible text: %q", marker, chunk, textContent)
			}
			if strings.Contains(strings.ToLower(textContent), "epse") {
				t.Fatalf("marker %q chunk %d leaked the canonical keyword to visible text: %q", marker, chunk, textContent)
			}
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
