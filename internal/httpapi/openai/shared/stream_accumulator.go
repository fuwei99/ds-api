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

	// policy is the set of cut rules applied to the raw text channel — the
	// upstream repetition loop (a long run of protocol closing tags such as
	// `</|EPSE>` or `</|EPSE|invoke>`) and the hallucinated `[Tool]:` role
	// marker — so the surface can stop reading and keep whatever was generated
	// instead of waiting out the idle watchdog. The rules live in sse so every
	// runtime shares one definition.
	policy sse.ContentPolicy
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
	// Cut reports that a content rule ended the raw text channel. The
	// already-accumulated content stays valid and everything from the cut on
	// was dropped; the caller should stop consuming upstream and run its normal
	// finish path. CutNone means the stream is still healthy.
	Cut sse.CutReason
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
	out.Cut = a.policy.Cut()
	return out
}

// Cut reports which content rule ended the raw text channel, or sse.CutNone
// while the stream is still healthy.
func (a *StreamAccumulator) Cut() sse.CutReason {
	if a == nil {
		return sse.CutNone
	}
	return a.policy.Cut()
}

// ResetContentPolicy starts a new upstream round. The cut rules belong to one
// upstream stream, so a rule that fired on the attempt that just ended must not
// keep discarding the next attempt's text: an empty-output retry (or a retry on
// another account) reads a fresh upstream response, and its content is what the
// client is waiting for. The accumulated text and every other field are left
// alone — only the detection state is cleared.
//
// Every surface calls this at the start of an attempt, i.e. in its
// consume*StreamAttempt, because that is the boundary where a new upstream
// response begins.
func (a *StreamAccumulator) ResetContentPolicy() {
	if a == nil {
		return
	}
	a.policy = sse.ContentPolicy{}
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
	// A content rule may end the raw text channel here — a hallucinated
	// `[Tool]:` role marker and everything after it is fabricated dialogue, and
	// a repetition loop means the rest of the stream is degenerate. Either way
	// the text produced before the cut is kept and the rest is dropped.
	rawTrimmed = a.policy.Feed(rawTrimmed)
	if rawTrimmed == "" {
		return StreamPartDelta{Type: "text"}
	}
	a.RawText.WriteString(rawTrimmed)
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
