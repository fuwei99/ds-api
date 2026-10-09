package shared

import (
	"strings"
	"testing"

	"ds2api/internal/sse"
)

func TestStreamAccumulatorAppliesThinkingAndTextDedupe(t *testing.T) {
	acc := StreamAccumulator{ThinkingEnabled: true, StripReferenceMarkers: true}
	thinkingPrefix := "this is a long thinking snapshot prefix used by DeepSeek continue replay"
	textPrefix := "this is a long visible answer snapshot prefix used by DeepSeek continue replay"
	first := acc.Apply(sse.LineResult{
		Parsed: true,
		Parts: []sse.ContentPart{
			{Type: "thinking", Text: thinkingPrefix},
			{Type: "text", Text: textPrefix},
		},
	})
	second := acc.Apply(sse.LineResult{
		Parsed: true,
		Parts: []sse.ContentPart{
			{Type: "thinking", Text: thinkingPrefix + " next"},
			{Type: "text", Text: textPrefix + " world"},
		},
	})

	if !first.ContentSeen || !second.ContentSeen {
		t.Fatalf("expected both chunks to mark content seen")
	}
	if got := acc.RawThinking.String(); got != thinkingPrefix+" next" {
		t.Fatalf("raw thinking = %q", got)
	}
	if got := acc.Thinking.String(); got != thinkingPrefix+" next" {
		t.Fatalf("thinking = %q", got)
	}
	if got := acc.RawText.String(); got != textPrefix+" world" {
		t.Fatalf("raw text = %q", got)
	}
	if got := acc.Text.String(); got != textPrefix+" world" {
		t.Fatalf("text = %q", got)
	}
	if got := second.Parts[0].VisibleText; got != " next" {
		t.Fatalf("thinking delta = %q", got)
	}
	if got := second.Parts[1].VisibleText; got != " world" {
		t.Fatalf("text delta = %q", got)
	}
}

func TestStreamAccumulatorKeepsHiddenThinkingForToolDetection(t *testing.T) {
	acc := StreamAccumulator{ThinkingEnabled: false, StripReferenceMarkers: true}
	result := acc.Apply(sse.LineResult{
		Parsed: true,
		Parts: []sse.ContentPart{
			{Type: "thinking", Text: "<tool_calls></tool_calls>"},
		},
		ToolDetectionThinkingParts: []sse.ContentPart{
			{Type: "thinking", Text: "detect"},
			{Type: "thinking", Text: " tools"},
		},
	})

	if !result.ContentSeen {
		t.Fatalf("expected hidden thinking to count as upstream content")
	}
	if got := acc.RawThinking.String(); got != "<tool_calls></tool_calls>" {
		t.Fatalf("raw thinking = %q", got)
	}
	if got := acc.Thinking.String(); got != "" {
		t.Fatalf("visible thinking = %q", got)
	}
	if got := acc.ToolDetectionThinking.String(); got != "detect tools" {
		t.Fatalf("tool detection thinking = %q", got)
	}
}

func TestStreamAccumulatorCountsThinkingOnlyAsActivity(t *testing.T) {
	// With reasoning hidden (thinking not requested by the client) the parser
	// drops thinking from Parts and only reports it via
	// ToolDetectionThinkingParts. That reasoning phase is still upstream
	// activity: it must count as ContentSeen so the stream watchdog's
	// no-content timeout cannot kill a long thinking-only generation.
	acc := StreamAccumulator{ThinkingEnabled: false, StripReferenceMarkers: true}
	result := acc.Apply(sse.LineResult{
		Parsed: true,
		ToolDetectionThinkingParts: []sse.ContentPart{
			{Type: "thinking", Text: "planning the long answer"},
		},
	})

	if !result.ContentSeen {
		t.Fatalf("expected thinking-only stream to count as upstream activity")
	}
	if got := acc.ToolDetectionThinking.String(); got != "planning the long answer" {
		t.Fatalf("tool detection thinking = %q", got)
	}
}

func TestStreamAccumulatorReportsUpstreamRepeatLoop(t *testing.T) {
	acc := StreamAccumulator{StripReferenceMarkers: true}
	first := acc.Apply(sse.LineResult{
		Parsed: true,
		Parts:  []sse.ContentPart{{Type: "text", Text: "answer"}},
	})
	if first.Cut != sse.CutNone {
		t.Fatalf("normal text must not report a repeat loop")
	}
	// The observed loop alternates between two closing shells, so feed both.
	tags := []string{"</|EPSE|invoke>", "</|EPSE|tool_calls>"}
	var last StreamAccumulatorResult
	for i := 0; i < sse.DefaultRepeatLoopThreshold; i++ {
		last = acc.Apply(sse.LineResult{
			Parsed: true,
			Parts:  []sse.ContentPart{{Type: "text", Text: tags[i%len(tags)]}},
		})
	}
	if last.Cut != sse.CutRepeatLoop || acc.Cut() != sse.CutRepeatLoop {
		t.Fatalf("expected repeat loop to be reported after %d closing tags", sse.DefaultRepeatLoopThreshold)
	}
	if got := acc.RawText.String(); !strings.HasPrefix(got, "answer") {
		t.Fatalf("content before the loop must be kept, got %q", got)
	}
}

func TestStreamAccumulatorSuppressesCitationTextWhenSearchEnabled(t *testing.T) {
	acc := StreamAccumulator{SearchEnabled: true, StripReferenceMarkers: true}
	result := acc.Apply(sse.LineResult{
		Parsed: true,
		Parts:  []sse.ContentPart{{Type: "text", Text: "[citation:1]"}},
	})

	if !result.ContentSeen {
		t.Fatalf("expected citation chunk to mark upstream content")
	}
	if len(result.Parts) != 1 || !result.Parts[0].CitationOnly {
		t.Fatalf("expected citation-only delta, got %#v", result.Parts)
	}
	if got := acc.RawText.String(); got != "[citation:1]" {
		t.Fatalf("raw text = %q", got)
	}
	if got := acc.Text.String(); got != "" {
		t.Fatalf("visible text = %q", got)
	}
}

func TestStreamAccumulatorStripsInlineCitationAndReferenceMarkers(t *testing.T) {
	acc := StreamAccumulator{SearchEnabled: true, StripReferenceMarkers: true}
	result := acc.Apply(sse.LineResult{
		Parsed: true,
		Parts:  []sse.ContentPart{{Type: "text", Text: "广州天气[citation:1] 多云[reference:0]"}},
	})

	if !result.ContentSeen {
		t.Fatalf("expected marker chunk to mark upstream content")
	}
	if got := acc.Text.String(); got != "广州天气 多云" {
		t.Fatalf("visible text = %q", got)
	}
	if len(result.Parts) != 1 || result.Parts[0].VisibleText != "广州天气 多云" {
		t.Fatalf("unexpected parts: %#v", result.Parts)
	}
}

func TestStreamAccumulatorCutsHallucinatedToolRoleMarker(t *testing.T) {
	acc := StreamAccumulator{StripReferenceMarkers: true}
	first := acc.Apply(sse.LineResult{
		Parsed: true,
		Parts:  []sse.ContentPart{{Type: "text", Text: "命令已执行。\n"}},
	})
	if first.Cut != sse.CutNone {
		t.Fatalf("normal text must not report a role marker")
	}

	// The model invents a tool result and keeps answering its own dialogue: the
	// marker and everything after it must be dropped, and the surface must be
	// told to stop reading upstream.
	cut := acc.Apply(sse.LineResult{
		Parsed: true,
		Parts:  []sse.ContentPart{{Type: "text", Text: "[Tool]:\n输出：ok\n[Assistant]: 继续\n"}},
	})
	if cut.Cut != sse.CutToolRoleMarker || acc.Cut() != sse.CutToolRoleMarker {
		t.Fatalf("expected the role marker to be reported")
	}
	if got := acc.RawText.String(); got != "命令已执行。\n" {
		t.Fatalf("raw text must stop at the marker, got %q", got)
	}
	if got := acc.Text.String(); got != "命令已执行。\n" {
		t.Fatalf("visible text must stop at the marker, got %q", got)
	}

	after := acc.Apply(sse.LineResult{
		Parsed: true,
		Parts:  []sse.ContentPart{{Type: "text", Text: "更多虚构内容"}},
	})
	if got := acc.RawText.String(); got != "命令已执行。\n" {
		t.Fatalf("text after the marker must be dropped, got %q", got)
	}
	if got := after.Parts; len(got) != 0 {
		t.Fatalf("no delta may be produced after the marker, got %#v", got)
	}
}

func TestStreamAccumulatorCutsToolRoleMarkerSplitAcrossChunks(t *testing.T) {
	acc := StreamAccumulator{StripReferenceMarkers: true}
	var deltas []string
	last := StreamAccumulatorResult{}
	for _, fragment := range []string{"好的，", "[To", "ol", "]:", "编造的工具结果"} {
		last = acc.Apply(sse.LineResult{
			Parsed: true,
			Parts:  []sse.ContentPart{{Type: "text", Text: fragment}},
		})
		for _, p := range last.Parts {
			deltas = append(deltas, p.RawText)
		}
	}
	if last.Cut != sse.CutToolRoleMarker {
		t.Fatalf("expected the split marker to be reported")
	}
	if joined := strings.Join(deltas, ""); joined != "好的，" {
		t.Fatalf("split marker must not leak into deltas, got %q", joined)
	}
	if got := acc.Text.String(); got != "好的，" {
		t.Fatalf("visible text = %q", got)
	}
}
