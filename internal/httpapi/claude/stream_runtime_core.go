package claude

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ds2api/internal/responsehistory"
	"ds2api/internal/sse"
	streamengine "ds2api/internal/stream"
	"ds2api/internal/toolcall"
	"ds2api/internal/toolstream"
)

type claudeStreamRuntime struct {
	w        http.ResponseWriter
	rc       *http.ResponseController
	canFlush bool

	model           string
	toolNames       []string
	messages        []any
	toolsRaw        any
	promptTokenText string

	thinkingEnabled       bool
	searchEnabled         bool
	bufferToolContent     bool
	stripReferenceMarkers bool

	messageID         string
	thinking          strings.Builder
	text              strings.Builder
	responseMessageID int

	sieve                 toolstream.State
	rawText               strings.Builder
	rawThinking           strings.Builder
	toolDetectionThinking strings.Builder
	toolCallsDetected     bool

	// policy is the set of cut rules applied to the raw text channel — the
	// upstream closing-tag repetition loop and the hallucinated `[Tool]:` role
	// marker — so the stream can be cut and finished normally instead of waiting
	// out the idle watchdog, and the fabricated remainder dropped. The rules
	// live in sse so every runtime shares one definition.
	policy sse.ContentPolicy

	nextBlockIndex     int
	thinkingBlockOpen  bool
	thinkingBlockIndex int
	textBlockOpen      bool
	textBlockIndex     int
	textEmitted        bool
	ended              bool
	upstreamErr        string
	finalErrorStatus   int
	finalErrorMessage  string
	finalErrorCode     string
	history            *responsehistory.Session

	// toolCallRepair / toolCallRepairCtx wire the phase-3 finalize-only LLM
	// tool-call repair into the streaming finalize path (M4). Nil unless the
	// handler enabled repair; the per-chunk sieve never receives them.
	toolCallRepair    toolcall.ToolCallRepairInvoker
	toolCallRepairCtx context.Context
}

// resetContentPolicy starts a new upstream round. The cut rules belong to one
// upstream stream, so a rule that fired on the attempt that just ended must not
// keep discarding the next attempt's text: an empty-output retry (or a retry on
// another account) reads a fresh upstream response, and its content is what the
// client is waiting for. Everything else the runtime holds is left alone.
func (s *claudeStreamRuntime) resetContentPolicy() {
	if s == nil {
		return
	}
	s.policy = sse.ContentPolicy{}
}

func newClaudeStreamRuntime(
	w http.ResponseWriter,
	rc *http.ResponseController,
	canFlush bool,
	model string,
	messages []any,
	thinkingEnabled bool,
	searchEnabled bool,
	stripReferenceMarkers bool,
	toolNames []string,
	toolsRaw any,
	promptTokenText string,
	history *responsehistory.Session,
) *claudeStreamRuntime {
	return &claudeStreamRuntime{
		w:                     w,
		rc:                    rc,
		canFlush:              canFlush,
		model:                 model,
		messages:              messages,
		thinkingEnabled:       thinkingEnabled,
		searchEnabled:         searchEnabled,
		bufferToolContent:     len(toolNames) > 0,
		stripReferenceMarkers: stripReferenceMarkers,
		toolNames:             toolNames,
		toolsRaw:              toolsRaw,
		promptTokenText:       promptTokenText,
		history:               history,
		messageID:             fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		thinkingBlockIndex:    -1,
		textBlockIndex:        -1,
	}
}

func (s *claudeStreamRuntime) onParsed(parsed sse.LineResult) streamengine.ParsedDecision {
	if !parsed.Parsed {
		return streamengine.ParsedDecision{}
	}
	if parsed.ErrorMessage != "" {
		s.upstreamErr = parsed.ErrorMessage
		return streamengine.ParsedDecision{Stop: true, StopReason: streamengine.StopReason("upstream_error")}
	}
	if parsed.ResponseMessageID > 0 {
		s.responseMessageID = parsed.ResponseMessageID
	}
	if parsed.Stop {
		return streamengine.ParsedDecision{Stop: true}
	}

	contentSeen := false
	for _, p := range parsed.ToolDetectionThinkingParts {
		trimmed := sse.TrimContinuationOverlapFromBuilder(&s.toolDetectionThinking, p.Text)
		if trimmed != "" {
			s.toolDetectionThinking.WriteString(trimmed)
		}
	}
	for _, p := range parsed.Parts {
		var rawTrimmed string
		if p.Type == "thinking" {
			rawTrimmed = sse.TrimContinuationOverlapFromBuilder(&s.rawThinking, p.Text)
		} else {
			rawTrimmed = sse.TrimContinuationOverlapFromBuilder(&s.rawText, p.Text)
		}
		if rawTrimmed == "" {
			continue
		}
		if p.Type == "thinking" {
			s.rawThinking.WriteString(rawTrimmed)
		} else {
			// 内容规则可能在此截断正文通道：编造的 `[Tool]:` 角色标记及其后
			// 全是虚构对话，复读死循环之后的输出也已退化。两种情况都只保留
			// 截断点之前的正文。
			rawTrimmed = s.policy.Feed(rawTrimmed)
			if rawTrimmed == "" {
				continue
			}
			s.rawText.WriteString(rawTrimmed)
		}
		cleanedText := cleanVisibleOutput(rawTrimmed, s.stripReferenceMarkers)
		if cleanedText == "" {
			continue
		}
		if p.Type != "thinking" && s.searchEnabled && sse.IsCitation(cleanedText) {
			continue
		}
		contentSeen = true

		if p.Type == "thinking" {
			if !s.thinkingEnabled {
				continue
			}
			trimmed := sse.TrimContinuationOverlapFromBuilder(&s.thinking, cleanedText)
			if trimmed == "" {
				continue
			}
			s.thinking.WriteString(trimmed)
			s.closeTextBlock()
			if !s.thinkingBlockOpen {
				s.thinkingBlockIndex = s.nextBlockIndex
				s.nextBlockIndex++
				s.send("content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": s.thinkingBlockIndex,
					"content_block": map[string]any{
						"type":     "thinking",
						"thinking": "",
					},
				})
				s.thinkingBlockOpen = true
			}
			s.send("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": s.thinkingBlockIndex,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": trimmed,
				},
			})
			continue
		}

		s.text.WriteString(cleanedText)

		if !s.bufferToolContent {
			s.closeThinkingBlock()
			if !s.textBlockOpen {
				s.textBlockIndex = s.nextBlockIndex
				s.nextBlockIndex++
				s.send("content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": s.textBlockIndex,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				})
				s.textBlockOpen = true
			}
			s.send("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": s.textBlockIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": cleanedText,
				},
			})
			s.textEmitted = true
			continue
		}

		events := toolstream.ProcessChunk(&s.sieve, rawTrimmed, s.toolNames)
		for _, evt := range events {
			if len(evt.ToolCalls) > 0 {
				s.closeTextBlock()
				s.toolCallsDetected = true
				normalized := toolcall.NormalizeParsedToolCallsForSchemas(evt.ToolCalls, s.toolsRaw)
				for _, tc := range normalized {
					idx := s.nextBlockIndex
					s.nextBlockIndex++
					s.sendToolUseBlock(idx, tc)
				}
				continue
			}
			if evt.Content == "" {
				continue
			}
			cleaned := cleanVisibleOutput(evt.Content, s.stripReferenceMarkers)
			if cleaned == "" || (s.searchEnabled && sse.IsCitation(cleaned)) {
				continue
			}
			s.closeThinkingBlock()
			if !s.textBlockOpen {
				s.textBlockIndex = s.nextBlockIndex
				s.nextBlockIndex++
				s.send("content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": s.textBlockIndex,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				})
				s.textBlockOpen = true
			}
			s.send("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": s.textBlockIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": cleaned,
				},
			})
			s.textEmitted = true
		}
	}

	if s.history != nil {
		s.history.Progress(
			responsehistory.ThinkingForArchive(s.rawThinking.String(), s.toolDetectionThinking.String(), s.thinking.String()),
			responsehistory.TextForArchive(s.rawText.String(), s.text.String()),
		)
	}
	// 上游内容规则（复读死循环 / 编造的 `[Tool]:` 角色标记）在此统一收口：
	// 结束读取，保留已生成内容走正常收尾。
	return streamengine.StopForCut(s.policy.Cut(), "claude_stream", "claude.messages", contentSeen)
}
