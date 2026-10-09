package assistantturn

import (
	"strings"
	"testing"

	"ds2api/internal/sse"
	"ds2api/internal/toolcall"
)

const markerUnderTest = "Q7ZK3M"

func markerToolCallText(marker string) string {
	return "<|" + marker + "|tool_calls>\n" +
		"  <|" + marker + "|invoke name=\"read_file\">\n" +
		"    <|" + marker + "|parameter name=\"path\">README.MD</|" + marker + "|parameter>\n" +
		"  </|" + marker + "|invoke>\n" +
		"</|" + marker + "|tool_calls>"
}

// TestBuildTurnFromCollectedNormalizesMarkerBackToCanonical pins the archive
// invariant: whatever form the model answered in, the turn that gets archived and
// replayed into later prompts carries the canonical EPSE keyword. The prompt
// boundary is responsible for rendering it back to the caller-specific marker.
func TestBuildTurnFromCollectedNormalizesMarkerBackToCanonical(t *testing.T) {
	turn := BuildTurnFromCollected(sse.CollectResult{Text: markerToolCallText(markerUnderTest)}, BuildOptions{
		Model:      "deepseek-v4.1-flash",
		Prompt:     "prompt",
		ToolNames:  []string{"read_file"},
		ToolMarker: markerUnderTest,
	})

	if strings.Contains(turn.RawText, markerUnderTest) {
		t.Fatalf("archived raw text must be canonical: %q", turn.RawText)
	}
	if !strings.Contains(turn.RawText, "<|"+toolcall.EPSEKeyword+"|tool_calls>") {
		t.Fatalf("archived raw text missing the canonical shell: %q", turn.RawText)
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Name != "read_file" {
		t.Fatalf("expected the marker tool call to be parsed: %#v", turn.ToolCalls)
	}
	if got, _ := turn.ToolCalls[0].Input["path"].(string); got != "README.MD" {
		t.Fatalf("unexpected tool parameter: %#v", turn.ToolCalls[0].Input)
	}
}

// TestBuildTurnFromStreamSnapshotNormalizesMarkerBackToCanonical is the streaming
// counterpart: the finalize snapshot is normalized before parsing so the sieve
// residual and the repair layer only ever see EPSE.
func TestBuildTurnFromStreamSnapshotNormalizesMarkerBackToCanonical(t *testing.T) {
	raw := markerToolCallText(markerUnderTest)
	turn := BuildTurnFromStreamSnapshot(StreamSnapshot{
		RawText:     raw,
		VisibleText: raw,
	}, BuildOptions{
		Model:      "deepseek-v4.1-flash",
		Prompt:     "prompt",
		ToolNames:  []string{"read_file"},
		ToolMarker: markerUnderTest,
	})

	if strings.Contains(turn.RawText, markerUnderTest) {
		t.Fatalf("archived raw text must be canonical: %q", turn.RawText)
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].Name != "read_file" {
		t.Fatalf("expected the marker tool call to be parsed: %#v", turn.ToolCalls)
	}
}
