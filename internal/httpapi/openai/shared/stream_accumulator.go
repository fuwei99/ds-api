package shared

import (
	"strings"

	"ds2api/internal/sse"
)

type StreamAccumulator struct {
	ThinkingEnabled       bool
	SearchEnabled         bool
	StripReferenceMarkers bool

	RawThinking           strings.Builder
	Thinking              strings.Builder
	ToolDetectionThinking strings.Builder
	RawText               strings.Builder
	Text                  strings.Builder

	// repeatGuard detects the upstream repetition loop (a long run of protocol
	// closing tags such as `</|EPSE>` or `</|EPSE|invoke>`) on the raw text
	// channel so the surface can stop reading and keep whatever was generated
	// instead of waiting out the idle watchdog.
	repeatGuard sse.RepeatLoopGuard
}

type StreamPartDelta struct {
	Type         string
	RawText      string
	VisibleText  string
	CitationOnly bool
}

type StreamAccumulatorResult struct {
	ContentSeen bool
	Parts       []StreamPartDelta
	// RepeatLoop reports that the raw upstream text degenerated into a
	// repetition loop. The already-accumulated content stays valid; the caller
	// should stop consuming upstream and run its normal finish path.
	RepeatLoop bool
}

func (a *StreamAccumulator) Apply(parsed sse.LineResult) StreamAccumulatorResult {
	out := StreamAccumulatorResult{}
	for _, p := range parsed.ToolDetectionThinkingParts {
		trimmed := sse.TrimContinuationOverlapFromBuilder(&a.ToolDetectionThinking, p.Text)
		if trimmed != "" {
			a.ToolDetectionThinking.WriteString(trimmed)
			// Tool-detection thinking is real upstream activity even when the
			// caller disabled visible reasoning; without this, a long silent
			// thinking phase would trip the no-content keepalive window and
			// truncate the stream.
			out.ContentSeen = true
		}
	}
	for _, p := range parsed.Parts {
		if p.Type == "thinking" {
			delta := a.applyThinkingPart(p.Text)
			if delta.RawText != "" {
				out.ContentSeen = true
			}
			if delta.RawText != "" || delta.VisibleText != "" {
				out.Parts = append(out.Parts, delta)
			}
			continue
		}
		delta := a.applyTextPart(p.Text)
		if delta.RawText != "" {
			out.ContentSeen = true
		}
		if delta.RawText != "" || delta.VisibleText != "" || delta.CitationOnly {
			out.Parts = append(out.Parts, delta)
		}
	}
	out.RepeatLoop = a.repeatGuard.Tripped()
	return out
}

// RepeatLoopDetected reports whether the accumulated raw text tripped the
// upstream repetition-loop guard.
func (a *StreamAccumulator) RepeatLoopDetected() bool {
	return a != nil && a.repeatGuard.Tripped()
}

// HasPartialOutput reports whether the accumulated stream contains any raw
// text, thinking or tool-detection content — i.e. the upstream produced
// something before the stream ended. Used to distinguish an interrupted stream
// worth resuming from a cleanly empty one.
func (a *StreamAccumulator) HasPartialOutput() bool {
	if a == nil {
		return false
	}
	return a.RawText.Len() > 0 || a.RawThinking.Len() > 0 || a.ToolDetectionThinking.Len() > 0
}

func (a *StreamAccumulator) applyThinkingPart(text string) StreamPartDelta {
	rawTrimmed := sse.TrimContinuationOverlapFromBuilder(&a.RawThinking, text)
	if rawTrimmed != "" {
		a.RawThinking.WriteString(rawTrimmed)
	}
	delta := StreamPartDelta{Type: "thinking", RawText: rawTrimmed}
	if !a.ThinkingEnabled || rawTrimmed == "" {
		return delta
	}
	cleanedText := CleanVisibleOutput(rawTrimmed, a.StripReferenceMarkers)
	if cleanedText == "" {
		return delta
	}
	trimmed := sse.TrimContinuationOverlapFromBuilder(&a.Thinking, cleanedText)
	if trimmed == "" {
		return delta
	}
	a.Thinking.WriteString(trimmed)
	delta.VisibleText = trimmed
	return delta
}

func (a *StreamAccumulator) applyTextPart(text string) StreamPartDelta {
	rawTrimmed := sse.TrimContinuationOverlapFromBuilder(&a.RawText, text)
	if rawTrimmed == "" {
		return StreamPartDelta{Type: "text"}
	}
	a.RawText.WriteString(rawTrimmed)
	a.repeatGuard.Feed(rawTrimmed)
	delta := StreamPartDelta{Type: "text", RawText: rawTrimmed}
	if a.SearchEnabled && sse.IsCitation(rawTrimmed) {
		delta.CitationOnly = true
		return delta
	}
	cleanedText := CleanVisibleOutput(rawTrimmed, a.StripReferenceMarkers)
	trimmed := sse.TrimContinuationOverlapFromBuilder(&a.Text, cleanedText)
	if trimmed == "" {
		return delta
	}
	a.Text.WriteString(trimmed)
	delta.VisibleText = trimmed
	return delta
}
