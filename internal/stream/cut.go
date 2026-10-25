package stream

import (
	"ds2api/internal/config"
	"ds2api/internal/sse"
)

// stopReasonForCut maps a content-policy cut to the stop reason reported to the
// caller. Every cut is a clean stop: the content produced before the cut is
// kept and the caller runs its normal finish path (HTTP 200) rather than
// reporting an interruption.
func stopReasonForCut(cut sse.CutReason) StopReason {
	switch cut {
	case sse.CutRepeatLoop:
		return StopReasonRepeatLoop
	case sse.CutToolRoleMarker:
		return StopReasonToolRoleMarker
	}
	return StopReasonHandlerRequested
}

// StopForCut converts the cut reported by a content policy into the decision
// that ends the upstream read, logging it with the surface's own warning prefix
// and structured label. name is the log prefix (e.g. "openai_chat_stream") and
// surface the structured "surface" field (e.g. "chat.completions").
//
// Surfaces call this as the single tail of their parse hook instead of testing
// each cut rule themselves, so a new rule in sse.ContentPolicy needs no change
// in any surface.
func StopForCut(cut sse.CutReason, name, surface string, contentSeen bool) ParsedDecision {
	if cut == sse.CutNone {
		return ParsedDecision{ContentSeen: contentSeen}
	}
	config.Logger.Warn("["+name+"] "+cut.LogMessage(), "surface", surface, "stream", true)
	return ParsedDecision{Stop: true, StopReason: stopReasonForCut(cut), ContentSeen: contentSeen}
}
