package sse

import (
	"net/http"
	"strings"

	dsprotocol "ds2api/internal/deepseek/protocol"
	"ds2api/internal/util"
)

// CollectResult holds the aggregated text and thinking content from a
// DeepSeek SSE stream, consumed to completion (non-streaming use case).
type CollectResult struct {
	Text                  string
	Thinking              string
	ToolDetectionThinking string
	ContentFilter         bool
	UpstreamError         string
	CitationLinks         map[int]string
	ResponseMessageID     int
	// Cut reports that collection was cut short by a content rule (see
	// ContentPolicy). The content collected before the cut is returned as-is;
	// the cut fragment and everything after it was dropped.
	Cut CutReason
}

// CollectStream fully consumes a DeepSeek SSE response and separates
// thinking content from text content. This replaces the duplicated
// stream-collection logic in openai.handleNonStream, claude.collectDeepSeek,
// and admin.testAccount.
//
// The caller is responsible for closing resp.Body unless closeBody is true.
func CollectStream(resp *http.Response, thinkingEnabled bool, closeBody bool) CollectResult {
	if closeBody {
		defer func() { _ = resp.Body.Close() }()
	}
	text := strings.Builder{}
	thinking := strings.Builder{}
	toolDetectionThinking := strings.Builder{}
	contentFilter := false
	upstreamError := ""
	stopped := false
	policy := ContentPolicy{}
	collector := newCitationLinkCollector()
	responseMessageID := 0
	currentType := "text"
	if thinkingEnabled {
		currentType = "thinking"
	}
	_ = dsprotocol.ScanSSELines(resp, func(line []byte) bool {
		chunk, done, parsed := ParseDeepSeekSSELine(line)
		if parsed && !done {
			collector.ingestChunk(chunk)
			observeResponseMessageID(chunk, &responseMessageID)
		}
		if done {
			return false
		}
		if stopped {
			return true
		}
		result := ParseDeepSeekContentLine(line, thinkingEnabled, currentType)
		currentType = result.NextType
		if !result.Parsed {
			return true
		}
		if result.Stop {
			if result.ContentFilter {
				contentFilter = true
			}
			if result.ErrorMessage != "" && upstreamError == "" {
				upstreamError = result.ErrorMessage
			}
			// Keep scanning to collect late-arriving citation metadata lines
			// that can appear after response/status=FINISHED, but stop as soon
			// as [DONE] arrives.
			stopped = true
			return true
		}
		for _, p := range result.Parts {
			if p.Type == "thinking" {
				trimmed := TrimContinuationOverlap(thinking.String(), p.Text)
				thinking.WriteString(trimmed)
			} else {
				trimmed := TrimContinuationOverlap(text.String(), p.Text)
				// 内容规则可能在此截断正文通道：编造的 `[Tool]:` 角色标记及其后
				// 全是虚构对话，复读死循环之后的输出也已退化。两种情况都只保留
				// 截断点之前的正文。
				trimmed = policy.Feed(trimmed)
				if trimmed == "" {
					continue
				}
				text.WriteString(trimmed)
			}
		}
		for _, p := range result.ToolDetectionThinkingParts {
			trimmed := TrimContinuationOverlap(toolDetectionThinking.String(), p.Text)
			toolDetectionThinking.WriteString(trimmed)
		}
		if policy.Cut() != CutNone {
			// A content rule ended the text channel: stop reading and keep what
			// was collected so the caller's normal finish path (tool sieve /
			// repair / usage) still runs.
			return false
		}
		return true
	})
	return CollectResult{
		Text:                  text.String(),
		Thinking:              thinking.String(),
		ToolDetectionThinking: toolDetectionThinking.String(),
		ContentFilter:         contentFilter,
		UpstreamError:         upstreamError,
		CitationLinks:         collector.build(),
		ResponseMessageID:     responseMessageID,
		Cut:                   policy.Cut(),
	}
}

// observeResponseMessageID extracts the response_message_id from a parsed SSE
// chunk. It mirrors the extraction logic in client_continue.go's observe
// method, checking top-level response_message_id, v.response.message_id, and
// message.response.message_id.
func observeResponseMessageID(chunk map[string]any, out *int) {
	if chunk == nil || out == nil {
		return
	}
	if id := util.IntFrom(chunk["response_message_id"]); id > 0 {
		*out = id
	}
	v, _ := chunk["v"].(map[string]any)
	if response, _ := v["response"].(map[string]any); response != nil {
		if id := util.IntFrom(response["message_id"]); id > 0 {
			*out = id
		}
	}
	if message, _ := chunk["message"].(map[string]any); message != nil {
		if response, _ := message["response"].(map[string]any); response != nil {
			if id := util.IntFrom(response["message_id"]); id > 0 {
				*out = id
			}
		}
	}
}
